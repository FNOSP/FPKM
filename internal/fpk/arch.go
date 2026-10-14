package fpk

import (
	"archive/tar"
	"compress/gzip"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Arch 表示一个架构标识
type Arch string

const (
	ArchX86_64  Arch = "x86_64"
	ArchAarch64 Arch = "aarch64"
	ArchI386    Arch = "i386"
	ArchArm     Arch = "arm"
	ArchUnknown Arch = "unknown"
)

// PackageArchReport 描述包内二进制的架构情况
type PackageArchReport struct {
	// MultiArch 包内含多套架构的二进制（按架构目录分发）
	MultiArch bool
	// Archs 包内出现的所有架构（去重）
	Archs []Arch
	// HasBinary 包内是否存在 ELF/PE/Mach-O 二进制
	HasBinary bool
	// ScriptOnly 包内没有任何原生二进制（纯脚本/Node/Python/前端）
	ScriptOnly bool
	// UnsupportedReason 非空表示当前机器无法运行，内容是给用户看的原因
	UnsupportedReason string
}

// maxBinaryProbe 单个文件探测时读取的头部字节数
const maxBinaryProbe = 20

// maxScanEntries 扫描包内条目的上限，避免异常包耗尽资源
const maxScanEntries = 20000

// InspectPackageArch 扫描 .fpk 包内的文件头，判断其真实支持的架构。
//
// 背景：应用源里的 `platform` 字段并不可靠 —— 实测有应用被标成 arm，
// 包里却没有任何原生二进制（纯 Node/脚本/前端），在 x86 上完全可以运行。
// 因此判断"能否安装"应以**包内实际的二进制架构**为准，而不是标签。
func InspectPackageArch(fpkPath string) (*PackageArchReport, error) {
	f, err := os.Open(fpkPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// .fpk 可能是 tar 也可能是 gzip 压缩的 tar（实测两者都有：
	// fn-appstores 与 hand.nas.pro 均为 gzip）。若不先尝试 gzip，
	// tar 解析会直接报 "invalid tar header"，导致扫描不到任何二进制。
	var r io.Reader = f
	if gz, gzErr := gzip.NewReader(f); gzErr == nil {
		defer gz.Close()
		r = gz
	} else if _, seekErr := f.Seek(0, io.SeekStart); seekErr != nil {
		return nil, seekErr
	}

	// 内层 app.tgz 需要二次解包
	archs, hasBinary, err := scanTarForArch(r, true)
	if err != nil {
		return nil, err
	}

	report := &PackageArchReport{
		HasBinary:  hasBinary,
		ScriptOnly: !hasBinary,
		Archs:      dedupeArchs(archs),
	}

	// 判断是否含多套架构（如同时存在 x86_64 与 aarch64）
	if len(report.Archs) > 1 {
		for _, a := range report.Archs {
			if a == ArchX86_64 || a == ArchI386 {
				for _, b := range report.Archs {
					if b == ArchAarch64 || b == ArchArm {
						report.MultiArch = true
					}
				}
			}
		}
	}

	report.UnsupportedReason = reasonForCurrentArch(report)
	return report, nil
}

// scanTarForArch 递归扫描 tar 流（含内层 *.tgz）收集架构信息
func scanTarForArch(r io.Reader, allowNested bool) ([]Arch, bool, error) {
	tr := tar.NewReader(r)
	var (
		archs     []Arch
		hasBinary bool
		entries   int
	)

	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			// 包尾损坏时返回已收集的结果，不整体失败
			break
		}
		entries++
		if entries > maxScanEntries {
			break
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}

		name := strings.ToLower(filepath.Base(h.Name))

		// 内层 app.tgz：递归解析
		if allowNested && strings.HasSuffix(name, ".tgz") {
			// 用临时缓冲避免把整个包读进内存（大包可能几十 MB）
			tmp, terr := os.CreateTemp("", "fpkm-arch-*.tgz")
			if terr != nil {
				continue
			}
			if _, cerr := io.Copy(tmp, io.LimitReader(tr, 512<<20)); cerr != nil {
				tmp.Close()
				os.Remove(tmp.Name())
				continue
			}
			if _, serr := tmp.Seek(0, io.SeekStart); serr != nil {
				tmp.Close()
				os.Remove(tmp.Name())
				continue
			}
			// 内层 .tgz 通常是 gzip；但也见过直接放 tar 的情况，
			// 因此 gzip 打不开时退回按普通 tar 解析。
			if gz, gerr := gzip.NewReader(tmp); gerr == nil {
				sub, subHas, _ := scanTarForArch(gz, false)
				gz.Close()
				archs = append(archs, sub...)
				hasBinary = hasBinary || subHas
			} else if _, serr := tmp.Seek(0, io.SeekStart); serr == nil {
				sub, subHas, _ := scanTarForArch(tmp, false)
				archs = append(archs, sub...)
				hasBinary = hasBinary || subHas
			}
			tmp.Close()
			os.Remove(tmp.Name())
			continue
		}

		// 只探测体积足够、看起来可能是二进制的文件
		if h.Size < 4 {
			continue
		}
		// 跳过明显的数据文件，减少探测次数
		switch filepath.Ext(name) {
		case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".ico",
			".woff", ".woff2", ".ttf", ".otf", ".md", ".txt", ".json",
			".css", ".html", ".map", ".gz", ".zip":
			continue
		}

		head := make([]byte, maxBinaryProbe)
		n, rerr := io.ReadFull(tr, head)
		if rerr != nil && rerr != io.ErrUnexpectedEOF && rerr != io.EOF {
			continue
		}
		if n < 4 {
			continue
		}

		if a, ok := archFromHeader(head[:n]); ok {
			archs = append(archs, a)
			hasBinary = true
		}
	}

	return archs, hasBinary, nil
}

// archFromHeader 从文件头识别二进制架构
func archFromHeader(h []byte) (Arch, bool) {
	if len(h) < 4 {
		return "", false
	}

	// ELF：byte 18-19 = e_machine（小端）
	if h[0] == 0x7F && h[1] == 'E' && h[2] == 'L' && h[3] == 'F' {
		if len(h) < 20 {
			return ArchUnknown, true
		}
		machine := binary.LittleEndian.Uint16(h[18:20])
		switch machine {
		case 0x3E:
			return ArchX86_64, true
		case 0x03:
			return ArchI386, true
		case 0xB7:
			return ArchAarch64, true
		case 0x28:
			return ArchArm, true
		}
		return ArchUnknown, true
	}

	// PE (Windows)：MZ + PE\0\0 后再取 machine
	if h[0] == 'M' && h[1] == 'Z' {
		return ArchUnknown, true
	}

	// Mach-O
	if (h[0] == 0xFE && h[1] == 0xED && h[2] == 0xFA) ||
		(h[0] == 0xCF && h[1] == 0xFA && h[2] == 0xED) {
		return ArchUnknown, true
	}

	return "", false
}

// dedupeArchs 去重并稳定排序
func dedupeArchs(in []Arch) []Arch {
	seen := map[Arch]bool{}
	var out []Arch
	for _, a := range in {
		if a == "" || seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	return out
}

// reasonForCurrentArch 依据当前运行的架构判断包是否可用。
// 返回空字符串表示"可用"。
func reasonForCurrentArch(r *PackageArchReport) string {
	// 包内没有任何原生二进制 → 与架构无关，任何平台都能装
	if !r.HasBinary {
		return ""
	}

	cur := runtime.GOARCH
	compat := map[string][]Arch{
		"amd64": {ArchX86_64, ArchI386},
		"386":   {ArchI386},
		"arm64": {ArchAarch64, ArchArm},
		"arm":   {ArchArm},
	}

	allowed, known := compat[cur]
	if !known {
		// 未知架构不拦截，交给安装后的二进制兼容性检查
		return ""
	}

	for _, a := range r.Archs {
		// ArchUnknown（PE/Mach-O 等）不参与判断
		if a == ArchUnknown {
			continue
		}
		for _, ok := range allowed {
			if a == ok {
				return "" // 找到匹配架构
			}
		}
	}

	// 包内只含其他架构的二进制
	return formatArchList(r.Archs)
}

// formatArchList 生成"仅支持 xxx"的可读说明
func formatArchList(archs []Arch) string {
	var names []string
	for _, a := range archs {
		if a == ArchUnknown {
			continue
		}
		names = append(names, string(a))
	}
	if len(names) == 0 {
		names = []string{"未知"}
	}
	return strings.Join(names, "/")
}

// UnscriptOnlyUnsupported 判断"包内确实含二进制且与当前架构不匹配"。
// 纯脚本包（ScriptOnly）永远返回 false —— 它们与架构无关。
func (r *PackageArchReport) UnscriptOnlyUnsupported() bool {
	if r == nil || r.ScriptOnly || !r.HasBinary {
		return false
	}
	return r.UnsupportedReason != ""
}
