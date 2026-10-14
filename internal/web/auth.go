package web

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"fpkm/internal/config"
)

// 密码存储采用 PBKDF2-HMAC-SHA256 + 随机盐。
//
// 迭代次数按 OWASP 对 PBKDF2-SHA256 的建议取 600000。
// 该强度下单次校验约需几十到几百毫秒，既能抵抗离线爆破，
// 又不至于让登录明显卡顿。
const (
	pwdIterations = 600000
	pwdKeyLen     = 32
	pwdSaltLen    = 16
)

// PasswordRecord 是持久化到磁盘的密码记录
type PasswordRecord struct {
	Algorithm  string `json:"algorithm"`
	Iterations int    `json:"iterations"`
	Salt       string `json:"salt"` // base64
	Hash       string `json:"hash"` // base64
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
}

// 登录失败限速：同一来源连续失败后指数退避，抵御在线爆破
const (
	loginMaxFailures = 5
	loginBaseLockout = 2 * time.Second
	loginMaxLockout  = 5 * time.Minute
)

// AuthManager 负责密码的设置、校验与登录限速
type AuthManager struct {
	path string

	mu       sync.Mutex
	locked   map[string]*loginAttempt
	sessions map[string]time.Time
}

type loginAttempt struct {
	failures int
	until    time.Time
}

// NewAuthManager 创建密码管理器，path 为密码记录文件路径
func NewAuthManager(path string) *AuthManager {
	return &AuthManager{
		path:     path,
		locked:   map[string]*loginAttempt{},
		sessions: map[string]time.Time{},
	}
}

// HasPassword 是否已设置密码（用于决定进"首次设置"还是"登录"页）
func (a *AuthManager) HasPassword() bool {
	rec, err := a.load()
	return err == nil && rec != nil && rec.Hash != ""
}

// load 读取密码记录，不存在返回 (nil, nil)
func (a *AuthManager) load() (*PasswordRecord, error) {
	data, err := os.ReadFile(a.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var rec PasswordRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("密码文件损坏: %w", err)
	}
	return &rec, nil
}

// SetPassword 设置或修改密码。若已存在密码，必须提供正确的旧密码。
func (a *AuthManager) SetPassword(oldPwd, newPwd string) error {
	if err := validatePassword(newPwd); err != nil {
		return err
	}

	existing, err := a.load()
	if err != nil {
		return err
	}

	if existing != nil && existing.Hash != "" {
		// 已设置过：必须验证旧密码
		if oldPwd == "" {
			return fmt.Errorf("请输入当前密码")
		}
		ok, verr := a.verifyRecord(existing, oldPwd)
		if verr != nil {
			return verr
		}
		if !ok {
			return fmt.Errorf("当前密码不正确")
		}
	}

	salt := make([]byte, pwdSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("生成随机盐失败: %w", err)
	}

	derived, err := pbkdf2.Key(sha256.New, newPwd, salt, pwdIterations, pwdKeyLen)
	if err != nil {
		return fmt.Errorf("派生密钥失败: %w", err)
	}

	now := time.Now().UnixMilli()
	created := now
	if existing != nil && existing.CreatedAt > 0 {
		created = existing.CreatedAt
	}

	rec := PasswordRecord{
		Algorithm:  "pbkdf2-hmac-sha256",
		Iterations: pwdIterations,
		Salt:       base64.StdEncoding.EncodeToString(salt),
		Hash:       base64.StdEncoding.EncodeToString(derived),
		CreatedAt:  created,
		UpdatedAt:  now,
	}

	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.path), 0700); err != nil {
		return err
	}
	// 0600：只有当前用户可读写
	if err := os.WriteFile(a.path, data, 0600); err != nil {
		return fmt.Errorf("保存密码失败: %w", err)
	}

	// 改密后使所有旧会话失效
	a.mu.Lock()
	a.sessions = map[string]time.Time{}
	a.mu.Unlock()

	return nil
}

// Verify 校验密码（含限速）。key 通常是客户端 IP。
func (a *AuthManager) Verify(key, pwd string) error {
	a.mu.Lock()
	att := a.locked[key]
	if att != nil && time.Now().Before(att.until) {
		wait := time.Until(att.until).Round(time.Second)
		a.mu.Unlock()
		return fmt.Errorf("尝试次数过多，请 %s 后再试", wait)
	}
	a.mu.Unlock()

	rec, err := a.load()
	if err != nil {
		return err
	}
	if rec == nil || rec.Hash == "" {
		return fmt.Errorf("尚未设置密码")
	}

	ok, verr := a.verifyRecord(rec, pwd)
	if verr != nil {
		return verr
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if ok {
		delete(a.locked, key)
		return nil
	}

	// 失败：累计并退避
	if a.locked[key] == nil {
		a.locked[key] = &loginAttempt{}
	}
	cur := a.locked[key]
	cur.failures++
	if cur.failures >= loginMaxFailures {
		backoff := loginBaseLockout << uint(cur.failures-loginMaxFailures)
		if backoff > loginMaxLockout {
			backoff = loginMaxLockout
		}
		cur.until = time.Now().Add(backoff)
	}
	return fmt.Errorf("密码不正确")
}

// verifyRecord 用记录中的参数校验密码（常量时间比较）
func (a *AuthManager) verifyRecord(rec *PasswordRecord, pwd string) (bool, error) {
	salt, err := base64.StdEncoding.DecodeString(rec.Salt)
	if err != nil {
		return false, fmt.Errorf("密码记录中的盐无效: %w", err)
	}
	want, err := base64.StdEncoding.DecodeString(rec.Hash)
	if err != nil {
		return false, fmt.Errorf("密码记录中的哈希无效: %w", err)
	}

	iter := rec.Iterations
	if iter <= 0 {
		iter = pwdIterations
	}
	keyLen := len(want)
	if keyLen == 0 {
		keyLen = pwdKeyLen
	}

	derived, err := pbkdf2.Key(sha256.New, pwd, salt, iter, keyLen)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(derived, want) == 1, nil
}

// ---------- 会话 ----------

// NewSession 登录成功后创建会话令牌
func (a *AuthManager) NewSession() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(buf)

	a.mu.Lock()
	a.sessions[token] = time.Now().Add(sessionTTL)
	a.mu.Unlock()
	return token, nil
}

// ValidSession 校验会话令牌
func (a *AuthManager) ValidSession(token string) bool {
	if token == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	exp, ok := a.sessions[token]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(a.sessions, token)
		return false
	}
	return true
}

// DropSession 注销会话
func (a *AuthManager) DropSession(token string) {
	a.mu.Lock()
	delete(a.sessions, token)
	a.mu.Unlock()
}

// sessionTTL 会话有效期
const sessionTTL = 7 * 24 * time.Hour

// validatePassword 密码强度校验
func validatePassword(pwd string) error {
	if len([]rune(pwd)) < 6 {
		return fmt.Errorf("密码至少 6 位")
	}
	if len([]rune(pwd)) > 128 {
		return fmt.Errorf("密码过长（最多 128 位）")
	}
	return nil
}

// HasPassword 判断指定配置下是否已设置管理密码
func HasPassword(cfg *config.Config) bool {
	return NewAuthManager(filepath.Join(cfg.BaseDir, "password.json")).HasPassword()
}

// ResetPassword 删除密码记录（忘记密码时的救援手段）。
// 返回是否确实删除了文件。
func ResetPassword(cfg *config.Config) (bool, error) {
	path := filepath.Join(cfg.BaseDir, "password.json")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if err := os.Remove(path); err != nil {
		return false, err
	}
	return true, nil
}
