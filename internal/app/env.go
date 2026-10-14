package app

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"fpkm/internal/config"
	"fpkm/internal/fpk"
)

// EnvMap 环境变量映射表
type EnvMap map[string]string

// BuildEnv 为应用构建环境变量映射
// 核心逻辑：将飞牛 TRIM_* 变量翻译为用户空间路径
func BuildEnv(cfg *config.Config, manifest *fpk.Manifest) (EnvMap, error) {
	appDir := cfg.AppDir(manifest.Appname)
	targetDir := filepath.Join(appDir, "target")
	env := EnvMap{}

	// 当前用户信息
	currentUser, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("获取当前用户失败: %w", err)
	}
	uid := currentUser.Uid
	if runtime.GOOS == "windows" {
		uid = "1000"
	}

	// 路径映射
	env["TRIM_APPNAME"] = manifest.Appname
	env["TRIM_APPVER"] = manifest.Version
	env["TRIM_APP_STATUS"] = "START"
	env["TRIM_APPDEST"] = targetDir
	env["TRIM_PKGETC"] = filepath.Join(appDir, "etc")
	env["TRIM_PKGVAR"] = filepath.Join(appDir, "var")
	env["TRIM_PKGTMP"] = filepath.Join(appDir, "tmp")
	env["TRIM_PKGHOME"] = filepath.Join(appDir, "home")
	env["TRIM_PKGMETA"] = filepath.Join(appDir, "meta")
	env["TRIM_APPDEST_VOL"] = appDir

	// 用户和权限映射
	env["TRIM_USERNAME"] = currentUser.Username
	env["TRIM_GROUPNAME"] = currentUser.Username
	env["TRIM_UID"] = uid
	env["TRIM_GID"] = uid
	env["TRIM_RUN_USERNAME"] = currentUser.Username
	env["TRIM_RUN_GROUPNAME"] = currentUser.Username
	env["TRIM_RUN_UID"] = uid
	env["TRIM_RUN_GID"] = uid

	// 数据共享路径
	env["TRIM_DATA_SHARE_PATHS"] = filepath.Join(appDir, "shares")
	env["TRIM_DATA_ACCESSIBLE_PATHS"] = ""

	// 日志文件
	env["TRIM_TEMP_LOGFILE"] = filepath.Join(appDir, "logs", "error.log")

	// 临时目录
	env["TRIM_TEMP_UPGRADE_FOLDER"] = filepath.Join(appDir, "tmp", "upgrade")
	env["TRIM_PKGINST_TEMP_DIR"] = filepath.Join(appDir, "tmp", "install")
	env["TRIM_TEMP_TPKFILE"] = filepath.Join(appDir, "tmp", "pkfile")

	// 系统版本（硬编码欺骗版本检查）
	env["TRIM_SYS_VERSION"] = "1.2.0"
	env["TRIM_SYS_VERSION_MAJOR"] = "1"
	env["TRIM_SYS_VERSION_MINOR"] = "2"
	env["TRIM_SYS_VERSION_BUILD"] = "0"
	env["TRIM_SYS_ARCH"] = runtime.GOARCH
	env["TRIM_KERNEL_VERSION"] = "unknown"
	env["TRIM_SYS_MACHINE_ID"] = "fpkm-host"
	env["TRIM_SYS_LANGUAGE"] = "zh-CN"

	// 识别应用类型：Socket 应用 / CGI 应用 / 端口应用
	if manifest.IsSocketApp() {
		// Socket 应用：通过 Unix Socket 提供 HTTP 服务
		// 前缀与启动路径都以 ui/config 为准（appname 可能与 url 前缀不一致）
		gatewayPrefix := ResolveGatewayPrefix(targetDir, appDir, manifest.Appname)
		socketPath := filepath.Join(targetDir, manifest.ServiceSocket)

		env["GATEWAY_PREFIX"] = gatewayPrefix
		env["LAUNCH_PATH"] = ResolveLaunchPath(targetDir, appDir, manifest.Appname)
		env["SOCKET_PATH"] = socketPath
		env["TRIM_SERVICE_SOCKET"] = socketPath
		env["TRIM_SERVICE_PORT"] = ""
		env["CGI_PATH"] = ""
	} else if cgiPath := readCGIPathFromUIConfig(targetDir, appDir); cgiPath != "" {
		// CGI 应用：通过执行 CGI 二进制提供 Web UI
		// cgiPath 形如 /cgi/ThirdParty/tailscale/index.cgi
		// 网关前缀使用完整 CGI 路径（含 index.cgi）
		// 因为 tailscale 前端通过 window.location.pathname 中的 .cgi 来推断 API 基础路径
		// 如果前缀不含 .cgi，前端会回退到 /api/ 绝对路径，导致 404
		gatewayPrefix := cgiPath

		// CGI 二进制路径：target/ui/index.cgi
		cgiBinary := filepath.Join(targetDir, "ui", filepath.Base(cgiPath))
		if !fileExists(cgiBinary) {
			// 兜底：在 appDir/ui/ 下找
			cgiBinary = filepath.Join(appDir, "ui", filepath.Base(cgiPath))
		}

		// tailscale 的 index.cgi 硬编码了 /var/apps/tailscale/var/tailscaled.sock
		// 需要通过 TS_SOCKET 环境变量覆盖，让它使用正确的 socket 路径
		// 注意：这里需要提前知道 tailscaled 使用的 socket 路径
		// 飞牛原版中，tailscaled 的 socket 在 TRIM_PKGVAR/tailscaled.sock
		tsSocket := filepath.Join(appDir, "var", "tailscaled.sock")

		env["GATEWAY_PREFIX"] = gatewayPrefix
		env["LAUNCH_PATH"] = gatewayPrefix
		env["CGI_PATH"] = cgiBinary
		env["SOCKET_PATH"] = ""
		env["TRIM_SERVICE_SOCKET"] = ""
		env["TRIM_SERVICE_PORT"] = ""
		env["TS_SOCKET"] = tsSocket
	} else {
		// 端口应用：监听 TCP 端口
		port := resolveServicePort(manifest)
		env["TRIM_SERVICE_PORT"] = port
		env["GATEWAY_PREFIX"] = ""
		env["LAUNCH_PATH"] = ""
		env["SOCKET_PATH"] = ""
		env["CGI_PATH"] = ""
	}

	return env, nil
}

// resolveServicePort 解析服务端口
// 若端口为80/443则+10000，检测占用则自动+1
func resolveServicePort(manifest *fpk.Manifest) string {
	portStr := manifest.ServicePort
	if portStr == "" {
		portStr = "8080"
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "8080"
	}

	// 特权端口偏移
	if port == 80 {
		port = 10080
	} else if port == 443 {
		port = 10443
	} else if port < 1024 {
		port += 10000
	}

	return strconv.Itoa(port)
}

// FindAvailablePort 查找可用端口（从指定端口开始检测）
func FindAvailablePort(startPort int) int {
	for p := startPort; p < startPort+100; p++ {
		if !isPortInUse(p) {
			return p
		}
	}
	return startPort
}

// SaveEnvFile 将环境变量写入 .env 文件
func SaveEnvFile(envFile string, env EnvMap) error {
	var lines []string
	for k, v := range env {
		lines = append(lines, fmt.Sprintf("%s=%s", k, v))
	}
	content := strings.Join(lines, "\n")
	return os.WriteFile(envFile, []byte(content), 0644)
}

// LoadEnvFile 从 .env 文件读取环境变量
func LoadEnvFile(envFile string) (EnvMap, error) {
	data, err := os.ReadFile(envFile)
	if err != nil {
		return nil, err
	}

	env := EnvMap{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, "=")
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		env[key] = val
	}
	return env, nil
}

// ToOsEnv 将 EnvMap 转换为 os.Environ 格式
func (e EnvMap) ToOsEnv() []string {
	result := make([]string, 0, len(e))
	for k, v := range e {
		result = append(result, fmt.Sprintf("%s=%s", k, v))
	}
	return result
}
