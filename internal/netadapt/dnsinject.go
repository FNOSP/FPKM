package netadapt

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// 本文件实现 Windows / Linux 两端的「公共 DNS 注入」这一平台无关的核心逻辑，
// 平台开关见 dnsinject_linux.go / dnsinject_windows.go / dnsinject_other.go。
//
// 背景：Android 端由壳应用注入 FPKM_DNS / FPKM_HOSTS；Windows 与 Linux
// （fnOS / NAS / 通用服务器 / 容器 / 虚拟机）没有壳应用。Linux 上
// /etc/resolv.conf 常只指向**本地存根**（systemd-resolved 的 127.0.0.53、
// dnsmasq 的 127.0.0.1），存根自身的上游不可达时，域名查询会以
// `read udp 127.0.0.1:x->127.0.0.53:53: i/o timeout` 失败；Windows 上系统
// 解析器同样可能指向不可达的 DNS。两种情况的共同表现都是「拉取应用源失败」。
//
// 处理策略（第 7 轮起由「系统解析器 + 条件兜底」改为「直接注入公共 DNS」）：
//  1. FPKM_DNS 显式注入时完全沿用注入值（优先级最高，与 Android 行为一致）；
//  2. 未显式注入时，Windows / Linux 两端**直接注入公共 DNS**：把解析器固定到
//     候选列表（FPKM_PUBLIC_DNS 或内置列表）中的第一台可用服务器；
//  3. 注入不阻塞启动：先安装「候选解析器」（首次解析时按候选顺序探测并缓存选中
//     结果），再在后台并行预探测，把选中结果提前固定下来并写 [fpkm-net] 日志；
//  4. FPKM_DNS_AUTO=0 可关闭注入、退回系统解析器（排查 DNS 问题的逃生开关）。
//
// 候选可用 FPKM_PUBLIC_DNS 覆盖（逗号分隔），探测域名可用 FPKM_PROBE_HOST 指定。
// 若需保留内网 DNS 解析，可把内网服务器写在 FPKM_PUBLIC_DNS 首位，或改用
// FPKM_DNS 显式注入。

// defaultPublicDNS 是注入用的公共 DNS 候选（国内可直连的公共解析优先）
var defaultPublicDNS = []string{"223.5.5.5", "119.29.29.29", "114.114.114.114", "1.1.1.1", "8.8.8.8"}

// resolvConfPath 是系统解析器配置文件位置（仅用于日志诊断，测试可覆盖）
var resolvConfPath = "/etc/resolv.conf"

// probeTimeout 是单台 DNS 服务器的探测上限。多台并行探测，总耗时约为一个上限。
var probeTimeout = 1500 * time.Millisecond

// ParseResolvConf 提取 resolv.conf 中的 nameserver 地址，忽略注释与其它指令
func ParseResolvConf(content string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}
		server := strings.TrimSpace(fields[1])
		if server == "" || seen[server] {
			continue
		}
		seen[server] = true
		out = append(out, server)
	}
	return out
}

// IsLoopbackServer 判断服务器是否为本机地址：systemd-resolved / dnsmasq 这类
// 「本地存根」以及 ::1、localhost、带端口的写法都算。
func IsLoopbackServer(server string) bool {
	host := server
	if h, _, err := net.SplitHostPort(server); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// PublicDNSServers 返回注入候选：FPKM_PUBLIC_DNS 优先，否则用内置公共 DNS 列表
func PublicDNSServers() []string {
	if raw := strings.TrimSpace(os.Getenv("FPKM_PUBLIC_DNS")); raw != "" {
		if list := ParseServers(raw); len(list) > 0 {
			return list
		}
	}
	return append([]string(nil), defaultPublicDNS...)
}

// dedupeServers 去掉重复项，保持原顺序
func dedupeServers(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// pickUsable 并行探测候选服务器，返回候选列表顺序中第一台可用者及其说明
func pickUsable(servers []string, probe func(server string) (bool, string)) (string, string) {
	type result struct {
		usable bool
		detail string
	}
	results := make([]result, len(servers))
	var wg sync.WaitGroup
	for i, srv := range servers {
		wg.Add(1)
		go func(i int, srv string) {
			defer wg.Done()
			ok, detail := probe(srv)
			results[i] = result{usable: ok, detail: detail}
		}(i, srv)
	}
	wg.Wait()

	for i, srv := range servers {
		if results[i].usable {
			return srv, results[i].detail
		}
	}
	for i := range results {
		if results[i].detail != "" {
			return "", results[i].detail
		}
	}
	return "", "无可用服务器"
}

// installResolver 把解析器固定到指定服务器（不再走后续探测）
func installResolver(server string) {
	mu.Lock()
	dnsServers = []string{server}
	dnsChosen = server
	mu.Unlock()
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialDirect(server)(ctx, network, address)
		},
	}
}

// installCandidateResolver 安装「候选解析器」：首次解析时按候选顺序探测并缓存选中结果。
// 这样注入动作本身不阻塞启动，探测耗时只在真正发生域名解析时付出。
func installCandidateResolver(servers []string) {
	mu.Lock()
	dnsServers = append([]string(nil), servers...)
	dnsChosen = ""
	mu.Unlock()
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			server := PickServer(strings.TrimSpace(os.Getenv("FPKM_PROBE_HOST")))
			if server == "" {
				return nil, fmt.Errorf("no DNS server available")
			}
			return dialDirect(server)(ctx, network, address)
		},
	}
}

// readResolvConf 读取系统解析器配置内容，读不到时返回空串
func readResolvConf() string {
	content, err := os.ReadFile(resolvConfPath)
	if err != nil {
		logf("读取 %s 失败（%v）", resolvConfPath, err)
		return ""
	}
	return string(content)
}

// noResolvConfHint 是「本平台无系统解析器配置文件」时的诊断文案
const noResolvConfHint = "Windows 系统解析器，本平台无 /etc/resolv.conf"

// formatSystemDNS 把 resolv.conf 内容格式化成一行诊断文本（纯函数，便于跨平台测试）
func formatSystemDNS(conf string) string {
	sys := ParseResolvConf(conf)
	if len(sys) == 0 {
		return fmt.Sprintf("系统 DNS 配置为空（%s 无有效 nameserver）", resolvConfPath)
	}
	loopbacks := 0
	for _, s := range sys {
		if IsLoopbackServer(s) {
			loopbacks++
		}
	}
	if loopbacks == len(sys) {
		return fmt.Sprintf("系统 DNS 仅含本地存根 %v", sys)
	}
	return fmt.Sprintf("系统 DNS %v", sys)
}

// describeSystemDNS 读取本平台可用的系统 DNS 配置并格式化，用于注入前的诊断日志。
// Windows 上没有 /etc/resolv.conf，直接说明由系统解析器负责，不去读不存在的文件。
func describeSystemDNS() string {
	if !hasResolvConf {
		return noResolvConfHint
	}
	return formatSystemDNS(readResolvConf())
}

// installInjectedDNS 是 Windows / Linux 端的公共 DNS 注入入口：
// 未显式注入 FPKM_DNS 时把解析器固定到公共 DNS，且不阻塞启动。
func installInjectedDNS() {
	if !injectDNSPlatform {
		logf("未注入 FPKM_DNS 且当前平台不注入公共 DNS，按系统 DNS 配置解析")
		return
	}
	if strings.TrimSpace(os.Getenv("FPKM_DNS")) != "" {
		return // 显式注入优先，行为与 Android 端一致
	}
	if os.Getenv("FPKM_DNS_AUTO") == "0" {
		logf("FPKM_DNS_AUTO=0，跳过公共 DNS 注入，按系统 DNS 配置解析")
		return
	}

	candidates := dedupeServers(PublicDNSServers())
	if len(candidates) == 0 {
		logf("未配置公共 DNS 候选，按系统 DNS 配置解析")
		return
	}

	installCandidateResolver(candidates)
	logf("已注入公共 DNS 候选 %v（%s；首次解析时探测选用）", candidates, describeSystemDNS())
	go warmInjectedDNS(candidates)
}

// warmInjectedDNS 后台并行预探测注入候选，把候选顺序中第一台可用服务器固定下来，
// 同时写日志，使随后的真实域名解析不必再等待探测。探测全部失败时保留候选解析器，
// 首次解析时仍会按顺序重试。
func warmInjectedDNS(candidates []string) {
	probeHost := strings.TrimSpace(os.Getenv("FPKM_PROBE_HOST"))
	chosen, detail := pickUsable(candidates, func(server string) (bool, string) {
		return serverUsableWithin(server, probeHost, probeTimeout)
	})
	if chosen == "" {
		logf("公共 DNS 预探测均未通过（%s），保留候选解析器，首次解析时按顺序重试", detail)
		return
	}
	installResolver(chosen)
	logf("公共 DNS 注入已生效：%s（%s；候选 %v）", chosen, detail, candidates)
}
