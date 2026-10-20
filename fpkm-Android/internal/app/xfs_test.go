package app

import (
	"os"
	"path/filepath"
	"testing"
)

// moveAcrossFS 应能完成目录搬运（模拟跨文件系统 rename 失败后的回退）
func TestMoveAcrossFSDirectory(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	in := filepath.Join(src, "cmd")
	if err := os.MkdirAll(in, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in, "main"), []byte("#!/bin/bash"), 0o755); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dst, "cmd")
	if err := moveAcrossFS(in, out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(in); !os.IsNotExist(err) {
		t.Error("源目录应被删除")
	}
	data, err := os.ReadFile(filepath.Join(out, "main"))
	if err != nil {
		t.Fatalf("目标文件不存在: %v", err)
	}
	if string(data) != "#!/bin/bash" {
		t.Errorf("内容不匹配: %q", data)
	}
}

// 可执行权限必须保留（历史踩过 permission denied）
func TestMoveAcrossFSPreservesExecBit(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	f := filepath.Join(src, "lucky")
	os.WriteFile(f, []byte("elf"), 0o755)

	out := filepath.Join(dst, "lucky")
	if err := moveAcrossFS(f, out); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode()&0o111 == 0 {
		t.Errorf("执行位丢失: %o", st.Mode().Perm())
	}
}

// 符号链接保持为链接，不解引用
func TestMoveAcrossFSKeepsSymlink(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	os.WriteFile(filepath.Join(src, "real"), []byte("x"), 0o644)
	os.Symlink("real", filepath.Join(src, "link"))

	out := filepath.Join(dst, "link")
	if err := moveAcrossFS(filepath.Join(src, "link"), out); err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(out)
	if err != nil {
		t.Fatalf("应为符号链接: %v", err)
	}
	if target != "real" {
		t.Errorf("链接目标应为 real，得到 %q", target)
	}
}

// 嵌套目录应完整搬运
func TestMoveAcrossFSNested(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	deep := filepath.Join(src, "a", "b", "c")
	os.MkdirAll(deep, 0o755)
	os.WriteFile(filepath.Join(deep, "f"), []byte("deep"), 0o644)

	out := filepath.Join(dst, "a")
	if err := moveAcrossFS(filepath.Join(src, "a"), out); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(out, "b", "c", "f"))
	if err != nil {
		t.Fatalf("深层文件未搬运: %v", err)
	}
	if string(data) != "deep" {
		t.Errorf("内容不匹配: %q", data)
	}
}
