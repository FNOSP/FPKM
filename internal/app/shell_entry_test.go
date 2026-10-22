package app

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDetectEntryFallbackUsesResolvableShell 验证「分发脚本回退执行」这条路径：
// cmd/main 是 start/stop/status 分发脚本、且无法提取出可直接启动的命令时，
// 探测结果必须是 EntryBashScript，且解释器来自 shellPath()（bash → sh 回退），
// 而不是上游写死的 "bash"。Android 上就是靠这一点修掉
// `exec: "bash": executable file not found in $PATH`。
func TestDetectEntryFallbackUsesResolvableShell(t *testing.T) {
	appDir := t.TempDir()
	targetDir := filepath.Join(appDir, "target")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmdDir := filepath.Join(appDir, "cmd")
	if err := os.MkdirAll(cmdDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 分发脚本：含 case 分发 + 函数定义，且启动命令指向不存在的二进制，
	// 迫使探测逻辑回退到「执行脚本」。
	script := `#!/bin/bash
set -e
start() { echo starting; }
stop() { echo stopping; }
case "$1" in
  start) start ;;
  stop) stop ;;
  *) echo "usage" ;;
esac
`
	if err := os.WriteFile(filepath.Join(cmdDir, "main"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	entry, err := DetectEntry(targetDir)
	if err != nil {
		t.Fatalf("DetectEntry 返回错误: %v", err)
	}
	if entry.Type != EntryBashScript {
		t.Fatalf("期望 EntryBashScript，实际 %s（%v）", entry.Type, entry)
	}
	if entry.Command != shellPath() {
		t.Errorf("解释器未走 shellPath() 解析：entry.Command=%q, shellPath()=%q", entry.Command, shellPath())
	}
	if entry.Command == "" {
		t.Error("解释器路径为空")
	}
}

// TestShellPathFallsBackToBashWhenNothingFound 用真实 LookPath 验证：
// PATH 中既无 bash 也无 sh 时，shellPath() 保底返回 "bash"（保持上游语义），
// 恢复 PATH 后可重新解析到真实解释器。
func TestShellPathFallsBackToBashWhenNothingFound(t *testing.T) {
	old, had := os.LookupEnv("PATH")
	emptyDir := t.TempDir()
	if err := os.Setenv("PATH", emptyDir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if had {
			_ = os.Setenv("PATH", old)
		} else {
			_ = os.Unsetenv("PATH")
		}
	}()

	if got := shellPath(); got != "bash" {
		t.Errorf("PATH 为空时期望保底 \"bash\"，实际 %q", got)
	}

	if had {
		if err := os.Setenv("PATH", old); err != nil {
			t.Fatal(err)
		}
		if got := shellPath(); got == "" {
			t.Error("恢复 PATH 后 shellPath() 不应为空")
		}
	}
}
