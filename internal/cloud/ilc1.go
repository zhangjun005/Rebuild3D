// Package cloud 定义点云的内部表示，以及 Go ↔ 浏览器之间的 ILC1 二进制编解码。
//
// ILC1 格式（docs/selection.md §4.2，全部小端）：
//
//	magic  "ILC1"             4 B
//	count  uint32             4 B
//	pos    float32[3*count]   x,y,z 交错
//	col    uint8[3*count]     r,g,b 交错
//
// 每点固定 15 字节，总长度 = 8 + 15*count。浏览器侧可以零解析地
// new Float32Array(buf, 8, 3*count) / new Uint8Array(buf, 8+12*count, 3*count)。
//
// 对外导出格式（PLY）与内部传输格式是两套，不在这里合并（见 docs/selection.md §4.2）。
package cloud

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"illust3d/internal/geometry"
)

const (
	// Magic 是 ILC1 的点云文件/帧魔数。
	Magic = "ILC1"
	// HeaderSize 是 ILC1 头部字节数：magic(4) + count(4)。
	HeaderSize = 8
	// BytesPerPoint 是每个点的字节数：pos 3*4 + col 3*1。
	BytesPerPoint = 3*4 + 3
	// MaxPoints 是单份点云的逻辑上限，防止 count 字段被滥用导致巨量分配。
	// （按 15 字节/点计算，2^28 点约 4 GB；远高于本项目 10 万~50 万的量级。）
	MaxPoints = 1 << 28
)

var (
	// ErrBadMagic 表示数据不是 ILC1（前 4 字节不是 "ILC1"）。
	ErrBadMagic = errors.New("cloud: 不是 ILC1 数据（magic 不匹配）")
	// ErrTruncated 表示数据比 count 声明的长度短。
	ErrTruncated = errors.New("cloud: ILC1 数据被截断")
	// ErrInvalidCount 表示 count 超出合理范围或与数据长度矛盾。
	ErrInvalidCount = errors.New("cloud: ILC1 点数非法")
	// ErrColorMismatch 表示编码时位置与颜色切片长度不一致。
	ErrColorMismatch = errors.New("cloud: 位置与颜色数量不一致")
)

// Cloud 是一份点云的内部表示：位置与颜色一一对应，下标共享。
type Cloud struct {
	Pos []geometry.Vec3
	Col []geometry.Color
}

// New 用给定的位置与颜色构造点云（不复制切片，调用方后续修改会反映到 Cloud 上）。
func New(pos []geometry.Vec3, col []geometry.Color) *Cloud {
	return &Cloud{Pos: pos, Col: col}
}

// Len 返回点数。
func (c *Cloud) Len() int {
	if c == nil {
		return 0
	}
	return len(c.Pos)
}

// Validate 检查位置与颜色数量是否一致（编码前必须通过）。
func (c *Cloud) Validate() error {
	if c == nil {
		return errors.New("cloud: Cloud 为 nil")
	}
	if len(c.Pos) != len(c.Col) {
		return fmt.Errorf("%w: pos=%d col=%d", ErrColorMismatch, len(c.Pos), len(c.Col))
	}
	if len(c.Pos) > MaxPoints {
		return fmt.Errorf("%w: %d 点超过上限 %d", ErrInvalidCount, len(c.Pos), MaxPoints)
	}
	return nil
}

// Encode 把点云编码为 ILC1 字节切片（全小端）。
func Encode(c *Cloud) ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	n := len(c.Pos)
	if uint64(n) > math.MaxUint32 {
		return nil, fmt.Errorf("%w: %d 点超过 uint32 上限", ErrInvalidCount, n)
	}

	buf := make([]byte, HeaderSize+n*BytesPerPoint)
	copy(buf[0:4], Magic)
	binary.LittleEndian.PutUint32(buf[4:8], uint32(n))

	off := HeaderSize
	for _, p := range c.Pos {
		binary.LittleEndian.PutUint32(buf[off:off+4], math.Float32bits(p.X))
		binary.LittleEndian.PutUint32(buf[off+4:off+8], math.Float32bits(p.Y))
		binary.LittleEndian.PutUint32(buf[off+8:off+12], math.Float32bits(p.Z))
		off += 12
	}
	for _, col := range c.Col {
		buf[off] = col.R
		buf[off+1] = col.G
		buf[off+2] = col.B
		off += 3
	}
	return buf, nil
}

// Decode 解析 ILC1 字节切片。接受尾部多余的字节（为将来扩展留余地），
// 但 8 + 15*count 必须完整。返回的 Cloud 与输入 data 不共享内存。
func Decode(data []byte) (*Cloud, error) {
	n, err := PointCount(data)
	if err != nil {
		return nil, err
	}

	pos := make([]geometry.Vec3, n)
	off := HeaderSize
	for i := 0; i < n; i++ {
		pos[i] = geometry.Vec3{
			X: math.Float32frombits(binary.LittleEndian.Uint32(data[off : off+4])),
			Y: math.Float32frombits(binary.LittleEndian.Uint32(data[off+4 : off+8])),
			Z: math.Float32frombits(binary.LittleEndian.Uint32(data[off+8 : off+12])),
		}
		off += 12
	}
	col := make([]geometry.Color, n)
	for i := 0; i < n; i++ {
		col[i] = geometry.Color{R: data[off], G: data[off+1], B: data[off+2]}
		off += 3
	}
	return &Cloud{Pos: pos, Col: col}, nil
}

// PointCount 只解析 ILC1 头部并校验数据长度，返回点数。
// 适合"只想知道点数"的场景（例如 /api/health），避免完整解码。
func PointCount(data []byte) (int, error) {
	if len(data) < HeaderSize {
		return 0, fmt.Errorf("%w: 头部需要 %d 字节，实际 %d", ErrTruncated, HeaderSize, len(data))
	}
	if Magic != string(data[0:4]) {
		return 0, ErrBadMagic
	}
	n := binary.LittleEndian.Uint32(data[4:8])
	if n > MaxPoints {
		return 0, fmt.Errorf("%w: count=%d 超过上限 %d", ErrInvalidCount, n, MaxPoints)
	}
	want := uint64(HeaderSize) + uint64(n)*BytesPerPoint
	if uint64(len(data)) < want {
		return 0, fmt.Errorf("%w: count=%d 需要 %d 字节，实际 %d 字节", ErrTruncated, n, want, len(data))
	}
	return int(n), nil
}

// ExpectedSize 返回给定点数的 ILC1 字节数（= 8 + 15*n）。
func ExpectedSize(n int) int {
	return HeaderSize + n*BytesPerPoint
}
