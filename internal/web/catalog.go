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

// Load 读取所有源；文件不存在时返回内置默认源
func (s *SourceStore) Load() ([]Source, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return defaultSources(), nil
		}
		return nil, err
	}
	var out []Source
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("解析 sources.json 失败: %w", err)
	}
	if len(out) == 0 {
		return defaultSources(), nil
	}
	return out, nil
}

// Save 覆盖写入源列表
func (s *SourceStore) Save(list []Source) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}

// defaultSources 内置默认源（与飞牛第三方商店生态一致）
func defaultSources() []Source {
	return []Source{{
		ID:      "official",
		Name:    "FN软仓官方源",
		URL:     "http://nas.hhxs2025.top:5660",
		Enabled: true,
	}}
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
		seen    = map[string]bool{}
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
			// 同一应用出现在多个源时，先出现的优先
			key := apps[i].ID
			if key == "" {
				key = apps[i].Name
			}
			if seen[key] {
				continue
			}
			seen[key] = true
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
