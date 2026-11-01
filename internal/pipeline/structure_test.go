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

func TestWorkResFor(t *testing.T) {
	tests := []struct {
		name            string
		maxPoints, w, h int
		wantW, wantH    int
	}{
		// 默认 200000 → 长边 round(sqrt(600000)) = 775，方形深度 → 775×775
		{"default-square", DefaultMaxPoints, 504, 504, 775, 775},
		// 下限：max_points=1000 → sqrt(3000)≈55 → 夹到 256
		{"min-clamp", MinMaxPoints, 504, 504, 256, 256},
		// 上限：max_points=5M → sqrt(15M)≈3873 → 夹到 2048；横图 2:1
		{"max-clamp", MaxMaxPoints, 1000, 500, 2048, 1024},
		// 竖图（2:3）：长边给 h；775 * 336/504 = 516.7 → 517
		{"portrait", DefaultMaxPoints, 336, 504, 517, 775},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gw, gh := workResFor(tc.maxPoints, tc.w, tc.h)
			if gw != tc.wantW || gh != tc.wantH {
				t.Errorf("workResFor = %dx%d，期望 %dx%d", gw, gh, tc.wantW, tc.wantH)
			}
		})
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
	if res.WorkRes != 256 {
		t.Errorf("WorkRes = %d，期望 256", res.WorkRes)
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
	const w, h = 64, 64
	depth := make([]float32, w*h)
	for i := range depth {
		depth[i] = 1
	}
	img := lineArtImage(w, h)

	const maxPoints = 4000
	structure, err := Build(depth, w, h, img, Params{SampleMode: SampleModeStructure, MaxPoints: maxPoints})
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
	gw, gh := workResFor(maxPoints, w, h)
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

// TestStructureEdgeWeight 验证 k 越大越偏向边缘（edgePointRatio 单调不减）。
// 注意 EdgeWeight=0 表示"用默认 6"，所以这里比较 2 / 6 / 24。
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
		res, err := Build(depth, w, h, img, Params{SampleMode: SampleModeStructure, MaxPoints: 4000, EdgeWeight: k})
		if err != nil {
			t.Fatalf("Build(k=%v): %v", k, err)
		}
		ratios = append(ratios, res.EdgePointRatio)
	}
	t.Logf("edgePointRatio by k: k=2 → %.4f, k=6 → %.4f, k=24 → %.4f", ratios[0], ratios[1], ratios[2])
	for i := 1; i < len(ratios); i++ {
		if ratios[i] < ratios[i-1] {
			t.Errorf("edgePointRatio 应随 k 单调不减：%v", ratios)
			break
		}
	}
	if ratios[2] <= ratios[0] {
		t.Errorf("大 edge_weight 应更偏向边缘：k=2 %.4f vs k=24 %.4f", ratios[0], ratios[2])
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
	params := Params{SampleMode: SampleModeStructure, MaxPoints: 5000, FocalFactor: 1.2}
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
	// edge_weight 为 0 → 默认 6
	p, err := (Params{SampleMode: SampleModeStructure, EdgeWeight: 0}).normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if p.EdgeWeight != DefaultEdgeWeight {
		t.Errorf("EdgeWeight = %v，期望默认 %v", p.EdgeWeight, DefaultEdgeWeight)
	}
	// edge_weight 超上限 → 夹到 MaxEdgeWeight
	p, err = (Params{SampleMode: SampleModeStructure, EdgeWeight: 1000}).normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if p.EdgeWeight != MaxEdgeWeight {
		t.Errorf("EdgeWeight = %v，期望夹到 %v", p.EdgeWeight, MaxEdgeWeight)
	}
	// 负数 → 报错
	if _, err := (Params{SampleMode: SampleModeStructure, EdgeWeight: -1}).normalize(); !errors.Is(err, ErrInvalidParams) {
		t.Errorf("负 edge_weight 错误 = %v，期望 ErrInvalidParams", err)
	}
	// depthpixel 模式不校验也不用 edge_weight
	if _, err := (Params{SampleMode: SampleModeDepthPixel, EdgeWeight: -5}).normalize(); err != nil {
		t.Errorf("depthpixel 模式不应因 edge_weight 报错: %v", err)
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
