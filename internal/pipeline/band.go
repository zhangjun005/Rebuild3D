// @kron:intent geometry/scatter-cleanup/seam-compression

package pipeline

import (
	"math"
	"sort"
)

// 深度缝合压缩（seam compression）：清理沿插画线稿的「层间散落点」。
//
// ## 问题
//
// 把散落点（深度落在层间的点）反投影回图像坐标做成热图，会发现它们**恰好描在插画的
// 每根线稿上**——发丝、眼睛轮廓、衣褶、指甲，全图都是，而不是集中在人物外轮廓。
// 原因：线稿是深度模型眼里的"遮挡提示"，线稿两侧的深度被混合成中间值，
// 反投影后这些点落在两层之间的中间 z，侧视时形成沿每根线的细丝。
//
// ## 与「语义分割划边界带」的区别
//
// 之前按角色 mask 划边界带，只覆盖人物外轮廓，改了 1.3% 像素、侧视无可见变化。
// 这里改为**按深度不连续划带**，不带任何语义假设。
//
// ## 关键：不能把所有深度突变都当伪影
//
// 真实几何也会产生大突变（例如手臂指向镜头：手臂 0.3 / 身体 0.7）。
// 判据是**突变两侧的稳定深度是否同层**：
//
//	突变两侧稳定深度 IQR 小  → 线条缝合伪影（两侧本是同一层）→ 压缩
//	突变两侧稳定深度 IQR 大  → 真实台阶（两侧是不同层）      → 不动
//
// 这样真实前后关系（手臂、鼻子、伸出的手）完整保留。

// SeamStats 是缝合压缩的统计，回给 meta。
type SeamStats struct {
	// Enabled 表示是否真的执行了处理。
	Enabled bool
	// Fallback 非空表示没执行的原因。
	Fallback string
	// BandWidth / Compress / FlatThresh 是实际使用的参数。
	BandWidth  int
	Compress   float64
	FlatThresh float64
	// EdgeThresh 是判定"深度不连续"的阈值（自动取分位数）。
	EdgeThresh float64
	// BandPixels 是边界带像素数。
	BandPixels int
	// Adjusted 是被压缩的像素数（只包含"两侧同层"的那部分）。
	Adjusted int
	// KeptRealEdge 是判定为"真实台阶"而**保持不动**的像素数。
	KeptRealEdge int
}

// adjustDepthSeams 沿深度不连续处压缩缝合伪影。
//
// bandWidth <= 0 或 compress >= 1 时原样返回（Fallback 说明原因）。
// flatThresh = 0 表示按深度跨度自动取。
func adjustDepthSeams(
	depth []float32, w, h int,
	bandWidth int, compress, flatThresh float64,
) ([]float32, SeamStats) {
	st := SeamStats{BandWidth: bandWidth, Compress: compress}
	n := w * h
	if bandWidth <= 0 {
		st.Fallback = "band_width=0（未启用）"
		return depth, st
	}
	if compress >= 1 {
		st.Fallback = "band_compress>=1（恒等，未启用）"
		return depth, st
	}
	if compress < 0 {
		compress = 0
		st.Compress = 0
	}
	if n == 0 {
		st.Fallback = "空深度图"
		return depth, st
	}

	// ── 1. 局部深度梯度（到 4 邻域的最大绝对差）。 ──
	grad := make([]float32, n)
	valid := make([]bool, n)
	maxGrad := float32(0)
	gradSamples := make([]float32, 0, n/8+1)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			if !validDepth(depth[i]) {
				continue
			}
			valid[i] = true
			d := depth[i]
			var g float32
			if x > 0 && validDepth(depth[i-1]) {
				g = maxF32(g, absF32(d-depth[i-1]))
			}
			if x+1 < w && validDepth(depth[i+1]) {
				g = maxF32(g, absF32(d-depth[i+1]))
			}
			if y > 0 && validDepth(depth[i-w]) {
				g = maxF32(g, absF32(d-depth[i-w]))
			}
			if y+1 < h && validDepth(depth[i+w]) {
				g = maxF32(g, absF32(d-depth[i+w]))
			}
			grad[i] = g
			if g > maxGrad {
				maxGrad = g
			}
			if g > 0 && i%8 == 0 {
				gradSamples = append(gradSamples, g)
			}
		}
	}
	if maxGrad <= 0 {
		st.Fallback = "深度完全平坦，没有不连续处"
		return depth, st
	}

	// ── 2. 自动阈值：梯度的 p85（只取有梯度的样本）。 ──
	if flatThresh == 0 {
		flatThresh = 0.06
		if len(gradSamples) > 0 {
			sort.Slice(gradSamples, func(a, b int) bool { return gradSamples[a] < gradSamples[b] })
			// 用深度跨度推一个"同层"阈值：IQR 小于跨度的 6% 视为同层。
			lo := gradSamples[0]
			hi := gradSamples[len(gradSamples)-1]
			span := float64(hi - lo)
			if span > 0 {
				flatThresh = 0.06 * span
			}
			if flatThresh < 1e-4 {
				flatThresh = 1e-4
			}
		}
	}
	st.FlatThresh = flatThresh

	edgeThresh := autoSeamEdgeThresh(gradSamples)
	st.EdgeThresh = float64(edgeThresh)
	if edgeThresh <= 0 {
		st.Fallback = "梯度阈值退化为 0"
		return depth, st
	}

	// ── 3. 不连续像素 + 膨胀成带。 ──
	seam := make([]bool, n)
	buf := make([]int32, 0, n/16)
	for i := 0; i < n; i++ {
		if valid[i] && grad[i] >= edgeThresh {
			seam[i] = true
			buf = append(buf, int32(i))
		}
	}
	if len(buf) == 0 {
		st.Fallback = "没有不连续像素"
		return depth, st
	}
	band := make([]bool, n)
	for _, qi := range buf {
		band[qi] = true
	}
	for step := 0; step < bandWidth && len(buf) > 0; step++ {
		next := buf[:0:0]
		push := func(j int) {
			if !band[j] && valid[j] {
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

	// ── 4. 稳定像素 = 有效且不在不变带里（梯度低于阈值）。 ──
	stable := make([]bool, n)
	for i := 0; i < n; i++ {
		stable[i] = valid[i] && !band[i]
	}

	// ── 5. 逐带内像素：用窗口里的稳定深度算 IQR，判断是同层伪影还是真实台阶。 ──
	out := make([]float32, n)
	copy(out, depth)
	radius := bandWidth + 3
	win := make([]float32, 0, (2*radius+1)*(2*radius+1))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			if !band[i] || !valid[i] {
				continue
			}
			win = win[:0]
			for dy := -radius; dy <= radius; dy++ {
				yy := y + dy
				if yy < 0 || yy >= h {
					continue
				}
				row := yy * w
				for dx := -radius; dx <= radius; dx++ {
					xx := x + dx
					if xx < 0 || xx >= w {
						continue
					}
					if j := row + xx; stable[j] {
						win = append(win, depth[j])
					}
				}
			}
			if len(win) < 4 {
				continue
			}
			sort.Slice(win, func(a, b int) bool { return win[a] < win[b] })
			q1 := float64(win[len(win)/4])
			q3 := float64(win[len(win)*3/4])
			iqr := q3 - q1
			if iqr > flatThresh {
				// 两侧深度明显不同 → 真实台阶（如手臂朝前）：保持原值。
				st.KeptRealEdge++
				continue
			}
			// 两侧同层 → 线稿缝合伪影：往最近稳定深度压缩，保持相对位置。
			st1 := float64(win[len(win)/2])
			d := float64(depth[i])
			out[i] = float32(st1 + compress*(d-st1))
			if math.Abs(float64(out[i]-depth[i])) > 1e-6 {
				st.Adjusted++
			}
		}
	}

	st.Enabled = true
	return out, st
}

// autoSeamEdgeThresh 从梯度样本里取 p85 作为"深度不连续"的阈值。
//
// 比 FilterDepthEdges 的 p99 低：后者只砍最极端的 flying pixels，
// 而线稿缝合处的梯度是"中等偏大"，需要更低的分位才能覆盖。
func autoSeamEdgeThresh(samples []float32) float32 {
	if len(samples) == 0 {
		return 0
	}
	cp := make([]float32, len(samples))
	copy(cp, samples)
	sort.Slice(cp, func(a, b int) bool { return cp[a] < cp[b] })
	idx := len(cp) * 85 / 100
	if idx >= len(cp) {
		idx = len(cp) - 1
	}
	return cp[idx]
}

func maxF32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}
