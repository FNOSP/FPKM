package gateway

import (
	"os"
	"os/user"
	"strings"
	"testing"
)

// gatewayUserID 应返回当前进程 uid（与转发头一致）
func TestGatewayUserIDMatchesProcess(t *testing.T) {
	got := gatewayUserID()
	if got == "" {
		t.Fatal("Userid 不应为空")
	}
	if u, err := user.Current(); err == nil && u.Uid != "" && got != u.Uid {
		t.Errorf("期望 uid %q，得到 %q", u.Uid, got)
	}
}

// gatewayUserName 应返回非空用户名
func TestGatewayUserNameNonEmpty(t *testing.T) {
	if gatewayUserName() == "" {
		t.Error("Username 不应为空")
	}
}

// 三个身份头必须成组出现：Lucky 缺 Userid/Username 时报
// "fnOS gateway identity invalid"
func TestGatewayIdentityHeadersAreGrouped(t *testing.T) {
	src, err := os.ReadFile("gateway.go")
	if err != nil {
		t.Skip("无法读取源码")
	}
	s := string(src)

	// HTTP 转发路径
	if !strings.Contains(s, `X-Trim-Userid", gatewayUserID()`) {
		t.Error("HTTP 转发缺少 X-Trim-Userid")
	}
	if !strings.Contains(s, `X-Trim-Username", gatewayUserName()`) {
		t.Error("HTTP 转发缺少 X-Trim-Username")
	}
	// Isadmin 用布尔字面量（飞牛规范），不用 "1"
	if !strings.Contains(s, `X-Trim-Isadmin", "true"`) {
		t.Error(`HTTP 转发 X-Trim-Isadmin 应为 "true"`)
	}

	// WebSocket 握手路径
	if !strings.Contains(s, `X-Trim-Userid: %s`) {
		t.Error("WebSocket 握手缺少 X-Trim-Userid")
	}
	if !strings.Contains(s, `X-Trim-Username: %s`) {
		t.Error("WebSocket 握手缺少 X-Trim-Username")
	}
}

// 三个头在两条转发路径上都存在（防止只改一处）
func TestGatewayIdentityAppliedToBothPaths(t *testing.T) {
	src, err := os.ReadFile("gateway.go")
	if err != nil {
		t.Skip("无法读取源码")
	}
	s := string(src)
	if n := strings.Count(s, "X-Trim-Userid"); n < 2 {
		t.Errorf("X-Trim-Userid 应出现在 HTTP 与 WebSocket 两处，实际 %d 次", n)
	}
	if n := strings.Count(s, "X-Trim-Username"); n < 2 {
		t.Errorf("X-Trim-Username 应出现在两处，实际 %d 次", n)
	}
}
