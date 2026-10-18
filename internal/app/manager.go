package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"fpkm/internal/config"
	"fpkm/internal/fpk"
	"fpkm/internal/gateway"
	"fpkm/internal/logger"
)

// AppStatus 应用状态
type AppStatus struct {
	Name      string
	Version   string
	Running   bool
	PID       int
	Port      string
	EntryType string
}

// AppInfo 已安装应用信息
type AppInfo struct {
	Name        string
	Version     string
	DisplayName string
	Desc        string
	Platform    string
	HasUI       bool
	IsDocker    bool
	Dir         string
}

// Manager 应用管理器
type Manager struct {
	cfg *config.Config
}

// NewManager 创建应用管理器
func NewManager(cfg *config.Config) *Manager {
	return &Manager{cfg: cfg}
}

// Install 安装应用
func (m *Manager) Install(fpkPath string, setArgs []string) error {
	// 1. 校验包
	logger.Info("正在校验 FPK 包...")
	result, err := fpk.VerifyPackage(fpkPath)
	if err != nil {
		return fmt.Errorf("校验失败: %w", err)
	}
	if !result.Valid {
		return fmt.Errorf("包校验失败: %s", strings.Join(result.Errors, "; "))
	}
	logger.Info("校验通过 (文件数: %d, 大小: %d bytes)", result.FileCount, result.TotalSize)
	logger.Debug("SHA256: %s", result.Checksum)

	for _, w := range result.Warnings {
		logger.Warn("警告: %s", w)
	}

	// 2. 解压到临时目录获取 manifest
	tmpDir, err := os.MkdirTemp("", "fpkm-install-*")
	if err != nil {
		return fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	logger.Info("正在解压 FPK 包...")
	if err := fpk.Extract(fpkPath, tmpDir); err != nil {
		return fmt.Errorf("解压失败: %w", err)
	}

	// 3. 解析 manifest
	manifest, err := fpk.ParseManifestFromDir(tmpDir)
	if err != nil {
		return fmt.Errorf("解析manifest失败: %w", err)
	}

	appname := manifest.Appname
	appDir := m.cfg.AppDir(appname)

	// 4. 检查是否已安装
	if _, err := os.Stat(appDir); err == nil {
		return fmt.Errorf("应用 %s 已安装，请先卸载或使用 upgrade 命令升级", appname)
	}

	// 5. 创建应用目录结构
	logger.Info("正在安装应用 %s v%s...", appname, manifest.Version)
	for _, dir := range []string{
		appDir,
		m.cfg.AppTargetDir(appname),
		m.cfg.AppEtcDir(appname),
		m.cfg.AppVarDir(appname),
		m.cfg.AppTmpDir(appname),
		m.cfg.AppHomeDir(appname),
		m.cfg.AppSharesDir(appname),
		m.cfg.AppLogsDir(appname),
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("创建目录失败 %s: %w", dir, err)
		}
	}

	// 5.1 创建 @appshare 符号链接指向 shares（兼容飞牛原版应用的 @appshare 路径约定）
	// 许多飞牛应用在 cmd/main 和 install_callback 中使用 ${TRIM_APPDEST_VOL}/@appshare/${TRIM_APPNAME}
	// fpkm 映射为 shares 目录，通过符号链接让原版脚本无需修改即可工作
	appshareLink := filepath.Join(appDir, "@appshare")
	if _, err := os.Lstat(appshareLink); err != nil {
		if err := os.Symlink("shares", appshareLink); err != nil {
			logger.Debug("创建 @appshare 符号链接失败: %v", err)
		} else {
			logger.Debug("已创建 @appshare -> shares 符号链接")
		}
	}

	// 6. 分离安装：FPK根文件→app根目录，app/内容→target/
	targetDir := m.cfg.AppTargetDir(appname)
	appContentDir := filepath.Join(tmpDir, "app")
	if _, err := os.Stat(appContentDir); err == nil {
		// app/ 目录存在，将其内容移动到 target/
		if err := moveDirContents(appContentDir, targetDir); err != nil {
			return fmt.Errorf("移动app内容到target失败: %w", err)
		}
	}

	// 6.1 兼容性修复：在 target/ 下创建 app 符号链接指向自身
	// 飞牛原版 TRIM_APPDEST 指向 app 目录的父目录，脚本中用 $TRIM_APPDEST/app/xxx 访问
	// fpkm 的 TRIM_APPDEST 直接是 target/，需要创建 target/app → target/ 兼容
	appSymlink := filepath.Join(targetDir, "app")
	if _, err := os.Lstat(appSymlink); err != nil {
		// 不存在才创建（避免覆盖已有的真实 app 目录）
		if err := os.Symlink(targetDir, appSymlink); err == nil {
			logger.Debug("已创建兼容符号链接: target/app -> target/")
		}
	}

	// 将 FPK 根文件（manifest, cmd/, config/, wizard/, ICON.*）移动到 app 根目录
	rootFiles := []string{"manifest", "ICON.PNG", "ICON_256.PNG", "cmd", "config", "wizard"}
	for _, name := range rootFiles {
		src := filepath.Join(tmpDir, name)
		dst := filepath.Join(appDir, name)
		if _, err := os.Stat(src); err == nil {
			os.Rename(src, dst)
		}
	}

	// 6.1 重映射脚本中的硬编码路径
	// 飞牛原版应用的 cmd/main、install_callback、restart.sh 等脚本通常硬编码 /var/apps/{appname}
	// fpkm 安装到 ~/.local/fpk/apps/{appname}，需要将脚本中的硬编码路径替换为 fpkm 路径
	// 这样原版脚本无需修改即可在 fpkm 下运行
	m.remapScriptPaths(appDir, appname)

	// 6.2 创建系统级符号链接（兼容二进制中硬编码的飞牛原版路径）
	// 飞牛原版应用的二进制（如 tailscale 的 index.cgi）中硬编码了：
	//   /var/apps/{appname}/target/bin/tailscale
	//   /vol1/@appdata/{appname}/tailscaled.sock
	//   /vol1/@appcenter/{appname}/bin/tailscale
	// 这些路径无法通过 remapScriptPaths 修改（二进制文件不能随意改长度），
	// 只能通过创建符号链接让这些硬编码路径指向 fpkm 的实际安装路径
	m.createSystemSymlinks(appname, appDir)

	// 7. manifest 已在 app 根目录
	// 8. 处理 wizard（安装向导）
	envFile := m.cfg.AppEnvFile(appname)
	env, err := BuildEnv(m.cfg, manifest)
	if err != nil {
		return fmt.Errorf("构建环境变量失败: %w", err)
	}

	// 应用 --set 参数
	for _, set := range setArgs {
		parts := strings.SplitN(set, "=", 2)
		if len(parts) == 2 {
			env[parts[0]] = parts[1]
		}
	}

	// 处理 wizard 默认值
	m.applyWizardDefaults(appDir, env)

	// 从 ui/config 检测是否是网关应用（有 gatewaySocket 字段）
	// 网关应用通过 Unix Socket 提供服务，不监听 TCP 端口
	gatewaySocket := readGatewaySocketFromUIConfig(targetDir, appDir)
	if gatewaySocket != "" {
		logger.Info("检测到统一网关应用 (gatewaySocket: %s)", gatewaySocket)
		// 路由前缀与启动路径都从 ui/config 读取：
		//   gatewayPrefix → 网关路由匹配（不带尾斜杠）
		//   url           → 桌面打开地址（可能带尾斜杠/子路径/查询串）
		// 两者可能不同，例如 fnchat 的前缀是 /app/fnchat，打开地址是 /app/fnchat/
		gatewayPrefix := readGatewayPrefixFromUIConfig(targetDir, appDir)
		if gatewayPrefix == "" {
			gatewayPrefix = "/app/" + appname
		}
		launchPath := readLaunchPathFromUIConfig(targetDir, appDir)
		if launchPath == "" {
			launchPath = ensureTrailingSlash(gatewayPrefix)
		}
		socketPath := filepath.Join(targetDir, gatewaySocket)
		env["GATEWAY_PREFIX"] = gatewayPrefix
		env["LAUNCH_PATH"] = launchPath
		env["SOCKET_PATH"] = socketPath
		env["TRIM_SERVICE_SOCKET"] = socketPath
		// 清空端口变量（网关应用不使用 TCP 端口）
		env["TRIM_SERVICE_PORT"] = ""
		env["port"] = ""
		env["UI_PORT"] = ""
		logger.Debug("网关前缀: %s, 打开路径: %s", gatewayPrefix, launchPath)
	} else if env["TRIM_SERVICE_PORT"] == "" || env["TRIM_SERVICE_PORT"] == "8080" {
		// 非网关应用：从 ui/config 读取端口（未通过 --set 指定时）
		uiConfigPort := readPortFromUIConfig(targetDir, appDir)
		if uiConfigPort != "" {
			logger.Info("从 ui/config 读取到端口: %s", uiConfigPort)
			env["TRIM_SERVICE_PORT"] = uiConfigPort
			env["port"] = uiConfigPort
		}
	}

	// 检查依赖应用是否已安装
	if manifest.InstallDepApps != "" {
		for _, dep := range strings.Split(manifest.InstallDepApps, ",") {
			dep = strings.TrimSpace(dep)
			if dep == "" {
				continue
			}
			depDir := m.cfg.AppDir(dep)
			if _, err := os.Stat(depDir); err != nil {
				logger.Warn("依赖应用 %s 未安装，请先安装: fpkm install %s.fpk", dep, dep)
			} else {
				logger.Info("依赖应用 %s 已安装", dep)
			}
		}
	}

	// 同步端口变量到 TRIM_SERVICE_PORT（支持多种端口变量名）
	// 优先级: port > UIPORT > TRIM_SERVICE_PORT
	portVars := []string{"port", "UIPORT", "uiport"}
	for _, pv := range portVars {
		if port, ok := env[pv]; ok && port != "" {
			env["TRIM_SERVICE_PORT"] = port
			break
		}
	}
	// 同步 UI_PORT（EasyTier-Web 等应用从 UI_PORT 读取端口）
	if port, ok := env["TRIM_SERVICE_PORT"]; ok && port != "" {
		env["UI_PORT"] = port
		env["port"] = port
	}

	if err := SaveEnvFile(envFile, env); err != nil {
		return fmt.Errorf("保存环境变量失败: %w", err)
	}

	// 9. 处理生命周期脚本（执行 install_callback，忽略 root 脚本）
	if err := m.processLifecycleScripts(appDir, appname, env); err != nil {
		logger.Warn("生命周期脚本处理失败: %v", err)
	}

	logger.Success("应用 %s v%s 安装成功", appname, manifest.Version)
	logger.Info("安装路径: %s", appDir)
	if env["CGI_PATH"] != "" {
		logger.Info("应用类型: CGI 应用 (binary: %s)", env["CGI_PATH"])
		logger.Info("访问路径: http://localhost:%d%s", m.cfg.GatewayPort, launchPathOf(env))
	} else if env["GATEWAY_PREFIX"] != "" {
		logger.Info("应用类型: 统一网关应用 (socket: %s)", env["SOCKET_PATH"])
		// 显示 ui/config 的 url（用户实际打开的地址），而不是路由前缀
		logger.Info("访问路径: http://localhost:%d%s", m.cfg.GatewayPort, launchPathOf(env))
	} else if port := env["TRIM_SERVICE_PORT"]; port != "" {
		logger.Info("服务端口: %s", port)
	}

	// 处理工具型应用（如 nodejs_v22）
	m.handleToolApp(appname, targetDir)

	return nil
}

// handleToolApp 处理工具型应用
// 工具型应用不启动服务，而是提供可执行文件给其他应用使用
// 多版本共存：nodejs_v22 → node22/npm22；nodejs_v23 → node23/npm23
// python312 → python3/python3.12 + LD_LIBRARY_PATH 配置
// 应用在 cmd/main 中自行指定使用哪个版本的依赖
func (m *Manager) handleToolApp(appname, targetDir string) {
	if !isToolApp(appname) {
		return
	}

	logger.Info("检测到工具型应用 %s，正在配置...", appname)

	binDir := filepath.Join(targetDir, "bin")
	if _, err := os.Stat(binDir); err != nil {
		return
	}

	linkDir := m.cfg.AppLinksDir()
	if err := os.MkdirAll(linkDir, 0755); err != nil {
		logger.Warn("创建链接目录失败: %v", err)
		return
	}

	versionSuffix := extractVersionSuffix(appname)

	// Python 特殊处理：创建 python3 → python3.12 的符号链接
	if strings.HasPrefix(appname, "python") {
		m.setupPythonTool(appname, targetDir, binDir, linkDir)
		return
	}

	entries, err := os.ReadDir(binDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		src := filepath.Join(binDir, entry.Name())
		linkName := entry.Name()
		if versionSuffix != "" {
			linkName = entry.Name() + versionSuffix
		}
		linkPath := filepath.Join(linkDir, linkName)

		os.Remove(linkPath)
		if err := os.Symlink(src, linkPath); err != nil {
			logger.Warn("创建符号链接失败 %s: %v", linkName, err)
		} else {
			logger.Info("已创建符号链接: %s -> %s", linkPath, src)
		}
	}

	logger.Info("提示: 请将 %s 添加到 PATH 环境变量", linkDir)
}

// setupPythonTool 配置 Python 工具型应用
// 1. 创建 python3 → python3.12 的符号链接
// 2. 生成 LD_LIBRARY_PATH 配置（.env 文件）
// 3. 链接所有 bin/ 下的可执行文件
func (m *Manager) setupPythonTool(appname, targetDir, binDir, linkDir string) {
	libDir := filepath.Join(targetDir, "lib")

	entries, err := os.ReadDir(binDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		src := filepath.Join(binDir, entry.Name())
		linkPath := filepath.Join(linkDir, entry.Name())

		os.Remove(linkPath)
		if err := os.Symlink(src, linkPath); err != nil {
			logger.Warn("创建符号链接失败 %s: %v", entry.Name(), err)
		} else {
			logger.Info("已创建符号链接: %s -> %s", linkPath, src)
		}
	}

	// 创建 python3 → python3.X 的符号链接（如果不存在）
	python3Link := filepath.Join(linkDir, "python3")
	if _, err := os.Lstat(python3Link); err != nil {
		// 查找 python3.x 格式的二进制
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "python3.") && !strings.HasSuffix(entry.Name(), "-config") {
				src := filepath.Join(binDir, entry.Name())
				os.Remove(python3Link)
				if err := os.Symlink(src, python3Link); err != nil {
					logger.Warn("创建 python3 符号链接失败: %v", err)
				} else {
					logger.Info("已创建符号链接: %s -> %s", python3Link, src)
				}
				break
			}
		}
	}

	// 生成环境配置文件（供其他应用 source 使用）
	envFile := filepath.Join(m.cfg.AppDir(appname), "env.sh")
	envContent := fmt.Sprintf(`# Python %s 环境配置
export PATH="%s:$PATH"
export LD_LIBRARY_PATH="%s:$LD_LIBRARY_PATH"
`, appname, binDir, libDir)
	if err := os.WriteFile(envFile, []byte(envContent), 0644); err != nil {
		logger.Warn("写入环境配置文件失败: %v", err)
	} else {
		logger.Info("已生成环境配置: %s", envFile)
	}

	logger.Info("提示: 请将 %s 添加到 PATH 环境变量", linkDir)
	logger.Info("提示: 如需动态库支持，请 source %s", envFile)
}

// extractVersionSuffix 从应用名提取版本后缀
// nodejs_v22 → 22, nodejs_v23 → 23, python3 → 3
func extractVersionSuffix(appname string) string {
	if idx := strings.Index(appname, "_v"); idx >= 0 {
		suffix := appname[idx+2:]
		var num string
		for _, c := range suffix {
			if c >= '0' && c <= '9' {
				num += string(c)
			} else {
				break
			}
		}
		return num
	}
	if strings.HasPrefix(appname, "python") {
		rest := strings.TrimPrefix(appname, "python")
		if rest != "" {
			return rest
		}
	}
	return ""
}

// isToolApp 判断是否是工具型应用
func isToolApp(appname string) bool {
	toolApps := []string{"nodejs", "python", "docker"}
	for _, name := range toolApps {
		if strings.Contains(appname, name) {
			return true
		}
	}
	return false
}

// Uninstall 卸载应用
func (m *Manager) Uninstall(appname string, values map[string]string) error {
	appDir := m.cfg.AppDir(appname)
	if _, err := os.Stat(appDir); err != nil {
		return fmt.Errorf("应用 %s 未安装", appname)
	}

	// 1. 停止应用
	logger.Info("正在停止应用 %s...", appname)
	pm := NewProcessManager(m.cfg.AppPidFile(appname), m.cfg.AppLogFile(appname))
	pm.Stop()

	// 2. 清理工具型应用的符号链接
	if isToolApp(appname) {
		targetDir := m.cfg.AppTargetDir(appname)
		binDir := filepath.Join(targetDir, "bin")
		if _, err := os.Stat(binDir); err == nil {
			linkDir := m.cfg.AppLinksDir()
			versionSuffix := extractVersionSuffix(appname)
			entries, _ := os.ReadDir(binDir)
			for _, entry := range entries {
				if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
					continue
				}
				linkName := entry.Name()
				if versionSuffix != "" {
					linkName = entry.Name() + versionSuffix
				}
				linkPath := filepath.Join(linkDir, linkName)
				if _, err := os.Lstat(linkPath); err == nil {
					os.Remove(linkPath)
					logger.Info("已删除符号链接: %s", linkPath)
				}
			}
		}
	}

	// 3. 执行卸载回调脚本（若有），并把卸载向导收集到的值作为环境变量传入。
	//    许多应用依赖 uninstall_callback 清理数据库、桌面图标或系统配置；
	//    向导里的 wizard_delete_data 之类的字段决定是否保留用户数据。
	if err := m.runUninstallCallback(appname, appDir, values); err != nil {
		// 回调失败不阻断卸载：否则用户将无法移除应用
		logger.Warn("卸载回调脚本执行失败: %v", err)
	}

	// 4. 删除目录
	logger.Info("正在卸载应用 %s...", appname)
	if err := os.RemoveAll(appDir); err != nil {
		return fmt.Errorf("删除目录失败: %w", err)
	}

	// 5. 清理系统级符号链接
	m.removeSystemSymlinks(appname)

	logger.Success("应用 %s 已卸载", appname)
	return nil
}

// runUninstallCallback 执行 cmd/uninstall_callback，并把向导值注入环境变量。
//
// 与 install_callback 一致：需要 root 的脚本会被跳过（IsRootScript），
// 因为我们以普通用户身份运行，执行只会失败并留下噪音日志。
func (m *Manager) runUninstallCallback(appname, appDir string, values map[string]string) error {
	script := filepath.Join(appDir, "cmd", "uninstall_callback")
	if !fileExists(script) {
		logger.Debug("未找到 uninstall_callback 脚本")
		return nil
	}
	if IsRootScript(script) {
		logger.Info("跳过需要 Root 权限的 uninstall_callback 脚本")
		return nil
	}

	logger.Info("执行卸载回调脚本: uninstall_callback")
	_ = os.Chmod(script, 0755)

	// 复用安装时的环境（含 TRIM_* 路径），并叠加卸载向导值
	env, err := LoadEnvFile(m.cfg.AppEnvFile(appname))
	if err != nil || env == nil {
		env = EnvMap{}
	}
	for k, v := range values {
		env[k] = v
		// 向导字段同时以大写形式导出，兼容部分脚本的写法差异
		env[strings.ToUpper(k)] = v
	}
	env["TRIM_APP_STATUS"] = "STOP"

	cmd := exec.Command("bash", script)
	cmd.Dir = appDir
	cmd.Env = append(os.Environ(), env.ToOsEnv()...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("uninstall_callback 执行失败: %w", err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if line != "" {
			logger.Info("  [uninstall] %s", line)
		}
	}
	return nil
}

// Upgrade 升级应用
func (m *Manager) Upgrade(fpkPath string, setArgs []string) error {
	// 1. 解压获取 manifest
	tmpDir, err := os.MkdirTemp("", "fpkm-upgrade-*")
	if err != nil {
		return fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	logger.Info("正在解压新版本 FPK 包...")
	if err := fpk.Extract(fpkPath, tmpDir); err != nil {
		return fmt.Errorf("解压失败: %w", err)
	}

	manifest, err := fpk.ParseManifestFromDir(tmpDir)
	if err != nil {
		return fmt.Errorf("解析manifest失败: %w", err)
	}

	appname := manifest.Appname
	appDir := m.cfg.AppDir(appname)

	// 2. 检查是否已安装
	oldManifest, err := fpk.ParseManifest(m.cfg.AppManifestFile(appname))
	if err != nil {
		return fmt.Errorf("应用 %s 未安装，请使用 install 命令", appname)
	}

	logger.Info("升级 %s: %s -> %s", appname, oldManifest.Version, manifest.Version)

	// 3. 停止应用
	pm := NewProcessManager(m.cfg.AppPidFile(appname), m.cfg.AppLogFile(appname))
	pm.Stop()

	// 4. 备份旧版本
	backupDir := appDir + ".backup"
	os.RemoveAll(backupDir)
	if err := os.Rename(appDir, backupDir); err != nil {
		return fmt.Errorf("备份失败: %w", err)
	}

	// 5. 安装新版本
	if err := m.Install(fpkPath, setArgs); err != nil {
		// 安装失败，恢复备份
		logger.Error("升级失败，正在恢复旧版本...")
		os.RemoveAll(appDir)
		os.Rename(backupDir, appDir)
		return err
	}

	// 6. 迁移数据（etc, var 目录）
	for _, dir := range []string{"etc", "var", "home"} {
		src := filepath.Join(backupDir, dir)
		dst := filepath.Join(appDir, dir)
		if _, err := os.Stat(src); err == nil {
			moveDirContents(src, dst)
		}
	}

	// 7. 清理备份
	os.RemoveAll(backupDir)

	logger.Success("应用 %s 升级成功", appname)
	return nil
}

// Start 启动应用
func (m *Manager) Start(appname string) error {
	appDir := m.cfg.AppDir(appname)
	if _, err := os.Stat(appDir); err != nil {
		return fmt.Errorf("应用 %s 未安装", appname)
	}

	// 已在运行则直接返回：启动是幂等操作。
	// 否则重复调用会拉起第二个进程，覆盖 PID 文件后旧进程失控
	// （FPKM 重启后恢复自启动时会命中这条路径）。
	if pm := NewProcessManager(m.cfg.AppPidFile(appname), m.cfg.AppLogFile(appname)); pm.IsRunning() {
		logger.Info("应用 %s 已在运行，跳过启动", appname)
		return nil
	}

	// 读取环境变量
	env, err := LoadEnvFile(m.cfg.AppEnvFile(appname))
	if err != nil {
		return fmt.Errorf("读取环境变量失败: %w", err)
	}

	// 确保系统符号链接存在（兼容二进制中硬编码的飞牛原版路径）
	// 每次 start 时检查，确保符号链接存在（用户可能手动删除过，或旧版本安装时未创建）
	m.createSystemSymlinks(appname, appDir)

	// 统一网关应用：始终以 ui/config 为准，补全/纠正路由前缀与打开路径。
	// 这样即使 env 文件来自旧版本安装（当时可能没有 LAUNCH_PATH，
	// 或前缀写成了 /app/{appname}），start 时也会自动修正。
	// 注意 gatewayPrefix 与 url 是两回事：
	//   fnchat: gatewayPrefix=/app/fnchat, url=/app/fnchat/  → 打开要带尾斜杠
	// 注意外层条件不能是 env["SOCKET_PATH"] != ""。
	// 旧版本的 .env 可能残留了错误的 SOCKET_PATH（例如把端口型应用
	// Lucky 误标成网关应用），而 ui/config 里根本没有 gatewaySocket。
	// 若以外层 != "" 为条件，就永远进不来、也永远清不掉，
	// 启动时会一直等一个永远不会出现的 app.sock 直到超时。
	{
		targetDirCheck := m.cfg.AppTargetDir(appname)
		gatewaySocket := readGatewaySocketFromUIConfig(targetDirCheck, appDir)
		if gatewaySocket == "" {
			// ui/config 明确不是网关应用：清理旧版本可能写错的网关字段。
			if env["SOCKET_PATH"] != "" || env["GATEWAY_PREFIX"] != "" || env["TRIM_SERVICE_SOCKET"] != "" {
				logger.Info("ui/config 未声明 gatewaySocket，清理残留的网关配置（应用类型为端口型）")
				env["SOCKET_PATH"] = ""
				env["GATEWAY_PREFIX"] = ""
				env["TRIM_SERVICE_SOCKET"] = ""
				env["LAUNCH_PATH"] = ""
				if env["TRIM_SERVICE_PORT"] == "" {
					if p := readPortFromUIConfig(targetDirCheck, appDir); p != "" {
						env["TRIM_SERVICE_PORT"] = p
						env["port"] = p
					}
				}
				SaveEnvFile(m.cfg.AppEnvFile(appname), env)
			}
		} else {
			wantPrefix := readGatewayPrefixFromUIConfig(targetDirCheck, appDir)
			if wantPrefix == "" {
				wantPrefix = "/app/" + appname
			}
			wantLaunch := readLaunchPathFromUIConfig(targetDirCheck, appDir)
			if wantLaunch == "" {
				wantLaunch = ensureTrailingSlash(wantPrefix)
			}
			wantSocket := filepath.Join(targetDirCheck, gatewaySocket)

			changed := false
			if env["GATEWAY_PREFIX"] != wantPrefix {
				logger.Info("修正网关前缀: %s -> %s (以 ui/config 为准)", env["GATEWAY_PREFIX"], wantPrefix)
				env["GATEWAY_PREFIX"] = wantPrefix
				changed = true
			}
			if env["LAUNCH_PATH"] != wantLaunch {
				logger.Info("修正打开路径: %s -> %s (以 ui/config 为准)", env["LAUNCH_PATH"], wantLaunch)
				env["LAUNCH_PATH"] = wantLaunch
				changed = true
			}
			if env["SOCKET_PATH"] != wantSocket {
				env["SOCKET_PATH"] = wantSocket
				env["TRIM_SERVICE_SOCKET"] = wantSocket
				changed = true
			}

			// 网关应用**不能带端口**。
			//
			// 真实故障：Lucky 3.0.2 是网关应用（ui/config 有 gatewaySocket），
			// 但旧版本安装时 .env 里留下了 TRIM_SERVICE_PORT=16601。
			// 应用启动时读到端口就以 TCP 模式运行、根本不去创建 unix socket，
			// fpkm 却在 target/app.sock 上空等 30 秒后报 "等待 Socket 超时"。
			// 端口字段的清空只在 install 路径做过，start 的修正逻辑漏了这一步，
			// 于是升级上来的应用会一直失败。
			for _, k := range []string{"TRIM_SERVICE_PORT", "port", "UI_PORT"} {
				if env[k] != "" {
					logger.Info("网关应用不应带端口，已清理 %s=%s (以 ui/config 为准)", k, env[k])
					env[k] = ""
					changed = true
				}
			}

			if changed {
				SaveEnvFile(m.cfg.AppEnvFile(appname), env)
			}
		}
	}

	// CGI 应用：始终以 ui/config 的 url 参数为准
	// 这样即使 env 文件是旧版本（如 prefix 不含 index.cgi），start 时也能自动修正
	// 原因：tailscale 前端通过 window.location.pathname 中的 .cgi 推断 API 基础路径，
	//       prefix 必须与 ui/config 的 url 完全一致
	if env["CGI_PATH"] != "" {
		targetDirCheck := m.cfg.AppTargetDir(appname)
		if cgiPath := readCGIPathFromUIConfig(targetDirCheck, appDir); cgiPath != "" {
			if env["GATEWAY_PREFIX"] != cgiPath {
				logger.Info("更新 CGI 网关前缀: %s -> %s (以 ui/config 为准)", env["GATEWAY_PREFIX"], cgiPath)
				env["GATEWAY_PREFIX"] = cgiPath
				SaveEnvFile(m.cfg.AppEnvFile(appname), env)
			}
		}

		// 确保 TS_SOCKET 环境变量存在（让 index.cgi 能找到 tailscaled socket）
		// tailscale 的 index.cgi 二进制硬编码了 /var/apps/tailscale/var/tailscaled.sock，
		// 但 fpkm 安装路径不同。TS_SOCKET 环境变量可以覆盖硬编码路径。
		// 对于旧版本安装的应用，env 文件可能没有 TS_SOCKET，这里补上。
		expectedTSSocket := filepath.Join(appDir, "var", "tailscaled.sock")
		if env["TS_SOCKET"] != expectedTSSocket {
			logger.Info("更新 TS_SOCKET: %s -> %s", env["TS_SOCKET"], expectedTSSocket)
			env["TS_SOCKET"] = expectedTSSocket
			SaveEnvFile(m.cfg.AppEnvFile(appname), env)
		}
	}

	// 探测入口
	targetDir := m.cfg.AppTargetDir(appname)
	entry, err := DetectEntryWithEnv(targetDir, env)
	if err != nil {
		return fmt.Errorf("探测入口失败: %w", err)
	}

	logger.Info("应用类型: %s", entry.Type)

	// Docker 应用特殊处理
	if entry.Type == EntryDocker {
		return fmt.Errorf("检测到Docker应用，请安装Rootless Docker后再试")
	}

	// 二进制兼容性检查（仅检查实际文件路径，跳过 PATH 命令如 node/python）
	if entry.BinaryPath != "" && fileExists(entry.BinaryPath) {
		compat, reason := CheckBinaryCompat(entry.BinaryPath)
		if !compat {
			return fmt.Errorf("二进制不兼容: %s\n  文件: %s\n  当前系统: %s/%s\n  建议: 在 Linux 系统上运行此应用，或使用 WSL/Docker",
				reason, entry.BinaryPath, runtime.GOOS, runtime.GOARCH)
		}
		logger.Info("二进制兼容性: %s", reason)
	}

	// Node.js 应用需要先安装依赖
	if entry.NeedsInstall {
		logger.Info("正在安装依赖...")
		if entry.Type == EntryNodeJS {
			npmCmd := "npm"
			if _, err := exec.LookPath("npm"); err != nil {
				return fmt.Errorf("未找到 npm，请先安装 Node.js")
			}
			cmd := exec.Command(npmCmd, "install", "--omit=dev")
			cmd.Dir = entry.WorkDir
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("安装依赖失败: %w", err)
			}
		}
	}

	// 创建工作目录
	if entry.WorkDir != "" {
		os.MkdirAll(entry.WorkDir, 0755)
	}

	// 为 cmd/main 类型应用创建数据目录和配置
	if entry.Type == EntryScript || entry.Type == EntryBinary {
		dataDir := filepath.Join(appDir, "var", "data")
		os.MkdirAll(dataDir, 0755)
		// 如果有端口配置，创建最小化配置文件
		port := env["TRIM_SERVICE_PORT"]
		if port != "" {
			configFile := filepath.Join(dataDir, "config.json")
			if _, err := os.Stat(configFile); err != nil {
				configContent := fmt.Sprintf(`{
    "port": %s,
    "scheme": {
        "address": "0.0.0.0",
        "http_port": %s,
        "https_port": -1,
        "force_https": false
    }
}`, port, port)
				os.WriteFile(configFile, []byte(configContent), 0644)
			}
		}
	}

	// 端口冲突检测与自动分配
	portStr := env["TRIM_SERVICE_PORT"]
	if portStr != "" {
		var port int
		fmt.Sscanf(portStr, "%d", &port)
		if isPortInUse(port) {
			// 判断占用者是否就是本应用的另一个实例（同 appname 的进程）
			// —— 这种情况下换端口没有意义，反而会造成两个实例并存。
			if holder := m.describePortHolder(port, appname); holder != "" {
				logger.Warn("端口 %d 已被占用（%s）", port, holder)
			}
			newPort := FindAvailablePort(port + 1)
			logger.Warn("端口 %d 被占用，自动切换到 %d", port, newPort)
			env["TRIM_SERVICE_PORT"] = fmt.Sprintf("%d", newPort)
			env["port"] = fmt.Sprintf("%d", newPort)
			SaveEnvFile(m.cfg.AppEnvFile(appname), env)
		}
	}

	// 同步端口环境变量（不同应用使用不同的端口变量名）
	if port, ok := env["TRIM_SERVICE_PORT"]; ok && port != "" {
		env["port"] = port
		env["UI_PORT"] = port
	}

	// 合并 CMD 中提取的环境变量（如 PORT=3388）
	if entry.CmdEnv != nil {
		for k, v := range entry.CmdEnv {
			env[k] = v
			logger.Debug("CMD 环境变量: %s=%s", k, v)
		}
		// 如果 CMD 中有 PORT，同步到 TRIM_SERVICE_PORT
		if port, ok := entry.CmdEnv["PORT"]; ok && port != "" {
			env["TRIM_SERVICE_PORT"] = port
			env["port"] = port
		}
	}

	// 修复 PATH：将飞牛路径 /var/apps/ 替换为 fpkm 路径
	m.fixPathEnv(env)

	// 用环境变量补全 entry 中剩余的 ${VAR} / $VAR 引用
	resolveEntryEnvVars(entry, env)

	// 准备 @appshare 数据目录（部分应用如 EasyTier-Web 需要将数据文件复制到 @appshare）
	m.prepareAppShare(appDir, appname, targetDir)

	// 执行预启动命令链（初始化数据库、设置密码等）
	logger.Debug("预启动命令数量: %d", len(entry.PreStartCmds))
	for i, preCmd := range entry.PreStartCmds {
		logger.Debug("预启动命令[%d]: %s %s (RunOnce=%v, FlagFile=%s, Desc=%s)",
			i, preCmd.Command, strings.Join(preCmd.Args, " "), preCmd.RunOnce, preCmd.FlagFile, preCmd.Desc)

		// RunOnce 命令：检查标记文件，已执行则跳过
		if preCmd.RunOnce && preCmd.FlagFile != "" {
			if _, err := os.Stat(preCmd.FlagFile); err == nil {
				logger.Info("跳过已执行的初始化命令: %s", preCmd.FlagFile)
				continue
			}
		}

		// 显示完整命令
		fullCmd := preCmd.Command + " " + strings.Join(preCmd.Args, " ")
		logger.Info("执行预启动命令: %s", fullCmd)
		logger.Debug("工作目录: %s", entry.WorkDir)

		// 带超时执行：预启动命令若被误解析成常驻进程（例如参数错乱后
		// 直接启动了主程序），无超时会让整个启动流程永久卡住。
		preCtx, preCancel := context.WithTimeout(context.Background(), preStartTimeout)
		initCmd := exec.CommandContext(preCtx, preCmd.Command, preCmd.Args...)
		initCmd.Dir = entry.WorkDir
		initCmd.Env = append(os.Environ(), env.ToOsEnv()...)
		output, err := initCmd.CombinedOutput()
		preCancel()
		if preCtx.Err() == context.DeadlineExceeded {
			logger.Warn("预启动命令超时（%s），已终止: %s", preStartTimeout, fullCmd)
		}
		if err != nil {
			logger.Warn("预启动命令执行失败: %v", err)
			if len(output) > 0 {
				logger.Warn("输出: %s", string(output))
			}
			// 预启动命令失败不中断启动流程（某些初始化可能已部分完成）
		} else {
			if len(output) > 0 {
				logger.Info("预启动输出: %s", string(output))
			} else {
				logger.Info("预启动命令执行成功")
			}
			// RunOnce 命令成功后创建标记文件
			if preCmd.RunOnce && preCmd.FlagFile != "" {
				os.MkdirAll(filepath.Dir(preCmd.FlagFile), 0755)
				if err := os.WriteFile(preCmd.FlagFile, []byte{}, 0644); err != nil {
					logger.Warn("创建标记文件失败: %v", err)
				} else {
					logger.Debug("已创建标记文件: %s", preCmd.FlagFile)
				}
			}
		}
	}

	// 启动进程
	pm := NewProcessManager(m.cfg.AppPidFile(appname), m.cfg.AppLogFile(appname))
	if err := pm.Start(entry, env); err != nil {
		return fmt.Errorf("启动失败: %w", err)
	}

	port := env["TRIM_SERVICE_PORT"]
	if port != "" {
		logger.Success("应用 %s 已启动 (PID: %d, 端口: %s)", appname, pm.GetPID(), port)
	} else {
		logger.Success("应用 %s 已启动 (PID: %d)", appname, pm.GetPID())
	}

	// Socket 应用：先等 socket 就绪，再注册到统一网关。
	// 若应用启动失败（如端口被占用导致进程退出），这里会给出明确原因，
	// 而不是等用户打开页面时才看到"Socket 不存在（应用可能未启动）"。
	socketPath := env["SOCKET_PATH"]
	if socketPath != "" && env["GATEWAY_PREFIX"] != "" {
		if err := m.waitForSocket(appname, socketPath, pm); err != nil {
			return err
		}
		m.registerToGateway(appname, env["GATEWAY_PREFIX"], socketPath)
	}

	// CGI 应用：注册 CGI 路由到统一网关
	cgiPath := env["CGI_PATH"]
	if cgiPath != "" && env["GATEWAY_PREFIX"] != "" {
		m.registerCGIToGateway(appname, env["GATEWAY_PREFIX"], cgiPath)
	}

	return nil
}

// waitForSocket 等待应用创建 Unix Socket，并确认它稳定可用。
//
// 统一网关应用（gatewaySocket）需要应用自己监听 socket，进程启动到监听
// 之间有间隔；此外应用可能"先建 socket 再崩溃"（典型如 uvicorn：先绑定
// unix socket，随后因 TCP 端口被占用而启动失败并清理掉 socket）。
// 两者最终都表现为网关返回"Socket 不存在（应用可能未启动）"，很难排查，
// 因此这里主动等待并诊断：
//   - 期间进程退出 → 读取应用日志中的真实原因
//   - socket 出现后又消失 → 视为启动失败
//   - 超时仍未出现 → 返回超时说明
func (m *Manager) waitForSocket(appname, socketPath string, pm *ProcessManager) error {
	const (
		timeout  = 30 * time.Second
		interval = 300 * time.Millisecond
		// settle 是 socket 出现后的观察窗口：必须在此期间保持
		// "socket 存在且进程存活"，才算真正启动成功。
		settle = 1500 * time.Millisecond
	)

	deadline := time.Now().Add(timeout)
	sawSocket := false
	var socketSeenAt time.Time

	for time.Now().Before(deadline) {
		exists := fileExists(socketPath)
		alive := pm.IsRunning()

		if exists && !sawSocket {
			sawSocket = true
			socketSeenAt = time.Now()
			logger.Debug("检测到 Socket，等待其稳定: %s", socketPath)
		}

		// 进程已退出：无论 socket 是否出现过，都算启动失败
		if !alive {
			if detail := m.appLogTail(appname, 12); detail != "" {
				return fmt.Errorf("应用 %s 启动后立即退出，未能提供可用的 Socket %s\n原因（摘自日志）:\n%s",
					appname, socketPath, detail)
			}
			return fmt.Errorf("应用 %s 启动后立即退出，未能创建 Socket %s（详见日志: %s）",
				appname, socketPath, m.cfg.AppLogFile(appname))
		}

		// socket 曾出现但已消失，说明应用启动过程中失败并清理了 socket
		if sawSocket && !exists {
			if detail := m.appLogTail(appname, 12); detail != "" {
				return fmt.Errorf("应用 %s 创建了 Socket %s 后又异常退出\n原因（摘自日志）:\n%s",
					appname, socketPath, detail)
			}
			return fmt.Errorf("应用 %s 创建了 Socket %s 后又异常退出（详见日志: %s）",
				appname, socketPath, m.cfg.AppLogFile(appname))
		}

		// socket 存在且进程存活，且已过观察窗口 → 认为稳定
		if exists && alive && time.Since(socketSeenAt) >= settle {
			logger.Debug("Socket 已就绪: %s", socketPath)
			return nil
		}

		time.Sleep(interval)
	}

	detail := m.appLogTail(appname, 12)
	if detail != "" {
		return fmt.Errorf("等待应用 %s 创建 Socket 超时（%s）\n最近的日志:\n%s", appname, socketPath, detail)
	}
	return fmt.Errorf("等待应用 %s 创建 Socket 超时（%s），请检查日志: %s",
		appname, socketPath, m.cfg.AppLogFile(appname))
}

// readAppLog 读取应用日志内容。
//
// fpkm 自身把子进程 stdout/stderr 写到 logs/app.log，但不少飞牛应用
// （如便签 note）在自己的 cmd/main 里把日志写到 ${TRIM_PKGVAR}/info.log，
// 于是 logs/app.log 始终为空 —— 诊断时若只读前者就什么都看不到。
// 这里按优先级依次尝试常见位置，取第一个非空文件。
func (m *Manager) readAppLog(appname string) []byte {
	appDir := m.cfg.AppDir(appname)
	candidates := []string{
		m.cfg.AppLogFile(appname),                  // logs/app.log（fpkm 转发）
		filepath.Join(appDir, "logs", "error.log"), // logs/error.log
		filepath.Join(appDir, "var", "info.log"),   // ${TRIM_PKGVAR}/info.log（note 等）
		filepath.Join(appDir, "var", "app.log"),    // ${TRIM_PKGVAR}/app.log
		filepath.Join(appDir, "var", "error.log"),  // ${TRIM_PKGVAR}/error.log
	}

	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err == nil && len(data) > 0 {
			return data
		}
	}

	// 兜底：logs/ 或 var/ 下任意 .log 文件中最新修改的一个
	for _, dir := range []string{
		filepath.Join(appDir, "logs"),
		filepath.Join(appDir, "var"),
	} {
		if p := newestLogFile(dir); p != "" {
			if data, err := os.ReadFile(p); err == nil && len(data) > 0 {
				return data
			}
		}
	}
	return nil
}

// newestLogFile 返回目录下最近修改的 .log 文件路径（无则空）
func newestLogFile(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var best string
	var bestTime int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".log") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if t := info.ModTime().UnixNano(); t > bestTime {
			bestTime = t
			best = filepath.Join(dir, e.Name())
		}
	}
	return best
}

// appLogTail 读取应用日志尾部用于诊断。
// 优先挑出"真正说明原因"的行（端口占用、权限、缺失依赖等），
// 否则退回最后若干行 —— 单纯给 traceback 尾巴对用户帮助不大。
func (m *Manager) appLogTail(appname string, maxLines int) string {
	data := m.readAppLog(appname)
	if len(data) == 0 {
		return ""
	}

	// 这些模式通常直接指向失败原因
	causePatterns := []string{
		"address already in use",
		"permission denied",
		"no such file or directory",
		"command not found",
		"module not found",
		"modulenotfounderror",
		"importerror",
		"errno 98",
		"errno 13",
		"cannot execute",
		"exec format error",
		"error:",
	}

	var causes []string
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		for _, p := range causePatterns {
			if strings.Contains(lower, p) {
				if len(causes) == 0 || causes[len(causes)-1] != trimmed {
					causes = append(causes, trimmed)
				}
				break
			}
		}
	}
	if len(causes) > 0 {
		if len(causes) > 5 {
			causes = causes[:5]
		}
		return strings.Join(causes, "\n")
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n")
}

// Stop 停止应用
func (m *Manager) Stop(appname string) error {
	appDir := m.cfg.AppDir(appname)
	if _, err := os.Stat(appDir); err != nil {
		return fmt.Errorf("应用 %s 未安装", appname)
	}

	// 读取环境变量，判断是否是网关应用
	// 注意：frpc 等应用的 socket 信息在 ui/config 中（不在 manifest），
	// 安装时已写入 env 文件的 GATEWAY_PREFIX/SOCKET_PATH/CGI_PATH 字段
	env, _ := LoadEnvFile(m.cfg.AppEnvFile(appname))
	isGatewayApp := env != nil && (env["GATEWAY_PREFIX"] != "" || env["SOCKET_PATH"] != "" || env["CGI_PATH"] != "")

	// 兜底：如果 env 未识别但 manifest 声明了 service_socket，也按网关应用处理
	manifest, _ := fpk.ParseManifest(m.cfg.AppManifestFile(appname))
	if !isGatewayApp && manifest != nil && manifest.IsSocketApp() {
		isGatewayApp = true
	}

	// 从网关移除路由（如果是 socket 或 CGI 应用）
	if isGatewayApp && env != nil {
		prefix := env["GATEWAY_PREFIX"]
		if prefix == "" {
			prefix = "/app/" + appname
		}
		client := gateway.NewClient(m.cfg)
		client.UnregisterRoute(prefix)
	}

	// 网关应用（bash 脚本类型）：执行 bash cmd/main stop
	// 这类脚本会自己管理 PID 文件和子进程，需要通过 stop 命令优雅停止
	if isGatewayApp {
		cmdMain := filepath.Join(appDir, "cmd", "main")
		if fileExists(cmdMain) && isFullBashScript(cmdMain) {
			logger.Info("执行停止脚本: bash %s stop", cmdMain)
			if env == nil {
				env = EnvMap{}
			}
			stopCmd := exec.Command("bash", cmdMain, "stop")
			stopCmd.Dir = m.cfg.AppTargetDir(appname)
			stopCmd.Env = append(os.Environ(), env.ToOsEnv()...)
			if output, err := stopCmd.CombinedOutput(); err != nil {
				logger.Warn("停止脚本执行失败: %v", err)
				if len(output) > 0 {
					logger.Debug("输出: %s", string(output))
				}
			}

			// 双保险：通过 fpkm 的 PID 文件杀死进程
			// 原因：fpkm 启动 tailscaled 时直接启动二进制，PID 写入 fpkm 的 PID 文件
			// 但 cmd/main stop 读取的是 ${TRIM_PKGVAR}/tailscale.pid，找不到 PID
			// 导致旧进程不被杀死，占用 TUN 设备，新进程无法启动
			pm := NewProcessManager(m.cfg.AppPidFile(appname), m.cfg.AppLogFile(appname))
			if pm.IsRunning() {
				logger.Info("通过 PID 文件终止残留进程 (PID: %d)", pm.readPID())
				pm.Stop()
			}

			// 清理 PID 文件
			os.Remove(m.cfg.AppPidFile(appname))
			logger.Success("应用 %s 已停止", appname)
			return nil
		}
	}

	pm := NewProcessManager(m.cfg.AppPidFile(appname), m.cfg.AppLogFile(appname))
	if !pm.IsRunning() {
		logger.Info("应用 %s 未在运行", appname)
		return nil
	}

	logger.Info("正在停止应用 %s...", appname)
	if err := pm.Stop(); err != nil {
		return fmt.Errorf("停止失败: %w", err)
	}

	logger.Success("应用 %s 已停止", appname)
	return nil
}

// Status 查看应用状态
func (m *Manager) Status(appname string) (*AppStatus, error) {
	appDir := m.cfg.AppDir(appname)
	if _, err := os.Stat(appDir); err != nil {
		return nil, fmt.Errorf("应用 %s 未安装", appname)
	}

	manifest, err := fpk.ParseManifest(m.cfg.AppManifestFile(appname))
	if err != nil {
		return nil, err
	}

	env, _ := LoadEnvFile(m.cfg.AppEnvFile(appname))
	pm := NewProcessManager(m.cfg.AppPidFile(appname), m.cfg.AppLogFile(appname))

	status := &AppStatus{
		Name:    appname,
		Version: manifest.Version,
		Running: pm.IsRunning(),
		PID:     pm.GetPID(),
		Port:    env["TRIM_SERVICE_PORT"],
	}

	if entry, err := DetectEntry(m.cfg.AppTargetDir(appname)); err == nil {
		status.EntryType = entry.Type.String()
	}

	return status, nil
}

// List 列出所有已安装应用
func (m *Manager) List() ([]AppInfo, error) {
	entries, err := os.ReadDir(m.cfg.AppsDir)
	if err != nil {
		return nil, err
	}

	var apps []AppInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		manifestPath := m.cfg.AppManifestFile(name)
		manifest, err := fpk.ParseManifest(manifestPath)
		if err != nil {
			continue
		}

		info := AppInfo{
			Name:        name,
			Version:     manifest.Version,
			DisplayName: manifest.DisplayName,
			Desc:        manifest.Desc,
			Platform:    manifest.Platform,
			Dir:         m.cfg.AppDir(name),
			IsDocker:    manifest.IsDockerApp(),
		}

		// 检查是否有 UI
		uiDir := filepath.Join(m.cfg.AppTargetDir(name), manifest.DesktopUidir)
		if _, err := os.Stat(uiDir); err == nil {
			info.HasUI = true
		}

		apps = append(apps, info)
	}

	return apps, nil
}

// applyWizardDefaults 把向导字段的默认值写入环境变量。
//
// 说明：这是"未经用户交互"时的兜底。通过 Web 界面安装时，
// 用户填写的值会通过 --set 传入并覆盖这里的默认值。
// 支持 i18n 占位符解析，因此解析前先做一次文案替换。
func (m *Manager) applyWizardDefaults(appDir string, env EnvMap) {
	steps, err := LoadWizard(appDir, WizardInstall)
	if err != nil {
		logger.Debug("读取安装向导失败: %v", err)
		return
	}
	if len(steps) == 0 {
		return
	}
	steps = ResolveWizardI18n(appDir, WizardInstall, steps)

	for field, val := range WizardDefaults(steps) {
		if _, exists := env[field]; exists {
			continue // 用户/--set 已提供，优先保留
		}
		if val != "" {
			env[field] = val
		}
		logger.Debug("向导默认值: %s=%s", field, val)
	}
}

// processLifecycleScripts 处理生命周期脚本
// 执行 install_callback（如果不是 root 脚本），跳过其他脚本
// install_callback 通常用于创建 venv、安装依赖、创建配置文件等
func (m *Manager) processLifecycleScripts(appDir, appname string, env EnvMap) error {
	cmdDir := filepath.Join(appDir, "cmd")

	// 执行 install_callback
	installCallback := filepath.Join(cmdDir, "install_callback")
	if !fileExists(installCallback) {
		logger.Debug("未找到 install_callback 脚本")
		return nil
	}

	if IsRootScript(installCallback) {
		logger.Info("跳过需要 Root 权限的 install_callback 脚本")
		return nil
	}

	logger.Info("执行安装回调脚本: install_callback")

	// 确保脚本有执行权限
	if err := os.Chmod(installCallback, 0755); err != nil {
		logger.Debug("chmod install_callback 失败: %v", err)
	}

	cmd := exec.Command("bash", installCallback)
	cmd.Dir = appDir
	cmd.Env = append(os.Environ(), env.ToOsEnv()...)

	// 捕获输出
	output, err := cmd.CombinedOutput()
	if err != nil {
		logger.Warn("安装回调脚本执行失败: %v", err)
		if len(output) > 0 {
			// 只显示最后 500 字符，避免输出过长
			outStr := string(output)
			if len(outStr) > 500 {
				outStr = "..." + outStr[len(outStr)-500:]
			}
			logger.Debug("回调输出: %s", outStr)
		}
		return fmt.Errorf("install_callback 执行失败: %w", err)
	}

	logger.Info("安装回调脚本执行完成")
	if len(output) > 0 {
		// 显示脚本的输出（每行前加前缀）
		outStr := strings.TrimSpace(string(output))
		if outStr != "" {
			for _, line := range strings.Split(outStr, "\n") {
				if line != "" {
					logger.Info("  [callback] %s", line)
				}
			}
		}
	}

	return nil
}

// remapScriptPaths 重映射应用脚本中的硬编码路径
// 飞牛原版应用的 cmd/main、install_callback、restart.sh 等脚本通常硬编码 /var/apps/{appname}
// fpkm 安装到 ~/.local/fpk/apps/{appname}，需要将脚本中的硬编码路径替换为 fpkm 路径
// 同时处理 /@appshare/ → /shares/ 的转换
// 扫描范围：
//   - cmd/ 目录下的所有文件（main, install_callback, uninstall_callback 等）
//   - target/ 目录下的所有 .sh 脚本文件
//   - target/ 目录下的 cmd/main 等无扩展名脚本（如果存在）
func (m *Manager) remapScriptPaths(appDir, appname string) {
	oldPrefix := "/var/apps/" + appname
	newPrefix := appDir

	// 统计重映射的文件数
	remappedCount := 0

	// remapFile 重映射单个文件中的路径
	remapFile := func(filePath string) {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return
		}
		content := string(data)

		// 跳过二进制文件（包含大量不可打印字符）
		if isBinaryContent(data) {
			return
		}

		// 替换硬编码路径
		newContent := content
		newContent = strings.ReplaceAll(newContent, oldPrefix, newPrefix)
		// 同时处理其他应用的 /var/apps/ 引用（如依赖应用）
		// 仅替换当前应用的路径，避免误伤其他应用
		// @appshare → shares
		newContent = strings.ReplaceAll(newContent, "/@appshare/", "/shares/")

		// 归一化行尾：部分第三方 FPK 包在 Windows 上打包，
		// 脚本带 CRLF 行尾。bash 执行时会报
		//   syntax error near unexpected token `$'{\r''
		// 并以 exit status 2 失败（实测便签 note 1.2.1 即为此问题）。
		// 这里统一转成 LF，让原脚本无需修改即可运行。
		normalized := normalizeLineEndings(newContent)

		if normalized == content {
			return // 没有变化
		}
		newContent = normalized

		// 保留原文件权限
		info, err := os.Stat(filePath)
		if err != nil {
			return
		}
		if err := os.WriteFile(filePath, []byte(newContent), info.Mode()); err != nil {
			logger.Debug("重映射脚本路径失败 %s: %v", filePath, err)
			return
		}
		remappedCount++
		relPath, _ := filepath.Rel(appDir, filePath)
		logger.Debug("已重映射脚本路径: %s", relPath)
	}

	// 1. 扫描 cmd/ 目录下的所有文件
	cmdDir := filepath.Join(appDir, "cmd")
	if entries, err := os.ReadDir(cmdDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			// 跳过明显的二进制文件（有扩展名的非脚本文件）
			name := entry.Name()
			if isScriptFile(name) {
				remapFile(filepath.Join(cmdDir, name))
			}
		}
	}

	// 2. 扫描 target/ 目录下的 .sh 脚本文件
	targetDir := filepath.Join(appDir, "target")
	if entries, err := os.ReadDir(targetDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			// 只处理 .sh 文件和无扩展名的脚本文件
			if isScriptFile(name) {
				remapFile(filepath.Join(targetDir, name))
			}
		}
	}

	// 3. 递归扫描 target/ 下的子目录中的 .sh 文件（如 target/ui/restart.sh）
	filepath.Walk(targetDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if isScriptFile(info.Name()) {
			remapFile(path)
		}
		return nil
	})

	if remappedCount > 0 {
		logger.Info("已重映射 %d 个脚本中的硬编码路径 (/var/apps/%s → %s)", remappedCount, appname, newPrefix)
	}
}

// createSystemSymlinks 创建系统级符号链接
// 飞牛原版应用的二进制中可能硬编码了以下路径：
//
//	/var/apps/{appname}/target/...        → appDir/target/...
//	/vol1/@appdata/{appname}/...          → appDir/var/...
//	/vol1/@appcenter/{appname}/...        → appDir/target/...
//
// fpkm 安装到 ~/.local/fpk/apps/{appname}，需要创建符号链接让硬编码路径能正常工作
// 注意：需要 root 权限；非 root 用户会跳过并给出警告
func (m *Manager) createSystemSymlinks(appname, appDir string) {
	if runtime.GOOS == "windows" {
		return // Windows 开发环境不需要
	}

	targetDir := filepath.Join(appDir, "target")
	varDir := filepath.Join(appDir, "var")

	// 系统符号链接映射：link -> target
	symlinks := map[string]string{
		fmt.Sprintf("/var/apps/%s", appname):        appDir,    // /var/apps/{appname} -> appDir
		fmt.Sprintf("/vol1/@appdata/%s", appname):   varDir,    // /vol1/@appdata/{appname} -> appDir/var
		fmt.Sprintf("/vol1/@appcenter/%s", appname): targetDir, // /vol1/@appcenter/{appname} -> appDir/target
	}

	createdCount := 0
	for link, target := range symlinks {
		// 检查是否已存在（可能是真实目录或已存在的符号链接）
		if fi, err := os.Lstat(link); err == nil {
			// 如果是符号链接，检查是否指向正确的目标
			if fi.Mode()&os.ModeSymlink != 0 {
				if dest, err := os.Readlink(link); err == nil && dest == target {
					continue // 已存在且指向正确，跳过
				}
				// 指向错误的目标，删除重建
				os.Remove(link)
			} else {
				// 是真实目录/文件，不覆盖，跳过
				logger.Debug("系统路径已存在（非符号链接），跳过: %s", link)
				continue
			}
		}

		// 创建父目录
		parentDir := filepath.Dir(link)
		if err := os.MkdirAll(parentDir, 0755); err != nil {
			logger.Debug("创建符号链接父目录失败 %s: %v", parentDir, err)
			continue
		}

		// 创建符号链接
		if err := os.Symlink(target, link); err != nil {
			logger.Warn("创建系统符号链接失败 %s -> %s: %v (可能需要 root 权限)", link, target, err)
			continue
		}
		logger.Info("已创建系统符号链接: %s -> %s", link, target)
		createdCount++
	}

	if createdCount > 0 {
		logger.Info("已创建 %d 个系统符号链接（兼容飞牛原版二进制硬编码路径）", createdCount)
	}
}

// removeSystemSymlinks 清理系统级符号链接
// 卸载应用时调用，删除 createSystemSymlinks 创建的符号链接
func (m *Manager) removeSystemSymlinks(appname string) {
	if runtime.GOOS == "windows" {
		return
	}

	links := []string{
		fmt.Sprintf("/var/apps/%s", appname),
		fmt.Sprintf("/vol1/@appdata/%s", appname),
		fmt.Sprintf("/vol1/@appcenter/%s", appname),
	}

	for _, link := range links {
		// 只删除符号链接，不删除真实目录
		if fi, err := os.Lstat(link); err == nil {
			if fi.Mode()&os.ModeSymlink != 0 {
				if err := os.Remove(link); err == nil {
					logger.Info("已删除系统符号链接: %s", link)
				}
			}
		}
	}
}

// normalizeLineEndings 把 CRLF / 孤立 CR 统一成 LF。
//
// 背景：不少第三方 FPK 包是在 Windows 上打包的，shell 脚本带 CRLF 行尾，
// bash 会因 `$'\r'` 报 "syntax error near unexpected token" 并以 status 2 退出，
// 表现为"应用启动失败"但脚本本身逻辑正确。
// 只对文本内容调用（二进制文件由 isBinaryContent 提前跳过）。
func normalizeLineEndings(s string) string {
	if !strings.ContainsRune(s, '\r') {
		return s
	}
	// 先统一 CRLF → LF，再把遗留的孤立 CR → LF
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}

// isScriptFile 判断文件是否是脚本文件（需要重映射路径）
// 包括：无扩展名的文件（如 cmd/main, install_callback）、.sh 文件
func isScriptFile(name string) bool {
	// .sh 文件
	if strings.HasSuffix(name, ".sh") {
		return true
	}
	// 无扩展名的文件（如 main, install_callback, uninstall_callback）
	// 跳过常见的二进制文件扩展名
	if strings.HasSuffix(name, ".cgi") || strings.HasSuffix(name, ".bin") ||
		strings.HasSuffix(name, ".exe") || strings.HasSuffix(name, ".so") ||
		strings.HasSuffix(name, ".pyc") || strings.HasSuffix(name, ".json") ||
		strings.HasSuffix(name, ".png") || strings.HasSuffix(name, ".jpg") ||
		strings.HasSuffix(name, ".ico") || strings.HasSuffix(name, ".db") ||
		strings.HasSuffix(name, ".toml") || strings.HasSuffix(name, ".yaml") ||
		strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".html") ||
		strings.HasSuffix(name, ".css") || strings.HasSuffix(name, ".js") {
		return false
	}
	// 无扩展名或未知扩展名，当作脚本处理
	return true
}

// isBinaryContent 检测内容是否是二进制文件
// 通过检测是否包含大量不可打印字符来判断
func isBinaryContent(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	// 检查前 1024 字节中的不可打印字符比例
	checkLen := len(data)
	if checkLen > 1024 {
		checkLen = 1024
	}
	nonPrintable := 0
	for i := 0; i < checkLen; i++ {
		c := data[i]
		// 允许的字符：可打印 ASCII、制表符、换行符、回车符
		if c == 0 || (c < 9) || (c > 13 && c < 32) {
			nonPrintable++
		}
	}
	// 如果不可打印字符超过 30%，认为是二进制文件
	return nonPrintable*100/checkLen > 30
}

// moveDirContents 移动目录内容到目标目录
func moveDirContents(srcDir, dstDir string) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		src := filepath.Join(srcDir, entry.Name())
		dst := filepath.Join(dstDir, entry.Name())

		if entry.IsDir() {
			// 保留源目录权限（至少补到 0755）：
			// 目录缺少 x 位同样会导致其中文件无法被 exec。
			dirMode := os.FileMode(0755)
			if st, serr := entry.Info(); serr == nil {
				dirMode = st.Mode().Perm() | 0755
			}
			if err := os.MkdirAll(dst, dirMode); err != nil {
				return err
			}
			// MkdirAll 对已存在目录不生效，显式对齐权限
			_ = os.Chmod(dst, dirMode.Perm())
			if err := moveDirContents(src, dst); err != nil {
				return err
			}
		} else {
			if err := os.Rename(src, dst); err != nil {
				// Rename 可能跨设备失败，用 Copy+Delete
				if copyErr := copyFile(src, dst); copyErr != nil {
					return copyErr
				}
				os.Remove(src)
			}
		}
	}
	return nil
}

// copyFile 复制文件
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	// 获取原文件权限
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	// 保留原文件权限，但确保当前用户可读写
	mode := srcInfo.Mode()
	if mode&0111 != 0 {
		mode = mode | 0755
	} else {
		mode = mode | 0644
	}

	if err := os.WriteFile(dst, data, mode); err != nil {
		return err
	}

	// 关键：os.WriteFile 的 perm 参数**只对新建文件生效**。
	// 如果 dst 已存在（升级/重装时常见），旧权限会被原样保留 ——
	// 旧版本若是 0644，即使源文件有执行位也补不回来，
	// 最终表现为 "fork/exec ...: permission denied"。
	// 因此这里显式 chmod 一次，保证权限与源文件一致。
	if err := os.Chmod(dst, mode.Perm()); err != nil {
		return err
	}
	return nil
}

// prepareAppShare 准备 shares 数据目录
// 部分应用（如 EasyTier-Web）需要将数据文件复制到 shares/{APPNAME}/ 目录
// @appshare 在 fpkm 中映射为 shares
func (m *Manager) prepareAppShare(appDir, appname, targetDir string) {
	shareDir := filepath.Join(appDir, "shares", appname)

	// 检查 target/server/data 目录是否有数据文件需要复制
	dataSrcDir := filepath.Join(targetDir, "server", "data")
	if _, err := os.Stat(dataSrcDir); err != nil {
		return
	}

	// 创建 shares/{APPNAME} 目录
	if err := os.MkdirAll(shareDir, 0755); err != nil {
		logger.Warn("创建 shares 目录失败: %v", err)
		return
	}
	logger.Debug("已创建 shares 目录: %s", shareDir)

	// 复制数据文件到 shares/{APPNAME}/
	if entries, err := os.ReadDir(dataSrcDir); err == nil {
		for _, entry := range entries {
			srcFile := filepath.Join(dataSrcDir, entry.Name())
			dstFile := filepath.Join(shareDir, entry.Name())
			if _, err := os.Stat(dstFile); err == nil {
				logger.Debug("文件已存在，跳过复制: %s", dstFile)
				continue
			}
			if err := copyFile(srcFile, dstFile); err != nil {
				logger.Warn("复制文件失败 %s: %v", entry.Name(), err)
			} else {
				logger.Debug("已复制: %s -> %s", srcFile, dstFile)
			}
		}
	}
}

// readGatewaySocketFromUIConfig 读取统一网关应用的 socket 文件名。
// 存在 gatewaySocket 即表示该应用通过 Unix Socket 提供服务。
func readGatewaySocketFromUIConfig(targetDir, appDir string) string {
	entry, ok := ReadUIConfigEntry(targetDir, appDir)
	if !ok {
		return ""
	}
	return strings.TrimSpace(entry.GatewaySocket)
}

// readGatewayPrefixFromUIConfig 读取网关路由前缀（不带尾斜杠）。
// 用于路由匹配与转发。
func readGatewayPrefixFromUIConfig(targetDir, appDir string) string {
	entry, ok := ReadUIConfigEntry(targetDir, appDir)
	if !ok {
		return ""
	}
	if p := normalizePrefix(entry.GatewayPrefix); p != "" {
		return p
	}
	return prefixFromURL(entry.URL)
}

// readLaunchPathFromUIConfig 读取"打开应用"应使用的路径（url 字段，原样保留）。
// 与网关前缀不同，它可能带尾斜杠、子路径或查询串。
// 找不到 url 时返回空字符串，由调用方回退到网关前缀。
func readLaunchPathFromUIConfig(targetDir, appDir string) string {
	entry, ok := ReadUIConfigEntry(targetDir, appDir)
	if !ok {
		return ""
	}
	url := strings.TrimSpace(entry.URL)
	if url == "" {
		return ""
	}
	if strings.HasPrefix(url, "/") {
		return url
	}
	return ""
}

// readCGIPathFromUIConfig 读取 CGI 应用的 url（以 /cgi/ 开头）。
// CGI 应用直接用该路径作为网关前缀，因为前端会从
// window.location.pathname 里的 .cgi 推断 API 基础路径。
func readCGIPathFromUIConfig(targetDir, appDir string) string {
	entry, ok := ReadUIConfigEntry(targetDir, appDir)
	if !ok {
		return ""
	}
	if strings.HasPrefix(entry.URL, "/cgi/") {
		return entry.URL
	}
	if strings.HasPrefix(entry.GatewayPrefix, "/cgi/") {
		return entry.GatewayPrefix
	}
	return ""
}

// readPortFromUIConfig 读取端口应用的端口
func readPortFromUIConfig(targetDir, appDir string) string {
	entry, ok := ReadUIConfigEntry(targetDir, appDir)
	if !ok {
		return ""
	}
	port := strings.TrimSpace(entry.Port)
	// ui/config 里的 port 常写成对向导字段的引用（如 "${port}"、"{port}"），
	// 表示"端口由安装向导决定"，并不是一个真正的端口号。
	// 这类占位符必须原样丢弃，否则会被当成端口写进环境变量，
	// 让应用拿着字面量 "${port}" 去监听而启动失败。
	if isTemplatePlaceholder(port) {
		logger.Debug("ui/config 的 port 是模板占位符（%s），改由向导默认值决定", port)
		return ""
	}
	return port
}

// isTemplatePlaceholder 判断字符串是否是模板占位符而非真实取值。
//
// 覆盖真实应用中出现的写法：
//
//	${port} / $port   —— 引用向导字段
//	{port} / {{port}} —— 某些应用的另一种占位写法
//	${1} / %s          —— 位置参数
func isTemplatePlaceholder(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if strings.Contains(s, "${") || strings.Contains(s, "{{") {
		return true
	}
	if strings.HasPrefix(s, "$") {
		return true
	}
	// 形如 {port} 的单个花括号占位
	if strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}") {
		return true
	}
	// 纯格式化占位
	if s == "%s" || s == "%d" {
		return true
	}
	return false
}

// cleanJSON 清理 JSON 中多余的逗号（如 "},", "}," 等尾随逗号）
func cleanJSON(data []byte) []byte {
	s := string(data)
	// 使用正则表达式移除 }, 或 ], 前的逗号（逗号后可能有空白字符）
	// 匹配 ", " 或 ",\n" 或 ",\r\n" 后跟 } 或 ]
	commaPattern := regexp.MustCompile(`,\s*([}\]])`)
	s = commaPattern.ReplaceAllString(s, "$1")
	return []byte(s)
}

// fixPathEnv 修复 PATH 环境变量
// 将飞牛路径 /var/apps/xxx 替换为 fpkm 路径 ~/.local/fpk/apps/xxx
// 自动检测已安装的工具型应用（如 python312）并添加到 PATH 和 LD_LIBRARY_PATH
func (m *Manager) fixPathEnv(env EnvMap) {
	pathVal, hasPath := env["PATH"]
	if !hasPath {
		pathVal = os.Getenv("PATH")
	}

	// 将 /var/apps/ 替换为 fpkm apps 目录
	appsDir := m.cfg.AppsDir
	pathVal = strings.ReplaceAll(pathVal, "/var/apps/", appsDir+"/")

	// 将飞牛的 @appshare 替换为 shares
	pathVal = strings.ReplaceAll(pathVal, "/@appshare/", "/shares/")

	// 确保 fpkm bin 目录在 PATH 中
	fpkBinDir := m.cfg.BinDir
	if !strings.Contains(pathVal, fpkBinDir) {
		pathVal = fpkBinDir + ":" + pathVal
	}

	// 检测已安装的工具型应用，自动添加 bin 和 lib 到环境变量
	ldLibraryPath := env["LD_LIBRARY_PATH"]
	if entries, err := os.ReadDir(appsDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !isToolApp(name) {
				continue
			}
			appBinDir := filepath.Join(appsDir, name, "target", "bin")
			appLibDir := filepath.Join(appsDir, name, "target", "lib")
			if _, err := os.Stat(appBinDir); err == nil {
				if !strings.Contains(pathVal, appBinDir) {
					pathVal = appBinDir + ":" + pathVal
				}
			}
			if _, err := os.Stat(appLibDir); err == nil {
				if !strings.Contains(ldLibraryPath, appLibDir) {
					if ldLibraryPath != "" {
						ldLibraryPath = appLibDir + ":" + ldLibraryPath
					} else {
						ldLibraryPath = appLibDir
					}
				}
			}
		}
	}

	env["PATH"] = pathVal
	if ldLibraryPath != "" {
		env["LD_LIBRARY_PATH"] = ldLibraryPath
	}
	logger.Debug("PATH: %s", pathVal)
	logger.Debug("LD_LIBRARY_PATH: %s", ldLibraryPath)
}

// resolveEntryEnvVars 用环境变量补全 entry 中剩余的 ${VAR} / $VAR / ${VAR:-default} 引用
func resolveEntryEnvVars(entry *EntryInfo, env EnvMap) {
	// 匹配 ${VAR:-default} 语法
	defaultVarPattern := regexp.MustCompile(`\$\{(\w+):-(\w+)\}`)

	resolveVar := func(s string) string {
		// 先处理 ${VAR:-default} 语法：env 中有值则用 env 值，否则用默认值
		s = defaultVarPattern.ReplaceAllStringFunc(s, func(match string) string {
			sub := defaultVarPattern.FindStringSubmatch(match)
			varName := sub[1]
			defaultVal := sub[2]
			if v, ok := env[varName]; ok && v != "" {
				return v
			}
			return defaultVal
		})
		// 再处理普通的 ${VAR} 和 $VAR
		for k, v := range env {
			s = strings.ReplaceAll(s, "${"+k+"}", v)
			s = strings.ReplaceAll(s, "$"+k, v)
		}
		return s
	}

	// remapPaths 将飞牛系统路径 /var/apps/{appname} 重映射为 fpkm 安装路径
	// 同时处理 @appshare → shares 的转换
	// 通过 TRIM_APPDEST_VOL 推断 fpkm apps 根目录
	remapPaths := func(s string) string {
		appDestVol := env["TRIM_APPDEST_VOL"] // 如 /root/.local/fpk/apps/EasyTier-Web
		if appDestVol != "" {
			// 从 appDestVol 提取 apps 根目录
			// appDestVol = /root/.local/fpk/apps/EasyTier-Web
			// appsDir  = /root/.local/fpk/apps
			parts := strings.Split(appDestVol, "/")
			if len(parts) >= 2 {
				appsDir := strings.Join(parts[:len(parts)-1], "/")
				// /var/apps/xxx → {appsDir}/xxx
				s = strings.ReplaceAll(s, "/var/apps/", appsDir+"/")
			}
		}
		// @appshare → shares
		s = strings.ReplaceAll(s, "/@appshare/", "/shares/")
		return s
	}

	// 补全主命令
	entry.Command = remapPaths(resolveVar(entry.Command))
	// 补全主命令参数
	for i, arg := range entry.Args {
		entry.Args[i] = remapPaths(resolveVar(arg))
	}
	// 补全工作目录
	entry.WorkDir = remapPaths(resolveVar(entry.WorkDir))

	// 补全预启动命令
	for i := range entry.PreStartCmds {
		for j, arg := range entry.PreStartCmds[i].Args {
			entry.PreStartCmds[i].Args[j] = remapPaths(resolveVar(arg))
		}
		if entry.PreStartCmds[i].FlagFile != "" {
			entry.PreStartCmds[i].FlagFile = remapPaths(resolveVar(entry.PreStartCmds[i].FlagFile))
		}
	}
	// 补全附加进程
	for i := range entry.ExtraCmds {
		entry.ExtraCmds[i].Command = remapPaths(resolveVar(entry.ExtraCmds[i].Command))
		for j, arg := range entry.ExtraCmds[i].Args {
			entry.ExtraCmds[i].Args[j] = remapPaths(resolveVar(arg))
		}
		if entry.ExtraCmds[i].WorkDir != "" {
			entry.ExtraCmds[i].WorkDir = remapPaths(resolveVar(entry.ExtraCmds[i].WorkDir))
		}
	}
}

// registerToGateway 注册应用到统一网关
// 需要先启动 fpkm 网关守护进程，再通过 HTTP API 注册路由
func (m *Manager) registerToGateway(appName, prefix, socketPath string) {
	client := gateway.NewClient(m.cfg)

	// 网关未运行则自动启动
	if !client.IsRunning() {
		logger.Info("统一网关未运行，正在自动启动...")
		if err := m.startGatewayDaemon(); err != nil {
			logger.Warn("自动启动网关失败: %v（请手动运行: fpkm gateway --start）", err)
			return
		}
		// 等待网关启动
		if !client.WaitForGateway(5 * time.Second) {
			logger.Warn("网关启动超时")
			return
		}
	}

	// 注册路由
	if err := client.RegisterRoute(appName, prefix, socketPath); err != nil {
		logger.Warn("注册网关路由失败: %v", err)
		return
	}

	logger.Info("访问地址: http://localhost:%d%s", m.cfg.GatewayPort, prefix)
}

// registerCGIToGateway 注册 CGI 应用到统一网关
// CGI 应用不需要 Unix Socket，而是由网关直接执行 CGI 二进制
func (m *Manager) registerCGIToGateway(appName, prefix, cgiPath string) {
	client := gateway.NewClient(m.cfg)

	// 网关未运行则自动启动
	if !client.IsRunning() {
		logger.Info("统一网关未运行，正在自动启动...")
		if err := m.startGatewayDaemon(); err != nil {
			logger.Warn("自动启动网关失败: %v（请手动运行: fpkm gateway --start）", err)
			return
		}
		if !client.WaitForGateway(5 * time.Second) {
			logger.Warn("网关启动超时")
			return
		}
	}

	// 注册 CGI 路由
	if err := client.RegisterCGIRoute(appName, prefix, cgiPath); err != nil {
		logger.Warn("注册 CGI 网关路由失败: %v", err)
		return
	}

	logger.Info("访问地址: http://localhost:%d%s", m.cfg.GatewayPort, prefix)
}

// startGatewayDaemon 启动网关守护进程
func (m *Manager) startGatewayDaemon() error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("获取可执行文件路径失败: %w", err)
	}

	logFile, err := os.OpenFile(m.cfg.GatewayLogFile(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("打开网关日志文件失败: %w", err)
	}

	cmd := exec.Command(execPath, "gateway", "--run", "--port", strconv.Itoa(m.cfg.GatewayPort))
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	setProcessGroup(cmd)

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("启动网关失败: %w", err)
	}

	// 记录网关 PID
	os.WriteFile(m.cfg.GatewayPidFile(), []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644)

	go func() {
		defer logFile.Close()
		cmd.Wait()
	}()

	return nil
}
