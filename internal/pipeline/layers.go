// @kron:intent geometry/scatter-cleanup/layer-quantization

package pipeline

import "sort"

// 深度层量化：把连续的相对深度吸附到 k 个「层」，消除层与层之间的中间深度点。
//
// 为什么需要：DA3 在插画上的输出并不是几块干净的平板，而是一条连续分布 ——
// 主体与背景之间有一条长长的"过渡尾巴"（实测 100 万点里有 16.9% 落在层间，
// 且集中在画面中心，也就是人物周围）。这些点反投影后位于两层之间的中间 z，
// 在 x/y 上却贴着人物轮廓，于是形成一圈「从人物拖向背景的丝状散落点云」。
//
// 做法（不改框架，只在反投影之前插一步）：
//  1. 对有效深度做一维 k-means，得到 k 个层中心；
//  2. 每个像素硬性归到最近的层；
//  3. 每层做连通域分析，每个连通块取自己的深度中位数；
//  4. 块内所有像素的深度改写为该中位数。
//
// 结果：
//  - 层间不再有中间 z（散落带消失，翻到侧面只会看到平整的层，而不是拉丝）；
//  - **人物不会被切开**：层是按连通域划分的，人物是一个连通块，整体只有一个深度；
//  - 连通块级的深度（而不是全局层中心）保留了「同一层里不同元素深浅不同」，
//    比全压平温和。

// LayerQuantizeStats 是层量化的统计信息（回给前端做 meta）。
type LayerQuantizeStats struct {
	// Layers 是实际使用的层数（0 = 未启用）。
	Layers int
	// Centers 是各层中心（按深度从小到大），仅用于展示。
	Centers []float32
	// Flattened 是被吸附到连通块深度的像素数。
	Flattened int
	// Components 是保留下来的连通块总数。
	Components int
	// Tiny 是被并入本层全局中位数的小碎块数（抗噪）。
	Tiny int
}

// quantizeDepthLayers 把深度量化成 k 层。
//
// 返回新的深度切片（不修改入参）与统计信息。discard 里已标记的像素不参与统计，
// 也不会被改写（它们本来就不会出点）。
func quantizeDepthLayers(
	depth []float32, w, h int, discard []bool, k int,
) ([]float32, LayerQuantizeStats) {
	var stats LayerQuantizeStats
	n := w * h
	if k < 2 || n == 0 {
		return depth, stats
	}

	valid := make([]bool, n)
	validCount := 0
	for i := 0; i < n; i++ {
		if validDepth(depth[i]) && !discard[i] {
			valid[i] = true
			validCount++
		}
	}
	// 有效像素太少就别分层了（分出来也是噪声）。
	if validCount < k*64 {
		return depth, stats
	}

	// ── 1. 一维 k-means。为省时间只在等步长抽样上迭代，最后再全量归类。──
	const maxSample = 200_000
	stride := 1
	if validCount > maxSample {
		stride = validCount / maxSample
		if stride < 1 {
			stride = 1
		}
	}
	sample := make([]float32, 0, maxSample+1)
	seen := 0
	for i := 0; i < n; i++ {
		if !valid[i] {
			continue
		}
		if seen%stride == 0 && len(sample) < maxSample {
			sample = append(sample, depth[i])
		}
		seen++
	}
	if len(sample) < k*4 {
		return depth, stats
	}
	sort.Slice(sample, func(a, b int) bool { return sample[a] < sample[b] })

	centers := make([]float64, k)
	for i := 0; i < k; i++ {
		// 初始中心取分位数中点：(i+0.5)/k
		pos := (float64(i) + 0.5) / float64(k) * float64(len(sample)-1)
		centers[i] = float64(sample[int(pos+0.5)])
	}
	for iter := 0; iter < 40; iter++ {
		sums := make([]float64, k)
		counts := make([]int, k)
		for _, v := range sample {
			best, bestD := 0, absF64(float64(v)-centers[0])
			for c := 1; c < k; c++ {
				if d := absF64(float64(v) - centers[c]); d < bestD {
					best, bestD = c, d
				}
			}
			sums[best] += float64(v)
			counts[best]++
		}
		changed := 0.0
		for c := 0; c < k; c++ {
			if counts[c] == 0 {
				continue
			}
			next := sums[c] / float64(counts[c])
			changed += absF64(next - centers[c])
			centers[c] = next
		}
		if changed < 1e-6 {
			break
		}
	}
	sort.Float64s(centers)

	// ── 2. 每个有效像素硬性归到最近层。 ──
	labels := make([]uint8, n)
	layerCounts := make([]int, k)
	for i := 0; i < n; i++ {
		if !valid[i] {
			continue
		}
		d := float64(depth[i])
		best, bestD := 0, absF64(d-centers[0])
		for c := 1; c < k; c++ {
			if dd := absF64(d - centers[c]); dd < bestD {
				best, bestD = c, dd
			}
		}
		labels[i] = uint8(best)
		layerCounts[best]++
	}

	// ── 3. 逐层连通域（4 邻域），求每个连通块的深度中位数。 ──
	out := make([]float32, n)
	copy(out, depth)

	visited := make([]bool, n)
	stack := make([]int32, 0, 1024)
	comp := make([]int32, 0, 4096)
	// 小碎块阈值：整幅有效像素的 0.02%，最少 16 个。
	minComp := validCount / 5000
	if minComp < 16 {
		minComp = 16
	}
	// 每层的全局中位数，给小碎块兜底。
	layerMedian := make([]float32, k)
	for c := 0; c < k; c++ {
		vals := make([]float32, 0, layerCounts[c])
		for i := 0; i < n; i++ {
			if valid[i] && int(labels[i]) == c {
				vals = append(vals, depth[i])
			}
		}
		if len(vals) > 0 {
			sort.Slice(vals, func(a, b int) bool { return vals[a] < vals[b] })
			layerMedian[c] = vals[len(vals)/2]
		}
	}

	for start := 0; start < n; start++ {
		if !valid[start] || visited[start] {
			continue
		}
		lab := labels[start]
		// BFS 收集一个连通块。
		comp = comp[:0]
		stack = stack[:0]
		stack = append(stack, int32(start))
		visited[start] = true
		for len(stack) > 0 {
			cur := int(stack[len(stack)-1])
			stack = stack[:len(stack)-1]
			comp = append(comp, int32(cur))
			x, y := cur%w, cur/w
			if x > 0 {
				if ni := cur - 1; valid[ni] && !visited[ni] && labels[ni] == lab {
					visited[ni] = true
					stack = append(stack, int32(ni))
				}
			}
			if x+1 < w {
				if ni := cur + 1; valid[ni] && !visited[ni] && labels[ni] == lab {
					visited[ni] = true
					stack = append(stack, int32(ni))
				}
			}
			if y > 0 {
				if ni := cur - w; valid[ni] && !visited[ni] && labels[ni] == lab {
					visited[ni] = true
					stack = append(stack, int32(ni))
				}
			}
			if y+1 < h {
				if ni := cur + w; valid[ni] && !visited[ni] && labels[ni] == lab {
					visited[ni] = true
					stack = append(stack, int32(ni))
				}
			}
		}

		var flat float32
		if len(comp) < minComp {
			// 小碎块：并入本层全局中位数，避免留下孤立深度尖刺。
			flat = layerMedian[lab]
			stats.Tiny++
		} else {
			vals := make([]float32, len(comp))
			for i, ci := range comp {
				vals[i] = depth[ci]
			}
			sort.Slice(vals, func(a, b int) bool { return vals[a] < vals[b] })
			flat = vals[len(vals)/2]
			stats.Components++
		}
		for _, ci := range comp {
			out[ci] = flat
			stats.Flattened++
		}
	}

	stats.Layers = k
	stats.Centers = make([]float32, k)
	for i := range centers {
		stats.Centers[i] = float32(centers[i])
	}
	return out, stats
}

func absF64(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
