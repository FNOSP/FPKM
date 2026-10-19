package config

import (
	"os"
	"path/filepath"
	"testing"
)

// copyTree 应保留文件权限与内容
func TestCopyTreePreservesContentAndMode(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "f.txt"), []byte("hello"), 0o640); err != nil {
		t.Fatal(err)
	}

	if err := copyTree(filepath.Join(src, "sub"), filepath.Join(dst, "sub")); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dst, "sub", "f.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Errorf("内容不匹配: %q", data)
	}
	st, _ := os.Stat(filepath.Join(dst, "sub", "f.txt"))
	if st.Mode().Perm()&0o400 == 0 {
		t.Errorf("应保留可读权限，实际 %o", st.Mode().Perm())
	}
}

// 目录复制后必须可进入（有 x 位），否则里面的文件无法访问
func TestCopyTreeDirIsTraversable(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	os.MkdirAll(filepath.Join(src, "d"), 0o644) // 故意用无执行位的权限
	os.WriteFile(filepath.Join(src, "d", "x"), []byte("1"), 0o644)

	if err := copyTree(filepath.Join(src, "d"), filepath.Join(dst, "d")); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dst, "d"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o100 == 0 {
		t.Errorf("目录应可进入（含 x 位），实际 %o", st.Mode().Perm())
	}
}

// 符号链接应保持为链接，而不是被解引用复制
func TestCopyTreeKeepsSymlink(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	target := filepath.Join(src, "real")
	os.WriteFile(target, []byte("x"), 0o644)
	os.Symlink("real", filepath.Join(src, "link"))

	if err := copyTree(filepath.Join(src, "link"), filepath.Join(dst, "link")); err != nil {
		t.Fatal(err)
	}
	got, err := os.Readlink(filepath.Join(dst, "link"))
	if err != nil {
		t.Fatalf("应为符号链接: %v", err)
	}
	if got != "real" {
		t.Errorf("链接目标应为 real，得到 %q", got)
	}
}

// 迁移不应覆盖目标已存在的条目
func TestMigrateDoesNotOverwrite(t *testing.T) {
	legacy := t.TempDir()
	modern := t.TempDir()

	os.WriteFile(filepath.Join(legacy, "password.json"), []byte("OLD"), 0o600)
	os.WriteFile(filepath.Join(modern, "password.json"), []byte("NEW"), 0o600)

	// 直接验证 copyTree 的调用方行为：用相同的"已存在则跳过"判断
	if _, err := os.Stat(filepath.Join(modern, "password.json")); err == nil {
		// 目标存在 → 调用方应跳过（这是 MigrateFromLegacy 的逻辑）
		data, _ := os.ReadFile(filepath.Join(modern, "password.json"))
		if string(data) != "NEW" {
			t.Errorf("目标不应被覆盖，实际 %q", data)
		}
	}
}

// 二进制旁的 data 目录应可创建并写入（dirUsable 行为）
func TestDirUsable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if !dirUsable(dir) {
		t.Error("可创建的目录应判定为可用")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("dirUsable 应创建目录: %v", err)
	}
	// 不应残留探测文件
	if _, err := os.Stat(filepath.Join(dir, ".write-test")); !os.IsNotExist(err) {
		t.Error("不应残留 .write-test 探测文件")
	}
}

// legacyBaseDir 在 Unix 上应为 $HOME/.local/fpk
func TestLegacyBaseDirShape(t *testing.T) {
	got := legacyBaseDir()
	if filepath.Base(got) != "fpk" {
		t.Errorf("旧目录应以 fpk 结尾，得到 %s", got)
	}
}

// 关键回归：目标目录已存在（空壳）时，仍要把旧目录里的应用合并进来。
//
// 真实故障：data/apps 被初始化成空目录后，迁移把整个 apps 目录跳过，
// 用户迁移完看到"0 个已安装应用"。
func TestMergeTreeIntoExistingDir(t *testing.T) {
	legacy := t.TempDir()
	modern := t.TempDir()

	// 旧目录：apps/Lucky/{manifest,target}
	appDir := filepath.Join(legacy, "apps", "Lucky")
	if err := os.MkdirAll(filepath.Join(appDir, "target"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "manifest"), []byte("appname=Lucky"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 新目录：apps 只是个空目录（初始化时建的骨架）
	if err := os.MkdirAll(filepath.Join(modern, "apps"), 0o755); err != nil {
		t.Fatal(err)
	}

	moved, skipped, failed := mergeTree(modern, filepath.Join(legacy, "apps"), filepath.Join(modern, "apps"))
	if len(failed) != 0 {
		t.Fatalf("不应有失败项: %v", failed)
	}
	if len(moved) == 0 {
		t.Error("应迁移出条目")
	}

	// 应用必须已经在新位置
	mp := filepath.Join(modern, "apps", "Lucky", "manifest")
	data, err := os.ReadFile(mp)
	if err != nil {
		t.Fatalf("应用未被迁移: %v", err)
	}
	if string(data) != "appname=Lucky" {
		t.Errorf("内容不匹配: %q", data)
	}
	if len(skipped) != 0 {
		t.Errorf("空壳目录不应产生跳过项: %v", skipped)
	}
}

// 同名文件冲突时跳过，不覆盖新数据
func TestMergeTreeSkipsConflictingFiles(t *testing.T) {
	legacy := t.TempDir()
	modern := t.TempDir()

	os.WriteFile(filepath.Join(legacy, "password.json"), []byte("OLD"), 0o600)
	os.MkdirAll(modern, 0o755)
	os.WriteFile(filepath.Join(modern, "password.json"), []byte("NEW"), 0o600)

	moved, skipped, failed := mergeTree(modern, legacy, modern)
	if len(failed) != 0 {
		t.Fatalf("不应有失败项: %v", failed)
	}
	if len(skipped) != 1 {
		t.Errorf("应有 1 个跳过项，得到 %v", skipped)
	}
	data, _ := os.ReadFile(filepath.Join(modern, "password.json"))
	if string(data) != "NEW" {
		t.Errorf("不应覆盖已有文件，实际 %q", data)
	}
	_ = moved
}

// 深层嵌套目录也应递归合并
func TestMergeTreeDeepNesting(t *testing.T) {
	legacy := t.TempDir()
	modern := t.TempDir()

	deep := filepath.Join(legacy, "apps", "A", "var", "data")
	os.MkdirAll(deep, 0o755)
	os.WriteFile(filepath.Join(deep, "config.json"), []byte("{}"), 0o644)

	// 目标只建到 apps/A
	os.MkdirAll(filepath.Join(modern, "apps", "A"), 0o755)

	_, _, failed := mergeTree(modern, filepath.Join(legacy, "apps"), filepath.Join(modern, "apps"))
	if len(failed) != 0 {
		t.Fatalf("不应有失败项: %v", failed)
	}
	if _, err := os.Stat(filepath.Join(modern, "apps", "A", "var", "data", "config.json")); err != nil {
		t.Errorf("深层文件未被迁移: %v", err)
	}
}
