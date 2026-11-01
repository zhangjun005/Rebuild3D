package infer

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestEncodeRequest(t *testing.T) {
	tests := []struct {
		name string
		cmd  byte
		arg  uint32
		body []byte
		want []byte
	}{
		{
			name: "ping",
			cmd:  CmdPing,
			want: []byte{5, 0, 0, 0, CmdPing, 0, 0, 0, 0},
		},
		{
			name: "infer-504-with-body",
			cmd:  CmdInfer,
			arg:  504,
			body: []byte{0xFF, 0xD8},
			want: []byte{7, 0, 0, 0, CmdInfer, 0xF8, 0x01, 0, 0, 0xFF, 0xD8},
		},
		{
			name: "unload",
			cmd:  CmdUnload,
			want: []byte{5, 0, 0, 0, CmdUnload, 0, 0, 0, 0},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := encodeRequest(tc.cmd, tc.arg, tc.body)
			if err != nil {
				t.Fatalf("encodeRequest: %v", err)
			}
			if !bytes.Equal(got, tc.want) {
				t.Errorf("帧 = %v，期望 %v", got, tc.want)
			}
		})
	}
}

func TestEncodeRequestTooLarge(t *testing.T) {
	if _, err := encodeRequest(CmdInfer, 0, make([]byte, MaxFrameBytes)); !errors.Is(err, ErrProtocol) {
		t.Errorf("超长请求错误 = %v，期望 ErrProtocol", err)
	}
}

// frame 按协议拼一帧响应字节。
func frame(status byte, body []byte) []byte {
	total := 1 + len(body)
	buf := make([]byte, 4+total)
	binary.LittleEndian.PutUint32(buf[0:4], uint32(total))
	buf[4] = status
	copy(buf[5:], body)
	return buf
}

func TestReadResponse(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		want    []byte
		wantErr error
	}{
		{"ok-empty", frame(StatusOK, nil), []byte{}, nil},
		{"ok-body", frame(StatusOK, []byte("hello")), []byte("hello"), nil},
		{"remote-error", frame(StatusError, []byte("挂了")), nil, ErrRemote},
		{"zero-length", []byte{0, 0, 0, 0}, nil, ErrProtocol},
		{"truncated-body", []byte{6, 0, 0, 0, StatusOK, 'a'}, nil, ErrProtocol},
		{"unknown-status", frame(7, nil), nil, ErrProtocol},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readResponse(bytes.NewReader(tc.data), CmdPing)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("错误 = %v，期望 errors.Is %v", err, tc.wantErr)
				}
				var remote *RemoteError
				if tc.wantErr == ErrRemote {
					if !errors.As(err, &remote) || remote.Cmd != CmdPing || remote.Message != "挂了" {
						t.Errorf("RemoteError = %+v", remote)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("未预期错误: %v", err)
			}
			if !bytes.Equal(got, tc.want) {
				t.Errorf("body = %q，期望 %q", got, tc.want)
			}
		})
	}
}

// inferBody 构造 INFER 响应体。
func inferBody(w, h uint32, flags uint32, depth, conf []float32) []byte {
	body := make([]byte, 12+4*len(depth)+4*len(conf))
	binary.LittleEndian.PutUint32(body[0:4], w)
	binary.LittleEndian.PutUint32(body[4:8], h)
	binary.LittleEndian.PutUint32(body[8:12], flags)
	off := 12
	for _, f := range depth {
		binary.LittleEndian.PutUint32(body[off:off+4], math.Float32bits(f))
		off += 4
	}
	for _, f := range conf {
		binary.LittleEndian.PutUint32(body[off:off+4], math.Float32bits(f))
		off += 4
	}
	return body
}

func TestDecodeInfer(t *testing.T) {
	depth := []float32{1, 2, 3, 4}
	conf := []float32{0.9, 0.8, 0.7, 0.6}

	t.Run("with-conf", func(t *testing.T) {
		res, err := decodeInfer(inferBody(2, 2, 1, depth, conf))
		if err != nil {
			t.Fatalf("decodeInfer: %v", err)
		}
		if res.Width != 2 || res.Height != 2 || !res.HasConf() {
			t.Fatalf("头部 = %dx%d flags=%d conf=%v", res.Width, res.Height, res.Flags, res.HasConf())
		}
		for i := range depth {
			if res.Depth[i] != depth[i] {
				t.Errorf("depth[%d] = %v，期望 %v", i, res.Depth[i], depth[i])
			}
			if res.Conf[i] != conf[i] {
				t.Errorf("conf[%d] = %v，期望 %v", i, res.Conf[i], conf[i])
			}
		}
	})

	t.Run("without-conf", func(t *testing.T) {
		res, err := decodeInfer(inferBody(2, 2, 0, depth, nil))
		if err != nil {
			t.Fatalf("decodeInfer: %v", err)
		}
		if res.HasConf() || res.Conf != nil {
			t.Errorf("flags=0 时不应有 conf：flags=%d conf=%v", res.Flags, res.Conf)
		}
	})

	t.Run("trailing-bytes-ignored", func(t *testing.T) {
		body := append(inferBody(1, 1, 0, []float32{5}, nil), 0xAA, 0xBB)
		if _, err := decodeInfer(body); err != nil {
			t.Errorf("尾部多余字节应被忽略，实际错误 %v", err)
		}
	})

	errorTests := []struct {
		name string
		body []byte
	}{
		{"short-header", make([]byte, 11)},
		{"zero-width", inferBody(0, 2, 0, nil, nil)},
		{"missing-depth", []byte{1, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0}},
		{"missing-conf", inferBody(2, 2, 1, depth, nil)},
	}
	for _, tc := range errorTests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := decodeInfer(tc.body); !errors.Is(err, ErrProtocol) {
				t.Errorf("错误 = %v，期望 ErrProtocol", err)
			}
		})
	}
}

func TestDecodePing(t *testing.T) {
	info, err := decodePing([]byte(`{"model":"da3","device":"cuda","vram_used_mb":1400,"ready":true}`))
	if err != nil {
		t.Fatalf("decodePing: %v", err)
	}
	if info.Model != "da3" || info.Device != "cuda" || info.VRAMUsedMB != 1400 || !info.Ready {
		t.Errorf("PingInfo = %+v", info)
	}
	if _, err := decodePing([]byte("not json")); !errors.Is(err, ErrProtocol) {
		t.Errorf("非法 JSON 错误 = %v，期望 ErrProtocol", err)
	}
}

func TestRemoteErrorMessage(t *testing.T) {
	err := &RemoteError{Cmd: CmdInfer, Message: "oom"}
	if !strings.Contains(err.Error(), "INFER") || !strings.Contains(err.Error(), "oom") {
		t.Errorf("错误信息 = %q", err.Error())
	}
}
