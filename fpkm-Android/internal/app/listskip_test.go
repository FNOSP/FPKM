package app

import (
	"os"
	"path/filepath"
	"testing"

	"fpkm/internal/config"
)

func skipTestCfg(t *testing.T) *config.Config {
	t.Helper()
	base := t.TempDir()
	cfg := &config.Config{
		BaseDir: base,
		AppsDir: filepath.Join(base, "apps"),
		BinDir:  filepath.Join(base, "bin"),
		LogDir:  filepath.Join(base, "logs"),
	}
	os.MkdirAll(cfg.AppsDir, 0o755)
	return cfg
}

// 正常应用应出现在列表，且不出现在 skipped 中
func TestListWithSkippedNormalApp(t *testing.T) {
	cfg := skipTestCfg(t)
	dir := filepath.Join(cfg.AppsDir, "good")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "manifest"), []byte("appname=good\nversion=1.0"), 0o644)

	apps, skipped, err := NewManager(cfg).ListWithSkipped()
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || apps[0].Name != "good" {
		t.Errorf("应返回 good，实际 %+v", apps)
	}
	if len(skipped) != 0 {
		t.Errorf("不应有 skipped，实际 %+v", skipped)
	}
}

// 缺 manifest 的目录应被报告，而不是静默消失
func TestListWithSkippedMissingManifest(t *testing.T) {
	cfg := skipTestCfg(t)
	dir := filepath.Join(cfg.AppsDir, "broken")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "other.txt"), []byte("x"), 0o644)

	apps, skipped, err := NewManager(cfg).ListWithSkipped()
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 0 {
		t.Errorf("不应有可用应用，实际 %+v", apps)
	}
	if len(skipped) != 1 {
		t.Fatalf("应报告 1 个被跳过的应用，实际 %+v", skipped)
	}
	if skipped[0].Name != "broken" {
		t.Errorf("名称应为 broken，得到 %q", skipped[0].Name)
	}
	if skipped[0].Reason == "" {
		t.Error("应给出跳过原因")
	}
}

// AppsDir 不存在时应视为"没有应用"，而不是报错
func TestListWithSkippedNoAppsDir(t *testing.T) {
	base := t.TempDir()
	cfg := &config.Config{
		BaseDir: base,
		AppsDir: filepath.Join(base, "not-exist"),
		BinDir:  filepath.Join(base, "bin"),
		LogDir:  filepath.Join(base, "logs"),
	}
	apps, skipped, err := NewManager(cfg).ListWithSkipped()
	if err != nil {
		t.Errorf("目录不存在不应报错: %v", err)
	}
	if len(apps) != 0 || len(skipped) != 0 {
		t.Errorf("应为空，实际 apps=%v skipped=%v", apps, skipped)
	}
}

// List 保持旧行为（只返回可用应用），保证既有调用方不受影响
func TestListUnchanged(t *testing.T) {
	cfg := skipTestCfg(t)
	dir := filepath.Join(cfg.AppsDir, "mix")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "manifest"), []byte("appname=mix"), 0o644)

	apps, err := NewManager(cfg).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 {
		t.Errorf("期望 1 个，得到 %d", len(apps))
	}
}
