// Command illust3d 是本项目的唯一入口：插画 → 点云 → 风格化渲染。
//
// 服务接口（单二进制，标准库 net/http）：
//   - POST /api/infer         插画 → 深度（Python sidecar）→ 点云（Go 几何）→ ILC1
//   - GET  /api/health        服务状态 + fixture 点数 + sidecar 状态（ready/显存/耗时）
//   - GET  /api/cloud/sample  样例点云 ILC1（浏览器可直接吃）
//   - GET  /                  web 静态资源（-web-dist 目录，缺省用内嵌占位页）
//
// sidecar 由本进程用 os/exec 拉起（stdio 帧协议，见 docs/selection.md §4.1）；
// 退出时先 UNLOAD 再关管道，保证不留孤儿 Python 进程。
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
	"strconv"
	"syscall"
	"time"

	"illust3d/internal/api"
	"illust3d/internal/infer"
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
	os.Exit(run())
}

// run 是真正的 main：退出路径统一收敛到 return，确保 defer（尤其是 sidecar 清理）一定执行。
//
// 注意：sidecar 启动后**禁止**使用 log.Fatalf / os.Exit —— 它们会跳过 defer，
// 可能留下孤儿 Python 进程（docs/selection.md §4.1 的"父死子随"是这条线的核心约束）。
func run() int {
	addr := flag.String("addr", defaultAddr, "HTTP 监听地址")
	fixture := flag.String("fixture", defaultFixture, "ILC1 点云 fixture 路径")
	webDist := flag.String("web-dist", "", "web 构建产物目录（如 web/dist）；空则用内嵌占位页")
	sidecarPython := flag.String("sidecar-python", infer.DefaultPython, "Python 解释器路径（illust3d conda 环境）")
	sidecarScript := flag.String("sidecar-script", infer.DefaultScript, "sidecar 入口脚本路径")
	sidecarRes := flag.Int("sidecar-res", infer.DefaultRes, "sidecar 的 process_res（像素）")
	shutdownAfter := flag.Duration("shutdown-after", 0, "调试/验收用：启动 N 秒后优雅退出（0 = 不自动退出）")
	panicAfter := flag.Duration("panic-after", 0, "调试/验收用：启动 N 秒后主动 panic（验证 defer 链能清理 sidecar；0 = 不 panic）")
	showVersion := flag.Bool("version", false, "打印版本号后退出")
	flag.Parse()

	if *showVersion {
		fmt.Printf("illust3d %s\n", version)
		return 0
	}

	log.SetFlags(0)
	log.SetPrefix("illust3d: ")

	// Ctrl+C / SIGTERM / -shutdown-after 都走同一条优雅退出路径。
	sigCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	ctx, cancel := context.WithCancel(sigCtx)
	defer cancel()
	if *shutdownAfter > 0 {
		log.Printf("调试模式：%s 后自动优雅退出", *shutdownAfter)
		go func() {
			select {
			case <-time.After(*shutdownAfter):
				cancel()
			case <-ctx.Done():
			}
		}()
	}

	// sidecar：异步预热，HTTP 先起来；第一个 /api/infer 会等预热完成。
	mgr := infer.NewManager(infer.ManagerConfig{
		Python:     *sidecarPython,
		Script:     *sidecarScript,
		ScriptArgs: []string{"--res", strconv.Itoa(*sidecarRes)},
		Logf:       log.Printf,
	})
	defer func() {
		if err := mgr.Close(); err != nil {
			log.Printf("关闭 sidecar 出错: %v", err)
		}
	}()
	mgr.Start(ctx)

	srv := api.New(api.Options{
		Version:     version,
		FixturePath: *fixture,
		WebDist:     *webDist,
		Sidecar:     mgr,
		ProcessRes:  *sidecarRes,
	})

	if err := srv.FixtureError(); err != nil {
		log.Printf("警告：点云 fixture 不可用：%v", err)
		log.Printf("  /api/cloud/sample 将返回 503。用 -fixture <path> 指定 ILC1 文件。")
	} else {
		log.Printf("点云 fixture：%s（%d 点）", *fixture, srv.PointCount())
	}
	if err := srv.StaticError(); err != nil {
		log.Printf("警告：%v（回退到内嵌占位页）", err)
	}
	if dir := srv.WebDist(); dir != "" {
		log.Printf("静态资源：%s（磁盘目录）", dir)
	} else {
		log.Printf("静态资源：内嵌占位页（未指定 -web-dist）")
	}

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("服务已启动：http://%s", *addr)
		log.Printf("  POST /api/infer  GET /api/health  GET /api/cloud/sample  GET /")
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("监听 %s 失败（端口被占用？可用 -addr 换端口）: %w", *addr, err)
			return
		}
		errCh <- nil
	}()

	// -panic-after：在主 goroutine 主动 panic，验证 defer 链（sidecar 清理）在崩溃路径也生效。
	var panicCh <-chan time.Time
	if *panicAfter > 0 {
		log.Printf("调试模式：%s 后主动 panic（验证崩溃路径不留孤儿进程）", *panicAfter)
		panicCh = time.After(*panicAfter)
	}

	select {
	case <-ctx.Done():
		log.Printf("收到退出信号，正在关闭……")
	case err := <-errCh:
		if err != nil {
			log.Printf("服务异常退出: %v", err)
			return 1
		}
		return 0
	case <-panicCh:
		panic("测试用：-panic-after 主动 panic（验证 defer 链会清理 sidecar）")
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancelShutdown()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP 关闭超时/出错（继续清理 sidecar）: %v", err)
	}
	log.Printf("HTTP 已停止，正在关闭 sidecar……")
	return 0
}
