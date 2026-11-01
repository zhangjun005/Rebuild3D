package geometry

// FilterDepthEdges 标记需要丢弃的像素，用于消除深度图遮挡边界两侧的 flying pixels。
//
// 判定规则（true = 丢弃）：
//   - 像素自身深度无效（<=0、NaN、±Inf）；
//   - 或与 4 邻域中任一像素的 |Δdepth| 大于 thresh（即深度跳变超过阈值）。
//
// 因为按"相邻两条边各标记两侧"处理，遮挡边界两侧的像素都会被标记，
// 只丢掉其中一侧的话仍会在裂缝里留下拉丝状飞点。
// thresh 是绝对深度差，单位与 depth 相同；thresh < 0 或 NaN 按 0 处理。
//
// 返回长度恰为 w*h 的掩码，下标 = y*w + x。参数非法时返回 nil。
func FilterDepthEdges(depth []float32, w, h int, thresh float32) []bool {
	if w <= 0 || h <= 0 || len(depth) < w*h {
		return nil
	}
	if !(thresh >= 0) { // 同时覆盖负数与 NaN
		thresh = 0
	}

	discard := make([]bool, w*h)
	for y := 0; y < h; y++ {
		row := y * w
		for x := 0; x < w; x++ {
			i := row + x
			d := depth[i]
			if !validDepth(d) {
				discard[i] = true
				continue
			}
			// 只需比较右、下两个方向，即可覆盖全部水平/垂直相邻对。
			if x+1 < w {
				nd := depth[i+1]
				if !validDepth(nd) || absDiff32(d, nd) > thresh {
					discard[i] = true
					discard[i+1] = true
				}
			}
			if y+1 < h {
				nd := depth[i+w]
				if !validDepth(nd) || absDiff32(d, nd) > thresh {
					discard[i] = true
					discard[i+w] = true
				}
			}
		}
	}
	return discard
}

// absDiff32 返回 |a-b|（调用方保证两者有限）。
func absDiff32(a, b float32) float32 {
	d := a - b
	if d < 0 {
		return -d
	}
	return d
}
