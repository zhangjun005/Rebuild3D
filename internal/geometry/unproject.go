package geometry

import "math"

// 反投影：深度图 → 点云。
//
// 两个函数都返回长度恰为 w*h 的切片，下标 = y*w + x（逐像素一一对应），
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
//	z = d
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
			z := float64(d)
			out[row+x] = Vec3{
				X: float32((u - cx) * z / fx),
				Y: float32((cy - v) * z / fy),
				Z: float32(z),
			}
		}
	}
	return out
}

// UnprojectOrthographic 用正交投影把深度图反投影成点云。
//
// 参数：
//   - depth: 长度至少 w*h 的深度图（直接作为 z 坐标）；
//   - w, h:  图像宽高；
//   - scale: 每个像素对应的世界单位（x、y 同比例；scale 越大点云越"散"）；
//   - cx, cy: 主点像素坐标。
//
// 像素取中心坐标，变换为
//
//	x = (u - cx) * scale
//	y = (cy - v) * scale
//	z = d
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
				Z: d,
			}
		}
	}
	return out
}

// validDepth 报告深度值是否可用于反投影。
func validDepth(d float32) bool { return d > 0 && isFinite32(d) }

// isFinite64 报告 f 是否有限。
func isFinite64(f float64) bool { return !math.IsNaN(f) && !math.IsInf(f, 0) }
