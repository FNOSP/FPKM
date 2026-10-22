// Package netadapt 提供网络环境适配：DNS 覆盖、host 映射与启动自检。
//
// Android 端由壳应用注入环境变量（未注入时完全保持上游 FPKM 的默认行为）；
// Windows / Linux 端没有壳应用，第 7 轮起在未显式注入 FPKM_DNS 时**直接注入
// 公共 DNS**（见 dnsinject.go）。
//
// 背景：Android 沙箱内 Go 交叉编译的进程读不到系统 DNS 配置（/etc/resolv.conf
// 不存在），纯 Go 解析器会退化为查询 127.0.0.1:53 / [::1]:53 并报
// connection refused。壳应用因此：
//
//	FPKM_DNS          逗号分隔的 DNS 服务器列表（可写 host:port，默认 53）
//	FPKM_HOSTS        host=ip 映射，多条用 ; 或换行分隔（Java 侧系统解析结果）
//	FPKM_PROBE_HOST   启动自检：解析该域名并写日志
//	FPKM_SELFTEST_URL 启动自检：请求该 URL 并写日志
//
// Windows / Linux 端另有两个仅作用于公共 DNS 注入的开关：
//
//	FPKM_PUBLIC_DNS   注入候选列表（逗号分隔），不设置时用内置公共 DNS 列表
//	FPKM_DNS_AUTO     置 0 表示关闭自动注入，退回系统解析器
package netadapt

import (
	"context"
	"errors"
	"fmt"
	"fpkm/internal/logger"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	mu            sync.Mutex
	dnsServers    []string
	dnsChosen     string
	hostOverrides = map[string]string{}
	dnsPort       = "53"
)

// errSink 是默认的诊断日志写入器：由 logger.AdaptWriter() 提供。
//
// 用它而不是直接包 stderr，是为了解决**时序**问题——本包在 `init` 阶段就安装
// DNS 适配并写诊断日志（见 fpkm_dns_override.go），那时 logger 还没 Init、
// 日志文件句柄还不存在。AdaptWriter 会先把这段早期输出缓存下来，等 logger
// 初始化后补写进当天日志文件，并始终镜像到 stderr。
var errSink = logger.AdaptWriter()

// logf 输出诊断日志：写「当天日志文件」并镜像到 stderr（Android 壳把服务进程
// stderr 追加到其侧日志文件）。每行以「发生时间 [fpkm-net] 内容」的形式输出，
// 便于事后按时间检索。
var logf = func(format string, args ...any) {
	fmt.Fprintf(errSink, "[fpkm-net] "+format+"\n", args...)
}

// SetLogWriter 替换日志输出目标（测试或宿主定制用）
func SetLogWriter(w io.Writer) {
	sink := logger.NewStampWriter(w)
	logf = func(format string, args ...any) {
		fmt.Fprintf(sink, "[fpkm-net] "+format+"\n", args...)
	}
}

// InstallFromEnv 读取环境变量并安装适配层
func InstallFromEnv() {
	InstallHosts(os.Getenv("FPKM_HOSTS"))
	InstallDNS(os.Getenv("FPKM_DNS"))
	InstallDefaultTransport()
	go SelfTest(strings.TrimSpace(os.Getenv("FPKM_PROBE_HOST")), strings.TrimSpace(os.Getenv("FPKM_SELFTEST_URL")))
}

// ParseHosts 解析 "host=ip;host2=ip2"（同时接受逗号/换行分隔），非法项被忽略
func ParseHosts(raw string) map[string]string {
	out := map[string]string{}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out
	}
	raw = strings.NewReplacer("\n", ";", ",", ";", " ", ";", "\t", ";").Replace(raw)
	for _, item := range strings.Split(raw, ";") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		parts := strings.SplitN(item, "=", 2)
		if len(parts) != 2 {
			continue
		}
		host := strings.ToLower(strings.TrimSpace(parts[0]))
		ip := strings.TrimSpace(parts[1])
		if host == "" || net.ParseIP(ip) == nil {
			continue
		}
		out[host] = ip
	}
	return out
}

// InstallHosts 装载 host 映射
func InstallHosts(raw string) {
	m := ParseHosts(raw)
	if len(m) == 0 {
		return
	}
	mu.Lock()
	for host, ip := range m {
		hostOverrides[host] = ip
	}
	total := len(hostOverrides)
	mu.Unlock()
	logf("host 映射已加载（共 %d 条）: %v", total, m)
}

// HostOverride 返回 host 对应的映射 IP，无映射返回空串
func HostOverride(host string) string {
	mu.Lock()
	defer mu.Unlock()
	return hostOverrides[strings.ToLower(strings.TrimSpace(host))]
}

// ParseServers 解析 DNS 服务器列表（逗号分隔，允许 host:port 形式）
func ParseServers(raw string) []string {
	var out []string
	for _, s := range strings.Split(raw, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// InstallDNS 按服务器列表安装解析器（实际服务器在首次解析时探测挑选）。
// 未显式注入 FPKM_DNS 时，Windows / Linux 端直接注入公共 DNS（见 dnsinject.go）。
func InstallDNS(raw string) {
	servers := ParseServers(raw)
	if len(servers) == 0 {
		installInjectedDNS()
		return
	}
	installCandidateResolver(servers)
	logf("待用 DNS 服务器: %v", servers)
}

// dnsAddr 把服务器写成 host:port（无端口时补默认 53）
func dnsAddr(server string) string {
	if _, _, err := net.SplitHostPort(server); err == nil {
		return server
	}
	return net.JoinHostPort(server, dnsPort)
}

// dialDirect 返回直连指定 DNS 服务器的拨号函数
func dialDirect(server string) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		d := net.Dialer{Timeout: 5 * time.Second}
		return d.DialContext(ctx, "udp", dnsAddr(server))
	}
}

// ServerUsable 探测 DNS 服务器：只要收到应答（含 NXDOMAIN）即视为可用
func ServerUsable(server, probeHost string) (bool, string) {
	return serverUsableWithin(server, probeHost, 3*time.Second)
}

// serverUsableWithin 与 ServerUsable 相同，但探测超时可调（供注入预探测控制耗时）
func serverUsableWithin(server, probeHost string, timeout time.Duration) (bool, string) {
	if probeHost == "" {
		probeHost = "www.baidu.com"
	}
	r := &net.Resolver{PreferGo: true, Dial: dialDirect(server)}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, err := r.LookupHost(ctx, probeHost)
	if err == nil {
		return true, "应答正常"
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsNotFound {
			return true, "应答 NXDOMAIN（服务器可达）"
		}
		return false, dnsErr.Error()
	}
	return false, err.Error()
}

// PickServer 首次调用时并行探测已注入的 DNS 服务器，选出候选顺序中第一台可用者并缓存。
// 并行探测使最坏耗时收敛到单台探测上限（3s），而不是候选数乘以该上限。
func PickServer(probeHost string) string {
	mu.Lock()
	defer mu.Unlock()
	if dnsChosen != "" {
		return dnsChosen
	}
	if len(dnsServers) == 0 {
		return ""
	}
	candidates := append([]string(nil), dnsServers...)
	chosen, detail := pickUsable(candidates, func(server string) (bool, string) {
		return ServerUsable(server, probeHost)
	})
	if chosen != "" {
		dnsChosen = chosen
		logf("选用 DNS 服务器: %s（%s；候选 %v）", chosen, detail, candidates)
		return dnsChosen
	}
	// 全部探测失败时退化为第一个，保持与不探测时一致的行为
	dnsChosen = dnsServers[0]
	logf("所有 DNS 服务器探测失败（%s），仍使用: %s", detail, dnsChosen)
	return dnsChosen
}

// InstallDefaultTransport 让默认 HTTP 传输走 host 映射（存在映射时才替换）
func InstallDefaultTransport() {
	mu.Lock()
	hasOverride := len(hostOverrides) > 0
	mu.Unlock()
	if !hasOverride {
		return
	}
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		logf("默认传输不是 *http.Transport，跳过 host 映射安装")
		return
	}
	tr := base.Clone()
	tr.DialContext = MappedDialContext
	http.DefaultTransport = tr
	logf("已安装 host 映射拨号器（http.DefaultTransport）")
}

// MappedDialContext 命中 host 映射时直接连 IP，其余情况走默认拨号
func MappedDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if host, port, err := net.SplitHostPort(addr); err == nil {
		if ip := HostOverride(host); ip != "" {
			addr = net.JoinHostPort(ip, port)
		}
	}
	d := net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	return d.DialContext(ctx, network, addr)
}

// SelfTest 启动后异步自检：先解析 FPKM_PROBE_HOST，再请求 FPKM_SELFTEST_URL，
// 结果写入日志，供真机排查使用。
func SelfTest(probeHost, url string) {
	time.Sleep(1200 * time.Millisecond)

	if probeHost != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		ips, err := net.DefaultResolver.LookupHost(ctx, probeHost)
		cancel()
		if err != nil {
			logf("自检：解析 %s 失败: %v", probeHost, err)
		} else {
			logf("自检：解析 %s 成功 -> %v", probeHost, ips)
		}
	}

	if url == "" {
		return
	}
	client := &http.Client{Timeout: 12 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		logf("自检：请求 %s 失败: %v", url, err)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
	resp.Body.Close()
	logf("自检：请求 %s 成功 HTTP %d，前 %d 字节: %s", url, resp.StatusCode, len(body), string(body))
}
