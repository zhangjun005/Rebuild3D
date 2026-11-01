// Package api 提供 Go 侧的 HTTP 服务：健康检查、点云 fixture 下发、静态资源。
//
// 路由用标准库 net/http + ServeMux（Go 1.22+ 的 method/pattern 语法，不引入 chi，
// 见 docs/go-scope.md §1①）；静态资源用 go:embed 打进单二进制。
//
// 当前阶段只做 v1 的第一块（服务骨架 + fixture 下发），点云生成/SSE 编排在后续接。
package api

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strconv"

	"illust3d/internal/cloud"
)

// embeddedStatic 内嵌 internal/api/static/ 下的占位页面。
// web 尚未接入时用它保证 GET / 不 404；将来换成 web/dist 的构建产物。
//
//go:embed all:static
var embeddedStatic embed.FS

// Options 配置 Server。
type Options struct {
	// Version 是 /api/health 返回的版本号。
	Version string
	// FixturePath 是 ILC1 点云文件路径；为空表示没有 fixture。
	FixturePath string
	// StaticFS 可替换内嵌静态资源（测试用）；nil 表示用 //go:embed 的 static/。
	StaticFS fs.FS
}

// Server 持有只读的 fixture 与静态资源。方法本身无状态，可被并发使用。
type Server struct {
	version    string
	static     fs.FS
	fixture    []byte
	fixtureErr error
	pointCount int
}

// New 构造 Server。fixture 读取/校验失败不会让构造失败——服务照常启动，
// /api/health 报 degraded、/api/cloud/sample 返回 503，这样 web 侧可以先独立开发。
func New(opts Options) *Server {
	s := &Server{version: opts.Version, static: opts.StaticFS}
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

// Handler 返回注册好路由的 http.Handler。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/cloud/sample", s.handleSampleCloud)
	// 静态资源挂在根上；更具体的 /api/ 路由优先匹配（Go 1.22 ServeMux 规则）。
	mux.Handle("GET /", http.FileServerFS(s.static))
	return mux
}

// healthResponse 是 /api/health 的 JSON 结构。
type healthResponse struct {
	Status       string `json:"status"`
	Version      string `json:"version,omitempty"`
	PointCount   int    `json:"pointCount,omitempty"`
	FixtureError string `json:"fixtureError,omitempty"`
}

// handleHealth 返回服务状态与 fixture 点数。
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	resp := healthResponse{Status: "ok", Version: s.version}
	if s.fixtureErr != nil {
		resp.Status = "degraded"
		resp.FixtureError = s.fixtureErr.Error()
	} else {
		resp.PointCount = s.pointCount
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
