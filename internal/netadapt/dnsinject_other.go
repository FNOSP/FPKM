//go:build !linux && !windows

package netadapt

// injectDNSPlatform 表示当前平台启用「公共 DNS 注入」。
//
// 其余平台不注入：Android 端由壳应用注入 FPKM_DNS / FPKM_HOSTS（见第 5 章），
// macOS 等平台保持上游默认的解析路径，不改动 DNS 行为。
const injectDNSPlatform = false

// hasResolvConf 表示当前平台存在 /etc/resolv.conf（该平台不注入，仅保持与
// dnsinject_linux.go / dnsinject_windows.go 的定义一致以便编译）
const hasResolvConf = false
