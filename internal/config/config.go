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
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}

	// 跨平台根目录选择
	if runtime.GOOS == "windows" {
		c.BaseDir = filepath.Join(home, ".fpk")
	} else {
		c.BaseDir = filepath.Join(home, ".local", "fpk")
	}

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

// AppSocketPath 返回应用的 Unix Socket 路径
// socket 文件放在 target/ 目录下（与飞牛一致）
func (c *Config) AppSocketPath(appname, socketName string) string {
	return filepath.Join(c.AppTargetDir(appname), socketName)
}
