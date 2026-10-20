package app

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 端到端：应用把 socket 建在 @appcenter 下（Lucky 行为），
// waitForSocket 应发现并返回该路径，而不是傻等配置路径直到超时。
func TestWaitForSocketDiscoversAppcenterSocket(t *testing.T) {
	// 用临时目录模拟一个 /volN 卷，避免依赖真实 /vol2 权限
	fakeVol := t.TempDir()
	orig := fnosVolumesFn
	fnosVolumesFn = func() []string { return []string{fakeVol} }
	defer func() { fnosVolumesFn = orig }()

	const appname = "fpkmtestapp"
	appDir := filepath.Join(fakeVol, "@appcenter", appname)
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := testConfig(t)
	m := &Manager{cfg: cfg}

	// 应用不会在 fpkm 配置的路径建 socket
	configured := filepath.Join(cfg.AppTargetDir(appname), "app.sock")

	// 用当前进程当作"存活的应用进程"
	if err := os.MkdirAll(filepath.Dir(cfg.AppPidFile(appname)), 0o755); err != nil {
		t.Fatal(err)
	}
	pm := NewProcessManager(cfg.AppPidFile(appname), cfg.AppLogFile(appname))
	if err := pm.writePID(os.Getpid()); err != nil {
		t.Fatal(err)
	}
	defer pm.clearPID()

	// 延迟在 appcenter 目录创建真实 unix socket（模拟 Lucky）
	sockPath := filepath.Join(appDir, "app.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Skipf("无法创建 unix socket: %v", err)
	}
	defer func() {
		ln.Close()
		os.Remove(sockPath)
	}()

	if got := m.socketCandidates(appname, configured); !containsPath(got, sockPath) {
		t.Fatalf("候选应包含 %s，实际 %v", sockPath, got)
	}

	actual, err := m.waitForSocket(appname, configured, pm)
	if err != nil {
		t.Fatalf("应发现 appcenter 下的 socket，实际报错: %v", err)
	}
	if actual != sockPath {
		t.Errorf("应返回实际路径 %q，得到 %q", sockPath, actual)
	}
}

// 配置路径优先生效时，不能被 appcenter 路径抢走
func TestWaitForSocketPrefersConfiguredWhenPresent(t *testing.T) {
	cfg := testConfig(t)
	m := &Manager{cfg: cfg}
	const appname = "fpkmtestapp2"

	if err := os.MkdirAll(cfg.AppTargetDir(appname), 0o755); err != nil {
		t.Fatal(err)
	}
	configured := filepath.Join(cfg.AppTargetDir(appname), "app.sock")
	ln, err := net.Listen("unix", configured)
	if err != nil {
		t.Skipf("无法创建 unix socket: %v", err)
	}
	defer func() {
		ln.Close()
		os.Remove(configured)
	}()

	if err := os.MkdirAll(filepath.Dir(cfg.AppPidFile(appname)), 0o755); err != nil {
		t.Fatal(err)
	}
	pm := NewProcessManager(cfg.AppPidFile(appname), cfg.AppLogFile(appname))
	if err := pm.writePID(os.Getpid()); err != nil {
		t.Fatal(err)
	}
	defer pm.clearPID()

	actual, err := m.waitForSocket(appname, configured, pm)
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if actual != configured {
		t.Errorf("应优先返回配置路径 %q，得到 %q", configured, actual)
	}
}

func containsPath(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

var _ = time.Second
