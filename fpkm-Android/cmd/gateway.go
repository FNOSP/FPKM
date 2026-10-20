package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	"fpkm/internal/config"
	"fpkm/internal/gateway"
	"fpkm/internal/logger"

	"github.com/spf13/cobra"
)

var gatewayStart bool
var gatewayStop bool
var gatewayStatus bool
var gatewayRun bool
var gatewayPort int

var gatewayCmd = &cobra.Command{
	Use:   "gateway",
	Short: "管理统一网关",
	Long: `管理 fpkm 统一网关。

统一网关模拟飞牛的统一网关功能，将 /app/{appname} 路径的请求
转发到对应应用的 Unix Socket，默认监听 7666 端口。

使用 Go 内置网关实现，零依赖，跨平台，支持 HTTP、FastCGI、WebSocket。

  fpkm gateway --start    启动网关
  fpkm gateway --stop     停止网关
  fpkm gateway --status   查看网关状态和路由
  fpkm gateway --run      前台运行网关（内部使用）`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := config.GetConfig()

		switch {
		case gatewayStart:
			startGateway(cfg)
		case gatewayStop:
			stopGateway(cfg)
		case gatewayStatus:
			showGatewayStatus(cfg)
		case gatewayRun:
			runGateway(cfg)
		default:
			cmd.Help()
		}
	},
}

func init() {
	rootCmd.AddCommand(gatewayCmd)
	gatewayCmd.Flags().BoolVar(&gatewayStart, "start", false, "启动网关守护进程")
	gatewayCmd.Flags().BoolVar(&gatewayStop, "stop", false, "停止网关")
	gatewayCmd.Flags().BoolVar(&gatewayStatus, "status", false, "查看网关状态")
	gatewayCmd.Flags().BoolVar(&gatewayRun, "run", false, "前台运行网关（内部使用）")
	gatewayCmd.Flags().IntVar(&gatewayPort, "port", 7666, "网关监听端口")
}

// startGateway 启动网关
func startGateway(cfg *config.Config) {
	port := gatewayPort
	if port <= 0 {
		port = gateway.DefaultPort
	}

	client := gateway.NewClient(cfg)

	if client.IsRunning() {
		logger.Info("统一网关已在运行 (端口: %d)", port)
		return
	}

	execPath, err := os.Executable()
	if err != nil {
		logger.Fatal("获取可执行文件路径失败: %v", err)
	}

	logFile, err := os.OpenFile(cfg.GatewayLogFile(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		logger.Fatal("打开网关日志文件失败: %v", err)
	}

	cmd := exec.Command(execPath, "gateway", "--run", "--port", strconv.Itoa(port))
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	setDaemonAttr(cmd)

	if err := cmd.Start(); err != nil {
		logFile.Close()
		logger.Fatal("启动网关失败: %v", err)
	}

	os.WriteFile(cfg.GatewayPidFile(), []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644)
	logFile.Close()

	time.Sleep(500 * time.Millisecond)

	if client.IsRunning() {
		logger.Success("统一网关已启动 (端口: %d, PID: %d)", port, cmd.Process.Pid)
		logger.Info("访问地址: http://localhost:%d", port)
	} else {
		logger.Warn("网关可能未成功启动，请检查日志: %s", cfg.GatewayLogFile())
	}
}

// stopGateway 停止网关
// gatewayEmbedded 探测当前网关是否已并入 Web 进程（同端口）
func gatewayEmbedded(cfg *config.Config) bool {
	client := gateway.NewClient(cfg)
	st, err := client.GetStatus()
	if err != nil {
		return false
	}
	embedded, _ := st["embedded"].(bool)
	return embedded
}

func stopGateway(cfg *config.Config) {
	client := gateway.NewClient(cfg)

	// 网关已并入 Web 进程时，杀掉这个 PID 会连带停掉整个 Web 界面，
	// 因此只提示，不执行 kill。
	if gatewayEmbedded(cfg) {
		logger.Info("统一网关已并入 Web 服务（同端口），请用 Ctrl+C 或停止 Web 服务来关闭")
		return
	}

	pidFile := cfg.GatewayPidFile()
	data, err := os.ReadFile(pidFile)
	if err != nil && !client.IsRunning() {
		logger.Info("统一网关未运行")
		return
	}

	pid := 0
	if data != nil {
		fmt.Sscanf(string(data), "%d", &pid)
	}

	if pid > 0 {
		if runtime.GOOS == "windows" {
			exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", pid)).Run()
		} else {
			exec.Command("kill", "-TERM", fmt.Sprintf("%d", pid)).Run()
		}
		time.Sleep(time.Second)
	}

	os.Remove(pidFile)
	logger.Success("统一网关已停止")
}

// showGatewayStatus 显示网关状态
func showGatewayStatus(cfg *config.Config) {
	client := gateway.NewClient(cfg)

	status, err := client.GetStatus()
	if err != nil {
		logger.Info("统一网关未运行")
		return
	}

	logger.Info("统一网关运行中")
	if port, ok := status["port"]; ok {
		logger.Info("  端口: %v", port)
	}
	if mode, ok := status["mode"]; ok {
		logger.Info("  模式: %v", mode)
	}
	if routes, ok := status["routes"]; ok {
		logger.Info("  路由数: %v", routes)
	}

	// 列出路由
	routes, err := client.ListRoutes()
	if err == nil && len(routes) > 0 {
		logger.Info("  路由列表:")
		for _, r := range routes {
			statusStr := "未就绪"
			if r.Available {
				statusStr = "就绪"
			}
			logger.Info("    %s -> %s (%s)", r.Prefix, r.AppName, statusStr)
		}
	}
}

// runGateway 前台运行网关（由 --start 启动的子进程调用）
func runGateway(cfg *config.Config) {
	port := gatewayPort
	if port <= 0 {
		port = gateway.DefaultPort
	}

	g := gateway.New(cfg, port)
	logger.Info("统一网关启动，监听端口: %d", port)

	// 扫描已安装的应用并注册路由
	scanAndRegisterRoutes(g, cfg)

	if err := g.Start(); err != nil {
		logger.Fatal("网关启动失败: %v", err)
	}
}

// scanAndRegisterRoutes 扫描已安装的应用并注册路由
func scanAndRegisterRoutes(g *gateway.Gateway, cfg *config.Config) {
	appsDir := cfg.AppsDir
	entries, err := os.ReadDir(appsDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		appName := entry.Name()

		// 检查 socket 文件
		sockPath := fmt.Sprintf("%s/%s/var/run/app.sock", appsDir, appName)
		if _, err := os.Stat(sockPath); err == nil {
			prefix := fmt.Sprintf("/app/%s", appName)
			g.AddRoute(appName, prefix, sockPath)
		}
	}
}
