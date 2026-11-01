package pipeline

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"math"
	"testing"
)

// lineArtImage 生成白底 + 黑线的"线稿"图：竖线 2px、横线 2px（模拟线上只有很少像素的情形）。
func lineArtImage(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{R: 255, G: 255, B: 255, A: 255}), image.Point{}, draw.Src)
	black := color.RGBA{R: 0, G: 0, B: 0, A: 255}
	for y := 0; y < h; y++ {
		for d := 0; d < 2; d++ {
			img.SetRGBA(w/3+d, y, black)
		}
	}
	for x := 0; x < w; x++ {
		for d := 0; d < 2; d++ {
			img.SetRGBA(x, h/2+d, black)
		}
	}
	return img
}

// TestStructureModeDepthSign 是 structure 模式的防回归**语义**测试。
//
// structure 模式自己算 x/y/z（不经过 geometry.UnprojectPerspective），所以 z 符号
// 必须单独保一个语义断言：中心深度小（主体）、外围深度大（背景）时，
// 输出点云里中心点的 z 必须**大于**外围点（z=-d，越接近 0 越近）。
func TestStructureModeDepthSign(t *testing.T) {
	const w, h = 64, 64
	depth := make([]float32, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx := float64(x) - float64(w-1)/2
			dy := float64(y) - float64(h-1)/2
			r := math.Hypot(dx, dy) / (float64(w-1) / 2) // 中心 0，四角 ≈1.41
			depth[y*w+x] = float32(0.5 + 0.3*r)          // 主体在中心、背景在外围
		}
	}
	res, err := Build(depth, w, h, uniformImage(w, h, color.RGBA{R: 128, G: 128, B: 128, A: 255}),
		Params{SampleMode: SampleModeStructure, MaxPoints: 4000})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	c := decodeResult(t, res)

	var centerSum, centerN, outerSum, outerN float64
	for _, p := range c.Pos {
		if p.Z >= 0 {
			t.Fatalf("z 必须为负（相机在 +Z、深度沿 -Z），实际 %v", p.Z)
		}
		// 用世界坐标 xy 半径分中心/外围（世界坐标尺度 ±0.5 左右）。
		r := math.Hypot(float64(p.X), float64(p.Y))
		switch {
		case r < 0.1:
			centerSum += float64(p.Z)
			centerN++
		case r > 0.3:
			outerSum += float64(p.Z)
			outerN++
		}
	}
	if centerN == 0 || outerN == 0 {
		t.Fatalf("分区样本不足：center=%d outer=%d", int(centerN), int(outerN))
	}
	centerMean := centerSum / centerN
	outerMean := outerSum / outerN
	t.Logf("structure 模式：中心(主体)平均 z=%.4f（%d 点），外围(背景)平均 z=%.4f（%d 点）",
		centerMean, int(centerN), outerMean, int(outerN))
	if !(centerMean > outerMean) {
		t.Fatalf("远近关系反了：中心(主体) mean z=%.4f 应大于 外围(背景) mean z=%.4f", centerMean, outerMean)
	}
}

// TestWorkResFor 验证 workRes 与 max_points **解耦**：
// 自动值由原图长边决定（/2，夹 512..2048），显式值只被夹到 [256,4096]。
func TestWorkResFor(t *testing.T) {
	tests := []struct {
		name           string
		workRes        int
		imgW, imgH     int
		depthW, depthH int
		wantW, wantH   int
	}{
		// 4000px 原图 → 自动 2000（不夹）
		{"auto-from-image", 0, 4000, 4000, 504, 504, 2000, 2000},
		// 1024px 原图 → 512（顶到下限）
		{"auto-min-clamp", 0, 1024, 1024, 504, 504, 512, 512},
		// 8000px 原图 → 4000 → 夹到 2048
		{"auto-max-clamp", 0, 8000, 4000, 504, 252, 2048, 1024},
		// 显式 775（老基线）：与 max_points 无关，原样使用
		{"explicit-775", 775, 4000, 4000, 504, 504, 775, 775},
		// 显式 2048
		{"explicit-2048", 2048, 4000, 4000, 504, 504, 2048, 2048},
		// 显式过小/过大 → 夹到 [256, 4096]
		{"explicit-min", 100, 4000, 4000, 504, 504, MinWorkRes, MinWorkRes},
		{"explicit-max", 99999, 4000, 4000, 504, 504, MaxWorkRes, MaxWorkRes},
		// 竖图（原图 4000x6000 → 自动长边 3000 夹到 2048；深度 336x504，2:3）
		{"portrait", 0, 4000, 6000, 336, 504, 1365, 2048},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw, gh := workResFor(Params{WorkRes: tc.workRes}, tc.imgW, tc.imgH, tc.depthW, tc.depthH)
			if gw != tc.wantW || gh != tc.wantH {
				t.Errorf("workResFor = %dx%d，期望 %dx%d", gw, gh, tc.wantW, tc.wantH)
			}
		})
	}
}

// TestWorkResDecoupledFromMaxPoints 验证同一 max_points 下，改 work_res 只改变采样精度，
// 不改变点数（点数精确等于 max_points）。
func TestWorkResDecoupledFromMaxPoints(t *testing.T) {
	const w, h = 320, 320
	depth := make([]float32, w*h)
	for i := range depth {
		depth[i] = 1
	}
	img := lineArtImage(w, h)
	for _, workRes := range []int{512, 1024} {
		res, err := Build(depth, w, h, img, Params{
			SampleMode: SampleModeStructure,
			EdgeWeight: DefaultEdgeWeight,
			WorkRes:    workRes,
			MaxPoints:  20000,
		})
		if err != nil {
			t.Fatalf("Build(workRes=%d): %v", workRes, err)
		}
		if res.WorkRes != workRes {
			t.Errorf("workRes=%d 时 Result.WorkRes = %d", workRes, res.WorkRes)
		}
		if res.PointCount != 20000 {
			t.Errorf("workRes=%d 时点数 = %d，期望精确 20000（与 workRes 无关）", workRes, res.PointCount)
		}
	}
}

// TestDepthPixelIsLibraryDefault 锁定库层默认行为：Params{} → depthpixel（老行为）。
// HTTP API 层的默认是 structure，由 internal/api 显式传入。
func TestDepthPixelIsLibraryDefault(t *testing.T) {
	depth := []float32{1, 1, 1, 1}
	res, err := Build(depth, 2, 2, uniformImage(2, 2, color.RGBA{}), Params{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if res.SampleMode != SampleModeDepthPixel {
		t.Errorf("库层默认 SampleMode = %q，期望 %q", res.SampleMode, SampleModeDepthPixel)
	}
	if res.PointCount != 4 {
		t.Errorf("点数 = %d，期望 4（1 深度像素 1 点）", res.PointCount)
	}
	if res.WorkRes != 2 {
		t.Errorf("WorkRes = %d，期望 2", res.WorkRes)
	}
}

// TestStructureExactPointCount 验证 structure 模式精确取 max_points 个点、不做自动体素下采样。
func TestStructureExactPointCount(t *testing.T) {
	const w, h = 32, 32
	depth := make([]float32, w*h)
	for i := range depth {
		depth[i] = 1 + float32(i%8)*0.1
	}
	res, err := Build(depth, w, h, uniformImage(w, h, color.RGBA{R: 100, G: 100, B: 100, A: 255}),
		Params{SampleMode: SampleModeStructure, MaxPoints: 3000})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if res.SampleMode != SampleModeStructure {
		t.Errorf("SampleMode = %q", res.SampleMode)
	}
	if res.PointCount != 3000 {
		t.Errorf("点数 = %d，期望精确 3000", res.PointCount)
	}
	// 原图 32x32 → 自动 workRes = round(32/2)=16 → 顶到下限 512（不再是 256）。
	if res.WorkRes != AutoWorkResMin {
		t.Errorf("WorkRes = %d，期望自动下限 %d", res.WorkRes, AutoWorkResMin)
	}
	if res.VoxelSize != 0 {
		t.Errorf("结构模式默认不应做体素下采样，VoxelSize = %v", res.VoxelSize)
	}
	if res.EdgePointRatio != 0 {
		t.Errorf("纯色图无结构边缘，EdgePointRatio 应 ≈0，实际 %v", res.EdgePointRatio)
	}
	// 深度原值域 [1, 1.7] → z = -d 落在 [-1.7, -1]（相机在 +Z 朝 -Z 看）。
	if res.BBoxMin.Z < -1.701 || res.BBoxMax.Z > -0.999 {
		t.Errorf("Z 范围 = [%v, %v]，期望落在 [-1.7, -1]", res.BBoxMin.Z, res.BBoxMax.Z)
	}
}

// TestStructureEdgeBias 是核心行为测试：线稿图上 structure 模式的点必须**富集**在
// 强边缘上——既显著高于 depthpixel，也显著高于强边缘在工作网格里的面积占比。
func TestStructureEdgeBias(t *testing.T) {
	// 用 256² 的图：structure 的 workRes 显式给 256（等于 depthpixel 指标图的分辨率），
	// 两边 edgePointRatio 才在同一分辨率上可比。
	const w, h = 256, 256
	depth := make([]float32, w*h)
	for i := range depth {
		depth[i] = 1
	}
	img := lineArtImage(w, h)

	const maxPoints = 4000
	structure, err := Build(depth, w, h, img, Params{
		SampleMode: SampleModeStructure, MaxPoints: maxPoints,
		EdgeWeight: DefaultEdgeWeight, WorkRes: w,
	})
	if err != nil {
		t.Fatalf("Build(structure): %v", err)
	}
	dp, err := Build(depth, w, h, img, Params{SampleMode: SampleModeDepthPixel, MaxPoints: maxPoints})
	if err != nil {
		t.Fatalf("Build(depthpixel): %v", err)
	}
	if structure.PointCount != maxPoints {
		t.Errorf("structure 点数 = %d，期望 %d", structure.PointCount, maxPoints)
	}

	// 强边缘在工作网格里的面积占比（"均匀采样会得到的比例"）。
	gw, gh := workResFor(Params{WorkRes: structure.WorkRes}, w, h, w, h)
	gn := computeStructureMap(img, gw, gh)
	edgePixels := 0
	for _, v := range gn {
		if v > EdgeRatioThreshold {
			edgePixels++
		}
	}
	globalFrac := float64(edgePixels) / float64(len(gn))

	t.Logf("edgePointRatio: structure=%.4f depthpixel=%.4f；workGrid 强边缘面积占比=%.4f（workRes=%d）",
		structure.EdgePointRatio, dp.EdgePointRatio, globalFrac, structure.WorkRes)

	if structure.EdgePointRatio <= dp.EdgePointRatio {
		t.Fatalf("structure 的 edgePointRatio (%.4f) 未高于 depthpixel (%.4f)",
			structure.EdgePointRatio, dp.EdgePointRatio)
	}
	if globalFrac > 0 {
		ratio := structure.EdgePointRatio / globalFrac
		if ratio < 2 {
			t.Errorf("结构模式的点没有显著富集到强边缘：edgePointRatio=%.4f，边缘面积占比=%.4f，富集 %.2fx（期望 ≥2x）",
				structure.EdgePointRatio, globalFrac, ratio)
		}
	}
}

// TestStructureEdgeWeight 验证 kEdge 越大越偏向边缘（edgePointRatio 单调不减）。
// 库里 0 = 显式关闭边缘通道，所以这里比较 2 / 6 / 24（墨量固定为 0）。
func TestStructureEdgeWeight(t *testing.T) {
	const w, h = 64, 64
	depth := make([]float32, w*h)
	for i := range depth {
		depth[i] = 1
	}
	img := lineArtImage(w, h)
	ks := []float64{2, DefaultEdgeWeight, 24}
	ratios := make([]float64, 0, len(ks))
	for _, k := range ks {
		res, err := Build(depth, w, h, img, Params{
			SampleMode: SampleModeStructure, MaxPoints: 4000,
			EdgeWeight: k, InkWeight: 0,
		})
		if err != nil {
			t.Fatalf("Build(k=%v): %v", k, err)
		}
		ratios = append(ratios, res.EdgePointRatio)
	}
	t.Logf("edgePointRatio by kEdge: k=2 → %.4f, k=6 → %.4f, k=24 → %.4f", ratios[0], ratios[1], ratios[2])
	for i := 1; i < len(ratios); i++ {
		if ratios[i] < ratios[i-1] {
			t.Errorf("edgePointRatio 应随 kEdge 单调不减：%v", ratios)
			break
		}
	}
	if ratios[2] <= ratios[0] {
		t.Errorf("大 edge_weight 应更偏向边缘：k=2 %.4f vs k=24 %.4f", ratios[0], ratios[2])
	}
}

// TestInkWeightEnrichment 是墨量通道的核心行为测试：
// 深色细线（线体本身梯度≈0）上，kInk 应该把点拉到暗像素上，
// 即 darkEnrichment 随 kInk 单调上升，且纯墨量(kEdge=0, kInk=4) 明显 > 纯均匀(=1)。
func TestInkWeightEnrichment(t *testing.T) {
	const w, h = 64, 64
	depth := make([]float32, w*h)
	for i := range depth {
		depth[i] = 1
	}
	img := lineArtImage(w, h)
	type config struct {
		name        string
		kEdge, kInk float64
	}
	configs := []config{
		{"uniform", 0, 0},
		{"edge-only", DefaultEdgeWeight, 0},
		{"ink-only", 0, DefaultInkWeight},
		{"edge+ink", DefaultEdgeWeight, DefaultInkWeight},
	}
	got := make(map[string]float64, len(configs))
	for _, c := range configs {
		res, err := Build(depth, w, h, img, Params{
			SampleMode: SampleModeStructure, MaxPoints: 4000,
			EdgeWeight: c.kEdge, InkWeight: c.kInk,
		})
		if err != nil {
			t.Fatalf("Build(%s): %v", c.name, err)
		}
		got[c.name] = res.DarkEnrichment
		t.Logf("%-10s darkEnrichment=%.3f darkPointRatio=%.3f（darkPixelRatio=%.3f）",
			c.name, res.DarkEnrichment, res.DarkPointRatio, res.DarkPixelRatio)
	}
	if got["uniform"] < 0.9 || got["uniform"] > 1.1 {
		t.Errorf("均匀权重时 darkEnrichment 应 ≈1，实际 %.3f", got["uniform"])
	}
	if got["ink-only"] < 2 {
		t.Errorf("纯墨量采样的 darkEnrichment 应 ≥2，实际 %.3f", got["ink-only"])
	}
	if got["edge+ink"] < got["edge-only"] {
		t.Errorf("加墨量通道后暗像素富集不应下降：edge-only %.3f vs edge+ink %.3f",
			got["edge-only"], got["edge+ink"])
	}
	if got["edge+ink"] <= 1.5 {
		t.Errorf("edge+ink 的 darkEnrichment 应显著 >1，实际 %.3f", got["edge+ink"])
	}
}

// TestStructureDeterministic 验证同输入两次结果逐字节相同。
func TestStructureDeterministic(t *testing.T) {
	const w, h = 64, 64
	depth := make([]float32, w*h)
	for i := range depth {
		depth[i] = 1 + float32((i*13)%17)*0.01
	}
	img := lineArtImage(w, h)
	params := Params{
		SampleMode: SampleModeStructure, MaxPoints: 5000, FocalFactor: 1.2,
		EdgeWeight: DefaultEdgeWeight, InkWeight: DefaultInkWeight,
	}
	a, err := Build(depth, w, h, img, params)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	b, err := Build(depth, w, h, img, params)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if string(a.ILC1) != string(b.ILC1) {
		t.Errorf("structure 模式结果不可复现：%d vs %d 字节", len(a.ILC1), len(b.ILC1))
	}
	// 再验证不同 GOMAXPROCS 无关（structure 内部无并行的可变量，仍测一次保险）。
	if a.PointCount != 5000 || len(a.ILC1) != 8+15*5000 {
		t.Errorf("点数/字节数异常：%d 点 / %d 字节", a.PointCount, len(a.ILC1))
	}
}

func TestNormalizeSampleParams(t *testing.T) {
	// 非法 sample_mode
	if _, err := (Params{SampleMode: "bogus"}).normalize(); !errors.Is(err, ErrInvalidParams) {
		t.Errorf("非法 sample_mode 错误 = %v，期望 ErrInvalidParams", err)
	}
	// 库层：edge_weight/ink_weight 的 0 表示**显式关闭**（默认值由 API 层给）
	p, err := (Params{SampleMode: SampleModeStructure, EdgeWeight: 0, InkWeight: 0}).normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if p.EdgeWeight != 0 || p.InkWeight != 0 {
		t.Errorf("权重 = %v/%v，期望 0/0（显式关闭）", p.EdgeWeight, p.InkWeight)
	}
	// 超上限 → 夹到 Max*
	p, err = (Params{SampleMode: SampleModeStructure, EdgeWeight: 1000, InkWeight: 1000}).normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if p.EdgeWeight != MaxEdgeWeight || p.InkWeight != MaxInkWeight {
		t.Errorf("权重 = %v/%v，期望夹到 %v/%v", p.EdgeWeight, p.InkWeight, MaxEdgeWeight, MaxInkWeight)
	}
	// 负数 / NaN → 报错
	if _, err := (Params{SampleMode: SampleModeStructure, EdgeWeight: -1}).normalize(); !errors.Is(err, ErrInvalidParams) {
		t.Errorf("负 edge_weight 错误 = %v，期望 ErrInvalidParams", err)
	}
	if _, err := (Params{SampleMode: SampleModeStructure, InkWeight: -1}).normalize(); !errors.Is(err, ErrInvalidParams) {
		t.Errorf("负 ink_weight 错误 = %v，期望 ErrInvalidParams", err)
	}
	if _, err := (Params{SampleMode: SampleModeStructure, InkWeight: math.NaN()}).normalize(); !errors.Is(err, ErrInvalidParams) {
		t.Errorf("NaN ink_weight 错误 = %v，期望 ErrInvalidParams", err)
	}
	// depthpixel 模式不校验也不用这些权重
	if _, err := (Params{SampleMode: SampleModeDepthPixel, EdgeWeight: -5, InkWeight: -5}).normalize(); err != nil {
		t.Errorf("depthpixel 模式不应因权重报错: %v", err)
	}
	// work_res：负数报错；0=自动；过小/过大夹到 [256,4096]
	if _, err := (Params{SampleMode: SampleModeStructure, WorkRes: -1}).normalize(); !errors.Is(err, ErrInvalidParams) {
		t.Errorf("负 work_res 错误 = %v，期望 ErrInvalidParams", err)
	}
	p, err = (Params{SampleMode: SampleModeStructure, WorkRes: 0}).normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if p.WorkRes != 0 {
		t.Errorf("WorkRes = %d，期望 0（自动，由 buildStructure 决定）", p.WorkRes)
	}
	p, err = (Params{SampleMode: SampleModeStructure, WorkRes: 99999}).normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if p.WorkRes != MaxWorkRes {
		t.Errorf("WorkRes = %d，期望夹到 %d", p.WorkRes, MaxWorkRes)
	}
}

// TestStructureAllInvalidDepth 验证深度全无效时返回 ErrNoPoints（而不是空点云）。
func TestStructureAllInvalidDepth(t *testing.T) {
	depth := []float32{0, 0, float32(math.NaN()), -1}
	_, err := Build(depth, 2, 2, uniformImage(2, 2, color.RGBA{}), Params{SampleMode: SampleModeStructure, MaxPoints: 1000})
	if !errors.Is(err, ErrNoPoints) {
		t.Errorf("错误 = %v，期望 ErrNoPoints", err)
	}
}
