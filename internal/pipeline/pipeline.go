// @kron:intent geometry/sampling/point-count-budget
// @kron:intent geometry/sampling/resolution-budget

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
	// DefaultMaxPoints 是默认点数上限（= 40 万）。
	// 实测（平涂 4000px 图，点径 3 / softness 0.25）：20 万点纸色从缝隙透出、背景斑驳；
	// 40 万点背景饱和均匀且线仍锐；60 万点画面几乎无差别但 ILC1 从 5.9MB 涨到 8.8MB。
	// 几何耗时几乎不随点数增长（成本在 4M 候选网格的权重计算），所以 40 万是近乎免费的覆盖率收益。
	DefaultMaxPoints = 400000
	// MinMaxPoints 是允许的最小点数上限（低于此值点云基本不可读）。
	MinMaxPoints = 1000
	// MaxMaxPoints 是允许的最大点数上限（浏览器侧的硬上限）。
	MaxMaxPoints = 5_000_000
	// DefaultEdgeWeight 是结构模式的默认边缘权重 kEdge（w = 1 + kEdge*Gn + kInk*Ink）。
	// 注意：pipeline 库层把 0 当作**显式关闭**该通道；默认值由 HTTP API 层填。
	DefaultEdgeWeight = 6.0
	// DefaultInkWeight 是结构模式的默认墨量权重 kInk（相对暗度通道）。
	DefaultInkWeight = 4.0
	// MaxEdgeWeight / MaxInkWeight 是权重上限，防止权重无限放大导致其它区域没点。
	MaxEdgeWeight = 64.0
	MaxInkWeight  = 64.0
	// DefaultLayerCount 是深度层量化的默认层数（0 = 关闭；2~8 有效）。
	//
	// 为什么默认开：DA3 在插画上的深度是一条连续分布，主体与背景之间有一条"过渡尾巴"
	// （实测 100 万点里 16.9% 落在层间且集中在人物周围）。这些点反投影后位于两层之间的
	// 中间 z，形成"从人物拖向背景的丝状散落点云"。层量化把它们吸附到连通块的深度上。
	DefaultLayerCount = 0
	// MinLayerCount / MaxLayerCount 是层数的允许区间。
	MinLayerCount = 2
	MaxLayerCount = 8
	// DefaultBandWidth / DefaultBandCompress 是「边界带深度压缩」的默认参数。
	//
	// 边界带 = 角色/背景轮廓向两侧膨胀 bandWidth 像素。
	// 压缩比 1 = 恒等（不动），越小越把边界带的深度往角色侧收（保持相对位置）。
	// 只影响边界带，角色内部与背景内部一律不动。
	//
	// ⚠ 默认关闭（0）。原因（实测，2026-11-01）：「按语义轮廓划边界带」只覆盖人物外轮廓，
	// 而散落点实际描在插画的**每根线稿**上（发丝/衣褶/指甲等内部线稿完全没覆盖）。
	// 用分割边界做带只改了 1.3% 像素，侧视 30° 前后看不出差别。
	// 待改成「深度不连续带 + 同层 IQR 判据（区分真实台阶与线稿缝合）」后再启用。
	DefaultBandWidth    = 0
	DefaultBandCompress = 0.3
	// MaxBandWidth 是边界带宽度的上限（再宽就动到不该动的地方了）。
	MaxBandWidth = 16
	// DefaultPerspective 是反投影的透视混合权重，默认 0 = **纯正交**。
	// 正交下各深度层都是与图像平行、等大的平面，只有 z 不同 → 转动是层间错动，
	// 不是以视点为中心的放射状扇形（这是插画 2.5D 想要的）。
	DefaultPerspective = 0.0
	// DefaultDepthScale 是 z 轴（垂直图片方向）的压缩系数，默认 1 = 不压缩。
	//
	// 正交投影下 z 只影响「浮雕厚度」与视差强度：越小越像原图、侧转露洞越小。
	// 注意 0 表示「用默认值」（与其他参数一致），要接近平面请用 0.001 这类小数。
	DefaultDepthScale = 1.0
	// MinDepthScale / MaxDepthScale 是允许区间。
	MinDepthScale = 0.001
	MaxDepthScale = 4.0
	// autoEdgePercentile 是自动边缘阈值取的分位数：相邻深度差超过 p99 的判为遮挡边界。
	autoEdgePercentile = 0.99

	// 结构模式的**工作分辨率**（长边像素）与 max_points 解耦：
	//   - 自动（WorkRes=0）：clamp(round(原图长边/2), AutoWorkResMin, AutoWorkResMax)
	//   - 显式：clamp 到 [MinWorkRes, MaxWorkRes]
	// workRes 决定"线稿最细能到多少"（由原图细节决定），max_points 只决定保留多少个点。
	AutoWorkResMin = 512
	AutoWorkResMax = 2048
	MinWorkRes     = 256
	MaxWorkRes     = 4096
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
	// MaxPoints 是点数上限。0 表示用默认 400000。
	MaxPoints int
	// VoxelSize 是体素下采样边长；0 表示自动（按包围盒与 MaxPoints 推）。
	// structure 模式默认**不做**体素下采样；显式给 >0 时才会应用。
	VoxelSize float32
	// EdgeThresh 是 FilterDepthEdges 的绝对深度差阈值；0 表示按深度分位数自动取。
	EdgeThresh float32
	// SampleMode 是采样模式："depthpixel" 或 "structure"。
	// 空值 = depthpixel（保持库层老行为；HTTP API 层默认 structure）。
	SampleMode string
	// EdgeWeight 是结构模式的边缘权重 kEdge（w = 1 + kEdge*Gn + kInk*Ink）。
	// 0 = 显式关闭边缘通道（库层不填默认值；HTTP API 默认 6）。
	EdgeWeight float64
	// InkWeight 是结构模式的墨量权重 kInk（相对暗度通道）。
	// 0 = 显式关闭墨量通道（库层不填默认值；HTTP API 默认 4）。
	InkWeight float64
	// WorkRes 是结构采样的工作分辨率长边（像素）。0 = 自动
	// （clamp(原图长边/2, 512, 2048)），显式值会被夹到 [256, 4096]。
	// **与 MaxPoints 解耦**：它决定线稿最细能到多少，MaxPoints 只决定保留多少点。
	WorkRes int
	// LayerCount 是深度层量化的层数：0 = 关闭（保留老行为），2~8 有效。
	// 开启后，反投影之前会把深度吸附到「连通块深度中位数」，消除层间中间深度点。
	// ⚠ 这会损失角色立体感（整块变平），不是默认路径，只作为对照工具。
	LayerCount int
	// BandWidth 是「边界带深度压缩」的边界带宽度（像素）；0 = 关闭。
	// 边界带沿角色/背景轮廓向两侧膨胀。只影响带内像素。
	BandWidth int
	// BandCompress 是边界带的深度压缩比，[0,1]；1 = 恒等（不动）。
	// 映射为 d' = d_char + BandCompress·(d - d_char)，d_char 是**局部**稳定深度，
	// 所以保持边界带内各点的相对位置。
	BandCompress float64
	// FlatThresh 是「突变两侧是否同层」的 IQR 阈值；0 = 自动（按梯度跨度取 6%）。
	// IQR <= FlatThresh → 同层缝合伪影（压缩）；否则 → 真实台阶（不动）。
	FlatThresh float64
	// Perspective 是反投影的透视混合权重 [0,1]：
	//   0 = 纯正交：各层与图像平行、等大，只有 z 不同（默认，插画 2.5D 的正确形态）
	//   1 = 纯透视：x/y 随深度放大，点云呈以视点为中心的放射状
	Perspective float64
	// DepthScale 是 z 轴压缩系数：z = -(d - dRef)·DepthScale。
	// 0 = 用默认 1（不压缩）；越小浮雕越薄、视差越弱、侧转露洞越小。
	DepthScale float64
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
	// DarkPointRatio 是落在原图暗像素（亮度 <= p20）上的点占比。
	DarkPointRatio float64
	// DarkPixelRatio 是原图里暗像素的占比（理论上≈0.20）。
	DarkPixelRatio float64
	// DarkEnrichment = DarkPointRatio / DarkPixelRatio：
	// 纯均匀采样 = 1.0；结构/墨量采样应显著 > 1（"线稿有没有被点云承载"的核心指标）。
	DarkEnrichment float64
	// LayerCount 是深度层量化的实际层数（0 = 未启用）。
	LayerCount int
	// LayerCenters 是各层中心（按深度从小到大），仅用于展示/调试。
	LayerCenters []float32
	// LayerFlattened 是被吸附到连通块深度中位数的像素数。
	LayerFlattened int
	// LayerComponents 是保留下来的连通块数（人物通常是一个）。
	LayerComponents int
	// Perspective 是实际使用的反投影透视权重（0=正交）。
	Perspective float64
	// DepthScale 是实际使用的 z 轴压缩系数。
	DepthScale float64
	// Seam 是深度缝合压缩的统计（Enabled=false 时 Fallback 说明原因）。
	Seam SeamStats
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
	// 层数：0 = 关闭；1 视作关闭；超出区间夹取（层数太多没有意义）。
	if p.LayerCount < 0 {
		return p, fmt.Errorf("%w: layer_count=%d 必须 >= 0", ErrInvalidParams, p.LayerCount)
	}
	if p.LayerCount == 1 {
		p.LayerCount = 0
	}
	if p.LayerCount > MaxLayerCount {
		p.LayerCount = MaxLayerCount
	}
	// 边界带：宽度 0 = 关闭；压缩比必须落在 [0,1]（1 = 恒等）。
	if p.BandWidth < 0 {
		return p, fmt.Errorf("%w: band_width=%d 必须 >= 0", ErrInvalidParams, p.BandWidth)
	}
	if p.BandWidth > MaxBandWidth {
		p.BandWidth = MaxBandWidth
	}
	if math.IsNaN(p.BandCompress) || math.IsInf(p.BandCompress, 0) || p.BandCompress < 0 || p.BandCompress > 1 {
		return p, fmt.Errorf("%w: band_compress=%v 必须落在 [0,1]", ErrInvalidParams, p.BandCompress)
	}
	if math.IsNaN(p.Perspective) || math.IsInf(p.Perspective, 0) || p.Perspective < 0 || p.Perspective > 1 {
		return p, fmt.Errorf("%w: perspective=%v 必须落在 [0,1]", ErrInvalidParams, p.Perspective)
	}
	if p.DepthScale == 0 {
		p.DepthScale = DefaultDepthScale
	}
	if math.IsNaN(p.DepthScale) || math.IsInf(p.DepthScale, 0) || p.DepthScale < 0 {
		return p, fmt.Errorf("%w: depth_scale=%v 必须 >= 0", ErrInvalidParams, p.DepthScale)
	}
	if p.DepthScale < MinDepthScale {
		p.DepthScale = MinDepthScale
	}
	if p.DepthScale > MaxDepthScale {
		p.DepthScale = MaxDepthScale
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
		// 0 = 显式关闭对应通道（默认值由 API 层给），负数/非有限 = 参数错。
		if math.IsNaN(p.EdgeWeight) || math.IsInf(p.EdgeWeight, 0) || p.EdgeWeight < 0 {
			return p, fmt.Errorf("%w: edge_weight=%v 必须 >= 0 的有限数", ErrInvalidParams, p.EdgeWeight)
		}
		if p.EdgeWeight > MaxEdgeWeight {
			p.EdgeWeight = MaxEdgeWeight
		}
		if math.IsNaN(p.InkWeight) || math.IsInf(p.InkWeight, 0) || p.InkWeight < 0 {
			return p, fmt.Errorf("%w: ink_weight=%v 必须 >= 0 的有限数", ErrInvalidParams, p.InkWeight)
		}
		if p.InkWeight > MaxInkWeight {
			p.InkWeight = MaxInkWeight
		}
	}
	if p.WorkRes < 0 {
		return p, fmt.Errorf("%w: work_res=%d 必须 >= 0（0=自动）", ErrInvalidParams, p.WorkRes)
	}
	if p.WorkRes > MaxWorkRes {
		p.WorkRes = MaxWorkRes
	}
	return p, nil
}

// Build 从深度图与原图生成点云（不带分割 mask；等价于 BuildWithMask 传 nil）。
//
// depth 长度必须 >= w*h（下标 = y*w+x）；img 用于给每个点取颜色。
// 返回的 Result.ILC1 已经是最终字节，可直接作为 HTTP 响应体。
//
// 采样模式见 Params.SampleMode；两种模式共用深度边缘剔除与相机约定。
func Build(depth []float32, w, h int, img image.Image, params Params) (*Result, error) {
	return BuildWithMask(depth, w, h, img, nil, 0, 0, params)
}

// BuildWithMask 与 Build 相同，但额外接受角色分割 mask 来做「边界带深度压缩」。
//
// mask 三档：0=背景 128=不确定 255=角色（尺寸 mw×mh，可与深度图不同）。
// mask 为空时行为与 Build 完全一致。
func BuildWithMask(
	depth []float32, w, h int, img image.Image,
	mask []uint8, mw, mh int,
	params Params,
) (*Result, error) {
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

	// 1.2) 深度缝合压缩（推荐路径）：沿**深度不连续处**（≈ 插画线稿）清理层间散落点。
	//
	// 只动「突变两侧稳定深度同层」的缝合伪影；真实台阶（手臂朝前等）保持不动，
	// 判据见 adjustDepthSeams 的注释。mask 目前不参与这一步（保留给将来的视差分类）。
	depth, seamStats := adjustDepthSeams(depth, w, h, p.BandWidth, p.BandCompress, p.FlatThresh)

	// 1.5) 深度层量化（可选，默认关闭）：整块压平——会损失角色立体感，
	// 只作为对照/极端散落时的工具，不作为默认路径。
	var layerStats LayerQuantizeStats
	if p.LayerCount >= MinLayerCount {
		depth, layerStats = quantizeDepthLayers(depth, w, h, discard, p.LayerCount)
	}

	// 2) 相机：fx = fy = focal_factor * max(W,H)，主点在图像中心。
	focal := p.FocalFactor * float64(maxInt(w, h))
	proj := makeProj(depth, w, h, focal, p.Perspective, p.DepthScale)

	var res *Result
	if p.SampleMode == SampleModeStructure {
		res, err = buildStructure(depth, w, h, img, p, focal, proj, discard, thresh)
	} else {
		res, err = buildDepthPixel(depth, w, h, img, p, focal, proj, discard, thresh)
	}
	if err != nil {
		return nil, err
	}
	res.LayerCount = layerStats.Layers
	res.LayerCenters = layerStats.Centers
	res.LayerFlattened = layerStats.Flattened
	res.LayerComponents = layerStats.Components
	res.Perspective = p.Perspective
	res.DepthScale = p.DepthScale
	res.Seam = seamStats
	return res, nil
}

// buildDepthPixel 是"1 深度像素 1 点"的路径（保留作 A/B 对照与回退）。
func buildDepthPixel(depth []float32, w, h int, img image.Image, p Params, focal float64, proj geometry.UnprojectBlendParams, discard []bool, thresh float32) (*Result, error) {
	// 2) 反投影（正交↔透视可混合，见 makeProj）。
	pts := geometry.UnprojectBlend(depth, w, h, proj)
	if pts == nil {
		return nil, fmt.Errorf("%w: UnprojectBlend 参数非法", ErrInvalidDepth)
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

	// 5) 与 structure 模式对照的指标：
	//    - 强边缘点占比（结构图按**深度分辨率**算，再把每个最终点反向投影回像素查 Gn）
	//    - 暗像素富集（线稿是深色细线，这个指标更贴"点云有没有承载线稿"）
	ratio := 0.0
	dark := imageDarkStats(img)
	darkPointRatio := 0.0
	if len(kept) > 0 && focal > 0 {
		gn := computeStructureMap(img, w, h)
		ratio = edgeRatioByInverse(kept, proj, w, h, w, h, gn)
		darkPointRatio = darkRatioByInverse(kept, proj, w, h, w, h, img, dark)
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
		DarkPointRatio: darkPointRatio,
		DarkPixelRatio: dark.darkPixelRatio,
		DarkEnrichment: darkEnrichmentOf(darkPointRatio, dark.darkPixelRatio),
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

// medianDepthMap 用采样近似深度图的中位深度（用于确定正交投影的代表深度）。
func medianDepthMap(depth []float32, w, h int) float64 {
	n := w * h
	if n == 0 {
		return 1
	}
	const maxSamples = 8192
	step := n / maxSamples
	if step < 1 {
		step = 1
	}
	vals := make([]float32, 0, maxSamples)
	for i := 0; i < n; i += step {
		if d := depth[i]; d > 0 && !math.IsNaN(float64(d)) && !math.IsInf(float64(d), 0) {
			vals = append(vals, d)
		}
	}
	if len(vals) == 0 {
		return 1
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
	return float64(vals[len(vals)/2])
}

// makeProj 组装反投影参数。
//
// 正交模式下 x/y 与深度无关（各层平行），需要给出「世界单位/像素」的比例；
// 取代表深度 dRef = 深度中位数，令 orthoScale = dRef/focal——
// 这样正交与透视在 dRef 处重合，切换 perspective 时尺寸连续。
func makeProj(depth []float32, w, h int, focal, perspective, depthScale float64) geometry.UnprojectBlendParams {
	dRef := medianDepthMap(depth, w, h)
	if dRef <= 0 || math.IsNaN(dRef) || math.IsInf(dRef, 0) {
		dRef = 1
	}
	return geometry.UnprojectBlendParams{
		FX:          focal,
		FY:          focal,
		CX:          float64(w) / 2,
		CY:          float64(h) / 2,
		OrthoScale:  dRef / focal,
		Perspective: perspective,
		RefDepth:    dRef,
		DepthScale:  depthScale,
	}
}

// gridPixelFromPoint 把点反解回采样网格（gw×gh）的像素坐标。
//
// 与 makeProj/UnprojectBlend 严格互逆：x = dxImg·k，所以 dxImg = x/k。
// 返回 false 表示该点无法反解（深度非正或比例非法）。
func gridPixelFromPoint(p geometry.Vec3, proj geometry.UnprojectBlendParams, w, h, gw, gh int) (float64, float64, bool) {
	if w <= 0 || h <= 0 || gw <= 0 || gh <= 0 {
		return 0, 0, false
	}
	d := -float64(p.Z) // z = -d
	if d <= 0 {
		return 0, 0, false
	}
	kx := geometry.BlendPixelScale(proj, d, false)
	ky := geometry.BlendPixelScale(proj, d, true)
	if kx <= 0 || ky <= 0 {
		return 0, 0, false
	}
	// 反解出原图像素坐标（与反投影同一 +0.5 约定）。
	uImg := float64(p.X)/kx + proj.CX
	vImg := proj.CY - float64(p.Y)/ky
	// 原图像素 → 工作网格像素。
	return uImg*float64(gw)/float64(w) - 0.5, vImg*float64(gh)/float64(h) - 0.5, true
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
