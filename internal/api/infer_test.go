package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"illust3d/internal/cloud"
	"illust3d/internal/geometry"
	"illust3d/internal/infer"
	"illust3d/internal/pipeline"
)

// stubSidecar 是 Sidecar 的测试替身（不启进程、不需要 GPU）。
type stubSidecar struct {
	res    *infer.DepthResult
	err    error
	status infer.Status
	image  []byte
	calls  int
}

func (s *stubSidecar) Infer(_ context.Context, img []byte, _ uint32) (*infer.DepthResult, error) {
	s.calls++
	s.image = append([]byte(nil), img...)
	return s.res, s.err
}

func (s *stubSidecar) Status() infer.Status { return s.status }

// makePNG 生成一张 w×h 纯色 PNG。
func makePNG(t *testing.T, w, h int, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

// tinyDepth 生成 4×3 的合法深度图（值 1..3.75）。
func tinyDepth() *infer.DepthResult {
	depth := make([]float32, 12)
	for i := range depth {
		depth[i] = 1 + float32(i)*0.25
	}
	return &infer.DepthResult{Width: 4, Height: 3, Depth: depth}
}

// postInfer 用 multipart 发请求；imgBytes 为 nil 时不带 image 字段。
func postInfer(t *testing.T, url string, imgBytes []byte, fields map[string]string) (*http.Response, []byte) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if imgBytes != nil {
		fw, err := mw.CreateFormFile("image", "test.png")
		if err != nil {
			t.Fatalf("CreateFormFile: %v", err)
		}
		if _, err := fw.Write(imgBytes); err != nil {
			t.Fatalf("写 image: %v", err)
		}
	}
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatalf("WriteField: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("multipart close: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, url, &body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	data, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("读响应: %v", err)
	}
	return resp, data
}

// inferServer 构造一个带 stub sidecar 的测试服务。
func inferServer(t *testing.T, side Sidecar, opts Options) *httptest.Server {
	t.Helper()
	if opts.FixturePath == "" {
		opts.FixturePath = writeFixturePath(t)
	}
	opts.Sidecar = side
	return newTestServer(t, opts)
}

func TestInferSuccess(t *testing.T) {
	side := &stubSidecar{res: tinyDepth()}
	s := inferServer(t, side, Options{Version: "v-test"})
	pngBytes := makePNG(t, 8, 6, color.RGBA{R: 10, G: 20, B: 30, A: 255})

	resp, body := postInfer(t, s.URL+"/api/infer", pngBytes, map[string]string{
		"edge_thresh": "1000",                        // 关掉自动阈值，方便断言点数
		"sample_mode": pipeline.SampleModeDepthPixel, // 这条测的是旧的逐像素路径
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200（body=%s）", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Content-Type = %q", ct)
	}
	// 4×3 深度、无丢点 → 12 点。
	if pc := resp.Header.Get("X-Ilc1-Pointcount"); pc != "12" {
		t.Errorf("X-Ilc1-Pointcount = %q，期望 12", pc)
	}
	if w := resp.Header.Get("X-Depth-Width"); w != "4" {
		t.Errorf("X-Depth-Width = %q，期望 4", w)
	}
	if h := resp.Header.Get("X-Depth-Height"); h != "3" {
		t.Errorf("X-Depth-Height = %q，期望 3", h)
	}
	if ms := resp.Header.Get("X-Infer-Ms"); ms == "" {
		t.Error("缺少 X-Infer-Ms 响应头")
	}
	if len(body) < 4 || string(body[0:4]) != "ILC1" {
		t.Fatalf("响应不是 ILC1：len=%d", len(body))
	}
	c, err := cloud.Decode(body)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if len(c.Pos) != 12 {
		t.Errorf("ILC1 点数 = %d，期望 12", len(c.Pos))
	}
	wantColor := geometry.Color{R: 10, G: 20, B: 30}
	for i := range c.Col {
		if c.Col[i] != wantColor {
			t.Errorf("Col[%d] = %+v，期望 %+v", i, c.Col[i], wantColor)
			break
		}
	}
	if side.calls != 1 {
		t.Errorf("sidecar 调用次数 = %d，期望 1", side.calls)
	}
	if !bytes.Equal(side.image, pngBytes) {
		t.Error("传给 sidecar 的不是原始图片字节")
	}
}

func TestInferMeta(t *testing.T) {
	side := &stubSidecar{res: tinyDepth()}
	s := inferServer(t, side, Options{Version: "v-test", ProcessRes: 504})
	pngBytes := makePNG(t, 8, 6, color.RGBA{R: 1, G: 2, B: 3, A: 255})

	resp, body := postInfer(t, s.URL+"/api/infer?format=meta", pngBytes, map[string]string{
		"edge_thresh":  "1000",
		"focal_factor": "2",
		"sample_mode":  pipeline.SampleModeDepthPixel,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200（body=%s）", resp.StatusCode, body)
	}
	var meta inferMeta
	if err := json.Unmarshal(body, &meta); err != nil {
		t.Fatalf("解析 meta: %v（body=%s）", err, body)
	}
	if meta.PointCount != 12 {
		t.Errorf("pointCount = %d，期望 12", meta.PointCount)
	}
	if meta.Depth.Width != 4 || meta.Depth.Height != 3 {
		t.Errorf("depth = %+v", meta.Depth)
	}
	if meta.Image.Width != 8 || meta.Image.Height != 6 || meta.Image.Format != "png" {
		t.Errorf("image = %+v", meta.Image)
	}
	if meta.FocalFactor != 2 {
		t.Errorf("focalFactor = %v，期望 2", meta.FocalFactor)
	}
	// focal = focal_factor * max(depthW, depthH) = 2 * 4 = 8
	if meta.Focal != 8 {
		t.Errorf("focal = %v，期望 8", meta.Focal)
	}
	if meta.ProcessRes != 504 {
		t.Errorf("processRes = %d，期望 504", meta.ProcessRes)
	}
	if meta.BBox.Max[2] <= meta.BBox.Min[2] {
		t.Errorf("bbox 非法: %+v", meta.BBox)
	}
	if meta.InferMs < 0 || meta.GeometryMs < 0 || meta.TotalMs < 0 {
		t.Errorf("耗时字段为负: %+v", meta)
	}
}

// TestInferStructureModeDefault 验证 API 层默认 sample_mode=structure：
// 点数精确、meta 带 sampleMode/workRes/edgePointRatio、响应头齐全、默认不做体素下采样。
func TestInferStructureModeDefault(t *testing.T) {
	side := &stubSidecar{res: tinyDepth()}
	s := inferServer(t, side, Options{Version: "v-test", ProcessRes: 504})
	white := makePNG(t, 8, 6, color.RGBA{R: 255, G: 255, B: 255, A: 255})

	// 先看 meta：4×3 深度、原图 8×6 → 自动 workRes = clamp(round(8/2),512,2048)=512
	// （4:3 → 512×384，候选 196608 > 5000，所以应精确取 5000 点）。
	resp, body := postInfer(t, s.URL+"/api/infer?format=meta", white, map[string]string{
		"max_points": "5000",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200（body=%s）", resp.StatusCode, body)
	}
	var meta inferMeta
	if err := json.Unmarshal(body, &meta); err != nil {
		t.Fatalf("解析 meta: %v（body=%s）", err, body)
	}
	if meta.SampleMode != pipeline.SampleModeStructure {
		t.Errorf("默认 sampleMode = %q，期望 %q", meta.SampleMode, pipeline.SampleModeStructure)
	}
	if meta.PointCount != 5000 {
		t.Errorf("pointCount = %d，期望精确 5000", meta.PointCount)
	}
	if meta.WorkRes != 512 {
		t.Errorf("workRes = %d，期望 512（自动值顶到下限）", meta.WorkRes)
	}
	if meta.VoxelSize != 0 {
		t.Errorf("structure 模式默认不做体素下采样，voxelSize = %v", meta.VoxelSize)
	}
	if meta.EdgeWeight != pipeline.DefaultEdgeWeight {
		t.Errorf("edgeWeight = %v，期望默认 %v", meta.EdgeWeight, pipeline.DefaultEdgeWeight)
	}
	if meta.InkWeight != pipeline.DefaultInkWeight {
		t.Errorf("inkWeight = %v，期望默认 %v", meta.InkWeight, pipeline.DefaultInkWeight)
	}
	if meta.EdgePointRatio < 0 || meta.EdgePointRatio > 1 {
		t.Errorf("edgePointRatio = %v，应在 [0,1]", meta.EdgePointRatio)
	}
	if meta.DarkPixelRatio < 0 || meta.DarkPixelRatio > 1 {
		t.Errorf("darkPixelRatio = %v，应在 [0,1]", meta.DarkPixelRatio)
	}
	if meta.DarkEnrichment < 0 {
		t.Errorf("darkEnrichment = %v，应 >= 0", meta.DarkEnrichment)
	}

	// 再看二进制响应：响应头 + ILC1 长度必须与点数自洽。
	resp2, bin := postInfer(t, s.URL+"/api/infer", white, map[string]string{"max_points": "5000"})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200（body=%s）", resp2.StatusCode, bin)
	}
	if got := resp2.Header.Get("X-Sample-Mode"); got != pipeline.SampleModeStructure {
		t.Errorf("X-Sample-Mode = %q", got)
	}
	if got := resp2.Header.Get("X-Work-Res"); got != "512" {
		t.Errorf("X-Work-Res = %q，期望 512", got)
	}
	if got := resp2.Header.Get("X-Edge-Point-Ratio"); got == "" {
		t.Error("缺少 X-Edge-Point-Ratio 响应头")
	}
	if got := resp2.Header.Get("X-Dark-Enrichment"); got == "" {
		t.Error("缺少 X-Dark-Enrichment 响应头")
	}
	if len(bin) != 8+15*5000 {
		t.Errorf("ILC1 字节数 = %d，期望 %d", len(bin), 8+15*5000)
	}
	if string(bin[0:4]) != "ILC1" {
		t.Errorf("magic = %q", bin[0:4])
	}

	// 显式 work_res / 权重会被采纳并如实报告（A/B 用）。
	_, abBody := postInfer(t, s.URL+"/api/infer?format=meta", white, map[string]string{
		"max_points":  "5000",
		"work_res":    "1024",
		"edge_weight": "0",
		"ink_weight":  "3",
	})
	var abMeta inferMeta
	if err := json.Unmarshal(abBody, &abMeta); err != nil {
		t.Fatalf("解析 A/B meta: %v", err)
	}
	if abMeta.WorkRes != 1024 || abMeta.EdgeWeight != 0 || abMeta.InkWeight != 3 {
		t.Errorf("A/B meta = workRes:%d edgeWeight:%v inkWeight:%v，期望 1024/0/3",
			abMeta.WorkRes, abMeta.EdgeWeight, abMeta.InkWeight)
	}

	// depthpixel 仍可回退，且 meta 如实报告。
	_, dpBody := postInfer(t, s.URL+"/api/infer?format=meta", white, map[string]string{
		"max_points":  "5000",
		"sample_mode": pipeline.SampleModeDepthPixel,
	})
	var dpMeta inferMeta
	if err := json.Unmarshal(dpBody, &dpMeta); err != nil {
		t.Fatalf("解析 depthpixel meta: %v", err)
	}
	if dpMeta.SampleMode != pipeline.SampleModeDepthPixel {
		t.Errorf("回退 sampleMode = %q", dpMeta.SampleMode)
	}
	if dpMeta.PointCount != 12 {
		t.Errorf("depthpixel 点数 = %d，期望 12", dpMeta.PointCount)
	}
	if dpMeta.WorkRes != 4 {
		t.Errorf("depthpixel workRes = %d，期望 4（深度长边）", dpMeta.WorkRes)
	}
}

func TestInferErrors(t *testing.T) {
	validPNG := makePNG(t, 8, 6, color.RGBA{})
	tests := []struct {
		name       string
		image      []byte // nil = 不带 image 字段
		fields     map[string]string
		side       *stubSidecar
		maxImgByte int64
		wantStatus int
		wantSubstr string
	}{
		{
			name:       "missing-image",
			image:      nil,
			side:       &stubSidecar{res: tinyDepth()},
			wantStatus: http.StatusBadRequest,
			wantSubstr: "image",
		},
		{
			name:       "empty-image",
			image:      []byte{},
			side:       &stubSidecar{res: tinyDepth()},
			wantStatus: http.StatusBadRequest,
			wantSubstr: "为空",
		},
		{
			name:       "not-an-image",
			image:      []byte("这不是图片"),
			side:       &stubSidecar{res: tinyDepth()},
			wantStatus: http.StatusBadRequest,
			wantSubstr: "不是可识别的图片",
		},
		{
			name:       "too-large-image",
			image:      bytes.Repeat([]byte{0xFF}, 300),
			side:       &stubSidecar{res: tinyDepth()},
			maxImgByte: 128,
			wantStatus: http.StatusRequestEntityTooLarge,
			wantSubstr: "过大",
		},
		{
			name:       "bad-focal-factor",
			image:      validPNG,
			fields:     map[string]string{"focal_factor": "abc"},
			side:       &stubSidecar{res: tinyDepth()},
			wantStatus: http.StatusBadRequest,
			wantSubstr: "focal_factor",
		},
		{
			name:       "negative-voxel-size",
			image:      validPNG,
			fields:     map[string]string{"voxel_size": "-1"},
			side:       &stubSidecar{res: tinyDepth()},
			wantStatus: http.StatusBadRequest,
			wantSubstr: "voxel_size",
		},
		{
			name:       "bad-sample-mode",
			image:      validPNG,
			fields:     map[string]string{"sample_mode": "bogus"},
			side:       &stubSidecar{res: tinyDepth()},
			wantStatus: http.StatusBadRequest,
			wantSubstr: "sample_mode",
		},
		{
			name:       "negative-edge-weight",
			image:      validPNG,
			fields:     map[string]string{"edge_weight": "-2"},
			side:       &stubSidecar{res: tinyDepth()},
			wantStatus: http.StatusBadRequest,
			wantSubstr: "edge_weight",
		},
		{
			name:       "negative-ink-weight",
			image:      validPNG,
			fields:     map[string]string{"ink_weight": "-2"},
			side:       &stubSidecar{res: tinyDepth()},
			wantStatus: http.StatusBadRequest,
			wantSubstr: "ink_weight",
		},
		{
			name:       "negative-work-res",
			image:      validPNG,
			fields:     map[string]string{"work_res": "-1"},
			side:       &stubSidecar{res: tinyDepth()},
			wantStatus: http.StatusBadRequest,
			wantSubstr: "work_res",
		},
		{
			name:       "sidecar-remote-error",
			image:      validPNG,
			side:       &stubSidecar{err: &infer.RemoteError{Cmd: infer.CmdInfer, Message: "模型未加载"}},
			wantStatus: http.StatusBadGateway,
			wantSubstr: "sidecar 推理失败",
		},
		{
			name:       "sidecar-pipe-error",
			image:      validPNG,
			side:       &stubSidecar{err: fmt.Errorf("%w: sidecar 已退出", infer.ErrProtocol)},
			wantStatus: http.StatusServiceUnavailable,
			wantSubstr: "sidecar 不可用",
		},
		{
			name:       "all-invalid-depth",
			image:      validPNG,
			side:       &stubSidecar{res: &infer.DepthResult{Width: 2, Height: 2, Depth: []float32{0, 0, -1, 0}}},
			wantStatus: http.StatusUnprocessableEntity,
			wantSubstr: "没有有效点",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts := Options{MaxImageBytes: tc.maxImgByte}
			s := inferServer(t, tc.side, opts)
			resp, body := postInfer(t, s.URL+"/api/infer", tc.image, tc.fields)
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("状态码 = %d，期望 %d（body=%s）", resp.StatusCode, tc.wantStatus, body)
			}
			if !strings.Contains(string(body), tc.wantSubstr) {
				t.Errorf("响应体 %q 不含 %q", body, tc.wantSubstr)
			}
		})
	}
}

// TestInferSidecarDisabled 验证没配 sidecar 时返回 503（web 可独立开发）。
func TestInferSidecarDisabled(t *testing.T) {
	s := inferServer(t, nil, Options{})
	resp, body := postInfer(t, s.URL+"/api/infer", makePNG(t, 4, 3, color.RGBA{}), nil)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("状态码 = %d，期望 503（body=%s）", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "sidecar") {
		t.Errorf("响应 = %s", body)
	}
}

// TestInferWrongMethod 验证 GET /api/infer 不会落到推理逻辑（被静态 catch-all 兜住 → 404）。
func TestInferWrongMethod(t *testing.T) {
	s := inferServer(t, &stubSidecar{res: tinyDepth()}, Options{})
	resp, err := http.Get(s.URL + "/api/infer")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("GET /api/infer 不应返回 200")
	}
}

func TestHealthIncludesSidecar(t *testing.T) {
	status := infer.Status{
		Ready:            true,
		Model:            "depth-anything/DA3MONO-LARGE",
		Device:           "cuda",
		VRAMUsedMB:       1276.2,
		LastInferSeconds: 0.42,
		PID:              4321,
	}
	side := &stubSidecar{res: tinyDepth(), status: status}
	s := inferServer(t, side, Options{Version: "v0"})

	_, body := doGet(t, s.URL+"/api/health")
	var got healthResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("解析 health: %v", err)
	}
	if got.Sidecar == nil {
		t.Fatal("health 缺少 sidecar 字段")
	}
	if !got.Sidecar.Ready || got.Sidecar.Device != "cuda" || got.Sidecar.VRAMUsedMB != 1276.2 {
		t.Errorf("sidecar 状态 = %+v", got.Sidecar)
	}
	if got.Sidecar.LastInferSeconds != 0.42 || got.Sidecar.PID != 4321 {
		t.Errorf("sidecar 状态 = %+v", got.Sidecar)
	}
	if got.Status != "ok" {
		t.Errorf("status = %q，期望 ok", got.Status)
	}

	// sidecar 报错时整体标 degraded。
	side.status = infer.Status{Error: "sidecar 预热失败", PID: 4321}
	_, body = doGet(t, s.URL+"/api/health")
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("解析 health: %v", err)
	}
	if got.Status != "degraded" || got.Sidecar == nil || got.Sidecar.Error == "" {
		t.Errorf("degraded health = %+v（sidecar=%+v）", got, got.Sidecar)
	}
}

// TestWebDistServing 验证 -web-dist 从磁盘服务静态资源，且目录不存在时回退内嵌占位页。
func TestWebDistServing(t *testing.T) {
	dir := t.TempDir()
	const html = "<!doctype html><h1>web dist 已构建</h1>"
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(html), 0o644); err != nil {
		t.Fatalf("写 web-dist: %v", err)
	}

	s := newTestServer(t, Options{WebDist: dir, FixturePath: writeFixturePath(t)})
	resp, body := doGet(t, s.URL+"/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", resp.StatusCode)
	}
	if !strings.Contains(string(body), "web dist 已构建") {
		t.Errorf("GET / 内容 = %q，期望来自 -web-dist", body)
	}

	// 目录不存在：回退内嵌占位页，StaticError 有值。
	missing := filepath.Join(dir, "不存在")
	srv := New(Options{WebDist: missing})
	if srv.StaticError() == nil {
		t.Error("不存在的 web-dist 应记录 StaticError")
	}
	if srv.WebDist() != "" {
		t.Errorf("回退后 WebDist = %q，期望空", srv.WebDist())
	}
	s2 := httptest.NewServer(srv.Handler())
	t.Cleanup(s2.Close)
	resp, body = doGet(t, s2.URL+"/")
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "web 尚未构建") {
		t.Errorf("回退占位页失败：status=%d body=%q", resp.StatusCode, body)
	}
}

// 确认 stubSidecar 满足 Sidecar 接口（编译期断言）。
var _ Sidecar = (*stubSidecar)(nil)
