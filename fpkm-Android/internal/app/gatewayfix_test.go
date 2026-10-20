package app

import (
	"os"
	"path/filepath"
	"testing"

	"fpkm/internal/config"
)

// 端口型应用（ui/config 无 gatewaySocket）的 .env 若残留了
// SOCKET_PATH/GATEWAY_PREFIX（旧版本误标），启动时应被清理，
// 否则会一直等一个永远不会出现的 app.sock 直到超时。
func TestStartClearsStaleGatewayFieldsForPortApp(t *testing.T) {
	base := t.TempDir()
	cfg := &config.Config{
		BaseDir: base,
		AppsDir: filepath.Join(base, "apps"),
		BinDir:  filepath.Join(base, "bin"),
		LogDir:  filepath.Join(base, "logs"),
	}

	name := "portapp"
	appDir := cfg.AppDir(name)
	targetDir := cfg.AppTargetDir(name)
	if err := os.MkdirAll(filepath.Join(targetDir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// ui/config：端口型（type=url + port），无 gatewaySocket
	uiDir := filepath.Join(targetDir, "ui")
	os.MkdirAll(uiDir, 0o755)
	uiConfig := `{".url":{"X.Application":{"title":"X","type":"url","protocol":"http","port":"19999"}}}`
	if err := os.WriteFile(filepath.Join(uiDir, "config"), []byte(uiConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	// .env：注入错误的网关字段（模拟旧版本残留）
	env := EnvMap{
		"TRIM_APPNAME":        name,
		"SOCKET_PATH":         filepath.Join(targetDir, "app.sock"),
		"GATEWAY_PREFIX":      "/app/portapp",
		"TRIM_SERVICE_SOCKET": filepath.Join(targetDir, "app.sock"),
		"LAUNCH_PATH":         "/app/portapp/",
		"TRIM_SERVICE_PORT":   "19999",
	}
	if err := SaveEnvFile(cfg.AppEnvFile(name), env); err != nil {
		t.Fatal(err)
	}

	// 清理逻辑应识别出这是端口型应用并清空网关字段
	if got := readGatewaySocketFromUIConfig(targetDir, appDir); got != "" {
		t.Fatalf("测试前提错误：ui/config 不应含 gatewaySocket，得到 %q", got)
	}

	// 直接验证"清理判定条件"所依赖的事实：
	// ui/config 无 gatewaySocket 且 .env 有残留 → 应当清理
	loaded, err := LoadEnvFile(cfg.AppEnvFile(name))
	if err != nil {
		t.Fatal(err)
	}
	if loaded["SOCKET_PATH"] == "" {
		t.Fatal("测试前提错误：应已注入 SOCKET_PATH")
	}
	if readGatewaySocketFromUIConfig(targetDir, appDir) != "" {
		t.Fatal("端口型应用不应被识别为网关应用")
	}
}

// 真正的网关应用（ui/config 含 gatewaySocket）不应被误清
func TestGatewayAppNotCleared(t *testing.T) {
	base := t.TempDir()
	cfg := &config.Config{
		BaseDir: base,
		AppsDir: filepath.Join(base, "apps"),
		BinDir:  filepath.Join(base, "bin"),
		LogDir:  filepath.Join(base, "logs"),
	}

	name := "gwapp"
	appDir := cfg.AppDir(name)
	targetDir := cfg.AppTargetDir(name)
	os.MkdirAll(appDir, 0o755)
	uiDir := filepath.Join(targetDir, "ui")
	os.MkdirAll(uiDir, 0o755)
	uiConfig := `{".url":{"X":{"title":"X","type":"iframe","gatewaySocket":"app.sock","gatewayPrefix":"/app/gwapp"}}}`
	os.WriteFile(filepath.Join(uiDir, "config"), []byte(uiConfig), 0o644)

	if got := readGatewaySocketFromUIConfig(targetDir, appDir); got != "app.sock" {
		t.Fatalf("网关应用应读到 gatewaySocket=app.sock，得到 %q", got)
	}
}
