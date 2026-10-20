package app

import (
	"path/filepath"
	"testing"

	"fpkm/internal/config"
)

// socketCandidates 首要候选必须是 fpkm 配置的路径（符合飞牛规范的应用）
func TestSocketCandidatesPrefersConfiguredPath(t *testing.T) {
	cfg := testConfig(t)
	m := &Manager{cfg: cfg}

	configured := filepath.Join(cfg.AppTargetDir("demo"), "app.sock")
	got := m.socketCandidates("demo", configured)

	if len(got) == 0 {
		t.Fatal("候选列表不应为空")
	}
	if got[0] != configured {
		t.Errorf("首选候选应为配置路径 %q，实际 %q", configured, got[0])
	}
}

// 必须包含 fnOS 原生 @appcenter 布局（Lucky 这类定制构建硬编码的位置）
func TestSocketCandidatesIncludesAppcenter(t *testing.T) {
	cfg := testConfig(t)
	m := &Manager{cfg: cfg}

	configured := filepath.Join(cfg.AppTargetDir("Lucky"), "app.sock")
	got := m.socketCandidates("Lucky", configured)

	wantSuffix := filepath.Join("@appcenter", "Lucky", "app.sock")
	found := false
	for _, c := range got {
		if len(c) >= len(wantSuffix) && c[len(c)-len(wantSuffix):] == wantSuffix {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("候选应包含 @appcenter/Lucky/app.sock，实际: %v", got)
	}
}

// 候选应去重（配置路径若恰好等于某卷路径时不能重复）
func TestSocketCandidatesDeduped(t *testing.T) {
	cfg := testConfig(t)
	m := &Manager{cfg: cfg}

	got := m.socketCandidates("demo", "/vol1/@appcenter/demo/app.sock")
	seen := map[string]bool{}
	for _, c := range got {
		if seen[c] {
			t.Errorf("候选出现重复: %s", c)
		}
		seen[c] = true
	}
}

// socket 文件名应沿用配置里的名字，而不是写死 app.sock
func TestSocketCandidatesKeepsSocketName(t *testing.T) {
	cfg := testConfig(t)
	m := &Manager{cfg: cfg}

	configured := filepath.Join(cfg.AppTargetDir("demo"), "custom.sock")
	got := m.socketCandidates("demo", configured)

	for _, c := range got[1:] {
		if filepath.Base(c) != "custom.sock" {
			t.Errorf("候选 %q 应沿用 socket 名 custom.sock", c)
		}
	}
}

// testConfig 构造一个隔离的配置
func testConfig(t *testing.T) *config.Config {
	t.Helper()
	base := t.TempDir()
	return &config.Config{
		BaseDir: base,
		AppsDir: filepath.Join(base, "apps"),
		BinDir:  filepath.Join(base, "bin"),
		LogDir:  filepath.Join(base, "logs"),
	}
}
