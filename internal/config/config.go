package config

import (
	"os"
	"path/filepath"
	"runtime"
)

// Config 全局配置
type Config struct {
	BaseDir     string // 根目录 ~/.local/fpk 或 ~/.fpk
	AppsDir     string // 应用安装目录
	BinDir      string // 可执行文件目录
	LogDir      string // 全局日志目录
	GatewayPort int    // 统一网关端口（默认 7666）
}

var instance *Config

// GetConfig 获取全局配置实例
func GetConfig() *Config {
	if instance != nil {
		return instance
	}
	instance = &Config{}
	instance.init()
	return instance
}

func (c *Config) init() {
	c.BaseDir = resolveBaseDir()

	c.AppsDir = filepath.Join(c.BaseDir, "apps")
	c.BinDir = filepath.Join(c.BaseDir, "bin")
	c.LogDir = filepath.Join(c.BaseDir, "logs")
	c.GatewayPort = 7666 // 默认网关端口

	// 确保目录存在
	for _, dir := range []string{c.BaseDir, c.AppsDir, c.BinDir, c.LogDir} {
		os.MkdirAll(dir, 0755)
	}
}

// AppDir 返回指定应用的安装目录
func (c *Config) AppDir(appname string) string {
	return filepath.Join(c.AppsDir, appname)
}

// AppTargetDir 返回应用的 target 目录（源码/二进制）
func (c *Config) AppTargetDir(appname string) string {
	return filepath.Join(c.AppDir(appname), "target")
}

// AppEtcDir 返回应用的配置目录
func (c *Config) AppEtcDir(appname string) string {
	return filepath.Join(c.AppDir(appname), "etc")
}

// AppVarDir 返回应用的运行时数据目录
func (c *Config) AppVarDir(appname string) string {
	return filepath.Join(c.AppDir(appname), "var")
}

// AppTmpDir 返回应用的临时文件目录
func (c *Config) AppTmpDir(appname string) string {
	return filepath.Join(c.AppDir(appname), "tmp")
}

// AppHomeDir 返回应用的用户数据目录
func (c *Config) AppHomeDir(appname string) string {
	return filepath.Join(c.AppDir(appname), "home")
}

// AppSharesDir 返回应用的共享目录
func (c *Config) AppSharesDir(appname string) string {
	return filepath.Join(c.AppDir(appname), "shares")
}

// AppLogsDir 返回应用的日志目录
func (c *Config) AppLogsDir(appname string) string {
	return filepath.Join(c.AppDir(appname), "logs")
}

// AppPidFile 返回应用的 PID 文件路径
func (c *Config) AppPidFile(appname string) string {
	return filepath.Join(c.AppDir(appname), "app.pid")
}

// AppEnvFile 返回应用的环境变量文件路径
func (c *Config) AppEnvFile(appname string) string {
	return filepath.Join(c.AppDir(appname), ".env")
}

// AppManifestFile 返回应用的 manifest 文件路径
func (c *Config) AppManifestFile(appname string) string {
	return filepath.Join(c.AppDir(appname), "manifest")
}

// AppLogFile 返回应用的主日志文件路径
func (c *Config) AppLogFile(appname string) string {
	return filepath.Join(c.AppLogsDir(appname), "app.log")
}

// AppErrorLogFile 返回应用的错误日志文件路径
func (c *Config) AppErrorLogFile(appname string) string {
	return filepath.Join(c.AppLogsDir(appname), "error.log")
}

// AppLinksDir 返回工具型应用符号链接目录
func (c *Config) AppLinksDir() string {
	return c.BinDir
}

// GatewayPidFile 返回网关 PID 文件路径
func (c *Config) GatewayPidFile() string {
	return filepath.Join(c.BaseDir, "gateway.pid")
}

// GatewayLogFile 返回网关日志文件路径
func (c *Config) GatewayLogFile() string {
	return filepath.Join(c.LogDir, "gateway.log")
}

// WebPortFile 记录 Web 界面实际监听的端口。
//
// 网关与 Web 是两个进程、且**端口不同**（如 web=7667、gateway=7666）。
// 未登录时网关要给出"前往登录"的链接，就必须知道 Web 界面在哪，
// 因此由 Web 进程在监听成功后把真实端口写到这里，网关读取。
func (c *Config) WebPortFile() string {
	return filepath.Join(c.BaseDir, "web.port")
}

// AppSocketPath 返回应用的 Unix Socket 路径
// socket 文件放在 target/ 目录下（与飞牛一致）
func (c *Config) AppSocketPath(appname, socketName string) string {
	return filepath.Join(c.AppTargetDir(appname), socketName)
}

// resolveBaseDir 决定数据根目录。
//
// 优先使用「可执行文件所在目录/data」：
//
//	/opt/fpkm/dist/fpkm  →  /opt/fpkm/dist/data
//
// 为什么改到这里：
//
//	旧方案用 $HOME/.local/fpk。数据跟着「运行时的 HOME」走，
//	而 HOME 会随用户改变（sudo、systemd、docker、不同的登录用户），
//	于是出现"某个用户装的应用，换个身份启动就全部消失"的问题。
//	数据放在程序旁边后，同一份二进制必然对应同一份数据，不会再错位。
//
// 回退：可执行文件所在目录不可写时（例如 /usr/local/bin 只读），
// 退回 $HOME/.local/fpk，保证程序仍能启动。
func resolveBaseDir() string {
	if exe, err := os.Executable(); err == nil {
		// 解析符号链接，避免通过软链接启动时把 data 建到软链接所在目录
		if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			exe = resolved
		}
		candidate := filepath.Join(filepath.Dir(exe), "data")
		if dirUsable(candidate) {
			return candidate
		}
	}
	return legacyBaseDir()
}

// legacyBaseDir 是旧版本的存储位置（$HOME/.local/fpk 或 %USERPROFILE%\.fpk）。
func legacyBaseDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(home, ".fpk")
	}
	return filepath.Join(home, ".local", "fpk")
}

// dirUsable 判断目录能否创建并写入。
//
// 只检查"存在"是不够的：目录可能存在但只读。这里实际尝试创建目录，
// 并在目录已存在时验证可写，避免把 data 放在只读位置后运行时报错。
func dirUsable(dir string) bool {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	probe := filepath.Join(dir, ".write-test")
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(probe)
	return true
}

// LegacyBaseDirForDisplay 返回旧版本的数据目录，供 CLI 展示。
func LegacyBaseDirForDisplay() string {
	return legacyBaseDir()
}
