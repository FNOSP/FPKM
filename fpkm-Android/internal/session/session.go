// Package session 提供 Web 服务与统一网关**跨进程共享**的会话令牌。
//
// 为什么不能用原来的内存会话：
//
//	`fpkm web` 与 `fpkm gateway --run` 是两个独立进程，Web 进程内存里的
//	会话表网关读不到。而网关要独立判断"这个请求是否已登录 FPKM"，
//	就必须能自己完成校验。
//
// 方案：令牌采用 **HMAC 签名的自包含格式**，签名密钥落盘共享。
// 网关只需读同一个密钥文件即可本地验签，无需回调 Web 进程。
//
// 令牌格式（两段，点号分隔，均为 base64url）：
//
//	<payload>.<signature>
//
// payload 是紧凑的 JSON，包含到期时间与随机 id：
//
//	{"e":1730000000,"i":"<random>"}
//
// 签名 = HMAC-SHA256(secret, payload)。校验时：
//  1. 重新计算签名并做**常量时间**比较（防时序侧信道）；
//  2. 检查 e（过期时间）是否已过。
//
// 为支持"登出后立即失效"与"改密后踢掉所有会话"，另维护一个
// 轻量的吊销列表文件（记录被吊销的令牌 id 及其自然过期时间），
// 由两个进程共同读取。吊销列表只增不减，但条目会在自然过期后清理。
package session

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// TTL 会话有效期（与旧的内存会话保持一致）
const TTL = 7 * 24 * time.Hour

// 文件名
const (
	secretFile  = "session.key"     // HMAC 签名密钥
	revokedFile = "session.revoked" // 吊销列表
)

// payload 令牌载荷（字段名尽量短，减小 Cookie 体积）
type payload struct {
	Exp int64  `json:"e"` // 过期时间（Unix 秒）
	ID  string `json:"i"` // 令牌唯一 id，用于吊销
}

// Store 会话存储。进程内并发安全；跨进程通过文件共享。
type Store struct {
	dir string

	mu      sync.Mutex
	secret  []byte
	revoked map[string]int64 // token id -> 自然过期时间（Unix 秒）
	// revokedMod 上次读取吊销列表时文件的修改时间，
	// 用于避免每个请求都做一次文件 I/O，又能及时感知其它进程的吊销操作。
	revokedMod time.Time
}

// New 创建（或加载）会话存储。dir 通常是 cfg.BaseDir。
func New(dir string) (*Store, error) {
	s := &Store{dir: dir, revoked: map[string]int64{}}
	if err := s.loadSecret(); err != nil {
		return nil, err
	}
	s.loadRevoked()
	return s, nil
}

// loadSecret 读取密钥；不存在则生成并以 0600 落盘。
//
// 两个进程谁先启动都可以：谁先跑谁生成，另一个直接读取。
func (s *Store) loadSecret() error {
	path := filepath.Join(s.dir, secretFile)

	data, err := os.ReadFile(path)
	if err == nil && len(data) > 0 {
		s.secret = data
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("读取会话密钥失败: %w", err)
	}

	// 生成 32 字节随机密钥
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Errorf("生成会话密钥失败: %w", err)
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}
	// 0600：只有属主可读写（两个进程同属主）
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		return fmt.Errorf("保存会话密钥失败: %w", err)
	}
	s.secret = buf
	return nil
}

// reloadSecretIfChanged 重新读取密钥文件。
//
// 用于处理"密钥被重建"（例如用户删掉 BaseDir 重来）的情况：
// 发现磁盘密钥与内存不一致时立即采用新密钥，
// 这样旧令牌会自动失效，而不是继续被错误地接受。
func (s *Store) reloadSecretIfChanged() {
	path := filepath.Join(s.dir, secretFile)
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return
	}
	if !hmac.Equal(data, s.secret) {
		s.secret = data
	}
}

// loadRevoked 读取吊销列表（启动时调用）
func (s *Store) loadRevoked() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadRevokedLocked()
}

// reloadRevokedLocked 重新读取吊销列表（调用方需持有锁）。
//
// 这一步至关重要：Web 进程登出时会写入吊销记录，而网关是**另一个进程**。
// 如果只在启动时读一次，登出就不会立刻生效 —— 用户点了登出，
// 网关却依然放行，直到网关重启为止。这是实测发现的真实缺陷。
//
// 这里**每次校验都读一次文件**，而不是用 mtime 做缓存判断：
//   - 文件通常不存在（无人登出）→ 一次 stat 级的 ENOENT，开销极小；
//   - 存在时也很小（只有被吊销的令牌 id）；
//   - 换来的是"登出立刻生效"的确定性，避免文件系统时间戳粒度
//     导致的漏判 —— 对安全判定而言，可靠性优先于这点微优化。
func (s *Store) reloadRevokedLocked() {
	data, err := os.ReadFile(filepath.Join(s.dir, revokedFile))
	if err != nil {
		s.revoked = map[string]int64{}
		s.revokedMod = time.Time{}
		return
	}
	var m map[string]int64
	if json.Unmarshal(data, &m) != nil {
		// 文件损坏时保守处理：视为"有未知吊销项"？
		// 不 —— 无法解析就无法判断哪些被吊销；此时保留已有内存副本，
		// 至少不会因为一个坏文件把所有会话误判为有效。
		return
	}
	s.revoked = m
}

// persistRevoked 写回吊销列表（调用方需持有锁）
func (s *Store) persistRevokedLocked() {
	// 顺手清理已自然过期的条目，避免文件无限增长
	now := time.Now().Unix()
	for id, exp := range s.revoked {
		if exp > 0 && exp < now {
			delete(s.revoked, id)
		}
	}
	data, err := json.Marshal(s.revoked)
	if err != nil {
		return
	}
	path := filepath.Join(s.dir, revokedFile)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return
	}
	// 记录自己写入后的 mtime，避免下次校验时重复读取
	if fi, err := os.Stat(path); err == nil {
		s.revokedMod = fi.ModTime()
	}
}

// ---------- 令牌签发与校验 ----------

// Issue 签发一个新令牌
func (s *Store) Issue() (string, time.Time, error) {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return "", time.Time{}, fmt.Errorf("生成令牌失败: %w", err)
	}
	exp := time.Now().Add(TTL)

	p := payload{Exp: exp.Unix(), ID: base64.RawURLEncoding.EncodeToString(id)}
	body, err := json.Marshal(p)
	if err != nil {
		return "", time.Time{}, err
	}
	enc := base64.RawURLEncoding.EncodeToString(body)

	s.mu.Lock()
	sig := s.signLocked(enc)
	s.mu.Unlock()

	return enc + "." + sig, exp, nil
}

// Verify 校验令牌是否有效（签名正确、未过期、未被吊销）
func (s *Store) Verify(token string) bool {
	if token == "" {
		return false
	}

	i := lastIndexByte(token, '.')
	if i <= 0 || i == len(token)-1 {
		return false
	}
	enc, sig := token[:i], token[i+1:]

	s.mu.Lock()
	// 令牌校验是高频路径，趁持有锁时顺便刷新：
	//   - 密钥被重建 → 旧令牌立刻失效；
	//   - 其它进程写了吊销记录 → 登出立刻生效。
	s.reloadSecretIfChanged()
	s.reloadRevokedLocked()
	want := s.signLocked(enc)
	revoked := s.revoked
	s.mu.Unlock()

	// 常量时间比较，避免通过响应时间推断签名
	if subtle.ConstantTimeCompare([]byte(sig), []byte(want)) != 1 {
		return false
	}

	body, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return false
	}
	var p payload
	if json.Unmarshal(body, &p) != nil {
		return false
	}
	if p.Exp <= 0 || time.Now().Unix() >= p.Exp {
		return false
	}
	if _, dead := revoked[p.ID]; dead {
		return false
	}
	return true
}

// Revoke 吊销单个令牌（登出）。无法解析的令牌直接忽略。
func (s *Store) Revoke(token string) {
	p, ok := parse(token)
	if !ok {
		return
	}
	s.mu.Lock()
	s.revoked[p.ID] = p.Exp
	s.persistRevokedLocked()
	s.mu.Unlock()
}

// RevokeAll 吊销全部已签发令牌（改密后调用）。
//
// 由于令牌是无状态签名，无法枚举已有令牌；这里改用**换密钥**的方式：
// 生成新密钥落盘。所有旧令牌的签名随即失效，
// 两个进程都会通过 reloadSecretIfChanged 感知到新密钥。
func (s *Store) RevokeAll() error {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Errorf("生成新密钥失败: %w", err)
	}
	if err := os.WriteFile(filepath.Join(s.dir, secretFile), buf, 0o600); err != nil {
		return fmt.Errorf("轮换会话密钥失败: %w", err)
	}
	s.mu.Lock()
	s.secret = buf
	s.revoked = map[string]int64{}
	s.persistRevokedLocked()
	s.mu.Unlock()
	return nil
}

// signLocked 计算签名（调用方需持有锁）
func (s *Store) signLocked(enc string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(enc))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// parse 解析令牌载荷（不校验签名，仅供内部吊销使用）
func parse(token string) (payload, bool) {
	i := lastIndexByte(token, '.')
	if i <= 0 {
		return payload{}, false
	}
	body, err := base64.RawURLEncoding.DecodeString(token[:i])
	if err != nil {
		return payload{}, false
	}
	var p payload
	if json.Unmarshal(body, &p) != nil {
		return payload{}, false
	}
	return p, p.ID != "" && p.Exp > 0
}

// lastIndexByte 从后往前找字符（避免引入 strings 仅为此一个函数）
func lastIndexByte(s string, c byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// ---------- 供诊断/测试使用 ----------

// Stats 返回当前吊销条目数（按过期时间排序后返回，便于测试断言）
func (s *Store) Stats() (revokedIDs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.revoked))
	for id := range s.revoked {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// SecretPath 会话密钥文件路径（供日志/排查使用）
func SecretPath(dir string) string { return filepath.Join(dir, secretFile) }

// base64RawURL 暴露给测试使用的编码函数（避免测试包重复实现）
func base64RawURL(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
