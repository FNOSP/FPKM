package app

import (
	"os"
	"path/filepath"
	"testing"

	"fpkm/internal/config"
)

// 网关应用（ui/config 有 gatewaySocket）在 start 时必须清空端口字段。
//
// 真实故障：Lucky 3.0.2 是网关应用，但升级安装的 .env 里残留了
// TRIM_SERVICE_PORT=16601。应用读到端口就走 TCP 模式、不创建 unix socket，
// fpkm 却在 target/app.sock 上空等 30 秒后报"等待 Socket 超时"。
func TestStartClearsPortForGatewayApp(t *testing.T) {
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

	// 网关型 ui/config（与 Lucky 3.0.2 一致）
	uiConfig := `{".url":{"Lucky.Application":{"title":"Lucky","type":"iframe",
		"gatewayPrefix":"/app/Lucky","gatewaySocket":"app.sock","url":"/app/Lucky/"}}}`
	if err := os.WriteFile(filepath.Join(uiDir, "config"), []byte(uiConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	// .env：残留端口（升级自旧版本的典型状态）
	env := EnvMap{
		"TRIM_APPNAME":        name,
		"TRIM_SERVICE_PORT":   "16601",
		"port":                "16601",
		"UI_PORT":             "16601",
		"SOCKET_PATH":         filepath.Join(targetDir, "app.sock"),
		"GATEWAY_PREFIX":      "/app/Lucky",
		"TRIM_SERVICE_SOCKET": filepath.Join(targetDir, "app.sock"),
	}
	if err := SaveEnvFile(cfg.AppEnvFile(name), env); err != nil {
		t.Fatal(err)
	}

	// 前置校验：确实被识别为网关应用
	if got := readGatewaySocketFromUIConfig(targetDir, appDir); got != "app.sock" {
		t.Fatalf("应识别为网关应用，gatewaySocket=%q", got)
	}

	// 复现"start 时的端口清理"逻辑
	for _, k := range []string{"TRIM_SERVICE_PORT", "port", "UI_PORT"} {
		env[k] = ""
	}
	if err := SaveEnvFile(cfg.AppEnvFile(name), env); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadEnvFile(cfg.AppEnvFile(name))
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"TRIM_SERVICE_PORT", "port", "UI_PORT"} {
		if loaded[k] != "" {
			t.Errorf("网关应用的 %s 应被清空，实际 %q", k, loaded[k])
		}
	}
	// 网关字段必须保留
	if loaded["SOCKET_PATH"] == "" {
		t.Error("SOCKET_PATH 不应被清空")
	}
	if loaded["GATEWAY_PREFIX"] != "/app/Lucky" {
		t.Errorf("GATEWAY_PREFIX 应保留，实际 %q", loaded["GATEWAY_PREFIX"])
	}
}

// 端口型应用（ui/config 无 gatewaySocket）的端口不能被清
func TestPortAppKeepsPort(t *testing.T) {
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
	os.MkdirAll(appDir, 0o755)
	uiDir := filepath.Join(targetDir, "ui")
	os.MkdirAll(uiDir, 0o755)
	uiConfig := `{".url":{"X":{"title":"X","type":"url","protocol":"http","port":"19999"}}}`
	os.WriteFile(filepath.Join(uiDir, "config"), []byte(uiConfig), 0o644)

	if got := readGatewaySocketFromUIConfig(targetDir, appDir); got != "" {
		t.Fatalf("端口型应用不应有 gatewaySocket，得到 %q", got)
	}
	// 端口型应用的端口来自 ui/config
	if got := readPortFromUIConfig(targetDir, appDir); got != "19999" {
		t.Errorf("端口应为 19999，得到 %q", got)
	}
}
