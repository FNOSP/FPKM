//go:build windows

package netadapt

// injectDNSPlatform 表示当前平台启用「公共 DNS 注入」。
//
// Windows 端的系统解析器同样可能指向不可达的 DNS（与 Linux 存根问题同源），
// 且没有壳应用注入 FPKM_DNS，因此第 7 轮起与 Linux 一样直接注入公共 DNS
// （可用 FPKM_DNS_AUTO=0 关闭、FPKM_PUBLIC_DNS 覆盖候选）。
const injectDNSPlatform = true

// hasResolvConf 表示当前平台存在 /etc/resolv.conf（Windows 无该文件，
// 注入前的诊断日志改为说明「由系统解析器负责」）
const hasResolvConf = false
