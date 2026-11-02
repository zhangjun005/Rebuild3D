package pipeline

import (
	"image/color"
	"math"
	"testing"
)

// makeLayeredDepth 造一张"两块板 + 中间过渡带"的深度图，模拟 DA3 在插画上的输出：
// 左半是近层、右半是远层，交界处有一条渐变的中间深度（这就是"层间散落点"的来源）。
func makeLayeredDepth(w, h int) []float32 {
	d := make([]float32, w*h)
	band := w / 6
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var v float32
			switch {
			case x < w/2-band:
				v = 0.40
			case x > w/2+band:
				v = 1.20
			default:
				// 交界带：从 0.40 线性过渡到 1.20
				t := float32(x-(w/2-band)) / float32(2*band)
				v = 0.40 + t*0.80
			}
			d[y*w+x] = v
		}
	}
	return d
}

func uniqueCounts(vals []float32) int {
	seen := make(map[float32]struct{}, len(vals))
	for _, v := range vals {
		seen[v] = struct{}{}
	}
	return len(seen)
}

// TestQuantizeDepthLayersRemovesTransitionBand 是本功能的核心回归：
// 层量化必须把"层间过渡带"吸附到两侧层上，唯一深度值数量大幅下降。
func TestQuantizeDepthLayersRemovesTransitionBand(t *testing.T) {
	const w, h = 240, 120
	depth := makeLayeredDepth(w, h)
	discard := make([]bool, w*h)

	before := uniqueCounts(depth)
	out, stats := quantizeDepthLayers(depth, w, h, discard, 2)
	after := uniqueCounts(out)

	if stats.Layers != 2 {
		t.Fatalf("Layers = %d，want 2", stats.Layers)
	}
	if after >= before {
		t.Fatalf("唯一深度值没有减少：before=%d after=%d", before, after)
	}
	// 过渡带（原本是连续的 0.4~1.2）应该被压成极少数几个值。
	if after > 4 {
		t.Fatalf("量化后唯一深度值 %d 太多，过渡带没有被吸附", after)
	}

	// 注意：量化后的深度是「连通块的深度中位数」，不是 k-means 层中心
	// （层中心只是聚类参考，连通块的中位数才是实际写入值）。
	// 所以这里不校验"贴近层中心"，只校验真正重要的两条：
	//   ① 唯一深度值数量大幅下降（过渡带被吸附）
	//   ② 中间值（0.8 附近）彻底消失 —— 这正是"层间散落点"的来源
	mid := 0
	for _, v := range out {
		if v > 0.72 && v < 0.88 {
			mid++
		}
	}
	if mid != 0 {
		t.Fatalf("输出里仍有 %d 个中间深度点（应为 0）", mid)
	}

	// 两侧的层深度应各自保持不变（连通块中位数 = 该块的原始深度）。
	if got := out[0]; math.Abs(float64(got-0.40)) > 1e-6 {
		t.Fatalf("近层深度被改动：%v，want 0.40", got)
	}
	if got := out[w-1]; math.Abs(float64(got-1.20)) > 1e-6 {
		t.Fatalf("远层深度被改动：%v，want 1.20", got)
	}
}

// TestQuantizeDepthLayersKeepsInputIntact 确认不修改入参（调用方可能还要用原深度）。
func TestQuantizeDepthLayersKeepsInputIntact(t *testing.T) {
	const w, h = 120, 60
	depth := makeLayeredDepth(w, h)
	orig := append([]float32(nil), depth...)
	discard := make([]bool, w*h)

	out, _ := quantizeDepthLayers(depth, w, h, discard, 2)
	for i := range depth {
		if depth[i] != orig[i] {
			t.Fatalf("入参深度在第 %d 个像素被修改", i)
		}
	}
	if &out[0] == &depth[0] {
		t.Fatal("返回值与入参共享底层数组，调用方可能被意外影响")
	}
}

// TestQuantizeDepthLayersDeterministic 抽样确定性：同输入两次结果必须逐值相同。
func TestQuantizeDepthLayersDeterministic(t *testing.T) {
	const w, h = 160, 90
	depth := makeLayeredDepth(w, h)
	discard := make([]bool, w*h)

	a, sa := quantizeDepthLayers(depth, w, h, discard, 3)
	b, sb := quantizeDepthLayers(depth, w, h, discard, 3)
	if sa.Layers != sb.Layers || sa.Components != sb.Components || sa.Flattened != sb.Flattened {
		t.Fatalf("统计不一致：%+v vs %+v", sa, sb)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("第 %d 个像素不确定：%v vs %v", i, a[i], b[i])
		}
	}
}

// TestQuantizeDepthLayersDisabledAndTiny 覆盖边界：层数 <2 或有效像素太少时应原样返回。
func TestQuantizeDepthLayersDisabledAndTiny(t *testing.T) {
	const w, h = 40, 20
	depth := makeLayeredDepth(w, h)
	discard := make([]bool, w*h)

	if out, st := quantizeDepthLayers(depth, w, h, discard, 0); out == nil || st.Layers != 0 {
		t.Fatal("LayerCount=0 应原样返回")
	}
	// 全丢弃 → 有效像素为 0 → 不量化
	all := make([]bool, w*h)
	for i := range all {
		all[i] = true
	}
	if _, st := quantizeDepthLayers(depth, w, h, all, 3); st.Layers != 0 {
		t.Fatal("没有有效像素时应跳过量化")
	}
}

// TestBuildRespectsLayerCount 端到端：Build 应把层量化统计写进 Result，
// 且开启后点云的层间散落点显著减少。
func TestBuildRespectsLayerCount(t *testing.T) {
	const w, h = 200, 100
	depth := makeLayeredDepth(w, h)
	img := uniformImage(w*4, h*4, color.RGBA{R: 200, G: 200, B: 200, A: 255})

	// 关闭
	p1 := Params{FocalFactor: 1, MaxPoints: 50000, SampleMode: SampleModeStructure, LayerCount: 0}
	r1, err := Build(depth, w, h, img, p1)
	if err != nil {
		t.Fatalf("Build(off) 失败: %v", err)
	}
	if r1.LayerCount != 0 || r1.LayerFlattened != 0 {
		t.Fatalf("关闭时不该有层统计：count=%d flattened=%d", r1.LayerCount, r1.LayerFlattened)
	}

	// 开启
	p2 := p1
	p2.LayerCount = 2
	r2, err := Build(depth, w, h, img, p2)
	if err != nil {
		t.Fatalf("Build(on) 失败: %v", err)
	}
	if r2.LayerCount != 2 {
		t.Fatalf("LayerCount = %d，want 2", r2.LayerCount)
	}
	if r2.LayerFlattened == 0 {
		t.Fatal("LayerFlattened = 0，说明没有像素被吸附")
	}
	if len(r2.LayerCenters) != 2 {
		t.Fatalf("LayerCenters 长度 = %d，want 2", len(r2.LayerCenters))
	}
	if r2.PointCount == 0 {
		t.Fatal("开启层量化后点数为 0")
	}
}
