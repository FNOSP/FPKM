package web

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fpkm/internal/app"
	"fpkm/internal/config"
	"fpkm/internal/logger"
)

// Options Web 服务启动参数
type Options struct {
	Bind     string // 监听地址，如 127.0.0.1 或 0.0.0.0
	Port     int    // 监听端口
	AllowFS  bool   // 是否允许浏览服务器文件系统
	FSPrefix string // 局域网便捷模式：仅允许浏览此前缀下的路径（可选）
	// AutoStart 安装/升级完成后是否自动启动应用
	AutoStart bool
	// PublicHost 覆盖应用访问地址中的主机名。
	// 默认留空 → 前端使用浏览器地址栏的主机名（适用于直连与局域网）。
	// 通过反向代理/域名访问、且网关端口不直接暴露时，可显式指定。
	PublicHost string
	// PublicScheme 覆盖应用访问地址的协议（http/https），默认跟随当前页面
	PublicScheme string
}

// Server Web 管理服务
type Server struct {
	cfg       *config.Config
	mgr       *app.Manager
	opts      Options
	tasks     *TaskManager
	autoStart bool
	auth      *AuthManager
	catalog   *Catalog
	icons     *iconCache
}

// NewServer 创建 Web 服务
func NewServer(cfg *config.Config, opts Options) *Server {
	return &Server{
		cfg:       cfg,
		mgr:       app.NewManager(cfg),
		opts:      opts,
		tasks:     NewTaskManager(),
		autoStart: opts.AutoStart,
		auth:      NewAuthManager(filepath.Join(cfg.BaseDir, "password.json")),
		catalog:   NewCatalog(NewSourceStore(cfg)),
		icons:     newIconCache(cfg),
	}
}

// Handler 构建 HTTP 路由
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// 认证相关（无需登录）
	mux.HandleFunc("/api/auth/status", s.handleAuthStatus)   // 是否需要初始化/登录
	mux.HandleFunc("/api/auth/setup", s.handleAuthSetup)     // 首次设置密码
	mux.HandleFunc("/api/auth/login", s.handleAuthLogin)     // 登录
	mux.HandleFunc("/api/auth/logout", s.handleAuthLogout)   // 注销
	mux.HandleFunc("/api/auth/password", s.handleAuthChange) // 修改密码

	// 业务 API（统一走登录校验）
	api := http.NewServeMux()
	api.HandleFunc("/api/apps", s.handleApps)                    // GET 已安装列表
	api.HandleFunc("/api/apps/", s.handleAppAction)              // POST /api/apps/{name}/{action}
	api.HandleFunc("/api/wizard", s.handleWizard)                // GET 安装/卸载/配置向导
	api.HandleFunc("/api/detail", s.handleAppDetail)             // GET 应用详情
	api.HandleFunc("/api/store/apps", s.handleStoreApps)         // GET 远程应用（商店）
	api.HandleFunc("/api/store/download", s.handleStoreDownload) // POST 下载远程包（不安装）
	api.HandleFunc("/api/store/install", s.handleStoreInstall)   // POST 从远程源安装
	api.HandleFunc("/api/store/sources", s.handleSources)        // GET/POST 应用源
	api.HandleFunc("/api/store/sources/", s.handleSourceItem)    // DELETE 应用源
	api.HandleFunc("/api/icons/", s.handleIcon)                  // GET 应用图标
	api.HandleFunc("/api/fs/list", s.handleFSList)               // GET 目录浏览
	api.HandleFunc("/api/fs/inspect", s.handleFSInspect)         // GET 解析包信息
	api.HandleFunc("/api/install/local", s.handleInstallLocal)   // POST 本地路径安装
	api.HandleFunc("/api/install/upload", s.handleInstallUpload) // POST 上传安装
	api.HandleFunc("/api/tasks/", s.handleTaskStream)            // GET 任务日志流(SSE)
	api.HandleFunc("/api/meta", s.handleMeta)                    // GET 运行环境信息
	mux.Handle("/api/", s.withAuth(api))

	// 前端静态资源（SPA）
	mux.Handle("/", s.spaHandler())

	return s.withSecurityHeaders(mux)
}

// ---------- 中间件 ----------

// withSecurityHeaders 设置基础安全响应头
func (s *Server) withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// withAuth 登录校验：要求带有效会话令牌。
//
// 会话令牌由登录接口下发，前端保存在 localStorage，并放在
// Authorization: Bearer 或 X-FPKM-Session 头中。
// SSE（EventSource）与 <img> 无法自定义请求头，因此也接受 ?session= 查询参数。
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 尚未设置密码时，业务接口一律拒绝，前端会引导到初始化页
		if !s.auth.HasPassword() {
			writeJSONError(w, http.StatusUnauthorized, "尚未设置管理密码")
			return
		}
		if !s.auth.ValidSession(extractSession(r)) {
			writeJSONError(w, http.StatusUnauthorized, "登录已失效，请重新登录")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// extractSession 从请求中提取会话令牌
func extractSession(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	if h := r.Header.Get("X-FPKM-Session"); h != "" {
		return strings.TrimSpace(h)
	}
	return r.URL.Query().Get("session")
}

// clientKey 用于登录限速的来源标识
func clientKey(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}

// ---------- SPA 托管 ----------

// spaHandler 提供 go:embed 中的前端产物，并对未知路径回退到 index.html
func (s *Server) spaHandler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "前端资源不可用: "+err.Error(), http.StatusInternalServerError)
		})
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(path(r.URL.Path), "/")
		if clean == "" {
			clean = "index.html"
		}
		// 命中的真实文件直接返回，否则回退 index.html（前端路由）
		if f, err := sub.Open(clean); err == nil {
			f.Close()
			// 带哈希的静态资源可长期缓存
			if strings.HasPrefix(clean, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		r.URL.Path = "/"
		w.Header().Set("Cache-Control", "no-cache")
		fileServer.ServeHTTP(w, r)
	})
}

func path(p string) string {
	if p == "" {
		return "/"
	}
	return p
}

// ---------- 基础接口 ----------

// handleAuthStatus 告诉前端当前处于哪种状态：
//   - needsSetup=true  → 首次运行，需要设置管理密码
//   - authenticated=true → 已有有效会话，可直接进主界面
func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	hasPwd := s.auth.HasPassword()
	authed := hasPwd && s.auth.ValidSession(extractSession(r))
	writeJSON(w, http.StatusOK, map[string]any{
		"needsSetup":    !hasPwd,
		"hasPassword":   hasPwd,
		"authenticated": authed,
		"allowFS":       s.opts.AllowFS,
		"version":       Version,
	})
}

// handleAuthSetup 首次设置管理密码（仅在尚未设置密码时可用）
func (s *Server) handleAuthSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	if s.auth.HasPassword() {
		writeJSONError(w, http.StatusConflict, "管理密码已设置，请使用登录或修改密码接口")
		return
	}

	var req struct {
		Password string `json:"password"`
		Confirm  string `json:"confirm"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.Password != req.Confirm {
		writeJSONError(w, http.StatusBadRequest, "两次输入的密码不一致")
		return
	}
	if err := s.auth.SetPassword("", req.Password); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	// 设置成功后直接登录，省去再输一次
	session, err := s.auth.NewSession()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "创建会话失败: "+err.Error())
		return
	}
	logger.Info("已设置管理密码，来源 %s", clientKey(r))
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

// handleAuthLogin 使用管理密码登录
func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	if !s.auth.HasPassword() {
		writeJSONError(w, http.StatusConflict, "尚未设置管理密码")
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}

	key := clientKey(r)
	if err := s.auth.Verify(key, req.Password); err != nil {
		logger.Warn("登录失败（来源 %s）: %v", key, err)
		writeJSONError(w, http.StatusUnauthorized, err.Error())
		return
	}

	session, err := s.auth.NewSession()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "创建会话失败: "+err.Error())
		return
	}
	logger.Info("登录成功（来源 %s）", key)
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

// handleAuthLogout 注销当前会话
func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	s.auth.DropSession(extractSession(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAuthChange 修改管理密码（需登录 + 旧密码）
func (s *Server) handleAuthChange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}
	if !s.auth.ValidSession(extractSession(r)) {
		writeJSONError(w, http.StatusUnauthorized, "请先登录")
		return
	}

	var req struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
		Confirm     string `json:"confirm"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if req.NewPassword != req.Confirm {
		writeJSONError(w, http.StatusBadRequest, "两次输入的新密码不一致")
		return
	}
	if err := s.auth.SetPassword(req.OldPassword, req.NewPassword); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	// 改密后旧会话已失效，直接下发新会话
	session, err := s.auth.NewSession()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "创建会话失败: "+err.Error())
		return
	}
	logger.Info("管理密码已修改")
	writeJSON(w, http.StatusOK, map[string]any{"session": session})
}

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	host, _ := os.Hostname()
	writeJSON(w, http.StatusOK, map[string]any{
		"version":      Version,
		"baseDir":      s.cfg.BaseDir,
		"appsDir":      s.cfg.AppsDir,
		"hostname":     host,
		"gatewayPort":  s.cfg.GatewayPort,
		"webPort":      s.opts.Port,
		"allowFS":      s.opts.AllowFS,
		"autoStart":    s.autoStart,
		"publicHost":   s.opts.PublicHost,
		"publicScheme": s.opts.PublicScheme,
	})
}

// ---------- 工具函数 ----------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"error": msg})
}

// version/token 生成辅助

// Version 前端资源版本标识（构建期可覆盖）
var Version = "1.0.0-web"

// RandomToken 生成随机访问令牌
func RandomToken(n int) (string, error) {
	if n <= 0 {
		n = 32
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// DefaultTokenFile 令牌持久化路径
func DefaultTokenFile(cfg *config.Config) string {
	return filepath.Join(cfg.BaseDir, "web.token")
}

// LoadOrCreateToken 读取已有令牌，不存在则生成并写入
func LoadOrCreateToken(cfg *config.Config) (string, error) {
	p := DefaultTokenFile(cfg)
	if data, err := os.ReadFile(p); err == nil {
		if t := strings.TrimSpace(string(data)); t != "" {
			return t, nil
		}
	}
	token, err := RandomToken(24)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(p, []byte(token), 0600); err != nil {
		return "", fmt.Errorf("写入令牌文件失败: %w", err)
	}
	return token, nil
}

// LocalIPs 返回本机可用于访问的 IPv4 地址（用于启动提示）
func LocalIPs() []string {
	var out []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			if v4 := ip.To4(); v4 != nil {
				out = append(out, v4.String())
			}
		}
	}
	return out
}

// nowMillis 当前时间戳（毫秒）
func nowMillis() int64 { return time.Now().UnixMilli() }
