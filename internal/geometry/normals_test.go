package geometry

import (
	"math"
	"testing"
)

func TestEstimateNormalsPlane(t *testing.T) {
	// z=0 平面上的规则网格，法线应接近 ±Z。
	var pts []Vec3
	for x := -1.0; x <= 1.0+1e-9; x += 0.1 {
		for y := -1.0; y <= 1.0+1e-9; y += 0.1 {
			pts = append(pts, Vec3{X: float32(x), Y: float32(y), Z: 0})
		}
	}
	normals := EstimateNormals(pts, 0.25)
	if len(normals) != len(pts) {
		t.Fatalf("法线数量 = %d，期望 %d", len(normals), len(pts))
	}
	bad := 0
	for i, n := range normals {
		l := n.Len()
		if math.Abs(float64(l)-1) > 1e-4 {
			t.Fatalf("法线 %d 未归一化：%+v len=%v", i, n, l)
		}
		if math.Abs(float64(n.Z)) < 0.99 || math.Abs(float64(n.X)) > 0.05 || math.Abs(float64(n.Y)) > 0.05 {
			bad++
		}
	}
	if bad != 0 {
		t.Errorf("有 %d/%d 个点的法线不接近 ±Z", bad, len(pts))
	}
}

func TestEstimateNormalsTooFewNeighbors(t *testing.T) {
	pts := []Vec3{{X: 0, Y: 0, Z: 0}, {X: 1, Y: 0, Z: 0}}
	for i, n := range EstimateNormals(pts, 0.1) {
		if n != (Vec3{}) {
			t.Errorf("邻居不足的点 %d 应返回零向量，实际 %+v", i, n)
		}
	}
}

func TestEstimateNormalsInvalidInput(t *testing.T) {
	pts := []Vec3{
		{X: 0, Y: 0, Z: 0},
		{X: float32(math.NaN()), Y: 0, Z: 0},
		{X: 0.1, Y: 0.1, Z: 0},
		{X: 0.2, Y: 0, Z: 0.1},
	}
	// radius=0：全零输出但长度保持。
	zero := EstimateNormals(pts, 0)
	if len(zero) != len(pts) {
		t.Fatalf("长度 = %d，期望 %d", len(zero), len(pts))
	}
	for i, n := range zero {
		if n != (Vec3{}) {
			t.Errorf("radius=0 时点 %d 应为零向量，实际 %+v", i, n)
		}
	}
	// 非有限点不参与邻居查询，自身输出零向量。
	normals := EstimateNormals(pts, 1)
	if normals[1] != (Vec3{}) {
		t.Errorf("NaN 点应为零向量，实际 %+v", normals[1])
	}
	if got := EstimateNormals(nil, 1); len(got) != 0 {
		t.Errorf("空输入应返回空切片，实际 %v", got)
	}
}

// TestEstimateNormalsDeterministic 验证相同输入下结果完全一致。
func TestEstimateNormalsDeterministic(t *testing.T) {
	pts := make([]Vec3, 200)
	for i := range pts {
		pts[i] = Vec3{X: float32(i%10) * 0.1, Y: float32(i/10) * 0.1, Z: 0}
	}
	a := EstimateNormals(pts, 0.2)
	b := EstimateNormals(pts, 0.2)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("结果不确定 @%d: %+v vs %+v", i, a[i], b[i])
		}
	}
}
