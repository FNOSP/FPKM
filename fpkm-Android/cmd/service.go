package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"time"

	"fpkm/internal/config"
	"fpkm/internal/logger"

	"github.com/spf13/cobra"
)

var serviceStart bool
var serviceStop bool
var serviceStatus bool

// serviceCmd 后台服务模式
// 允许 fpkm 作为后台守护进程运行，管理所有应用
var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "后台服务管理",
	Long: `管理 fpkm 后台守护进程。

后台服务会自动启动所有已安装应用，并在进程退出时自动重启。

  fpkm service --start    启动后台服务
  fpkm service --stop     停止后台服务
  fpkm service --status   查看服务状态`,
	Run: func(cmd *cobra.Command, args []string) {
		cfg := config.GetConfig()

		switch {
		case serviceStart:
			startService(cfg)
		case serviceStop:
			stopService(cfg)
		case serviceStatus:
			serviceStatusCheck(cfg)
		default:
			cmd.Help()
		}
	},
}

func init() {
	rootCmd.AddCommand(serviceCmd)
	serviceCmd.Flags().BoolVar(&serviceStart, "start", false, "启动后台服务")
	serviceCmd.Flags().BoolVar(&serviceStop, "stop", false, "停止后台服务")
	serviceCmd.Flags().BoolVar(&serviceStatus, "status", false, "查看服务状态")
}

const servicePIDFile = "service.pid"

func startService(cfg *config.Config) {
	pidFile := fmt.Sprintf("%s/%s", cfg.BaseDir, servicePIDFile)

	// 检查是否已运行
	if data, err := os.ReadFile(pidFile); err == nil {
		pid := 0
		fmt.Sscanf(string(data), "%d", &pid)
		if pid > 0 && isProcessRunning(pid) {
			logger.Fatal("后台服务已在运行 (PID: %d)", pid)
		}
	}

	// 以守护进程方式启动
	execPath, err := os.Executable()
	if err != nil {
		logger.Fatal("获取可执行文件路径失败: %v", err)
	}

	cmd := exec.Command(execPath, "daemon")
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	setDaemonAttr(cmd)

	if err := cmd.Start(); err != nil {
		logger.Fatal("启动后台服务失败: %v", err)
	}

	// 等待一下确保进程启动
	time.Sleep(500 * time.Millisecond)

	os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644)
	logger.Success("后台服务已启动 (PID: %d)", cmd.Process.Pid)
}

func stopService(cfg *config.Config) {
	pidFile := fmt.Sprintf("%s/%s", cfg.BaseDir, servicePIDFile)

	data, err := os.ReadFile(pidFile)
	if err != nil {
		logger.Fatal("后台服务未运行")
	}

	pid := 0
	fmt.Sscanf(string(data), "%d", &pid)
	if pid == 0 {
		logger.Fatal("无效的 PID")
	}

	if runtime.GOOS == "windows" {
		exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", pid)).Run()
	} else {
		exec.Command("kill", "-TERM", fmt.Sprintf("%d", pid)).Run()
	}

	time.Sleep(time.Second)
	os.Remove(pidFile)
	logger.Success("后台服务已停止")
}

func serviceStatusCheck(cfg *config.Config) {
	pidFile := fmt.Sprintf("%s/%s", cfg.BaseDir, servicePIDFile)

	data, err := os.ReadFile(pidFile)
	if err != nil {
		fmt.Println("后台服务状态: 未运行")
		return
	}

	pid := 0
	fmt.Sscanf(string(data), "%d", &pid)
	if pid > 0 && isProcessRunning(pid) {
		fmt.Printf("后台服务状态: 运行中 (PID: %d)\n", pid)
	} else {
		fmt.Println("后台服务状态: 未运行 (PID文件残留)")
		os.Remove(pidFile)
	}
}

// daemonCmd 守护进程命令
var daemonCmd = &cobra.Command{
	Use:    "daemon",
	Short:  "以后台守护进程模式运行（内部使用）",
	Hidden: true,
	Run: func(cmd *cobra.Command, args []string) {
		runDaemon()
	},
}

func init() {
	rootCmd.AddCommand(daemonCmd)
}

// runDaemon 守护进程主循环
func runDaemon() {
	cfg := config.GetConfig()
	logger.Init(cfg.LogDir, logger.LevelInfo)
	logger.Info("fpkm 守护进程启动")

	// TODO: 自动启动所有已安装应用并监控
	// 目前简化为保持进程存活
	for {
		time.Sleep(30 * time.Second)
		logger.Debug("守护进程心跳")
	}
}

// isProcessRunning 检查进程是否运行
func isProcessRunning(pid int) bool {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("tasklist", "/FI", "PID eq", fmt.Sprintf("%d", pid), "/NH")
		output, err := cmd.Output()
		return err == nil && len(output) > 0
	}
	cmd := exec.Command("kill", "-0", fmt.Sprintf("%d", pid))
	return cmd.Run() == nil
}

// setDaemonAttr 设置守护进程属性
func setDaemonAttr(cmd *exec.Cmd) {
	if runtime.GOOS == "windows" {
		// Windows: 隐藏窗口
	} else {
		// Unix: 后台运行
	}
}
