package pipeline

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"math"
	"testing"

	"illust3d/internal/cloud"
	"illust3d/internal/geometry"
)

// uniformImage 构造一张纯色 RGB 图。
func uniformImage(w, h int, c color.RGBA) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{}, draw.Src)
	return img
}

func decodeResult(t *testing.T, res *Result) *cloud.Cloud {
	t.Helper()
	c, err := cloud.Decode(res.ILC1)
	if err != nil {
		t.Fatalf("Decode 返回的 ILC1: %v", err)
	}
	return c
}

func TestBuildGeometryAndColor(t *testing.T) {
	const w, h = 4, 3
	depth := make([]float32, w*h)
	for i := range depth {
		depth[i] = 2
	}
	img := uniformImage(8, 6, color.RGBA{R: 10, G: 20, B: 30, A: 255})

	res, err := Build(depth, w, h, img, Params{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if res.PointCount != w*h {
		t.Fatalf("点数 = %d，期望 %d", res.PointCount, w*h)
	}
	if res.DepthWidth != w || res.DepthHeight != h {
		t.Errorf("深度尺寸 = %dx%d", res.DepthWidth, res.DepthHeight)
	}
	if res.ImageWidth != 8 || res.ImageHeight != 6 {
		t.Errorf("图像尺寸 = %dx%d", res.ImageWidth, res.ImageHeight)
	}
	if res.VoxelSize != 0 || res.EdgeThresh != 0 {
		t.Errorf("平缓深度不应下采样/不设阈值：voxel=%v thresh=%v", res.VoxelSize, res.EdgeThresh)
	}
	// focal_factor=1 → focal = max(4,3) = 4；主点 (2, 1.5)。
	if res.Focal != 4 {
		t.Errorf("Focal = %v，期望 4", res.Focal)
	}
	c := decodeResult(t, res)
	for i := range c.Pos {
		if !approx(c.Pos[i].Z, 2) {
			t.Errorf("Pos[%d].Z = %v，期望 2", i, c.Pos[i].Z)
		}
		if c.Col[i] != (geometry.Color{R: 10, G: 20, B: 30}) {
			t.Errorf("Col[%d] = %+v，期望纯色 (10,20,30)", i, c.Col[i])
		}
	}
	// x=(x+0.5-2)*2/4 ∈ [-0.75, 0.75]，y=(1.5-(y+0.5))*2/4 ∈ [-0.5, 0.5]。
	if !approx(res.BBoxMin.X, -0.75) || !approx(res.BBoxMax.X, 0.75) {
		t.Errorf("X 包围盒 = [%v, %v]", res.BBoxMin.X, res.BBoxMax.X)
	}
	if !approx(res.BBoxMin.Y, -0.5) || !approx(res.BBoxMax.Y, 0.5) {
		t.Errorf("Y 包围盒 = [%v, %v]", res.BBoxMin.Y, res.BBoxMax.Y)
	}
	if !approx(res.BBoxMin.Z, 2) || !approx(res.BBoxMax.Z, 2) {
		t.Errorf("Z 包围盒 = [%v, %v]", res.BBoxMin.Z, res.BBoxMax.Z)
	}
}

// TestBuildFocalFactorIsArtistic 验证 focal_factor 只缩放 x/y（焦距越大点云越"薄"）。
func TestBuildFocalFactorIsArtistic(t *testing.T) {
	const w, h = 4, 3
	depth := make([]float32, w*h)
	for i := range depth {
		depth[i] = 2
	}
	img := uniformImage(4, 3, color.RGBA{R: 1, G: 2, B: 3, A: 255})

	wide, err := Build(depth, w, h, img, Params{FocalFactor: 1})
	if err != nil {
		t.Fatalf("Build(1): %v", err)
	}
	tele, err := Build(depth, w, h, img, Params{FocalFactor: 2})
	if err != nil {
		t.Fatalf("Build(2): %v", err)
	}
	if tele.Focal != 2*wide.Focal {
		t.Errorf("焦距未按系数缩放：%v vs %v", tele.Focal, wide.Focal)
	}
	// x 方向范围应减半（y 同理），z 不变。
	wWidth := wide.BBoxMax.X - wide.BBoxMin.X
	tWidth := tele.BBoxMax.X - tele.BBoxMin.X
	if !approx(tWidth, wWidth/2) {
		t.Errorf("长焦 X 宽度 = %v，期望 %v（广角 %v）", tWidth, wWidth/2, wWidth)
	}
	if !approx(tele.BBoxMin.Z, 2) || !approx(tele.BBoxMax.Z, 2) {
		t.Errorf("焦距不应影响 z：[%v,%v]", tele.BBoxMin.Z, tele.BBoxMax.Z)
	}
}

func TestBuildDropsEdgePixels(t *testing.T) {
	// 1x3 深度，右侧跳变 4；阈值 0.5 → 跳变两侧（第 1、2 列）都丢，只剩第 0 列。
	res, err := Build([]float32{1, 1, 5}, 3, 1, uniformImage(3, 1, color.RGBA{}), Params{EdgeThresh: 0.5})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if res.PointCount != 1 {
		t.Errorf("点数 = %d，期望 1（边界两侧被丢）", res.PointCount)
	}
	if res.DroppedPixels != 2 {
		t.Errorf("DroppedPixels = %d，期望 2", res.DroppedPixels)
	}
	if res.EdgeThresh != 0.5 {
		t.Errorf("EdgeThresh = %v，期望 0.5", res.EdgeThresh)
	}
}

func TestBuildAutoEdgeThresh(t *testing.T) {
	// 相邻差恒为 1，p99 = 1；过滤器用严格大于，所以一个都不丢。
	res, err := Build([]float32{1, 2, 3, 4}, 4, 1, uniformImage(4, 1, color.RGBA{}), Params{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if res.EdgeThresh != 1 {
		t.Errorf("自动 EdgeThresh = %v，期望 1（相邻差 p99）", res.EdgeThresh)
	}
	if res.PointCount != 4 {
		t.Errorf("点数 = %d，期望 4", res.PointCount)
	}
}

func TestBuildAutoDownsample(t *testing.T) {
	const w, h = 100, 100
	depth := make([]float32, w*h)
	for i := range depth {
		depth[i] = 1 + float32(i)*0.001
	}
	// 显式给一个很大的 edge_thresh，隔离出"自动下采样"这一条路径（自动阈值本身另有测试）。
	res, err := Build(depth, w, h, uniformImage(w, h, color.RGBA{R: 9, G: 9, B: 9, A: 255}), Params{MaxPoints: 1000, EdgeThresh: 100})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if res.PointCount > 1000 || res.PointCount < 500 {
		t.Errorf("点数 = %d，期望落在 [500, 1000]（自动体素应接近但不超过上限）", res.PointCount)
	}
	if res.VoxelSize <= 0 {
		t.Errorf("自动下采样应报告体素边长，实际 %v", res.VoxelSize)
	}
	if res.DroppedPixels != 0 {
		t.Errorf("显式大阈值不应丢像素，实际 %d", res.DroppedPixels)
	}
	c := decodeResult(t, res)
	if len(c.Pos) != res.PointCount {
		t.Errorf("ILC1 点数 %d != PointCount %d", len(c.Pos), res.PointCount)
	}
}

// TestBuildAutoDownsampleWithOutliers 是回归测试：深度图里的少量飞点会把包围盒撑大，
// 旧实现（按包围盒最长边猜初始体素）会在这种输入上把点云压到几百点甚至个位数。
func TestBuildAutoDownsampleWithOutliers(t *testing.T) {
	const w, h = 200, 200
	depth := make([]float32, w*h)
	for i := range depth {
		depth[i] = 1 + float32(i%17)*0.01
	}
	// 两个极端深度点：会显著撑大点云包围盒。
	depth[0] = 50
	depth[w*h-1] = 40

	res, err := Build(depth, w, h, uniformImage(w, h, color.RGBA{}), Params{MaxPoints: 20000, EdgeThresh: 100})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if res.PointCount < 5000 || res.PointCount > 20000 {
		t.Errorf("点数 = %d，期望落在 [5000, 20000]（不能被飞点撑大的 bbox 带偏）", res.PointCount)
	}
	if res.VoxelSize <= 0 {
		t.Errorf("应报告自动体素边长，实际 %v", res.VoxelSize)
	}
}

func TestBuildExplicitVoxelSize(t *testing.T) {
	const w, h = 10, 10
	depth := make([]float32, w*h)
	for i := range depth {
		depth[i] = 1 + float32(i%10)*0.1
	}
	img := uniformImage(w, h, color.RGBA{R: 5, G: 6, B: 7, A: 255})
	res, err := Build(depth, w, h, img, Params{VoxelSize: 0.5})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if res.VoxelSize != 0.5 {
		t.Errorf("VoxelSize = %v，期望 0.5", res.VoxelSize)
	}
	if res.PointCount >= w*h {
		t.Errorf("显式体素下采样后点数 %d 应少于 %d", res.PointCount, w*h)
	}
	if res.PointCount == 0 {
		t.Error("点数不应为 0")
	}
}

func TestBuildDeterministic(t *testing.T) {
	const w, h = 32, 24
	depth := make([]float32, w*h)
	for i := range depth {
		depth[i] = 1 + float32((i*7)%13)*0.05
	}
	img := uniformImage(w, h, color.RGBA{R: 200, G: 100, B: 50, A: 255})
	params := Params{FocalFactor: 1.3, MaxPoints: 500}
	a, err := Build(depth, w, h, img, params)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	b, err := Build(depth, w, h, img, params)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if string(a.ILC1) != string(b.ILC1) {
		t.Error("相同输入两次 Build 结果不一致")
	}
}

func TestBuildInvalidParams(t *testing.T) {
	img := uniformImage(2, 2, color.RGBA{})
	depth := []float32{1, 1, 1, 1}
	tests := []struct {
		name    string
		depth   []float32
		w, h    int
		img     image.Image
		params  Params
		wantErr error
	}{
		{"nil-image", depth, 2, 2, nil, Params{}, nil},
		{"short-depth", []float32{1}, 2, 2, img, Params{}, ErrInvalidDepth},
		{"zero-width", depth, 0, 2, img, Params{}, ErrInvalidDepth},
		{"focal-negative", depth, 2, 2, img, Params{FocalFactor: -1}, ErrInvalidDepth},
		{"focal-nan", depth, 2, 2, img, Params{FocalFactor: math.NaN()}, ErrInvalidDepth},
		{"voxel-negative", depth, 2, 2, img, Params{VoxelSize: -1}, ErrInvalidDepth},
		{"edge-negative", depth, 2, 2, img, Params{EdgeThresh: -1}, ErrInvalidDepth},
		{"all-invalid-depth", []float32{0, 0, -1, float32(math.NaN())}, 2, 2, img, Params{}, ErrNoPoints},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Build(tc.depth, tc.w, tc.h, tc.img, tc.params)
			if err == nil {
				t.Fatal("期望报错，实际 nil")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("错误 = %v，期望 errors.Is %v", err, tc.wantErr)
			}
		})
	}
}

// TestBuildMaxPointsClamp 验证极端 max_points 会被夹到允许区间。
func TestBuildMaxPointsClamp(t *testing.T) {
	p, err := (Params{MaxPoints: 1}).normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if p.MaxPoints != MinMaxPoints {
		t.Errorf("MaxPoints = %d，期望夹到 %d", p.MaxPoints, MinMaxPoints)
	}
	p, err = (Params{MaxPoints: MaxMaxPoints + 1}).normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if p.MaxPoints != MaxMaxPoints {
		t.Errorf("MaxPoints = %d，期望夹到 %d", p.MaxPoints, MaxMaxPoints)
	}
}

func approx(a, b float32) bool { return math.Abs(float64(a-b)) <= 1e-5 }
