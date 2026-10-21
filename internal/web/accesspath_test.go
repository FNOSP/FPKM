package web

import (
	"os"
	"path/filepath"
	"testing"

	"fpkm/internal/app"
	"fpkm/internal/config"
)

// makePortApp 构造一个端口型应用目录：target/ui/config 声明 url
func makePortApp(t *testing.T, uiConfig string) (appDir string) {
	t.Helper()
	appDir = t.TempDir()
	uiDir := filepath.Join(appDir, "target", "ui")
	if err := os.MkdirAll(uiDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if uiConfig != "" {
		if err := os.WriteFile(filepath.Join(uiDir, "config"), []byte(uiConfig), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return appDir
}

func testCfg() *config.Config {
	return &config.Config{GatewayPort: 7666}
}

const panelUIConfig = `{
  ".url": {
    "wb2api.Application": {
      "type": "url",
      "port": "${port}",
      "url": "/panel",
      "gatewayPrefix": "",
      "gatewaySocket": "",
      "protocol": "http",
      "title": "workbuddy2api"
    }
  }
}`

// 关键回归：端口型应用的打开路径必须取 ui/config 的 url，不能硬编码 "/"。
//
// 真实故障（wb2api 1.13.0）：ui/config 声明 url="/panel"，
// 应用只在 /panel/ 提供面板，根路径返回 404。
// 之前 AccessPath 写死 "/"，用户点"打开"必然 404。
func TestDescribeAccessPortUsesUIConfigPath(t *testing.T) {
	appDir := makePortApp(t, panelUIConfig)
	env := app.EnvMap{"TRIM_SERVICE_PORT": "7863"}

	got := describeAccessFor(testCfg(), "wb2api", "7863", env, appDir)

	if got.Kind != "port" {
		t.Fatalf("应为 port 类型，得到 %q", got.Kind)
	}
	if got.AccessPort != 7863 {
		t.Errorf("端口应为 7863，得到 %d", got.AccessPort)
	}
	if got.AccessPath != "/panel" {
		t.Errorf("打开路径应取 ui/config 的 /panel，得到 %q", got.AccessPath)
	}
}

// env 里的 LAUNCH_PATH 优先（启动流程已按 ui/config 同步过）
func TestDescribeAccessPortPrefersLaunchPathEnv(t *testing.T) {
	appDir := makePortApp(t, panelUIConfig)
	env := app.EnvMap{
		"TRIM_SERVICE_PORT": "7863",
		"LAUNCH_PATH":       "/panel/",
	}

	got := describeAccessFor(testCfg(), "wb2api", "7863", env, appDir)
	if got.AccessPath != "/panel/" {
		t.Errorf("应优先使用 LAUNCH_PATH，得到 %q", got.AccessPath)
	}
}

// ui/config 未声明 url 时回退 "/"，保持旧行为
func TestDescribeAccessPortFallsBackToRoot(t *testing.T) {
	appDir := makePortApp(t, "")
	env := app.EnvMap{"TRIM_SERVICE_PORT": "8080"}

	got := describeAccessFor(testCfg(), "plain", "8080", env, appDir)
	if got.AccessPath != "/" {
		t.Errorf("无 ui/config 时应回退 \"/\"，得到 %q", got.AccessPath)
	}
	if got.AccessPort != 8080 {
		t.Errorf("端口应为 8080，得到 %d", got.AccessPort)
	}
}

// ui/config 的 url 若是绝对地址（http://...）不应当成路径使用
func TestDescribeAccessPortIgnoresAbsoluteURL(t *testing.T) {
	appDir := makePortApp(t, `{".url":{"x":{"type":"url","url":"http://example.com/foo","port":"${port}"}}}`)
	env := app.EnvMap{"TRIM_SERVICE_PORT": "9000"}

	got := describeAccessFor(testCfg(), "abs", "9000", env, appDir)
	if got.AccessPath != "/" {
		t.Errorf("绝对 URL 不应作为路径，应回退 \"/\"，得到 %q", got.AccessPath)
	}
}
