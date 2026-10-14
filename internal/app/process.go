package app

import (
	"context"
	"fmt"
	"fpkm/internal/logger"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ProcessManager 进程管理器
type ProcessManager struct {
	pidFile string
	logFile string
}

// NewProcessManager 创建进程管理器
func NewProcessManager(pidFile, logFile string) *ProcessManager {
	return &ProcessManager{
		pidFile: pidFile,
		logFile: logFile,
	}
}

// Start 启动应用进程
func (pm *ProcessManager) Start(entry *EntryInfo, env EnvMap) error {
	if pm.IsRunning() {
		return fmt.Errorf("应用已在运行中 (PID: %d)", pm.readPID())
	}

	os.MkdirAll(filepath.Dir(pm.logFile), 0755)

	logF, err := os.OpenFile(pm.logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("打开日志文件失败: %w", err)
	}

	if entry.Command == "__builtin_static__" {
		logF.Close()
		logger.Debug("启动内置静态文件服务器: %s", entry.WorkDir)
		return pm.startStaticServer(entry, env)
	}

	// Bash 脚本应用：执行 bash cmd/main start
	// 这类脚本会后台启动二进制并管理自己的 PID 文件
	if entry.Type == EntryBashScript {
		logF.Close()
		return pm.startBashScript(entry, env)
	}

	// 执行预启动命令（如创建 TUN 设备、加载内核模块等系统级设置）
	for _, preCmd := range entry.PreStartCmds {
		fullPreCmd := preCmd.Command + " " + strings.Join(preCmd.Args, " ")
		logger.Info("执行预启动命令: %s", fullPreCmd)

		preLog, err := os.OpenFile(pm.logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			logger.Warn("打开预启动命令日志失败: %v", err)
			continue
		}

		// 带超时执行，避免被误解析的命令让启动永久卡住
		pctx, pcancel := context.WithTimeout(context.Background(), preStartTimeout)
		c := exec.CommandContext(pctx, preCmd.Command, preCmd.Args...)
		c.Dir = entry.WorkDir
		c.Stdout = preLog
		c.Stderr = preLog
		c.Env = append(os.Environ(), env.ToOsEnv()...)

		if err := c.Run(); err != nil {
			if pctx.Err() == context.DeadlineExceeded {
				logger.Warn("预启动命令超时（%s），已终止: %s", preStartTimeout, fullPreCmd)
			} else {
				logger.Warn("预启动命令失败（继续启动主进程）: %s: %v", fullPreCmd, err)
			}
		} else {
			logger.Info("预启动命令完成: %s", fullPreCmd)
		}
		pcancel()
		preLog.Close()
	}

	// 详细日志：显示实际启动命令
	fullCmd := entry.Command + " " + strings.Join(entry.Args, " ")
	logger.Info("启动命令: %s", fullCmd)
	logger.Debug("工作目录: %s", entry.WorkDir)
	logger.Debug("环境变量: %v", env.ToOsEnv())

	cmd := exec.Command(entry.Command, entry.Args...)
	cmd.Dir = entry.WorkDir
	cmd.Stdout = logF
	cmd.Stderr = logF
	cmd.Env = append(os.Environ(), env.ToOsEnv()...)
	setProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		logF.Close()
		return fmt.Errorf("启动进程失败: %w", err)
	}

	pid := cmd.Process.Pid
	if err := pm.writePID(pid); err != nil {
		logF.Close()
		return fmt.Errorf("写入PID文件失败: %w", err)
	}

	go func() {
		defer logF.Close()
		cmd.Wait()
		pm.clearPID()
	}()

	// 启动附加进程（多进程应用，如 EasyTier-Web 的 easytier-core 和 gateway-proxy）
	for _, extra := range entry.ExtraCmds {
		extraCmd := extra.Command + " " + strings.Join(extra.Args, " ")
		logger.Info("启动附加进程: %s", extraCmd)

		extraLog, err := os.OpenFile(pm.logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			logger.Warn("打开附加进程日志失败: %v", err)
			continue
		}

		workDir := extra.WorkDir
		if workDir == "" {
			workDir = entry.WorkDir
		}

		c := exec.Command(extra.Command, extra.Args...)
		c.Dir = workDir
		c.Stdout = extraLog
		c.Stderr = extraLog
		c.Env = append(os.Environ(), env.ToOsEnv()...)
		setProcessGroup(c)

		if err := c.Start(); err != nil {
			logger.Warn("附加进程启动失败: %v", err)
			extraLog.Close()
			continue
		}
		logger.Debug("附加进程已启动 (PID: %d)", c.Process.Pid)

		go func() {
			defer extraLog.Close()
			c.Wait()
		}()
	}

	return nil
}

// startBashScript 启动 Bash 脚本应用
// 执行 bash cmd/main start，脚本会后台启动二进制并写入自己的 PID 文件
// fpkm 读取脚本的 PID 文件来跟踪进程
func (pm *ProcessManager) startBashScript(entry *EntryInfo, env EnvMap) error {
	fullCmd := entry.Command + " " + strings.Join(entry.Args, " ")
	logger.Info("启动命令: %s", fullCmd)
	logger.Debug("工作目录: %s", entry.WorkDir)
	logger.Debug("环境变量: %v", env.ToOsEnv())

	cmd := exec.Command(entry.Command, entry.Args...)
	cmd.Dir = entry.WorkDir
	cmd.Env = append(os.Environ(), env.ToOsEnv()...)
	setProcessGroup(cmd)

	// 脚本的输出重定向到日志文件
	logF, err := os.OpenFile(pm.logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err == nil {
		cmd.Stdout = logF
		cmd.Stderr = logF
	}

	if err := cmd.Run(); err != nil {
		if logF != nil {
			logF.Close()
		}
		return fmt.Errorf("启动脚本失败: %w", err)
	}
	if logF != nil {
		logF.Close()
	}

	// 脚本会在后台启动二进制并写入自己的 PID 文件
	// 等待 PID 文件出现，然后读取
	// PID 文件路径可能是：
	//   - ${TRIM_PKGVAR}/${APP_NAME}.pid （fpkm 约定）
	//   - ${TRIM_PKGVAR}/app.pid         （飞牛原版约定，如 fnchat）
	//   - ${TRIM_PKGVAR}/info.pid        （其他可能）
	//   - ${TRIM_PKGVAR}/*.pid           （通配符匹配）
	pidFile := env["TRIM_PKGVAR"]
	if pidFile != "" {
		appName := env["TRIM_APPNAME"]

		// 候选 PID 文件列表
		candidatePidFiles := []string{
			filepath.Join(pidFile, appName+".pid"), // fpkm 约定
			filepath.Join(pidFile, "app.pid"),      // 飞牛原版约定
			filepath.Join(pidFile, "info.pid"),     // 其他可能
		}

		// 等待 PID 文件创建（最多 10 分钟）
		// 某些应用（如 fnchat）需要在启动前创建 venv 并安装 pip 依赖，耗时较长
		for i := 0; i < 6000; i++ {
			// 1. 先检查候选 PID 文件
			for _, pidFilePath := range candidatePidFiles {
				if pid, ok := readPidFromFile(pidFilePath); ok && isProcessAlive(pid) {
					pm.writePID(pid)
					logger.Debug("从脚本 PID 文件读取到 PID: %d (%s)", pid, filepath.Base(pidFilePath))
					return nil
				}
			}

			// 2. 通配符匹配 var 目录下的所有 .pid 文件
			matches, _ := filepath.Glob(filepath.Join(pidFile, "*.pid"))
			for _, match := range matches {
				// 跳过 fpkm 自己的 PID 文件（app.pid 在 appDir 下，不在 var 下，所以这里不会匹配到）
				if pid, ok := readPidFromFile(match); ok && isProcessAlive(pid) {
					pm.writePID(pid)
					logger.Debug("从脚本 PID 文件读取到 PID: %d (%s)", pid, filepath.Base(match))
					return nil
				}
			}

			time.Sleep(100 * time.Millisecond)
		}
		logger.Warn("未找到脚本 PID 文件（已等待 10 分钟），候选: %v", candidatePidFiles)
	}

	return nil
}

// readPidFromFile 从文件读取 PID
// 返回 PID 和是否成功
func readPidFromFile(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pidStr := strings.TrimSpace(string(data))
	// 处理可能的非数字字符（如换行符）
	pidStr = strings.TrimPrefix(pidStr, "\n")
	pidStr = strings.TrimSuffix(pidStr, "\n")
	pid, err := strconv.Atoi(pidStr)
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// startStaticServer 启动内置静态文件服务器
func (pm *ProcessManager) startStaticServer(entry *EntryInfo, env EnvMap) error {
	staticDir := entry.Args[0]
	port := env["TRIM_SERVICE_PORT"]
	if port == "" {
		port = "8080"
	}

	cmd := exec.Command(os.Args[0], "serve", "--dir", staticDir, "--port", port)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	setProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动静态服务器失败: %w", err)
	}

	if err := pm.writePID(cmd.Process.Pid); err != nil {
		return fmt.Errorf("写入PID文件失败: %w", err)
	}

	go func() {
		cmd.Wait()
		pm.clearPID()
	}()

	return nil
}

// Stop 停止应用进程
func (pm *ProcessManager) Stop() error {
	pid := pm.readPID()
	if pid == 0 {
		return nil
	}

	// 优雅停止
	pm.terminateProcess(pid)

	// 等待退出
	timeout := 10 * time.Second
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !pm.IsRunning() {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	// 超时强制杀死
	if pm.IsRunning() {
		pm.killProcess(pid)
		time.Sleep(time.Second)
	}

	pm.clearPID()
	return nil
}

// IsRunning 检查进程是否在运行
func (pm *ProcessManager) IsRunning() bool {
	pid := pm.readPID()
	if pid == 0 {
		return false
	}
	return isProcessAlive(pid)
}

// GetPID 获取当前运行的 PID
func (pm *ProcessManager) GetPID() int {
	return pm.readPID()
}

// readPID 从文件读取 PID
func (pm *ProcessManager) readPID() int {
	data, err := os.ReadFile(pm.pidFile)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return pid
}

// writePID 写入 PID 到文件
func (pm *ProcessManager) writePID(pid int) error {
	return os.WriteFile(pm.pidFile, []byte(fmt.Sprintf("%d", pid)), 0644)
}

// clearPID 清除 PID 文件
func (pm *ProcessManager) clearPID() {
	os.Remove(pm.pidFile)
}

// isPortInUse 检查端口是否被占用（跨平台）
func isPortInUse(port int) bool {
	addr := fmt.Sprintf(":%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return true
	}
	ln.Close()
	return false
}

// preStartTimeout 单条预启动命令的超时上限。
//
// 预启动命令用于"创建 TUN 设备""初始化数据库"这类短任务，
// 正常都应在数秒内结束。设置上限可避免解析异常时把启动流程卡死。
const preStartTimeout = 60 * time.Second
