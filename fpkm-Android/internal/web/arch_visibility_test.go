package web

import "testing"

// TestArchUsable 覆盖"只显示与当前架构匹配的应用"这条规则。
//
// 商店列表（handleStoreApps）与商店详情（detailForStore）都依赖它，
// 因此这里把它当成契约来测：arm64 设备上不能再出现标注 x86 的条目，
// 除非本地已下载的包实测证明它其实是纯脚本、任何架构都能跑。
func TestArchUsable(t *testing.T) {
	cases := []struct {
		name     string
		platform string
		verdict  archVerdict
		arch     string
		want     bool
	}{
		{"arm64 设备 + arm 标签", "arm", archVerdict{ok: true}, "arm64", true},
		{"arm64 设备 + arm64 标签", "arm64", archVerdict{ok: true}, "arm64", true},
		{"arm64 设备 + all 标签", "all", archVerdict{ok: true}, "arm64", true},
		{"arm64 设备 + 空标签", "", archVerdict{ok: true}, "arm64", true},
		{"arm64 设备 + x86 标签 → 隐藏", "x86", archVerdict{ok: true, warning: "可能不兼容"}, "arm64", false},
		{"arm64 设备 + x86_64 标签 → 隐藏", "x86_64", archVerdict{ok: true, warning: "可能不兼容"}, "arm64", false},
		{"arm64 设备 + 本地包实测纯脚本 → 仍显示", "x86", archVerdict{ok: true, proven: true}, "arm64", true},
		{"arm64 设备 + 本地包实测架构不符 → 隐藏", "arm", archVerdict{ok: false, reason: "仅含 x86_64 二进制", proven: true}, "arm64", false},

		{"amd64 设备 + x86 标签", "x86", archVerdict{ok: true}, "amd64", true},
		{"amd64 设备 + amd64 标签", "amd64", archVerdict{ok: true}, "amd64", true},
		{"amd64 设备 + 空标签", "", archVerdict{ok: true}, "amd64", true},
		{"amd64 设备 + arm 标签 → 隐藏", "arm", archVerdict{ok: true, warning: "可能不兼容"}, "amd64", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := archUsable(RemoteApp{ID: "test.app", Platform: c.platform}, c.verdict, c.arch)
			if got != c.want {
				t.Fatalf("platform=%q arch=%q verdict=%+v: 期望 %v，实际 %v",
					c.platform, c.arch, c.verdict, c.want, got)
			}
		})
	}
}

// TestArchUsableKeepsDedupResult 保护"先按架构去重、再按架构过滤"的组合效果：
// 同一应用同时存在 x86 与 arm 两条目时，arm64 设备保留 arm 条目并展示。
func TestArchUsableKeepsDedupResult(t *testing.T) {
	apps := []RemoteApp{
		{ID: "com.demo.app", Name: "Demo", Platform: "x86"},
		{ID: "com.demo.app", Name: "Demo", Platform: "arm"},
		{ID: "com.other.app", Name: "Other", Platform: "x86"},
	}

	// 复现 catalog.List 里的去重：同 key 保留 archMatchScore 更高的一条
	kept := map[string]int{}
	var all []RemoteApp
	for _, a := range apps {
		key := a.ID
		if idx, ok := kept[key]; ok {
			score := archMatchScoreFor(a.Platform, "arm64")
			if score > archMatchScoreFor(all[idx].Platform, "arm64") {
				all[idx] = a
			}
			continue
		}
		kept[key] = len(all)
		all = append(all, a)
	}

	var visible []string
	for _, a := range all {
		if archUsable(a, archVerdict{ok: true}, "arm64") {
			visible = append(visible, a.ID+"/"+a.Platform)
		}
	}

	if len(visible) != 1 || visible[0] != "com.demo.app/arm" {
		t.Fatalf("arm64 设备应只保留并展示 arm 条目，实际: %v", visible)
	}
}
