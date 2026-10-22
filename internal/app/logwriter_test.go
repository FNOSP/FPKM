package app

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// appLogStampRe 匹配应用日志行首的日志发生时间：YYYY-MM-DD HH:MM:SS
var appLogStampRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} `)

// TestOpenAppLogStampsSubprocessOutput 校验 openAppLog 返回的写入器
// 会把应用子进程的原始输出逐行补上发生时间。
func TestOpenAppLogStampsSubprocessOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")

	w, err := openAppLog(path)
	if err != nil {
		t.Fatalf("openAppLog: %v", err)
	}
	// 追加写入：两次打开同一文件，内容应继续累积
	w.Write([]byte("boot ok\n"))
	w.Write([]byte("listen "))
	w.Write([]byte(":8080\n"))
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	text := strings.TrimRight(string(data), "\n")
	lines := strings.Split(text, "\n")
	if len(lines) != 2 {
		t.Fatalf("期望 2 行，实际 %d 行: %q", len(lines), text)
	}
	want := []string{"boot ok", "listen :8080"}
	for i, line := range lines {
		if !appLogStampRe.MatchString(line) {
			t.Errorf("第 %d 行缺少时间戳前缀: %q", i+1, line)
		}
		if body := appLogStampRe.ReplaceAllString(line, ""); body != want[i] {
			t.Errorf("第 %d 行内容不符: 得到 %q，期望 %q", i+1, body, want[i])
		}
	}

	// 再次打开同一文件：新内容追加、旧内容保留
	w2, err := openAppLog(path)
	if err != nil {
		t.Fatalf("openAppLog(第二次): %v", err)
	}
	w2.Write([]byte("restart\n"))
	w2.Close()

	data, _ = os.ReadFile(path)
	if got := strings.Count(string(data), "\n"); got != 3 {
		t.Errorf("追加写入后应保留 3 行，实际按行计数 %d: %q", got, string(data))
	}
}
