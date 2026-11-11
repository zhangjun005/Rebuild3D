// @kron:intent arch/three-way-split/frame-protocol
// @kron:intent geometry/segmentation

// Package infer 实现 Go ↔ Python sidecar 的 stdio 帧协议客户端（docs/selection.md §4.1）。
//
// 帧结构（全小端，请求与响应都带 4 字节长度前缀）：
//
//	请求：total_len u32 | cmd u8 | arg u32 | body
//	响应：total_len u32 | status u8 | body（body 按 cmd 解析）
//
// cmd: 1=PING 2=INFER 3=UNLOAD 4=SEGMENT；status: 0=成功 1=错误（body 是 UTF-8 错误信息）。
//
// 约定：
//   - sidecar 的日志必须写 stderr，stdout 只放帧，否则会污染协议流；
//   - PING 的 body 是 UTF-8 JSON（model/device/vram_used_mb/ready）；
//   - INFER 的 body 是 width u32 + height u32 + flags u32 + depth f32[W*H]，
//     flags&1 时追加 conf f32[W*H]；
//   - SEGMENT 的 body 是 width u32 + height u32 + mask u8[W*H]，
//     mask 三档：0=背景 128=不确定 255=角色（见 MaskBackground/MaskUncertain/MaskCharacter）；
//   - UNLOAD 的 body 为空。
package infer

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
)

// 协议命令字。
const (
	CmdPing    byte = 1
	CmdInfer   byte = 2
	CmdUnload  byte = 3
	CmdSegment byte = 4
)

// 协议响应状态。
const (
	StatusOK    byte = 0
	StatusError byte = 1
)

// 协议安全上限，防止坏数据导致巨量分配。
const (
	// MaxFrameBytes 是单帧 body 上限（不含 4 字节长度前缀）。
	MaxFrameBytes = 128 << 20
	// MaxDepthDim 是 INFER 响应单边像素上限。
	MaxDepthDim = 16384
)

var (
	// ErrProtocol 表示帧格式错误或管道层面的失败。
	ErrProtocol = errors.New("infer: 协议错误")
	// ErrDesync 表示管道**失步**：响应帧边界已错位（长度/状态字非法），
	// 之后的字节都不能再信任，必须重建整条管道（关管道 → 杀进程 → 重启 → warmup）。
	// 与"进程崩溃/EOF"分开，因为两者的修复预算与策略不同。
	ErrDesync = errors.New("infer: 管道失步")
	// ErrClosed 表示客户端已关闭。
	ErrClosed = errors.New("infer: 客户端已关闭")
	// ErrRemote 是 sidecar 以 status=1 返回错误的哨兵（用 errors.Is 判断）。
	ErrRemote = errors.New("infer: sidecar 报错")
)

// RemoteError 是 sidecar 返回的业务错误（status=1）。此时管道本身仍然可用。
type RemoteError struct {
	// Cmd 是出错请求的命令字。
	Cmd byte
	// Message 是 sidecar 给出的 UTF-8 错误信息。
	Message string
}

func (e *RemoteError) Error() string {
	return fmt.Sprintf("infer: sidecar 返回错误（%s）: %s", cmdName(e.Cmd), e.Message)
}

// Is 让 errors.Is(err, ErrRemote) 成立。
func (e *RemoteError) Is(target error) bool { return target == ErrRemote }

// PingInfo 是 PING 响应的 JSON 体，字段对应 sidecar/depth_engine.py 的 status()。
type PingInfo struct {
	Model      string `json:"model"`
	Device     string `json:"device"`
	Ready      bool   `json:"ready"`
	ProcessRes int    `json:"process_res"`
	// VRAMUsedMB / VRAMPeakMB 是 PyTorch 分配器口径的显存（MiB）。
	VRAMUsedMB float64 `json:"vram_used_mb"`
	VRAMPeakMB float64 `json:"vram_peak_mb"`
	// LoadSeconds / LastInferSeconds 在从未加载/推理时为 null，故用指针。
	LoadSeconds      *float64 `json:"load_seconds"`
	LastInferSeconds *float64 `json:"last_infer_seconds"`
}

// DepthResult 是 INFER 响应的解码结果。
type DepthResult struct {
	Width  int
	Height int
	Flags  uint32
	// Depth 长度恒为 Width*Height。
	Depth []float32
	// Conf 仅在 flags&1 != 0 时非 nil，长度同 Depth。
	Conf []float32
}

// HasConf 报告结果是否带置信度图。
func (r *DepthResult) HasConf() bool { return r != nil && r.Flags&1 != 0 }

// cmdName 返回命令的可读名字，仅用于错误信息。
func cmdName(cmd byte) string {
	switch cmd {
	case CmdPing:
		return "PING"
	case CmdInfer:
		return "INFER"
	case CmdUnload:
		return "UNLOAD"
	case CmdSegment:
		return "SEGMENT"
	default:
		return fmt.Sprintf("cmd=%d", cmd)
	}
}

// encodeRequest 把一次请求编码成完整帧（含长度前缀）。
func encodeRequest(cmd byte, arg uint32, body []byte) ([]byte, error) {
	total := 1 + 4 + len(body)
	if total > MaxFrameBytes {
		return nil, fmt.Errorf("%w: 请求体 %d 字节超过上限 %d", ErrProtocol, total, MaxFrameBytes)
	}
	buf := make([]byte, 4+total)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(total))
	buf[4] = cmd
	binary.LittleEndian.PutUint32(buf[5:9], arg)
	copy(buf[9:], body)
	return buf, nil
}

// readResponse 从 r 读一帧响应，返回 status=0 时的 body（已去掉 status 字节）。
//
// 错误分类（上层据此决定"重建管道"还是"重启进程"）：
//   - status=1 → *RemoteError（管道仍然可用）；
//   - 长度/状态字非法 → **ErrDesync**（帧边界已错位，管道不可再用）；
//   - 读到 EOF / 半帧 → ErrProtocol（进程崩溃/退出，管道已断）。
func readResponse(r io.Reader, cmd byte) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, fmt.Errorf("%w: 读响应长度失败（sidecar 可能已退出）: %w", ErrProtocol, err)
	}
	n := binary.LittleEndian.Uint32(hdr[:])
	if n == 0 || n > MaxFrameBytes {
		return nil, fmt.Errorf("%w: %w: 响应长度非法 %d（帧边界已错位）", ErrProtocol, ErrDesync, n)
	}
	buf := make([]byte, int(n))
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, fmt.Errorf("%w: 读响应体失败: %w", ErrProtocol, err)
	}
	switch buf[0] {
	case StatusOK:
		return buf[1:], nil
	case StatusError:
		return nil, &RemoteError{Cmd: cmd, Message: string(buf[1:])}
	default:
		return nil, fmt.Errorf("%w: %w: 未知响应状态 %d（帧边界不可信）", ErrProtocol, ErrDesync, buf[0])
	}
}

// decodePing 解析 PING 的 JSON 体。
func decodePing(body []byte) (PingInfo, error) {
	var info PingInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return info, fmt.Errorf("%w: PING 响应不是合法 JSON: %w", ErrProtocol, err)
	}
	return info, nil
}

// decodeInfer 解析 INFER 的二进制体。
func decodeInfer(body []byte) (*DepthResult, error) {
	if len(body) < 12 {
		return nil, fmt.Errorf("%w: INFER 响应头不足 12 字节（实际 %d）", ErrProtocol, len(body))
	}
	w := binary.LittleEndian.Uint32(body[0:4])
	h := binary.LittleEndian.Uint32(body[4:8])
	flags := binary.LittleEndian.Uint32(body[8:12])
	if w == 0 || h == 0 || w > MaxDepthDim || h > MaxDepthDim {
		return nil, fmt.Errorf("%w: INFER 尺寸非法 %dx%d", ErrProtocol, w, h)
	}
	n := uint64(w) * uint64(h)
	need := uint64(12) + 4*n
	if flags&1 != 0 {
		need += 4 * n
	}
	if uint64(len(body)) < need {
		return nil, fmt.Errorf("%w: INFER 响应体不足：需要 %d 字节，实际 %d", ErrProtocol, need, len(body))
	}

	depth := make([]float32, n)
	off := 12
	for i := range depth {
		depth[i] = math.Float32frombits(binary.LittleEndian.Uint32(body[off : off+4]))
		off += 4
	}
	var conf []float32
	if flags&1 != 0 {
		conf = make([]float32, n)
		for i := range conf {
			conf[i] = math.Float32frombits(binary.LittleEndian.Uint32(body[off : off+4]))
			off += 4
		}
	}
	return &DepthResult{Width: int(w), Height: int(h), Flags: flags, Depth: depth, Conf: conf}, nil
}

// 分割 mask 的三档取值（与 sidecar/seg_engine.py 约定一致）。
const (
	// MaskBackground 表示背景（prob < 0.4）。
	MaskBackground uint8 = 0
	// MaskUncertain 表示不确定（0.4 <= prob < 0.6）——天然的边界带起点。
	MaskUncertain uint8 = 128
	// MaskCharacter 表示角色（prob >= 0.6）。
	MaskCharacter uint8 = 255
)

// SegResult 是 SEGMENT 的响应：角色分割 mask，尺寸与原图一致。
type SegResult struct {
	Width  int
	Height int
	// Mask 是三档 mask（MaskBackground / MaskUncertain / MaskCharacter）。
	Mask []uint8
}

// decodeSegment 解析 SEGMENT 的二进制体：width u32 + height u32 + mask u8[W*H]。
func decodeSegment(body []byte) (*SegResult, error) {
	if len(body) < 8 {
		return nil, fmt.Errorf("%w: SEGMENT 响应头不足 8 字节（实际 %d）", ErrProtocol, len(body))
	}
	w := binary.LittleEndian.Uint32(body[0:4])
	h := binary.LittleEndian.Uint32(body[4:8])
	if w == 0 || h == 0 || w > MaxDepthDim || h > MaxDepthDim {
		return nil, fmt.Errorf("%w: SEGMENT 尺寸非法 %dx%d", ErrProtocol, w, h)
	}
	n := uint64(w) * uint64(h)
	if uint64(len(body)) < 8+n {
		return nil, fmt.Errorf("%w: SEGMENT 响应体不足：需要 %d 字节，实际 %d", ErrProtocol, 8+n, len(body))
	}
	mask := make([]uint8, n)
	raw := body[8 : 8+n]
	for i := range mask {
		mask[i] = raw[i]
	}
	return &SegResult{Width: int(w), Height: int(h), Mask: mask}, nil
}
