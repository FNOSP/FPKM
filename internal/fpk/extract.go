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

// Extract 解压 .fpk 文件到目标目录
// .fpk 文件本质是 tar 归档，可能包含 gzip 压缩
// 内部可能含有 app.tgz（应用文件的 gzip+tar 归档）
func Extract(fpkPath, destDir string) error {
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("创建目标目录失败: %w", err)
	}

	file, err := os.Open(fpkPath)
	if err != nil {
		return fmt.Errorf("打开fpk文件失败: %w", err)
	}
	defer file.Close()

	// 尝试按 gzip 读取
	if err := tryGzipTar(file, destDir); err != nil {
		// gzip 失败，尝试按原始 tar 读取
		if _, seekErr := file.Seek(0, 0); seekErr != nil {
			return fmt.Errorf("重置文件指针失败: %w", seekErr)
		}
		if err := untar(file, destDir); err != nil {
			return fmt.Errorf("解压fpk失败: %w", err)
		}
	}

	// 检查并解压内部的 app.tgz
	appTgz := filepath.Join(destDir, "app.tgz")
	if _, err := os.Stat(appTgz); err == nil {
		appDir := filepath.Join(destDir, "app")
		if err := os.MkdirAll(appDir, 0755); err != nil {
			return fmt.Errorf("创建app目录失败: %w", err)
		}
		if err := extractGzipTar(appTgz, appDir); err != nil {
			return fmt.Errorf("解压app.tgz失败: %w", err)
		}
		// 删除 app.tgz 临时文件
		os.Remove(appTgz)
	}

	return nil
}

// tryGzipTar 尝试以 gzip 方式解压 tar
func tryGzipTar(file *os.File, destDir string) error {
	gzr, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzr.Close()
	return untar(gzr, destDir)
}

// extractGzipTar 解压 gzip+tar 文件
func extractGzipTar(srcPath, destDir string) error {
	file, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer file.Close()

	gzr, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzr.Close()

	return untar(gzr, destDir)
}

// untar 从 tar 流解压到目标目录
// 注意：为支持非 root 用户安装，解压时会调整权限，确保当前用户可访问
func untar(reader io.Reader, destDir string) error {
	tr := tar.NewReader(reader)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		// 安全检查：防止路径穿越
		target := filepath.Join(destDir, header.Name)
		if !isSafePath(destDir, target) {
			return fmt.Errorf("不安全的路径: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			// 使用安全的目录权限：确保当前用户可读写执行
			// 保留原权限的执行位，但确保至少有 0755
			dirMode := os.FileMode(header.Mode) | 0755
			if err := os.MkdirAll(target, dirMode); err != nil {
				return fmt.Errorf("创建目录失败 %s: %w", target, err)
			}

		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			// 使用安全的文件权限：确保当前用户可读写
			// 如果原文件有执行权限（0111），保留执行权限
			fileMode := os.FileMode(header.Mode)
			if fileMode&0111 != 0 {
				fileMode = fileMode | 0755
			} else {
				fileMode = fileMode | 0644
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fileMode)
			if err != nil {
				return fmt.Errorf("创建文件失败 %s: %w", target, err)
			}
			// 先读几个字节判断是不是 shebang 脚本（#!），
			// 再原样写回，不改变文件内容。
			var head [4]byte
			hn, _ := io.ReadFull(tr, head[:])
			isScript := hn >= 2 && head[0] == '#' && head[1] == '!'
			if hn > 0 {
				if _, werr := f.Write(head[:hn]); werr != nil {
					f.Close()
					return fmt.Errorf("写入文件失败 %s: %w", target, werr)
				}
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return fmt.Errorf("写入文件失败 %s: %w", target, err)
			}
			f.Close()

			// 关键：显式 chmod。
			//
			// os.OpenFile 的 perm 只对"新建"文件生效；升级安装时目标已存在，
			// 旧权限会被原样保留，导致新版本修正过的权限无法落地。
			//
			// 另外：部分 .fpk 打包时漏给脚本执行位（真实案例 Octop 的
			// bin/octop 是 644，而同包的 bin/octop-cli 是 755），fnOS 下能跑
			// 是因为打包环境权限更宽松。这里对 shebang 脚本补执行位，
			// 否则启动时报 "Permission denied"。
			finalMode := fileMode
			if isScript && finalMode&0111 == 0 {
				finalMode |= 0755
			}
			if err := os.Chmod(target, finalMode); err != nil {
				return fmt.Errorf("设置文件权限失败 %s: %w", target, err)
			}

		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			os.Symlink(header.Linkname, target)

		case tar.TypeXGlobalHeader:
			continue
		}
	}

	return nil
}

// isSafePath 检查目标路径是否在基础目录内（防止路径穿越）
func isSafePath(baseDir, target string) bool {
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return false
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	return strings.HasPrefix(absTarget, absBase)
}
