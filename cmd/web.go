package cmd

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"fpkm/internal/app"
	"fpkm/internal/config"
	"fpkm/internal/logger"
	"fpkm/internal/web"

	"github.com/spf13/cobra"
)

var (
	webBind          string
	webPort          int
	webResetPassword bool
	webAllowFS       bool
	webOpenAddr      bool
	webAutoStart     bool
	webPublicHost    string
	webPublicScheme  string
)

var webCmd = &cobra.Command{
	Use:   "web",
	Short: "启动 Web 应用商店界面",
	Long: `启动 fpkm 的 Web 图形界面，用于浏览、安装和管理 FPK 应用。

功能：
  - 浏览远程应用源并一键安装（应用商店）
  - 从服务器本地文件安装 .fpk 应用
  - 从本机上传 .fpk 安装包安装
  - 已安装应用的启动 / 停止 / 卸载 / 升级

首次运行：
  打开页面会要求设置一个管理密码，之后用这个密码登录。
  密码以 PBKDF2-HMAC-SHA256 加盐存储，不会明文保存。

忘记密码：
  fpkm web --reset-password    # 清除密码，下次打开页面重新设置

安全提示：
  管理接口可以安装并运行任意程序，务必设置强密码。
  默认监听 127.0.0.1；如需局域网访问，请使用 --bind 0.0.0.0。

  fpkm web                                  # 本机访问
  fpkm web --bind 0.0.0.0 --port 7666       # 局域网访问
  fpkm web --reset-password                 # 重置管理密码`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := config.GetConfig()
		runWebServer(cfg)
	},
}

func init() {
	rootCmd.AddCommand(webCmd)
	webCmd.Flags().StringVar(&webBind, "bind", "127.0.0.1", "监听地址（0.0.0.0 表示允许局域网访问）")
	webCmd.Flags().IntVar(&webPort, "port", 7666, "监听端口（与统一网关共用同一端口）")
	webCmd.Flags().BoolVar(&webResetPassword, "reset-password", false, "清除已设置的管理密码（忘记密码时使用，下次打开页面可重新设置）")
	webCmd.Flags().BoolVar(&webAllowFS, "allow-fs", true, "允许浏览服务器文件系统以选择本地安装包")
	webCmd.Flags().BoolVar(&webOpenAddr, "show-address", true, "启动后打印可访问地址")
	webCmd.Flags().BoolVar(&webAutoStart, "auto-start", true, "安装/升级完成后自动启动应用")
	webCmd.Flags().StringVar(&webPublicHost, "public-host", "", "对外主机名（含端口），用于反向代理场景；留空则用浏览器当前地址")
	webCmd.Flags().StringVar(&webPublicScheme, "public-scheme", "", "对外协议 http/https；留空则跟随浏览器当前协议")
}

func runWebServer(cfg *config.Config) {
	// --reset-password：忘记密码时的救援开关，删除密码记录后退出
	if webResetPassword {
		removed, err := web.ResetPassword(cfg)
		if err != nil {
			logger.Fatal("重置管理密码失败: %v", err)
		}
		if removed {
			logger.Success("已清除管理密码，下次打开页面可重新设置")
		} else {
			logger.Info("当前未设置管理密码，无需重置")
		}
		return
	}

	opts := web.Options{
		Bind:         webBind,
		Port:         webPort,
		AllowFS:      webAllowFS,
		AutoStart:    webAutoStart,
		PublicHost:   webPublicHost,
		PublicScheme: webPublicScheme,
	}

	// 关键：让 cfg.GatewayPort 与实际监听端口保持一致。
	//
	// app.Manager 注册应用路由的方式是「HTTP POST 到 127.0.0.1:cfg.GatewayPort」，
	// 而网关现已并入本进程。若两者不一致（例如 --port 8000），
	// 注册请求会打到错误的端口，应用启动后 /app/* 无法访问。
	// 同时 describeAccess 也用它生成访问地址，必须同源。
	if webPort > 0 {
		cfg.GatewayPort = webPort
	}

	srv := web.NewServer(cfg, opts)
	addr := net.JoinHostPort(webBind, strconv.Itoa(webPort))

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		// 上传大包需要较长时间，不设置 WriteTimeout
		IdleTimeout: 120 * time.Second,
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		// 最常见的失败原因是"旧的独立网关仍占着同一端口"：
		// Web 与网关现已统一端口，升级后必须先停掉旧网关进程。
		if strings.Contains(err.Error(), "address already in use") {
			logger.Fatal("监听 %s 失败：端口已被占用。\n"+
				"  网关与 Web 现已统一到同一端口；若旧版独立网关仍在运行，请先停止：\n"+
				"    fpkm gateway --stop      # 停止旧网关\n"+
				"    fpkm web --bind 0.0.0.0 --port %d\n"+
				"  或用其它端口启动: fpkm web --port 8000（应用地址会自动跟随）",
				addr, webPort)
		}
		logger.Fatal("监听 %s 失败: %v", addr, err)
	}

	// 记录实际监听端口。
	//
	// --port 0 时由系统分配，因此要在监听成功后取真实值，
	// 并回写 cfg.GatewayPort：Manager 注册应用路由时要用它。
	if tcpAddr, ok := ln.Addr().(*net.TCPAddr); ok {
		cfg.GatewayPort = tcpAddr.Port
		if werr := os.WriteFile(cfg.WebPortFile(),
			[]byte(strconv.Itoa(tcpAddr.Port)), 0o644); werr != nil {
			logger.Warn("写入 Web 端口记录失败（不影响运行）: %v", werr)
		}
	}

	// 扫描已安装应用并注册路由到内嵌网关。
	//
	// 网关与 Web 现在同一个进程、同一个端口：应用路由必须在这里注册，
	// 否则重启后已安装的应用（socket/CGI）不会被代理，/app/* 会 404。
	registerExistingRoutes(srv)

	// 恢复上次处于运行状态的应用（异步，不阻塞监听）。
	// 应用是 fpkm 拉起的子进程，fpkm 退出时随之结束，
	// 因此这里按"用户意图"重新拉起它们。
	web.RestoreAutostart(cfg, srv)

	logger.Success("FPKM 应用商店已启动")
	logger.Info("  监听地址: %s", addr)
	if webOpenAddr {
		logger.Info("  本机访问: http://127.0.0.1:%d", webPort)
		if webBind == "0.0.0.0" {
			for _, ip := range web.LocalIPs() {
				logger.Info("  局域网访问: http://%s:%d", ip, webPort)
			}
		}
	}
	// 引导用户完成首次密码设置
	if web.HasPassword(cfg) {
		logger.Info("  管理密码: 已设置（登录后可在界面中修改）")
	} else {
		logger.Info("  管理密码: 尚未设置 — 请打开页面完成初始化")
	}
	if webBind != "127.0.0.1" && webBind != "localhost" && !web.HasPassword(cfg) {
		logger.Warn("  当前监听 %s 且尚未设置密码，请在首次打开页面时立即设置", webBind)
	}

	// 优雅关闭
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		logger.Fatal("Web 服务异常退出: %v", err)
	case <-ctx.Done():
		logger.Info("正在关闭 Web 服务...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			logger.Warn("关闭超时: %v", err)
		}
		logger.Success("Web 服务已停止")
	}
}

// registerExistingRoutes 把已安装应用的网关路由注册到内嵌网关。
//
// 与独立的 `fpkm gateway --run` 不同：网关不再单独扫描（它已并入本进程），
// 因此启动时必须主动扫描一次，否则重启后已安装应用的 /app/* 会 404。
//
// 判定依据与 cmd/gateway.go 的扫描保持一致：以应用 .env 中的
// GATEWAY_PREFIX / SOCKET_PATH / CGI_PATH 为准。
func registerExistingRoutes(srv *web.Server) {
	gw := srv.Gateway()
	if gw == nil {
		return
	}

	cfg := config.GetConfig()
	mgr := app.NewManager(cfg)
	infos, err := mgr.List()
	if err != nil {
		logger.Warn("扫描已安装应用失败，应用路由未注册: %v", err)
		return
	}

	registered := 0
	for _, info := range infos {
		env, err := app.LoadEnvFile(cfg.AppEnvFile(info.Name))
		if err != nil || env == nil {
			continue
		}
		prefix := env["GATEWAY_PREFIX"]
		if prefix == "" {
			continue
		}

		switch {
		case env["CGI_PATH"] != "":
			gw.AddRouteCGI(info.Name, prefix, env["CGI_PATH"])
			registered++
		case env["SOCKET_PATH"] != "":
			// 只注册 socket 已存在的应用：未运行的应用注册了也没用，
			// 反而会让 /app/* 返回"Socket 不存在"而不是前端页面。
			if _, err := os.Stat(env["SOCKET_PATH"]); err == nil {
				gw.AddRoute(info.Name, prefix, env["SOCKET_PATH"])
				registered++
			}
		}
	}
	if registered > 0 {
		logger.Info("已注册 %d 个应用路由到统一网关", registered)
	}
}
