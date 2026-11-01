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
//	max_points    点数上限，默认 200000
//	voxel_size    体素边长，0 = 自动（按包围盒与 max_points 推）
//	edge_thresh   边缘阈值，0 = 按深度分位数自动
//
// 响应：application/octet-stream（ILC1 字节），附 X-Ilc1-Pointcount / X-Depth-Width /
// X-Depth-Height / X-Infer-Ms 等响应头。带 ?format=meta 时改为返回 JSON 元数据。
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

	// 4) 深度 → 点云（几何与 ILC1 编码都在 Go 侧）。
	geoStart := time.Now()
	res, err := pipeline.Build(depth.Depth, depth.Width, depth.Height, img, params)
	geoMs := milliseconds(time.Since(geoStart))
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, pipeline.ErrInvalidDepth) {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]string{"error": err.Error()}, true)
		return
	}

	if r.URL.Query().Get("format") == "meta" {
		meta := newInferMeta(res, params, s.processRes, format, len(imgBytes), inferMs, geoMs, milliseconds(time.Since(totalStart)))
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
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write(res.ILC1); err != nil {
		return // 客户端提前断开，无需上报
	}
}

// writeInferError 把 sidecar 错误映射成合适的 HTTP 状态码。
func (s *Server) writeInferError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.Canceled):
		return // 客户端断开，不必再写响应
	case errors.Is(err, context.DeadlineExceeded):
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{
			"error": "sidecar 推理超时: " + err.Error(),
		}, true)
	case errors.Is(err, infer.ErrRemote):
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "sidecar 推理失败: " + err.Error(),
		}, true)
	case errors.Is(err, infer.ErrClosed):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "sidecar 正在关闭",
		}, true)
	default:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "sidecar 不可用: " + err.Error(),
			"hint":  "检查 -sidecar-python 路径与 Go 日志里的 sidecar 报错",
		}, true)
	}
}

// parseInferParams 解析并校验 multipart 文本参数。
func parseInferParams(form url.Values) (pipeline.Params, error) {
	p := pipeline.Params{
		FocalFactor: pipeline.DefaultFocalFactor,
		MaxPoints:   pipeline.DefaultMaxPoints,
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
	}
)

// newInferMeta 组装返回给前端的点云元数据。
func newInferMeta(res *pipeline.Result, params pipeline.Params, processRes int, format string, imgBytes int, inferMs, geoMs, totalMs float64) inferMeta {
	return inferMeta{
		PointCount: res.PointCount,
		Depth:      metaSize{Width: res.DepthWidth, Height: res.DepthHeight},
		Image:      metaImage{Width: res.ImageWidth, Height: res.ImageHeight, Format: format, Bytes: imgBytes},
		BBox: metaBBox{
			Min: [3]float64{float64(res.BBoxMin.X), float64(res.BBoxMin.Y), float64(res.BBoxMin.Z)},
			Max: [3]float64{float64(res.BBoxMax.X), float64(res.BBoxMax.Y), float64(res.BBoxMax.Z)},
		},
		DroppedPixels: res.DroppedPixels,
		VoxelSize:     res.VoxelSize,
		EdgeThresh:    res.EdgeThresh,
		Focal:         res.Focal,
		FocalFactor:   params.FocalFactor,
		ProcessRes:    processRes,
		InferMs:       inferMs,
		GeometryMs:    geoMs,
		TotalMs:       totalMs,
	}
}

// milliseconds 把 Duration 转成毫秒（保留 1 位小数）。
func milliseconds(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}
