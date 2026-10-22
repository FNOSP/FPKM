package netadapt

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// fakeDNS 是一个极简 DNS 应答器：对任何 A 查询都回答 127.0.0.1。
// 用于在无外网、无系统 DNS 的条件下验证「注入 DNS → Go 解析」这条链路。
type fakeDNS struct {
	conn *net.UDPConn
	addr string
}

func startFakeDNS(t *testing.T) *fakeDNS {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("启动本地 DNS 失败: %v", err)
	}
	f := &fakeDNS{conn: conn, addr: conn.LocalAddr().String()}
	go func() {
		buf := make([]byte, 1500)
		for {
			n, peer, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if resp := buildDNSResponse(buf[:n]); resp != nil {
				_, _ = conn.WriteToUDP(resp, peer)
			}
		}
	}()
	t.Cleanup(func() { conn.Close() })
	return f
}

// buildDNSResponse 解析查询并返回带 A 记录的应答
func buildDNSResponse(query []byte) []byte {
	if len(query) < 12 {
		return nil
	}
	// 跳过 header 解析出问题段（QNAME + QTYPE + QCLASS）
	offset := 12
	for offset < len(query) {
		l := int(query[offset])
		if l == 0 {
			offset++
			break
		}
		offset += l + 1
	}
	if offset+4 > len(query) {
		return nil
	}
	offset += 4
	question := query[12:offset]

	resp := make([]byte, 0, offset+16)
	resp = append(resp, query[0], query[1])   // ID
	resp = append(resp, 0x81, 0x80)           // 标准应答，无错误
	resp = append(resp, 0x00, 0x01)           // QDCOUNT
	resp = append(resp, 0x00, 0x01)           // ANCOUNT
	resp = append(resp, 0x00, 0x00)           // NSCOUNT
	resp = append(resp, 0x00, 0x00)           // ARCOUNT
	resp = append(resp, question...)          // 回显问题
	resp = append(resp, 0xC0, 0x0C)           // 指向 QNAME
	resp = append(resp, 0x00, 0x01)           // TYPE A
	resp = append(resp, 0x00, 0x01)           // CLASS IN
	resp = append(resp, 0x00, 0x00, 0x00, 60) // TTL
	resp = append(resp, 0x00, 0x04)           // RDLENGTH
	resp = append(resp, 127, 0, 0, 1)         // RDATA
	return resp
}

func TestParseHosts(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int
	}{
		{"空值", "   ", 0},
		{"单条", "nas.hhxs2025.top=8.148.9.41", 1},
		{"多条分号", "a.com=1.1.1.1;b.com=2.2.2.2", 2},
		{"换行与空格", "a.com=1.1.1.1\n b.com=2.2.2.2 ", 2},
		{"非法 IP 被忽略", "a.com=not-an-ip;b.com=2.2.2.2", 1},
		{"缺少等号被忽略", "a.com;b.com=2.2.2.2", 1},
	}
	for _, c := range cases {
		if got := ParseHosts(c.raw); len(got) != c.want {
			t.Errorf("%s: ParseHosts(%q) 得到 %d 条，期望 %d 条 (%v)", c.name, c.raw, len(got), c.want, got)
		}
	}
	if got := ParseHosts("NAS.HHXS2025.top=8.148.9.41"); got["nas.hhxs2025.top"] != "8.148.9.41" {
		t.Errorf("域名应统一小写，实际: %v", got)
	}
}

// TestMappedDialWithoutDNS 验证最关键的场景：域名无法解析时，
// 只要注入 host 映射，HTTP 请求依然能连通（Android 沙箱的等价模拟）。
func TestMappedDialWithoutDNS(t *testing.T) {
	InstallHosts("")
	mu.Lock()
	hostOverrides = map[string]string{}
	mu.Unlock()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "fpkm-ok")
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	const host = "fpkm-test.invalid"

	// 未注入映射时：该域名不可解析，请求应当失败
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_, err := MappedDialContext(ctx, "tcp", host+":"+port)
	cancel()
	if err == nil {
		t.Fatalf("未注入映射时 %s 竟然连接成功，用例前提不成立", host)
	}
	t.Logf("未注入映射时按预期失败: %v", err)

	// 注入映射后：直接连到本地测试服务器
	InstallHosts(host + "=127.0.0.1")
	defer func() {
		mu.Lock()
		hostOverrides = map[string]string{}
		mu.Unlock()
	}()

	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{DialContext: MappedDialContext},
	}
	resp, err := client.Get("http://" + host + ":" + port + "/ping")
	if err != nil {
		t.Fatalf("注入映射后请求失败: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "fpkm-ok" {
		t.Fatalf("响应内容异常: %q", string(body))
	}
}

// TestInstallDefaultTransportMapped 验证 http.DefaultTransport 被替换后，
// 未显式指定 Transport 的 http.Get 也能走 host 映射
func TestInstallDefaultTransportMapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "default-transport-ok")
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	const host = "fpkm-default.invalid"

	InstallHosts(host + "=127.0.0.1")
	InstallDefaultTransport()
	defer func() {
		mu.Lock()
		hostOverrides = map[string]string{}
		mu.Unlock()
		http.DefaultTransport = http.DefaultTransport.(*http.Transport).Clone()
	}()

	resp, err := http.Get("http://" + host + ":" + port + "/")
	if err != nil {
		t.Fatalf("默认传输走映射后请求失败: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "default-transport-ok" {
		t.Fatalf("响应内容异常: %q", string(body))
	}
}

// TestInstalledDNSResolver 验证注入 DNS 后 net.LookupHost 能真正解析，
// 且不可用服务器会被跳过、可用服务器被选中。
func TestInstalledDNSResolver(t *testing.T) {
	fake := startFakeDNS(t)

	InstallDNS("192.0.2.1," + fake.addr) // 192.0.2.1 是保留的不可达地址
	if net.DefaultResolver == nil {
		t.Fatal("解析器未安装")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupHost(ctx, "anything.test")
	if err != nil {
		t.Fatalf("注入 DNS 后解析失败: %v", err)
	}
	if len(ips) == 0 || ips[0] != "127.0.0.1" {
		t.Fatalf("解析结果异常: %v", ips)
	}

	mu.Lock()
	chosen := dnsChosen
	mu.Unlock()
	if chosen != fake.addr {
		t.Fatalf("应跳过不可达服务器并选中 %s，实际选中 %q", fake.addr, chosen)
	}

	mu.Lock()
	dnsServers = nil
	dnsChosen = ""
	mu.Unlock()
	net.DefaultResolver = &net.Resolver{}
}

// TestEnvEntry 验证 InstallFromEnv 在未注入任何变量时不改变默认行为
func TestEnvEntry(t *testing.T) {
	for _, k := range []string{"FPKM_DNS", "FPKM_HOSTS", "FPKM_PROBE_HOST", "FPKM_SELFTEST_URL"} {
		os.Unsetenv(k)
	}
	// 本用例只验证「显式注入才生效」：关掉 Linux 端的自动 DNS 兜底以隔离其影响
	os.Setenv("FPKM_DNS_AUTO", "0")
	defer os.Unsetenv("FPKM_DNS_AUTO")
	before := net.DefaultResolver
	InstallFromEnv()
	if net.DefaultResolver != before {
		t.Fatal("未注入 FPKM_DNS 时不应替换解析器")
	}
	if len(hostOverrides) != 0 {
		t.Fatal("未注入 FPKM_HOSTS 时不应存在 host 映射")
	}
}
