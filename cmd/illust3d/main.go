// Command illust3d 是本项目的唯一入口：插画 → 点云 → 风格化渲染。
//
// 当前阶段提供 Go 侧 HTTP 服务骨架（单二进制）：
//   - GET /api/health         服务状态 + 点云点数
//   - GET /api/cloud/sample   ILC1 点云二进制（原样返回 fixture）
//   - GET /                   静态占位页（web 构建后替换为 web/dist）
//
// 深度推理 sidecar（internal/infer）与几何管线（internal/geometry）已经就绪，
// 但 Python sidecar 尚未实现，因此本阶段还没有 /api/infer 之类的端到端接线。
// 阶段计划见 docs/scope.md，接口规范见 docs/selection.md。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"illust3d/internal/api"
)

// version 由 -ldflags 注入，例如
//
//	go build -ldflags "-X main.version=v0.1.0" ./cmd/illust3d
var version = "0.0.0-dev"

const (
	// 默认端口 18123：本机 8123 被无关系统进程（ParkCloud）占用，
	// 见 docs/selection.md §4.5。换端口用 -addr。
	defaultAddr    = "127.0.0.1:18123"
	defaultFixture = "testdata/sample.ilcl"
	shutdownGrace  = 5 * time.Second
)

func main() {
	addr := flag.String("addr", defaultAddr, "HTTP 监听地址")
	fixture := flag.String("fixture", defaultFixture, "ILC1 点云 fixture 路径")
	showVersion := flag.Bool("version", false, "打印版本号后退出")
	flag.Parse()

	if *showVersion {
		fmt.Printf("illust3d %s\n", version)
		return
	}

	log.SetFlags(0)
	log.SetPrefix("illust3d: ")

	srv := api.New(api.Options{Version: version, FixturePath: *fixture})
	if err := srv.FixtureError(); err != nil {
		log.Printf("警告：点云 fixture 不可用：%v", err)
		log.Printf("  /api/cloud/sample 将返回 503。用 -fixture <path> 指定 ILC1 文件。")
	} else {
		log.Printf("点云 fixture：%s（%d 点）", *fixture, srv.PointCount())
	}

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Ctrl+C / SIGTERM 时优雅关闭。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Printf("服务已启动：http://%s", *addr)
		log.Printf("  GET /api/health  GET /api/cloud/sample  GET /")
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("监听 %s 失败（端口被占用？可用 -addr 换端口）: %w", *addr, err)
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		log.Printf("收到退出信号，正在关闭……")
	case err := <-errCh:
		if err != nil {
			log.Fatalf("服务异常退出: %v", err)
		}
		return
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("关闭超时，强制退出: %v", err)
	}
}
