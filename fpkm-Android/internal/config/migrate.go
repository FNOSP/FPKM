package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"fpkm/internal/logger"
)

// MigrateResult 描述一次数据迁移的结果。
type MigrateResult struct {
	From     string   // 旧目录
	To       string   // 新目录
	Moved    []string // 已迁移的条目
	Skipped  []string // 因目标已存在而跳过的条目
	Failed   []string // 迁移失败的条目
	NoSource bool     // 旧目录不存在，无需迁移
}

// MigrateFromLegacy 把旧位置（$HOME/.local/fpk）的数据搬到新位置
// （可执行文件旁的 data/）。
//
// 设计取舍：
//   - **不覆盖**：目标已存在的条目一律跳过，避免把新数据冲掉；
//   - **不删除源**：迁移后保留旧目录，用户确认无误后可自行清理；
//   - 逐项搬运，单个失败不影响其它条目，最后汇总回报。
//
// 之所以做成显式命令而不是启动时自动执行：自动搬运大量文件可能
// 让首次启动卡住，也会在用户不知情时改动磁盘布局。
func MigrateFromLegacy() (*MigrateResult, error) {
	cfg := GetConfig()
	from := legacyBaseDir()
	to := cfg.BaseDir

	res := &MigrateResult{From: from, To: to}

	if filepath.Clean(from) == filepath.Clean(to) {
		res.NoSource = true
		return res, nil
	}
	if _, err := os.Stat(from); err != nil {
		res.NoSource = true
		return res, nil
	}
	if err := os.MkdirAll(to, 0o755); err != nil {
		return nil, fmt.Errorf("创建目标目录失败: %w", err)
	}

	entries, err := os.ReadDir(from)
	if err != nil {
		return nil, fmt.Errorf("读取旧目录失败: %w", err)
	}

	for _, e := range entries {
		src := filepath.Join(from, e.Name())
		dst := filepath.Join(to, e.Name())

		// 目录要递归合并，不能"目标存在就整体跳过"。
		//
		// 曾经的 bug：目标 data/apps 已存在（空目录，可能只是初始化时
		// 建的骨架），旧目录里的应用就被整体跳过，用户迁移后看到
		// "0 个已安装应用"。目录必须进入内部逐项判断，只有**文件**
		// 冲突才跳过。
		if e.IsDir() {
			merged, skipped, failed := mergeTree(to, src, dst)
			if len(merged) > 0 {
				res.Moved = append(res.Moved, merged...)
			}
			if len(skipped) > 0 {
				res.Skipped = append(res.Skipped, skipped...)
			}
			if len(failed) > 0 {
				res.Failed = append(res.Failed, failed...)
			}
			continue
		}

		// 文件：目标已存在则跳过（不覆盖既有数据）
		if _, err := os.Stat(dst); err == nil {
			res.Skipped = append(res.Skipped, e.Name())
			continue
		}

		if err := copyTree(src, dst); err != nil {
			res.Failed = append(res.Failed, fmt.Sprintf("%s: %v", e.Name(), err))
			continue
		}
		res.Moved = append(res.Moved, e.Name())
	}

	logger.Info("数据迁移完成: %s → %s（成功 %d，跳过 %d，失败 %d）",
		from, to, len(res.Moved), len(res.Skipped), len(res.Failed))
	return res, nil
}

// copyTree 递归复制文件或目录，保留权限位。
//
// 用复制而不是 rename：新旧目录可能在不同文件系统（如 HOME 在
// 网络盘、程序在本地盘），rename 会因跨设备失败。
func copyTree(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}

	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)

	case info.IsDir():
		// 目录至少要有属主的 rwx；同时叠加源目录权限，
		// 避免源目录里缺少执行位时复制出无法进入的目录。
		dirMode := info.Mode().Perm() | 0o700
		if err := os.MkdirAll(dst, dirMode); err != nil {
			return err
		}
		// MkdirAll 对已存在目录不生效，显式对齐一次
		if err := os.Chmod(dst, dirMode); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil

	default:
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()

		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		// 复制后显式对齐权限：os.OpenFile 的 perm 只对新建文件生效
		return os.Chmod(dst, info.Mode().Perm())
	}
}

// mergeTree 把 src 目录的内容合并进 dst，返回已迁移/跳过/失败的条目。
//
// 与 copyTree 的区别：dst 可以已存在，按**逐项**方式合并 ——
// 目录继续递归，同名文件才跳过。这样"目标目录先被初始化成空壳"
// 也不会挡住真实的旧数据。
func mergeTree(base, src, dst string) (moved, skipped, failed []string) {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return nil, nil, []string{fmt.Sprintf("%s: 创建目录失败: %v", dst, err)}
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return nil, nil, []string{fmt.Sprintf("%s: 读取失败: %v", src, err)}
	}

	for _, e := range entries {
		s := filepath.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())

		if e.IsDir() {
			m, sk, f := mergeTree(base, s, d)
			moved = append(moved, m...)
			skipped = append(skipped, sk...)
			failed = append(failed, f...)
			continue
		}

		if _, err := os.Stat(d); err == nil {
			skipped = append(skipped, relToBase(base, d))
			continue
		}
		if err := copyTree(s, d); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", relToBase(base, d), err))
			continue
		}
		moved = append(moved, relToBase(base, d))
	}
	return moved, skipped, failed
}

// relToBase 返回相对数据根目录的路径，让迁移输出更易读。
func relToBase(base, p string) string {
	if rel, err := filepath.Rel(base, p); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return p
}
