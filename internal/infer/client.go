package infer

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"time"
)

// 默认超时。
const (
	defaultPingTimeout   = 120 * time.Second // 首次 PING 可能触发模型加载（30~120s）
	defaultInferTimeout  = 60 * time.Second
	defaultUnloadTimeout = 10 * time.Second
	closeGrace           = 5 * time.Second
)

// Config 描述如何拉起 sidecar 以及各项超时。
type Config struct {
	// Path 是 sidecar 可执行文件（通常是 python 解释器或一个 exe），必填。
	Path string
	// Args 是传给 sidecar 的参数。
	Args []string
	// Dir 是 sidecar 的工作目录；空表示继承当前进程。
	Dir string
	// Env 是要追加的环境变量；nil 表示只继承当前环境。
	Env []string
	// PingTimeout 是 PING 响应超时；<=0 用默认 120s。
	PingTimeout time.Duration
	// InferTimeout 是单帧 INFER 响应超时；<=0 用默认 60s。
	InferTimeout time.Duration
	// UnloadTimeout 是 UNLOAD 响应超时；<=0 用默认 10s。
	UnloadTimeout time.Duration
	// Logf 接收 sidecar 的 stderr 行；nil 时用标准库 log。
	Logf func(format string, args ...any)
}

// Client 是 sidecar 的 stdio 客户端。
//
// 请求在内部串行化（只有一条管道），因此可被多个 goroutine 并发调用，但会排队。
// 超时或管道层错误后客户端进入"已损坏"状态，后续调用直接返回该错误；
// 需要继续使用请重新 Start（进程管理/重启策略由上层编排负责）。
type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	logf   func(format string, args ...any)

	pingTimeout   time.Duration
	inferTimeout  time.Duration
	unloadTimeout time.Duration

	mu     sync.Mutex
	closed bool
	broken error // 非 nil：管道已损坏，不再接受新请求
}

// Start 拉起 sidecar 进程并返回客户端。
//
// ctx 只用于启动阶段的取消检查，不决定进程生命周期：进程的生命周期由 Close 管理。
func Start(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Path == "" {
		return nil, errors.New("infer: Config.Path 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	cmd := exec.Command(cfg.Path, cfg.Args...)
	cmd.Dir = cfg.Dir
	if cfg.Env != nil {
		cmd.Env = append(os.Environ(), cfg.Env...)
	}
	// 日志一律走 stderr，stdout 只放协议帧。
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("infer: 创建 stderr 管道失败: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("infer: 创建 stdout 管道失败: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("infer: 创建 stdin 管道失败: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("infer: 启动 sidecar 失败（%s）: %w", cfg.Path, err)
	}

	logf := cfg.Logf
	if logf == nil {
		logf = log.Printf
	}
	c := &Client{
		cmd:           cmd,
		stdin:         stdin,
		stdout:        bufio.NewReaderSize(stdout, 1<<20),
		logf:          logf,
		pingTimeout:   defaultPingTimeout,
		inferTimeout:  defaultInferTimeout,
		unloadTimeout: defaultUnloadTimeout,
	}
	if cfg.PingTimeout > 0 {
		c.pingTimeout = cfg.PingTimeout
	}
	if cfg.InferTimeout > 0 {
		c.inferTimeout = cfg.InferTimeout
	}
	if cfg.UnloadTimeout > 0 {
		c.unloadTimeout = cfg.UnloadTimeout
	}

	go c.forwardStderr(stderr)
	return c, nil
}

// forwardStderr 把 sidecar 的 stderr 逐行转发到日志。
func (c *Client) forwardStderr(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		c.logf("sidecar stderr: %s", sc.Text())
	}
}

// PID 返回 sidecar 进程号（未启动成功时返回 0）。
func (c *Client) PID() int {
	if c == nil || c.cmd == nil || c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}

// Ping 发送 PING，解析 model/device/vram_used_mb/ready。
func (c *Client) Ping(ctx context.Context) (PingInfo, error) {
	body, err := c.call(ctx, CmdPing, 0, nil, c.pingTimeout)
	if err != nil {
		return PingInfo{}, err
	}
	return decodePing(body)
}

// Infer 发送 INFER，返回解码后的深度（及可选置信度）图。
// image 是原始图片字节，不做任何重新编码；processRes 为 0 表示用 sidecar 默认分辨率。
func (c *Client) Infer(ctx context.Context, image []byte, processRes uint32) (*DepthResult, error) {
	body, err := c.call(ctx, CmdInfer, processRes, image, c.inferTimeout)
	if err != nil {
		return nil, err
	}
	return decodeInfer(body)
}

// Unload 让 sidecar 释放模型/显存。
func (c *Client) Unload(ctx context.Context) error {
	_, err := c.call(ctx, CmdUnload, 0, nil, c.unloadTimeout)
	return err
}

// call 完成一次"写请求 + 读响应"，并在超时/管道错误后把客户端标记为损坏。
func (c *Client) call(ctx context.Context, cmd byte, arg uint32, body []byte, timeout time.Duration) ([]byte, error) {
	req, err := encodeRequest(cmd, arg, body)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrClosed
	}
	if c.broken != nil {
		return nil, c.broken
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	if _, err := c.stdin.Write(req); err != nil {
		c.broken = fmt.Errorf("%w: 写请求失败（sidecar 可能已退出）: %w", ErrProtocol, err)
		return nil, c.broken
	}

	// 读操作放到 goroutine 里，这样 ctx 超时时能返回而不是永久阻塞。
	type result struct {
		body []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		b, err := readResponse(c.stdout, cmd)
		ch <- result{body: b, err: err}
	}()

	select {
	case <-ctx.Done():
		// 读 goroutine 可能还阻塞在管道上：报废客户端，避免并发读写同一条管道。
		c.broken = fmt.Errorf("%w: 等待 %s 响应超时: %w", ErrProtocol, cmdName(cmd), ctx.Err())
		return nil, c.broken
	case r := <-ch:
		if r.err != nil {
			var remote *RemoteError
			if errors.As(r.err, &remote) {
				// 远端业务错误：帧完整、管道仍然可用。
				return nil, r.err
			}
			c.broken = r.err
			return nil, r.err
		}
		return r.body, nil
	}
}

// Close 优雅关闭：先关 stdin 让 sidecar 读到 EOF 自行退出，超时则强杀。
// 重复调用安全。返回 Wait 的错误（正常退出为 nil，被强杀时可能非 nil）。
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()

	_ = c.stdin.Close()
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(closeGrace):
		_ = c.cmd.Process.Kill()
		return <-done
	}
}

// Kill 立即强杀子进程，不等待 UNLOAD/优雅退出。
// 仅用于关闭超时兜底（例如推理卡住、管道阻塞）；正常路径请用 Close。
func (c *Client) Kill() error {
	if c == nil || c.cmd == nil || c.cmd.Process == nil {
		return nil
	}
	return c.cmd.Process.Kill()
}
