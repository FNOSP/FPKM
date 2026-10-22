package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"fpkm/internal/config"
	"fpkm/internal/fpk"
	"fpkm/internal/logger"
)

// RemoteApp 是远程应用源索引中的一个应用条目。
// 字段与飞牛第三方源（/api/apps）返回的结构保持一致。
type RemoteApp struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Desc         string   `json:"desc"`
	Author       string   `json:"author"`
	Tags         string   `json:"tags"`
	Type         string   `json:"type"`
	Platform     string   `json:"platform"`
	UpdatedAt    string   `json:"updated_at"`
	Icon         string   `json:"icon"`
	DownloadURL  string   `json:"download_url"`
	DownloadURLs []string `json:"download_urls"`
	Size         int64    `json:"size"`
	SHA256       string   `json:"sha256"`
	Downloads    int      `json:"downloads"`
	RatingAvg    float64  `json:"rating_avg"`
	RatingCount  int      `json:"rating_count"`
	Repo         string   `json:"repo"`

	// 运行时补充：来自哪个源
	SourceID   string `json:"_source_id,omitempty"`
	SourceName string `json:"_source_name,omitempty"`
}

// Source 是一个应用源配置
type Source struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`

	// Builtin 标记该源由程序内置（不来自 sources.json）。
	// 内置源可以被停用，但不能被删除。
	Builtin bool `json:"builtin,omitempty"`
}

// SourceStore 管理应用源配置（持久化到 sources.json）
type SourceStore struct {
	path string
	mu   sync.Mutex
}

// NewSourceStore 创建应用源存储
func NewSourceStore(cfg *config.Config) *SourceStore {
	return &SourceStore{path: filepath.Join(cfg.BaseDir, "sources.json")}
}

// Load 返回「内置源 + 用户配置源」合并后的生效列表。
//
// 内置源永远在列、且排在最前：配置文件缺失、为空、内容损坏，或者里面
// 只剩已失效的历史源（例如测试留下的本地 Mock 源），都不会让内置源消失，
// 从而保证客户端开箱即有可用应用源，无需任何外部配置。
//
// 用户配置只能对内置源做覆盖（改名 / 改地址 / 停用），不能把它移除。
func (s *SourceStore) Load() ([]Source, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, err := s.readUserSources()
	if err != nil {
		return nil, err
	}
	return mergeSources(builtinSources(), user), nil
}

// readUserSources 读取用户配置（sources.json，不含内置源）。
//
// 调用方需持有 s.mu。文件不存在视为「无用户配置」；文件损坏时只记录告警
// 并退化为内置源，避免一份坏配置把整个应用商店拖成不可用。
func (s *SourceStore) readUserSources() ([]Source, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Source
	if err := json.Unmarshal(data, &out); err != nil {
		logger.Warn("解析 sources.json 失败，已改用内置源: %v", err)
		return nil, nil
	}
	return out, nil
}

// Save 只持久化「用户可编辑的部分」：自定义源 + 对内置源的覆盖项。
//
// 内置源由程序提供，与内置默认值一致时不落盘；仅当用户改了它的地址 /
// 名称，或把它停用时，才写一条覆盖项，使这些改动在重启后仍然保留。
func (s *SourceStore) Save(list []Source) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	user := make([]Source, 0, len(list))
	for _, src := range list {
		def, isBuiltin := builtinDefault(src.ID)
		if isBuiltin {
			if src.Enabled && src.URL == def.URL && src.Name == def.Name {
				continue // 与内置默认一致，无需落盘
			}
			user = append(user, Source{
				ID:      src.ID,
				Name:    src.Name,
				URL:     src.URL,
				Enabled: src.Enabled,
			})
			continue
		}
		user = append(user, Source{
			ID:      src.ID,
			Name:    src.Name,
			URL:     src.URL,
			Enabled: src.Enabled,
		})
	}

	data, err := json.MarshalIndent(user, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}

// builtinSources 返回随程序内置的应用源（与飞牛第三方商店生态一致）。
//
// 这些源编译进二进制，不依赖 data/sources.json，因此天然满足
// 「开箱可用」：新装、清空数据目录、旧配置只剩失效源时都能照常加载应用列表。
func builtinSources() []Source {
	return []Source{{
		ID:      "official",
		Name:    "FN软仓官方源",
		URL:     "http://nas.hhxs2025.top:5660",
		Enabled: true,
	}}
}

// builtinDefault 按 ID 查内置源定义。
func builtinDefault(id string) (Source, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Source{}, false
	}
	for _, b := range builtinSources() {
		if b.ID == id {
			return b, true
		}
	}
	return Source{}, false
}

// isBuiltinSourceID 判断给定 ID 是否属于内置源。
func isBuiltinSourceID(id string) bool {
	_, ok := builtinDefault(id)
	return ok
}

// mergeSources 合并内置源与用户配置：
//   - 内置源在前，并统一标记 Builtin；
//   - 与内置源同 ID 的用户项视为「覆盖」，可改名、改地址、停用；
//   - 其余用户项作为自定义源追加在后面。
func mergeSources(builtin, user []Source) []Source {
	out := make([]Source, 0, len(builtin)+len(user))
	index := map[string]int{}
	for _, b := range builtin {
		b.Builtin = true
		index[b.ID] = len(out)
		out = append(out, b)
	}

	for _, u := range user {
		if u.ID != "" {
			if i, ok := index[u.ID]; ok {
				if name := strings.TrimSpace(u.Name); name != "" {
					out[i].Name = name
				}
				if u2 := strings.TrimSpace(u.URL); u2 != "" {
					out[i].URL = u2
				}
				out[i].Enabled = u.Enabled
				continue
			}
			index[u.ID] = len(out)
		}
		out = append(out, u)
	}
	return out
}

// ---------- 索引拉取与缓存 ----------

// sourceIndex 是一个源的索引缓存
type sourceIndex struct {
	apps      []RemoteApp
	fetchedAt time.Time
	err       error
}

// Catalog 负责聚合多个应用源
type Catalog struct {
	store *SourceStore
	http  *http.Client

	mu      sync.Mutex
	cache   map[string]*sourceIndex
	ttl     time.Duration
	catalog []RemoteApp
	builtAt time.Time
	lastErr string
}

// NewCatalog 创建应用源聚合器
func NewCatalog(store *SourceStore) *Catalog {
	return &Catalog{
		store: store,
		http: &http.Client{
			Timeout: 25 * time.Second,
		},
		cache: map[string]*sourceIndex{},
		ttl:   5 * time.Minute,
	}
}

// appsAPI 从源地址推导索引接口。
// 源地址可能写根地址（http://host:port）也可能直接写完整接口地址。
func appsAPI(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(base, "/api/apps") {
		return base
	}
	return base + "/api/apps"
}

// List 返回所有启用源的应用（带缓存）
func (c *Catalog) List(force bool) ([]RemoteApp, string, error) {
	sources, err := c.store.Load()
	if err != nil {
		return nil, "", err
	}

	// 缓存仍有效且未强制刷新
	c.mu.Lock()
	if !force && c.catalog != nil && time.Since(c.builtAt) < c.ttl {
		out := c.catalog
		lastErr := c.lastErr
		c.mu.Unlock()
		return out, lastErr, nil
	}
	c.mu.Unlock()

	var (
		all     []RemoteApp
		errMsgs []string
		kept    = map[string]int{} // key -> 在 all 中的下标
	)

	for _, src := range sources {
		if !src.Enabled {
			continue
		}

		apps, ferr := c.fetchSource(src, force)
		if ferr != nil {
			logger.Warn("拉取应用源失败 %s (%s): %v", src.Name, src.URL, ferr)
			errMsgs = append(errMsgs, fmt.Sprintf("%s: %v", src.Name, ferr))
			continue
		}

		for i := range apps {
			apps[i].SourceID = src.ID
			apps[i].SourceName = src.Name
			// 同一应用可能出现多架构条目（同源内 x86/arm 各一条），
			// 优先保留与当前运行架构最匹配的条目，避免 arm64 手机上误留 x86。
			key := apps[i].ID
			if key == "" {
				key = apps[i].Name
			}
			score := archMatchScore(apps[i].Platform)
			if idx, ok := kept[key]; ok {
				if score > archMatchScore(all[idx].Platform) {
					all[idx] = apps[i]
				}
				continue
			}
			kept[key] = len(all)
			all = append(all, apps[i])
		}
	}

	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Downloads != all[j].Downloads {
			return all[i].Downloads > all[j].Downloads
		}
		return all[i].Name < all[j].Name
	})

	lastErr := strings.Join(errMsgs, "; ")

	c.mu.Lock()
	c.catalog = all
	c.builtAt = time.Now()
	c.lastErr = lastErr
	c.mu.Unlock()

	if len(all) == 0 && lastErr != "" {
		return nil, lastErr, fmt.Errorf("所有应用源均不可用: %s", lastErr)
	}
	return all, lastErr, nil
}

// fetchSource 拉取单个源的索引（带缓存）
func (c *Catalog) fetchSource(src Source, force bool) ([]RemoteApp, error) {
	c.mu.Lock()
	if idx, ok := c.cache[src.URL]; ok && !force && time.Since(idx.fetchedAt) < c.ttl {
		apps, err := idx.apps, idx.err
		c.mu.Unlock()
		return apps, err
	}
	c.mu.Unlock()

	apps, err := c.doFetch(src)

	c.mu.Lock()
	c.cache[src.URL] = &sourceIndex{apps: apps, fetchedAt: time.Now(), err: err}
	c.mu.Unlock()

	return apps, err
}

// doFetch 实际发起 HTTP 请求解析索引
func (c *Catalog) doFetch(src Source) ([]RemoteApp, error) {
	url := appsAPI(src.URL)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "fpkm/"+Version)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// 限制索引体积，避免异常源打爆内存
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}

	// 兼容两种形态：{"data":[...]} 或直接 [...]
	var wrapper struct {
		Data    []RemoteApp `json:"data"`
		Success *bool       `json:"success"`
	}
	if err := json.Unmarshal(body, &wrapper); err == nil && wrapper.Data != nil {
		return wrapper.Data, nil
	}

	var arr []RemoteApp
	if err := json.Unmarshal(body, &arr); err != nil {
		return nil, fmt.Errorf("索引格式无法识别: %w", err)
	}
	return arr, nil
}

// Invalidate 清空缓存（手动刷新时调用）
func (c *Catalog) Invalidate() {
	c.mu.Lock()
	c.cache = map[string]*sourceIndex{}
	c.catalog = nil
	c.builtAt = time.Time{}
	c.mu.Unlock()
}

// ---------- 下载 ----------

// downloadDir 返回远程包下载存放目录
func downloadDir(cfg *config.Config) string {
	return filepath.Join(cfg.BaseDir, "packages")
}

// DownloadResult 下载结果
type DownloadResult struct {
	Path     string
	Size     int64
	FromURL  string
	Verified bool
}

// Download 下载远程安装包到本地 packages 目录。
// onProgress 可为 nil；会依次尝试 download_urls 中的候选地址。
func (c *Catalog) Download(cfg *config.Config, app RemoteApp, onProgress func(done, total int64)) (*DownloadResult, error) {
	urls := candidateURLs(app)
	if len(urls) == 0 {
		return nil, fmt.Errorf("该应用未提供下载地址")
	}

	dir := downloadDir(cfg)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("创建下载目录失败: %w", err)
	}

	// 文件名：优先用 URL 里的原始名，保证扩展名与可读性
	name := fileNameFor(app)
	dst := filepath.Join(dir, name)

	var lastErr error
	for _, u := range urls {
		logger.Info("正在下载: %s", u)
		size, err := c.downloadOne(u, dst, onProgress)
		if err != nil {
			lastErr = err
			logger.Warn("下载失败，尝试下一个地址: %v", err)
			continue
		}

		// 校验包结构，避免把错误页/半截文件当安装包
		if _, err := fpk.InspectPackage(dst, false); err != nil {
			lastErr = fmt.Errorf("下载内容不是有效的 .fpk: %w", err)
			os.Remove(dst)
			continue
		}

		// 源提供了 sha256 时校验（目前多数源未提供）
		verified := false
		if app.SHA256 != "" {
			sum, err := fileSHA256(dst)
			if err != nil {
				os.Remove(dst)
				lastErr = err
				continue
			}
			if !strings.EqualFold(sum, app.SHA256) {
				os.Remove(dst)
				lastErr = fmt.Errorf("校验和不匹配（期望 %s，实际 %s）", app.SHA256, sum)
				continue
			}
			verified = true
		}

		return &DownloadResult{Path: dst, Size: size, FromURL: u, Verified: verified}, nil
	}

	return nil, fmt.Errorf("所有下载地址均失败: %v", lastErr)
}

// candidateURLs 收集候选下载地址（去重、过滤空值）
func candidateURLs(app RemoteApp) []string {
	var out []string
	seen := map[string]bool{}
	add := func(u string) {
		u = strings.TrimSpace(u)
		if u == "" || seen[u] {
			return
		}
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			return
		}
		seen[u] = true
		out = append(out, u)
	}
	add(app.DownloadURL)
	for _, u := range app.DownloadURLs {
		add(u)
	}
	return out
}

// fileNameFor 依据应用信息推导本地文件名
func fileNameFor(app RemoteApp) string {
	base := fmt.Sprintf("%s-%s.fpk", sanitizeFile(app.ID), sanitizeFile(app.Version))
	if app.ID == "" {
		base = fmt.Sprintf("%s-%s.fpk", sanitizeFile(app.Name), sanitizeFile(app.Version))
	}
	return base
}

// sanitizeFile 去掉文件名中的路径分隔符与危险字符
func sanitizeFile(s string) string {
	s = strings.TrimSpace(s)
	repl := strings.NewReplacer("/", "_", "\\", "_", "..", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	s = repl.Replace(s)
	if s == "" {
		s = "app"
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

// downloadOne 下载单个地址，先写临时文件再原子改名
func (c *Catalog) downloadOne(url, dst string, onProgress func(done, total int64)) (int64, error) {
	client := &http.Client{Timeout: 0} // 大包不限总时长，靠外层控制
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", "fpkm/"+Version)

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	tmp := dst + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return 0, err
	}

	total := resp.ContentLength
	var written int64
	buf := make([]byte, 256<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				os.Remove(tmp)
				return written, werr
			}
			written += int64(n)
			if onProgress != nil {
				onProgress(written, total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			os.Remove(tmp)
			return written, rerr
		}
	}

	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return written, err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return written, err
	}
	return written, nil
}

// goArch 返回当前 CPU 架构（与 manifest 的 platform 字段对照使用）
func goArch() string { return runtime.GOARCH }

// archMatchScore 评估条目的 platform 与当前运行架构的匹配度，分三档：
//   - 2：明确匹配当前架构
//   - 1：中性（all / 空 / 未知写法，不拦截也不挑拣）
//   - 0：明确是其他架构
//
// 用于同一应用存在多架构条目（如 x86 与 arm 各一条）时，
// 优先保留与当前机器一致的条目。口径与 platformSupported 保持一致。
func archMatchScore(platform string) int {
	return archMatchScoreFor(platform, goArch())
}

// archMatchScoreFor 与 archMatchScore 同口径，但架构可显式指定（便于单测）。
func archMatchScoreFor(platform, arch string) int {
	p := strings.ToLower(strings.TrimSpace(platform))
	if p == "" || p == "all" {
		return 1
	}
	switch arch {
	case "amd64":
		if strings.Contains(p, "x86") || strings.Contains(p, "amd64") || strings.Contains(p, "x64") {
			return 2
		}
	case "arm64":
		if strings.Contains(p, "arm") {
			return 2
		}
	}
	return 0
}

// fileSHA256 计算文件 sha256
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
