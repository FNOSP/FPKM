//go:build linux

package netadapt

// injectDNSPlatform 表示当前平台启用「公共 DNS 注入」。
//
// Linux（fnOS / NAS / 通用服务器 / 容器 / 虚拟机）没有 Android 那样的壳应用注入
// FPKM_DNS，且 /etc/resolv.conf 常只指向本地存根，因此直接注入公共 DNS
// （可用 FPKM_DNS_AUTO=0 关闭、FPKM_PUBLIC_DNS 覆盖候选）。
const injectDNSPlatform = true

// hasResolvConf 表示当前平台存在 /etc/resolv.conf（仅用于注入前的诊断日志）
const hasResolvConf = true
