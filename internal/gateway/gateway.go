package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"fpkm/internal/config"
	"fpkm/internal/logger"
	"fpkm/internal/session"
)

// DefaultPort 默认网关端口
const DefaultPort = 7666

// Route 路由条目
type Route struct {
	AppName   string `json:"app_name"`
	Prefix    string `json:"prefix"`    // 如 /app/fntermx
	Socket    string `json:"socket"`    // Unix Socket 路径
	Available bool   `json:"available"` // socket 是否存在

	// 协议类型: "http"（默认, HTTP 反向代理）或 "fastcgi"（FastCGI 协议）
	// 飞牛中大部分应用是 HTTP（proxy_pass），部分老应用是 FastCGI（fastcgi_pass）
	Protocol string `json:"protocol"`

	// FastCGI 专用：SCRIPT_FILENAME 环境变量值
	// 对应 nginx 的 fastcgi_param SCRIPT_FILENAME
	ScriptFilename string `json:"script_filename,omitempty"`

	// 以下字段不参与 JSON 序列化
	proxy     *httputil.ReverseProxy `json:"-"`
	transport *http.Transport        `json:"-"`
}

// Gateway 统一网关
type Gateway struct {
	cfg    *config.Config
	port   int
	mu     sync.RWMutex
	routes map[string]*Route // prefix -> Route
	server *http.Server

	// sessions 与 Web 进程共享的会话存储（同一份 HMAC 密钥文件）。
	// 网关据此**独立**校验 FPKM 登录态，无需回调 Web 进程。
	sessions *session.Store
	// gate 是否启用登录校验（关掉时行为与升级前一致）
	gate bool
}

// New 创建网关实例。
//
// 会同时加载与 Web 进程共享的会话密钥，用于校验 FPKM 登录态；
// 这样统一网关与 CGI 应用只有登录 FPKM 后才能访问。
func New(cfg *config.Config, port int) *Gateway {
	if port <= 0 {
		port = DefaultPort
	}
	g := &Gateway{
		cfg:    cfg,
		port:   port,
		routes: make(map[string]*Route),
		gate:   true,
	}
	// 密钥读取失败不阻断网关启动（可能是权限问题），
	// 但此时 gate 会自动失效并在日志中明确告警 —— 绝不静默放行。
	if st, err := session.New(cfg.BaseDir); err == nil {
		g.sessions = st
	} else {
		logger.Error("网关加载会话密钥失败，将无法校验登录态: %v", err)
	}
	return g
}

// AddRoute 添加路由
// protocol: "http" 或 "fastcgi"，默认为 "http"
func (g *Gateway) AddRoute(appName, prefix, socket string) {
	g.AddRouteWithProtocol(appName, prefix, socket, "", "")
}

// AddRouteFastCGI 添加 FastCGI 路由
func (g *Gateway) AddRouteFastCGI(appName, prefix, socket, scriptFilename string) {
	g.AddRouteWithProtocol(appName, prefix, socket, "fastcgi", scriptFilename)
}

// AddRouteCGI 添加 CGI 路由（直接执行 CGI 二进制）
// cgiBinary 是 CGI 二进制的完整路径
func (g *Gateway) AddRouteCGI(appName, prefix, cgiBinary string) {
	g.AddRouteWithProtocol(appName, prefix, cgiBinary, "cgi", "")
}

// AddRouteWithProtocol 添加指定协议的路由
func (g *Gateway) AddRouteWithProtocol(appName, prefix, socket, protocol, scriptFilename string) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if protocol == "" {
		protocol = "http"
	}

	// 如果已存在且 socket/protocol 未变，直接返回
	if r, ok := g.routes[prefix]; ok && r.Socket == socket && r.Protocol == protocol {
		r.Available = fileExists(socket)
		return
	}

	g.routes[prefix] = &Route{
		AppName:        appName,
		Prefix:         prefix,
		Socket:         socket,
		Available:      fileExists(socket),
		Protocol:       protocol,
		ScriptFilename: scriptFilename,
	}
	logger.Info("网关路由已添加: %s -> %s (socket: %s, protocol: %s)", prefix, appName, socket, protocol)
}

// RemoveRoute 移除路由
func (g *Gateway) RemoveRoute(prefix string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if r, ok := g.routes[prefix]; ok {
		// 关闭缓存的 transport
		if r.transport != nil {
			r.transport.CloseIdleConnections()
		}
	}
	delete(g.routes, prefix)
	logger.Info("网关路由已移除: %s", prefix)
}

// GetRoutes 获取所有路由
func (g *Gateway) GetRoutes() []*Route {
	g.mu.RLock()
	defer g.mu.RUnlock()
	routes := make([]*Route, 0, len(g.routes))
	for _, r := range g.routes {
		r.Available = fileExists(r.Socket)
		routes = append(routes, r)
	}
	return routes
}

// Start 启动网关 HTTP 服务（阻塞，直到服务停止）
func (g *Gateway) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", g.handleRequest)
	// 管理接口仅允许本机访问（见 localOnly）：
	// /__gateway__/routes 支持 POST 添加路由，若对外开放，
	// 任何人都能把任意路径指向任意 socket，属于提权风险。
	mux.HandleFunc("/__gateway__/routes", localOnly(g.handleRoutesAPI))
	mux.HandleFunc("/__gateway__/status", localOnly(g.handleStatus))

	g.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", g.port),
		Handler: mux,
	}

	ln, err := net.Listen("tcp", g.server.Addr)
	if err != nil {
		return fmt.Errorf("网关监听端口 %d 失败: %w", g.port, err)
	}

	// 写入 PID 文件
	pidFile := g.PidFile()
	os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)

	logger.Success("统一网关已启动 (端口: %d, PID: %d)", g.port, os.Getpid())

	// 阻塞运行服务
	err = g.server.Serve(ln)
	if err != nil && err != http.ErrServerClosed {
		logger.Error("网关服务异常退出: %v", err)
		return err
	}
	return nil
}

// Stop 停止网关
func (g *Gateway) Stop() error {
	if g.server == nil {
		return nil
	}
	// 关闭所有 route 的连接池
	g.mu.Lock()
	for _, r := range g.routes {
		if r.transport != nil {
			r.transport.CloseIdleConnections()
		}
	}
	g.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := g.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("网关关闭失败: %w", err)
	}
	os.Remove(g.PidFile())
	logger.Info("统一网关已停止")
	return nil
}

// handleRequest 主请求处理器：根据路径前缀转发到 Unix Socket
func (g *Gateway) handleRequest(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	logger.Info("[GATEWAY] --> %s %s (from %s)", r.Method, r.URL.Path, r.RemoteAddr)

	g.mu.RLock()
	// 找到匹配的路由（最长前缀匹配）
	var matched *Route
	path := r.URL.Path
	for prefix, route := range g.routes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			if matched == nil || len(prefix) > len(matched.Prefix) {
				matched = route
			}
		}
	}
	g.mu.RUnlock()

	if matched == nil {
		logger.Info("[GATEWAY] <-- %s 404 (无匹配路由, %.1fms)", r.URL.Path, float64(time.Since(start).Microseconds())/1000)
		http.Error(w, "未找到匹配的应用路由: "+path, http.StatusNotFound)
		return
	}

	logger.Info("[GATEWAY] 匹配路由: %s -> %s (socket: %s, protocol: %s)", matched.Prefix, matched.AppName, matched.Socket, matched.Protocol)

	// 登录校验（与飞牛一致：网关 / CGI 应用必须登录后才能访问）。
	//
	// 必须在任何代理/执行动作**之前**拦截，否则未登录用户依然
	// 能透过网关读取应用数据，甚至触发 CGI 产生副作用。
	if !g.authorized(r) {
		logger.Warn("[GATEWAY] <-- %s 401 未登录 (app=%s, from %s)",
			r.URL.Path, matched.AppName, r.RemoteAddr)
		g.writeLoginRequired(w, r)
		return
	}

	// CGI 路由：直接执行 CGI 二进制（不需要 socket 检查）
	if matched.Protocol == "cgi" {
		logger.Info("[GATEWAY] CGI 执行: %s (binary: %s)", r.URL.Path, matched.Socket)
		g.proxyCGI(w, r, matched)
		logger.Info("[GATEWAY] <-- CGI 完成 (%.1fms)", float64(time.Since(start).Microseconds())/1000)
		return
	}

	// 检查 socket 是否存在
	if !fileExists(matched.Socket) {
		logger.Warn("[GATEWAY] <-- %s 502 (socket 不存在: %s)", r.URL.Path, matched.Socket)

		// 给出可操作的排查提示，而不是一句笼统的"应用可能未启动"
		hint := fmt.Sprintf("应用 %s 的 Socket 不存在（应用可能未启动）", matched.AppName)
		if pid, running := g.appProcessState(matched.AppName); running {
			hint += fmt.Sprintf("；进程正在运行 (PID: %d) 但尚未监听 Socket，"+
				"通常说明应用启动中或启动失败，请查看日志: %s",
				pid, g.appLogPath(matched.AppName))
		} else {
			hint += fmt.Sprintf("；未检测到运行中的进程，请在应用商店中启动该应用。日志: %s",
				g.appLogPath(matched.AppName))
		}
		http.Error(w, hint, http.StatusBadGateway)
		return
	}

	// WebSocket 升级支持（单独处理）
	if isWebSocketRequest(r) {
		logger.Info("[GATEWAY] WebSocket 升级: %s", r.URL.Path)
		g.proxyWebSocket(w, r, matched)
		logger.Info("[GATEWAY] <-- WebSocket 完成 (%.1fms)", float64(time.Since(start).Microseconds())/1000)
		return
	}

	// FastCGI 路由
	if matched.Protocol == "fastcgi" {
		logger.Info("[GATEWAY] FastCGI 转发: %s", r.URL.Path)
		g.proxyFastCGI(w, r, matched)
		logger.Info("[GATEWAY] <-- FastCGI 完成 (%.1fms)", float64(time.Since(start).Microseconds())/1000)
		return
	}

	// HTTP 请求使用 ReverseProxy 转发
	// ReverseProxy 正确处理了 HTTP 协议（Content-Length、chunked、keep-alive），
	// 连接管理（连接池），错误处理和响应流式传输
	logger.Info("[GATEWAY] HTTP 转发: %s -> %s (strip prefix: %s)", r.URL.Path, matched.Socket, matched.Prefix)
	g.proxyHTTP(w, r, matched)
	logger.Info("[GATEWAY] <-- HTTP 完成 (%.1fms)", float64(time.Since(start).Microseconds())/1000)
}

// getProxy 获取或创建 route 的 ReverseProxy
// 模拟飞牛 nginx 的 trim_http_cgi 配置：
//   - proxy_http_version 1.1
//   - Connection "upgrade"（hop-by-hop，实际效果等同 close）
//   - proxy_read_timeout 300s
//
// 核心问题与解决方案：
// 许多飞牛应用后端（index.cgi、easytier-web-embed、gateway-proxy 等）
// 返回响应时没有 Content-Length 也不是 chunked 编码。
// 在 keep-alive 模式下，ReverseProxy 无法判断响应何时结束，会一直等待，
// 导致浏览器端表现为"加载中"、"挂起"、页面长时间白屏。
//
// 解决方案：使用 bufferingTransport 包装底层 Transport
//  1. 如果响应有 Content-Length 或是 chunked → 直接流式转发（最快）
//  2. 如果都没有 → 缓冲整个响应体，计算 Content-Length 后再发送
//     （对于飞牛应用的 UI 文件，通常几 MB 以内，完全可接受）
//
// 这比依赖 Connection: close 更可靠，因为中间代理（如 gateway-proxy）
// 会吃掉 hop-by-hop 头，导致后端收不到 Connection: close。
func (g *Gateway) getProxy(route *Route) *httputil.ReverseProxy {
	if route.proxy != nil {
		return route.proxy
	}

	// 底层 Transport：所有连接都走 Unix Socket
	baseTransport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			d := net.Dialer{}
			return d.DialContext(ctx, "unix", route.Socket)
		},
		// 禁用连接复用：模拟 nginx 的 Connection "upgrade" 行为
		// 每个请求使用独立连接，请求完成后关闭连接
		DisableKeepAlives: true,
		MaxIdleConns:      0,
		IdleConnTimeout:   30 * time.Second,
		// 不限制后端响应头时间（长轮询/SSE 场景需要）
		ResponseHeaderTimeout: 0,
	}

	// 用 bufferingTransport 包装，处理无 Content-Length 的响应
	transport := &bufferingTransport{
		base:       baseTransport,
		bufferMax:  64 * 1024 * 1024, // 最大缓冲 64MB（足够所有飞牛 UI 应用）
		socketPath: route.Socket,
	}

	prefix := route.Prefix // 闭包捕获，避免 route 变化影响

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			// 剥离网关前缀
			// /app/frpc?action=status → /?action=status
			// /app/frpc/statics/x.js → /statics/x.js
			strippedPath := strings.TrimPrefix(req.URL.Path, prefix)
			if strippedPath == "" {
				strippedPath = "/"
			}
			req.URL.Path = strippedPath
			req.URL.RawPath = ""

			// 设置 URL scheme 和 host（ReverseProxy 必需）
			// scheme 必须是 http/https，host 任意（实际连接由 Transport.DialContext 决定）
			req.URL.Scheme = "http"
			req.URL.Host = "unix" // 占位符，实际通过 Unix Socket 连接

			// 设置转发头
			origHost := req.Host
			req.Header.Set("X-Forwarded-For", req.RemoteAddr)
			req.Header.Set("X-Forwarded-Proto", "http")
			req.Header.Set("X-Gateway-Prefix", prefix)
			req.Header.Set("X-Original-Host", origHost)

			// 模拟飞牛 trim_http_cgi 网关的管理员认证头
			// 飞牛原版中 trim_http_cgi 服务验证用户身份后设置此头，
			// 部分应用（如 hermes）通过此头判断是否为管理员访问
			// fpkm 为单用户模型，始终设置为管理员
			req.Header.Set("X-Trim-Isadmin", "1")

			// 设置 Connection: close（尽量让后端关闭连接）
			// 注意：中间代理可能吃掉这个 hop-by-hop 头，所以 bufferingTransport
			// 才是最终的兜底方案
			req.Header.Set("Connection", "close")
			req.Close = true
		},
		Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			logger.Warn("网关转发失败 %s: %v", prefix, err)
			http.Error(w, fmt.Sprintf("后端连接失败: %v", err), http.StatusBadGateway)
		},
		// FlushInterval：流式转发时的 flush 间隔
		// 对于 SSE/长轮询等场景需要及时刷新，这里设为 100ms
		FlushInterval: 100 * time.Millisecond,
	}

	route.proxy = proxy
	route.transport = baseTransport
	return proxy
}

// bufferingTransport 包装 http.RoundTripper，处理无 Content-Length 的响应
//
// 问题背景：
// 许多飞牛应用后端（index.cgi、gateway-proxy 等）返回响应时，
// 既没有 Content-Length 也不是 chunked 编码。在 keep-alive 模式下，
// ReverseProxy 无法判断响应何时结束，会一直等待连接关闭，
// 导致浏览器端表现为"加载中"、"挂起"、页面长时间白屏。
//
// 解决方案（双重保障）：
//  1. 空闲超时检测：用 idleTimeoutReader 包装响应体，如果 500ms 内没有新数据，
//     就认为响应已结束（普通 HTTP 请求的响应数据是连续发送的，不会有长停顿）。
//  2. 缓冲 + 补全 Content-Length：读取完整个响应体后，设置 Content-Length，
//     这样浏览器端能准确知道响应大小，立即解析并加载后续资源。
//
// 例外：SSE / WebSocket 等长连接场景不启用缓冲，保持流式转发。
type bufferingTransport struct {
	base       http.RoundTripper
	bufferMax  int64  // 最大缓冲字节数
	socketPath string // 仅用于日志
}

// RoundTrip 实现 http.RoundTripper 接口
func (t *bufferingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	logger.Info("[BUFFER] 开始转发: %s %s", req.Method, req.URL.Path)

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		logger.Warn("[BUFFER] 后端连接失败: %v", err)
		return nil, err
	}

	logger.Info("[BUFFER] 收到响应头: status=%d, ContentLength=%d, TransferEncoding=%v",
		resp.StatusCode, resp.ContentLength, resp.TransferEncoding)
	for k, v := range resp.Header {
		logger.Info("[BUFFER]   %s: %s", k, strings.Join(v, ", "))
	}

	// 检查是否有明确的响应长度标识
	hasContentLength := resp.ContentLength >= 0
	isChunked := len(resp.TransferEncoding) > 0 && resp.TransferEncoding[0] == "chunked"

	// SSE（Server-Sent Events）流式响应：即使没有 Content-Length 也不缓冲
	contentType := resp.Header.Get("Content-Type")
	isSSE := strings.HasPrefix(contentType, "text/event-stream")

	// 有 Content-Length、是 chunked、或 SSE 流式响应 → 直接返回（流式转发，最高效）
	if hasContentLength || isChunked || isSSE {
		mode := "content-length"
		if isChunked {
			mode = "chunked"
		} else if isSSE {
			mode = "sse-stream"
		}
		logger.Info("[BUFFER] 流式转发（%s）: %s", mode, req.URL.Path)
		return resp, nil
	}

	logger.Info("[BUFFER] 无 Content-Length 且非 chunked，启用缓冲模式: %s", req.URL.Path)

	// 没有长度标识 → 使用空闲超时 reader 包装响应体
	idleReader := &idleTimeoutReader{
		reader:      resp.Body,
		idleTimeout: 2 * time.Second,
		path:        req.URL.Path,
		firstRead:   true,
	}

	// 缓冲整个响应体（有空闲超时兜底，不会永久挂起）
	body, err := io.ReadAll(idleReader)
	resp.Body.Close()
	if err != nil && err != errIdleTimeout {
		logger.Warn("[BUFFER] 读取响应体失败: %v", err)
		return nil, fmt.Errorf("缓冲响应体失败: %w", err)
	}

	logger.Info("[BUFFER] 读取完成: %d 字节 (idle_timeout=%v)", len(body), err == errIdleTimeout)

	// 检查是否超过缓冲上限
	if int64(len(body)) > t.bufferMax {
		logger.Warn("[BUFFER] 响应体超过缓冲上限 %d 字节（实际 %d）", t.bufferMax, len(body))
	}

	if err == errIdleTimeout {
		logger.Info("[BUFFER] 空闲超时触发，认为响应已结束: %d 字节", len(body))
	}

	// 设置 Content-Length
	resp.ContentLength = int64(len(body))
	resp.Body = io.NopCloser(bytes.NewReader(body))

	// 清除 Transfer-Encoding（如果有的话），因为我们有 Content-Length 了
	resp.TransferEncoding = nil

	logger.Info("[BUFFER] 缓冲完成，补全 Content-Length: %d 字节 -> %s", len(body), req.URL.Path)

	return resp, nil
}

// idleTimeoutReader 带有空闲超时的 io.Reader
// 如果在 idleTimeout 时间内没有读到任何数据，返回 errIdleTimeout 并关闭底层 reader
// 用于检测无 Content-Length 响应的结束
type idleTimeoutReader struct {
	reader      io.ReadCloser
	idleTimeout time.Duration
	path        string
	firstRead   bool
	closed      bool
}

var errIdleTimeout = fmt.Errorf("idle timeout")

func (r *idleTimeoutReader) Read(p []byte) (int, error) {
	if r.closed {
		return 0, io.EOF
	}

	// 调整超时：第一次读取等后端生成响应，后续读取检测响应结束
	timeout := r.idleTimeout
	if r.firstRead {
		timeout = 5 * time.Second
		logger.Info("[IDLE] 首次读取，超时: %v (path: %s)", timeout, r.path)
		r.firstRead = false
	}

	type readResult struct {
		n   int
		err error
	}
	ch := make(chan readResult, 1)
	go func() {
		n, err := r.reader.Read(p)
		ch <- readResult{n, err}
	}()

	select {
	case res := <-ch:
		if res.err != nil && res.err != io.EOF {
			logger.Info("[IDLE] 读取: %d 字节, err=%v (path: %s)", res.n, res.err, r.path)
		} else if res.n > 0 {
			logger.Info("[IDLE] 读取: %d 字节 (path: %s)", res.n, r.path)
		}
		return res.n, res.err
	case <-time.After(timeout):
		logger.Info("[IDLE] 空闲超时 %v，认为响应结束 (path: %s)", timeout, r.path)
		// 空闲超时：关闭底层 reader，让 goroutine 退出
		r.closed = true
		r.reader.Close()
		return 0, errIdleTimeout
	}
}

func (r *idleTimeoutReader) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	return r.reader.Close()
}

// proxyHTTP 使用 ReverseProxy 转发 HTTP 请求到 Unix Socket
// 相比手工 Hijack 转发，ReverseProxy 正确处理了：
//   - HTTP 协议（Content-Length、chunked、keep-alive）
//   - 连接管理（连接池复用、超时）
//   - 响应流式传输（按 Content-Length 或 chunked 边界读取）
//   - 错误处理与重试
//
// 修复了原实现中 io.Copy 阻塞导致连接挂起的问题
// 模拟飞牛 nginx 的 proxy_read_timeout 300s，防止后端无响应时永久挂起
func (g *Gateway) proxyHTTP(w http.ResponseWriter, r *http.Request, route *Route) {
	// 为每个请求设置 5 分钟超时（模拟 nginx 的 proxy_read_timeout 300s）
	// 超时后 context 取消，ReverseProxy 中断请求并返回 502
	// 这防止了后端不响应 Connection: close 或响应体不结束时永久挂起
	ctx, cancel := context.WithTimeout(r.Context(), 300*time.Second)
	defer cancel()
	r = r.WithContext(ctx)

	proxy := g.getProxy(route)
	proxy.ServeHTTP(w, r)
}

// proxyFastCGI 通过 FastCGI 协议转发请求
// 对应飞牛 nginx 的 fastcgi_pass 指令，模拟 trim_http_cgi 的行为
func (g *Gateway) proxyFastCGI(w http.ResponseWriter, r *http.Request, route *Route) {
	client := NewFastCGIClient("unix", route.Socket)

	// 计算 SCRIPT_FILENAME
	scriptFilename := route.ScriptFilename
	if scriptFilename == "" {
		// 默认值：剥离前缀后的路径
		strippedPath := strings.TrimPrefix(r.URL.Path, route.Prefix)
		if strippedPath == "" {
			strippedPath = "/"
		}
		scriptFilename = "/usr/trim/var/www" + strippedPath
	}

	// 为每个请求设置超时（模拟 nginx 的 fastcgi_read_timeout）
	ctx, cancel := context.WithTimeout(r.Context(), 300*time.Second)
	defer cancel()
	r = r.WithContext(ctx)

	if err := client.Do(w, r, scriptFilename); err != nil {
		logger.Warn("FastCGI 转发失败 %s: %v", route.Prefix, err)
		http.Error(w, fmt.Sprintf("FastCGI 后端连接失败: %v", err), http.StatusBadGateway)
	}
}

// proxyCGI 执行 CGI 二进制
// CGI 协议：每个请求 fork 一个进程，通过环境变量传参，stdout 返回响应
// 响应格式：HTTP 头 + 空行 + 响应体
func (g *Gateway) proxyCGI(w http.ResponseWriter, r *http.Request, route *Route) {
	// route.Socket 字段复用为 CGI 二进制路径
	cgiBinary := route.Socket
	if !fileExists(cgiBinary) {
		http.Error(w, fmt.Sprintf("CGI 二进制不存在: %s", cgiBinary), http.StatusInternalServerError)
		return
	}

	// 读取请求体（CGI 通过 stdin 传给进程）
	var bodyBytes []byte
	if r.Body != nil {
		bodyBytes, _ = io.ReadAll(r.Body)
		r.Body.Close()
	}

	// 构建 CGI 环境变量
	// 参考 RFC 3875 和 nginx fastcgi_params
	cgiEnv := []string{}

	// 基础 CGI 变量
	// SCRIPT_NAME 是 CGI 脚本的 URL 路径
	// 兼容两种前缀写法：
	//   1. 前缀已含脚本名：/cgi/ThirdParty/tailscale/index.cgi → 直接用
	//   2. 前缀是父目录：/cgi/ThirdParty/tailscale → 拼接 index.cgi
	scriptName := route.Prefix
	basename := filepath.Base(cgiBinary)
	if !strings.HasSuffix(scriptName, basename) {
		scriptName = strings.TrimSuffix(scriptName, "/") + "/" + basename
	}
	pathInfo := strings.TrimPrefix(r.URL.Path, route.Prefix)
	if pathInfo == "" {
		pathInfo = "/"
	}

	cgiEnv = append(cgiEnv,
		"GATEWAY_INTERFACE=CGI/1.1",
		"SERVER_PROTOCOL=HTTP/1.1",
		"SERVER_SOFTWARE=fpkm-gateway",
		"REQUEST_METHOD="+r.Method,
		"QUERY_STRING="+r.URL.RawQuery,
		"SCRIPT_NAME="+scriptName,
		"PATH_INFO="+pathInfo,
		"REQUEST_URI="+r.URL.RequestURI(),
		"DOCUMENT_ROOT="+filepath.Dir(cgiBinary),
		"SCRIPT_FILENAME="+cgiBinary,
	)

	// HTTP 头转 CGI 环境变量（HTTP_HOST → HTTP_HOST）
	for key, values := range r.Header {
		if len(values) == 0 {
			continue
		}
		headerName := "HTTP_" + strings.ReplaceAll(strings.ToUpper(key), "-", "_")
		cgiEnv = append(cgiEnv, headerName+"="+values[0])
	}

	// Content-Type 和 Content-Length 特殊处理（不带 HTTP_ 前缀）
	if ct := r.Header.Get("Content-Type"); ct != "" {
		cgiEnv = append(cgiEnv, "CONTENT_TYPE="+ct)
	}
	if r.ContentLength > 0 {
		cgiEnv = append(cgiEnv, fmt.Sprintf("CONTENT_LENGTH=%d", r.ContentLength))
	}

	// RemoteAddr
	if r.RemoteAddr != "" {
		cgiEnv = append(cgiEnv, "REMOTE_ADDR="+strings.Split(r.RemoteAddr, ":")[0])
	}

	// 传递应用环境变量（让 CGI 能找到 tailscaled socket 等）
	// 从应用的 env 文件读取，而不是从网关进程的环境变量读取
	// 因为 cmd/main 脚本里 export 的变量只在脚本执行期间有效
	appEnvFile := filepath.Join(g.cfg.BaseDir, "apps", route.AppName, "env")
	if envData, err := os.ReadFile(appEnvFile); err == nil {
		for _, line := range strings.Split(string(envData), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			// env 文件格式：KEY=VALUE
			cgiEnv = append(cgiEnv, line)
		}
		// 打印关键环境变量用于调试
		for _, e := range cgiEnv {
			if strings.HasPrefix(e, "TS_SOCKET=") || strings.HasPrefix(e, "CGI_PATH=") || strings.HasPrefix(e, "GATEWAY_PREFIX=") {
				logger.Info("[CGI] env: %s", e)
			}
		}
		logger.Debug("[CGI] 从 env 文件加载 %d 个环境变量", len(cgiEnv))
	}

	// 补充网关进程中的 TRIM_* 和 TS_* 变量（env 文件可能不完整）
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "TRIM_") || strings.HasPrefix(e, "TS_") {
			// 避免重复
			parts := strings.SplitN(e, "=", 2)
			if len(parts) == 2 {
				key := parts[0] + "="
				alreadyExists := false
				for _, existing := range cgiEnv {
					if strings.HasPrefix(existing, key) {
						alreadyExists = true
						break
					}
				}
				if !alreadyExists {
					cgiEnv = append(cgiEnv, e)
				}
			}
		}
	}

	// 确保 PATH 存在（CGI 二进制可能需要调用 sh 等系统命令）
	// 例如 tailscale 的 index.cgi 启动 taildrop 监控时需要执行 sh
	hasPath := false
	for _, e := range cgiEnv {
		if strings.HasPrefix(e, "PATH=") {
			hasPath = true
			break
		}
	}
	if !hasPath {
		cgiEnv = append(cgiEnv, "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
	}

	// 执行 CGI
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, cgiBinary)
	cmd.Env = cgiEnv
	cmd.Dir = filepath.Dir(cgiBinary)
	cmd.Stdin = bytes.NewReader(bodyBytes)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	logger.Info("[CGI] 执行: %s (env: %d 项)", cgiBinary, len(cgiEnv))

	if err := cmd.Run(); err != nil {
		logger.Error("[CGI] 执行失败: %v", err)
		if stderr.Len() > 0 {
			logger.Error("[CGI] stderr: %s", stderr.String())
		}
		http.Error(w, fmt.Sprintf("CGI 执行失败: %v", err), http.StatusBadGateway)
		return
	}

	// 解析 CGI 输出：HTTP 头 + 空行 + 响应体
	output := stdout.Bytes()
	if len(output) == 0 {
		http.Error(w, "CGI 无输出", http.StatusBadGateway)
		return
	}

	// 分离头和体
	headerEnd := bytes.Index(output, []byte("\r\n\r\n"))
	if headerEnd < 0 {
		headerEnd = bytes.Index(output, []byte("\n\n"))
		if headerEnd < 0 {
			// 没有头，直接当 body
			w.WriteHeader(http.StatusOK)
			w.Write(output)
			return
		}
		// 用 \n\n 分隔
		headerPart := output[:headerEnd]
		body := output[headerEnd+2:]

		// 解析头
		statusCode := http.StatusOK
		for _, line := range bytes.Split(headerPart, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			idx := bytes.Index(line, []byte(":"))
			if idx <= 0 {
				continue
			}
			key := strings.TrimSpace(string(line[:idx]))
			val := strings.TrimSpace(string(line[idx+1:]))

			// 处理 Status 头（CGI 特殊头）
			if strings.EqualFold(key, "Status") {
				if code, err := strconv.Atoi(strings.Split(val, " ")[0]); err == nil {
					statusCode = code
				}
				continue
			}
			w.Header().Set(key, val)
		}

		w.WriteHeader(statusCode)
		w.Write(body)
		return
	}

	// 用 \r\n\r\n 分隔
	headerPart := output[:headerEnd]
	body := output[headerEnd+4:]

	// 解析头
	statusCode := http.StatusOK
	for _, line := range bytes.Split(headerPart, []byte("\r\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		idx := bytes.Index(line, []byte(":"))
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(string(line[:idx]))
		val := strings.TrimSpace(string(line[idx+1:]))

		if strings.EqualFold(key, "Status") {
			if code, err := strconv.Atoi(strings.Split(val, " ")[0]); err == nil {
				statusCode = code
			}
			continue
		}
		w.Header().Set(key, val)
	}

	logger.Info("[CGI] 响应: %d, body=%d 字节", statusCode, len(body))
	w.WriteHeader(statusCode)
	w.Write(body)
}

// isWebSocketRequest 检查是否是 WebSocket 升级请求
// localOnly 限制处理器只能被本机访问。
//
// 网关的管理接口（查看/添加路由、状态）不需要登录，但绝不能暴露到
// 局域网：POST /__gateway__/routes 可以直接改写路由表。
func localOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLoopback(r.RemoteAddr) {
			http.Error(w, "该接口仅允许本机访问", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// isLoopback 判断来源地址是否为本机
func isLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// ---------- 登录校验 ----------

// sessionCookieName 会话 Cookie 名（必须与 internal/web 保持一致）
const sessionCookieName = "fpkm_session"

// authorized 判断请求是否携带有效的 FPKM 登录令牌。
//
// 令牌来源（与 Web 端一致）：
//  1. Cookie（浏览器访问时的主要方式 —— Cookie 不区分端口，
//     因此 :8080 登录后 :7666 的请求也会带上）；
//  2. Authorization: Bearer（API / 命令行）；
//  3. X-FPKM-Session 头；
//  4. ?session= 查询参数（兼容旧链接）。
func (g *Gateway) authorized(r *http.Request) bool {
	// 未启用门禁（或密钥不可用）时，为避免"静默放行"，
	// 这里返回 false 并已在 New 中记录错误日志。
	if !g.gate || g.sessions == nil {
		return false
	}
	return g.sessions.Verify(extractToken(r))
}

// extractToken 从请求中提取会话令牌
func extractToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	if h := r.Header.Get("X-FPKM-Session"); h != "" {
		return strings.TrimSpace(h)
	}
	if c, err := r.Cookie(sessionCookieName); err == nil && c.Value != "" {
		return c.Value
	}
	return r.URL.Query().Get("session")
}

// writeLoginRequired 返回"需要登录"提示。
//
// 浏览器直接访问时给出可点击的引导页（而不是干巴巴的 401 文本），
// 因为用户是在桌面里点开应用，看到一页错误会不知所措。
// 非 HTML 请求（fetch/XHR/WebSocket 握手等）仍返回标准 401，
// 便于前端代码判断并跳转登录。
func (g *Gateway) writeLoginRequired(w http.ResponseWriter, r *http.Request) {
	if !wantsHTML(r) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"未登录或登录已失效，请先在 FPKM 应用商店登录"}`))
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = io.WriteString(w, loginRequiredPage)
}

// wantsHTML 判断请求是否期望 HTML 响应
func wantsHTML(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	// WebSocket / fetch 等按非 HTML 处理
	if isWebSocketRequest(r) {
		return false
	}
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		return true
	}
	// Accept 缺失时的兜底：导航请求通常带 Sec-Fetch-Mode: navigate
	if r.Header.Get("Sec-Fetch-Mode") == "navigate" {
		return true
	}
	// 目录式路径（以 / 结尾）通常是页面导航
	return strings.HasSuffix(r.URL.Path, "/")
}

// loginRequiredPage 未登录时的引导页。
//
// 说明：这里刻意不自动跳转到 FPKM 首页 —— 桌面用 iframe 打开应用，
// iframe 内跳转会让用户困在窗口里。改为给出明确提示与"重新登录"链接，
// 链接 target="_top" 让整个页面跳转，而不是在 iframe 内打转。
const loginRequiredPage = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>需要登录 · FPKM</title>
<style>
  :root { color-scheme: dark; }
  html, body { height: 100%; margin: 0; }
  body {
    display: flex; align-items: center; justify-content: center;
    background: radial-gradient(900px 600px at 50% 20%, #16233a, #0b1220 70%);
    color: #e6edf7;
    font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', 'PingFang SC',
                 'Hiragino Sans GB', 'Microsoft YaHei', sans-serif;
  }
  .card {
    width: min(420px, calc(100vw - 48px));
    padding: 32px 28px;
    border-radius: 16px;
    text-align: center;
    background: rgba(22, 32, 50, .55);
    backdrop-filter: blur(18px) saturate(160%);
    border: 1px solid rgba(160, 205, 255, .22);
    box-shadow: 0 18px 48px rgba(0,0,0,.5);
  }
  .icon { margin-bottom: 16px; color: #7dd3fc; }
  .icon svg { width: 44px; height: 44px; display: block; margin: 0 auto; }
  h1 { font-size: 18px; margin: 0 0 10px; font-weight: 600; }
  p { font-size: 13.5px; line-height: 1.7; color: #a9bad2; margin: 0 0 22px; }
  a {
    display: inline-block; padding: 10px 22px; border-radius: 10px;
    background: #2b6cff; color: #fff; text-decoration: none;
    font-size: 14px; font-weight: 600;
  }
  a:hover { background: #1f5be0; }
</style>
</head>
<body>
  <div class="card">
    <div class="icon" aria-hidden="true">
      <!-- 用内联 SVG 而不是 emoji：emoji 依赖系统字体，
           在缺少 emoji 字体的环境（如部分 NAS 精简系统）会渲染成豆腐块 -->
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6"
           stroke-linecap="round" stroke-linejoin="round">
        <rect x="4" y="10.5" width="16" height="10.5" rx="2.4"></rect>
        <path d="M8 10.5V7.5a4 4 0 0 1 8 0v3"></path>
        <circle cx="12" cy="15.6" r="1.3" fill="currentColor" stroke="none"></circle>
      </svg>
    </div>
    <h1>需要先登录 FPKM</h1>
    <p>该应用通过 FPKM 统一网关访问，需要登录后才能使用。<br>登录后回到桌面重新打开即可。</p>
    <a href="/" target="_top">前往登录</a>
  </div>
</body>
</html>`

func isWebSocketRequest(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// proxyWebSocket WebSocket 代理
// WebSocket 是长连接，使用 Hijack 双向转发是正确的做法
func (g *Gateway) proxyWebSocket(w http.ResponseWriter, r *http.Request, route *Route) {
	backend, err := net.Dial("unix", route.Socket)
	if err != nil {
		http.Error(w, "连接后端失败", http.StatusBadGateway)
		return
	}
	defer backend.Close()

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "不支持 WebSocket", http.StatusInternalServerError)
		return
	}

	clientConn, _, err := hj.Hijack()
	if err != nil {
		return
	}
	defer clientConn.Close()

	// 剥离前缀
	strippedPath := strings.TrimPrefix(r.URL.Path, route.Prefix)
	if strippedPath == "" {
		strippedPath = "/"
	}
	if r.URL.RawQuery != "" {
		strippedPath += "?" + r.URL.RawQuery
	}

	// 转发升级请求
	// 手动构造 HTTP 请求，因为 Hijack 后需要原始 TCP 转发
	reqLine := fmt.Sprintf("%s %s HTTP/1.1\r\n", r.Method, strippedPath)
	headers := reqLine
	headers += fmt.Sprintf("Host: %s\r\n", r.Host)

	// 转发客户端头，跳过 Content-Length（WebSocket 升级请求无 body）
	// 和 Connection（hop-by-hop，由下面统一设置）
	for key, values := range r.Header {
		lowerKey := strings.ToLower(key)
		if lowerKey == "content-length" {
			continue
		}
		for _, v := range values {
			headers += fmt.Sprintf("%s: %s\r\n", key, v)
		}
	}

	// 设置网关转发头（与 HTTP Director 保持一致）
	headers += fmt.Sprintf("X-Forwarded-For: %s\r\n", r.RemoteAddr)
	headers += "X-Forwarded-Proto: http\r\n"
	headers += fmt.Sprintf("X-Gateway-Prefix: %s\r\n", route.Prefix)
	headers += fmt.Sprintf("X-Original-Host: %s\r\n", r.Host)

	// 模拟飞牛 trim_http_cgi 网关的管理员认证头
	// 部分应用（如 hermes）通过此头判断是否为管理员访问，
	// 未设置时返回 403 fnos_admin_required，导致 WebSocket 握手失败
	headers += "X-Trim-Isadmin: 1\r\n"

	headers += "\r\n"

	backend.Write([]byte(headers))

	// 双向转发
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(backend, clientConn)
		done <- struct{}{}
	}()
	go func() {
		io.Copy(clientConn, backend)
		done <- struct{}{}
	}()
	<-done
}

// handleRoutesAPI 路由管理 API
// GET  /__gateway__/routes          - 列出所有路由
// POST /__gateway__/routes          - 添加路由
// DELETE /__gateway__/routes?prefix= - 删除路由
func (g *Gateway) handleRoutesAPI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(g.GetRoutes())

	case http.MethodPost:
		var route Route
		if err := json.NewDecoder(r.Body).Decode(&route); err != nil {
			http.Error(w, "无效的 JSON", http.StatusBadRequest)
			return
		}
		if route.Prefix == "" || route.Socket == "" {
			http.Error(w, "prefix 和 socket 不能为空", http.StatusBadRequest)
			return
		}
		g.AddRouteWithProtocol(route.AppName, route.Prefix, route.Socket, route.Protocol, route.ScriptFilename)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "路由已添加")

	case http.MethodDelete:
		prefix := r.URL.Query().Get("prefix")
		if prefix == "" {
			http.Error(w, "缺少 prefix 参数", http.StatusBadRequest)
			return
		}
		g.RemoveRoute(prefix)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "路由已删除")

	default:
		http.Error(w, "不支持的方法", http.StatusMethodNotAllowed)
	}
}

// handleStatus 返回网关状态
func (g *Gateway) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	status := map[string]interface{}{
		"running": true,
		"port":    g.port,
		"pid":     os.Getpid(),
		"routes":  len(g.GetRoutes()),
	}
	json.NewEncoder(w).Encode(status)
}

// PidFile 网关 PID 文件路径
func (g *Gateway) PidFile() string {
	return filepath.Join(g.cfg.BaseDir, "gateway.pid")
}

// Port 返回网关端口
func (g *Gateway) Port() int {
	return g.port
}

// fileExists 检查文件是否存在
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// appLogPath 返回应用日志路径（用于错误提示）
func (g *Gateway) appLogPath(appName string) string {
	return filepath.Join(g.cfg.AppDir(appName), "logs", "app.log")
}

// appProcessState 读取应用 PID 文件判断进程是否存活
func (g *Gateway) appProcessState(appName string) (int, bool) {
	data, err := os.ReadFile(g.cfg.AppPidFile(appName))
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return pid, false
	}
	// Unix 上用信号 0 探测进程是否存在
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return pid, false
	}
	return pid, true
}
