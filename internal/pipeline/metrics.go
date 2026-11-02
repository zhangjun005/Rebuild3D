package pipeline

import (
	"image"
	"math"

	"illust3d/internal/geometry"
)

// 本文件放"点云质量指标"与共用的直方图/亮度工具，不参与采样本身。
//
// 核心指标是**暗像素富集**（dark enrichment）：
//
//	把点云的点反解回原图像素，统计落在"暗像素"（亮度 < p20）上的点占比，
//	再除以暗像素占全图的比例。
//
// 纯均匀采样 = 1.0；理想的结构/墨量采样应显著 > 1（目标 ≥ 2）。
// 它比 edgePointRatio 更贴近目标，因为线稿是**深色细线**，梯度只在线的两侧大。

// darkPixelPercentile 是"暗像素"用的亮度分位（p20）。
const darkPixelPercentile = 0.20

// darkStats 是原图的"暗像素"统计。
type darkStats struct {
	// thresholdBin 是亮度直方图的 p20 bin（0..255）；
	// 亮度 bin **严格小于** thresholdBin 视为暗（用严格小于避免双峰图里
	// p20 恰好落在最亮的一档时把整幅图都算成"暗"）。
	thresholdBin int
	// darkPixelRatio 是原图里真正暗像素的占比（可能明显小于 0.20，例如线稿图）。
	darkPixelRatio float64
}

// lumaBin 把 RGB 转成 0..255 的灰度直方图 bin。
func lumaBin(r, g, b uint8) int {
	l := (299*int(r) + 587*int(g) + 114*int(b)) / 1000
	if l < 0 {
		return 0
	}
	if l > 255 {
		return 255
	}
	return l
}

// imageDarkStats 用抽稀采样估计原图的 p20 亮度阈值与暗像素占比。
// 采样量约 100 万像素，对 4000×4000 的图也足够稳定。
func imageDarkStats(img image.Image) darkStats {
	if img == nil {
		return darkStats{}
	}
	bounds := img.Bounds()
	iw, ih := bounds.Dx(), bounds.Dy()
	if iw <= 0 || ih <= 0 {
		return darkStats{}
	}
	px := makePixelAt(img)
	step := 1
	if total := iw * ih; total > 1_000_000 {
		step = int(math.Sqrt(float64(total) / 1_000_000))
		if step < 1 {
			step = 1
		}
	}

	var hist [256]int
	n := 0
	for y := 0; y < ih; y += step {
		for x := 0; x < iw; x += step {
			hist[lumaBin(px(x, y))]++
			n++
		}
	}
	if n == 0 {
		return darkStats{}
	}
	target := int(math.Ceil(darkPixelPercentile * float64(n)))
	cum := 0
	bin := 0
	for i, c := range hist {
		cum += c
		if cum >= target {
			bin = i
			break
		}
	}
	// 暗像素 = 亮度严格低于 p20 阈值的像素（cum 里包含阈值 bin 自身，要减掉）。
	darkCount := cum - hist[bin]
	return darkStats{thresholdBin: bin, darkPixelRatio: float64(darkCount) / float64(n)}
}

// darkEnrichmentOf 把暗点占比与暗像素占比相除（均匀采样 → 1.0）。
func darkEnrichmentOf(darkPointRatio, darkPixelRatio float64) float64 {
	if darkPixelRatio <= 0 || darkPointRatio <= 0 {
		return 0
	}
	return darkPointRatio / darkPixelRatio
}

// darkRatioByInverse 把点云反解回图像像素，统计落在暗像素（<= p20）上的比例。
// gw/gh 是采样网格（depthpixel 模式 = 深度图尺寸）；反解统一走 gridPixelFromPoint，
// 因此正交/透视两种模式都正确。
func darkRatioByInverse(pts []geometry.Vec3, proj geometry.UnprojectBlendParams, w, h, gw, gh int, img image.Image, st darkStats) float64 {
	if len(pts) == 0 || gw <= 0 || gh <= 0 || img == nil {
		return 0
	}
	bounds := img.Bounds()
	iw, ih := bounds.Dx(), bounds.Dy()
	if iw <= 0 || ih <= 0 {
		return 0
	}
	px := makePixelAt(img)
	dark := 0
	for _, p := range pts {
		u, v, ok := gridPixelFromPoint(p, proj, w, h, gw, gh)
		if !ok {
			continue
		}
		gx := int(math.Round(u))
		gy := int(math.Round(v))
		// 网格像素 → 原图像素（与 sampleColor 同一映射），越界夹到边缘。
		imgX := int((float64(gx) + 0.5) * float64(iw) / float64(gw))
		imgY := int((float64(gy) + 0.5) * float64(ih) / float64(gh))
		if imgX < 0 {
			imgX = 0
		}
		if imgX >= iw {
			imgX = iw - 1
		}
		if imgY < 0 {
			imgY = 0
		}
		if imgY >= ih {
			imgY = ih - 1
		}
		if lumaBin(px(imgX, imgY)) < st.thresholdBin {
			dark++
		}
	}
	return float64(dark) / float64(len(pts))
}

// uint8Percentile 从 256 桶亮度直方图取分位数（q ∈ [0,1]），返回 0..255。
func uint8Percentile(hist *[256]int, total int, q float64) float64 {
	if total <= 0 {
		return 0
	}
	target := int(math.Ceil(q * float64(total)))
	cum := 0
	for i, c := range hist {
		cum += c
		if cum >= target {
			return float64(i)
		}
	}
	return 255
}
