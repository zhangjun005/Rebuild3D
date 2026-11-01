package geometry

import (
	"math"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
)

// 体素下采样的分块大小。分块边界只取决于输入长度，与 goroutine 数量无关，
// 因此累加顺序固定、结果可复现（不同 GOMAXPROCS 下逐位一致）。
const voxelChunkSize = 4096

// cellKey 是体素格子的整数坐标。
type cellKey struct{ X, Y, Z int32 }

// voxelAccum 累积一个体素内的位置与颜色和。
type voxelAccum struct {
	n                uint64
	sumX, sumY, sumZ float64
	r, g, b          uint64
}

// VoxelDownsample 用体素网格下采样：每个非空格子输出一个代表点（格内位置均值）
// 与该格颜色均值（四舍五入到 8 位）。
//
// 输出按格子坐标 (X,Y,Z) 字典序排列，与分块/并行调度无关，所以结果可复现。
// 位置非有限（NaN/Inf）的点直接丢弃；cols 比 pts 短时缺失的颜色按 0 处理。
//
// 并行安全：函数内部按固定分块并行累加到各自独立的 map，再按分块顺序合并，
// 不共享可变状态；函数本身也是纯函数，可被多个 goroutine 同时调用。
//
// voxelSize <= 0 或非有限时退化为"原样返回"（返回的是拷贝，调用方可安全修改）。
// 输入为空时返回 nil, nil。
func VoxelDownsample(pts []Vec3, cols []Color, voxelSize float32) ([]Vec3, []Color) {
	n := len(pts)
	if n == 0 {
		return nil, nil
	}
	if !isFinite32(voxelSize) || voxelSize <= 0 {
		return cloneVec3(pts), cloneColors(cols, n)
	}

	numChunks := (n + voxelChunkSize - 1) / voxelChunkSize
	parts := make([]map[cellKey]*voxelAccum, numChunks)

	workers := runtime.GOMAXPROCS(0)
	if workers > numChunks {
		workers = numChunks
	}

	var next atomic.Int64
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for {
				ci := int(next.Add(1)) - 1
				if ci >= numChunks {
					return
				}
				lo := ci * voxelChunkSize
				hi := lo + voxelChunkSize
				if hi > n {
					hi = n
				}
				m := make(map[cellKey]*voxelAccum, hi-lo)
				for i := lo; i < hi; i++ {
					p := pts[i]
					if !p.IsFinite() {
						continue
					}
					k := voxelKey(p, voxelSize)
					a := m[k]
					if a == nil {
						a = &voxelAccum{}
						m[k] = a
					}
					a.n++
					a.sumX += float64(p.X)
					a.sumY += float64(p.Y)
					a.sumZ += float64(p.Z)
					if i < len(cols) {
						c := cols[i]
						a.r += uint64(c.R)
						a.g += uint64(c.G)
						a.b += uint64(c.B)
					}
				}
				parts[ci] = m
			}
		}()
	}
	wg.Wait()

	// 按分块顺序合并：每个格子的累加顺序固定，结果逐位可复现。
	merged := make(map[cellKey]*voxelAccum, n/voxelChunkSize+1)
	for _, m := range parts {
		for k, a := range m {
			dst := merged[k]
			if dst == nil {
				merged[k] = a
				continue
			}
			dst.n += a.n
			dst.sumX += a.sumX
			dst.sumY += a.sumY
			dst.sumZ += a.sumZ
			dst.r += a.r
			dst.g += a.g
			dst.b += a.b
		}
	}

	keys := make([]cellKey, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.X != b.X {
			return a.X < b.X
		}
		if a.Y != b.Y {
			return a.Y < b.Y
		}
		return a.Z < b.Z
	})

	outPts := make([]Vec3, len(keys))
	outCols := make([]Color, len(keys))
	for i, k := range keys {
		a := merged[k]
		inv := 1 / float64(a.n)
		outPts[i] = Vec3{
			X: float32(a.sumX * inv),
			Y: float32(a.sumY * inv),
			Z: float32(a.sumZ * inv),
		}
		outCols[i] = Color{
			R: roundU8(a.r, a.n),
			G: roundU8(a.g, a.n),
			B: roundU8(a.b, a.n),
		}
	}
	return outPts, outCols
}

// voxelKey 返回点 p 所在体素格子的整数坐标（向下取整）。
func voxelKey(p Vec3, size float32) cellKey {
	return cellKey{
		clampCell(float64(p.X) / float64(size)),
		clampCell(float64(p.Y) / float64(size)),
		clampCell(float64(p.Z) / float64(size)),
	}
}

// clampCell 对 float64 格坐标向下取整并夹到 int32 范围，避免溢出。
func clampCell(f float64) int32 {
	f = math.Floor(f)
	if f <= math.MinInt32 {
		return math.MinInt32
	}
	if f >= math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(f)
}

// roundU8 返回 sum/n 四舍五入后的 8 位值。
func roundU8(sum, n uint64) uint8 {
	if n == 0 {
		return 0
	}
	v := (sum + n/2) / n
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func cloneVec3(s []Vec3) []Vec3 {
	if s == nil {
		return nil
	}
	out := make([]Vec3, len(s))
	copy(out, s)
	return out
}

func cloneColors(s []Color, n int) []Color {
	out := make([]Color, n)
	copy(out, s)
	return out
}
