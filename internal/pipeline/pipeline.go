// Package pipeline 把"深度图 + 原图"变成可直接下发浏览器的 ILC1 点云。
//
// 流程（相机约定见 docs/selection.md §4.4）：
//
//	深度图 → FilterDepthEdges（消 flying pixels）→ UnprojectPerspective
//	      → 用原图取色 → VoxelDownsample（控制点数）→ ILC1 编码
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
	// autoEdgePercentile 是自动边缘阈值取的分位数：相邻深度差超过 p99 的判为遮挡边界。
	autoEdgePercentile = 0.99
)

var (
	// ErrInvalidDepth 表示深度图尺寸/参数非法。
	ErrInvalidDepth = errors.New("pipeline: 深度图参数非法")
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
	VoxelSize float32
	// EdgeThresh 是 FilterDepthEdges 的绝对深度差阈值；0 表示按深度分位数自动取。
	EdgeThresh float32
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
	// DroppedPixels 是被边缘过滤/无效深度丢掉的像素数。
	DroppedPixels int
	// VoxelSize 是实际使用的体素边长（0 表示没有下采样）。
	VoxelSize float32
	// EdgeThresh 是实际使用的边缘阈值。
	EdgeThresh float32
	// Focal 是实际使用的焦距像素值（fx = fy）。
	Focal float64
}

// normalize 补默认值并做范围校验。
func (p Params) normalize() (Params, error) {
	if p.FocalFactor == 0 {
		p.FocalFactor = DefaultFocalFactor
	}
	if math.IsNaN(p.FocalFactor) || math.IsInf(p.FocalFactor, 0) || p.FocalFactor <= 0 {
		return p, fmt.Errorf("%w: focal_factor=%v 必须 > 0 的有限数", ErrInvalidDepth, p.FocalFactor)
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
		return p, fmt.Errorf("%w: voxel_size=%v 必须 >= 0", ErrInvalidDepth, p.VoxelSize)
	}
	if math.IsNaN(float64(p.EdgeThresh)) || math.IsInf(float64(p.EdgeThresh), 0) || p.EdgeThresh < 0 {
		return p, fmt.Errorf("%w: edge_thresh=%v 必须 >= 0", ErrInvalidDepth, p.EdgeThresh)
	}
	return p, nil
}

// Build 从深度图与原图生成点云。
//
// depth 长度必须 >= w*h（下标 = y*w+x）；img 用于给每个点取颜色。
// 返回的 Result.ILC1 已经是最终字节，可直接作为 HTTP 响应体。
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

	// 1) 边缘剔除：自动阈值取相邻深度差的 p99，超过它的判为遮挡边界。
	thresh := p.EdgeThresh
	if thresh == 0 {
		thresh = autoEdgeThresh(depth, w, h)
	}
	discard := geometry.FilterDepthEdges(depth, w, h, thresh)
	if discard == nil {
		return nil, fmt.Errorf("%w: FilterDepthEdges 参数非法", ErrInvalidDepth)
	}

	// 2) 反投影：fx = fy = focal_factor * max(W,H)，主点在图像中心。
	focal := p.FocalFactor * float64(maxInt(w, h))
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
	voxel := p.VoxelSize
	switch {
	case voxel > 0:
		kept, cols = geometry.VoxelDownsample(kept, cols, voxel)
		if len(kept) > p.MaxPoints {
			// 用户给的体素太细：仍然尊重 max_points 上限。
			kept, cols, voxel = autoDownsample(kept, cols, p.MaxPoints)
		}
	case len(kept) > p.MaxPoints:
		kept, cols, voxel = autoDownsample(kept, cols, p.MaxPoints)
	}
	if len(kept) == 0 {
		return nil, ErrNoPoints
	}

	minB, maxB := bbox(kept)
	data, err := cloud.Encode(cloud.New(kept, cols))
	if err != nil {
		return nil, fmt.Errorf("pipeline: 编码 ILC1 失败: %w", err)
	}
	return &Result{
		ILC1:          data,
		PointCount:    len(kept),
		DepthWidth:    w,
		DepthHeight:   h,
		ImageWidth:    bounds.Dx(),
		ImageHeight:   bounds.Dy(),
		BBoxMin:       minB,
		BBoxMax:       maxB,
		DroppedPixels: dropped,
		VoxelSize:     voxel,
		EdgeThresh:    thresh,
		Focal:         focal,
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

// autoDownsample 迭代推出体素边长，使点数不超过 maxPoints。
// 初始体素取包围盒最长边的 1/1000（偏小），再用"点数 ~ 1/v²"（表面点云）迭代放大；
// 体积点云下会略微过冲（点更稀），可接受，因为 max_points 是上限而不是目标值。
func autoDownsample(pts []geometry.Vec3, cols []geometry.Color, maxPoints int) ([]geometry.Vec3, []geometry.Color, float32) {
	minB, maxB := bbox(pts)
	ex := float64(maxB.X - minB.X)
	ey := float64(maxB.Y - minB.Y)
	ez := float64(maxB.Z - minB.Z)
	maxExt := math.Max(ex, math.Max(ey, ez))
	if !(maxExt > 0) || math.IsInf(maxExt, 0) || math.IsNaN(maxExt) {
		maxExt = 1
	}
	v := maxExt / 1000 // 初始体素：足够细，保证第一轮点数接近原始点数
	for iter := 0; iter < 10; iter++ {
		dp, dc := geometry.VoxelDownsample(pts, cols, float32(v))
		if len(dp) == 0 {
			return dp, dc, float32(v)
		}
		if len(dp) <= maxPoints {
			return dp, dc, float32(v)
		}
		ratio := float64(len(dp)) / float64(maxPoints)
		v *= math.Sqrt(ratio) // 表面点云：点数 ≈ 面积 / v²
		if v > 4*maxExt {
			v = 4 * maxExt
		}
	}
	// 兜底：4×包围盒最长边 → 至多 8 个格子，必不超上限。
	v = 4 * maxExt
	dp, dc := geometry.VoxelDownsample(pts, cols, float32(v))
	return dp, dc, float32(v)
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
