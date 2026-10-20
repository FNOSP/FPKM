package app

import (
	"encoding/json"
	"os"
	"path/filepath"

	"fpkm/internal/config"
	"fpkm/internal/logger"
)

// AccessMode 是用户为应用手动选择的接入方式。
//
// 为什么需要覆盖 ui/config：ui/config 由打包者决定，用户无法改。
// 典型场景是 Lucky 这类「飞牛定制构建」——它硬编码了 fnOS 原生
// socket 路径并按 fnOS 网关签名校验请求，经 fpkm 网关访问会被它
// 判为非法（返回 "fnOS gateway identity invalid"）。此时只要让用户
// 把该应用改成「端口应用」，fpkm 就让浏览器直连应用端口，绕开网关。
type AccessMode string

const (
	// AccessAuto 跟随 ui/config（默认行为，不覆盖）
	AccessAuto AccessMode = ""
	// AccessPort 端口应用：直接访问 host:port
	AccessPort AccessMode = "port"
	// AccessGateway 统一网关：经 fpkm 网关 /app/{name} 转发
	AccessGateway AccessMode = "gateway"
	// AccessCGI CGI 应用：经 fpkm 网关转发到 CGI 二进制
	AccessCGI AccessMode = "cgi"
)

// AccessOverride 是某个应用的接入方式覆盖设置。
type AccessOverride struct {
	Mode AccessMode `json:"mode"`
	// Port 端口应用的端口号（0 表示沿用环境变量里的端口）
	Port int `json:"port,omitempty"`
	// Path 访问路径（默认 "/"）
	Path string `json:"path,omitempty"`
}

// accessOverrideFile 返回覆盖配置的存储路径。
func accessOverrideFile(cfg *config.Config) string {
	return filepath.Join(cfg.BaseDir, "access-overrides.json")
}

// LoadAccessOverrides 读取全部覆盖设置；文件不存在或损坏时返回空表。
func LoadAccessOverrides(cfg *config.Config) map[string]AccessOverride {
	data, err := os.ReadFile(accessOverrideFile(cfg))
	if err != nil {
		return map[string]AccessOverride{}
	}
	var m map[string]AccessOverride
	if err := json.Unmarshal(data, &m); err != nil {
		logger.Warn("access-overrides.json 解析失败，已忽略: %v", err)
		return map[string]AccessOverride{}
	}
	if m == nil {
		return map[string]AccessOverride{}
	}
	return m
}

// SaveAccessOverrides 原子写入全部覆盖设置。
func SaveAccessOverrides(cfg *config.Config, m map[string]AccessOverride) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	p := accessOverrideFile(cfg)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// GetAccessOverride 返回单个应用的覆盖设置（不存在时为零值）。
func GetAccessOverride(cfg *config.Config, appname string) AccessOverride {
	return LoadAccessOverrides(cfg)[appname]
}

// SetAccessOverride 设置单个应用的覆盖；mode 为空表示清除覆盖。
func SetAccessOverride(cfg *config.Config, appname string, ov AccessOverride) error {
	m := LoadAccessOverrides(cfg)
	if ov.Mode == AccessAuto {
		delete(m, appname)
	} else {
		m[appname] = ov
	}
	return SaveAccessOverrides(cfg, m)
}
