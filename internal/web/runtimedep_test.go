package web

import (
	"os"
	"path/filepath"
	"testing"
)

// 构造带/不带 ui/config 的应用目录
func makeUIDir(t *testing.T, appDir string, withUIConfig bool) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(appDir, "target"), 0o755); err != nil {
		t.Fatal(err)
	}
	if withUIConfig {
		ui := filepath.Join(appDir, "target", "ui")
		if err := os.MkdirAll(ui, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(ui, "config"), []byte(`{".url":{}}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// 运行时依赖（python / node / bun）必须被标记，供桌面与任务栏隐藏图标。
func TestIsRuntimeDepApp(t *testing.T) {
	base := t.TempDir()

	// 纯运行时：无 ui/config，接入方式未知
	pyDir := filepath.Join(base, "python312")
	makeUIDir(t, pyDir, false)

	// 正常应用：有 ui/config
	webDir := filepath.Join(base, "wb2api")
	makeUIDir(t, webDir, true)

	deps := map[string]bool{"python312": true}

	cases := []struct {
		name    string
		appName string
		appDir  string
		want    bool
	}{
		{"被依赖的运行时", "python312", pyDir, true},
		{"未被依赖但无 ui/config", "standalone-rt", pyDir, true},
		{"有界面的应用", "wb2api", webDir, false},
		{"有界面且被依赖", "python312", webDir, true},
	}
	for _, c := range cases {
		if got := isRuntimeDepApp(c.appName, deps, c.appDir); got != c.want {
			t.Errorf("%s: 期望 %v，得到 %v", c.name, c.want, got)
		}
	}
}

// hasUIConfig 应能识别 target/ui/config 与 app/ui/config 两种落点
func TestHasUIConfig(t *testing.T) {
	base := t.TempDir()

	a := filepath.Join(base, "a")
	makeUIDir(t, a, true)
	if !hasUIConfig(a) {
		t.Error("target/ui/config 存在时应返回 true")
	}

	b := filepath.Join(base, "b")
	os.MkdirAll(filepath.Join(b, "ui"), 0o755)
	os.WriteFile(filepath.Join(b, "ui", "config"), []byte("{}"), 0o644)
	if !hasUIConfig(b) {
		t.Error("app/ui/config 存在时应返回 true")
	}

	c := filepath.Join(base, "c")
	os.MkdirAll(filepath.Join(c, "target"), 0o755)
	if hasUIConfig(c) {
		t.Error("无 ui/config 时应返回 false")
	}
}
