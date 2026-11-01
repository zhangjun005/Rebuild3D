// Package geometry 提供点云几何的纯函数：反投影、深度边缘剔除、体素下采样、法线估计。
//
// 设计约定（与 docs/selection.md §3、docs/go-scope.md §1.④ 对齐）：
//   - 只依赖标准库，不引入数值库；
//   - 函数是纯函数：不读取全局状态，不修改调用方传入的切片；
//   - 相机坐标系：+x 向右、+y 向上、+z 指向观察者（右手系）；相机在 +Z 朝 -Z 看，
//     单目深度 d 是距离、越大越远，反投影后 z = -d（见 docs/selection.md §4.4）。
package geometry

import "math"

// Vec3 是单精度三维点/向量。ILC1 的 pos 段就是按 x,y,z 交错存储的 float32。
type Vec3 struct{ X, Y, Z float32 }

// Color 是 8 位 RGB 颜色，与 ILC1 的 col 段一一对应。
type Color struct{ R, G, B uint8 }

// Add 返回 v+o。
func (v Vec3) Add(o Vec3) Vec3 { return Vec3{v.X + o.X, v.Y + o.Y, v.Z + o.Z} }

// Sub 返回 v-o。
func (v Vec3) Sub(o Vec3) Vec3 { return Vec3{v.X - o.X, v.Y - o.Y, v.Z - o.Z} }

// Scale 返回 v*s。
func (v Vec3) Scale(s float32) Vec3 { return Vec3{v.X * s, v.Y * s, v.Z * s} }

// Dot 返回点积。
func (v Vec3) Dot(o Vec3) float32 { return v.X*o.X + v.Y*o.Y + v.Z*o.Z }

// Cross 返回叉积。
func (v Vec3) Cross(o Vec3) Vec3 {
	return Vec3{
		v.Y*o.Z - v.Z*o.Y,
		v.Z*o.X - v.X*o.Z,
		v.X*o.Y - v.Y*o.X,
	}
}

// Len 返回模长。
func (v Vec3) Len() float32 { return float32(math.Sqrt(float64(v.Dot(v)))) }

// Normalize 返回单位向量；零向量或非有限向量返回零向量。
func (v Vec3) Normalize() Vec3 {
	l := v.Len()
	if l == 0 || !isFinite32(l) {
		return Vec3{}
	}
	return v.Scale(1 / l)
}

// IsFinite 报告三个分量是否都是有限值（非 NaN、非 ±Inf）。
func (v Vec3) IsFinite() bool { return isFinite32(v.X) && isFinite32(v.Y) && isFinite32(v.Z) }

// isFinite32 报告 f 是否有限。
func isFinite32(f float32) bool { return !math.IsNaN(float64(f)) && !math.IsInf(float64(f), 0) }
