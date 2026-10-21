package web

import (
	"os"
	"path/filepath"
	"testing"

	"fpkm/internal/config"
)

// splitDepApps 解析 manifest 的 install_dep_apps
func TestSplitDepApps(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"python312", []string{"python312"}},
		{"python312,nodejs_v24", []string{"python312", "nodejs_v24"}},
		{" python312 , nodejs_v24 ", []string{"python312", "nodejs_v24"}},
		{"python312,nodejs_v24,python312", []string{"python312", "nodejs_v24"}},
		{"python312;bunjs", []string{"python312", "bunjs"}},
	}
	for _, c := range cases {
		got := splitDepApps(c.in)
		if len(got) != len(c.want) {
			t.Errorf("%q → %v，期望 %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%q → %v，期望 %v", c.in, got, c.want)
				break
			}
		}
	}
}

// firstTag 取第一个标签作为分类
func TestFirstTag(t *testing.T) {
	cases := map[string]string{
		"":          "",
		"开发工具":      "开发工具",
		"开发工具,网络":   "开发工具",
		" 工具 , 网络 ": "工具",
	}
	for in, want := range cases {
		if got := firstTag(in); got != want {
			t.Errorf("%q → %q，期望 %q", in, got, want)
		}
	}
}

// depAppsForPackage 从安装包读取依赖
func TestDepAppsForPackageMissing(t *testing.T) {
	if got := depAppsForPackage("/nonexistent/x.fpk"); got != nil {
		t.Errorf("不存在的包应返回 nil，得到 %v", got)
	}
}

// resolveDepApps 未安装且不在应用源时，仍返回条目但标记不可用
func TestResolveDepAppsUnknown(t *testing.T) {
	base := t.TempDir()
	s := &Server{cfg: &config.Config{BaseDir: base, AppsDir: filepath.Join(base, "apps")}}
	os.MkdirAll(s.cfg.AppsDir, 0o755)

	got := s.resolveDepApps([]string{"nosuchruntime"})
	if len(got) != 1 {
		t.Fatalf("应返回 1 条，得到 %d", len(got))
	}
	if got[0].Installed {
		t.Error("不应标记为已安装")
	}
	if got[0].Available {
		t.Error("应用源找不到时 Available 应为 false")
	}
	if got[0].Display != "nosuchruntime" {
		t.Errorf("展示名应回退为应用名，得到 %q", got[0].Display)
	}
}

// 已安装的依赖应被识别，并带出版本
func TestResolveDepAppsInstalled(t *testing.T) {
	base := t.TempDir()
	cfg := &config.Config{BaseDir: base, AppsDir: filepath.Join(base, "apps")}
	appDir := filepath.Join(cfg.AppsDir, "python312")
	os.MkdirAll(appDir, 0o755)
	os.WriteFile(filepath.Join(appDir, "manifest"),
		[]byte("appname=python312\nversion=3.12.13\ndisplay_name=Python 3.12\n"), 0o644)

	s := &Server{cfg: cfg}
	got := s.resolveDepApps([]string{"python312"})
	if len(got) != 1 {
		t.Fatalf("应返回 1 条，得到 %d", len(got))
	}
	if !got[0].Installed {
		t.Error("应标记为已安装")
	}
	if got[0].Version != "3.12.13" {
		t.Errorf("版本应为 3.12.13，得到 %q", got[0].Version)
	}
	if got[0].Display != "Python 3.12" {
		t.Errorf("展示名应为 Python 3.12，得到 %q", got[0].Display)
	}
}
