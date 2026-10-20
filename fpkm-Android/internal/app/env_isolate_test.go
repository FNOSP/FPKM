package app

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// 继承来的 TRIM_* / GATEWAY_* 必须被剔除，只保留 fpkm 自己设置的值。
//
// 真实故障：fpkm 运行在 fn-deepseek-harness 容器里，环境带有
// GATEWAY_SOCKET=/vol2/@appcenter/fn-deepseek-harness/app.sock，
// 原样泄漏给 Lucky 后被用来定位 socket，导致 socket 建到 /vol2
// 而 fpkm 在 appDir/target 上等待，报"等待 Socket 超时"。
func TestAppEnvironDropsInheritedFnosVars(t *testing.T) {
	// 模拟被污染的环境
	os.Setenv("TRIM_APPDEST", "/vol2/@appcenter/parent-app")
	os.Setenv("GATEWAY_SOCKET", "/vol2/@appcenter/parent-app/app.sock")
	os.Setenv("TRIM_PKGVAR", "/vol2/@appdata/parent-app")
	defer func() {
		os.Unsetenv("TRIM_APPDEST")
		os.Unsetenv("GATEWAY_SOCKET")
		os.Unsetenv("TRIM_PKGVAR")
	}()

	env := EnvMap{
		"TRIM_APPDEST": "/root/.local/fpk/apps/Lucky/target",
		"TRIM_PKGVAR":  "/root/.local/fpk/apps/Lucky/var",
	}
	out := appEnviron(env)

	get := func(key string) (string, int) {
		count := 0
		val := ""
		for _, kv := range out {
			if strings.HasPrefix(kv, key+"=") {
				count++
				val = strings.TrimPrefix(kv, key+"=")
			}
		}
		return val, count
	}

	// GATEWAY_SOCKET 不应出现（fpkm 未设置，且继承值被剔除）
	if _, n := get("GATEWAY_SOCKET"); n != 0 {
		t.Errorf("继承的 GATEWAY_SOCKET 应被剔除，实际出现 %d 次", n)
	}

	// TRIM_APPDEST 应只有一个，且是 fpkm 设置的值
	v, n := get("TRIM_APPDEST")
	if n != 1 {
		t.Errorf("TRIM_APPDEST 应只出现 1 次，实际 %d 次", n)
	}
	if v != "/root/.local/fpk/apps/Lucky/target" {
		t.Errorf("TRIM_APPDEST 应为 fpkm 设置的值，实际 %q", v)
	}

	// TRIM_PKGVAR 同理
	v, n = get("TRIM_PKGVAR")
	if n != 1 || v != "/root/.local/fpk/apps/Lucky/var" {
		t.Errorf("TRIM_PKGVAR 应为 fpkm 值，实际 %q (出现 %d 次)", v, n)
	}
}

// 非飞牛命名空间的变量必须保留（不能误删用户环境）
func TestAppEnvironKeepsUnrelatedVars(t *testing.T) {
	os.Setenv("FPKM_TEST_KEEP_ME", "hello")
	defer os.Unsetenv("FPKM_TEST_KEEP_ME")

	out := appEnviron(EnvMap{})
	found := false
	for _, kv := range out {
		if kv == "FPKM_TEST_KEEP_ME=hello" {
			found = true
		}
	}
	if !found {
		t.Error("非飞牛变量 FPKM_TEST_KEEP_ME 应被保留")
	}

	// PATH 等基础变量也要保留
	hasPath := false
	for _, kv := range out {
		if strings.HasPrefix(kv, "PATH=") {
			hasPath = true
		}
	}
	if !hasPath {
		t.Error("PATH 应被保留")
	}
}

// 端到端：子进程实际看到的环境里没有继承的飞牛变量
func TestAppEnvironEffectiveInSubprocess(t *testing.T) {
	os.Setenv("GATEWAY_SOCKET", "/vol2/@appcenter/parent/app.sock")
	defer os.Unsetenv("GATEWAY_SOCKET")

	cmd := exec.Command("sh", "-c", "echo \"GS=[$GATEWAY_SOCKET] AD=[$TRIM_APPDEST]\"")
	cmd.Env = appEnviron(EnvMap{"TRIM_APPDEST": "/tmp/mine/target"})
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	got := strings.TrimSpace(string(out))
	want := "GS=[] AD=[/tmp/mine/target]"
	if got != want {
		t.Errorf("子进程环境不正确\n期望: %s\n实际: %s", want, got)
	}
}
