package fpk

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// PackageInfo 描述一个 .fpk 包的元信息（不安装即可获知）
type PackageInfo struct {
	Path     string `json:"path"`
	FileName string `json:"fileName"`
	Size     int64  `json:"size"`

	Appname     string `json:"appname"`
	Version     string `json:"version"`
	DisplayName string `json:"displayName"`
	Desc        string `json:"desc"`
	Maintainer  string `json:"maintainer"`
	Platform    string `json:"platform"`

	ServicePort   string `json:"servicePort,omitempty"`
	ServiceSocket string `json:"serviceSocket,omitempty"`
	InstallType   string `json:"installType,omitempty"`
	// InstallDepApps 是 manifest 声明的依赖应用（逗号分隔的应用名，
	// 如 python312,nodejs_v24）。安装前需要先把它们装好。
	InstallDepApps string `json:"installDepApps,omitempty"`

	// HasIcon 表示包内是否存在 ICON.PNG / ICON_256.PNG
	HasIcon bool `json:"hasIcon"`
	// Checksum 为包文件的 sha256（用于界面展示/校验）
	Checksum string `json:"checksum,omitempty"`
}

// maxManifestBytes 限制从包中读取 manifest 的最大体积，避免恶意包撑爆内存
const maxManifestBytes = 1 << 20 // 1 MiB

// ReadManifestFromPackage 直接从 .fpk（tar 或 tar.gz）中提取 manifest，
// 无需将整个包解压到磁盘。用于安装前的预览。
func ReadManifestFromPackage(fpkPath string) (*Manifest, error) {
	f, err := os.Open(fpkPath)
	if err != nil {
		return nil, fmt.Errorf("打开fpk文件失败: %w", err)
	}
	defer f.Close()

	// .fpk 可能是纯 tar，也可能是 gzip 压缩的 tar
	var r io.Reader = f
	gz, gzErr := gzip.NewReader(f)
	if gzErr == nil {
		defer gz.Close()
		r = gz
	} else if _, seekErr := f.Seek(0, io.SeekStart); seekErr != nil {
		return nil, fmt.Errorf("重置文件指针失败: %w", seekErr)
	}

	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("读取包内容失败: %w", err)
		}
		name := strings.TrimPrefix(strings.TrimPrefix(h.Name, "./"), "/")
		if name != "manifest" {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(tr, maxManifestBytes))
		if err != nil {
			return nil, fmt.Errorf("读取manifest失败: %w", err)
		}
		return ParseManifestContent(data)
	}

	return nil, fmt.Errorf("包中未找到 manifest 文件")
}

// InspectPackage 读取包元信息（含 manifest 解析与图标存在性判断）
// withChecksum 为 true 时计算 sha256（大包会较慢）
func InspectPackage(fpkPath string, withChecksum bool) (*PackageInfo, error) {
	info, err := os.Stat(fpkPath)
	if err != nil {
		return nil, fmt.Errorf("文件不存在: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s 是目录，不是 .fpk 文件", fpkPath)
	}

	m, err := ReadManifestFromPackage(fpkPath)
	if err != nil {
		return nil, err
	}

	pkg := &PackageInfo{
		Path:          fpkPath,
		FileName:      filepath.Base(fpkPath),
		Size:          info.Size(),
		Appname:       m.Appname,
		Version:       m.Version,
		DisplayName:   m.DisplayName,
		Desc:          m.Desc,
		Maintainer:    m.Maintainer,
		Platform:      m.Platform,
		ServicePort:   m.ServicePort,
		ServiceSocket: m.ServiceSocket,
		InstallType:   m.InstallType,

		InstallDepApps: m.InstallDepApps,
	}

	if pkg.DisplayName == "" {
		pkg.DisplayName = m.Appname
	}

	pkg.HasIcon = packageHasIcon(fpkPath)

	if withChecksum {
		if sum, err := calcSHA256(fpkPath); err == nil {
			pkg.Checksum = sum
		}
	}

	return pkg, nil
}

// packageHasIcon 扫描包中是否存在 ICON.PNG / ICON_256.PNG
func packageHasIcon(fpkPath string) bool {
	f, err := os.Open(fpkPath)
	if err != nil {
		return false
	}
	defer f.Close()

	var r io.Reader = f
	if gz, err := gzip.NewReader(f); err == nil {
		defer gz.Close()
		r = gz
	} else {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return false
		}
	}

	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err != nil {
			return false
		}
		name := strings.TrimPrefix(strings.TrimPrefix(h.Name, "./"), "/")
		if name == "ICON.PNG" || name == "ICON_256.PNG" ||
			strings.EqualFold(name, "icon.png") || strings.EqualFold(name, "icon_256.png") {
			return true
		}
	}
}

// ReadFileFromPackage 从 .fpk（tar 或 tar.gz）中读取指定路径的文件内容。
// 找不到该文件时返回 os.ErrNotExist，便于调用方区分"没有"与"读失败"。
func ReadFileFromPackage(fpkPath, name string) ([]byte, error) {
	f, err := os.Open(fpkPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var r io.Reader = f
	if gz, gzErr := gzip.NewReader(f); gzErr == nil {
		defer gz.Close()
		r = gz
	} else if _, seekErr := f.Seek(0, io.SeekStart); seekErr != nil {
		return nil, seekErr
	}

	want := strings.TrimPrefix(strings.TrimPrefix(name, "./"), "/")
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		got := strings.TrimPrefix(strings.TrimPrefix(h.Name, "./"), "/")
		if got != want {
			continue
		}
		return io.ReadAll(io.LimitReader(tr, maxManifestBytes))
	}
	return nil, os.ErrNotExist
}

// ReadI18nFromPackage 从包中读取 i18n 语言文件并解析成文案字典。
//
// 飞牛的 i18n 是 INI 风格、按语言分文件（i18n/zh-CN 等）。
// 这里按简体中文 → 英文的优先级挑选，返回 section.key → 文案。
func ReadI18nFromPackage(fpkPath string) map[string]string {
	// 先列出 i18n/ 下的候选文件，避免把整个包读两遍
	names, err := listPackageEntries(fpkPath, "i18n/")
	if err != nil || len(names) == 0 {
		return nil
	}

	preferred := []string{"zh-CN", "zh_CN", "zh-cn", "zh-Hans", "en-US", "en_US", "en"}
	pick := ""
	for _, want := range preferred {
		for _, n := range names {
			if strings.EqualFold(n, "i18n/"+want) {
				pick = n
				break
			}
		}
		if pick != "" {
			break
		}
	}
	if pick == "" {
		pick = names[0]
	}

	data, err := ReadFileFromPackage(fpkPath, pick)
	if err != nil {
		return nil
	}
	return ParseI18nINI(string(data))
}

// listPackageEntries 列出包中指定前缀下的文件（不含目录自身）
func listPackageEntries(fpkPath, prefix string) ([]string, error) {
	f, err := os.Open(fpkPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var r io.Reader = f
	if gz, gzErr := gzip.NewReader(f); gzErr == nil {
		defer gz.Close()
		r = gz
	} else if _, seekErr := f.Seek(0, io.SeekStart); seekErr != nil {
		return nil, seekErr
	}

	var out []string
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		name := strings.TrimPrefix(strings.TrimPrefix(h.Name, "./"), "/")
		if strings.HasPrefix(name, prefix) && !strings.Contains(strings.TrimPrefix(name, prefix), "/") {
			out = append(out, name)
		}
	}
	return out, nil
}
