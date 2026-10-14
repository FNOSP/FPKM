package web

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"fpkm/internal/config"
	"fpkm/internal/logger"
)

// iconCache 负责从 FPK 包中提取图标并缓存到磁盘。
//
// FPK 包根目录通常带 ICON.PNG / ICON_256.PNG；飞牛桌面就是用它们显示图标的。
// 远程源虽然提供 icon 外链，但外链依赖第三方图床（如 gitee），
// 内网/离线环境常加载失败，因此优先使用包内图标。
type iconCache struct {
	dir string

	mu    sync.Mutex
	file  map[string]string // appname -> 缓存文件路径（"" 表示已确认无图标）
	inFly map[string]bool
}

// maxIconBytes 单个图标上限（避免异常包塞入超大文件）
const maxIconBytes = 2 << 20 // 2 MiB

// newIconCache 创建图标缓存
func newIconCache(cfg *config.Config) *iconCache {
	dir := filepath.Join(cfg.BaseDir, "icons")
	_ = os.MkdirAll(dir, 0755)
	return &iconCache{dir: dir, file: map[string]string{}, inFly: map[string]bool{}}
}

// Lookup 返回应用图标文件的缓存路径；无图标返回空字符串。
//
// 数据来源优先级：
//  1. 已安装应用目录下的 ICON.PNG / ICON_256.PNG
//  2. 本地已下载的 .fpk 包内的同名文件
//  3. 远程源图标链接（由调用方处理，不在本函数内）
func (c *iconCache) Lookup(appname string, installedDir string) string {
	appname = strings.TrimSpace(appname)
	if appname == "" {
		return ""
	}

	c.mu.Lock()
	if p, ok := c.file[appname]; ok {
		c.mu.Unlock()
		if p != "" && fileExistsSimple(p) {
			return p
		}
		if p == "" {
			return ""
		}
		// 缓存文件被删了，重新提取
	}
	c.mu.Unlock()

	c.mu.Lock()
	if c.inFly[appname] {
		c.mu.Unlock()
		return ""
	}
	c.inFly[appname] = true
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.inFly, appname)
		c.mu.Unlock()
	}()

	dst := filepath.Join(c.dir, sanitizeFile(appname)+".png")

	// 1) 已安装目录
	if installedDir != "" {
		for _, name := range []string{"ICON_256.PNG", "ICON.PNG", "icon.png", "icon_256.png"} {
			src := filepath.Join(installedDir, name)
			if fileExistsSimple(src) {
				if err := copyFileTo(src, dst); err == nil {
					c.remember(appname, dst)
					return dst
				}
			}
		}
		// 有些包把图标放在 target/ui/images 下
		for _, rel := range []string{"target/ui/images/icon_256.png", "target/ui/images/icon_64.png", "ui/images/icon_256.png"} {
			src := filepath.Join(installedDir, rel)
			if fileExistsSimple(src) {
				if err := copyFileTo(src, dst); err == nil {
					c.remember(appname, dst)
					return dst
				}
			}
		}
	}

	c.remember(appname, "")
	return ""
}

// ExtractFromPackage 从 .fpk 包中提取图标到缓存；失败返回空字符串。
// 适用于"尚未安装但已下载"的远程应用。
func (c *iconCache) ExtractFromPackage(appname, fpkPath string) string {
	appname = strings.TrimSpace(appname)
	if appname == "" || fpkPath == "" {
		return ""
	}

	c.mu.Lock()
	if p, ok := c.file[appname]; ok && p != "" {
		c.mu.Unlock()
		return p
	}
	c.mu.Unlock()

	dst := filepath.Join(c.dir, sanitizeFile(appname)+".png")
	if err := extractIconFromFPK(fpkPath, dst); err != nil {
		logger.Debug("从包中提取图标失败 %s: %v", fpkPath, err)
		return ""
	}
	c.remember(appname, dst)
	return dst
}

// remember 记录缓存结果（path 为空表示确认无图标）
func (c *iconCache) remember(appname, path string) {
	c.mu.Lock()
	c.file[appname] = path
	c.mu.Unlock()
}

// extractIconFromFPK 从 tar/tar.gz 包中抽取第一个图标文件
func extractIconFromFPK(fpkPath, dst string) error {
	f, err := os.Open(fpkPath)
	if err != nil {
		return err
	}
	defer f.Close()

	var r io.Reader = f
	if gz, err := gzip.NewReader(f); err == nil {
		defer gz.Close()
		r = gz
	} else if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}

	// 优先取 256 尺寸；先扫一遍记录候选
	var fallback string
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(strings.TrimPrefix(h.Name, "./"), "/")
		upper := strings.ToUpper(name)

		isIcon := upper == "ICON_256.PNG" || upper == "ICON.PNG" ||
			strings.HasSuffix(upper, "/ICON_256.PNG") || strings.HasSuffix(upper, "/ICON.PNG")
		if !isIcon {
			continue
		}

		// 优先 256
		if strings.Contains(upper, "256") {
			if err := writeIcon(tr, dst); err != nil {
				return err
			}
			return nil
		}
		if fallback == "" {
			fallback = dst
		}
		// 没有 256 的情况下继续找，先记住这个
		if err := writeIcon(tr, dst); err != nil {
			return err
		}
		return nil
	}

	if fallback != "" {
		return nil
	}
	return fmt.Errorf("包中未找到图标")
}

// writeIcon 把 tar 条目写入目标文件
func writeIcon(r io.Reader, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, io.LimitReader(r, maxIconBytes))
	return err
}

// copyFileTo 复制文件（保留 0644）
func copyFileTo(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, io.LimitReader(in, maxIconBytes))
	return err
}

// fileExistsSimple 判断普通文件是否存在
func fileExistsSimple(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
