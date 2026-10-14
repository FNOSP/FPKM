package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"fpkm/internal/config"
	"fpkm/internal/session"
)

// newTestGateway 构造一个带会话存储的网关（不启动监听）
func newTestGateway(t *testing.T) (*Gateway, *session.Store) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{BaseDir: dir}

	st, err := session.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	return &Gateway{
		cfg:      cfg,
		port:     DefaultPort,
		routes:   map[string]*Route{},
		gate:     true,
		sessions: st,
	}, st
}

// TestGatewayRejectsWithoutToken 未登录不得访问网关应用。
//
// 这是本次改动的核心安全目标：统一网关与 CGI 应用
// 必须像飞牛那样，登录后才能访问。
func TestGatewayRejectsWithoutToken(t *testing.T) {
	g, _ := newTestGateway(t)

	cases := []struct {
		name string
		req  *http.Request
	}{
		{"裸请求", httptest.NewRequest(http.MethodGet, "http://nas/app/demo/", nil)},
		{"伪造的 Cookie", func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "http://nas/app/demo/", nil)
			r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "forged.signature"})
			return r
		}()},
		{"伪造的 Bearer", func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "http://nas/app/demo/", nil)
			r.Header.Set("Authorization", "Bearer forged.signature")
			return r
		}()},
		{"空 Cookie", func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "http://nas/app/demo/", nil)
			r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: ""})
			return r
		}()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if g.authorized(c.req) {
				t.Errorf("%s 不应通过鉴权", c.name)
			}
		})
	}
}

// TestGatewayAcceptsValidToken 有效令牌（任何来源）都应放行
func TestGatewayAcceptsValidToken(t *testing.T) {
	g, st := newTestGateway(t)
	tok, _, err := st.Issue()
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		req  *http.Request
	}{
		{"Cookie（浏览器访问网关的主要方式）", func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "http://nas/app/demo/", nil)
			r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tok})
			return r
		}()},
		{"Bearer 头", func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "http://nas/app/demo/", nil)
			r.Header.Set("Authorization", "Bearer "+tok)
			return r
		}()},
		{"X-FPKM-Session 头", func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "http://nas/app/demo/", nil)
			r.Header.Set("X-FPKM-Session", tok)
			return r
		}()},
		{"session 查询参数", httptest.NewRequest(http.MethodGet, "http://nas/app/demo/?session="+tok, nil)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !g.authorized(c.req) {
				t.Errorf("%s 携带有效令牌应通过鉴权", c.name)
			}
		})
	}
}

// TestGatewayAdminEndpointsLocalOnly 管理接口不得对局域网开放
func TestGatewayAdminEndpointsLocalOnly(t *testing.T) {
	called := false
	h := localOnly(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	// 本机：放行
	called = false
	req := httptest.NewRequest(http.MethodPost, "http://nas/__gateway__/routes", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	rec := httptest.NewRecorder()
	h(rec, req)
	if !called || rec.Code != http.StatusOK {
		t.Errorf("本机访问应放行，code=%d called=%v", rec.Code, called)
	}

	// 局域网：拒绝，且不得触达处理器
	called = false
	req = httptest.NewRequest(http.MethodPost, "http://nas/__gateway__/routes", nil)
	req.RemoteAddr = "192.168.1.50:54321"
	rec = httptest.NewRecorder()
	h(rec, req)
	if called {
		t.Error("局域网来源不应触达管理处理器")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("局域网来源应返回 403，实际 %d", rec.Code)
	}
}

// TestLoginRequiredResponse 未登录的响应形态：
// 浏览器导航得到引导页，接口调用得到 JSON 401。
func TestLoginRequiredResponse(t *testing.T) {
	g, _ := newTestGateway(t)

	// 浏览器导航（Accept: text/html）→ HTML 引导页
	req := httptest.NewRequest(http.MethodGet, "http://nas/app/demo/", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	rec := httptest.NewRecorder()
	g.writeLoginRequired(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("应返回 401，实际 %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct == "" || ct[:9] != "text/html" {
		t.Errorf("浏览器请求应返回 HTML，实际 %q", ct)
	}
	if body := rec.Body.String(); !contains(body, "需要先登录 FPKM") {
		t.Error("引导页应包含登录提示")
	}

	// fetch / XHR → JSON 401
	req = httptest.NewRequest(http.MethodGet, "http://nas/app/demo/api", nil)
	req.Header.Set("Accept", "application/json")
	rec = httptest.NewRecorder()
	g.writeLoginRequired(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("应返回 401，实际 %d", rec.Code)
	}
	if body := rec.Body.String(); !contains(body, "未登录") {
		t.Errorf("接口请求应返回 JSON 错误，实际 %q", body)
	}

	// 非 GET（如 POST 表单）→ 不返回 HTML
	req = httptest.NewRequest(http.MethodPost, "http://nas/app/demo/", nil)
	req.Header.Set("Accept", "text/html")
	rec = httptest.NewRecorder()
	g.writeLoginRequired(rec, req)
	if body := rec.Body.String(); contains(body, "<!doctype html>") {
		t.Error("非 GET 请求不应返回 HTML 引导页")
	}
}

// TestGateDisabledDeniesByDefault 若密钥不可用（sessions 为 nil），
// 必须**拒绝**而不是放行 —— 绝不静默降级成无鉴权。
func TestGateDisabledDeniesByDefault(t *testing.T) {
	g := &Gateway{cfg: &config.Config{}, routes: map[string]*Route{}, gate: true}
	req := httptest.NewRequest(http.MethodGet, "http://nas/app/demo/", nil)
	if g.authorized(req) {
		t.Error("会话存储不可用时必须拒绝访问，不能静默放行")
	}
}

// TestSessionWorksWithoutFileSystemRace 令牌在本进程签发后可立即校验
func TestGatewayIssueThenVerify(t *testing.T) {
	g, st := newTestGateway(t)
	tok, exp, err := st.Issue()
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(exp) <= 0 {
		t.Error("令牌应立即有效")
	}
	req := httptest.NewRequest(http.MethodGet, "http://nas/app/demo/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tok})
	if !g.authorized(req) {
		t.Error("本进程签发的令牌应通过网关校验")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// TestLoginLinkPointsToWebUI 登录引导页的链接必须指向 FPKM Web 界面，
// 而不是网关自己的根路径。
//
// 这是实测发现的真实缺陷：网关与 Web 是**不同端口**的两个进程
// （如 gateway=7666、web=7667），早期硬编码 href="/"，
// 用户点「前往登录」会落到网关根路径并得到 404「未找到匹配的应用路由」。
func TestLoginLinkPointsToWebUI(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{BaseDir: dir}

	// 模拟 Web 进程写入的端口记录
	if err := os.WriteFile(cfg.WebPortFile(), []byte("7667"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := session.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	g := &Gateway{cfg: cfg, routes: map[string]*Route{}, gate: true, sessions: st}

	req := httptest.NewRequest(http.MethodGet, "http://nas:7666/app/demo/", nil)
	req.Host = "nas:7666"
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	g.writeLoginRequired(rec, req)

	body := rec.Body.String()
	if !contains(body, `href="http://nas:7667/"`) {
		t.Errorf("引导页应链接到 Web 界面 http://nas:7667/，实际内容:\n%s",
			body)
	}
	// 不应再出现指向网关根路径的裸 "/"
	if contains(body, `href="/"`) {
		t.Error("不应使用 href=\"/\"（会落到网关根路径并 404）")
	}
}

// TestLoginLinkFallsBackGracefully 未记录端口时不应产生坏链接
func TestLoginLinkFallsBackGracefully(t *testing.T) {
	dir := t.TempDir()
	g := &Gateway{cfg: &config.Config{BaseDir: dir}, routes: map[string]*Route{}}

	req := httptest.NewRequest(http.MethodGet, "http://nas:7666/app/demo/", nil)
	req.Host = "nas:7666"
	if got := g.loginURL(req); got != "/" {
		t.Errorf("未记录端口时应退回 \"/\"，实际 %q", got)
	}
}

// TestAuthenticatedContentNotCacheable 已鉴权内容必须禁止浏览器缓存。
//
// 实测发现的真实缺陷：应用返回 `Cache-Control: max-age=3600, public` 时，
// 浏览器会缓存整份响应，**登出后再访问根本不发请求**（实测状态码 200、
// 内容仍是应用页面），网关鉴权没有机会执行。
// 鉴权只有"每个请求都经过网关"才有意义，因此必须压掉缓存头。
func TestAuthenticatedContentNotCacheable(t *testing.T) {
	dir := t.TempDir()
	g := &Gateway{cfg: &config.Config{BaseDir: dir}, routes: map[string]*Route{}, gate: true}

	if !g.gate {
		t.Skip("门禁未启用")
	}

	// 直接验证网关在入口设置的头
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://nas/app/demo/", nil)
	g.setNoCacheHeaders(rec, req)

	cc := rec.Header().Get("Cache-Control")
	if !contains(cc, "no-store") {
		t.Errorf("Cache-Control 应含 no-store，实际 %q", cc)
	}
	if contains(cc, "max-age=3600") {
		t.Error("不应保留后端的 max-age")
	}
	if pragma := rec.Header().Get("Pragma"); pragma != "no-cache" {
		t.Errorf("Pragma = %q, 期望 no-cache", pragma)
	}
	if exp := rec.Header().Get("Expires"); exp != "0" {
		t.Errorf("Expires = %q, 期望 0", exp)
	}
}
