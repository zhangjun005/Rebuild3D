// Package pipeline 把"深度图 + 原图"变成可直接下发浏览器的 ILC1 点云。
//
// 两种采样模式（docs/selection.md §4.4 的相机约定，两种通用）：
//
//	depthpixel  1 个深度像素 1 个点（老行为，保留作 A/B 对照）
//	structure   把深度双线性上采样到工作分辨率后，按**原图结构**（Sobel 梯度）
//	            加权做确定性分层抽样：点数花在线稿/边缘上，而不是均摊全图
//
// HTTP 层默认 structure；`sample_mode=depthpixel` 可回退。
// 结构模式不做体素下采样（会把刚加密的线稿点合并掉）。
//
// 关于深度：DA3MONO 输出的是**相对深度**，不带米制、内参、置信度
// （见 kron 假设 da3mono-no-confidence-map）。所以：
//   - 不按 conf 过滤（模型没有 conf），用深度跳变过滤代替；
//   - 不做尺度归一化（前端按包围盒自适应取景）；
//   - 焦距是艺术参数：fx = fy = focal_factor * max(W, H)，不是标定值。
package pipeline

import (
	"errors"
	"fmt"
	"image"
	"math"
	"sort"

	"illust3d/internal/cloud"
	"illust3d/internal/geometry"
)

// 采样模式。
const (
	// SampleModeDepthPixel 是"1 深度像素 1 点"的老行为，保留作对照与回退。
	SampleModeDepthPixel = "depthpixel"
	// SampleModeStructure 是结构感知采样（按原图 Sobel 梯度加权），产品默认。
	SampleModeStructure = "structure"
)

// 默认参数。
const (
	// DefaultFocalFactor 是默认焦距系数（fx = fy = factor * max(W,H)）。
	DefaultFocalFactor = 1.0
	// DefaultMaxPoints 是默认点数上限（渲染 60fps 的经验范围，见 docs/selection.md §6）。
	DefaultMaxPoints = 200000
	// MinMaxPoints 是允许的最小点数上限（低于此值点云基本不可读）。
	MinMaxPoints = 1000
	// MaxMaxPoints 是允许的最大点数上限（浏览器侧的硬上限）。
	MaxMaxPoints = 5_000_000
	// DefaultEdgeWeight 是结构模式的默认边缘权重 k（w = 1 + k*Gn）。
	DefaultEdgeWeight = 6.0
	// MaxEdgeWeight 是 k 的上限，防止权重无限放大导致其它区域没点。
	MaxEdgeWeight = 64.0
	// autoEdgePercentile 是自动边缘阈值取的分位数：相邻深度差超过 p99 的判为遮挡边界。
	autoEdgePercentile = 0.99

	// WorkResPointFactor 决定结构模式的工作分辨率：长边 ≈ sqrt(max_points*3)。
	WorkResPointFactor = 3
	// MinWorkRes / MaxWorkRes 夹住工作分辨率长边（避免太小没细节、太大费时）。
	MinWorkRes = 256
	MaxWorkRes = 2048
	// EdgeRatioThreshold 是判定"强边缘点"的 Gn 阈值（edgePointRatio 指标用）。
	EdgeRatioThreshold = 0.5
)

var (
	// ErrInvalidDepth 表示深度图尺寸/参数非法。
	ErrInvalidDepth = errors.New("pipeline: 深度图参数非法")
	// ErrInvalidParams 表示采样参数非法（sample_mode / edge_weight 等）。
	ErrInvalidParams = errors.New("pipeline: 采样参数非法")
	// ErrNoPoints 表示过滤/下采样后没有可用点。
	ErrNoPoints = errors.New("pipeline: 过滤后没有有效点")
)

// Params 是生成点云的艺术/性能参数。
type Params struct {
	// FocalFactor 是焦距系数：fx = fy = FocalFactor * max(depthW, depthH)。0 表示用默认 1.0。
	// 小 -> 广角、点云"厚"；大 -> 长焦、接近浮雕层叠。
	FocalFactor float64
	// MaxPoints 是点数上限。0 表示用默认 200000。
	MaxPoints int
	// VoxelSize 是体素下采样边长；0 表示自动（按包围盒与 MaxPoints 推）。
	// structure 模式默认**不做**体素下采样；显式给 >0 时才会应用。
	VoxelSize float32
	// EdgeThresh 是 FilterDepthEdges 的绝对深度差阈值；0 表示按深度分位数自动取。
	EdgeThresh float32
	// SampleMode 是采样模式："depthpixel" 或 "structure"。
	// 空值 = depthpixel（保持库层老行为；HTTP API 层默认 structure）。
	SampleMode string
	// EdgeWeight 是结构模式的边缘权重 k（w = 1 + k*Gn）。0 表示用默认 6。
	// k 越大越偏向强边缘（最多 MaxEdgeWeight）。
	EdgeWeight float64
}

// Result 是一次点云生成的结果（附带回给前端的元数据）。
type Result struct {
	// ILC1 是编码后的点云字节（docs/selection.md §4.2）。
	ILC1 []byte
	// PointCount 是最终点数（= ILC1 里的 count）。
	PointCount int
	// DepthWidth / DepthHeight 是 sidecar 返回的深度图尺寸。
	DepthWidth, DepthHeight int
	// ImageWidth / ImageHeight 是原始插画尺寸（取色用）。
	ImageWidth, ImageHeight int
	// BBoxMin / BBoxMax 是最终点云的包围盒（前端按它取景）。
	BBoxMin, BBoxMax geometry.Vec3
	// DroppedPixels 是 depthpixel 模式下被边缘过滤/无效深度丢掉的像素数；
	// structure 模式下是工作网格里因深度无效而不可采样的像素数。
	DroppedPixels int
	// VoxelSize 是实际使用的体素边长（0 表示没有下采样）。
	VoxelSize float32
	// EdgeThresh 是实际使用的边缘阈值。
	EdgeThresh float32
	// Focal 是实际使用的焦距像素值（fx = fy）。
	Focal float64
	// SampleMode 是实际使用的采样模式。
	SampleMode string
	// WorkRes 是实际采样网格的长边（depthpixel = 深度图长边；structure = 工作分辨率长边）。
	WorkRes int
	// EdgePointRatio 是落在强边缘（Gn > 0.5）上的点占比，用于对比两种模式。
	EdgePointRatio float64
}

// normalize 补默认值并做范围校验。
func (p Params) normalize() (Params, error) {
	if p.FocalFactor == 0 {
		p.FocalFactor = DefaultFocalFactor
	}
	if math.IsNaN(p.FocalFactor) || math.IsInf(p.FocalFactor, 0) || p.FocalFactor <= 0 {
		return p, fmt.Errorf("%w: focal_factor=%v 必须 > 0 的有限数", ErrInvalidParams, p.FocalFactor)
	}
	if p.MaxPoints == 0 {
		p.MaxPoints = DefaultMaxPoints
	}
	if p.MaxPoints < MinMaxPoints {
		p.MaxPoints = MinMaxPoints
	}
	if p.MaxPoints > MaxMaxPoints {
		p.MaxPoints = MaxMaxPoints
	}
	if math.IsNaN(float64(p.VoxelSize)) || math.IsInf(float64(p.VoxelSize), 0) || p.VoxelSize < 0 {
		return p, fmt.Errorf("%w: voxel_size=%v 必须 >= 0", ErrInvalidParams, p.VoxelSize)
	}
	if math.IsNaN(float64(p.EdgeThresh)) || math.IsInf(float64(p.EdgeThresh), 0) || p.EdgeThresh < 0 {
		return p, fmt.Errorf("%w: edge_thresh=%v 必须 >= 0", ErrInvalidParams, p.EdgeThresh)
	}
	switch p.SampleMode {
	case "", SampleModeDepthPixel:
		p.SampleMode = SampleModeDepthPixel
	case SampleModeStructure:
	default:
		return p, fmt.Errorf("%w: sample_mode=%q 只支持 %q / %q",
			ErrInvalidParams, p.SampleMode, SampleModeDepthPixel, SampleModeStructure)
	}
	if p.SampleMode == SampleModeStructure {
		if p.EdgeWeight == 0 {
			p.EdgeWeight = DefaultEdgeWeight
		}
		if math.IsNaN(p.EdgeWeight) || math.IsInf(p.EdgeWeight, 0) || p.EdgeWeight < 0 {
			return p, fmt.Errorf("%w: edge_weight=%v 必须 >= 0 的有限数", ErrInvalidParams, p.EdgeWeight)
		}
		if p.EdgeWeight > MaxEdgeWeight {
			p.EdgeWeight = MaxEdgeWeight
		}
	}
	return p, nil
}

// Build 从深度图与原图生成点云。
//
// depth 长度必须 >= w*h（下标 = y*w+x）；img 用于给每个点取颜色。
// 返回的 Result.ILC1 已经是最终字节，可直接作为 HTTP 响应体。
//
// 采样模式见 Params.SampleMode；两种模式共用深度边缘剔除与相机约定。
func Build(depth []float32, w, h int, img image.Image, params Params) (*Result, error) {
	p, err := params.normalize()
	if err != nil {
		return nil, err
	}
	if w <= 0 || h <= 0 || len(depth) < w*h {
		return nil, fmt.Errorf("%w: 深度图 %dx%d、%d 个值", ErrInvalidDepth, w, h, len(depth))
	}
	if img == nil {
		return nil, errors.New("pipeline: 原图为 nil（给点云取色需要原图）")
	}

	// 1) 深度边缘剔除（消 flying pixels）：两种采样模式共用。
	thresh := p.EdgeThresh
	if thresh == 0 {
		thresh = autoEdgeThresh(depth, w, h)
	}
	discard := geometry.FilterDepthEdges(depth, w, h, thresh)
	if discard == nil {
		return nil, fmt.Errorf("%w: FilterDepthEdges 参数非法", ErrInvalidDepth)
	}

	// 2) 相机：fx = fy = focal_factor * max(W,H)，主点在图像中心。
	focal := p.FocalFactor * float64(maxInt(w, h))

	if p.SampleMode == SampleModeStructure {
		return buildStructure(depth, w, h, img, p, focal, discard, thresh)
	}
	return buildDepthPixel(depth, w, h, img, p, focal, discard, thresh)
}

// buildDepthPixel 是"1 深度像素 1 点"的路径（保留作 A/B 对照与回退）。
func buildDepthPixel(depth []float32, w, h int, img image.Image, p Params, focal float64, discard []bool, thresh float32) (*Result, error) {
	// 2) 反投影：fx = fy = focal_factor * max(W,H)，主点在图像中心。
	pts := geometry.UnprojectPerspective(depth, w, h, focal, focal, float64(w)/2, float64(h)/2)
	if pts == nil {
		return nil, fmt.Errorf("%w: UnprojectPerspective 参数非法", ErrInvalidDepth)
	}

	// 3) 过滤 + 从原图取色（深度像素 → 原图对应位置，最近邻）。
	kept := make([]geometry.Vec3, 0, len(pts))
	cols := make([]geometry.Color, 0, len(pts))
	bounds := img.Bounds()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			if discard[i] || !validDepth(depth[i]) {
				continue // 无效深度与边界像素都跳过（零值点不要进点云）
			}
			kept = append(kept, pts[i])
			cols = append(cols, sampleColor(img, bounds, x, y, w, h))
		}
	}
	dropped := w*h - len(kept)
	if len(kept) == 0 {
		return nil, ErrNoPoints
	}

	// 4) 点数控制：显式 voxel_size 优先，否则超过 max_points 时自动推。
	// 自动体素的初值用源像素间距（中位深度 / focal），对飞点与包围盒离群值稳健。
	spacingHint := 0.0
	if focal > 0 {
		spacingHint = medianDepth(kept, 8192) / focal
	}
	voxel := p.VoxelSize
	switch {
	case voxel > 0:
		kept, cols = geometry.VoxelDownsample(kept, cols, voxel)
		if len(kept) > p.MaxPoints {
			// 用户给的体素太细：仍然尊重 max_points 上限。
			kept, cols, voxel = autoDownsample(kept, cols, p.MaxPoints, spacingHint)
		}
	case len(kept) > p.MaxPoints:
		kept, cols, voxel = autoDownsample(kept, cols, p.MaxPoints, spacingHint)
	}
	if len(kept) == 0 {
		return nil, ErrNoPoints
	}

	// 5) 与 structure 模式对照的指标：最终点里有多少落在原图强边缘上。
	//    结构图按**深度分辨率**算，再把每个最终点反向投影回像素查 Gn。
	ratio := 0.0
	if len(kept) > 0 && focal > 0 {
		gn := computeStructureMap(img, w, h)
		ratio = edgeRatioByInverse(kept, w, h, focal, focal, gn)
	}

	minB, maxB := bbox(kept)
	data, err := cloud.Encode(cloud.New(kept, cols))
	if err != nil {
		return nil, fmt.Errorf("pipeline: 编码 ILC1 失败: %w", err)
	}
	return &Result{
		ILC1:           data,
		PointCount:     len(kept),
		DepthWidth:     w,
		DepthHeight:    h,
		ImageWidth:     bounds.Dx(),
		ImageHeight:    bounds.Dy(),
		BBoxMin:        minB,
		BBoxMax:        maxB,
		DroppedPixels:  dropped,
		VoxelSize:      voxel,
		EdgeThresh:     thresh,
		Focal:          focal,
		SampleMode:     SampleModeDepthPixel,
		WorkRes:        maxInt(w, h),
		EdgePointRatio: ratio,
	}, nil
}

// autoEdgeThresh 取相邻（右/下）有效深度差的 p99 作为边界阈值。
// 用分位数而不是固定值，是因为 DA3MONO 的相对深度尺度逐图不同。
func autoEdgeThresh(depth []float32, w, h int) float32 {
	diffs := make([]float32, 0, 2*w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			d := depth[i]
			if !validDepth(d) {
				continue
			}
			if x+1 < w {
				if nd := depth[i+1]; validDepth(nd) {
					diffs = append(diffs, absF32(d-nd))
				}
			}
			if y+1 < h {
				if nd := depth[i+w]; validDepth(nd) {
					diffs = append(diffs, absF32(d-nd))
				}
			}
		}
	}
	if len(diffs) == 0 {
		return 0
	}
	sort.Slice(diffs, func(i, j int) bool { return diffs[i] < diffs[j] })
	idx := int(float64(len(diffs)-1) * autoEdgePercentile)
	t := diffs[idx]
	if t <= 0 {
		return 0
	}
	return t
}

// autoDownsample 推出体素边长，使点数不超过 maxPoints（即自动 voxel_size 的实现）。
//
// 关键教训：初始值不能凭"包围盒最长边/1000"瞎猜。深度图里的少量飞点会把包围盒撑大，
// 而那个初值又远小于真实点间距，迭代迟迟收敛不了（实测曾把 24.7 万点压成 4 点）。
// 这里改用**源数据自身的像素间距**来估：
//
//	相邻深度像素在世界空间的间距 ≈ 中位深度 / focal
//	表面点云占格数 ≈ 点数 * (间距/体素)²
//	→ 体素 ≈ 间距 * sqrt(点数/上限)
//
// spacingHint 由 Build 用中位深度算出（对飞点稳健）；之后按 sqrt(点数/上限) 迭代修正，
// 正常 1~2 轮就能命中，最多 8 轮。
func autoDownsample(pts []geometry.Vec3, cols []geometry.Color, maxPoints int, spacingHint float64) ([]geometry.Vec3, []geometry.Color, float32) {
	if len(pts) == 0 {
		return nil, nil, 0
	}
	v := initialVoxelSize(pts, maxPoints, spacingHint)
	for iter := 0; iter < 8; iter++ {
		dp, dc := geometry.VoxelDownsample(pts, cols, float32(v))
		if len(dp) <= maxPoints {
			return dp, dc, float32(v)
		}
		v *= math.Sqrt(float64(len(dp)) / float64(maxPoints))
		if !(v > 0) || math.IsNaN(v) || math.IsInf(v, 0) {
			break
		}
	}
	// 兜底（理论上到不了）：等步长抽样，保证点数不超上限、也不会塌成个位数。
	dp, dc := strideSample(pts, cols, maxPoints)
	return dp, dc, float32(v)
}

// initialVoxelSize 给出自动体素的初值：优先用源像素间距（spacingHint），
// 没有线索时退化为"稳健包围盒面积 / 上限"。
func initialVoxelSize(pts []geometry.Vec3, maxPoints int, spacingHint float64) float64 {
	n := len(pts)
	factor := 1.0
	if maxPoints > 0 && n > maxPoints {
		factor = math.Sqrt(float64(n) / float64(maxPoints))
	}
	if spacingHint > 0 && !math.IsNaN(spacingHint) && !math.IsInf(spacingHint, 0) {
		return spacingHint * factor
	}

	// 无 hint：用分位数包围盒（p1/p99）估表面积，避免飞点撑大 bbox。
	minB, maxB := robustBBox(pts, 0.01, 0.99)
	ext := [3]float64{
		float64(maxB.X - minB.X),
		float64(maxB.Y - minB.Y),
		float64(maxB.Z - minB.Z),
	}
	sort.Float64s(ext[:])
	area := ext[1] * ext[2] // 最大的两个边长 → 表面积近似（表面点云）
	if area > 0 && maxPoints > 0 {
		if v := math.Sqrt(area / float64(maxPoints)); v > 0 && !math.IsInf(v, 0) && !math.IsNaN(v) {
			return v
		}
	}
	return 1 // 极端兜底：单位体素（调用方最多再迭代修正）
}

// medianDepth 用采样近似中位**深度距离**。
// 反投影后 z = -d（d 是距离，越大越远），所以这里取 |z| 的中位数，与 z 的符号约定解耦。
func medianDepth(pts []geometry.Vec3, maxSamples int) float64 {
	if len(pts) == 0 {
		return 0
	}
	step := 1
	if maxSamples > 0 && len(pts) > maxSamples {
		step = len(pts) / maxSamples
	}
	vals := make([]float32, 0, len(pts)/step+1)
	for i := 0; i < len(pts); i += step {
		vals = append(vals, float32(math.Abs(float64(pts[i].Z))))
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
	mid := len(vals) / 2
	if len(vals)%2 == 0 && mid > 0 {
		return float64(vals[mid-1]+vals[mid]) / 2
	}
	return float64(vals[mid])
}

// robustBBox 用分位数（lo/hi，如 0.01/0.99）估计包围盒，抽稀采样以控制开销。
func robustBBox(pts []geometry.Vec3, lo, hi float64) (geometry.Vec3, geometry.Vec3) {
	const maxSamples = 8192
	if len(pts) == 0 {
		return geometry.Vec3{}, geometry.Vec3{}
	}
	step := 1
	if len(pts) > maxSamples {
		step = len(pts) / maxSamples
	}
	xs := make([]float32, 0, len(pts)/step+1)
	ys := make([]float32, 0, len(pts)/step+1)
	zs := make([]float32, 0, len(pts)/step+1)
	for i := 0; i < len(pts); i += step {
		p := pts[i]
		xs = append(xs, p.X)
		ys = append(ys, p.Y)
		zs = append(zs, p.Z)
	}
	sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })
	sort.Slice(ys, func(i, j int) bool { return ys[i] < ys[j] })
	sort.Slice(zs, func(i, j int) bool { return zs[i] < zs[j] })
	at := func(s []float32, q float64) float32 {
		i := int(float64(len(s)-1) * q)
		if i < 0 {
			i = 0
		}
		if i >= len(s) {
			i = len(s) - 1
		}
		return s[i]
	}
	return geometry.Vec3{X: at(xs, lo), Y: at(ys, lo), Z: at(zs, lo)},
		geometry.Vec3{X: at(xs, hi), Y: at(ys, hi), Z: at(zs, hi)}
}

// strideSample 是最后的保底抽样（只用在下采样迭代都不收敛的极端情况）。
func strideSample(pts []geometry.Vec3, cols []geometry.Color, maxPoints int) ([]geometry.Vec3, []geometry.Color) {
	if maxPoints <= 0 || len(pts) <= maxPoints {
		return pts, cols
	}
	step := float64(len(pts)) / float64(maxPoints)
	outPts := make([]geometry.Vec3, 0, maxPoints)
	outCols := make([]geometry.Color, 0, maxPoints)
	for k := 0; k < maxPoints; k++ {
		i := int(float64(k) * step)
		outPts = append(outPts, pts[i])
		if i < len(cols) {
			outCols = append(outCols, cols[i])
		} else {
			outCols = append(outCols, geometry.Color{})
		}
	}
	return outPts, outCols
}

// sampleColor 把深度图像素 (x,y) 映射到原图并取最近邻颜色。
func sampleColor(img image.Image, bounds image.Rectangle, x, y, w, h int) geometry.Color {
	iw, ih := bounds.Dx(), bounds.Dy()
	ix := int((float64(x) + 0.5) * float64(iw) / float64(w))
	iy := int((float64(y) + 0.5) * float64(ih) / float64(h))
	if ix < 0 {
		ix = 0
	}
	if ix >= iw {
		ix = iw - 1
	}
	if iy < 0 {
		iy = 0
	}
	if iy >= ih {
		iy = ih - 1
	}
	r, g, b, _ := img.At(bounds.Min.X+ix, bounds.Min.Y+iy).RGBA()
	return geometry.Color{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8)}
}

// bbox 返回点云的包围盒；空点云返回零值。
func bbox(pts []geometry.Vec3) (minB, maxB geometry.Vec3) {
	if len(pts) == 0 {
		return geometry.Vec3{}, geometry.Vec3{}
	}
	minB = pts[0]
	maxB = pts[0]
	for _, p := range pts[1:] {
		if p.X < minB.X {
			minB.X = p.X
		}
		if p.Y < minB.Y {
			minB.Y = p.Y
		}
		if p.Z < minB.Z {
			minB.Z = p.Z
		}
		if p.X > maxB.X {
			maxB.X = p.X
		}
		if p.Y > maxB.Y {
			maxB.Y = p.Y
		}
		if p.Z > maxB.Z {
			maxB.Z = p.Z
		}
	}
	return minB, maxB
}

// validDepth 报告深度值是否可用于反投影（与 geometry 内部约定一致：>0 且有限）。
func validDepth(d float32) bool {
	return d > 0 && !math.IsNaN(float64(d)) && !math.IsInf(float64(d), 0)
}

func absF32(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
