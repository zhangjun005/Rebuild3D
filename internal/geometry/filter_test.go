package geometry

import (
	"math"
	"testing"
)

func TestFilterDepthEdges(t *testing.T) {
	tests := []struct {
		name   string
		depth  []float32
		w, h   int
		thresh float32
		want   []bool
	}{
		{
			// 平缓平面：无跳变，全部保留。
			name:  "flat-plane",
			depth: []float32{1, 1, 1, 1, 1, 1},
			w:     3, h: 2,
			thresh: 0.5,
			want:   []bool{false, false, false, false, false, false},
		},
		{
			// 3x2，第 2 列与第 1 列差 4 > 1：两列都被标记，第 0 列保留。
			name:  "vertical-step",
			depth: []float32{1, 1, 5, 1, 1, 5},
			w:     3, h: 2,
			thresh: 1,
			want:   []bool{false, true, true, false, true, true},
		},
		{
			// 2x2，第 2 行与第 1 行差 3 > 1：两行都被标记。
			name:  "horizontal-step",
			depth: []float32{1, 1, 4, 4},
			w:     2, h: 2,
			thresh: 1,
			want:   []bool{true, true, true, true},
		},
		{
			// 深度差恰好等于阈值不算跳变（严格大于才丢）。
			name:  "threshold-equal",
			depth: []float32{1, 2},
			w:     2, h: 1,
			thresh: 1,
			want:   []bool{false, false},
		},
		{
			// 无效深度（0 / 负数 / NaN）直接丢，并连带标记相邻像素。
			name:  "invalid-depth",
			depth: []float32{1, 0, -2, float32(math.NaN())},
			w:     4, h: 1,
			thresh: 0.5,
			want:   []bool{true, true, true, true},
		},
		{
			// 负阈值按 0 处理：任何非零差异都算跳变。
			name:  "negative-threshold",
			depth: []float32{1, 1.01},
			w:     2, h: 1,
			thresh: -1,
			want:   []bool{true, true},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FilterDepthEdges(tc.depth, tc.w, tc.h, tc.thresh)
			if len(got) != len(tc.want) {
				t.Fatalf("掩码长度 = %d，期望 %d", len(got), len(tc.want))
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("掩码[%d] = %v，期望 %v（完整：%v）", i, got[i], tc.want[i], got)
					break
				}
			}
		})
	}
}

func TestFilterDepthEdgesParamErrors(t *testing.T) {
	if got := FilterDepthEdges([]float32{1}, 0, 1, 1); got != nil {
		t.Errorf("零宽期望 nil，实际 %v", got)
	}
	if got := FilterDepthEdges([]float32{1, 2}, 2, 2, 1); got != nil {
		t.Errorf("深度不足期望 nil，实际 %v", got)
	}
}
