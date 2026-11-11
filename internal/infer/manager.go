// @kron:intent arch/three-way-split/health
// @kron:intent env/gpu-vram-budget
// @kron:intent geometry/sampling/resolution-budget

package infer

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

// sidecar 的默认启动配置。
const (
	// DefaultPython 是 illust3d conda 环境的解释器（torch/DA3 依赖都装在这里，见 docs/selection.md §1）。
	DefaultPython = `E:\conda\envs_dirs\illust3d\python.exe`
	// DefaultScript 是 sidecar 入口脚本（相对进程工作目录，即仓库根目录）。
	DefaultScript = `sidecar/server.py`
	// DefaultRes 是默认 process_res（像素，长边）。
	//
	// 实测（平涂 4000px 图 + 点径 3 / softness 0.25 / posterize 10，400k 点）：
	//
	//	504： 峰值显存 2160 MiB / 推理 ~0.9s —— 遮挡边界只有 1~2px，
	//	      边界像素深度混两层 → 反投影后位置错乱 → 主体边缘明显撕裂/锯齿
	//	1008：峰值显存 3027 MiB / 推理 ~2.2s —— 边界有 2~4px 过渡，锯齿基本消失
	//	      ← 新默认（8GB 显存下 <4GB 峰值，留足余量）
	//	1344：峰值显存 3919 MiB / 推理 ~3.7s —— 画质更高但太慢，留作 -sidecar-res 显式选项
	//
	// 注意：process_res 决定**几何细节的真实上限**（边界/遮挡关系），
	// 与 max_points（颜色覆盖密度）是两件事，不要互相替代。
	DefaultRes = 1008
	// maxRestarts 是 sidecar **崩溃/EOF** 后的重启上限：只重启一次，避免崩溃循环。
	maxRestarts = 1
	// maxDesyncRepairs 是 **管道失步** 后的重建上限（与崩溃重启分开计数）。
	// 失步是可自愈的：重建管道（关管道 → 杀进程 → 重启 → warmup）后重试即可；
	// 连续 3 次还失步说明 sidecar 协议实现有问题，此时报错而不是无限重建。
	maxDesyncRepairs = 3
)

// ManagerConfig 描述如何拉起 sidecar 以及各项超时。
type ManagerConfig struct {
	// Python 是解释器路径；空则用 DefaultPython。
	Python string
	// Script 是 sidecar 入口脚本；空则用 DefaultScript。
	Script string
	// ScriptArgs 是传给脚本的参数；nil 则用 ["--res", DefaultRes]。
	ScriptArgs []string
	// Dir 是工作目录；空表示继承当前进程（即仓库根目录）。
	Dir string
	// Env 是追加的环境变量；nil 表示只继承当前环境。
	Env []string
	// WarmupTimeout 是模型预热总超时（实测加载 12.3s，给 180s 余量）；<=0 用 180s。
	WarmupTimeout time.Duration
	// PingInterval 是未就绪时的轮询间隔；<=0 用 500ms。
	PingInterval time.Duration
	// PingTimeout 是单次 PING 超时；<=0 用 120s。
	PingTimeout time.Duration
	// InferTimeout 是单帧 INFER 超时；<=0 用 60s。
	InferTimeout time.Duration
	// SegmentTimeout 是单帧 SEGMENT（角色分割）超时；<=0 用 120s。
	SegmentTimeout time.Duration
	// Logf 接收日志（sidecar stderr 与生命周期事件）；nil 用标准库 log。
	Logf func(format string, args ...any)
}

// Status 是 sidecar 的可观察状态，供 GET /api/health 使用。
type Status struct {
	Ready      bool   `json:"ready"`
	Warming    bool   `json:"warming"`
	Model      string `json:"model,omitempty"`
	Device     string `json:"device,omitempty"`
	ProcessRes int    `json:"process_res,omitempty"`

	// VRAMUsedMB / VRAMPeakMB 是 sidecar 自报的 PyTorch 分配器显存（MiB）。
	VRAMUsedMB float64 `json:"vram_used_mb"`
	VRAMPeakMB float64 `json:"vram_peak_mb,omitempty"`
	// LoadSeconds 是模型加载耗时；LastInferSeconds 是最近一次推理耗时。
	LoadSeconds      float64 `json:"load_seconds,omitempty"`
	LastInferSeconds float64 `json:"last_infer_seconds"`
	// LastSegmentSeconds 是最近一次角色分割耗时（CPU 推理，约 0.7s）。
	LastSegmentSeconds float64 `json:"last_segment_seconds,omitempty"`

	// PID 是当前 sidecar 进程号；Restarts 是崩溃重启次数（上限 maxRestarts）。
	PID      int `json:"pid,omitempty"`
	Restarts int `json:"restarts"`
	// DesyncRepairs 是"管道失步"后的重建次数（独立预算，成功推理后清零）。
	DesyncRepairs int `json:"desync_repairs"`

	// Error 是预热/管道失败原因（就绪后为空）。
	Error string `json:"error,omitempty"`
}

// Manager 管理 sidecar 子进程的完整生命周期：拉起、预热、崩溃重启一次、优雅关闭。
//
// 关键约束（docs/selection.md §4.1）：**Go 退出时不能留下孤儿 Python 进程**。
// 这里依靠三件事共同保证：
//  1. 正常退出/Ctrl+C/panic 都会走到 defer 的 Close()：先 UNLOAD 再关 stdin；
//  2. sidecar 读到 stdin EOF 会自行退出（server.py 已实现），父进程强杀时管道关闭同样触发 EOF；
//  3. Close() 内部有超时兜底，超时直接 Kill 进程。
//
// Manager 可并发使用：推理请求在内部串行化（单管道），状态查询只读缓存，不会阻塞。
type Manager struct {
	cfg  ManagerConfig
	logf func(format string, args ...any)

	// opMu 串行化"推理 / 重启 / 等待就绪"这类会改变管道状态的复合操作。
	opMu sync.Mutex
	// mu 只保护下面这些短小的状态字段，不做 I/O。
	mu       sync.Mutex
	client   *Client
	warming  bool
	ready    bool
	warmErr  error
	warmDone chan struct{}
	closed   bool
	restarts int
	// desyncBudget 是"本轮"已用的失步重建次数（成功的推理会清零）；
	// 累计总数在 status.DesyncRepairs 里（health 展示，只增不清）。
	desyncBudget int
	status       Status
}

// NewManager 构造 Manager（不启动进程，需再调用 Start）。
func NewManager(cfg ManagerConfig) *Manager {
	if cfg.Python == "" {
		cfg.Python = DefaultPython
	}
	if cfg.Script == "" {
		cfg.Script = DefaultScript
	}
	if cfg.ScriptArgs == nil {
		cfg.ScriptArgs = []string{"--res", fmt.Sprint(DefaultRes)}
	}
	if cfg.WarmupTimeout <= 0 {
		cfg.WarmupTimeout = 180 * time.Second
	}
	if cfg.PingInterval <= 0 {
		cfg.PingInterval = 500 * time.Millisecond
	}
	if cfg.PingTimeout <= 0 {
		cfg.PingTimeout = 120 * time.Second
	}
	if cfg.InferTimeout <= 0 {
		cfg.InferTimeout = 60 * time.Second
	}
	if cfg.SegmentTimeout <= 0 {
		// 分割走 CPU、单张约 0.7s；留足余量给大图与冷启动（首次要加载 ONNX）。
		cfg.SegmentTimeout = 120 * time.Second
	}
	logf := cfg.Logf
	if logf == nil {
		logf = log.Printf
	}
	cfg.Logf = logf
	return &Manager{cfg: cfg, logf: logf}
}

// Start 异步拉起 sidecar 并预热：立刻返回，预热在后台进行。
// 第一个 Infer 会等待预热完成（最长 WarmupTimeout），所以不必阻塞 HTTP 服务启动。
func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	if m.closed || m.warming || m.ready {
		m.mu.Unlock()
		return
	}
	m.warming = true
	m.status.Warming = true
	m.status.Error = ""
	m.warmDone = make(chan struct{})
	done := m.warmDone
	m.mu.Unlock()

	m.logf("sidecar 预热中：%s %s %v（超时 %s）",
		m.cfg.Python, m.cfg.Script, m.cfg.ScriptArgs, m.cfg.WarmupTimeout)
	go m.warmup(ctx, done)
}

// warmup 是后台预热协程：拉进程 → 轮询 PING 直到 ready。
func (m *Manager) warmup(ctx context.Context, done chan struct{}) {
	defer close(done)

	wctx, cancel := context.WithTimeout(ctx, m.cfg.WarmupTimeout)
	defer cancel()

	err := m.launch()
	if err == nil {
		if c := m.currentClient(); c != nil {
			err = m.waitReady(wctx, c)
		} else {
			err = errors.New("infer: sidecar 客户端缺失")
		}
	}
	if err != nil {
		m.finishWarmup(err)
		return
	}
	m.markReady()
	if c := m.currentClient(); c != nil {
		st := m.Status()
		m.logf("sidecar 就绪：pid=%d model=%s device=%s vram=%.0fMiB load=%.1fs",
			c.PID(), st.Model, st.Device, st.VRAMUsedMB, st.LoadSeconds)
	}
}

// launch 拉起 sidecar 进程并登记客户端。
func (m *Manager) launch() error {
	args := make([]string, 0, 1+len(m.cfg.ScriptArgs))
	args = append(args, m.cfg.Script)
	args = append(args, m.cfg.ScriptArgs...)

	c, err := Start(context.Background(), Config{
		Path:           m.cfg.Python,
		Args:           args,
		Dir:            m.cfg.Dir,
		Env:            m.cfg.Env,
		Logf:           m.logf,
		PingTimeout:    m.cfg.PingTimeout,
		InferTimeout:   m.cfg.InferTimeout,
		SegmentTimeout: m.cfg.SegmentTimeout,
	})
	if err != nil {
		return fmt.Errorf("拉起 sidecar 失败（%s %s）: %w", m.cfg.Python, m.cfg.Script, err)
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		_ = c.Close()
		return ErrClosed
	}
	m.client = c
	m.status.PID = c.PID()
	m.mu.Unlock()

	m.logf("sidecar 进程已启动 pid=%d：%s %s %v", c.PID(), m.cfg.Python, m.cfg.Script, m.cfg.ScriptArgs)
	return nil
}

// waitReady 反复 PING 直到 ready 或 ctx 到期。
func (m *Manager) waitReady(ctx context.Context, c *Client) error {
	var lastErr error
	for {
		info, err := c.Ping(ctx)
		if err == nil {
			m.applyPing(info)
			if info.Ready {
				return nil
			}
			m.logf("sidecar PING 正常但模型未就绪（load_seconds=%v），继续等待", derefFloat(info.LoadSeconds))
		} else if ctx.Err() != nil {
			return fmt.Errorf("等待 sidecar 就绪超时（最后一次错误: %v）: %w", err, ctx.Err())
		} else if errors.Is(err, ErrProtocol) {
			return fmt.Errorf("sidecar 管道错误（进程可能已退出）: %w", err)
		}
		lastErr = err

		select {
		case <-ctx.Done():
			return fmt.Errorf("等待 sidecar 就绪超时（最后一次错误: %v）: %w", lastErr, ctx.Err())
		case <-time.After(m.cfg.PingInterval):
		}
	}
}

// applyPing 把 PING 结果写入缓存状态。
func (m *Manager) applyPing(info PingInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status.Model = info.Model
	m.status.Device = info.Device
	m.status.ProcessRes = info.ProcessRes
	m.status.VRAMUsedMB = info.VRAMUsedMB
	m.status.VRAMPeakMB = info.VRAMPeakMB
	if info.LoadSeconds != nil {
		m.status.LoadSeconds = *info.LoadSeconds
	}
	if info.LastInferSeconds != nil {
		m.status.LastInferSeconds = *info.LastInferSeconds
	}
}

func (m *Manager) finishWarmup(err error) {
	m.mu.Lock()
	m.warming = false
	m.ready = false
	m.warmErr = err
	m.status.Warming = false
	m.status.Ready = false
	m.status.Error = err.Error()
	m.mu.Unlock()
	if errors.Is(err, ErrClosed) {
		return
	}
	m.logf("sidecar 预热失败：%v（/api/infer 将返回 503；第一次请求会再尝试重启一次）", err)
}

func (m *Manager) markReady() {
	m.mu.Lock()
	m.warming = false
	m.ready = true
	m.warmErr = nil
	m.status.Warming = false
	m.status.Ready = true
	m.status.Error = ""
	m.mu.Unlock()
}

// currentClient 返回当前客户端（可能为 nil）。
func (m *Manager) currentClient() *Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.client
}

// Status 返回当前 sidecar 状态快照（非阻塞，纯内存读取）。
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.status
	s.Ready = m.ready
	s.Warming = m.warming
	if m.warmErr != nil && !m.ready {
		s.Error = m.warmErr.Error()
	}
	return s
}

// markNotReady 把 sidecar 标记为不可用（health 必须如实反映：ready=false + 原因）。
func (m *Manager) markNotReady(err error) {
	if err == nil {
		return
	}
	m.mu.Lock()
	m.ready = false
	m.warming = false
	m.warmErr = err
	m.status.Ready = false
	m.status.Warming = false
	m.status.Error = err.Error()
	m.mu.Unlock()
	m.logf("sidecar 不可用：%v（/api/health 会显示 ready=false / degraded；desync_repairs=%d）", err, m.Status().DesyncRepairs)
}

// WaitReady 阻塞到 sidecar 就绪（或 ctx 结束 / 不可用）。
func (m *Manager) WaitReady(ctx context.Context) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	return m.waitReadyOp(ctx)
}

// waitReadyOp 是已持有 opMu 时的等待逻辑；预热失败时用掉唯一的重启预算。
func (m *Manager) waitReadyOp(ctx context.Context) error {
	for {
		m.mu.Lock()
		if m.ready {
			m.mu.Unlock()
			return nil
		}
		if m.closed {
			m.mu.Unlock()
			return ErrClosed
		}
		if m.warming {
			done := m.warmDone
			m.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		warmErr := m.warmErr
		canRestart := m.restarts < maxRestarts
		m.mu.Unlock()

		if canRestart {
			m.logf("sidecar 未就绪，尝试重启（唯一一次崩溃预算）：%v", warmErr)
			if err := m.restartOp(); err != nil {
				return fmt.Errorf("sidecar 重启失败: %w（初始错误: %v）", err, warmErr)
			}
			continue
		}
		if warmErr != nil {
			return fmt.Errorf("sidecar 不可用: %w", warmErr)
		}
		return errors.New("infer: sidecar 未就绪")
	}
}

// restartOp 处理**进程崩溃/EOF**：关进程 → 拉起新进程 → warmup。调用方必须持有 opMu。
func (m *Manager) restartOp() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrClosed
	}
	if m.restarts >= maxRestarts {
		m.mu.Unlock()
		return fmt.Errorf("已达崩溃重启上限（%d 次）", maxRestarts)
	}
	m.restarts++
	m.status.Restarts = m.restarts
	old := m.client
	m.client = nil
	m.ready = false
	m.warming = true
	m.warmErr = nil
	m.status.Ready = false
	m.status.Warming = true
	m.status.Error = "进程崩溃，正在重启"
	m.mu.Unlock()

	m.logf("重启 sidecar（第 %d 次）", m.restarts)
	if err := m.rebuildSidecar(old); err != nil {
		m.markNotReady(err)
		return err
	}
	return nil
}

// repairDesyncOp 处理**管道失步**：重建整条管道并重新 warmup，然后由调用方重试。
// 使用独立预算（maxDesyncRepairs），与崩溃重启互不影响；成功推理后预算清零。
func (m *Manager) repairDesyncOp(cause error) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrClosed
	}
	if m.desyncBudget >= maxDesyncRepairs {
		m.mu.Unlock()
		return fmt.Errorf("已连续重建管道 %d 次仍失步，放弃（需检查 sidecar 协议实现）", maxDesyncRepairs)
	}
	m.desyncBudget++
	m.status.DesyncRepairs++ // health 展示累计重建次数（只增不清）
	old := m.client
	m.client = nil
	m.ready = false
	m.warming = true
	m.warmErr = nil
	m.status.Ready = false
	m.status.Warming = true
	m.status.Error = fmt.Sprintf("管道失步，正在重建（第 %d/%d 次）", m.desyncBudget, maxDesyncRepairs)
	m.mu.Unlock()

	m.logf("sidecar 管道失步（本轮第 %d/%d 次，累计 %d 次）：%v",
		m.desyncBudget, maxDesyncRepairs, m.Status().DesyncRepairs, cause)
	if err := m.rebuildSidecar(old); err != nil {
		m.markNotReady(err)
		return err
	}
	return nil
}

// rebuildSidecar 关闭旧客户端（关 stdin → 等进程退出）、拉起新进程并重新 warmup。
// 用独立 context（不跟随调用方请求取消）：重建一旦开始就要走完，否则状态不一致。
func (m *Manager) rebuildSidecar(old *Client) error {
	if old != nil {
		_ = old.Close()
	}
	if err := m.launch(); err != nil {
		return err
	}
	c := m.currentClient()
	if c == nil {
		return errors.New("infer: 重建后客户端丢失")
	}
	rctx, cancel := context.WithTimeout(context.Background(), m.cfg.WarmupTimeout)
	defer cancel()
	if err := m.waitReady(rctx, c); err != nil {
		return err
	}
	m.markReady()
	return nil
}

// Infer 发送图片做推理。
//
// 健壮性约定（线上事故修复）：
//  1. sidecar 调用使用**独立于 HTTP 请求的 context**：客户端断开时也把响应读完，
//     保证管道永远成帧——这是"一次中断不再让服务变砖"的根因修复；
//  2. 管道失步（ErrDesync）→ 重建管道后重试，独立预算 maxDesyncRepairs；
//  3. 进程崩溃/EOF → 沿用崩溃重启预算 maxRestarts；
//  4. 出错后 health 必须如实反映（ready=false + 原因）。
func (m *Manager) Infer(ctx context.Context, image []byte, processRes uint32) (*DepthResult, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	if err := m.waitReadyOp(ctx); err != nil {
		return nil, err
	}
	c := m.currentClient()
	if c == nil {
		return nil, errors.New("infer: sidecar 客户端缺失")
	}

	start := time.Now()
	res, err := m.inferOnce(c, image, processRes)

	// ① 管道失步：重建管道（独立 3 次预算）后重试。
	for repairs := 0; err != nil && errors.Is(err, ErrDesync) && repairs < maxDesyncRepairs; repairs++ {
		m.logf("推理时 sidecar 管道失步，重建管道后重试：%v", err)
		if rerr := m.repairDesyncOp(err); rerr != nil {
			m.markNotReady(rerr) // health 必须反映不可用
			return nil, fmt.Errorf("sidecar 管道重建失败: %w（原始错误: %v）", rerr, err)
		}
		c = m.currentClient()
		if c == nil {
			return nil, errors.New("infer: sidecar 客户端缺失")
		}
		res, err = m.inferOnce(c, image, processRes)
	}

	// ② 进程崩溃/EOF：沿用"重启一次"预算。
	if err != nil && m.shouldRestart(err) {
		m.logf("推理时 sidecar 进程错误，重启一次后重试：%v", err)
		if rerr := m.restartOp(); rerr != nil {
			m.markNotReady(rerr) // 重启预算耗尽也必须诚实报告，不能假装 ready
			return nil, fmt.Errorf("sidecar 重启失败: %w（原始错误: %v）", rerr, err)
		}
		c = m.currentClient()
		if c == nil {
			return nil, errors.New("infer: sidecar 客户端缺失")
		}
		res, err = m.inferOnce(c, image, processRes)
	}

	if err != nil {
		// 管道/进程类错误：health 必须显示不可用，不能假装 ready。
		if isSidecarBroken(err) {
			m.markNotReady(err)
		}
		return nil, err
	}

	m.mu.Lock()
	m.status.LastInferSeconds = time.Since(start).Seconds()
	m.ready = true
	m.warmErr = nil
	m.status.Ready = true
	m.status.Error = ""
	// 这次推理成功了：失步重建预算清零（累计次数保留，供 health 观察）。
	m.desyncBudget = 0
	m.mu.Unlock()

	// 调用方在推理期间取消了：结果丢弃（管道已同步），交给 HTTP 层返回非 2xx。
	if cerr := ctx.Err(); cerr != nil {
		return nil, fmt.Errorf("推理完成但请求已取消（结果丢弃，管道保持同步）: %w", cerr)
	}
	return res, nil
}

// inferOnce 用**独立于 HTTP 请求的 context** 调 sidecar。
// 客户端取消/断开时仍然把响应读完（结果丢弃），这是防止管道失步的关键。
func (m *Manager) inferOnce(c *Client, image []byte, processRes uint32) (*DepthResult, error) {
	opCtx, cancel := context.WithTimeout(context.Background(), m.cfg.InferTimeout)
	defer cancel()
	return c.Infer(opCtx, image, processRes)
}

// Segment 请求角色分割 mask。纪律与 Infer 完全一致（独立 context、失步重建、崩溃重启）。
//
// 为什么同样严格：SEGMENT 和 INFER 共用同一条管道，任何一边留下半帧都会让另一边失步。
func (m *Manager) Segment(ctx context.Context, image []byte) (*SegResult, error) {
	m.opMu.Lock()
	defer m.opMu.Unlock()

	if err := m.waitReadyOp(ctx); err != nil {
		return nil, err
	}
	c := m.currentClient()
	if c == nil {
		return nil, errors.New("infer: sidecar 客户端缺失")
	}

	start := time.Now()
	res, err := m.segmentOnce(c, image)

	for repairs := 0; err != nil && errors.Is(err, ErrDesync) && repairs < maxDesyncRepairs; repairs++ {
		m.logf("分割时 sidecar 管道失步，重建管道后重试：%v", err)
		if rerr := m.repairDesyncOp(err); rerr != nil {
			m.markNotReady(rerr)
			return nil, fmt.Errorf("sidecar 管道重建失败: %w（原始错误: %v）", rerr, err)
		}
		c = m.currentClient()
		if c == nil {
			return nil, errors.New("infer: sidecar 客户端缺失")
		}
		res, err = m.segmentOnce(c, image)
	}

	if err != nil && m.shouldRestart(err) {
		m.logf("分割时 sidecar 进程错误，重启一次后重试：%v", err)
		if rerr := m.restartOp(); rerr != nil {
			m.markNotReady(rerr)
			return nil, fmt.Errorf("sidecar 重启失败: %w（原始错误: %v）", rerr, err)
		}
		c = m.currentClient()
		if c == nil {
			return nil, errors.New("infer: sidecar 客户端缺失")
		}
		res, err = m.segmentOnce(c, image)
	}

	if err != nil {
		if isSidecarBroken(err) {
			m.markNotReady(err)
		}
		return nil, err
	}

	m.mu.Lock()
	m.status.LastSegmentSeconds = time.Since(start).Seconds()
	m.mu.Unlock()

	if cerr := ctx.Err(); cerr != nil {
		return nil, fmt.Errorf("分割完成但请求已取消（结果丢弃，管道保持同步）: %w", cerr)
	}
	return res, nil
}

// segmentOnce 用独立 context 调 SEGMENT。
func (m *Manager) segmentOnce(c *Client, image []byte) (*SegResult, error) {
	opCtx, cancel := context.WithTimeout(context.Background(), m.cfg.SegmentTimeout)
	defer cancel()
	return c.Segment(opCtx, image)
}

// shouldRestart 判断错误是否属于"进程崩溃/EOF"，值得用崩溃预算重启一次。
// 管道失步走独立的 repairDesyncOp；业务错误（RemoteError）不重启。
func (m *Manager) shouldRestart(err error) bool {
	if err == nil {
		return false
	}
	var remote *RemoteError
	if errors.As(err, &remote) {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return errors.Is(err, ErrProtocol) && !errors.Is(err, ErrDesync)
}

// isSidecarBroken 报告错误是否意味着 sidecar/管道已不可用（health 应显示 ready=false）。
func isSidecarBroken(err error) bool {
	if err == nil {
		return false
	}
	var remote *RemoteError
	if errors.As(err, &remote) {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return errors.Is(err, ErrProtocol) || errors.Is(err, ErrDesync) || errors.Is(err, ErrClosed)
}

// PID 返回当前 sidecar 进程号（无进程时 0）。
func (m *Manager) PID() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.client == nil {
		return 0
	}
	return m.client.PID()
}

// Close 优雅关闭 sidecar：先 UNLOAD 释放显存，再关管道让进程退出；超时强杀兜底。
// 重复调用安全。返回 nil 表示已尽力清理（UNLOAD 失败不影响进程清理）。
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	c := m.client
	m.client = nil
	m.ready = false
	m.status.Ready = false
	m.status.Warming = false
	m.mu.Unlock()

	if c == nil {
		return nil
	}
	m.logf("关闭 sidecar（先 UNLOAD 释放显存，再关 stdin）…")

	done := make(chan struct{})
	go func() {
		defer close(done)
		uctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := c.Unload(uctx); err != nil {
			m.logf("sidecar UNLOAD 失败（忽略，继续关进程）：%v", err)
		}
		_ = c.Close()
	}()

	select {
	case <-done:
		m.logf("sidecar 已退出")
	case <-time.After(10 * time.Second):
		m.logf("sidecar 关闭超时，强制结束进程 pid=%d", c.PID())
		_ = c.Kill()
	}
	return nil
}

// derefFloat 安全解引用（nil → 0）。
func derefFloat(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}
