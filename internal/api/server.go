// Package api 提供 Go 侧的 HTTP 服务：健康检查、点云 fixture 下发、推理接口、静态资源。
//
// 路由用标准库 net/http + ServeMux（Go 1.22+ 的 method/pattern 语法，不引入 chi，
// 见 docs/go-scope.md §1①）；静态资源在开发期从磁盘目录服务（-web-dist），
// 不传时用 //go:embed 的内嵌占位页（go:embed 不能跨包，见 docs/selection.md §5）。
package api

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strconv"

	"illust3d/internal/cloud"
	"illust3d/internal/infer"
)

// embeddedStatic 内嵌 internal/api/static/ 下的占位页面（-web-dist 未提供时兜底）。
//
//go:embed all:static
var embeddedStatic embed.FS

// 默认上限。
const (
	// defaultMaxImageBytes 是单张上传图片的字节上限（32 MiB；本机插画最大约 5 MB）。
	defaultMaxImageBytes = 32 << 20
	// defaultMaxPixels 是解码前的像素数上限，防止"解压炸弹"。
	defaultMaxPixels = 32 << 20
)

// Sidecar 是 API 层对推理后端的依赖（*infer.Manager 实现它；测试可注入假实现）。
type Sidecar interface {
	// Infer 把原始图片字节送进 sidecar，返回深度图。
	Infer(ctx context.Context, image []byte, processRes uint32) (*infer.DepthResult, error)
	// Segment 返回角色分割 mask（三档：0=背景 128=不确定 255=角色）。
	// 失败不应影响主链路：调用方据此退化（不做边界带处理）。
	Segment(ctx context.Context, image []byte) (*infer.SegResult, error)
	// Status 返回 sidecar 状态快照（非阻塞，供 /api/health 使用）。
	Status() infer.Status
}

// Options 配置 Server。
type Options struct {
	// Version 是 /api/health 返回的版本号。
	Version string
	// FixturePath 是 ILC1 点云文件路径；为空表示没有 fixture。
	FixturePath string
	// StaticFS 可替换内嵌静态资源（测试用）；nil 时按 WebDist / 内嵌占位页的顺序取。
	StaticFS fs.FS
	// WebDist 是 web 构建产物目录（如 web/dist）。非空且存在时从磁盘服务。
	WebDist string
	// Sidecar 是推理后端；nil 时 /api/infer 返回 503。
	Sidecar Sidecar
	// ProcessRes 是传给 sidecar 的 process_res（像素）；<=0 用 infer.DefaultRes。
	ProcessRes int
	// MaxImageBytes 是上传图片字节上限；<=0 用 32 MiB。
	MaxImageBytes int64
}

// Server 持有只读的 fixture、静态资源与推理后端。方法本身无状态，可被并发使用。
type Server struct {
	version       string
	static        fs.FS
	staticErr     error
	webDist       string
	fixture       []byte
	fixtureErr    error
	pointCount    int
	sidecar       Sidecar
	processRes    int
	maxImageBytes int64
	maxPixels     int64
}

// New 构造 Server。fixture 读取/校验失败不会让构造失败——服务照常启动，
// /api/health 报 degraded、/api/cloud/sample 返回 503，这样 web 侧可以先独立开发。
func New(opts Options) *Server {
	s := &Server{
		version:       opts.Version,
		static:        opts.StaticFS,
		sidecar:       opts.Sidecar,
		processRes:    opts.ProcessRes,
		maxImageBytes: opts.MaxImageBytes,
		maxPixels:     defaultMaxPixels,
	}
	if s.processRes <= 0 {
		s.processRes = infer.DefaultRes
	}
	if s.maxImageBytes <= 0 {
		s.maxImageBytes = defaultMaxImageBytes
	}

	// 静态资源优先级：显式 StaticFS → -web-dist 目录 → 内嵌占位页。
	if s.static == nil && opts.WebDist != "" {
		if info, err := os.Stat(opts.WebDist); err == nil && info.IsDir() {
			s.static = os.DirFS(opts.WebDist)
			s.webDist = opts.WebDist
		} else {
			s.staticErr = fmt.Errorf("web-dist 目录 %q 不可用: %v", opts.WebDist, err)
		}
	}
	if s.static == nil {
		sub, err := fs.Sub(embeddedStatic, "static")
		if err != nil {
			// 内嵌资源在编译期固定，子目录一定存在；走到这里说明构建被改坏了。
			panic("api: 内嵌静态资源目录缺失: " + err.Error())
		}
		s.static = sub
	}

	s.loadFixture(opts.FixturePath)
	return s
}

// loadFixture 读取并校验 ILC1 fixture，失败时只记录错误。
func (s *Server) loadFixture(path string) {
	if path == "" {
		s.fixtureErr = errors.New("未指定点云 fixture")
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		s.fixtureErr = fmt.Errorf("读取点云 fixture %q 失败: %w", path, err)
		return
	}
	n, err := cloud.PointCount(data)
	if err != nil {
		s.fixtureErr = fmt.Errorf("点云 fixture %q 不是合法 ILC1: %w", path, err)
		return
	}
	s.fixture = data
	s.pointCount = n
}

// FixtureError 返回 fixture 不可用的原因（可用时为 nil）。
func (s *Server) FixtureError() error { return s.fixtureErr }

// PointCount 返回 fixture 的点数（fixture 不可用时为 0）。
func (s *Server) PointCount() int { return s.pointCount }

// StaticError 返回静态资源目录不可用的原因（回退到内嵌占位页时为 nil 以外的值）。
func (s *Server) StaticError() error { return s.staticErr }

// WebDist 返回实际使用的 web 构建产物目录（未使用磁盘目录时为空）。
func (s *Server) WebDist() string { return s.webDist }

// Handler 返回注册好路由的 http.Handler。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/cloud/sample", s.handleSampleCloud)
	mux.HandleFunc("POST /api/infer", s.handleInfer)
	// 静态资源挂在根上；更具体的 /api/ 路由优先匹配（Go 1.22 ServeMux 规则）。
	mux.Handle("GET /", http.FileServerFS(s.static))
	return mux
}

// healthResponse 是 /api/health 的 JSON 结构。
type healthResponse struct {
	Status       string        `json:"status"`
	Version      string        `json:"version,omitempty"`
	PointCount   int           `json:"pointCount,omitempty"`
	FixtureError string        `json:"fixtureError,omitempty"`
	StaticError  string        `json:"staticError,omitempty"`
	Sidecar      *infer.Status `json:"sidecar,omitempty"`
}

// handleHealth 返回服务状态、fixture 点数与 sidecar 状态。
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	resp := healthResponse{Status: "ok", Version: s.version}
	if s.fixtureErr != nil {
		resp.Status = "degraded"
		resp.FixtureError = s.fixtureErr.Error()
	} else {
		resp.PointCount = s.pointCount
	}
	if s.staticErr != nil {
		resp.StaticError = s.staticErr.Error()
	}
	if s.sidecar != nil {
		st := s.sidecar.Status()
		resp.Sidecar = &st
		if st.Error != "" {
			resp.Status = "degraded"
		}
	}
	writeJSON(w, http.StatusOK, resp, false)
}

// handleSampleCloud 原样返回 fixture 的 ILC1 字节（浏览器零解析直接建 TypedArray）。
func (s *Server) handleSampleCloud(w http.ResponseWriter, _ *http.Request) {
	if s.fixtureErr != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": s.fixtureErr.Error(),
			"hint":  "用 -fixture <path> 指定 ILC1 点云文件（默认 testdata/sample.ilcl）",
		}, false)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(s.fixture)))
	w.Header().Set("X-ILC1-PointCount", strconv.Itoa(s.pointCount))
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write(s.fixture); err != nil {
		return // 客户端提前断开，无需上报
	}
}

// writeJSON 写 JSON 响应；noStore 为 true 时加 Cache-Control: no-store。
func writeJSON(w http.ResponseWriter, status int, v any, noStore bool) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if noStore {
		w.Header().Set("Cache-Control", "no-store")
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
