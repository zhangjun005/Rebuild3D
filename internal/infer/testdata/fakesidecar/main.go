// 假 sidecar：按 docs/selection.md §4.1 的 stdio 帧协议与 Go 客户端对话，
// 供 internal/infer 的端到端测试使用（不依赖 Python / GPU，纯 Go 程序）。
//
// 它由测试通过 `go build ./testdata/fakesidecar` 编译后作为子进程运行：
// 从 stdin 读请求帧，只往 stdout 写响应帧；诊断信息写 stderr。
//
// 行为由环境变量控制：
//
//	FAKE_MODE=ok（默认）  正常应答
//	FAKE_MODE=warming    前 2 次 PING 返回 ready=false，第 3 次起 true（测预热轮询）
//	FAKE_MODE=infer-error  PING/UNLOAD 正常，INFER 返回 status=1（模拟业务错误）
//	FAKE_MODE=exit        启动后立刻退出（模拟 sidecar 崩溃）
//	FAKE_MODE=crash-once  第一次 INFER 时进程直接退出（模拟推理中崩溃）；
//	                      用 FAKE_CRASH_MARKER 文件记录"已经崩过一次"，
//	                      重启后的进程看到 marker 即正常应答（测"重启一次并重试"）
//
//	FAKE_LOG=<path>         每个请求追加一行 "cmd=N arg=M"（测请求序列/UNLOAD 是否发出）
//	FAKE_CRASH_MARKER=<path> crash-once 的持久化标记
//	FAKE_INFER_DELAY_MS=<n> INFER 响应前 sleep n 毫秒（测"耗时可观测"，本机时钟粒度约 0.5ms）
//
// INFER 的约定：固定返回 5x3；arg==0 时带 conf（flags=1），arg!=0 时不带（flags=0）。
// depth[i] = i*0.25 - 1；conf[i] = (i+1)/15，方便测试断言具体数值。
package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"time"
)

const (
	cmdPing   byte = 1
	cmdInfer  byte = 2
	cmdUnload byte = 3

	statusOK    byte = 0
	statusError byte = 1
)

func main() {
	mode := os.Getenv("FAKE_MODE")
	if mode == "" {
		mode = "ok"
	}
	if mode == "exit" {
		os.Exit(0)
	}

	in := bufio.NewReader(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	defer func() { _ = out.Flush() }()

	pingCount := 0
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(in, hdr[:]); err != nil {
			if err == io.EOF {
				return // 父进程关掉 stdin：正常退出
			}
			fatalf("读请求长度失败: %v", err)
		}
		n := binary.LittleEndian.Uint32(hdr[:])
		if n < 5 || n > 1<<20 {
			fatalf("请求长度非法: %d", n)
		}
		buf := make([]byte, int(n))
		if _, err := io.ReadFull(in, buf); err != nil {
			fatalf("读请求体失败: %v", err)
		}
		cmd := buf[0]
		arg := binary.LittleEndian.Uint32(buf[1:5])
		appendLog(fmt.Sprintf("cmd=%d arg=%d", cmd, arg))

		switch cmd {
		case cmdPing:
			pingCount++
			ready := true
			if mode == "warming" && pingCount < 3 {
				ready = false
			}
			respond(out, statusOK, pingJSON(ready))
		case cmdInfer:
			if mode == "infer-error" {
				respond(out, statusError, []byte("fake sidecar: 模型未加载（测试用错误）"))
				continue
			}
			if mode == "crash-once" && crashFirstTime() {
				// 不写响应，直接退出：Go 侧应看到管道 EOF 并重启进程。
				os.Exit(1)
			}
			if mode == "half-once" && crashFirstTime() {
				// 只写半帧就退出：模拟"响应只写一半就断开"。
				// Go 侧会读到 EOF（不完整帧），应判定进程崩溃并重启一次。
				writeHalfFrameThenExit(out, arg)
			}
			if mode == "desync-once" && crashFirstTime() {
				// 写一个非法的长度前缀并**继续运行**：模拟管道失步。
				// Go 侧应识别 ErrDesync → 重建管道 → 重试成功（新进程看到 marker 正常应答）。
				var bogus [4]byte
				binary.LittleEndian.PutUint32(bogus[:], 926505755) // 线上事故里读到的垃圾长度
				if _, err := out.Write(bogus[:]); err != nil {
					fatalf("写失步帧失败: %v", err)
				}
				if err := out.Flush(); err != nil {
					fatalf("flush 失步帧失败: %v", err)
				}
				continue
			}
			if d := inferDelay(); d > 0 {
				time.Sleep(d)
			}
			respond(out, statusOK, inferPayload(arg))
		case cmdUnload:
			respond(out, statusOK, nil)
		default:
			respond(out, statusError, []byte(fmt.Sprintf("未知命令 %d", cmd)))
		}
	}
}

// crashFirstTime 在第一次触发时返回 true，并把 marker 落盘；
// 重启后的进程看到 marker 返回 false（正常应答），从而能测"重启/重建后重试成功"。
// marker 路径优先取 FAKE_CRASH_MARKER，其次 FAKE_DESYNC_MARKER（两种故障测试各用各的）。
func crashFirstTime() bool {
	marker := os.Getenv("FAKE_CRASH_MARKER")
	if marker == "" {
		marker = os.Getenv("FAKE_DESYNC_MARKER")
	}
	if marker == "" {
		return true // 没配 marker：每次都崩（会触发重启/重建上限）
	}
	if _, err := os.Stat(marker); err == nil {
		return false
	}
	_ = os.WriteFile(marker, []byte("crashed"), 0o644)
	return true
}

// writeHalfFrameThenExit 写一个"长度前缀完整、body 只写一半"的残帧，然后退出进程。
// 用来模拟 sidecar 在推理中途崩溃 / 写响应写一半就断开。
func writeHalfFrameThenExit(out *bufio.Writer, arg uint32) {
	body := inferPayload(arg)
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(1+len(body)))
	_, _ = out.Write(hdr[:])
	_ = out.WriteByte(statusOK)
	half := body
	if len(half) > 5 {
		half = half[:5]
	}
	_, _ = out.Write(half)
	_ = out.Flush()
	os.Exit(1)
}

// pingJSON 构造与 sidecar/depth_engine.py status() 同构的 PING 响应。
func pingJSON(ready bool) []byte {
	return []byte(fmt.Sprintf(
		`{"model":"fake-da3","device":"cpu","ready":%t,"process_res":504,`+
			`"vram_used_mb":1234.5,"vram_peak_mb":2000.0,"load_seconds":0.1,"last_infer_seconds":null}`,
		ready))
}

// inferDelay 返回 INFER 的人为延迟（模拟真实推理耗时）。
func inferDelay() time.Duration {
	v := os.Getenv("FAKE_INFER_DELAY_MS")
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Millisecond
}

// appendLog 把请求记录到 FAKE_LOG（未设置则无操作）。
func appendLog(line string) {
	path := os.Getenv("FAKE_LOG")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintln(f, line)
}

// inferPayload 构造 INFER 响应体：width u32 + height u32 + flags u32 + depth [+ conf]。
func inferPayload(arg uint32) []byte {
	const (
		w uint32 = 5
		h uint32 = 3
	)
	flags := uint32(1) // 默认带 conf
	if arg != 0 {
		flags = 0 // 传了 process_res：走"无 conf"分支，覆盖两条解码路径
	}
	n := int(w * h)
	body := make([]byte, 12+4*n+4*n)
	binary.LittleEndian.PutUint32(body[0:4], w)
	binary.LittleEndian.PutUint32(body[4:8], h)
	binary.LittleEndian.PutUint32(body[8:12], flags)
	off := 12
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint32(body[off:off+4], math.Float32bits(float32(i)*0.25-1))
		off += 4
	}
	if flags&1 != 0 {
		for i := 0; i < n; i++ {
			binary.LittleEndian.PutUint32(body[off:off+4], math.Float32bits(float32(i+1)/float32(n)))
			off += 4
		}
	}
	return body[:off]
}

// respond 写一帧响应：total_len u32 + status u8 + body。
func respond(out *bufio.Writer, status byte, body []byte) {
	total := 1 + len(body)
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(total))
	if _, err := out.Write(hdr[:]); err != nil {
		fatalf("写响应头失败: %v", err)
	}
	if err := out.WriteByte(status); err != nil {
		fatalf("写状态失败: %v", err)
	}
	if _, err := out.Write(body); err != nil {
		fatalf("写响应体失败: %v", err)
	}
	if err := out.Flush(); err != nil {
		fatalf("flush 失败: %v", err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "fake sidecar: "+format+"\n", args...)
	os.Exit(1)
}
