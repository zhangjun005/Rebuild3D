package infer

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

// buildFakeSidecar 把 testdata/fakesidecar（真正独立的 Go 程序）编译到临时目录。
// 集成测试直接把它当 Python sidecar 拉起来，端到端跑 stdio 帧协议。
func buildFakeSidecar(t *testing.T) string {
	t.Helper()
	name := "fakesidecar"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	exe := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-o", exe, "./testdata/fakesidecar")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("编译假 sidecar 失败: %v\n%s", err, out)
	}
	return exe
}

// startFake 启动一个指定模式的假 sidecar 客户端，测试结束时关闭。
// extraEnv 用于追加 FAKE_* 环境变量（例如推理延迟、崩溃 marker）。
func startFake(t *testing.T, mode string, extraEnv ...string) *Client {
	t.Helper()
	env := append([]string{"FAKE_MODE=" + mode}, extraEnv...)
	c, err := Start(context.Background(), Config{
		Path:          buildFakeSidecar(t),
		Env:           env,
		Logf:          t.Logf,
		PingTimeout:   10 * time.Second,
		InferTimeout:  10 * time.Second,
		UnloadTimeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Logf("Close: %v", err)
		}
	})
	return c
}

func TestPing(t *testing.T) {
	c := startFake(t, "ok")
	if c.PID() == 0 {
		t.Error("PID 应非 0")
	}
	info, err := c.Ping(context.Background())
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	loadSeconds := 0.1
	want := PingInfo{
		Model:       "fake-da3",
		Device:      "cpu",
		Ready:       true,
		ProcessRes:  504,
		VRAMUsedMB:  1234.5,
		VRAMPeakMB:  2000,
		LoadSeconds: &loadSeconds,
	}
	if !reflect.DeepEqual(info, want) {
		t.Errorf("PingInfo = %+v，期望 %+v", info, want)
	}
}

func TestInferWithConf(t *testing.T) {
	c := startFake(t, "ok")
	res, err := c.Infer(context.Background(), []byte{0xFF, 0xD8, 0xFF, 0xE0}, 0)
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	if res.Width != 5 || res.Height != 3 || !res.HasConf() {
		t.Fatalf("结果头 = %dx%d flags=%d HasConf=%v", res.Width, res.Height, res.Flags, res.HasConf())
	}
	if len(res.Depth) != 15 || len(res.Conf) != 15 {
		t.Fatalf("depth/conf 长度 = %d/%d，期望 15/15", len(res.Depth), len(res.Conf))
	}
	for i := 0; i < 15; i++ {
		wantDepth := float32(i)*0.25 - 1
		wantConf := float32(i+1) / 15
		if res.Depth[i] != wantDepth {
			t.Errorf("depth[%d] = %v，期望 %v", i, res.Depth[i], wantDepth)
		}
		if res.Conf[i] != wantConf {
			t.Errorf("conf[%d] = %v，期望 %v", i, res.Conf[i], wantConf)
		}
	}
}

func TestInferWithoutConf(t *testing.T) {
	c := startFake(t, "ok")
	res, err := c.Infer(context.Background(), []byte{1, 2, 3}, 504)
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}
	if res.HasConf() || res.Conf != nil || len(res.Depth) != 15 {
		t.Fatalf("无 conf 分支解析错误：flags=%d conf=%v depth=%d", res.Flags, res.Conf, len(res.Depth))
	}
	// 同一进程里可以连续请求（验证串行化与帧边界都没问题）。
	for i := 0; i < 3; i++ {
		if _, err := c.Ping(context.Background()); err != nil {
			t.Fatalf("第 %d 次重复请求失败: %v", i, err)
		}
	}
}

func TestUnload(t *testing.T) {
	c := startFake(t, "ok")
	if err := c.Unload(context.Background()); err != nil {
		t.Fatalf("Unload: %v", err)
	}
	// UNLOAD 之后管道仍然可用（真实 sidecar 卸载后仍可重新推理）。
	if _, err := c.Ping(context.Background()); err != nil {
		t.Fatalf("UNLOAD 后 Ping: %v", err)
	}
}

// TestRemoteErrorKeepsConnection 验证 status=1 是业务错误，不是管道损坏。
func TestRemoteErrorKeepsConnection(t *testing.T) {
	c := startFake(t, "infer-error")
	_, err := c.Infer(context.Background(), []byte{1}, 0)
	if !errors.Is(err, ErrRemote) {
		t.Fatalf("错误 = %v，期望 errors.Is ErrRemote", err)
	}
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Message == "" || remote.Cmd != CmdInfer {
		t.Errorf("RemoteError = %+v", remote)
	}
	// 连接仍可用。
	if _, err := c.Ping(context.Background()); err != nil {
		t.Fatalf("远端错误后 Ping: %v", err)
	}
}

// TestSidecarCrash 验证 sidecar 启动即崩溃时，调用返回协议错误而不是挂死。
func TestSidecarCrash(t *testing.T) {
	c, err := Start(context.Background(), Config{
		Path:        buildFakeSidecar(t),
		Env:         []string{"FAKE_MODE=exit"},
		Logf:        t.Logf,
		PingTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = c.Close() }()
	_, err = c.Ping(context.Background())
	if !errors.Is(err, ErrProtocol) {
		t.Fatalf("错误 = %v，期望 errors.Is ErrProtocol", err)
	}
	// 损坏后后续调用直接失败，不再写管道。
	if _, err := c.Ping(context.Background()); !errors.Is(err, ErrProtocol) {
		t.Errorf("第二次调用错误 = %v，期望 ErrProtocol", err)
	}
}

// TestCallerCancelDoesNotBreakClient 验证调用方 ctx 取消不会误伤连接。
func TestCallerCancelDoesNotBreakClient(t *testing.T) {
	c := startFake(t, "ok")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Ping(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("已取消 ctx 的错误 = %v，期望 context.Canceled", err)
	}
	info, err := c.Ping(context.Background())
	if err != nil {
		t.Fatalf("取消后重新 Ping: %v", err)
	}
	if info.Model == "" {
		t.Error("重新 Ping 结果为空")
	}
}

// TestCanceledInferKeepsPipeInSync 是"管道失步"事故的客户端层回归测试。
//
// 事故：调用方中途取消时，旧实现直接放弃读取；sidecar 已经写出的剩余字节留在管道里，
// 下一个请求把它当成新的 total_len → 读到 926505755 这种垃圾 → 管道永久失步。
// 修复后：取消时必须先把这一帧**读完**（结果丢弃）再返回，管道保持成帧。
func TestCanceledInferKeepsPipeInSync(t *testing.T) {
	c := startFake(t, "ok", "FAKE_INFER_DELAY_MS=150")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := c.Infer(ctx, []byte{1, 2, 3}, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("取消后的错误 = %v，期望 context.DeadlineExceeded", err)
	}
	// 必须在返回前排空响应：耗时应该接近 fake 的 150ms 延迟，而不是 20ms。
	if el := time.Since(start); el < 120*time.Millisecond {
		t.Errorf("取消返回过早（%v）：没有排空响应，管道会失步", el)
	}

	// 关键断言：管道仍同步 → 下一次请求必须成功。
	res, err := c.Infer(context.Background(), []byte{4, 5, 6}, 0)
	if err != nil {
		t.Fatalf("取消之后的下一次 Infer 失败（管道失步）: %v", err)
	}
	if res.Width != 5 || res.Height != 3 {
		t.Errorf("下一次 Infer 结果异常：%dx%d", res.Width, res.Height)
	}
	if _, err := c.Ping(context.Background()); err != nil {
		t.Fatalf("取消之后 Ping 失败: %v", err)
	}
}

// TestDesyncErrorClassification 验证非法长度/状态字被归类为 ErrDesync（而不是普通崩溃）。
func TestDesyncErrorClassification(t *testing.T) {
	// 合法帧头之后跟一个垃圾长度：模拟拿到错位的 4 字节。
	bogus := []byte{0x9B, 0x1A, 0x37, 0x00} // 小端 = 3611675 之类，仍 < MaxFrameBytes 时换更大的
	bogus = []byte{0xDB, 0x3C, 0x3A, 0x37}  // 926505755 > MaxFrameBytes
	if _, err := readResponse(bytes.NewReader(bogus), CmdInfer); !errors.Is(err, ErrDesync) || !errors.Is(err, ErrProtocol) {
		t.Errorf("非法长度错误 = %v，期望同时 errors.Is ErrDesync+ErrProtocol", err)
	}
	if _, err := readResponse(bytes.NewReader(frame(9, nil)), CmdInfer); !errors.Is(err, ErrDesync) {
		t.Errorf("未知状态字错误 = %v，期望 ErrDesync", err)
	}
}

func TestStartValidation(t *testing.T) {
	if _, err := Start(context.Background(), Config{}); err == nil {
		t.Error("空 Path 应报错")
	}
	if _, err := Start(context.Background(), Config{Path: filepath.Join(t.TempDir(), "不存在")}); err == nil {
		t.Error("不存在的可执行文件应报错")
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	exe := buildFakeSidecar(t)
	c, err := Start(context.Background(), Config{Path: exe, Logf: t.Logf})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("首次 Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("重复 Close: %v", err)
	}
	if _, err := c.Ping(context.Background()); !errors.Is(err, ErrClosed) {
		t.Errorf("关闭后 Ping 错误 = %v，期望 ErrClosed", err)
	}
}
