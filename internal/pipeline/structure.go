package pipeline

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"sort"

	"illust3d/internal/cloud"
	"illust3d/internal/geometry"
)

// 结构感知采样（structure 模式）的内部常量。
const (
	// structureTileDivisor：工作网格按"长边/32"分块，每块作为一层做分层抽样。
	structureTileDivisor = 32
	// structureTileMin / structureTileMax 夹住分块边长（像素）。
	structureTileMin = 8
	structureTileMax = 64
	// graySamplesPerAxis：把原图缩到工作分辨率时每轴最多采几个点。
	// 用 box 采样而不是最近邻：线稿只有 2~3px 宽，最近邻会整条漏掉。
	// 实际采样数按"每个工作像素覆盖的源像素数"自适应（见 grayGrid）。
	graySamplesPerAxis = 3
	// sobelHistBins：按 p99 归一化时直方图的精度。
	sobelHistBins = 4096
	// inkPercentileLow / High：相对暗度 Ink 的拉伸分位（p5 / p95）。
	inkPercentileLow  = 0.05
	inkPercentileHigh = 0.95
)

// buildStructure 是结构感知采样路径：
//
//	深度上采样 → 原图结构图（Sobel 梯度 Gn + 相对暗度 Ink）
//	→ 权重 w = 1 + kEdge*Gn + kInk*Ink → 确定性分层抽样 max_points 个点
//
// 工作分辨率由原图决定（work_res，0=自动=原图长边/2），与 max_points 解耦；
// 默认不做体素下采样（会把刚加密到线稿上的点合并掉），显式 voxel_size>0 才做。
func buildStructure(depth []float32, w, h int, img image.Image, p Params, focal float64, discard []bool, thresh float32) (*Result, error) {
	bounds := img.Bounds()
	iw, ih := bounds.Dx(), bounds.Dy()
	gw, gh := workResFor(p, iw, ih, w, h)

	// 1) 候选掩码：工作像素的 2×2 深度邻域里至少有一个有效（且未被边缘剔除）的深度。
	candOK := candidateDepthOK(depth, w, h, gw, gh, discard)

	// 2) 原图结构权重：Sobel 梯度 Gn ∈ [0,1] + 相对暗度 Ink ∈ [0,1]。
	gn, ink := structureWeights(img, gw, gh)

	// 3) 确定性分层抽样。
	sel, candidateCount := selectStructured(gn, ink, candOK, gw, gh, p.MaxPoints, p.EdgeWeight, p.InkWeight)
	if len(sel) == 0 {
		return nil, ErrNoPoints
	}

	// 4) 采样点：深度 = 双线性上采样值，颜色 = 原图对应位置的原始像素。
	boundsPx := makePixelAt(img)
	worldPerPixelX := float64(w) / focal
	worldPerPixelY := float64(h) / focal
	dark := imageDarkStats(img)

	pts := make([]geometry.Vec3, 0, len(sel))
	cols := make([]geometry.Color, 0, len(sel))
	edgeCount := 0
	darkCount := 0
	for _, si := range sel {
		i := int(si)
		wx := i % gw
		wy := i / gw
		d, ok := sampleDepthBilinear(depth, w, h, gw, gh, discard, wx, wy)
		if !ok {
			continue // 候选掩码保证不会发生，防御性跳过
		}
		// 与 UnprojectPerspective 等价（把焦距按工作分辨率缩放后代入，z = -d）：
		//   x = ((wx+0.5)/gw - 0.5) * d * w/focal
		//   y = (0.5 - (wy+0.5)/gh) * d * h/focal
		//   z = -d        （d 是距离、越大越远；相机在 +Z 朝 -Z 看）
		x := ((float64(wx)+0.5)/float64(gw) - 0.5) * float64(d) * worldPerPixelX
		y := (0.5 - (float64(wy)+0.5)/float64(gh)) * float64(d) * worldPerPixelY
		pts = append(pts, geometry.Vec3{X: float32(x), Y: float32(y), Z: float32(-float64(d))})
		cols = append(cols, sampleColor(img, bounds, wx, wy, gw, gh))
		if gn[i] > EdgeRatioThreshold {
			edgeCount++
		}
		// 暗像素富集：选中像素 → 原图像素（与 sampleColor 同映射）→ 亮度是否 <= p20。
		imgX := int((float64(wx) + 0.5) * float64(iw) / float64(gw))
		imgY := int((float64(wy) + 0.5) * float64(ih) / float64(gh))
		if imgX < 0 {
			imgX = 0
		}
		if imgX >= iw {
			imgX = iw - 1
		}
		if imgY < 0 {
			imgY = 0
		}
		if imgY >= ih {
			imgY = ih - 1
		}
		if lumaBin(boundsPx(imgX, imgY)) < dark.thresholdBin {
			darkCount++
		}
	}
	if len(pts) == 0 {
		return nil, ErrNoPoints
	}
	n := float64(len(pts))
	ratio := float64(edgeCount) / n
	darkPointRatio := float64(darkCount) / n
	darkPixelRatio := dark.darkPixelRatio
	enrichment := darkEnrichmentOf(darkPointRatio, darkPixelRatio)

	// 5) 结构模式默认不下采样；只有显式 voxel_size>0 才做（用户明确要求时）。
	voxel := float32(0)
	if p.VoxelSize > 0 {
		pts, cols = geometry.VoxelDownsample(pts, cols, p.VoxelSize)
		if len(pts) > p.MaxPoints {
			pts, cols, _ = autoDownsample(pts, cols, p.MaxPoints, medianDepth(pts, 8192)/focal)
		}
		if len(pts) == 0 {
			return nil, ErrNoPoints
		}
		voxel = p.VoxelSize
		// 下采样后点位变了，按最终点重新统计指标。
		fxWork := focal * float64(gw) / float64(w)
		fyWork := focal * float64(gh) / float64(h)
		ratio = edgeRatioByInverse(pts, gw, gh, fxWork, fyWork, gn)
		darkPointRatio = darkRatioByInverse(pts, gw, gh, fxWork, fyWork, img, dark)
		enrichment = darkEnrichmentOf(darkPointRatio, darkPixelRatio)
	}

	minB, maxB := bbox(pts)
	data, err := cloud.Encode(cloud.New(pts, cols))
	if err != nil {
		return nil, fmt.Errorf("pipeline: 编码 ILC1 失败: %w", err)
	}
	return &Result{
		ILC1:           data,
		PointCount:     len(pts),
		DepthWidth:     w,
		DepthHeight:    h,
		ImageWidth:     iw,
		ImageHeight:    ih,
		BBoxMin:        minB,
		BBoxMax:        maxB,
		DroppedPixels:  gw*gh - candidateCount,
		VoxelSize:      voxel,
		EdgeThresh:     thresh,
		Focal:          focal,
		SampleMode:     SampleModeStructure,
		WorkRes:        maxInt(gw, gh),
		EdgePointRatio: ratio,
		DarkPointRatio: darkPointRatio,
		DarkPixelRatio: darkPixelRatio,
		DarkEnrichment: enrichment,
	}, nil
}

// workResFor 决定结构采样的工作分辨率（长边像素）。
//
// 关键：**与 max_points 解耦**。workRes 决定"线稿最细能到多少"，应由原图细节决定；
// max_points 只决定保留多少个点。
//
//   - Params.WorkRes == 0：自动 = clamp(round(原图长边/2), 512, 2048)
//   - 显式给出：clamp 到 [256, 4096]
//
// 另一条边按深度图宽高比缩放（深度图与插画同比例，所以工作网格也同比例）。
func workResFor(p Params, imgW, imgH, depthW, depthH int) (gw, gh int) {
	long := p.WorkRes
	if long == 0 {
		long = int(math.Round(float64(maxInt(imgW, imgH)) / 2))
		if long < AutoWorkResMin {
			long = AutoWorkResMin
		}
		if long > AutoWorkResMax {
			long = AutoWorkResMax
		}
	} else {
		if long < MinWorkRes {
			long = MinWorkRes
		}
		if long > MaxWorkRes {
			long = MaxWorkRes
		}
	}
	if depthW >= depthH {
		gw = long
		gh = int(math.Round(float64(long) * float64(depthH) / float64(depthW)))
	} else {
		gh = long
		gw = int(math.Round(float64(long) * float64(depthW) / float64(depthH)))
	}
	if gw < 1 {
		gw = 1
	}
	if gh < 1 {
		gh = 1
	}
	return gw, gh
}

// depthNeighbor 是工作像素对应的一个深度图邻点（下标 + 双线性权重）。
type depthNeighbor struct {
	idx int
	wt  float64
}

// workPixelNeighbors 返回工作像素 (gx,gy) 对应的 2×2 深度邻点与双线性权重。
func workPixelNeighbors(w, h, gw, gh, gx, gy int) [4]depthNeighbor {
	sy := (float64(gy)+0.5)*float64(h)/float64(gh) - 0.5
	y0f := math.Floor(sy)
	y0 := int(y0f)
	fy := sy - y0f
	if y0 < 0 {
		y0, fy = 0, 0
	}
	if y0 >= h-1 {
		y0, fy = h-1, 0
	}
	y1 := y0 + 1
	if y1 >= h {
		y1 = h - 1
	}

	sx := (float64(gx)+0.5)*float64(w)/float64(gw) - 0.5
	x0f := math.Floor(sx)
	x0 := int(x0f)
	fx := sx - x0f
	if x0 < 0 {
		x0, fx = 0, 0
	}
	if x0 >= w-1 {
		x0, fx = w-1, 0
	}
	x1 := x0 + 1
	if x1 >= w {
		x1 = w - 1
	}

	return [4]depthNeighbor{
		{y0*w + x0, (1 - fx) * (1 - fy)},
		{y0*w + x1, fx * (1 - fy)},
		{y1*w + x0, (1 - fx) * fy},
		{y1*w + x1, fx * fy},
	}
}

// candidateDepthOK 标记哪些工作像素可采样：2×2 深度邻域里至少有一个有效深度。
// 只算"是否可采样"，不存深度值（2048² 时省下 ~17MB 内存与一遍写入）。
func candidateDepthOK(depth []float32, w, h, gw, gh int, discard []bool) []bool {
	ok := make([]bool, gw*gh)
	for gy := 0; gy < gh; gy++ {
		for gx := 0; gx < gw; gx++ {
			nbrs := workPixelNeighbors(w, h, gw, gh, gx, gy)
			for _, n := range nbrs {
				if validDepth(depth[n.idx]) {
					ok[gy*gw+gx] = true
					break
				}
			}
		}
	}
	return ok
}

// sampleDepthBilinear 对单个工作像素做深度双线性上采样。
//
// 关键细节：**不跨深度跳变插值**——若 4 个邻点中有被 FilterDepthEdges 判为边界/无效的，
// 就取权重最大的"干净"邻点深度。这样线稿处的点也能保留，但不会把遮挡边界拉成飞点丝。
func sampleDepthBilinear(depth []float32, w, h, gw, gh int, discard []bool, gx, gy int) (float32, bool) {
	var sum float64
	validCount := 0
	cleanCount := 0
	bestCleanW := -1.0
	var bestClean, anyVal float32
	for _, n := range workPixelNeighbors(w, h, gw, gh, gx, gy) {
		d := depth[n.idx]
		if !validDepth(d) {
			continue
		}
		validCount++
		anyVal = d
		sum += n.wt * float64(d)
		if !discard[n.idx] {
			cleanCount++
			if n.wt > bestCleanW {
				bestCleanW = n.wt
				bestClean = d
			}
		}
	}
	switch {
	case validCount == 0:
		return 0, false // 邻域全无效：该工作像素不可采样
	case cleanCount == 4:
		return float32(sum), true // 四个邻点都干净：正常双线性
	case bestCleanW >= 0:
		return bestClean, true // 边界附近：取最近的干净邻点，不跨界插值
	default:
		return anyVal, true // 四个邻点都被判为边界（极窄带）：用最近的有效值
	}
}

// structureWeights 计算工作分辨率下的两个采样权重通道：
//
//	gn  = Sobel 梯度幅值，按 p99 归一化到 [0,1]        —— 找"边缘"
//	ink = clamp((p95-lum)/(p95-p5), 0, 1)              —— 找"线体"（相对暗度）
//
// 之所以要 ink：线稿是深色细线，线的两侧梯度大、线体本身梯度≈0；只按梯度
// 加权会采到两条边而不是实线。用**相对暗度**而不是绝对亮度，能自适应整体偏亮/偏暗的图。
func structureWeights(img image.Image, gw, gh int) (gn []float32, ink []uint8) {
	gray := grayGrid(img, gw, gh)
	smoothed := blur3x3(gray, gw, gh)
	gn = sobelNormalized(smoothed, gw, gh)

	// 相对暗度：在平滑灰度上取 p5 / p95 拉伸（对噪声更稳；墨线在 2~3px 尺度上仍显著变暗）。
	var hist [256]int
	for _, v := range smoothed {
		hist[v]++
	}
	n := len(smoothed)
	ink = make([]uint8, n)
	p5 := uint8Percentile(&hist, n, inkPercentileLow)
	p95 := uint8Percentile(&hist, n, inkPercentileHigh)
	den := p95 - p5
	if den < 1 {
		den = 1 // 近纯色图：Ink 全 0（无"更暗"可言），权重只剩边缘通道
	}
	inv := 255.0 / den
	for i, v := range smoothed {
		t := (p95 - float64(v)) * inv
		if t < 0 {
			t = 0
		}
		if t > 255 {
			t = 255
		}
		ink[i] = uint8(t + 0.5)
	}
	return gn, ink
}

// sobelNormalized 对平滑灰度图算 Sobel 幅值并按 p99 归一化到 [0,1]。
func sobelNormalized(smoothed []uint8, gw, gh int) []float32 {
	mag := sobelMagnitude(smoothed, gw, gh)
	out := make([]float32, len(mag))
	maxV := 0.0
	for _, m := range mag {
		if v := float64(m); v > maxV {
			maxV = v
		}
	}
	if maxV <= 0 {
		return out // 完全平坦：所有权重相同，退化为均匀分层抽样
	}

	// 直方图求 p99（比排序 400 万 float32 快一个量级）。
	hist := make([]int, sobelHistBins)
	inv := float64(sobelHistBins-1) / maxV
	for _, m := range mag {
		b := int(float64(m) * inv)
		if b < 0 {
			b = 0
		}
		if b >= sobelHistBins {
			b = sobelHistBins - 1
		}
		hist[b]++
	}
	target := int(math.Ceil(0.99 * float64(len(mag))))
	cum := 0
	p99bin := 0
	for b, c := range hist {
		cum += c
		if cum >= target {
			p99bin = b
			break
		}
	}
	p99 := maxV * float64(p99bin+1) / float64(sobelHistBins)
	if floor := maxV / 256; p99 < floor {
		p99 = floor
	}
	if p99 <= 0 {
		return out
	}

	scale := 1 / p99
	for i, m := range mag {
		v := float64(m) * scale
		if v > 1 {
			v = 1
		}
		out[i] = float32(v)
	}
	return out
}

// computeStructureMap 计算工作分辨率下的 Sobel 结构图 Gn ∈ [0,1]
// （只算梯度通道；depthpixel 模式测 edgePointRatio、以及测试用）。
func computeStructureMap(img image.Image, gw, gh int) []float32 {
	gray := grayGrid(img, gw, gh)
	smoothed := blur3x3(gray, gw, gh)
	return sobelNormalized(smoothed, gw, gh)
}

// pixelAt 返回原图相对 Bounds().Min 的 (x,y) 处 RGB（0..255）。
type pixelAt func(x, y int) (r, g, b uint8)

// makePixelAt 为常见图像类型提供直接读 Pix 的快路径（比 interface At() 快很多），
// 其它类型（例如 JPEG 的 YCbCr）走标准 color 转换。
func makePixelAt(img image.Image) pixelAt {
	b := img.Bounds()
	switch im := img.(type) {
	case *image.RGBA:
		return func(x, y int) (uint8, uint8, uint8) {
			i := im.PixOffset(b.Min.X+x, b.Min.Y+y)
			p := im.Pix[i : i+4 : i+4]
			return p[0], p[1], p[2]
		}
	case *image.NRGBA:
		return func(x, y int) (uint8, uint8, uint8) {
			i := im.PixOffset(b.Min.X+x, b.Min.Y+y)
			p := im.Pix[i : i+4 : i+4]
			return p[0], p[1], p[2]
		}
	case *image.Gray:
		return func(x, y int) (uint8, uint8, uint8) {
			v := im.Pix[im.PixOffset(b.Min.X+x, b.Min.Y+y)]
			return v, v, v
		}
	case *image.YCbCr:
		return func(x, y int) (uint8, uint8, uint8) {
			c := im.YCbCrAt(b.Min.X+x, b.Min.Y+y)
			r, g, bl := color.YCbCrToRGB(c.Y, c.Cb, c.Cr)
			return r, g, bl
		}
	}
	return func(x, y int) (uint8, uint8, uint8) {
		r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
		return uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8)
	}
}

// grayGrid 把原图缩小成 gw×gh 的灰度图。
// 每个工作像素取覆盖源区域内的 n×n 个均匀采样点做平均（n 按覆盖大小自适应，
// 避免细线被最近邻抽样漏掉，同时控制 2048² 时的开销）。
func grayGrid(img image.Image, gw, gh int) []uint8 {
	bounds := img.Bounds()
	iw, ih := bounds.Dx(), bounds.Dy()
	px := makePixelAt(img)
	out := make([]uint8, gw*gh)
	boxX := (iw + gw - 1) / gw
	boxY := (ih + gh - 1) / gh
	box := maxInt(boxX, boxY)
	n := graySamplesPerAxis
	if box <= 1 {
		n = 1
	} else if box <= 2 {
		n = 2
	}
	for gy := 0; gy < gh; gy++ {
		sy0 := gy * ih / gh
		sy1 := (gy + 1) * ih / gh
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		if sy1 > ih {
			sy1 = ih
		}
		for gx := 0; gx < gw; gx++ {
			sx0 := gx * iw / gw
			sx1 := (gx + 1) * iw / gw
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}
			if sx1 > iw {
				sx1 = iw
			}
			var sum uint32
			for j := 0; j < n; j++ {
				yy := sy0 + (sy1-sy0)*(2*j+1)/(2*n)
				if yy >= ih {
					yy = ih - 1
				}
				for i := 0; i < n; i++ {
					xx := sx0 + (sx1-sx0)*(2*i+1)/(2*n)
					if xx >= iw {
						xx = iw - 1
					}
					r, g, b := px(xx, yy)
					sum += 299*uint32(r) + 587*uint32(g) + 114*uint32(b)
				}
			}
			out[gy*gw+gx] = uint8(sum / (uint32(n*n) * 1000))
		}
	}
	return out
}

// blur3x3 做一次 3×3 高斯（1 2 1 / 2 4 2 / 1 2 1，/16）轻微抑噪。
func blur3x3(src []uint8, w, h int) []uint8 {
	dst := make([]uint8, len(src))
	for y := 0; y < h; y++ {
		y0 := y - 1
		if y0 < 0 {
			y0 = 0
		}
		y2 := y + 1
		if y2 >= h {
			y2 = h - 1
		}
		for x := 0; x < w; x++ {
			x0 := x - 1
			if x0 < 0 {
				x0 = 0
			}
			x2 := x + 1
			if x2 >= w {
				x2 = w - 1
			}
			sum := int(src[y0*w+x0]) + 2*int(src[y0*w+x]) + int(src[y0*w+x2]) +
				2*int(src[y*w+x0]) + 4*int(src[y*w+x]) + 2*int(src[y*w+x2]) +
				int(src[y2*w+x0]) + 2*int(src[y2*w+x]) + int(src[y2*w+x2])
			dst[y*w+x] = uint8(sum / 16)
		}
	}
	return dst
}

// sobelMagnitude 计算 Sobel 梯度幅值 sqrt(Gx²+Gy²)。
func sobelMagnitude(src []uint8, w, h int) []float32 {
	out := make([]float32, len(src))
	for y := 0; y < h; y++ {
		y0 := y - 1
		if y0 < 0 {
			y0 = 0
		}
		y2 := y + 1
		if y2 >= h {
			y2 = h - 1
		}
		for x := 0; x < w; x++ {
			x0 := x - 1
			if x0 < 0 {
				x0 = 0
			}
			x2 := x + 1
			if x2 >= w {
				x2 = w - 1
			}
			a00 := float64(src[y0*w+x0])
			a10 := float64(src[y0*w+x])
			a20 := float64(src[y0*w+x2])
			a01 := float64(src[y*w+x0])
			a21 := float64(src[y*w+x2])
			a02 := float64(src[y2*w+x0])
			a12 := float64(src[y2*w+x])
			a22 := float64(src[y2*w+x2])
			gx := -a00 + a20 - 2*a01 + 2*a21 - a02 + a22
			gy := -a00 - 2*a10 - a20 + a02 + 2*a12 + a22
			out[y*w+x] = float32(math.Sqrt(gx*gx + gy*gy))
		}
	}
	return out
}

// selectStructured 做确定性分层抽样：工作网格切成小块（层），
// 每层预算 ∝ 该层权重和（w = 1 + kEdge*Gn + kInk*Ink），层内做加权系统抽样。
//
// 全程无随机数、无 map 遍历，输出顺序与输入/并行度无关 → 同输入逐字节可复现。
// 返回被选中的工作像素下标与候选像素总数。
func selectStructured(gn []float32, ink []uint8, ok []bool, gw, gh, maxPoints int, kEdge, kInk float64) ([]int32, int) {
	weightOf := func(i int) float64 {
		w := 1.0
		if kEdge != 0 {
			w += kEdge * float64(gn[i])
		}
		if kInk != 0 {
			w += kInk * float64(ink[i]) / 255.0
		}
		return w
	}

	tileSize := (maxInt(gw, gh) + structureTileDivisor - 1) / structureTileDivisor
	if tileSize < structureTileMin {
		tileSize = structureTileMin
	}
	if tileSize > structureTileMax {
		tileSize = structureTileMax
	}
	tw := (gw + tileSize - 1) / tileSize
	th := (gh + tileSize - 1) / tileSize

	type tile struct {
		cands []int32
		wsum  float64
	}
	tiles := make([]tile, tw*th)
	totalW := 0.0
	candidateCount := 0
	for y := 0; y < gh; y++ {
		for x := 0; x < gw; x++ {
			i := y*gw + x
			if !ok[i] {
				continue
			}
			t := (y/tileSize)*tw + x/tileSize
			tiles[t].cands = append(tiles[t].cands, int32(i))
			w := weightOf(i)
			tiles[t].wsum += w
			totalW += w
			candidateCount++
		}
	}
	if candidateCount == 0 || totalW <= 0 {
		return nil, candidateCount
	}

	// 候选不足：全取（"不超过 max_points" 的合法情况）。
	if candidateCount <= maxPoints {
		sel := make([]int32, 0, candidateCount)
		for t := range tiles {
			sel = append(sel, tiles[t].cands...)
		}
		return sel, candidateCount
	}

	// 预算 = max_points × 层权重占比，最大余数法精确分配。
	budgets := make([]int, len(tiles))
	fracs := make([]float64, len(tiles))
	allocated := 0
	for t := range tiles {
		b := float64(maxPoints) * tiles[t].wsum / totalW
		budgets[t] = int(b)
		fracs[t] = b - float64(budgets[t])
		allocated += budgets[t]
	}
	order := make([]int, len(tiles))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		ia, ib := order[a], order[b]
		if fracs[ia] != fracs[ib] {
			return fracs[ia] > fracs[ib]
		}
		return ia < ib
	})
	remain := maxPoints - allocated
	for _, t := range order {
		if remain <= 0 {
			break
		}
		budgets[t]++
		remain--
	}

	// 夹到每层候选数上限；超出的预算按层序轮转补给仍有空位的层。
	leftover := 0
	for t := range tiles {
		if budgets[t] > len(tiles[t].cands) {
			leftover += budgets[t] - len(tiles[t].cands)
			budgets[t] = len(tiles[t].cands)
		}
	}
	for leftover > 0 {
		progressed := false
		for t := range tiles {
			if budgets[t] < len(tiles[t].cands) {
				budgets[t]++
				leftover--
				progressed = true
				if leftover == 0 {
					break
				}
			}
		}
		if !progressed {
			break
		}
	}

	sel := make([]int32, 0, maxPoints)
	for t := range tiles {
		cands := tiles[t].cands
		b := budgets[t]
		if b <= 0 {
			continue
		}
		if b >= len(cands) {
			sel = append(sel, cands...)
			continue
		}
		// 层内做"加权系统抽样"：沿行序扫描候选，累计 p_i = w_i*q/W_tile，
		// 越过整数就选一个。平坦区域会被**均匀铺开**（不会因为权重相同退化成
		// "取前 q 个"的条纹），权重高的点（边缘/墨）被更高概率选中。
		var (
			acc      float64
			picked   = make([]bool, len(cands))
			selCount = 0
			wsum     = tiles[t].wsum
			q        = float64(b)
		)
		for j, ci := range cands {
			acc += weightOf(int(ci)) * q / wsum
			if acc >= 1 {
				acc--
				picked[j] = true
				selCount++
				sel = append(sel, ci)
			}
		}
		// 高权重点的 p_i>1 会被"每候选最多选一次"截断，导致少量预算没用掉；
		// 用等步长从未选中的候选里补齐，保持空间均匀且确定性。
		if deficit := b - selCount; deficit > 0 {
			unpicked := make([]int32, 0, len(cands)-selCount)
			for j, ci := range cands {
				if !picked[j] {
					unpicked = append(unpicked, ci)
				}
			}
			sel = append(sel, stridePick(unpicked, deficit)...)
		}
	}
	return sel, candidateCount
}

// stridePick 从有序候选里等步长取 want 个（确定性；用于补齐抽样预算）。
func stridePick(cands []int32, want int) []int32 {
	if want <= 0 || len(cands) == 0 {
		return nil
	}
	if want >= len(cands) {
		return cands
	}
	out := make([]int32, 0, want)
	step := float64(len(cands)) / float64(want)
	for k := 0; k < want; k++ {
		out = append(out, cands[int(float64(k)*step)])
	}
	return out
}

// edgeRatioByInverse 把点云反向投影回网格像素，统计 Gn > 0.5 的点占比。
// focalX/focalY 是"网格分辨率下的焦距"（depthpixel 模式直接传 focal）。
// 注意点云里 z = -d（d 是距离），反解像素时要先取 d = -z。
func edgeRatioByInverse(pts []geometry.Vec3, gw, gh int, focalX, focalY float64, gn []float32) float64 {
	if len(pts) == 0 || len(gn) == 0 || focalX == 0 || focalY == 0 {
		return 0
	}
	cx := float64(gw) / 2
	cy := float64(gh) / 2
	edge := 0
	for _, p := range pts {
		dist := -float64(p.Z) // z = -d
		if dist <= 0 {
			continue
		}
		// 由 x=(u+0.5-cx)*d/fx 反解 u、v。
		u := float64(p.X)*focalX/dist + cx - 0.5
		v := cy - float64(p.Y)*focalY/dist - 0.5
		ix := int(math.Round(u))
		iy := int(math.Round(v))
		if ix < 0 {
			ix = 0
		}
		if ix >= gw {
			ix = gw - 1
		}
		if iy < 0 {
			iy = 0
		}
		if iy >= gh {
			iy = gh - 1
		}
		if gn[iy*gw+ix] > EdgeRatioThreshold {
			edge++
		}
	}
	return float64(edge) / float64(len(pts))
}
