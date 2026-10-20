package web

import (
	"testing"

	"fpkm/internal/app"
	"fpkm/internal/config"
)

func accessTestCfg(t *testing.T) *config.Config {
	t.Helper()
	base := t.TempDir()
	return &config.Config{BaseDir: base}
}

// 端口覆盖：设为 port + 16601 后，访问信息应为 port@16601
func TestAccessOverridePort(t *testing.T) {
	cfg := accessTestCfg(t)
	env := app.EnvMap{"GATEWAY_PREFIX": "/app/Lucky", "SOCKET_PATH": "/x/app.sock"}

	if err := app.SetAccessOverride(cfg, "Lucky", app.AccessOverride{
		Mode: app.AccessPort, Port: 16601, Path: "/",
	}); err != nil {
		t.Fatal(err)
	}

	got := describeAccess(cfg, "Lucky", "", env)
	if got.Kind != "port" {
		t.Errorf("期望 kind=port，得到 %q", got.Kind)
	}
	if got.AccessPort != 16601 {
		t.Errorf("期望端口 16601，得到 %d", got.AccessPort)
	}
	if got.AccessPath != "/" {
		t.Errorf("期望路径 /，得到 %q", got.AccessPath)
	}
	if !got.Overridden {
		t.Error("应标记 overridden=true")
	}
}

// 未设置覆盖时保持 ui/config 推导结果（网关）
func TestAccessNoOverrideKeepsGateway(t *testing.T) {
	cfg := accessTestCfg(t)
	env := app.EnvMap{
		"GATEWAY_PREFIX": "/app/fnchat",
		"SOCKET_PATH":    "/x/app.sock",
		"LAUNCH_PATH":    "/app/fnchat/",
	}
	got := describeAccess(cfg, "fnchat", "", env)
	if got.Kind != "gateway" {
		t.Errorf("期望 gateway，得到 %q", got.Kind)
	}
	if got.Overridden {
		t.Error("未覆盖时 overridden 应为 false")
	}
}

// 清除覆盖（auto）后应回到 ui/config 推导
func TestAccessOverrideCleared(t *testing.T) {
	cfg := accessTestCfg(t)
	env := app.EnvMap{"GATEWAY_PREFIX": "/app/Lucky", "SOCKET_PATH": "/x/app.sock"}

	_ = app.SetAccessOverride(cfg, "Lucky", app.AccessOverride{Mode: app.AccessPort, Port: 16601})
	if got := describeAccess(cfg, "Lucky", "", env); got.Kind != "port" {
		t.Fatalf("前置失败：应为 port，得到 %q", got.Kind)
	}

	// 清除
	if err := app.SetAccessOverride(cfg, "Lucky", app.AccessOverride{Mode: app.AccessAuto}); err != nil {
		t.Fatal(err)
	}
	got := describeAccess(cfg, "Lucky", "", env)
	if got.Kind != "gateway" {
		t.Errorf("清除后应回到 gateway，得到 %q", got.Kind)
	}
	if got.Overridden {
		t.Error("清除后 overridden 应为 false")
	}
}

// 端口型应用在未填端口时，沿用环境变量里的端口
func TestAccessOverridePortFallbackToEnv(t *testing.T) {
	cfg := accessTestCfg(t)
	env := app.EnvMap{"TRIM_SERVICE_PORT": "18888"}

	_ = app.SetAccessOverride(cfg, "X", app.AccessOverride{Mode: app.AccessPort, Path: "/"})
	got := describeAccess(cfg, "X", "", env)
	if got.AccessPort != 18888 {
		t.Errorf("应沿用 env 端口 18888，得到 %d", got.AccessPort)
	}
}

// 自定义子路径
func TestAccessOverrideCustomPath(t *testing.T) {
	cfg := accessTestCfg(t)
	_ = app.SetAccessOverride(cfg, "X", app.AccessOverride{
		Mode: app.AccessPort, Port: 8080, Path: "/admin",
	})
	got := describeAccess(cfg, "X", "", app.EnvMap{})
	if got.AccessPath != "/admin" {
		t.Errorf("期望 /admin，得到 %q", got.AccessPath)
	}
}

// 强制网关：即使原本是端口型应用也能改成走网关
func TestAccessOverrideForceGateway(t *testing.T) {
	cfg := accessTestCfg(t)
	_ = app.SetAccessOverride(cfg, "Y", app.AccessOverride{Mode: app.AccessGateway})
	got := describeAccess(cfg, "Y", "8080", app.EnvMap{})
	if got.Kind != "gateway" {
		t.Errorf("期望 gateway，得到 %q", got.Kind)
	}
	if got.AccessPort == 8080 {
		t.Error("走网关时端口应为网关端口，而非应用端口")
	}
}
