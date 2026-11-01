package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"illust3d/internal/cloud"
	"illust3d/internal/geometry"
)

// writeFixture 生成一份合法 ILC1 到临时目录，返回路径与字节。
func writeFixture(t *testing.T, n int) (string, []byte) {
	t.Helper()
	pos := make([]geometry.Vec3, n)
	col := make([]geometry.Color, n)
	for i := range pos {
		pos[i] = geometry.Vec3{X: float32(i), Y: float32(i) * 0.5, Z: -float32(i)}
		col[i] = geometry.Color{R: uint8(i), G: 2, B: 3}
	}
	data, err := cloud.Encode(cloud.New(pos, col))
	if err != nil {
		t.Fatalf("构造 fixture: %v", err)
	}
	path := filepath.Join(t.TempDir(), "sample.ilcl")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("写 fixture: %v", err)
	}
	return path, data
}

// newTestServer 用给定配置启动一个 httptest 服务。
func newTestServer(t *testing.T, opts Options) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(New(opts).Handler())
	t.Cleanup(s.Close)
	return s
}

func doGet(t *testing.T, url string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("读响应: %v", err)
	}
	return resp, body
}

func TestHealth(t *testing.T) {
	path, _ := writeFixture(t, 2)
	s := newTestServer(t, Options{Version: "v1.2.3", FixturePath: path})

	resp, body := doGet(t, s.URL+"/api/health")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
	var got healthResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("解析 JSON: %v（body=%s）", err, body)
	}
	if got.Status != "ok" || got.Version != "v1.2.3" || got.PointCount != 2 || got.FixtureError != "" {
		t.Errorf("health = %+v", got)
	}
}

func TestHealthMissingFixture(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "不存在.ilcl")
	s := newTestServer(t, Options{Version: "dev", FixturePath: missing})

	resp, body := doGet(t, s.URL+"/api/health")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200（degraded 也算健康响应）", resp.StatusCode)
	}
	var got healthResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("解析 JSON: %v", err)
	}
	if got.Status != "degraded" || got.FixtureError == "" || got.PointCount != 0 {
		t.Errorf("health = %+v", got)
	}

	// 没有 fixture 时点云接口返回 503 + JSON 错误，而不是空 200。
	resp2, body2 := doGet(t, s.URL+"/api/cloud/sample")
	if resp2.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("状态码 = %d，期望 503", resp2.StatusCode)
	}
	var errResp map[string]string
	if err := json.Unmarshal(body2, &errResp); err != nil {
		t.Fatalf("解析错误 JSON: %v（body=%s）", err, body2)
	}
	if errResp["error"] == "" || errResp["hint"] == "" {
		t.Errorf("错误响应 = %+v", errResp)
	}
}

func TestHealthInvalidFixture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.ilcl")
	if err := os.WriteFile(path, []byte("NOPE this is not ILC1"), 0o644); err != nil {
		t.Fatalf("写坏 fixture: %v", err)
	}
	s := newTestServer(t, Options{FixturePath: path})
	_, body := doGet(t, s.URL+"/api/health")
	var got healthResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("解析 JSON: %v", err)
	}
	if got.Status != "degraded" || !strings.Contains(got.FixtureError, "ILC1") {
		t.Errorf("health = %+v", got)
	}
}

func TestSampleCloud(t *testing.T) {
	path, data := writeFixture(t, 3)
	s := newTestServer(t, Options{Version: "v0", FixturePath: path})

	resp, body := doGet(t, s.URL+"/api/cloud/sample")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Content-Type = %q，期望 application/octet-stream", ct)
	}
	if cl := resp.Header.Get("Content-Length"); cl != itoa(len(data)) {
		t.Errorf("Content-Length = %q，期望 %d", cl, len(data))
	}
	if pc := resp.Header.Get("X-ILC1-PointCount"); pc != "3" {
		t.Errorf("X-ILC1-PointCount = %q，期望 3", pc)
	}
	if string(body) != string(data) {
		t.Errorf("响应字节与 fixture 不一致：%d vs %d 字节", len(body), len(data))
	}
}

func TestStaticIndex(t *testing.T) {
	s := newTestServer(t, Options{FixturePath: writeFixturePath(t)})
	resp, body := doGet(t, s.URL+"/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q，期望 text/html", ct)
	}
	if !strings.Contains(string(body), "web 尚未构建") {
		t.Errorf("占位页内容不符：%s", body)
	}
}

func TestStaticNotFound(t *testing.T) {
	s := newTestServer(t, Options{FixturePath: writeFixturePath(t)})
	resp, _ := doGet(t, s.URL+"/no-such-file.js")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("状态码 = %d，期望 404", resp.StatusCode)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	s := newTestServer(t, Options{FixturePath: writeFixturePath(t)})
	resp, err := http.Post(s.URL+"/api/health", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("状态码 = %d，期望 405", resp.StatusCode)
	}
}

// TestRepoFixture 用仓库里真实的 testdata/sample.ilcl 端到端验证（文件不存在时跳过）。
func TestRepoFixture(t *testing.T) {
	const path = "../../testdata/sample.ilcl"
	want, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("跳过：没有 %s", path)
	}
	s := newTestServer(t, Options{Version: "v0", FixturePath: path})
	resp, body := doGet(t, s.URL+"/api/cloud/sample")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", resp.StatusCode)
	}
	if string(body[0:4]) != "ILC1" {
		t.Errorf("magic = %q，期望 ILC1", body[0:4])
	}
	if len(body) != len(want) {
		t.Errorf("字节数 = %d，期望 %d", len(body), len(want))
	}
	if string(body) != string(want) {
		t.Error("响应字节与仓库 fixture 不一致")
	}
	_, health := doGet(t, s.URL+"/api/health")
	var got healthResponse
	if err := json.Unmarshal(health, &got); err != nil {
		t.Fatalf("解析 health: %v", err)
	}
	if got.PointCount <= 0 {
		t.Errorf("pointCount = %d，期望 > 0", got.PointCount)
	}
}

// writeFixturePath 是只需要一个可用 fixture 时的简写。
func writeFixturePath(t *testing.T) string {
	t.Helper()
	p, _ := writeFixture(t, 1)
	return p
}

// itoa 避免为了一个测试再引入 strconv。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
