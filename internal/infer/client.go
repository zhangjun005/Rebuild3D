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
	defaultPingTimeout  = 120 * time.Second // 首次 PING 可能触发模型加载（30~120s）
	defaultInferTimeout = 60 * time.Second
	// defaultSegmentTimeout 给 CPU 上的 ONNX 分割留足时间（单张约 0.7s，大图更久）。
	defaultSegmentTimeout = 120 * time.Second
	defaultUnloadTimeout  = 10 * time.Second
	closeGrace            = 5 * time.Second
	// drainGrace 是"调用被取消后，允许等这一帧读完"的上限。
	// 读完 → 管道保持同步；读不完 → 判定失步，交给上层重建管道。
	drainGrace = 10 * time.Second
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
	// SegmentTimeout 是 SEGMENT（角色分割）响应超时；<=0 用默认 120s。
	SegmentTimeout time.Duration
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

	pingTimeout    time.Duration
	inferTimeout   time.Duration
	segmentTimeout time.Duration
	unloadTimeout  time.Duration

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
		cmd:            cmd,
		stdin:          stdin,
		stdout:         bufio.NewReaderSize(stdout, 1<<20),
		logf:           logf,
		pingTimeout:    defaultPingTimeout,
		inferTimeout:   defaultInferTimeout,
		segmentTimeout: defaultSegmentTimeout,
		unloadTimeout:  defaultUnloadTimeout,
	}
	if cfg.PingTimeout > 0 {
		c.pingTimeout = cfg.PingTimeout
	}
	if cfg.InferTimeout > 0 {
		c.inferTimeout = cfg.InferTimeout
	}
	if cfg.SegmentTimeout > 0 {
		c.segmentTimeout = cfg.SegmentTimeout
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

// Segment 请求角色分割 mask（sidecar 用 anime-seg 的 ONNX，CPU 推理）。
//
// image 与 Infer 用同一份原始图片字节。SEGMENT 与 INFER 是独立命令，
// 便于分别测试和降级：分割失败不应该影响深度这条主链路。
func (c *Client) Segment(ctx context.Context, image []byte) (*SegResult, error) {
	body, err := c.call(ctx, CmdSegment, 0, image, c.segmentTimeout)
	if err != nil {
		return nil, err
	}
	return decodeSegment(body)
}

// call 完成一次"写请求 + 读响应"。
//
// 管道纪律（这个函数是整条链路的根）：
//  1. 请求与响应必须**成对消费**：绝不能在"响应只读了一半"时把管道交还给下一个请求，
//     否则剩余字节会被当成下一帧的 total_len，管道永久失步（线上事故：926505755）。
//  2. 父 ctx 取消时：继续把这帧读完（结果丢弃），保持管道同步；只有读不干净才报废管道。
//  3. 只有真正的协议错位（ErrDesync）或 EOF/崩溃才把客户端标记为 broken，交给上层重建。
func (c *Client) call(ctx context.Context, cmd byte, arg uint32, body []byte, timeout time.Duration) ([]byte, error) {
	req, err := encodeRequest(cmd, arg, body)
	if err != nil {
		return nil, err
	}
	// 调用方已经取消：一个字节都不要写（避免留下半帧）。管道保持干净。
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

	// 读操作放到 goroutine 里：这样 ctx 取消时还能选择"等它读完"（排空管道）。
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
	case r := <-ch:
		return c.accept(cmd, r.body, r.err)
	case <-ctx.Done():
		// 父 ctx 取消/超时：**必须把这一帧读完**，否则剩余字节会污染下一帧。
		select {
		case r := <-ch:
			if r.err != nil {
				return c.accept(cmd, nil, r.err) // 读这一帧时管道已坏：优先报告管道错误
			}
			// 管道已同步，但结果没人要了：丢弃并返回 ctx 错误。
			return nil, fmt.Errorf("infer: 等待 %s 响应的调用被取消（已排空响应，管道保持同步）: %w", cmdName(cmd), ctx.Err())
		case <-time.After(drainGrace):
			// 排不干净（sidecar 卡死/半帧）：管道不可再信任，交给上层重建。
			c.broken = fmt.Errorf("%w: %w: 等待 %s 响应被取消后 %v 内仍未排空管道",
				ErrProtocol, ErrDesync, cmdName(cmd), drainGrace)
			return nil, c.broken
		}
	}
}

// accept 统一处理一次读完的响应：业务错误不损坏管道；协议/崩溃错误标记 broken。
func (c *Client) accept(cmd byte, body []byte, err error) ([]byte, error) {
	if err == nil {
		return body, nil
	}
	var remote *RemoteError
	if errors.As(err, &remote) {
		return nil, err // 帧完整、管道仍然可用
	}
	c.broken = err
	return nil, err
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
