package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"  // 注册 GIF 解码（标准库）
	_ "image/jpeg" // 注册 JPEG 解码（标准库）
	_ "image/png"  // 注册 PNG 解码（标准库）
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"illust3d/internal/infer"
	"illust3d/internal/pipeline"
)

// handleInfer 是核心接口：插画 → 深度（sidecar）→ 点云（Go 几何）→ ILC1。
//
// 请求：multipart/form-data
//
//	image         必填，插画文件（标准库可解码的 png/jpeg/gif）
//	focal_factor  艺术参数，默认 1.0：fx = fy = factor * max(depthW, depthH)
//	sample_mode   采样模式，默认 structure：depthpixel = 逐深度像素；structure = 结构感知
//	edge_weight   结构模式的边缘权重 kEdge（w = 1 + kEdge*Gn + kInk*Ink），默认 6；0 = 关闭
//	ink_weight    结构模式的墨量权重 kInk（相对暗度通道），默认 4；0 = 关闭
//	work_res      结构采样工作分辨率长边，0 = 自动（原图长边/2，夹 512..2048）
//	max_points    点数上限，默认 400000（与 work_res 解耦）
//	voxel_size    体素边长，默认 0（structure 模式默认不做体素下采样）
//	edge_thresh   边缘阈值，0 = 按深度分位数自动
//
// 响应：application/octet-stream（ILC1 字节），附 X-Ilc1-Pointcount / X-Depth-Width /
// X-Depth-Height / X-Infer-Ms / X-Sample-Mode / X-Work-Res / X-Edge-Point-Ratio /
// X-Dark-Enrichment 等响应头。带 ?format=meta 时改为返回 JSON 元数据。
func (s *Server) handleInfer(w http.ResponseWriter, r *http.Request) {
	totalStart := time.Now()
	if s.sidecar == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "sidecar 未启用，/api/infer 不可用",
			"hint":  "用 -sidecar-python 指定 illust3d 环境的 python.exe",
		}, true)
		return
	}

	// 1) 解析 multipart：整个请求体限制在内存里（maxImageBytes 的 2 倍 + 1 MiB 表单开销），
	//    避免临时文件落盘（本机 C 盘空间紧张）。
	maxBody := s.maxImageBytes*2 + 1<<20
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseMultipartForm(maxBody); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
				"error": fmt.Sprintf("请求体过大（上限 %d MiB）", maxBody>>20),
			}, true)
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "multipart/form-data 解析失败: " + err.Error(),
		}, true)
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	params, err := parseInferParams(r.PostForm)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()}, true)
		return
	}

	file, _, err := r.FormFile("image")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "缺少 image 文件字段（multipart 参数名必须是 image）",
		}, true)
		return
	}
	defer func() { _ = file.Close() }()

	imgBytes, err := io.ReadAll(io.LimitReader(file, s.maxImageBytes+1))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "读取 image 失败: " + err.Error()}, true)
		return
	}
	if len(imgBytes) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "image 为空文件"}, true)
		return
	}
	if int64(len(imgBytes)) > s.maxImageBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
			"error": fmt.Sprintf("图片过大（上限 %d MiB）", s.maxImageBytes>>20),
		}, true)
		return
	}

	// 2) 先只解图片头部拒绝"解压炸弹"，再完整解码（给点云取色需要像素）。
	cfg, format, err := image.DecodeConfig(bytes.NewReader(imgBytes))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "不是可识别的图片（标准库支持 png/jpeg/gif）: " + err.Error(),
		}, true)
		return
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("图片尺寸非法 %dx%d", cfg.Width, cfg.Height),
		}, true)
		return
	}
	if int64(cfg.Width)*int64(cfg.Height) > s.maxPixels {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
			"error": fmt.Sprintf("图片像素数 %dx%d 超过上限 %d", cfg.Width, cfg.Height, s.maxPixels),
		}, true)
		return
	}
	img, _, err := image.Decode(bytes.NewReader(imgBytes))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "图片解码失败: " + err.Error()}, true)
		return
	}

	// 3) sidecar 推理（首次请求可能等模型预热，最长约 180s）。
	inferStart := time.Now()
	depth, err := s.sidecar.Infer(r.Context(), imgBytes, uint32(s.processRes))
	inferMs := milliseconds(time.Since(inferStart))
	if err != nil {
		s.writeInferError(w, err)
		return
	}
	if depth == nil || depth.Width <= 0 || depth.Height <= 0 || len(depth.Depth) < depth.Width*depth.Height {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "sidecar 返回的深度图非法",
		}, true)
		return
	}

	// 3.5) 角色分割（best-effort）：给「边界带深度压缩」提供语义边界。
	//
	// 只在真的要用时才调（band_width>0）——分割是 CPU 推理、约 0.9s，
	// 边界带关闭时白花这个时间没有意义。
	//
	// 为什么允许失败：分割只是锦上添花——失败时退化成"不做边界带处理"，
	// 深度→点云这条主链路必须照常工作。
	var segMs float64
	var mask []uint8
	var maskW, maskH int
	segError := ""
	if params.BandWidth > 0 {
		segStart := time.Now()
		seg, segErr := s.sidecar.Segment(r.Context(), imgBytes)
		segMs = milliseconds(time.Since(segStart))
		switch {
		case segErr != nil:
			segError = segErr.Error()
		case seg == nil || seg.Width <= 0 || seg.Height <= 0:
			segError = "分割返回空结果"
		case len(seg.Mask) != seg.Width*seg.Height:
			segError = fmt.Sprintf("分割 mask 尺寸不一致：len=%d, %dx%d", len(seg.Mask), seg.Width, seg.Height)
		default:
			mask, maskW, maskH = seg.Mask, seg.Width, seg.Height
		}
	}

	// 4) 深度 → 点云（几何与 ILC1 编码都在 Go 侧）。
	geoStart := time.Now()
	res, err := pipeline.BuildWithMask(depth.Depth, depth.Width, depth.Height, img, mask, maskW, maskH, params)
	geoMs := milliseconds(time.Since(geoStart))
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, pipeline.ErrInvalidDepth) || errors.Is(err, pipeline.ErrInvalidParams) {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]string{"error": err.Error()}, true)
		return
	}

	if r.URL.Query().Get("format") == "meta" {
		meta := newInferMeta(res, params, s.processRes, format, len(imgBytes), inferMs, segMs, geoMs, milliseconds(time.Since(totalStart)), segError)
		writeJSON(w, http.StatusOK, meta, true)
		return
	}

	totalMs := milliseconds(time.Since(totalStart))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(res.ILC1)))
	w.Header().Set("X-Ilc1-Pointcount", strconv.Itoa(res.PointCount))
	w.Header().Set("X-Depth-Width", strconv.Itoa(res.DepthWidth))
	w.Header().Set("X-Depth-Height", strconv.Itoa(res.DepthHeight))
	w.Header().Set("X-Infer-Ms", strconv.FormatFloat(inferMs, 'f', 1, 64))
	w.Header().Set("X-Geometry-Ms", strconv.FormatFloat(geoMs, 'f', 1, 64))
	w.Header().Set("X-Total-Ms", strconv.FormatFloat(totalMs, 'f', 1, 64))
	w.Header().Set("X-Sample-Mode", res.SampleMode)
	w.Header().Set("X-Work-Res", strconv.Itoa(res.WorkRes))
	w.Header().Set("X-Edge-Point-Ratio", strconv.FormatFloat(res.EdgePointRatio, 'f', 4, 64))
	w.Header().Set("X-Dark-Enrichment", strconv.FormatFloat(res.DarkEnrichment, 'f', 4, 64))
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write(res.ILC1); err != nil {
		return // 客户端提前断开，无需上报
	}
}

// statusClientClosedRequest 是"客户端提前断开"的返回码（499，nginx 约定；
// Go 标准库没有常量，但前端只要是非 2xx 就能正确分辨失败，不会把空 body 当成功）。
const statusClientClosedRequest = 499

// writeInferError 把 sidecar 错误映射成合适的 HTTP 状态码。
//
// **铁律：任何错误都必须返回非 2xx + JSON body**。历史事故：客户端中断后走
// context.Canceled 分支直接 return，导致 200 + Content-Length: 0，前端静默失败。
func (s *Server) writeInferError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.Canceled):
		// 请求被客户端取消（关页面/超时/取消上传）：本次结果作废，但不是服务故障。
		writeJSON(w, statusClientClosedRequest, map[string]string{
			"error": "请求已取消（客户端提前断开），本次结果丢弃；服务仍可用，可直接重试",
			"code":  "client_canceled",
		}, true)
	case errors.Is(err, context.DeadlineExceeded):
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{
			"error": "sidecar 推理超时: " + err.Error(),
			"code":  "infer_timeout",
		}, true)
	case errors.Is(err, infer.ErrDesync):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "sidecar 管道失步（帧边界错位）: " + err.Error(),
			"code":  "desync",
		}, true)
	case errors.Is(err, infer.ErrRemote):
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "sidecar 推理失败: " + err.Error(),
			"code":  "sidecar_error",
		}, true)
	case errors.Is(err, infer.ErrClosed):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "sidecar 正在关闭",
			"code":  "sidecar_closed",
		}, true)
	default:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "sidecar 不可用: " + err.Error(),
			"code":  "sidecar_unavailable",
			"hint":  "检查 -sidecar-python 路径与 Go 日志里的 sidecar 报错",
		}, true)
	}
}

// parseInferParams 解析并校验 multipart 文本参数。
//
// 注意 API 层默认 sample_mode=structure（产品默认）；pipeline 库层默认 depthpixel
// 以保持向后兼容，两边都在结果里如实报告实际使用的模式。
func parseInferParams(form url.Values) (pipeline.Params, error) {
	p := pipeline.Params{
		FocalFactor:  pipeline.DefaultFocalFactor,
		MaxPoints:    pipeline.DefaultMaxPoints,
		SampleMode:   pipeline.SampleModeStructure,
		EdgeWeight:   pipeline.DefaultEdgeWeight,
		InkWeight:    pipeline.DefaultInkWeight,
		LayerCount:   pipeline.DefaultLayerCount,
		BandWidth:    pipeline.DefaultBandWidth,
		BandCompress: pipeline.DefaultBandCompress,
	}
	if v := form.Get("focal_factor"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return p, fmt.Errorf("focal_factor 不是数字: %q", v)
		}
		if f <= 0 || math.IsNaN(f) || math.IsInf(f, 0) {
			return p, fmt.Errorf("focal_factor 必须 > 0（当前 %v）", f)
		}
		p.FocalFactor = f
	}
	if v := form.Get("max_points"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return p, fmt.Errorf("max_points 必须是非负整数: %q", v)
		}
		if n > 0 {
			p.MaxPoints = n
		}
	}
	if v := form.Get("voxel_size"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 || math.IsNaN(f) || math.IsInf(f, 0) {
			return p, fmt.Errorf("voxel_size 必须是非负有限数: %q", v)
		}
		p.VoxelSize = float32(f)
	}
	if v := form.Get("edge_thresh"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 || math.IsNaN(f) || math.IsInf(f, 0) {
			return p, fmt.Errorf("edge_thresh 必须是非负有限数: %q", v)
		}
		p.EdgeThresh = float32(f)
	}
	if v := form.Get("sample_mode"); v != "" {
		switch v {
		case pipeline.SampleModeStructure, pipeline.SampleModeDepthPixel:
			p.SampleMode = v
		default:
			return p, fmt.Errorf("sample_mode 只支持 %q / %q（当前 %q）",
				pipeline.SampleModeDepthPixel, pipeline.SampleModeStructure, v)
		}
	}
	if v := form.Get("edge_weight"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 || math.IsNaN(f) || math.IsInf(f, 0) {
			return p, fmt.Errorf("edge_weight 必须是非负有限数: %q", v)
		}
		p.EdgeWeight = f // 0 = 显式关闭边缘通道
	}
	if v := form.Get("ink_weight"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 || math.IsNaN(f) || math.IsInf(f, 0) {
			return p, fmt.Errorf("ink_weight 必须是非负有限数: %q", v)
		}
		p.InkWeight = f // 0 = 显式关闭墨量通道
	}
	if v := form.Get("work_res"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return p, fmt.Errorf("work_res 必须是非负整数（0=自动）: %q", v)
		}
		p.WorkRes = n
	}
	if v := form.Get("band_width"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return p, fmt.Errorf("band_width 必须是非负整数（像素，0=关闭）: %q", v)
		}
		p.BandWidth = n
	}
	if v := form.Get("band_compress"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0 || f > 1 || math.IsNaN(f) || math.IsInf(f, 0) {
			return p, fmt.Errorf("band_compress 必须落在 [0,1]: %q", v)
		}
		p.BandCompress = f
	}
	if v := form.Get("layer_count"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return p, fmt.Errorf("layer_count 必须是非负整数（0=关闭，2~8 有效）: %q", v)
		}
		p.LayerCount = n
	}
	return p, nil
}

// 元数据结构（?format=meta 与后续 /api/cloud/{id}/meta 共用）。
type (
	metaSize struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}
	metaImage struct {
		Width  int    `json:"width"`
		Height int    `json:"height"`
		Format string `json:"format"`
		Bytes  int    `json:"bytes"`
	}
	metaBBox struct {
		Min [3]float64 `json:"min"`
		Max [3]float64 `json:"max"`
	}
	inferMeta struct {
		PointCount    int       `json:"pointCount"`
		Depth         metaSize  `json:"depth"`
		Image         metaImage `json:"image"`
		BBox          metaBBox  `json:"bbox"`
		DroppedPixels int       `json:"droppedPixels"`
		VoxelSize     float32   `json:"voxelSize"`
		EdgeThresh    float32   `json:"edgeThresh"`
		Focal         float64   `json:"focal"`
		FocalFactor   float64   `json:"focalFactor"`
		ProcessRes    int       `json:"processRes"`
		InferMs       float64   `json:"inferMs"`
		GeometryMs    float64   `json:"geometryMs"`
		TotalMs       float64   `json:"totalMs"`
		// SampleMode / WorkRes / 权重 / 指标：结构感知采样的关键元数据。
		// edgePointRatio = 落在原图强边缘（Gn>0.5）上的点占比；
		// darkEnrichment = 落在暗像素（lum<p20）上的点占比 / 暗像素占比（1.0 = 均匀采样）。
		SampleMode     string  `json:"sampleMode"`
		WorkRes        int     `json:"workRes"`
		EdgePointRatio float64 `json:"edgePointRatio"`
		EdgeWeight     float64 `json:"edgeWeight"`
		InkWeight      float64 `json:"inkWeight"`
		DarkPointRatio float64 `json:"darkPointRatio"`
		DarkPixelRatio float64 `json:"darkPixelRatio"`
		DarkEnrichment float64 `json:"darkEnrichment"`
		// 深度层量化：把连续深度吸附到连通块深度，消除层间"拉丝"散落点。
		LayerCount      int       `json:"layerCount"`
		LayerCenters    []float32 `json:"layerCenters,omitempty"`
		LayerFlattened  int       `json:"layerFlattened"`
		LayerComponents int       `json:"layerComponents"`
		// 边界带深度压缩（mask 引导）：只动人物轮廓附近，角色/背景内部不动。
		BandEnabled    bool    `json:"bandEnabled"`
		BandFallback   string  `json:"bandFallback,omitempty"`
		BandWidth      int     `json:"bandWidth"`
		BandCompress   float64 `json:"bandCompress"`
		BandPixels     int     `json:"bandPixels"`
		BandAdjusted   int     `json:"bandAdjusted"`
		BandCharRatio  float64 `json:"bandCharRatio"`
		BandBorderFrac float64 `json:"bandBorderFrac"`
		// 角色分割（anime-seg，CPU 推理）。SegError 非空 = 分割失败、边界带已退化。
		SegmentMs float64 `json:"segmentMs"`
		SegError  string  `json:"segError,omitempty"`
	}
)

// newInferMeta 组装返回给前端的点云元数据。
func newInferMeta(res *pipeline.Result, params pipeline.Params, processRes int, format string, imgBytes int, inferMs, segMs, geoMs, totalMs float64, segError string) inferMeta {
	edgeWeight, inkWeight := params.EdgeWeight, params.InkWeight
	if res.SampleMode != pipeline.SampleModeStructure {
		edgeWeight, inkWeight = 0, 0 // depthpixel 模式不用这些权重，别误导前端
	}
	return inferMeta{
		PointCount: res.PointCount,
		Depth:      metaSize{Width: res.DepthWidth, Height: res.DepthHeight},
		Image:      metaImage{Width: res.ImageWidth, Height: res.ImageHeight, Format: format, Bytes: imgBytes},
		BBox: metaBBox{
			Min: [3]float64{float64(res.BBoxMin.X), float64(res.BBoxMin.Y), float64(res.BBoxMin.Z)},
			Max: [3]float64{float64(res.BBoxMax.X), float64(res.BBoxMax.Y), float64(res.BBoxMax.Z)},
		},
		DroppedPixels:  res.DroppedPixels,
		VoxelSize:      res.VoxelSize,
		EdgeThresh:     res.EdgeThresh,
		Focal:          res.Focal,
		FocalFactor:    params.FocalFactor,
		ProcessRes:     processRes,
		InferMs:        inferMs,
		GeometryMs:     geoMs,
		TotalMs:        totalMs,
		SampleMode:     res.SampleMode,
		WorkRes:        res.WorkRes,
		EdgePointRatio: res.EdgePointRatio,
		EdgeWeight:     edgeWeight,
		InkWeight:      inkWeight,
		DarkPointRatio: res.DarkPointRatio,
		DarkPixelRatio: res.DarkPixelRatio,
		DarkEnrichment: res.DarkEnrichment,

		LayerCount:      res.LayerCount,
		LayerCenters:    res.LayerCenters,
		LayerFlattened:  res.LayerFlattened,
		LayerComponents: res.LayerComponents,

		BandEnabled:    res.Band.Enabled,
		BandFallback:   res.Band.Fallback,
		BandWidth:      res.Band.BandWidth,
		BandCompress:   res.Band.Compress,
		BandPixels:     res.Band.BandPixels,
		BandAdjusted:   res.Band.Adjusted,
		BandCharRatio:  res.Band.CharRatio,
		BandBorderFrac: res.Band.BorderFrac,
		SegmentMs:      segMs,
		SegError:       segError,
	}
}

// milliseconds 把 Duration 转成毫秒（保留 1 位小数）。
func milliseconds(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}
