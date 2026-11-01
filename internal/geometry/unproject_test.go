package geometry

import (
	"math"
	"testing"
)

// approx 判断两个 float32 是否在容差内相等。
func approx(a, b float32) bool { return math.Abs(float64(a-b)) <= 1e-5 }

func vecEqual(a, b Vec3) bool { return approx(a.X, b.X) && approx(a.Y, b.Y) && approx(a.Z, b.Z) }

func TestUnprojectPerspective(t *testing.T) {
	// 2x2 图，fx=fy=2，主点 (1,1)，深度逐像素增大。
	depth := []float32{1, 2, 3, 4}
	got := UnprojectPerspective(depth, 2, 2, 2, 2, 1, 1)
	want := []Vec3{
		{X: -0.25, Y: 0.25, Z: 1},  // (0.5,0.5) d=1
		{X: 0.5, Y: 0.5, Z: 2},     // (1.5,0.5) d=2
		{X: -0.75, Y: -0.75, Z: 3}, // (0.5,1.5) d=3
		{X: 1, Y: -1, Z: 4},        // (1.5,1.5) d=4
	}
	if len(got) != len(want) {
		t.Fatalf("点数 = %d，期望 %d", len(got), len(want))
	}
	for i := range want {
		if !vecEqual(got[i], want[i]) {
			t.Errorf("[%d] = %+v，期望 %+v", i, got[i], want[i])
		}
	}
}

func TestUnprojectPerspectiveNonSquareFocal(t *testing.T) {
	// fx=4, fy=2 应分别作用在 x/y 上；y 轴在图像里向下，世界坐标里向上（翻号）。
	got := UnprojectPerspective([]float32{4}, 1, 1, 4, 2, 0, 0)
	want := Vec3{X: 0.5, Y: -1, Z: 4}
	if len(got) != 1 || !vecEqual(got[0], want) {
		t.Fatalf("got %+v，期望 %+v", got, want)
	}
}

func TestUnprojectInvalidDepth(t *testing.T) {
	nan := float32(math.NaN())
	inf := float32(math.Inf(1))
	depth := []float32{1, 0, -1, nan, inf, 2}
	got := UnprojectPerspective(depth, 3, 2, 1, 1, 0, 0)
	if len(got) != 6 {
		t.Fatalf("点数 = %d，期望 6（逐像素一一对应）", len(got))
	}
	for i := 1; i < 5; i++ {
		if got[i] != (Vec3{}) {
			t.Errorf("无效深度像素 [%d] = %+v，期望零值点", i, got[i])
		}
	}
	if got[0] == (Vec3{}) || got[5] == (Vec3{}) {
		t.Errorf("有效深度像素不应为零值点：%+v %+v", got[0], got[5])
	}
}

func TestUnprojectParamErrors(t *testing.T) {
	tests := []struct {
		name   string
		depth  []float32
		w, h   int
		fx, fy float64
	}{
		{"zero-width", []float32{1}, 0, 1, 1, 1},
		{"negative-height", []float32{1}, 1, -1, 1, 1},
		{"short-depth", []float32{1, 2}, 2, 2, 1, 1},
		{"zero-fx", []float32{1}, 1, 1, 0, 1},
		{"nan-fy", []float32{1}, 1, 1, 1, math.NaN()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := UnprojectPerspective(tc.depth, tc.w, tc.h, tc.fx, tc.fy, 0, 0); got != nil {
				t.Errorf("期望 nil，实际 %v", got)
			}
		})
	}
	if got := UnprojectPerspective([]float32{1}, 1, 1, 1, 1, math.Inf(1), 0); got != nil {
		t.Errorf("主点非有限时期望 nil，实际 %v", got)
	}
}

func TestUnprojectOrthographic(t *testing.T) {
	depth := []float32{1, 2, 3, 4}
	got := UnprojectOrthographic(depth, 2, 2, 0.5, 1, 1)
	// docs/selection.md §4.4：正交投影 z = d * scale。
	want := []Vec3{
		{X: -0.25, Y: 0.25, Z: 0.5},
		{X: 0.25, Y: 0.25, Z: 1},
		{X: -0.25, Y: -0.25, Z: 1.5},
		{X: 0.25, Y: -0.25, Z: 2},
	}
	if len(got) != len(want) {
		t.Fatalf("点数 = %d，期望 %d", len(got), len(want))
	}
	for i := range want {
		if !vecEqual(got[i], want[i]) {
			t.Errorf("[%d] = %+v，期望 %+v", i, got[i], want[i])
		}
	}

	// scale=1 时 z 就是深度；x/y 偏移 = 半像素 * scale。
	doubled := UnprojectOrthographic([]float32{1}, 1, 1, 1, 0, 0)
	if want := (Vec3{X: 0.5, Y: -0.5, Z: 1}); !vecEqual(doubled[0], want) {
		t.Errorf("scale 测试 got %+v，期望 %+v", doubled[0], want)
	}
}

func TestUnprojectOrthographicParamErrors(t *testing.T) {
	if got := UnprojectOrthographic([]float32{1}, 0, 1, 1, 0, 0); got != nil {
		t.Errorf("零宽期望 nil，实际 %v", got)
	}
	if got := UnprojectOrthographic([]float32{1, 2}, 2, 2, 1, 0, 0); got != nil {
		t.Errorf("深度不足期望 nil，实际 %v", got)
	}
	if got := UnprojectOrthographic([]float32{1}, 1, 1, math.NaN(), 0, 0); got != nil {
		t.Errorf("NaN scale 期望 nil，实际 %v", got)
	}
}
