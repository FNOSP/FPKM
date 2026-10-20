package fpk

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Manifest 应用清单文件
type Manifest struct {
	Appname              string
	Version              string
	DisplayName          string
	Desc                 string
	Source               string
	Platform             string
	Maintainer           string
	MaintainerURL        string
	OsMinVersion         string
	OsMaxVersion         string
	DesktopUidir         string
	DesktopAppLaunchName string
	ServicePort          string
	ServiceSocket        string // service_socket 字段（如 fntermx.sock）
	CheckPort            string
	CtlStop              string
	InstallType          string
	InstallDepApps       string
	Changelog            string
	Raw                  map[string]string // 原始键值对
}

// ParseManifest 解析 manifest 文件
// 文件格式为 key=value 的键值对，每行一个
func ParseManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("打开manifest失败: %w", err)
	}
	return ParseManifestContent(data)
}

// ParseManifestContent 从字节内容解析 manifest
func ParseManifestContent(data []byte) (*Manifest, error) {
	raw := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, "=")
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		// 去除可能的引号
		val = strings.Trim(val, `"'`)
		raw[key] = val
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取manifest失败: %w", err)
	}

	m := &Manifest{
		Appname:              raw["appname"],
		Version:              raw["version"],
		DisplayName:          raw["display_name"],
		Desc:                 raw["desc"],
		Source:               raw["source"],
		Platform:             raw["platform"],
		Maintainer:           raw["maintainer"],
		MaintainerURL:        raw["maintainer_url"],
		OsMinVersion:         raw["os_min_version"],
		OsMaxVersion:         raw["os_max_version"],
		DesktopUidir:         raw["desktop_uidir"],
		DesktopAppLaunchName: raw["desktop_applaunchname"],
		ServicePort:          raw["service_port"],
		ServiceSocket:        raw["service_socket"],
		CheckPort:            raw["checkport"],
		CtlStop:              raw["ctl_stop"],
		InstallType:          raw["install_type"],
		InstallDepApps:       raw["install_dep_apps"],
		Changelog:            raw["changelog"],
		Raw:                  raw,
	}

	if m.Appname == "" {
		return nil, fmt.Errorf("manifest中缺少appname字段")
	}

	if m.DesktopUidir == "" {
		m.DesktopUidir = "ui"
	}

	return m, nil
}

// ParseManifestFromDir 从目录中读取 manifest
func ParseManifestFromDir(dir string) (*Manifest, error) {
	return ParseManifest(filepath.Join(dir, "manifest"))
}

// Get 获取manifest中指定字段的值
func (m *Manifest) Get(key string) string {
	return m.Raw[key]
}

// HasServicePort 是否声明了服务端口
func (m *Manifest) HasServicePort() bool {
	return m.ServicePort != ""
}

// IsDockerApp 是否是Docker应用
func (m *Manifest) IsDockerApp() bool {
	return m.Get("install_type") == "docker" || m.InstallDepApps != ""
}

// IsStaticApp 是否是静态页面应用（无启停控制）
func (m *Manifest) IsStaticApp() bool {
	return m.CtlStop == "false"
}

// IsSocketApp 是否是 Socket 应用（通过 Unix Socket 提供服务，不监听 TCP 端口）
// 判定条件：checkport=false 且有 service_socket
func (m *Manifest) IsSocketApp() bool {
	return (m.CheckPort == "false" || m.ServiceSocket != "") && m.ServiceSocket != ""
}

// GetServiceSocket 获取 socket 路径
// manifest 中 service_socket 通常是文件名（如 fntermx.sock）
// 实际路径为 TRIM_APPDEST/service_socket
func (m *Manifest) GetServiceSocket() string {
	return m.ServiceSocket
}
