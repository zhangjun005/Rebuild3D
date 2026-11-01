package infer

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// startTestManager 用假 sidecar 启动一个 Manager，并返回请求日志路径。
func startTestManager(t *testing.T, mode string, env ...string) (*Manager, string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "cmds.log")
	fullEnv := append([]string{"FAKE_MODE=" + mode, "FAKE_LOG=" + logPath}, env...)
	m := NewManager(ManagerConfig{
		Python:        buildFakeSidecar(t),
		Env:           fullEnv,
		Logf:          t.Logf,
		WarmupTimeout: 10 * time.Second,
		PingInterval:  20 * time.Millisecond,
		PingTimeout:   5 * time.Second,
		InferTimeout:  5 * time.Second,
	})
	m.Start(context.Background())
	t.Cleanup(func() { _ = m.Close() })
	return m, logPath
}

func waitReady(t *testing.T, m *Manager) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.WaitReady(ctx); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
}

// processAlive 报告 pid 是否仍然存在（用于断言没有孤儿进程）。
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if runtime.GOOS == "windows" {
		out, err := exec.Command("tasklist", "/FI", "PID eq "+strconv.Itoa(pid), "/NH", "/FO", "CSV").Output()
		if err != nil {
			return true // 查询失败时保守认为还活着
		}
		return strings.Contains(string(out), `,"`+strconv.Itoa(pid)+`",`)
	}
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "pid=").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// waitProcessGone 等待进程退出（最多 3s），超时则判定为孤儿。
func waitProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("pid %d 仍未退出（孤儿进程）", pid)
}

func TestManagerWarmupAndInfer(t *testing.T) {
	// 给假 sidecar 加 20ms 推理延迟：本机 time.Now 粒度约 0.5ms，
	// 太快（<1 次时钟 tick）会让"耗时"可靠地测成 0，测不出可观测性。
	m, _ := startTestManager(t, "warming", "FAKE_INFER_DELAY_MS=20")
	waitReady(t, m)

	st := m.Status()
	if !st.Ready || st.Warming || st.Error != "" {
		t.Fatalf("状态 = %+v", st)
	}
	if st.Model != "fake-da3" || st.Device != "cpu" {
		t.Errorf("model/device = %q/%q", st.Model, st.Device)
	}
	if st.VRAMUsedMB != 1234.5 || st.VRAMPeakMB != 2000 {
		t.Errorf("显存字段 = %v/%v", st.VRAMUsedMB, st.VRAMPeakMB)
	}
	if st.LoadSeconds != 0.1 {
		t.Errorf("LoadSeconds = %v，期望 0.1", st.LoadSeconds)
	}
	if st.PID == 0 || st.PID != m.PID() {
		t.Errorf("PID = %d（m.PID=%d）", st.PID, m.PID())
	}

	before := time.Now()
	res, err := m.Infer(context.Background(), []byte{1, 2, 3}, 0)
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	if res.Width != 5 || res.Height != 3 {
		t.Errorf("推断尺寸 = %dx%d", res.Width, res.Height)
	}
	// 假 sidecar 有 20ms 人为延迟，所以耗时字段应能测出 ≥ 15ms。
	if st := m.Status(); st.LastInferSeconds < 0.015 {
		t.Errorf("LastInferSeconds = %v，期望 ≥ 0.015s", st.LastInferSeconds)
	}
	if elapsed := time.Since(before); elapsed < 15*time.Millisecond {
		t.Errorf("Infer 实际耗时 %v 太短，测试假设不成立", elapsed)
	}
	if st := m.Status(); st.Restarts != 0 {
		t.Errorf("正常路径不应重启，restarts=%d", st.Restarts)
	}
}

func TestManagerInferWaitsForWarmup(t *testing.T) {
	m, _ := startTestManager(t, "warming")
	// 不显式 WaitReady：第一个 Infer 必须自己等预热完成（warming 模式要 3 次 PING）。
	res, err := m.Infer(context.Background(), []byte{9}, 0)
	if err != nil {
		t.Fatalf("Infer 应等待预热后成功: %v", err)
	}
	if res.Width != 5 {
		t.Errorf("尺寸 = %d", res.Width)
	}
}

// TestManagerRestartOnceOnCrash 验证"推理时 sidecar 崩溃 → 重启一次 → 重试成功"。
func TestManagerRestartOnceOnCrash(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "crashed.marker")
	m, _ := startTestManager(t, "crash-once", "FAKE_CRASH_MARKER="+marker)
	waitReady(t, m)
	firstPID := m.PID()

	res, err := m.Infer(context.Background(), []byte{1, 2, 3}, 0)
	if err != nil {
		t.Fatalf("崩溃后重试应成功: %v", err)
	}
	if res.Width != 5 || res.Height != 3 {
		t.Errorf("尺寸 = %dx%d", res.Width, res.Height)
	}
	st := m.Status()
	if st.Restarts != 1 {
		t.Errorf("Restarts = %d，期望 1", st.Restarts)
	}
	if st.PID == 0 || st.PID == firstPID {
		t.Errorf("重启后 pid 应变化：旧 %d 新 %d", firstPID, st.PID)
	}
	if _, err := m.Infer(context.Background(), []byte{4}, 0); err != nil {
		t.Fatalf("重启后的 sidecar 应继续可用: %v", err)
	}
}

// TestManagerRemoteErrorDoesNotRestart 验证业务错误（status=1）不会触发重启。
func TestManagerRemoteErrorDoesNotRestart(t *testing.T) {
	m, _ := startTestManager(t, "infer-error")
	waitReady(t, m)
	pid := m.PID()

	_, err := m.Infer(context.Background(), []byte{1}, 0)
	if !errors.Is(err, ErrRemote) {
		t.Fatalf("错误 = %v，期望 errors.Is ErrRemote", err)
	}
	if st := m.Status(); st.Restarts != 0 || st.PID != pid {
		t.Errorf("业务错误不应重启：restarts=%d pid=%d（原 %d）", st.Restarts, st.PID, pid)
	}
	waitReady(t, m) // 连接仍可用
}

// TestManagerCloseSendsUnloadAndReapsChild 验证优雅关闭：先 UNLOAD，再让子进程退出。
func TestManagerCloseSendsUnloadAndReapsChild(t *testing.T) {
	m, logPath := startTestManager(t, "ok")
	waitReady(t, m)
	pid := m.PID()

	if err := m.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if processAlive(pid) {
		t.Errorf("Close 后 pid %d 仍在运行", pid)
	}
	if err := m.Close(); err != nil { // 幂等
		t.Errorf("重复 Close: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("读请求日志: %v", err)
	}
	if !strings.Contains(string(data), "cmd=3") {
		t.Errorf("关闭时应先发 UNLOAD（cmd=3），实际日志：\n%s", data)
	}
	if _, err := m.Infer(context.Background(), []byte{1}, 0); !errors.Is(err, ErrClosed) {
		t.Errorf("关闭后 Infer 错误 = %v，期望 ErrClosed", err)
	}
}

// TestManagerRepairsDesync 验证「管道失步能自愈」：
// 失步后重建管道（独立预算，不占用崩溃重启预算）并重试成功，health 如实累加 desync_repairs。
func TestManagerRepairsDesync(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "desync.marker")
	m, _ := startTestManager(t, "desync-once", "FAKE_DESYNC_MARKER="+marker)
	waitReady(t, m)

	res, err := m.Infer(context.Background(), []byte{1, 2, 3}, 0)
	if err != nil {
		t.Fatalf("失步后应重建管道并重试成功: %v", err)
	}
	if res.Width != 5 || res.Height != 3 {
		t.Errorf("重试结果异常：%dx%d", res.Width, res.Height)
	}
	st := m.Status()
	if st.DesyncRepairs != 1 {
		t.Errorf("desync_repairs = %d，期望 1", st.DesyncRepairs)
	}
	if st.Restarts != 0 {
		t.Errorf("失步不应占用崩溃重启预算，restarts = %d", st.Restarts)
	}
	if !st.Ready || st.Error != "" {
		t.Errorf("重建成功后状态应恢复 ready：%+v", st)
	}
	if _, err := m.Infer(context.Background(), []byte{4}, 0); err != nil {
		t.Fatalf("重建后应持续可用: %v", err)
	}
	// health 里 desync_repairs 是累计次数（只增不清），成功推理只重置"本轮预算"。
	if st := m.Status(); st.DesyncRepairs != 1 {
		t.Errorf("累计 desync_repairs = %d，期望 1", st.DesyncRepairs)
	}
}

// TestManagerHalfResponseCrashRestart 是事故场景的回归测试：
// 假 sidecar 在 INFER 时「只写半帧就断开」→ 客户端读到 EOF（不完整帧）→
// 按进程崩溃重启一次 → 重试成功。
func TestManagerHalfResponseCrashRestart(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "crash.marker")
	m, _ := startTestManager(t, "half-once", "FAKE_CRASH_MARKER="+marker)
	waitReady(t, m)

	res, err := m.Infer(context.Background(), []byte{1, 2, 3}, 0)
	if err != nil {
		t.Fatalf("半帧断开后应重启一次并重试成功: %v", err)
	}
	if res.Width != 5 {
		t.Errorf("重试结果异常：%dx%d", res.Width, res.Height)
	}
	st := m.Status()
	if st.Restarts != 1 {
		t.Errorf("restarts = %d，期望 1", st.Restarts)
	}
	if st.DesyncRepairs != 0 {
		t.Errorf("半帧 EOF 属于进程崩溃，不应算作失步重建：desync_repairs=%d", st.DesyncRepairs)
	}
	if !st.Ready {
		t.Errorf("重启后状态应恢复 ready：%+v", st)
	}
}

// TestManagerDesyncRepairBudgetExhausted 验证连续失步会耗尽重建预算，
// 并把「不可用」如实反映到 health（ready=false + 原因），而不是假装正常。
func TestManagerDesyncRepairBudgetExhausted(t *testing.T) {
	// 不配 marker：每个新 sidecar 进程第一次 INFER 都会失步，必然耗尽预算。
	m, _ := startTestManager(t, "desync-once")
	waitReady(t, m)

	_, err := m.Infer(context.Background(), []byte{1}, 0)
	if err == nil {
		t.Fatal("连续失步应最终失败，而不是假装成功")
	}
	if !errors.Is(err, ErrDesync) && !errors.Is(err, ErrProtocol) {
		t.Errorf("错误 = %v，期望 ErrDesync/ErrProtocol", err)
	}
	st := m.Status()
	if st.Ready {
		t.Error("health 必须显示 ready=false")
	}
	if st.DesyncRepairs != maxDesyncRepairs {
		t.Errorf("desync_repairs = %d，期望耗尽为 %d", st.DesyncRepairs, maxDesyncRepairs)
	}
	if st.Error == "" {
		t.Error("health 必须给出失败原因")
	}
}

// TestManagerAbortedCallerKeepsSidecarHealthy 验证根因修复：
// 调用方在推理中途取消（关页面/取消上传）后，sidecar 管道保持同步、health 保持 ready，
// 下一次请求正常——不会像事故那样"用着用着就废了"。
func TestManagerAbortedCallerKeepsSidecarHealthy(t *testing.T) {
	m, _ := startTestManager(t, "ok", "FAKE_INFER_DELAY_MS=150")
	waitReady(t, m)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := m.Infer(ctx, []byte{1}, 0)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("取消后的错误 = %v，期望 context.DeadlineExceeded", err)
	}
	// sidecar 调用用独立 context：取消时仍会把这帧读完（约 150ms 延迟）。
	if el := time.Since(start); el < 120*time.Millisecond {
		t.Errorf("返回过早（%v）：没有排空响应，管道会失步", el)
	}

	st := m.Status()
	if !st.Ready {
		t.Errorf("请求取消不应让 sidecar 变成不可用：%+v", st)
	}
	if st.DesyncRepairs != 0 || st.Restarts != 0 {
		t.Errorf("请求取消不应触发重建/重启：desync=%d restarts=%d", st.DesyncRepairs, st.Restarts)
	}

	// 关键断言：下一次请求必须正常。
	res, err := m.Infer(context.Background(), []byte{2}, 0)
	if err != nil {
		t.Fatalf("取消之后的下一次推理失败（管道失步）: %v", err)
	}
	if res.Width != 5 {
		t.Errorf("结果异常：%dx%d", res.Width, res.Height)
	}
}

// TestManagerBadPython 验证拉起失败不会 panic/永久阻塞，而是给出可读错误。
func TestManagerBadPython(t *testing.T) {
	m := NewManager(ManagerConfig{
		Python:        filepath.Join(t.TempDir(), "not-a-python.exe"),
		Logf:          t.Logf,
		WarmupTimeout: 500 * time.Millisecond,
		PingInterval:  10 * time.Millisecond,
	})
	m.Start(context.Background())
	defer func() { _ = m.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := m.WaitReady(ctx)
	if err == nil {
		t.Fatal("坏 python 路径应返回错误")
	}
	if st := m.Status(); st.Ready {
		t.Errorf("状态不应为 ready：%+v", st)
	}
	if _, err := m.Infer(context.Background(), []byte{1}, 0); err == nil {
		t.Error("sidecar 不可用时 Infer 应返回错误")
	}
}

func TestManagerStatusBeforeStart(t *testing.T) {
	m := NewManager(ManagerConfig{Logf: t.Logf})
	st := m.Status()
	if st.Ready || st.Warming || st.PID != 0 {
		t.Errorf("未启动状态 = %+v", st)
	}
	if m.PID() != 0 {
		t.Errorf("PID = %d，期望 0", m.PID())
	}
	if err := m.Close(); err != nil {
		t.Errorf("未启动时 Close: %v", err)
	}
}
