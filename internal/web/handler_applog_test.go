package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fpkm/internal/config"
)

// appLogTestCfg 构造只带目录配置的 Config，用于日志读取测试。
func appLogTestCfg(t *testing.T) *config.Config {
	t.Helper()
	base := t.TempDir()
	return &config.Config{
		BaseDir: base,
		AppsDir: filepath.Join(base, "apps"),
	}
}

// TestAppLogsMissingDir 日志目录不存在时应给出 Missing 与提示，而不是报错。
func TestAppLogsMissingDir(t *testing.T) {
	dir := t.TempDir()
	view := buildAppLogsView("demo", filepath.Join(dir, "demo"), appLogDefaultTail)

	if !view.Missing {
		t.Error("目录不存在时应标记 missing")
	}
	if view.Hint == "" {
		t.Error("应给出提示文案")
	}
	if len(view.Files) != 0 {
		t.Errorf("不应有文件，得到 %d 个", len(view.Files))
	}
}

// TestAppLogsReturnsAllFiles 应返回日志目录下的全部日志文件，并按最近修改排序。
func TestAppLogsReturnsAllFiles(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "demo")
	logsDir := filepath.Join(appDir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 旧文件：error.log；新文件：app.log
	if err := os.WriteFile(filepath.Join(logsDir, "error.log"), []byte("ERROR old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(logsDir, "app.log"), []byte("INFO new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 隐藏文件应被跳过
	if err := os.WriteFile(filepath.Join(logsDir, ".keep"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	view := buildAppLogsView("demo", appDir, appLogDefaultTail)
	if view.Missing {
		t.Fatal("有日志文件时不应标记 missing")
	}
	if len(view.Files) != 2 {
		t.Fatalf("应返回 2 个日志文件（隐藏文件不计），得到 %d: %v", len(view.Files), logNames(view))
	}
	if view.Files[0].Name != "app.log" {
		t.Errorf("最近修改的文件应排在前面，实际顺序: %v", logNames(view))
	}
	for _, f := range view.Files {
		if f.Truncated {
			t.Errorf("%s 内容很小，不应标记截断", f.Name)
		}
		if f.Path == "" || f.ModTime == "" {
			t.Errorf("%s 缺少路径或时间: %+v", f.Name, f)
		}
	}
	if got := view.Files[0].Content; got != "INFO new\n" {
		t.Errorf("app.log 内容不符: %q", got)
	}
}

// TestAppLogsTailTruncatesFromWholeLine 大文件截断时必须从完整行开始，不返回半行。
func TestAppLogsTailTruncatesFromWholeLine(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "demo")
	logsDir := filepath.Join(appDir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	var sb strings.Builder
	for i := 0; i < 500; i++ {
		sb.WriteString("line-")
		sb.WriteString(strings.Repeat("x", 60))
		sb.WriteString("\n")
	}
	content := sb.String()
	p := filepath.Join(logsDir, "app.log")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	view := buildAppLogsView("demo", appDir, 1000)
	if len(view.Files) != 1 {
		t.Fatalf("应返回 1 个文件，得到 %d", len(view.Files))
	}
	f := view.Files[0]
	if !f.Truncated {
		t.Fatal("大文件应标记 truncated")
	}
	if !strings.HasPrefix(f.Content, "line-") {
		t.Errorf("截断后首行应完整（以 line- 开头），实际: %q", firstLine(f.Content))
	}
	if strings.Count(f.Content, "line-") == 0 {
		t.Error("截断后内容不应为空")
	}
	if len(f.Content) > 1000 {
		t.Errorf("截断后内容不应超过 limit，实际 %d 字节", len(f.Content))
	}
	// 内容必须是原文件的尾部
	if !strings.HasSuffix(content, f.Content) {
		t.Error("截断后内容应为原文件尾部")
	}
}

// TestParseAppLogTail 非法与越界参数应被规整。
func TestParseAppLogTail(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"", appLogDefaultTail},
		{"0", appLogDefaultTail},
		{"-5", appLogDefaultTail},
		{"abc", appLogDefaultTail},
		{"4096", 4096},
		{"999999999", appLogMaxTail},
	}
	for _, tc := range cases {
		if got := parseAppLogTail(tc.in); got != tc.want {
			t.Errorf("parseAppLogTail(%q) = %d，期望 %d", tc.in, got, tc.want)
		}
	}
}

// TestHandleAppLogRoute 验证 GET /api/apps/{name}/log 的接口行为（JSON 结构）。
func TestHandleAppLogRoute(t *testing.T) {
	cfg := appLogTestCfg(t)
	appDir := cfg.AppDir("demo")
	logsDir := filepath.Join(appDir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logsDir, "app.log"), []byte("hello log\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &Server{cfg: cfg}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/apps/demo/log?tail=2048", nil)
	s.handleAppAction(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200；body=%s", rec.Code, rec.Body.String())
	}

	var view AppLogsView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("返回体不是合法 JSON: %v", err)
	}
	if view.Name != "demo" || view.TailBytes != 2048 {
		t.Errorf("返回体字段不符: %+v", view)
	}
	if len(view.Files) != 1 || view.Files[0].Content != "hello log\n" {
		t.Errorf("日志内容不符: %+v", view.Files)
	}
}

// TestHandleAppLogRejectsOtherGetActions 其他动作仍只允许 POST。
func TestHandleAppLogRejectsOtherGetActions(t *testing.T) {
	cfg := appLogTestCfg(t)
	s := &Server{cfg: cfg}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/apps/demo/start", nil)
	s.handleAppAction(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET start 应返回 405，实际 %d", rec.Code)
	}
}

// TestBuildLogsViewHints 通用日志读取：目录缺失与目录为空应分别使用各自的提示文案。
func TestBuildLogsViewHints(t *testing.T) {
	base := t.TempDir()

	// 目录不存在
	missing := buildLogsView("fpkm", filepath.Join(base, "nope"), appLogDefaultTail, "目录不存在提示", "目录为空提示")
	if !missing.Missing || missing.Hint != "目录不存在提示" {
		t.Errorf("目录不存在时应使用 hintMissing，得到 %+v", missing)
	}

	// 目录存在但没有日志文件
	emptyDir := filepath.Join(base, "logs")
	if err := os.MkdirAll(emptyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	empty := buildLogsView("fpkm", emptyDir, appLogDefaultTail, "目录不存在提示", "目录为空提示")
	if !empty.Missing || empty.Hint != "目录为空提示" {
		t.Errorf("目录为空时应使用 hintEmpty，得到 %+v", empty)
	}
}

// TestHandleFpkmLogRoute 验证 GET /api/logs 读取的是程序自身日志目录。
func TestHandleFpkmLogRoute(t *testing.T) {
	cfg := appLogTestCfg(t)
	cfg.LogDir = filepath.Join(cfg.BaseDir, "logs")
	if err := os.MkdirAll(cfg.LogDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.LogDir, "gateway.log"), []byte("gateway started\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &Server{cfg: cfg}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/logs?tail=4096", nil)
	s.handleFpkmLog(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200；body=%s", rec.Code, rec.Body.String())
	}

	var view AppLogsView
	if err := json.Unmarshal(rec.Body.Bytes(), &view); err != nil {
		t.Fatalf("返回体不是合法 JSON: %v", err)
	}
	if view.Name != "fpkm" || view.Dir != cfg.LogDir || view.TailBytes != 4096 {
		t.Errorf("返回体字段不符: %+v", view)
	}
	if len(view.Files) != 1 || view.Files[0].Content != "gateway started\n" {
		t.Errorf("日志内容不符: %+v", view.Files)
	}
}

// TestHandleFpkmLogRejectsPost FPKM 日志接口只接受 GET。
func TestHandleFpkmLogRejectsPost(t *testing.T) {
	cfg := appLogTestCfg(t)
	cfg.LogDir = filepath.Join(cfg.BaseDir, "logs")
	s := &Server{cfg: cfg}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/logs", nil)
	s.handleFpkmLog(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/logs 应返回 405，实际 %d", rec.Code)
	}
}

// logNames 便于断言失败时输出文件顺序
func logNames(v AppLogsView) []string {
	out := make([]string, 0, len(v.Files))
	for _, f := range v.Files {
		out = append(out, f.Name)
	}
	return out
}

// firstLine 返回首行内容，便于断言
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}