//go:build android || linux || darwin || windows

package main

import (
	"fpkm/internal/netadapt"
)

// Android 移植版的网络环境适配入口。
//
// Android 沙箱内 Go 交叉编译的进程读不到系统 DNS 配置，域名解析会退化为
// 查询 127.0.0.1:53 / [::1]:53 并报 connection refused。壳应用会注入：
//
//	FPKM_DNS         系统 DNS 服务器列表（探测后选用可用者）
//	FPKM_HOSTS       Java 侧系统解析器得到的 host=ip 映射（最强兜底，直接连 IP）
//	FPKM_PROBE_HOST  启动自检域名
//	FPKM_SELFTEST_URL 启动自检 URL
//
// 未注入上述变量时不做任何改动，保持上游默认行为。具体实现见
// internal/netadapt，配套单元测试覆盖了「域名无法解析时靠 host 映射连通」
// 与「注入 DNS 后解析生效」两条关键路径。
//
// 例外：Windows / Linux 端没有壳应用注入，未显式注入 FPKM_DNS 时会**直接注入
// 公共 DNS**（把解析器固定到候选列表中第一台可用服务器），以避开「系统解析器
// 指向不可达的本地存根 / 失效 DNS」导致拉取应用源失败的场景。可用环境变量覆盖：
//
//	FPKM_PUBLIC_DNS  注入候选列表（逗号分隔），不设置时用内置公共 DNS 列表
//	FPKM_DNS_AUTO    置 0 关闭自动注入，退回系统解析器
//
// 详见 internal/netadapt/dnsinject.go；Android 由壳应用注入、macOS 不启用该路径，
// 行为均与上游一致。
func init() {
	netadapt.InstallFromEnv()
}
