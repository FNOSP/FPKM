package app

import (
	"context"
	"errors"
	"fmt"
	"fpkm/internal/logger"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
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
		c.Env = appEnviron(env)

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

	// 启动前确保可执行权限。
	//
	// 真实故障：Lucky 报 "fork/exec .../bin/lucky: permission denied"。
	// 文件存在、架构也对，只是丢了执行位（x）。来源不止一处：
	//   - 包内文件权限本来就是 0644（打包时不规范）；
	//   - 升级/迁移时经 copyFile 复制，而 os.WriteFile 对**已存在**的
	//     文件不会改权限，于是旧文件的 0644 被保留下来。
	// 这里在 exec 之前补一次 chmod，对正常文件是幂等的（no-op）。
	if err := ensureExecutable(entry.Command); err != nil {
		logF.Close()
		return err
	}

	cmd := exec.Command(entry.Command, entry.Args...)
	cmd.Dir = entry.WorkDir
	cmd.Stdout = logF
	cmd.Stderr = logF
	cmd.Env = appEnviron(env)
	setProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		logF.Close()
		return execStartError(entry.Command, err)
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
		c.Env = appEnviron(env)
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
	cmd.Env = appEnviron(env)
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
		var deadPidSince time.Time
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
			sawPidFile := false
			matches, _ := filepath.Glob(filepath.Join(pidFile, "*.pid"))
			for _, match := range matches {
				// 跳过 fpkm 自己的 PID 文件（app.pid 在 appDir 下，不在 var 下，所以这里不会匹配到）
				if pid, ok := readPidFromFile(match); ok {
					sawPidFile = true
					if isProcessAlive(pid) {
						pm.writePID(pid)
						logger.Debug("从脚本 PID 文件读取到 PID: %d (%s)", pid, filepath.Base(match))
						return nil
					}
				}
			}

			// PID 文件已写出、但进程已经不在了 —— 说明应用启动后立刻退出
			// （典型原因：端口被占用 bind 失败、缺少配置）。
			// 这种情况不能空等 10 分钟然后当作成功，要尽快报错并附日志尾部。
			if sawPidFile {
				if deadPidSince.IsZero() {
					deadPidSince = time.Now()
				} else if time.Since(deadPidSince) > 3*time.Second {
					return fmt.Errorf("应用启动后立即退出（PID 文件存在但进程已消失）%s",
						TailLogHint(pm.logFile, 12))
				}
			} else {
				deadPidSince = time.Time{}
			}

			time.Sleep(100 * time.Millisecond)
		}
		logger.Warn("未找到脚本 PID 文件（已等待 10 分钟），候选: %v", candidatePidFiles)
	}

	return nil
}

// TailLogHint 读取日志文件末尾若干行，用于把失败原因直接写进错误信息。
//
// 为什么需要：应用启动失败时（典型 "bind: address already in use"），
// 真正原因只写在应用自己的日志里。若上层只报"启动失败"，用户与维护者
// 都无从判断，只能反复猜测。
//
// 只读取文件尾部固定大小的窗口，避免把巨大的日志整份读进内存。
func TailLogHint(logFile string, n int) string {
	if logFile == "" || n <= 0 {
		return ""
	}
	f, err := os.Open(logFile)
	if err != nil {
		return ""
	}
	defer f.Close()

	const window = 64 << 10 // 64KB 足够覆盖最近若干行
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return ""
	}
	offset := int64(0)
	if st.Size() > window {
		offset = st.Size() - window
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return ""
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return ""
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	kept := make([]string, 0, len(lines))
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		return ""
	}
	return "\n应用日志尾部:\n" + strings.Join(kept, "\n")
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

// ensureExecutable 在 exec 之前确保文件具备执行权限。
//
// 只处理"缺少 x 位"这一种情况；其它错误（文件不存在、是目录、
// 文件系统 noexec 等）原样交给后续 exec 报错，避免掩盖真实原因。
func ensureExecutable(path string) error {
	if path == "" || strings.HasPrefix(path, "__builtin_") {
		return nil
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil // 不存在的情况让 exec 自己去报错，信息更准确
	}
	if st.IsDir() {
		return nil
	}
	if st.Mode()&0111 != 0 {
		return nil // 已有执行位
	}

	// 保留原权限，补上执行位（0644 → 0755）
	newMode := st.Mode().Perm() | 0755
	if err := os.Chmod(path, newMode); err != nil {
		return fmt.Errorf("文件缺少执行权限且自动修复失败: %s: %w", path, err)
	}
	logger.Warn("已自动补上执行权限: %s (%o → %o)", path, st.Mode().Perm(), newMode)
	return nil
}

// execStartError 把 exec 的底层错误翻译成可操作的中文提示。
//
// exec.Command 报错文本很生硬（"fork/exec /path: permission denied"），
// 用户看不出该改什么。这里按 errno 分类给出具体建议。
func execStartError(path string, err error) error {
	msg := err.Error()

	// 优先用 errors.Is 判断底层 errno（exec 返回的
	// *os.PathError / *fs.PathError 可被 Is 识别），
	// 文本匹配只作为兜底。
	isPerm := errors.Is(err, fs.ErrPermission) || strings.Contains(msg, "permission denied")
	isNotExist := errors.Is(err, fs.ErrNotExist) || strings.Contains(msg, "no such file")
	isExecFormat := errors.Is(err, syscall.ENOEXEC) || strings.Contains(msg, "exec format error")

	switch {
	case isPerm:
		detail := fmt.Sprintf(
			"启动失败：文件没有执行权限\n"+
				"  文件: %s\n"+
				"  处理: chmod +x %q\n"+
				"  若执行上面命令后仍报同样错误，说明该目录所在文件系统挂载了 noexec，\n"+
				"  需要用 mount -o remount,exec 重新挂载，或把应用装到别的分区。",
			path, path)
		// 附带目录权限链诊断：路径上任意一级缺少 x 也会导致 EACCES
		if bad := firstUntraversableDir(path); bad != "" {
			detail += fmt.Sprintf("\n  另外发现上级目录不可进入（缺少 x 权限）: %s", bad)
		}
		return errors.New(detail)

	case isNotExist:
		return fmt.Errorf("启动失败：找不到可执行文件\n  文件: %s", path)

	case isExecFormat:
		return fmt.Errorf(
			"启动失败：可执行文件格式不匹配\n"+
				"  文件: %s\n"+
				"  当前系统: %s/%s，请确认安装的是对应架构的包",
			path, runtime.GOOS, runtime.GOARCH)

	case strings.Contains(msg, "text file busy"):
		return fmt.Errorf("启动失败：可执行文件正被占用（可能上次进程尚未完全退出）\n  文件: %s", path)

	case strings.Contains(msg, "too many open files"):
		return fmt.Errorf("启动失败：文件句柄数达到上限，请检查系统的 ulimit -n")
	}

	return fmt.Errorf("启动进程失败: %w", err)
}

// firstUntraversableDir 找出路径中第一个"当前用户无法进入"的目录。
//
// 用于诊断 EACCES：exec 失败时报的是目标文件，但真正原因
// 往往是某一级上级目录缺少 x 权限（例如安装目录父级是 0700）。
func firstUntraversableDir(path string) string {
	dir := filepath.Dir(path)
	var dirs []string
	for {
		dirs = append(dirs, dir)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	// 从最上层往下检查，报最靠近根的那一级，更接近真实原因
	for i := len(dirs) - 1; i >= 0; i-- {
		d := dirs[i]
		st, err := os.Stat(d)
		if err != nil {
			continue
		}
		// 用 stat + 访问检查：能 ListDir 说明可进入
		if _, err := os.ReadDir(d); err != nil {
			return d
		}
		if st.Mode().Perm()&0111 == 0 {
			return d
		}
	}
	return ""
}

// appEnviron 构造应用子进程的环境变量。
//
// 关键：**不能直接 os.Environ() + 覆盖**，必须剔除从 fpkm 自身继承来的
// 飞牛约定变量（TRIM_* / GATEWAY_*）。
//
// 真实故障（Lucky 3.0.2）：fpkm 是作为飞牛应用运行在 fn-deepseek-harness
// 容器里的，其进程环境带有该应用的飞牛变量：
//
//	TRIM_APPDEST=/vol2/@appcenter/fn-deepseek-harness
//	GATEWAY_SOCKET=/vol2/@appcenter/fn-deepseek-harness/app.sock
//
// fpkm 只设置了自己认识的 TRIM_* 项，却把整个 os.Environ() 原样传给子进程。
// Lucky 按飞牛约定使用继承来的 @appcenter 前缀定位 socket，于是把
// app.sock 建到 /vol2/@appcenter/Lucky/app.sock，而 fpkm 一直在
// {appDir}/target/app.sock 上等待 —— 表现为"等待 Socket 超时"，
// 但应用日志里明明是 "LuckyWeb Start success"。
//
// 因此这里先清空所有继承的 TRIM_*/GATEWAY_*，再追加 fpkm 自己算好的值，
// 保证应用看到的飞牛变量只来自 fpkm。
func appEnviron(env EnvMap) []string {
	inherited := os.Environ()
	out := make([]string, 0, len(inherited)+len(env))

	for _, kv := range inherited {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		if isFnosEnvName(name) {
			continue // 丢弃继承来的飞牛变量
		}
		out = append(out, kv)
	}

	// fpkm 计算的值放最后：即使某个键重复，后出现的会覆盖先出现的
	out = append(out, env.ToOsEnv()...)
	return out
}

// isFnosEnvName 判断是否为飞牛约定的环境变量名。
//
// 只匹配 fpkm 需要独占的命名空间（TRIM_*、GATEWAY_*），
// 避免误删用户自定义的无关变量。
func isFnosEnvName(name string) bool {
	return strings.HasPrefix(name, "TRIM_") || strings.HasPrefix(name, "GATEWAY_")
}
