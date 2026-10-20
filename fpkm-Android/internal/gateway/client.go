package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"fpkm/internal/config"
)

// Client 网关客户端
//
// fpkm 主进程通过 HTTP API 与内置 Go 网关守护进程通信，
// 用于注册/移除路由、查询状态等操作。
type Client struct {
	port int
	cfg  *config.Config
}

// NewClient 创建网关客户端
func NewClient(cfg *config.Config) *Client {
	port := readGatewayPort(cfg)
	return &Client{port: port, cfg: cfg}
}

// readGatewayPort 从配置读取网关端口，默认 7666
func readGatewayPort(cfg *config.Config) int {
	confFile := fmt.Sprintf("%s/gateway.conf", cfg.BaseDir)
	if data, err := os.ReadFile(confFile); err == nil {
		port, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err == nil && port > 0 {
			return port
		}
	}
	return DefaultPort
}

// IsRunning 检查网关是否在运行
func (c *Client) IsRunning() bool {
	url := fmt.Sprintf("http://127.0.0.1:%d/__gateway__/status", c.port)
	resp, err := http.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// RegisterRoute 向网关注册路由
func (c *Client) RegisterRoute(appName, prefix, socket string) error {
	route := Route{
		AppName: appName,
		Prefix:  prefix,
		Socket:  socket,
	}
	data, _ := json.Marshal(route)
	url := fmt.Sprintf("http://127.0.0.1:%d/__gateway__/routes", c.port)
	resp, err := http.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("连接网关失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("注册路由失败: %s", string(body))
	}
	return nil
}

// RegisterCGIRoute 向网关注册 CGI 路由
// cgiBinary 是 CGI 二进制的完整路径
func (c *Client) RegisterCGIRoute(appName, prefix, cgiBinary string) error {
	route := Route{
		AppName:  appName,
		Prefix:   prefix,
		Socket:   cgiBinary, // Socket 字段复用为 CGI 二进制路径
		Protocol: "cgi",
	}
	data, _ := json.Marshal(route)
	url := fmt.Sprintf("http://127.0.0.1:%d/__gateway__/routes", c.port)
	resp, err := http.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("连接网关失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("注册 CGI 路由失败: %s", string(body))
	}
	return nil
}

// UnregisterRoute 从网关移除路由
func (c *Client) UnregisterRoute(prefix string) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/__gateway__/routes?prefix=%s", c.port, prefix)
	req, _ := http.NewRequest(http.MethodDelete, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("连接网关失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("移除路由失败")
	}
	return nil
}

// ListRoutes 列出网关所有路由
func (c *Client) ListRoutes() ([]*Route, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d/__gateway__/routes", c.port)
	resp, err := http.Get(url)
	if err != nil {
		return nil, fmt.Errorf("连接网关失败: %w", err)
	}
	defer resp.Body.Close()
	var routes []*Route
	if err := json.NewDecoder(resp.Body).Decode(&routes); err != nil {
		return nil, err
	}
	return routes, nil
}

// GetStatus 获取网关状态
func (c *Client) GetStatus() (map[string]interface{}, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d/__gateway__/status", c.port)
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var status map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&status)
	return status, nil
}

// WaitForGateway 等待网关启动
func (c *Client) WaitForGateway(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if c.IsRunning() {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}
