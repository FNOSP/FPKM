// Package netadapt 提供移动端（Android）沙箱下的网络环境适配：
// DNS 覆盖、host 映射与启动自检。仅在壳应用注入对应环境变量时生效，
// 未注入时完全保持上游 FPKM 的默认行为。
//
// 背景：Android 沙箱内 Go 交叉编译的进程读不到系统 DNS 配置（/etc/resolv.conf
// 不存在），纯 Go 解析器会退化为查询 127.0.0.1:53 / [::1]:53 并报
// connection refused。壳应用因此：
//
//	FPKM_DNS          逗号分隔的 DNS 服务器列表（可写 host:port，默认 53）
//	FPKM_HOSTS        host=ip 映射，多条用 ; 或换行分隔（Java 侧系统解析结果）
//	FPKM_PROBE_HOST   启动自检：解析该域名并写日志
//	FPKM_SELFTEST_URL 启动自检：请求该 URL 并写日志
package netadapt

import (
	"context"
	"errors"
	"fmt"
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

// logf 输出诊断日志，默认写 stderr（壳应用把服务进程 stderr 落到日志文件）
var logf = func(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[fpkm-net] "+format+"\n", args...)
}

// SetLogWriter 替换日志输出目标（测试或宿主定制用）
func SetLogWriter(w io.Writer) {
	logf = func(format string, args ...any) {
		fmt.Fprintf(w, "[fpkm-net] "+format+"\n", args...)
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

// InstallDNS 按服务器列表安装解析器（实际服务器在首次解析时探测挑选）
func InstallDNS(raw string) {
	servers := ParseServers(raw)
	if len(servers) == 0 {
		logf("未注入 FPKM_DNS，保持系统默认解析行为")
		return
	}
	mu.Lock()
	dnsServers = servers
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
	if probeHost == "" {
		probeHost = "www.baidu.com"
	}
	r := &net.Resolver{PreferGo: true, Dial: dialDirect(server)}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
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

// PickServer 首次调用时逐个探测已注入的 DNS 服务器，选出可用的一个并缓存
func PickServer(probeHost string) string {
	mu.Lock()
	defer mu.Unlock()
	if dnsChosen != "" {
		return dnsChosen
	}
	if len(dnsServers) == 0 {
		return ""
	}
	for _, srv := range dnsServers {
		ok, detail := ServerUsable(srv, probeHost)
		if ok {
			dnsChosen = srv
			logf("选用 DNS 服务器: %s（%s）", srv, detail)
			return dnsChosen
		}
		logf("DNS 服务器不可用: %s（%s）", srv, detail)
	}
	// 全部探测失败时退化为第一个，保持与不探测时一致的行为
	dnsChosen = dnsServers[0]
	logf("所有 DNS 服务器探测失败，仍使用: %s", dnsChosen)
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
