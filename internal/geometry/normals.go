package geometry

import "math"

// EstimateNormals 用局部邻域 PCA（协方差最小特征向量）估计每个点的法线。
//
// 邻域定义：以 radius 为半径的球内所有点（含自身），用均匀网格哈希加速查询。
// 返回与 pts 一一对应的单位法线；邻居少于 3 个、位置非有限、或邻域退化的点
// 返回零向量。法线朝向未统一（渲染时可结合视线方向翻转），这是有意的：
// PCA 无法在不引入额外约定的前提下决定内外，强制统一反而会引入错误。
//
// radius <= 0 或非有限时返回全零法线（长度仍等于 len(pts)）。
// 函数只读 pts，纯函数、可并发调用。
func EstimateNormals(pts []Vec3, radius float32) []Vec3 {
	out := make([]Vec3, len(pts))
	if len(pts) == 0 {
		return out
	}
	if !isFinite32(radius) || radius <= 0 {
		return out
	}

	// 均匀网格：格子边长 = radius，查询时检查 27 个相邻格子即可覆盖半径内所有点。
	grid := make(map[cellKey][]int32, len(pts)/4+1)
	for i, p := range pts {
		if !p.IsFinite() {
			continue
		}
		k := voxelKey(p, radius)
		grid[k] = append(grid[k], int32(i))
	}

	r2 := float64(radius) * float64(radius)
	for i, p := range pts {
		if !p.IsFinite() {
			continue
		}
		base := voxelKey(p, radius)
		// 第一遍：求质心。
		var n, sx, sy, sz float64
		for dx := int32(-1); dx <= 1; dx++ {
			for dy := int32(-1); dy <= 1; dy++ {
				for dz := int32(-1); dz <= 1; dz++ {
					for _, j := range grid[cellKey{base.X + dx, base.Y + dy, base.Z + dz}] {
						q := pts[j]
						ex := float64(q.X) - float64(p.X)
						ey := float64(q.Y) - float64(p.Y)
						ez := float64(q.Z) - float64(p.Z)
						if ex*ex+ey*ey+ez*ez > r2 {
							continue
						}
						n++
						sx += float64(q.X)
						sy += float64(q.Y)
						sz += float64(q.Z)
					}
				}
			}
		}
		if n < 3 {
			continue
		}
		mx, my, mz := sx/n, sy/n, sz/n

		// 第二遍：协方差矩阵。
		var xx, xy, xz, yy, yz, zz float64
		for dx := int32(-1); dx <= 1; dx++ {
			for dy := int32(-1); dy <= 1; dy++ {
				for dz := int32(-1); dz <= 1; dz++ {
					for _, j := range grid[cellKey{base.X + dx, base.Y + dy, base.Z + dz}] {
						q := pts[j]
						ex := float64(q.X) - float64(p.X)
						ey := float64(q.Y) - float64(p.Y)
						ez := float64(q.Z) - float64(p.Z)
						if ex*ex+ey*ey+ez*ez > r2 {
							continue
						}
						cx := float64(q.X) - mx
						cy := float64(q.Y) - my
						cz := float64(q.Z) - mz
						xx += cx * cx
						xy += cx * cy
						xz += cx * cz
						yy += cy * cy
						yz += cy * cz
						zz += cz * cz
					}
				}
			}
		}
		out[i] = smallestEigenvector(xx, xy, xz, yy, yz, zz).Normalize()
	}
	return out
}

// smallestEigenvector 用循环 Jacobi 求 3x3 对称矩阵最小特征值对应的特征向量。
// 输入未归一化也正确（特征向量与整体缩放无关）。
func smallestEigenvector(xx, xy, xz, yy, yz, zz float64) Vec3 {
	a := [3][3]float64{
		{xx, xy, xz},
		{xy, yy, yz},
		{xz, yz, zz},
	}
	v := [3][3]float64{
		{1, 0, 0},
		{0, 1, 0},
		{0, 0, 1},
	}
	for iter := 0; iter < 50; iter++ {
		p, q := 0, 1
		maxOff := math.Abs(a[0][1])
		if f := math.Abs(a[0][2]); f > maxOff {
			p, q, maxOff = 0, 2, f
		}
		if f := math.Abs(a[1][2]); f > maxOff {
			p, q, maxOff = 1, 2, f
		}
		if maxOff < 1e-12 {
			break
		}

		app, aqq, apq := a[p][p], a[q][q], a[p][q]
		theta := (aqq - app) / (2 * apq)
		t := math.Copysign(1, theta) / (math.Abs(theta) + math.Sqrt(theta*theta+1))
		c := 1 / math.Sqrt(t*t+1)
		s := t * c

		for k := 0; k < 3; k++ {
			if k == p || k == q {
				continue
			}
			akp, akq := a[k][p], a[k][q]
			a[k][p] = c*akp - s*akq
			a[p][k] = a[k][p]
			a[k][q] = s*akp + c*akq
			a[q][k] = a[k][q]
		}
		a[p][p] = c*c*app - 2*c*s*apq + s*s*aqq
		a[q][q] = s*s*app + 2*c*s*apq + c*c*aqq
		a[p][q] = 0
		a[q][p] = 0

		for k := 0; k < 3; k++ {
			vkp, vkq := v[k][p], v[k][q]
			v[k][p] = c*vkp - s*vkq
			v[k][q] = s*vkp + c*vkq
		}
	}

	k := 0
	if a[1][1] < a[k][k] {
		k = 1
	}
	if a[2][2] < a[k][k] {
		k = 2
	}
	return Vec3{X: float32(v[0][k]), Y: float32(v[1][k]), Z: float32(v[2][k])}
}
