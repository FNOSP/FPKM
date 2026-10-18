package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"fpkm/internal/config"
)

func testStore(t *testing.T) *autostartStore {
	t.Helper()
	dir := t.TempDir()
	return &autostartStore{path: filepath.Join(dir, "autostart.json")}
}

// 基本标记与读取
func TestAutostartMarkAndList(t *testing.T) {
	a := testStore(t)
	if got := a.list(); len(got) != 0 {
		t.Fatalf("初始应为空，得到 %v", got)
	}
	if err := a.mark("alpha"); err != nil {
		t.Fatal(err)
	}
	if err := a.mark("beta"); err != nil {
		t.Fatal(err)
	}
	got := a.list()
	if len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("期望 [alpha beta]，得到 %v", got)
	}
}

// 重复标记不应产生重复项（幂等）
func TestAutostartMarkIsIdempotent(t *testing.T) {
	a := testStore(t)
	for i := 0; i < 5; i++ {
		if err := a.mark("alpha"); err != nil {
			t.Fatal(err)
		}
	}
	if got := a.list(); len(got) != 1 {
		t.Fatalf("重复标记后应只有 1 项，得到 %v", got)
	}
}

// 取消标记（用户点停止/卸载）
func TestAutostartUnmark(t *testing.T) {
	a := testStore(t)
	_ = a.mark("alpha")
	_ = a.mark("beta")
	if err := a.unmark("alpha"); err != nil {
		t.Fatal(err)
	}
	got := a.list()
	if len(got) != 1 || got[0] != "beta" {
		t.Fatalf("期望 [beta]，得到 %v", got)
	}
	// 取消不存在的项不应报错
	if err := a.unmark("nothing"); err != nil {
		t.Fatalf("取消不存在的项不应报错: %v", err)
	}
}

// prune 清理已不存在的应用（卸载残留）
func TestAutostartPrune(t *testing.T) {
	a := testStore(t)
	_ = a.mark("alpha")
	_ = a.mark("gone")
	kept := a.prune(map[string]bool{"alpha": true})
	if len(kept) != 1 || kept[0] != "alpha" {
		t.Fatalf("期望保留 [alpha]，得到 %v", kept)
	}
	// prune 结果应已落盘
	if got := a.list(); len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("prune 后文件应为 [alpha]，得到 %v", got)
	}
}

// 文件损坏时不应崩溃，按空集处理
func TestAutostartCorruptFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "autostart.json")
	if err := os.WriteFile(p, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &autostartStore{path: p}
	if got := a.list(); len(got) != 0 {
		t.Fatalf("损坏文件应按空处理，得到 %v", got)
	}
	// 仍能正常写入
	if err := a.mark("alpha"); err != nil {
		t.Fatalf("损坏后应能重新写入: %v", err)
	}
	if got := a.list(); len(got) != 1 {
		t.Fatalf("期望 [alpha]，得到 %v", got)
	}
}

// 落盘格式应为合法 JSON 数组
func TestAutostartFileFormat(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "autostart.json")
	a := &autostartStore{path: p}
	_ = a.mark("alpha")

	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		t.Fatalf("文件应为合法 JSON 数组: %v (%s)", err, data)
	}
	if len(names) != 1 || names[0] != "alpha" {
		t.Fatalf("期望 [alpha]，得到 %v", names)
	}
}

// 不留下临时文件（原子写）
func TestAutostartNoTempLeftover(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "autostart.json")
	a := &autostartStore{path: p}
	_ = a.mark("alpha")
	_ = a.mark("beta")
	if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
		t.Error("原子写后不应残留 .tmp 文件")
	}
}

// 空名与重复名应被过滤
func TestAutostartFiltersEmptyAndDup(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "autostart.json")
	_ = os.WriteFile(p, []byte(`["a","","a","b"]`), 0o644)
	a := &autostartStore{path: p}
	got := a.list()
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("应过滤空名与重复，得到 %v", got)
	}
}

// 并发标记不丢数据
func TestAutostartConcurrentMark(t *testing.T) {
	a := testStore(t)
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(i int) {
			defer func() { done <- struct{}{} }()
			_ = a.mark(string(rune('a' + i)))
		}(i)
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	if got := a.list(); len(got) != 8 {
		t.Fatalf("并发标记应保留 8 项，得到 %d: %v", len(got), got)
	}
}

// AutostartFile 路径应在 BaseDir 下
func TestAutostartFileConfigPath(t *testing.T) {
	cfg := &config.Config{BaseDir: "/tmp/fpk-test"}
	want := filepath.Join("/tmp/fpk-test", "autostart.json")
	if got := cfg.AutostartFile(); got != want {
		t.Errorf("期望 %s，得到 %s", want, got)
	}
}
