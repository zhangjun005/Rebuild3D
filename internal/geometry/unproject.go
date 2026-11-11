// @kron:intent geometry/projection/orthographic
// @kron:intent geometry/projection/depth-scale
// @kron:intent geometry/projection/z-sign

package geometry

import "math"

// 反投影：深度图 → 点云。
//
// 相机约定（docs/selection.md §4.4，必须三端一致）：
//   - +x 向右、+y 向上、+z 指向观察者（右手系，three.js 默认朝向）；
//   - 相机站在 +Z 朝 -Z 看；
//   - **单目深度 d 是距离，d 越大越远**（DA3MONO 实测：主体 d≈0.48，背景 d≈0.86）。
//     所以反投影后 z = -d：主体 z 更接近 0（离相机更近），背景 z 更负（更远）。
//
// 两个函数都返回长度恰为 w*h 的切片，下标 = y*w+x（逐像素一一对应），
// 这样调用方可以同时用深度图、边缘掩码和点云按同一套下标操作。
// 深度无效（<=0、NaN、±Inf）的像素输出零值点 Vec3{}，调用方应结合
// FilterDepthEdges 或自行判断跳过。
//
// 焦距是艺术参数而不是标定值（docs/scope.md §3.3①）：
// fx 越小视角越广、点云越"厚"；fx 越大越接近浮雕/层叠。

// UnprojectPerspective 用针孔相机模型把深度图反投影成点云。
//
// 参数：
//   - depth: 长度至少 w*h 的深度图，单位任意（通常与 fx/fy 单位一致）；
//   - w, h:  图像宽高；
//   - fx, fy: 以像素为单位的焦距（x、y 方向分开给，允许非方形像素）；
//   - cx, cy: 主点像素坐标。
//
// 像素取中心坐标：u = x + 0.5，v = y + 0.5。变换为
//
//	x = (u - cx) * d / fx
//	y = (cy - v) * d / fy
//	z = -d          ← d 是距离，越大越远；z 为负（相机在 +Z）
//
// 返回 nil 表示参数非法（w/h <= 0、len(depth) < w*h、fx/fy 非有限或为 0）。
func UnprojectPerspective(depth []float32, w, h int, fx, fy, cx, cy float64) []Vec3 {
	if w <= 0 || h <= 0 || len(depth) < w*h {
		return nil
	}
	if !isFinite64(fx) || !isFinite64(fy) || fx == 0 || fy == 0 {
		return nil
	}
	if !isFinite64(cx) || !isFinite64(cy) {
		return nil
	}

	out := make([]Vec3, w*h)
	for y := 0; y < h; y++ {
		v := float64(y) + 0.5
		row := y * w
		for x := 0; x < w; x++ {
			d := depth[row+x]
			if !validDepth(d) {
				continue
			}
			u := float64(x) + 0.5
			dist := float64(d)
			out[row+x] = Vec3{
				X: float32((u - cx) * dist / fx),
				Y: float32((cy - v) * dist / fy),
				Z: float32(-dist), // 相机在 +Z 朝 -Z 看：距离越大 z 越负
			}
		}
	}
	return out
}

// UnprojectOrthographic 用正交投影把深度图反投影成点云。
//
// 参数：
//   - depth: 长度至少 w*h 的深度图；
//   - w, h:  图像宽高；
//   - scale: 每个像素对应的世界单位（x、y、z 同比例，见 docs/selection.md §4.4）；
//   - cx, cy: 主点像素坐标。
//
// 像素取中心坐标，变换为
//
//	x = (u - cx) * scale
//	y = (cy - v) * scale
//	z = -d * scale   ← d 是距离，越大越远；z 为负（相机在 +Z）
//
// 返回 nil 表示参数非法（同 UnprojectPerspective，且 scale 必须有限）。
func UnprojectOrthographic(depth []float32, w, h int, scale, cx, cy float64) []Vec3 {
	if w <= 0 || h <= 0 || len(depth) < w*h {
		return nil
	}
	if !isFinite64(scale) || !isFinite64(cx) || !isFinite64(cy) {
		return nil
	}

	out := make([]Vec3, w*h)
	for y := 0; y < h; y++ {
		v := float64(y) + 0.5
		row := y * w
		for x := 0; x < w; x++ {
			d := depth[row+x]
			if !validDepth(d) {
				continue
			}
			u := float64(x) + 0.5
			out[row+x] = Vec3{
				X: float32((u - cx) * scale),
				Y: float32((cy - v) * scale),
				Z: float32(-float64(d) * scale),
			}
		}
	}
	return out
}

// validDepth 报告深度值是否可用于反投影。
func validDepth(d float32) bool { return d > 0 && isFinite32(d) }

// UnprojectBlend 是「正交 ↔ 透视」可混合的反投影。
//
// Perspective = 0（默认）：纯正交，各深度层都是**与图像平行、等大的平面**，只有 z 不同。
// 这是插画 2.5D 想要的效果——转动时是层与层错动，而不是以视点为中心的放射状扇形。
//
// Perspective = 1：纯透视，等价于 UnprojectPerspective（x/y 随 d 放大 → 放射状）。
//
// 中间值在两者之间线性插值。数学上两者对 (u-cx) 都是线性的，所以可以合并成
//
//	k = (1-t)·orthoScale + t·(d/f)
//	x = (u - cx)·k,  y = (cy - v)·k',  z = -d
//
// 反解（把点投回像素）也就很简单：u = x/k + cx。
//
// orthoScale 一般取 dRef/f（dRef 是代表深度），这样正交与透视在 dRef 处重合，
// 切换时尺寸连续。
func UnprojectBlend(depth []float32, w, h int, blend UnprojectBlendParams) []Vec3 {
	if w <= 0 || h <= 0 || len(depth) < w*h {
		return nil
	}
	if !isFinite64(blend.FX) || !isFinite64(blend.FY) || blend.FX <= 0 || blend.FY <= 0 {
		return nil
	}
	if !isFinite64(blend.OrthoScale) || blend.OrthoScale <= 0 {
		return nil
	}
	t := blend.Perspective
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}

	out := make([]Vec3, w*h)
	omt := 1 - t
	for y := 0; y < h; y++ {
		v := float64(y) + 0.5
		row := y * w
		dy := blend.CY - v
		for x := 0; x < w; x++ {
			d := depth[row+x]
			if !validDepth(d) {
				continue
			}
			u := float64(x) + 0.5
			dx := u - blend.CX
			dd := float64(d)
			kx := omt*blend.OrthoScale + t*dd/blend.FX
			ky := omt*blend.OrthoScale + t*dd/blend.FY
			out[row+x] = Vec3{
				X: float32(dx * kx),
				Y: float32(dy * ky),
				Z: float32(-blend.RefDepth - (dd-blend.RefDepth)*depthScaleOf(blend)),
			}
		}
	}
	return out
}

// UnprojectBlendParams 是 UnprojectBlend 的参数。
type UnprojectBlendParams struct {
	// FX / FY 是透视焦距（像素）。
	FX, FY float64
	// CX / CY 是主点（像素）。
	CX, CY float64
	// OrthoScale 是正交模式下的世界单位/像素（一般取 dRef/焦距）。
	OrthoScale float64
	// Perspective 是混合权重：[0,1]，0=正交（层平行），1=透视（放射）。
	Perspective float64
	// RefDepth 是纵深压缩的参考深度（一般取深度中位数）。
	// 压缩以它为基准，保证正面视图不因压缩而整体平移。
	RefDepth float64
	// DepthScale 是 z 轴（垂直于图片方向）的压缩系数：
	//   z = -RefDepth - (d - RefDepth) · DepthScale
	// DepthScale=1 → z = -d（保持 docs/selection.md §4.4 的约定不变）；
	// DepthScale=0.3 → 厚度压到三成（更像原图、侧转空洞更小）；
	// DepthScale=0 → 完全平面（贴在 z = -RefDepth）。
	DepthScale float64
}

// depthScaleOf 返回有效的纵深压缩系数（默认 1 = 不压缩；负数按 0 处理）。
func depthScaleOf(blend UnprojectBlendParams) float64 {
	if blend.DepthScale < 0 {
		return 0
	}
	if blend.DepthScale == 0 {
		return 0
	}
	return blend.DepthScale
}

// BlendPixelScale 返回某个深度下「像素 → 世界」的横向比例 k（x/y 共用近似）。
//
// 用于把点投回像素（u = x/k + cx），以及把扫描间距换算成世界单位。
func BlendPixelScale(blend UnprojectBlendParams, d float64, fy bool) float64 {
	t := blend.Perspective
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	f := blend.FX
	if fy {
		f = blend.FY
	}
	return (1-t)*blend.OrthoScale + t*d/f
}

// isFinite64 报告 f 是否有限。
func isFinite64(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }
