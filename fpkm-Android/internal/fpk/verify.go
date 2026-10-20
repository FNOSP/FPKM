package fpk

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// VerifyResult 校验结果
type VerifyResult struct {
	Valid     bool
	Errors    []string
	Warnings  []string
	FileCount int
	TotalSize int64
	Checksum  string
}

// VerifyPackage 校验 FPK 包的完整性和结构
func VerifyPackage(fpkPath string) (*VerifyResult, error) {
	result := &VerifyResult{Valid: true}

	// 1. 检查文件是否存在
	info, err := os.Stat(fpkPath)
	if err != nil {
		return nil, fmt.Errorf("文件不存在: %w", err)
	}
	result.TotalSize = info.Size()

	// 2. 计算文件校验和
	checksum, err := calcSHA256(fpkPath)
	if err != nil {
		return nil, fmt.Errorf("计算校验和失败: %w", err)
	}
	result.Checksum = checksum

	// 3. 尝试解压到临时目录验证结构
	tmpDir, err := os.MkdirTemp("", "fpkm-verify-*")
	if err != nil {
		return nil, fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := Extract(fpkPath, tmpDir); err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, fmt.Sprintf("解压失败: %v", err))
		return result, nil
	}

	// 4. 检查必需文件
	manifestPath := filepath.Join(tmpDir, "manifest")
	if _, err := os.Stat(manifestPath); err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, "缺少 manifest 文件")
	} else {
		// 验证 manifest 内容
		m, err := ParseManifest(manifestPath)
		if err != nil {
			result.Valid = false
			result.Errors = append(result.Errors, fmt.Sprintf("manifest解析失败: %v", err))
		} else {
			if m.Appname == "" {
				result.Valid = false
				result.Errors = append(result.Errors, "manifest缺少 appname 字段")
			}
			if m.Version == "" {
				result.Warnings = append(result.Warnings, "manifest未指定 version")
			}
		}
	}

	// 5. 检查图标
	for _, icon := range []string{"ICON.PNG", "ICON_256.PNG"} {
		if _, err := os.Stat(filepath.Join(tmpDir, icon)); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("缺少 %s", icon))
		}
	}

	// 6. 检查 app 目录
	appDir := filepath.Join(tmpDir, "app")
	if _, err := os.Stat(appDir); err != nil {
		result.Warnings = append(result.Warnings, "缺少 app 目录")
	}

	// 7. 统计文件数
	result.FileCount = countFiles(tmpDir)

	return result, nil
}

// calcSHA256 计算文件 SHA256
func calcSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

// countFiles 递归统计目录下文件数
func countFiles(dir string) int {
	count := 0
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			count++
		}
		return nil
	})
	return count
}
