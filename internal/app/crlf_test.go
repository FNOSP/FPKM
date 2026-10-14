package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fpkm/internal/config"
)

// TestNormalizeLineEndings 覆盖 CRLF 归一化。
//
// 真实案例：便签 note 1.2.1 的 cmd/main 是 Windows 打包的 CRLF 脚本，
// bash 执行时报
//
//	syntax error near unexpected token `$'{\r''
//
// 并以 exit status 2 退出，表现为"启动失败: 启动脚本失败: exit status 2"。
func TestNormalizeLineEndings(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"纯 LF 不变", "#!/bin/bash\nfoo() {\n  echo hi\n}\n", "#!/bin/bash\nfoo() {\n  echo hi\n}\n"},
		{"CRLF 转 LF", "#!/bin/bash\r\nfoo() {\r\n  echo hi\r\n}\r\n", "#!/bin/bash\nfoo() {\n  echo hi\n}\n"},
		{"孤立 CR 转 LF", "a\rb\rc", "a\nb\nc"},
		{"混合行尾", "a\r\nb\nc\rd", "a\nb\nc\nd"},
		{"空串", "", ""},
		{"无换行", "abc", "abc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeLineEndings(tc.in); got != tc.want {
				t.Errorf("normalizeLineEndings(%q) = %q, 期望 %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestInstallNormalizesCRLFScripts 端到端验证：
// 安装一个脚本为 CRLF 的包后，脚本应被归一化为 LF 且语法正确。
func TestInstallNormalizesCRLFScripts(t *testing.T) {
	base := t.TempDir()
	cfg := &config.Config{
		BaseDir:     base,
		AppsDir:     filepath.Join(base, "apps"),
		BinDir:      filepath.Join(base, "bin"),
		LogDir:      filepath.Join(base, "logs"),
		GatewayPort: 7666,
	}
	for _, d := range []string{cfg.BaseDir, cfg.AppsDir, cfg.BinDir, cfg.LogDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// 构造一个带 CRLF cmd/main 的最小应用目录，直接调用重映射逻辑
	appDir := cfg.AppDir("crlfapp")
	targetDir := cfg.AppTargetDir("crlfapp")
	for _, d := range []string{appDir, targetDir, filepath.Join(appDir, "cmd")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// CRLF 脚本：含 case 与函数定义（真实 cmd/main 的典型形态）
	crlfScript := "#!/bin/bash\r\n" +
		"LOG_FILE=\"${TRIM_PKGVAR}/info.log\"\r\n" +
		"\r\n" +
		"start_process() {\r\n" +
		"  echo starting\r\n" +
		"}\r\n" +
		"\r\n" +
		"case $1 in\r\n" +
		"  start)\r\n" +
		"    start_process\r\n" +
		"    ;;\r\n" +
		"esac\r\n"

	mainPath := filepath.Join(appDir, "cmd", "main")
	if err := os.WriteFile(mainPath, []byte(crlfScript), 0o755); err != nil {
		t.Fatal(err)
	}

	// 归一化前：确认确实是 CRLF（否则测试无意义）
	before, _ := os.ReadFile(mainPath)
	if normalizeLineEndings(string(before)) == string(before) {
		t.Fatal("测试数据不是 CRLF，测试无效")
	}

	// 执行安装期的脚本处理（路径重映射 + 行尾归一化）
	m := NewManager(cfg)
	m.remapScriptPaths(appDir, "crlfapp")

	after, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(after); got != normalizeLineEndings(crlfScript) {
		t.Errorf("脚本未被归一化为 LF，实际内容:\n%q", got)
	}
	if normalizeLineEndings(string(after)) != string(after) {
		t.Error("归一化后仍残留 CR")
	}
}

// TestNormalizeSkipsBinary 确认二进制文件不会被行尾/路径处理破坏
func TestNormalizeSkipsBinary(t *testing.T) {
	// ELF 头 + 含 0x0D 的二进制数据
	bin := []byte{0x7F, 'E', 'L', 'F', 0x00, 0x0D, 0x0A, 0x00, 0x01, 0x00, 0x0D}
	if !isBinaryContent(bin) {
		t.Error("应被识别为二进制内容，从而跳过重写")
	}
}

// TestRemapScriptPathsHandlesSlashlessCRLF 确保 CRLF 脚本里的
// /var/apps/xxx 路径替换与行尾归一化可同时生效。
func TestRemapScriptPathsHandlesSlashlessCRLF(t *testing.T) {
	dir := t.TempDir()
	appDir := filepath.Join(dir, "apps", "myapp")
	targetDir := filepath.Join(appDir, "target")
	if err := os.MkdirAll(filepath.Join(appDir, "cmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}

	script := "#!/bin/bash\r\nBIN=\"/var/apps/myapp/target/server/app\"\r\nexec \"$BIN\"\r\n"
	mainPath := filepath.Join(appDir, "cmd", "main")
	if err := os.WriteFile(mainPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		BaseDir: dir,
		AppsDir: filepath.Join(dir, "apps"),
		BinDir:  filepath.Join(dir, "bin"),
		LogDir:  filepath.Join(dir, "logs"),
	}
	m := NewManager(cfg)
	m.remapScriptPaths(appDir, "myapp")

	got, _ := os.ReadFile(mainPath)
	s := string(got)

	if strings.Contains(s, "\r") {
		t.Error("行尾未归一化")
	}
	if strings.Contains(s, "/var/apps/myapp") {
		t.Error("硬编码路径未被重映射")
	}
	if !strings.Contains(s, filepath.Join(appDir, "target", "server", "app")) {
		t.Errorf("重映射后的路径不正确:\n%s", s)
	}
}
