package pipeline

import "math"

// 边界带深度压缩（mask 引导）。
//
// 目标：**只让人物轮廓附近的点云变干净**，角色内部与背景内部一个值都不动。
//
// 为什么需要：DA3 在插画上的深度是连续分布，人物与背景之间有一条"过渡尾巴"
// （实测 100 万点里 16.9% 落在层间，且集中在人物周围）。这些点反投影后落在两层之间
// 的中间 z，形成"从人物拖向背景的丝状散落点云"。
//
// 早先的做法是"按连通域把整块压平"，那会让角色变成平面——观感对了但信息没了。
// 本方法的区别：
//   - 用分割 mask 找到**真正的语义边界**（不是深度阈值）；
//   - 只动边界带内的深度；
//   - 压缩用「保持相对位置」的仿射：d' = d_char + compress·(d - d_char)，
//     compress=1 是恒等（不动），compress→0 才趋近平面；
//   - d_char 取**局部**最近角色点的深度（角色本身跨深度，用全局中位数会拉错方向）。
//
// 边界带最终归属角色：压缩把它们拉向角色侧；本来就是角色的点因为 d≈d_char 几乎不动。

// 分割 mask 的三档取值（与 sidecar/seg_engine.py 一致）。
const (
	maskBackground uint8 = 0
	maskUncertain  uint8 = 128
	maskCharacter  uint8 = 255
)

// BandStats 是边界带处理的统计，回给 meta。
type BandStats struct {
	// Enabled 表示是否真的执行了处理（false 时 Fallback 说明原因）。
	Enabled bool
	// Fallback 非空表示退化原因（分割不可靠 / 未启用）。
	Fallback string
	// BandWidth / Compress 是实际使用的参数。
	BandWidth int
	Compress  float64
	// BandPixels 是边界带内的像素数。
	BandPixels int
	// Adjusted 是被改了深度的像素数。
	Adjusted int
	// CharRatio 是角色像素占比；BorderFrac 是画面四边被判为角色的比例（失败检测用）。
	CharRatio  float64
	BorderFrac float64
}

// adjustBoundaryBand 用分割 mask 对边界带做深度压缩。
//
// mask 尺寸 (mw,mh) 可以与深度图 (w,h) 不同（最近邻缩放对齐）。
// bandWidth <= 0 或 compress >= 1 或 mask 为空时原样返回（Fallback 说明原因）。
func adjustBoundaryBand(
	depth []float32, w, h int,
	mask []uint8, mw, mh int,
	bandWidth int, compress float64,
) ([]float32, BandStats) {
	st := BandStats{BandWidth: bandWidth, Compress: compress}
	n := w * h
	if bandWidth <= 0 {
		st.Fallback = "band_width=0（未启用）"
		return depth, st
	}
	if compress >= 1 {
		st.Fallback = "band_compress>=1（恒等，未启用）"
		return depth, st
	}
	if len(mask) == 0 || mw <= 0 || mh <= 0 || n == 0 {
		st.Fallback = "没有分割 mask"
		return depth, st
	}
	if compress < 0 {
		compress = 0
		st.Compress = 0
	}

	// ── 1. mask 最近邻缩放到深度图尺寸。 ──
	cls := make([]uint8, n)
	for y := 0; y < h; y++ {
		sy := y * mh / h
		row := sy * mw
		for x := 0; x < w; x++ {
			cls[y*w+x] = mask[row+x*mw/w]
		}
	}

	// ── 2. 失败检测：角色太少、或角色糊到画面边缘（实测的典型失败模式）。 ──
	charCount := 0
	for _, c := range cls {
		if c == maskCharacter {
			charCount++
		}
	}
	st.CharRatio = float64(charCount) / float64(n)

	b := maxInt(2, minInt(w, h)/50)
	borderTotal, borderChar := 0, 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if y >= b && y < h-b && x >= b && x < w-b {
				continue
			}
			borderTotal++
			if cls[y*w+x] == maskCharacter {
				borderChar++
			}
		}
	}
	if borderTotal > 0 {
		st.BorderFrac = float64(borderChar) / float64(borderTotal)
	}
	if st.CharRatio < 0.02 {
		st.Fallback = "分割失败：角色占比 < 2%"
		return depth, st
	}
	if st.BorderFrac > 0.5 {
		st.Fallback = "分割失败：角色糊到画面边缘（>50%）"
		return depth, st
	}

	// ── 3. 轮廓像素：类别与任一四邻不同（含"不确定"像素本身）。 ──
	isEdge := make([]bool, n)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			c := cls[i]
			if c == maskUncertain {
				isEdge[i] = true
				continue
			}
			switch {
			case x > 0 && cls[i-1] != c,
				x+1 < w && cls[i+1] != c,
				y > 0 && cls[i-w] != c,
				y+1 < h && cls[i+w] != c:
				isEdge[i] = true
			}
		}
	}

	// ── 4. 膨胀成带（按层 BFS，曼哈顿半径 bandWidth）。 ──
	band := make([]bool, n)
	buf := make([]int32, 0, n/16)
	for i, e := range isEdge {
		if e {
			band[i] = true
			buf = append(buf, int32(i))
		}
	}
	for step := 0; step < bandWidth && len(buf) > 0; step++ {
		next := buf[:0:0]
		push := func(j int) {
			if !band[j] {
				band[j] = true
				next = append(next, int32(j))
			}
		}
		for _, qi := range buf {
			i := int(qi)
			x, y := i%w, i/w
			if x > 0 {
				push(i - 1)
			}
			if x+1 < w {
				push(i + 1)
			}
			if y > 0 {
				push(i - w)
			}
			if y+1 < h {
				push(i + w)
			}
		}
		buf = next
	}
	for _, on := range band {
		if on {
			st.BandPixels++
		}
	}
	if st.BandPixels == 0 {
		st.Fallback = "边界带为空"
		return depth, st
	}

	// ── 5. 每个带内像素的 d_char = 最近**角色内部**像素的深度。
	//
	// 关键：种子只能取角色内部（class=角色 且 不在边界带里），**不能**包含边界带上的
	// 角色像素——否则边界像素"最近的角色点就是它自己"，它那个被混合过的深度永远改不掉，
	// 而散落点云恰恰主要来自这些像素（实测：只改背景侧像素时仅有 1% 被调整，
	// 散落点却有 16.9%）。
	haveChar := make([]bool, n)
	charDepth := make([]float32, n)
	buf = buf[:0]
	for i, c := range cls {
		if c == maskCharacter && !band[i] {
			haveChar[i] = true
			charDepth[i] = depth[i]
			buf = append(buf, int32(i))
		}
	}
	if len(buf) == 0 {
		st.Fallback = "角色内部为空（边界带覆盖了整个角色），无法取局部角色深度"
		return depth, st
	}
	// 传播半径要够大：边界带宽度 + 几像素。
	for step := 0; step < bandWidth+3 && len(buf) > 0; step++ {
		next := buf[:0:0]
		for _, qi := range buf {
			i := int(qi)
			x, y := i%w, i/w
			try := func(j int) {
				if !haveChar[j] {
					haveChar[j] = true
					charDepth[j] = charDepth[i]
					next = append(next, int32(j))
				}
			}
			if x > 0 {
				try(i - 1)
			}
			if x+1 < w {
				try(i + 1)
			}
			if y > 0 {
				try(i - w)
			}
			if y+1 < h {
				try(i + w)
			}
		}
		buf = next
	}

	// ── 6. 只改边界带：d' = d_char + compress·(d - d_char)，保持相对位置。 ──
	out := make([]float32, n)
	copy(out, depth)
	for i := 0; i < n; i++ {
		if !band[i] || !haveChar[i] || !validDepth(depth[i]) {
			continue
		}
		dc := float64(charDepth[i])
		d := float64(depth[i])
		out[i] = float32(dc + compress*(d-dc))
		if math.Abs(float64(out[i]-depth[i])) > 1e-6 {
			st.Adjusted++
		}
	}

	st.Enabled = true
	return out, st
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
