// 假 sidecar：按 docs/selection.md §4.1 的 stdio 帧协议与 Go 客户端对话，
// 供 internal/infer 的端到端测试使用（真实 Python sidecar 尚未实现）。
//
// 它由测试通过 `go build ./testdata/fakesidecar` 编译后作为子进程运行：
// 从 stdin 读请求帧，只往 stdout 写响应帧；任何诊断信息都写 stderr。
//
// 行为由环境变量 FAKE_MODE 控制：
//
//	ok（默认）  正常应答
//	infer-error PING/UNLOAD 正常，INFER 返回 status=1（模拟模型未加载）
//	exit        启动后立刻退出（模拟 sidecar 崩溃）
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

		switch cmd {
		case cmdPing:
			respond(out, statusOK, []byte(`{"model":"fake-da3","device":"cpu","vram_used_mb":1234,"ready":true}`))
		case cmdInfer:
			if mode == "infer-error" {
				respond(out, statusError, []byte("fake sidecar: 模型未加载（测试用错误）"))
				continue
			}
			respond(out, statusOK, inferPayload(arg))
		case cmdUnload:
			respond(out, statusOK, nil)
		default:
			respond(out, statusError, []byte(fmt.Sprintf("未知命令 %d", cmd)))
		}
	}
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
