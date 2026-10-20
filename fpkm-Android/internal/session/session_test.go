package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestIssueAndVerify 基本签发/校验
func TestIssueAndVerify(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	tok, exp, err := s.Issue()
	if err != nil {
		t.Fatal(err)
	}
	if tok == "" {
		t.Fatal("令牌不应为空")
	}
	if time.Until(exp) < TTL-time.Minute {
		t.Errorf("过期时间异常: %v", exp)
	}
	if !s.Verify(tok) {
		t.Error("刚签发的令牌应校验通过")
	}
}

// TestRejectTampered 篡改令牌必须被拒绝
func TestRejectTampered(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)
	tok, _, _ := s.Issue()

	cases := map[string]string{
		"空字符串":   "",
		"无签名部分":  tok[:len(tok)-6],
		"乱改签名":   tok[:len(tok)-4] + "AAAA",
		"去掉点号":   tok[:len(tok)/2] + tok[len(tok)/2+1:],
		"伪造的载荷":  "eyJlIjoxOTAwMDAwMDAwLCJpIjoiZmFrZSJ9.AAAA",
		"只有点号":   ".",
		"随机字符串":  "not-a-token",
		"载荷被改过期": tamperPayload(tok),
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			if s.Verify(bad) {
				t.Errorf("%s 不应通过校验", name)
			}
		})
	}

	// 原令牌仍应有效，确认测试本身没有破坏它
	if !s.Verify(tok) {
		t.Error("原令牌应仍然有效")
	}
}

// TestExpiry 过期令牌应被拒绝
func TestExpiry(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)

	// 手工构造一个已过期的令牌（用真实密钥签名）
	enc := b64(`{"e":1000000000,"i":"expired"}`)
	s.mu.Lock()
	tok := enc + "." + s.signLocked(enc)
	s.mu.Unlock()

	if s.Verify(tok) {
		t.Error("已过期的令牌不应通过校验")
	}
}

// TestRevoke 登出后令牌立即失效
func TestRevoke(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)

	tok, _, _ := s.Issue()
	if !s.Verify(tok) {
		t.Fatal("签发后应有效")
	}
	s.Revoke(tok)
	if s.Verify(tok) {
		t.Error("吊销后不应通过校验")
	}
}

// TestRevokeSharedAcrossProcesses 吊销状态必须跨进程可见。
//
// 这正是本包存在的理由：`fpkm web` 与 `fpkm gateway --run` 是两个进程，
// 网关必须能读到 Web 进程写下的吊销记录。
func TestRevokeSharedAcrossProcesses(t *testing.T) {
	dir := t.TempDir()

	web, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	gw, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	tok, _, _ := web.Issue()

	// 网关（另一个实例）应能校验 Web 签发的令牌
	if !gw.Verify(tok) {
		t.Fatal("网关应能校验 Web 进程签发的令牌（共享密钥）")
	}

	// Web 登出后，网关重新加载吊销列表应立刻拒绝
	web.Revoke(tok)
	gw.loadRevoked()
	if gw.Verify(tok) {
		t.Error("Web 吊销后，网关应立即拒绝该令牌")
	}
}

// TestRevokeAll 改密后所有旧令牌失效（通过换密钥）
func TestRevokeAll(t *testing.T) {
	dir := t.TempDir()
	web, _ := New(dir)
	gw, _ := New(dir)

	tok, _, _ := web.Issue()
	if !gw.Verify(tok) {
		t.Fatal("初始应有效")
	}

	if err := web.RevokeAll(); err != nil {
		t.Fatal(err)
	}

	// 网关刷新密钥后旧令牌应失效
	gw.reloadSecretIfChanged()
	if gw.Verify(tok) {
		t.Error("换密钥后旧令牌不应通过校验")
	}

	// 新签发的令牌应有效
	nt, _, err := web.Issue()
	if err != nil {
		t.Fatal(err)
	}
	if !gw.Verify(nt) {
		t.Error("新令牌应有效")
	}
}

// TestSecretRotatedOnDisk 磁盘密钥被替换后，旧令牌自动失效
func TestSecretRotatedOnDisk(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)
	tok, _, _ := s.Issue()
	if !s.Verify(tok) {
		t.Fatal("初始应有效")
	}

	// 模拟"删掉 BaseDir 重来"：直接换掉密钥文件
	if err := os.WriteFile(SecretPath(dir), make([]byte, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	if s.Verify(tok) {
		t.Error("密钥变更后旧令牌不应通过校验")
	}
}

// TestSecretFilePermissions 密钥文件必须是 0600
func TestSecretFilePermissions(t *testing.T) {
	dir := t.TempDir()
	if _, err := New(dir); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, secretFile))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("密钥权限 = %o, 期望 600", perm)
	}
}

// ---------- 测试辅助 ----------

func tamperPayload(tok string) string {
	i := lastIndexByte(tok, '.')
	// 换一个签名同样长度的伪造载荷
	return b64(`{"e":1900000000,"i":"hacked"}`) + tok[i:]
}

func b64(s string) string {
	return base64RawURL([]byte(s))
}

// TestRevokeTakesEffectWithoutRestart 登出必须**立刻**生效，
// 不能等到重启网关。
//
// 这是一个实测发现的真实缺陷：早期实现只在启动时读一次吊销列表，
// 结果 Web 进程登出后，网关进程依然放行该令牌（返回 200 并泄露应用内容），
// 直到网关重启为止。此测试锁定该行为。
func TestRevokeTakesEffectWithoutRestart(t *testing.T) {
	dir := t.TempDir()

	// 模拟两个长期运行的进程
	web, _ := New(dir)
	gw, _ := New(dir)

	tok, _, err := web.Issue()
	if err != nil {
		t.Fatal(err)
	}
	if !gw.Verify(tok) {
		t.Fatal("前置条件：网关应能校验该令牌")
	}

	// Web 登出（网关**不重启**、也不显式重新加载）
	web.Revoke(tok)

	if gw.Verify(tok) {
		t.Error("登出后网关必须立刻拒绝该令牌（无需重启）")
	}
}

// TestRevokeOnlyTargetsThatToken 吊销一个令牌不应影响其它令牌
func TestRevokeOnlyTargetsThatToken(t *testing.T) {
	dir := t.TempDir()
	s, _ := New(dir)

	a, _, _ := s.Issue()
	b, _, _ := s.Issue()

	s.Revoke(a)

	if s.Verify(a) {
		t.Error("被吊销的令牌不应通过")
	}
	if !s.Verify(b) {
		t.Error("其它令牌不应受影响")
	}
}
