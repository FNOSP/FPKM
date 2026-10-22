package web

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"fpkm/internal/config"
)

// newTestSourceStore 造一个只依赖临时目录的 SourceStore。
func newTestSourceStore(t *testing.T) (*SourceStore, string) {
	t.Helper()
	dir := t.TempDir()
	return NewSourceStore(&config.Config{BaseDir: dir}), filepath.Join(dir, "sources.json")
}

// writeSources 直接写配置文件，模拟"历史遗留 / 用户手工编辑"的配置。
func writeSources(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("写入 %s 失败: %v", path, err)
	}
}

// findSource 按 ID 查找生效源。
func findSource(list []Source, id string) (Source, bool) {
	for _, s := range list {
		if s.ID == id {
			return s, true
		}
	}
	return Source{}, false
}

// TestBuiltinSourcesPresentWithoutConfig 没有配置文件时，内置源必须在列。
func TestBuiltinSourcesPresentWithoutConfig(t *testing.T) {
	store, _ := newTestSourceStore(t)

	list, err := store.Load()
	if err != nil {
		t.Fatalf("Load 报错: %v", err)
	}
	official, ok := findSource(list, "official")
	if !ok {
		t.Fatalf("内置源缺失，实际列表: %+v", list)
	}
	if !official.Builtin {
		t.Error("内置源未标记 Builtin")
	}
	if !official.Enabled || official.URL == "" {
		t.Errorf("内置源应默认启用且带地址，实际: %+v", official)
	}
}

// TestBuiltinSurvivesStaleConfig 配置里只剩历史遗留的失效源时，
// 内置源仍应出现（这正是"客户端没有内置应用源"的直接原因）。
func TestBuiltinSurvivesStaleConfig(t *testing.T) {
	store, path := newTestSourceStore(t)
	writeSources(t, path, `[{"id":"mock","name":"Mock源","url":"http://127.0.0.1:18765","enabled":true}]`)

	list, err := store.Load()
	if err != nil {
		t.Fatalf("Load 报错: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("期望 内置源 + Mock 源 共 2 条，实际 %d 条: %+v", len(list), list)
	}
	if list[0].ID != "official" || !list[0].Builtin {
		t.Errorf("内置源应排在最前，实际首条: %+v", list[0])
	}
	if _, ok := findSource(list, "mock"); !ok {
		t.Error("用户自定义源丢失")
	}
}

// TestBuiltinPresentWhenConfigEmptyOrCorrupt 配置为空或损坏都不能让内置源消失。
func TestBuiltinPresentWhenConfigEmptyOrCorrupt(t *testing.T) {
	cases := []struct {
		name    string
		content string
	}{
		{"空数组", `[]`},
		{"损坏 JSON", `{"not":"an array"`},
		{"null", `null`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store, path := newTestSourceStore(t)
			writeSources(t, path, c.content)

			list, err := store.Load()
			if err != nil {
				t.Fatalf("Load 不应报错，实际: %v", err)
			}
			official, ok := findSource(list, "official")
			if !ok || !official.Builtin || !official.Enabled {
				t.Fatalf("内置源缺失或未启用: %+v", list)
			}
		})
	}
}

// TestBuiltinCanBeDisabledAndPersisted 内置源可以停用，且停用状态要能落盘。
func TestBuiltinCanBeDisabledAndPersisted(t *testing.T) {
	store, path := newTestSourceStore(t)

	list, err := store.Load()
	if err != nil {
		t.Fatalf("Load 报错: %v", err)
	}
	for i := range list {
		if list[i].ID == "official" {
			list[i].Enabled = false
		}
	}
	if err := store.Save(list); err != nil {
		t.Fatalf("Save 报错: %v", err)
	}

	// 磁盘上应只有一条覆盖项，而不是整个内置源定义
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取配置失败: %v", err)
	}
	var persisted []Source
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatalf("配置不是合法 JSON: %v (%s)", err, raw)
	}
	if len(persisted) != 1 || persisted[0].ID != "official" || persisted[0].Enabled {
		t.Fatalf("落盘内容应为停用覆盖项，实际: %s", raw)
	}

	// 重新加载后：内置源仍在列（标记 Builtin），但处于停用状态
	list2, err := store.Load()
	if err != nil {
		t.Fatalf("二次 Load 报错: %v", err)
	}
	official, ok := findSource(list2, "official")
	if !ok {
		t.Fatalf("重启后内置源消失: %+v", list2)
	}
	if !official.Builtin || official.Enabled {
		t.Errorf("内置源应为 存在且停用，实际: %+v", official)
	}
}

// TestBuiltinNotDuplicatedAcrossSaveLoad 反复存取不得产生重复内置源。
func TestBuiltinNotDuplicatedAcrossSaveLoad(t *testing.T) {
	store, _ := newTestSourceStore(t)

	list, err := store.Load()
	if err != nil {
		t.Fatalf("Load 报错: %v", err)
	}
	for round := 0; round < 3; round++ {
		if err := store.Save(list); err != nil {
			t.Fatalf("第 %d 轮 Save 报错: %v", round, err)
		}
		list, err = store.Load()
		if err != nil {
			t.Fatalf("第 %d 轮 Load 报错: %v", round, err)
		}
	}

	n := 0
	for _, s := range list {
		if s.ID == "official" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("内置源重复出现 %d 次: %+v", n, list)
	}
}

// TestSaveEmptyListKeepsBuiltin 保存空列表（例如删掉最后一个自定义源）
// 之后，内置源仍然可用。
func TestSaveEmptyListKeepsBuiltin(t *testing.T) {
	store, _ := newTestSourceStore(t)

	if err := store.Save(nil); err != nil {
		t.Fatalf("Save 报错: %v", err)
	}
	list, err := store.Load()
	if err != nil {
		t.Fatalf("Load 报错: %v", err)
	}
	official, ok := findSource(list, "official")
	if !ok || !official.Enabled {
		t.Fatalf("内置源应保持启用: %+v", list)
	}
}

// TestCustomSourceRoundTrip 自定义源可正常增删（内置源不受影响）。
func TestCustomSourceRoundTrip(t *testing.T) {
	store, _ := newTestSourceStore(t)

	list, err := store.Load()
	if err != nil {
		t.Fatalf("Load 报错: %v", err)
	}
	list = append(list, Source{ID: "my-mirror", Name: "我的镜像", URL: "http://mirror.local:5660", Enabled: true})
	if err := store.Save(list); err != nil {
		t.Fatalf("Save 报错: %v", err)
	}

	list2, err := store.Load()
	if err != nil {
		t.Fatalf("Load 报错: %v", err)
	}
	custom, ok := findSource(list2, "my-mirror")
	if !ok {
		t.Fatal("自定义源丢失")
	}
	if custom.Builtin {
		t.Error("自定义源不应被标记为内置源")
	}
	if _, ok := findSource(list2, "official"); !ok {
		t.Error("内置源丢失")
	}
}

// TestBuiltinIDIsProtected 删除判定：内置源 ID 必须被识别出来。
func TestBuiltinIDIsProtected(t *testing.T) {
	if !isBuiltinSourceID("official") {
		t.Error("official 应被识别为内置源 ID")
	}
	for _, id := range []string{"", "   ", "my-mirror", "Official"} {
		if isBuiltinSourceID(id) {
			t.Errorf("%q 不应被识别为内置源 ID", id)
		}
	}
}