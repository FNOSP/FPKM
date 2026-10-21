package fpk

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// buildFPK 构造一个最小的 fpk（gzip tar），entries 为 名称→(内容, 权限)
func buildFPK(t *testing.T, entries []struct {
	Name    string
	Content []byte
	Mode    int64
}) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.Name, Mode: e.Mode, Size: int64(len(e.Content))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(e.Content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "test.fpk")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// shebang 脚本即使包里没给执行位，解压后也应可执行。
//
// 真实案例：Octop 的 bin/octop 在包内是 644（同包的 bin/octop-cli 是 755），
// 启动时报 "Permission denied"，应用永远起不来。
func TestExtractAddsExecBitToShebangScripts(t *testing.T) {
	pkg := buildFPK(t, []struct {
		Name    string
		Content []byte
		Mode    int64
	}{
		{"manifest", []byte("appname=x\nversion=1\n"), 0o644},
		{"bin/octop", []byte("#!/bin/bash\necho hi\n"), 0o644}, // 包内漏了执行位
		{"bin/data.txt", []byte("plain text\n"), 0o644},        // 非脚本不应加执行位
	})

	dest := t.TempDir()
	if err := Extract(pkg, dest); err != nil {
		t.Fatal(err)
	}

	st, err := os.Stat(filepath.Join(dest, "bin", "octop"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode()&0o111 == 0 {
		t.Errorf("shebang 脚本应可执行，实际 %o", st.Mode().Perm())
	}

	st2, err := os.Stat(filepath.Join(dest, "bin", "data.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if st2.Mode()&0o111 != 0 {
		t.Errorf("普通文本不应加执行位，实际 %o", st2.Mode().Perm())
	}

	// 内容不能被读头部时破坏
	data, _ := os.ReadFile(filepath.Join(dest, "bin", "octop"))
	if string(data) != "#!/bin/bash\necho hi\n" {
		t.Errorf("文件内容被破坏: %q", data)
	}
}

// 包内本来就是 755 的脚本应保持可执行
func TestExtractKeepsExistingExecBit(t *testing.T) {
	pkg := buildFPK(t, []struct {
		Name    string
		Content []byte
		Mode    int64
	}{
		{"manifest", []byte("appname=x\nversion=1\n"), 0o644},
		{"bin/app", []byte("#!/bin/sh\nexit 0\n"), 0o755},
	})
	dest := t.TempDir()
	if err := Extract(pkg, dest); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dest, "bin", "app"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode()&0o111 == 0 {
		t.Errorf("原有执行位不应丢失，实际 %o", st.Mode().Perm())
	}
}

var _ = io.Discard
