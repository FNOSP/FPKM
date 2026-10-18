package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 缺执行位时应自动补上（Lucky 事故的直接回归测试）
func TestEnsureExecutableAddsMissingX(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "lucky")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ensureExecutable(bin); err != nil {
		t.Fatalf("应自动修复，得到错误: %v", err)
	}
	st, _ := os.Stat(bin)
	if st.Mode()&0111 == 0 {
		t.Fatalf("修复后仍无执行位: %o", st.Mode().Perm())
	}
}

// 已有执行位时不应改变权限（幂等）
func TestEnsureExecutableKeepsExistingMode(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "app")
	if err := os.WriteFile(bin, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensureExecutable(bin); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(bin)
	if st.Mode().Perm() != 0o755 {
		t.Errorf("不应改变权限，得到 %o", st.Mode().Perm())
	}
}

// 文件不存在时不报错（交给 exec 给更准确的信息）
func TestEnsureExecutableMissingFileNoError(t *testing.T) {
	if err := ensureExecutable(filepath.Join(t.TempDir(), "nope")); err != nil {
		t.Errorf("文件不存在时不应报错: %v", err)
	}
}

// 内置命令占位符应跳过
func TestEnsureExecutableSkipsBuiltin(t *testing.T) {
	if err := ensureExecutable("__builtin_static__"); err != nil {
		t.Errorf("内置命令应跳过: %v", err)
	}
	if err := ensureExecutable(""); err != nil {
		t.Errorf("空路径应跳过: %v", err)
	}
}

// copyFile 对已存在的目标文件也要修正权限
// （os.WriteFile 的 perm 只对新建文件生效，这是 Lucky 事故的根因）
func TestCopyFileFixesModeOnExistingDest(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")

	if err := os.WriteFile(src, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 预置一个 0644 的目标文件（模拟升级时遗留的旧文件）
	if err := os.WriteFile(dst, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}

	st, _ := os.Stat(dst)
	if st.Mode()&0111 == 0 {
		t.Fatalf("复制后目标文件应具备执行位，实际 %o", st.Mode().Perm())
	}
	data, _ := os.ReadFile(dst)
	if string(data) != "binary" {
		t.Errorf("内容未更新: %q", data)
	}
}

// 非可执行源文件不应被加上执行位（避免无意义地扩大权限）
func TestCopyFileKeepsNonExecutable(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("text"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(dst)
	if st.Mode()&0111 != 0 {
		t.Errorf("非可执行文件不应被加上执行位: %o", st.Mode().Perm())
	}
}

// 错误信息应包含可操作建议
func TestExecStartErrorPermissionHint(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "lucky")
	os.WriteFile(bin, []byte("x"), 0o644)
	err := execStartError(bin, os.ErrPermission)
	if err == nil {
		t.Fatal("应返回错误")
	}
	msg := err.Error()
	for _, want := range []string{"chmod", bin, "noexec"} {
		if !strings.Contains(msg, want) {
			t.Errorf("提示应包含 %q，实际:\n%s", want, msg)
		}
	}
}

// no such file 应给出清晰提示
func TestExecStartErrorNotFound(t *testing.T) {
	err := execStartError("/x/y", os.ErrNotExist)
	if err == nil || !strings.Contains(err.Error(), "找不到可执行文件") {
		t.Errorf("提示不明确: %v", err)
	}
}

// moveDirContents 跨目录移动后应保留可执行位
func TestMoveDirContentsKeepsExecBit(t *testing.T) {
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	bin := filepath.Join(srcDir, "bin")
	os.MkdirAll(bin, 0o755)
	exe := filepath.Join(bin, "lucky")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := moveDirContents(srcDir, dstDir); err != nil {
		t.Fatal(err)
	}

	moved := filepath.Join(dstDir, "bin", "lucky")
	st, err := os.Stat(moved)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode()&0111 == 0 {
		t.Errorf("移动后可执行位丢失: %o", st.Mode().Perm())
	}
}
