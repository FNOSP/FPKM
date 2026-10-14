package fpk

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestArchFromHeader(t *testing.T) {
	mk := func(b ...byte) []byte { return b }
	elf := func(machine uint16) []byte {
		h := make([]byte, 20)
		copy(h, []byte{0x7F, 'E', 'L', 'F'})
		h[18] = byte(machine)
		h[19] = byte(machine >> 8)
		return h
	}
	cases := []struct {
		name string
		in   []byte
		want Arch
		ok   bool
	}{
		{"x86_64 ELF", elf(0x3E), ArchX86_64, true},
		{"i386 ELF", elf(0x03), ArchI386, true},
		{"aarch64 ELF", elf(0xB7), ArchAarch64, true},
		{"arm ELF", elf(0x28), ArchArm, true},
		{"PE", mk('M', 'Z', 0x90, 0x00), ArchUnknown, true},
		{"脚本", []byte("#!/bin/bash\n"), "", false},
		{"PNG", []byte{0x89, 'P', 'N', 'G'}, "", false},
		{"太短", []byte{0x7F}, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := archFromHeader(c.in)
			if ok != c.ok || got != c.want {
				t.Errorf("archFromHeader = (%q,%v), 期望 (%q,%v)", got, ok, c.want, c.ok)
			}
		})
	}
}

// TestReasonScriptOnlyAlwaysCompatible 纯脚本/前端包必须在任何架构上都可安装。
// 实测案例：CloudSaver / Cloudimgs / guandan 被应用源标成 arm，
// 但包内没有任何原生二进制，在 x86 上完全可以运行。
func TestReasonScriptOnlyAlwaysCompatible(t *testing.T) {
	r := &PackageArchReport{HasBinary: false, ScriptOnly: true}
	if got := reasonForCurrentArch(r); got != "" {
		t.Errorf("无二进制的包应判为兼容，实际原因: %q", got)
	}
}

// TestReasonArchMismatch 含不匹配架构的二进制应被拦截
func TestReasonArchMismatch(t *testing.T) {
	cur := runtime.GOARCH
	var foreign Arch
	switch cur {
	case "amd64":
		foreign = ArchAarch64
	case "arm64":
		foreign = ArchX86_64
	default:
		t.Skip("未覆盖的架构: " + cur)
	}
	r := &PackageArchReport{HasBinary: true, Archs: []Arch{foreign}}
	if got := reasonForCurrentArch(r); got == "" {
		t.Errorf("含 %s 二进制的包在 %s 上应判为不兼容", foreign, cur)
	} else {
		t.Logf("按预期拦截: %s", got)
	}
}

// TestReasonMatchingArch 匹配架构应通过
func TestReasonMatchingArch(t *testing.T) {
	cur := runtime.GOARCH
	var match Arch
	switch cur {
	case "amd64":
		match = ArchX86_64
	case "arm64":
		match = ArchAarch64
	default:
		t.Skip("未覆盖的架构: " + cur)
	}
	r := &PackageArchReport{HasBinary: true, Archs: []Arch{match}}
	if got := reasonForCurrentArch(r); got != "" {
		t.Errorf("匹配架构应通过，实际: %q", got)
	}
}

// TestInspectPackageArchRealLayouts 覆盖两种真实的 .fpk 布局：
//
//  1. gzip 压缩的 tar（fn-appstores、hand.nas.pro 都是这种）；
//  2. 二进制在**内层 app.tgz** 里。
//
// 早期实现直接按 tar 解析、且没先尝试 gzip，遇到真实包会
// 报 "invalid tar header" 并得出"包内无二进制"的错误结论 ——
// 这会让纯脚本应用被误判为"与架构无关"，也会让真正的 arm 包漏检。
func TestInspectPackageArchRealLayouts(t *testing.T) {
	tmp := t.TempDir()
	fpkPath := filepath.Join(tmp, "sample.fpk")

	if err := writeGzipTarFPK(fpkPath); err != nil {
		t.Fatal(err)
	}

	r, err := InspectPackageArch(fpkPath)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if !r.HasBinary {
		t.Fatal("应检测到内层 app.tgz 中的二进制")
	}
	if len(r.Archs) != 1 || r.Archs[0] != ArchAarch64 {
		t.Errorf("架构 = %v, 期望 [aarch64]", r.Archs)
	}
	if r.ScriptOnly {
		t.Error("含二进制的包不应标记为 ScriptOnly")
	}
}

// TestInspectPackageArchScriptOnly 纯脚本包（无任何原生二进制）
func TestInspectPackageArchScriptOnly(t *testing.T) {
	tmp := t.TempDir()
	fpkPath := filepath.Join(tmp, "script.fpk")

	if err := writeGzipTarFPK(fpkPath, true); err != nil {
		t.Fatal(err)
	}
	r, err := InspectPackageArch(fpkPath)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if r.HasBinary {
		t.Errorf("纯脚本包不应检测到二进制, archs=%v", r.Archs)
	}
	if !r.ScriptOnly {
		t.Error("应标记为 ScriptOnly")
	}
	if r.UnsupportedReason != "" {
		t.Errorf("纯脚本包在任何架构上都应可用，实际: %q", r.UnsupportedReason)
	}
}

// writeGzipTarFPK 生成一个 gzip 压缩、二进制位于内层 app.tgz 的 .fpk
func writeGzipTarFPK(dst string, scriptOnly ...bool) error {
	var inner bytes.Buffer
	itw := tar.NewWriter(&inner)

	if len(scriptOnly) > 0 && scriptOnly[0] {
		// 纯脚本：只有 JS 与 shell
		for name, body := range map[string]string{
			"server.js": "console.log('hi')\n",
			"cmd/main":  "#!/bin/bash\nnode server.js\n",
		} {
			if err := itw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body))}); err != nil {
				return err
			}
			if _, err := itw.Write([]byte(body)); err != nil {
				return err
			}
		}
	} else {
		// 含 aarch64 ELF 的假二进制
		elf := make([]byte, 64)
		copy(elf, []byte{0x7F, 'E', 'L', 'F', 2, 1, 1})
		elf[18], elf[19] = 0xB7, 0x00 // e_machine = aarch64
		if err := itw.WriteHeader(&tar.Header{Name: "handnas", Mode: 0o755, Size: int64(len(elf))}); err != nil {
			return err
		}
		if _, err := itw.Write(elf); err != nil {
			return err
		}
	}
	if err := itw.Close(); err != nil {
		return err
	}

	// 外层：gzip 压缩的 tar，二进制放在 app.tgz 内
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)

	manifest := "appname = sample\nversion = 1.0.0\n"
	if err := tw.WriteHeader(&tar.Header{Name: "manifest", Mode: 0o644, Size: int64(len(manifest))}); err != nil {
		return err
	}
	if _, err := tw.Write([]byte(manifest)); err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: "app.tgz", Mode: 0o644, Size: int64(inner.Len())}); err != nil {
		return err
	}
	if _, err := tw.Write(inner.Bytes()); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}
