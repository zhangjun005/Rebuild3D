package cloud

import (
	"encoding/binary"
	"errors"
	"math"
	"os"
	"testing"

	"illust3d/internal/geometry"
)

// sample 构造一份可预测的测试点云。
func sample() *Cloud {
	return New(
		[]geometry.Vec3{
			{X: 1, Y: 2, Z: 3},
			{X: -1.5, Y: 0.25, Z: -0.125},
			{X: 0, Y: -1, Z: 1e-6},
		},
		[]geometry.Color{
			{R: 10, G: 20, B: 30},
			{R: 255, G: 0, B: 128},
			{R: 1, G: 2, B: 3},
		},
	)
}

func mustEncode(t *testing.T, c *Cloud) []byte {
	t.Helper()
	b, err := Encode(c)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return b
}

// TestEncodeLayout 逐字节校验 ILC1 布局，而不是只做 round-trip。
func TestEncodeLayout(t *testing.T) {
	c := sample()
	got := mustEncode(t, c)

	if want := ExpectedSize(3); len(got) != want {
		t.Fatalf("长度 = %d，期望 %d", len(got), want)
	}
	if string(got[0:4]) != "ILC1" {
		t.Errorf("magic = %q，期望 ILC1", got[0:4])
	}
	if n := binary.LittleEndian.Uint32(got[4:8]); n != 3 {
		t.Errorf("count = %d，期望 3", n)
	}
	// pos 段从偏移 8 开始，每点 12 字节，x/y/z 交错。
	wantPos := []float32{1, 2, 3, -1.5, 0.25, -0.125, 0, -1, 1e-6}
	for i, want := range wantPos {
		off := 8 + i*4
		if got := math.Float32frombits(binary.LittleEndian.Uint32(got[off : off+4])); got != want {
			t.Errorf("pos[%d] = %v，期望 %v", i, got, want)
		}
	}
	// col 段从偏移 8 + 12*3 = 44 开始，每点 3 字节。
	wantCol := []byte{10, 20, 30, 255, 0, 128, 1, 2, 3}
	if string(got[44:]) != string(wantCol) {
		t.Errorf("col = %v，期望 %v", got[44:], wantCol)
	}
}

func TestRoundTrip(t *testing.T) {
	c := sample()
	got, err := Decode(mustEncode(t, c))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Len() != c.Len() {
		t.Fatalf("点数 = %d，期望 %d", got.Len(), c.Len())
	}
	for i := range c.Pos {
		if got.Pos[i] != c.Pos[i] {
			t.Errorf("Pos[%d] = %+v，期望 %+v", i, got.Pos[i], c.Pos[i])
		}
		if got.Col[i] != c.Col[i] {
			t.Errorf("Col[%d] = %+v，期望 %+v", i, got.Col[i], c.Col[i])
		}
	}
}

// TestDecodeTable 是表驱动的解码边界测试。
func TestDecodeTable(t *testing.T) {
	valid := mustEncode(t, sample())
	// 零点和空点云：只有头部。
	empty, err := Encode(New(nil, nil))
	if err != nil {
		t.Fatalf("Encode(empty): %v", err)
	}
	badMagic := append([]byte(nil), valid...)
	badMagic[0] = 'X'
	// count 声明为 MaxPoints+1（长度检查之前应被点数上限拦下）。
	hugeCount := make([]byte, 8)
	copy(hugeCount[0:4], Magic)
	binary.LittleEndian.PutUint32(hugeCount[4:8], MaxPoints+1)
	// 尾部多余字节应被容忍。
	trailing := append(append([]byte(nil), valid...), 0xAA, 0xBB)

	tests := []struct {
		name    string
		data    []byte
		wantN   int
		wantErr error
	}{
		{"valid", valid, 3, nil},
		{"zero-count", empty, 0, nil},
		{"trailing-bytes", trailing, 3, nil},
		{"nil", nil, 0, ErrTruncated},
		{"empty", []byte{}, 0, ErrTruncated},
		{"short-header", valid[:7], 0, ErrTruncated},
		{"bad-magic", badMagic, 0, ErrBadMagic},
		{"truncated-body", valid[:len(valid)-1], 0, ErrTruncated},
		{"truncated-col", valid[:8+12*3], 0, ErrTruncated},
		{"count-too-large", hugeCount, 0, ErrInvalidCount},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			n, err := PointCount(tc.data)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("PointCount: 未预期错误 %v", err)
				}
				if n != tc.wantN {
					t.Errorf("PointCount = %d，期望 %d", n, tc.wantN)
				}
			} else {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("PointCount 错误 = %v，期望 errors.Is %v", err, tc.wantErr)
				}
				return
			}

			// 同一批数据也走完整 Decode。
			c, err := Decode(tc.data)
			if err != nil {
				t.Fatalf("Decode: 未预期错误 %v", err)
			}
			if c.Len() != tc.wantN {
				t.Errorf("Decode 点数 = %d，期望 %d", c.Len(), tc.wantN)
			}
		})
	}
}

func TestDecodeDoesNotAliasInput(t *testing.T) {
	data := mustEncode(t, sample())
	c, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	first := c.Pos[0]
	for i := range data {
		data[i] = 0
	}
	if c.Pos[0] != first {
		t.Errorf("Decode 结果与输入字节共享内存：修改输入后 Pos[0]=%+v，期望 %+v", c.Pos[0], first)
	}
}

func TestEncodeValidation(t *testing.T) {
	pos := []geometry.Vec3{{}, {}}
	if _, err := Encode(New(pos, []geometry.Color{{}})); !errors.Is(err, ErrColorMismatch) {
		t.Errorf("颜色数量不匹配错误 = %v，期望 ErrColorMismatch", err)
	}
	if _, err := Encode(nil); err == nil {
		t.Error("Encode(nil) 期望报错")
	}
}

// TestRepoFixture 用独立生成的 testdata/sample.ilcl 交叉验证编解码。
// 该文件由 testdata/gen/main.go 生成，不是本项目代码写的，所以能抓格式错误。
func TestRepoFixture(t *testing.T) {
	const path = "../../testdata/sample.ilcl"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("跳过：没有 %s（%v）", path, err)
	}
	n, err := PointCount(data)
	if err != nil {
		t.Fatalf("PointCount: %v", err)
	}
	if n != 118401 {
		t.Errorf("点数 = %d，期望 118401", n)
	}
	c, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	again, err := Encode(c)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if string(again) != string(data) {
		t.Errorf("解码再编码后字节不一致：%d vs %d 字节", len(again), len(data))
	}
}
