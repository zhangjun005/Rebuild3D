package geometry

import (
	"math"
	"math/rand"
	"runtime"
	"testing"
)

func TestVoxelDownsampleBasic(t *testing.T) {
	// 前 4 点落在格子 (0,0,0)，第 5 点落在格子 (1,0,0)。
	pts := []Vec3{
		{X: 0.1, Y: 0.1, Z: 0.1},
		{X: 0.2, Y: 0.2, Z: 0.2},
		{X: 0.3, Y: 0.3, Z: 0.3},
		{X: 0.4, Y: 0.4, Z: 0.4},
		{X: 1.1, Y: 0.1, Z: 0.1},
	}
	cols := []Color{
		{R: 0, G: 0, B: 0},
		{R: 100, G: 50, B: 10},
		{R: 200, G: 100, B: 20},
		{R: 255, G: 255, B: 255},
		{R: 10, G: 20, B: 30},
	}

	gotPts, gotCols := VoxelDownsample(pts, cols, 1)
	if len(gotPts) != 2 || len(gotCols) != 2 {
		t.Fatalf("点数 = %d/%d，期望 2/2", len(gotPts), len(gotCols))
	}
	// 格子 (0,0,0)：位置均值 0.25，颜色均值四舍五入 (139,101,71)。
	wantP0 := Vec3{X: 0.25, Y: 0.25, Z: 0.25}
	if !vecEqual(gotPts[0], wantP0) {
		t.Errorf("格子0 代表点 = %+v，期望 %+v", gotPts[0], wantP0)
	}
	if want := (Color{R: 139, G: 101, B: 71}); gotCols[0] != want {
		t.Errorf("格子0 颜色 = %+v，期望 %+v", gotCols[0], want)
	}
	// 格子 (1,0,0)：单点原样保留。
	if !vecEqual(gotPts[1], pts[4]) {
		t.Errorf("格子1 代表点 = %+v，期望 %+v", gotPts[1], pts[4])
	}
	if gotCols[1] != cols[4] {
		t.Errorf("格子1 颜色 = %+v，期望 %+v", gotCols[1], cols[4])
	}
}

func TestVoxelDownsampleOrder(t *testing.T) {
	// 输出必须按格子坐标字典序：Y 为 -1 的格子排在 Y=0 之前。
	pts := []Vec3{
		{X: 0.5, Y: 0.5, Z: 0},
		{X: 0.5, Y: -0.5, Z: 0},
		{X: -0.5, Y: 0.5, Z: 0},
		{X: 1.5, Y: 0.5, Z: 0},
	}
	gotPts, _ := VoxelDownsample(pts, nil, 1)
	wantKeys := []Vec3{
		{X: -0.5, Y: 0.5, Z: 0}, // 格子 (-1,0,0)
		{X: 0.5, Y: -0.5, Z: 0}, // 格子 (0,-1,0)
		{X: 0.5, Y: 0.5, Z: 0},  // 格子 (0,0,0)
		{X: 1.5, Y: 0.5, Z: 0},  // 格子 (1,0,0)
	}
	if len(gotPts) != len(wantKeys) {
		t.Fatalf("点数 = %d，期望 %d", len(gotPts), len(wantKeys))
	}
	for i := range wantKeys {
		if !vecEqual(gotPts[i], wantKeys[i]) {
			t.Errorf("第 %d 个点 = %+v，期望 %+v", i, gotPts[i], wantKeys[i])
		}
	}
}

// TestVoxelDownsampleDeterministic 验证不同并行度下结果逐位一致，且输出有序。
func TestVoxelDownsampleDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	const n = 20000
	pts := make([]Vec3, n)
	cols := make([]Color, n)
	for i := range pts {
		pts[i] = Vec3{
			X: float32(rng.Float64()*10 - 5),
			Y: float32(rng.Float64()*10 - 5),
			Z: float32(rng.Float64()*10 - 5),
		}
		cols[i] = Color{R: uint8(rng.Intn(256)), G: uint8(rng.Intn(256)), B: uint8(rng.Intn(256))}
	}

	old := runtime.GOMAXPROCS(1)
	p1, c1 := VoxelDownsample(pts, cols, 0.5)
	runtime.GOMAXPROCS(8)
	defer runtime.GOMAXPROCS(old)
	p8, c8 := VoxelDownsample(pts, cols, 0.5)

	if len(p1) != len(p8) || len(p1) == 0 {
		t.Fatalf("输出点数不一致：%d vs %d", len(p1), len(p8))
	}
	for i := range p1 {
		if p1[i] != p8[i] || c1[i] != c8[i] {
			t.Fatalf("不同并行度结果不一致 @%d: %+v/%+v vs %+v/%+v", i, p1[i], c1[i], p8[i], c8[i])
		}
	}
	// 再跑一次确认幂等。
	pAgain, _ := VoxelDownsample(pts, cols, 0.5)
	for i := range p1 {
		if p1[i] != pAgain[i] {
			t.Fatalf("重复调用结果不一致 @%d", i)
		}
	}
}

func TestVoxelDownsampleNoop(t *testing.T) {
	pts := []Vec3{{X: 1, Y: 2, Z: 3}}
	cols := []Color{{R: 4, G: 5, B: 6}}
	gotPts, gotCols := VoxelDownsample(pts, cols, 0)
	if !vecEqual(gotPts[0], pts[0]) || gotCols[0] != cols[0] {
		t.Fatalf("voxelSize=0 应原样返回：%+v/%+v", gotPts, gotCols)
	}
	// 返回的是拷贝，改结果不能影响输入。
	gotPts[0].X = 99
	if pts[0].X != 1 {
		t.Error("voxelSize=0 时返回了共享切片")
	}
}

func TestVoxelDownsampleEdgeCases(t *testing.T) {
	if pts, cols := VoxelDownsample(nil, nil, 1); pts != nil || cols != nil {
		t.Errorf("空输入应返回 nil, nil，实际 %v/%v", pts, cols)
	}
	// 非有限位置被丢弃；仅剩 1 个有效点。
	pts := []Vec3{
		{X: float32(math.NaN()), Y: 0, Z: 0},
		{X: 0, Y: float32(math.Inf(1)), Z: 0},
		{X: 0.5, Y: 0.5, Z: 0.5},
	}
	got, _ := VoxelDownsample(pts, nil, 1)
	if len(got) != 1 || !vecEqual(got[0], pts[2]) {
		t.Fatalf("非有限点处理错误：%+v", got)
	}
	// 颜色比位置短：缺失颜色按 0 处理，不 panic。
	gotPts, gotCols := VoxelDownsample([]Vec3{{X: 1}, {X: 2}}, []Color{{R: 10}}, 1)
	if len(gotPts) != 2 || len(gotCols) != 2 {
		t.Fatalf("颜色缺失时点数 = %d/%d，期望 2/2", len(gotPts), len(gotCols))
	}
}
