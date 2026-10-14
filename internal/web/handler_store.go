package web

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"fpkm/internal/app"
	"fpkm/internal/fpk"
	"fpkm/internal/logger"
)

// StoreApp 是返回给商店界面的应用条目（远程索引 + 本地安装状态）
type StoreApp struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Desc        string   `json:"desc"`
	Author      string   `json:"author"`
	Tags        []string `json:"tags"`
	Type        string   `json:"type"`
	Platform    string   `json:"platform"`
	UpdatedAt   string   `json:"updatedAt"`
	Size        int64    `json:"size"`
	Downloads   int      `json:"downloads"`
	RatingAvg   float64  `json:"ratingAvg"`
	RatingCount int      `json:"ratingCount"`
	Repo        string   `json:"repo"`
	SourceName  string   `json:"sourceName"`
	// Icon 远程图标地址（来自应用源）；本地已安装/已下载时前端优先用 /api/icons/{id}
	Icon string `json:"icon"`

	// 本地状态
	Installed        bool   `json:"installed"`
	InstalledVersion string `json:"installedVersion"`
	UpdateAvailable  bool   `json:"updateAvailable"`
	// PlatformSupported 当前机器能否运行
	PlatformSupported bool `json:"platformSupported"`
	// UnsupportedReason 确定不可运行的原因（为空表示可运行）
	UnsupportedReason string `json:"unsupportedReason,omitempty"`
	// PlatformWarning 软提示：平台标签看起来不匹配，但未核实，不阻止安装
	PlatformWarning string `json:"platformWarning,omitempty"`

	// 以下字段仅在应用已安装时填充，供列表页"打开"按钮直接使用
	Running bool       `json:"running"`
	Access  AccessInfo `json:"access,omitempty"`
	// AppName 本地安装的应用名（可能与商店 ID 不同）
	AppName string `json:"appName,omitempty"`
}

// handleStoreApps GET /api/store/apps?refresh=1
// 返回远程应用源中的全部应用，并附带本地安装状态。
func (s *Server) handleStoreApps(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "仅支持 GET")
		return
	}

	force := r.URL.Query().Get("refresh") == "1"
	if force {
		s.catalog.Invalidate()
	}

	apps, warn, err := s.catalog.List(force)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}

	// 已安装应用版本表
	installed := map[string]string{}
	if infos, err := s.mgr.List(); err == nil {
		for _, info := range infos {
			installed[info.Name] = info.Version
		}
	}

	arch := archName()

	// 已下载的包可以精确探测真实架构，优先于平台标签
	filter := newArchFilter(s.scanDownloadedPackages())

	out := make([]StoreApp, 0, len(apps))
	for _, a := range apps {
		v := StoreApp{
			ID:          a.ID,
			Name:        a.Name,
			Version:     a.Version,
			Desc:        a.Desc,
			Author:      a.Author,
			Tags:        splitTags(a.Tags),
			Type:        a.Type,
			Platform:    a.Platform,
			UpdatedAt:   a.UpdatedAt,
			Size:        a.Size,
			Downloads:   a.Downloads,
			RatingAvg:   a.RatingAvg,
			RatingCount: a.RatingCount,
			Repo:        a.Repo,
			SourceName:  a.SourceName,
			Icon:        a.Icon,
		}
		verdict := filter.check(a)
		v.PlatformSupported = verdict.ok
		v.UnsupportedReason = verdict.reason
		v.PlatformWarning = verdict.warning
		if iv, ok := installed[a.ID]; ok {
			v.Installed = true
			v.InstalledVersion = iv
			v.UpdateAvailable = compareVersion(a.Version, iv) > 0

			// 已安装的应用需要运行状态与访问地址，
			// 让商店列表的"打开"按钮无需再发一次请求。
			// 安装时使用的本地 appname 就是商店 ID（见 handleStoreInstall）。
			appName := a.ID
			v.AppName = appName

			if st, serr := s.mgr.Status(appName); serr == nil && st != nil {
				v.Running = st.Running
				env, _ := app.LoadEnvFile(s.cfg.AppEnvFile(appName))
				v.Access = describeAccessFor(s.cfg, appName, st.Port, env, s.cfg.AppDir(appName))
			}
		}
		out = append(out, v)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"apps":    out,
		"total":   len(out),
		"warning": warn,
		"arch":    arch,
	})
}

// handleSources GET/POST /api/store/sources
func (s *Server) handleSources(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list, err := s.catalog.store.Load()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"sources": list})

	case http.MethodPost:
		var req struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			URL     string `json:"url"`
			Enabled *bool  `json:"enabled"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
			return
		}
		req.URL = strings.TrimSpace(req.URL)
		if req.URL == "" {
			writeJSONError(w, http.StatusBadRequest, "源地址不能为空")
			return
		}
		u, err := url.Parse(req.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			writeJSONError(w, http.StatusBadRequest, "源地址必须是 http/https URL")
			return
		}

		list, err := s.catalog.store.Load()
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}

		enabled := true
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		name := strings.TrimSpace(req.Name)
		if name == "" {
			name = u.Host
		}
		id := strings.TrimSpace(req.ID)
		if id == "" {
			id = sanitizeFile(u.Host + u.Path)
		}

		// 已存在则更新，否则追加
		replaced := false
		for i := range list {
			if list[i].ID == id || list[i].URL == req.URL {
				list[i].Name = name
				list[i].URL = req.URL
				list[i].Enabled = enabled
				replaced = true
				break
			}
		}
		if !replaced {
			list = append(list, Source{ID: id, Name: name, URL: req.URL, Enabled: enabled})
		}

		if err := s.catalog.store.Save(list); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "保存失败: "+err.Error())
			return
		}
		s.catalog.Invalidate()
		writeJSON(w, http.StatusOK, map[string]any{"sources": list})

	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "仅支持 GET/POST")
	}
}

// handleSourceItem DELETE /api/store/sources/{id}
func (s *Server) handleSourceItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeJSONError(w, http.StatusMethodNotAllowed, "仅支持 DELETE")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/store/sources/")
	id, _ = url.PathUnescape(id)
	id = strings.Trim(id, "/")
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "缺少源 ID")
		return
	}

	list, err := s.catalog.store.Load()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := list[:0]
	found := false
	for _, src := range list {
		if src.ID == id {
			found = true
			continue
		}
		out = append(out, src)
	}
	if !found {
		writeJSONError(w, http.StatusNotFound, "源不存在: "+id)
		return
	}
	if err := s.catalog.store.Save(out); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "保存失败: "+err.Error())
		return
	}
	s.catalog.Invalidate()
	writeJSON(w, http.StatusOK, map[string]any{"sources": out})
}

// handleStoreInstall POST /api/store/install {id} 或 {ids:[...]}
// 下载远程包并安装（复用统一的安装任务流程）。
func (s *Server) handleStoreInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}

	var req struct {
		ID  string   `json:"id"`
		IDs []string `json:"ids"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}

	ids := req.IDs
	if req.ID != "" {
		ids = append(ids, req.ID)
	}
	if len(ids) == 0 {
		writeJSONError(w, http.StatusBadRequest, "缺少应用 ID")
		return
	}

	apps, _, err := s.catalog.List(false)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}

	// 按 ID 找到目标应用
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var targets []RemoteApp
	for _, a := range apps {
		if want[a.ID] {
			targets = append(targets, a)
		}
	}
	if len(targets) == 0 {
		writeJSONError(w, http.StatusNotFound, "在应用源中未找到指定应用")
		return
	}
	if len(targets) > 1 {
		writeJSONError(w, http.StatusBadRequest, "暂不支持一次下载多个应用")
		return
	}

	target := targets[0]
	if !platformSupported(target.Platform, archName()) {
		writeJSONError(w, http.StatusBadRequest,
			fmt.Sprintf("应用 %s 仅支持 %s 平台，当前为 %s", target.Name, target.Platform, archName()))
		return
	}

	// 原子占用执行权（下载+安装是长任务，且会写同一批目录）
	title := "安装 " + target.Name
	task, ok := s.tasks.TryBegin("install", title)
	if !ok {
		writeJSONError(w, http.StatusConflict, "已有操作正在执行（"+task.Title+"），请稍后再试")
		return
	}

	go func() {
		defer s.tasks.Clear(task)

		task.append(LogLevel("info"), fmt.Sprintf("正在下载 %s v%s …", target.Name, target.Version))
		if target.Size > 0 {
			task.append(LogLevel("info"), fmt.Sprintf("文件大小: %.1f MB", float64(target.Size)/1024/1024))
		}

		var lastPct int
		res, err := s.catalog.Download(s.cfg, target, func(done, total int64) {
			if total <= 0 {
				return
			}
			pct := int(done * 100 / total)
			// 每 10% 记一条，避免日志被刷屏
			if pct >= lastPct+10 {
				lastPct = pct
				task.append(LogLevel("info"), fmt.Sprintf("下载进度 %d%%", pct))
			}
		})
		if err != nil {
			task.append(LogLevel("error"), "下载失败: "+err.Error())
			task.finish(TaskFailed, err.Error())
			return
		}
		if res.Verified {
			task.append(LogLevel("info"), "校验和验证通过")
		}
		task.append(LogLevel("info"), fmt.Sprintf("下载完成: %s", res.Path))

		// 按包内真实二进制复核架构：标签可能不准（纯脚本应用会被误标为 arm）
		if report, aerr := fpk.InspectPackageArch(res.Path); aerr == nil && report != nil {
			if report.UnscriptOnlyUnsupported() {
				msg := fmt.Sprintf("安装包内仅含 %s 架构的二进制，无法在当前机器（%s）上运行",
					report.UnsupportedReason, archName())
				task.append(LogLevel("error"), msg)
				task.finish(TaskFailed, msg)
				return
			}
			if report.ScriptOnly {
				task.append(LogLevel("info"), "检测到纯脚本/前端应用，与 CPU 架构无关")
			} else if len(report.Archs) > 0 {
				task.append(LogLevel("info"), "包内二进制架构: "+strings.Join(archStrings(report.Archs), ", "))
			}
		}

		// 提取图标（供商店列表显示）
		if p := s.icons.ExtractFromPackage(remoteAppKey(target), res.Path); p != "" {
			task.append(LogLevel("debug"), "已提取应用图标")
		}

		// 复用安装/升级流程
		appname := remoteAppKey(target)
		if _, statErr := os.Stat(s.cfg.AppManifestFile(appname)); statErr == nil {
			task.append(LogLevel("info"), fmt.Sprintf("%s 已安装，执行升级流程", appname))
			if err := s.mgr.Upgrade(res.Path, nil); err != nil {
				task.append(LogLevel("error"), err.Error())
				task.finish(TaskFailed, err.Error())
				return
			}
		} else {
			if err := s.mgr.Install(res.Path, nil); err != nil {
				task.append(LogLevel("error"), err.Error())
				task.finish(TaskFailed, err.Error())
				return
			}
		}

		if s.autoStart {
			task.append(LogLevel("info"), "正在自动启动应用…")
			if err := s.mgr.Start(appname); err != nil {
				msg := fmt.Sprintf("应用已安装，但自动启动失败: %v", err)
				task.append(LogLevel("warn"), msg)
				task.finish(TaskSuccess, msg)
				return
			}
			task.append(LogLevel("info"), "应用已启动")
		}
		task.finish(TaskSuccess, "")
	}()

	writeJSON(w, http.StatusAccepted, map[string]any{
		"taskId": task.ID,
		"name":   target.Name,
	})
}

// handleStoreDownload POST /api/store/download {id}
//
// 只下载安装包（不安装），返回 taskId。
// 前端订阅该任务的日志流即可显示下载进度；下载完成后任务结果里
// 带上本地路径与包内向导，前端据此决定"弹向导"还是"直接安装"。
//
// 已下载过的包会立即完成（无需重复下载）。
func (s *Server) handleStoreDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}

	var req struct {
		ID string `json:"id"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求体解析失败: "+err.Error())
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		writeJSONError(w, http.StatusBadRequest, "缺少应用 ID")
		return
	}

	apps, _, err := s.catalog.List(false)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}

	var target *RemoteApp
	for i := range apps {
		if apps[i].ID == req.ID {
			target = &apps[i]
			break
		}
	}
	if target == nil {
		writeJSONError(w, http.StatusNotFound, "在应用源中未找到该应用")
		return
	}

	if !platformSupported(target.Platform, archName()) {
		writeJSONError(w, http.StatusBadRequest,
			fmt.Sprintf("应用 %s 仅支持 %s 平台，当前为 %s", target.Name, target.Platform, archName()))
		return
	}

	appKey := remoteAppKey(*target)
	title := "下载 " + target.Name

	task, ok := s.tasks.TryBegin("download", title)
	if !ok {
		writeJSONError(w, http.StatusConflict, "已有操作正在执行（"+task.Title+"），请稍后再试")
		return
	}

	go func() {
		defer s.tasks.Clear(task)

		// 已下载过就直接复用，避免重复下载
		expected := filepath.Join(downloadDir(s.cfg),
			fmt.Sprintf("%s-%s.fpk", sanitizeFile(appKey), sanitizeFile(target.Version)))
		if fileExistsSimple(expected) {
			if _, ierr := fpk.InspectPackage(expected, false); ierr == nil {
				task.append(LogLevel("info"), "安装包已在本地，跳过下载")
				task.setResult(s.buildDownloadResult(expected, *target))
				task.finish(TaskSuccess, "")
				return
			}
		}

		if target.Size > 0 {
			task.append(LogLevel("info"),
				fmt.Sprintf("开始下载 %s v%s（%.1f MB）", target.Name, target.Version, float64(target.Size)/1024/1024))
		} else {
			task.append(LogLevel("info"), fmt.Sprintf("开始下载 %s v%s", target.Name, target.Version))
		}

		var lastPct int
		res, derr := s.catalog.Download(s.cfg, *target, func(done, total int64) {
			task.setProgress(done, total)
			if total <= 0 {
				return
			}
			pct := int(done * 100 / total)
			// 每 10% 记一条，避免刷屏
			if pct >= lastPct+10 {
				lastPct = pct
				task.append(LogLevel("info"),
					fmt.Sprintf("下载进度 %d%%（%s / %s）", pct,
						humanSize(done), humanSize(total)))
			}
		})
		if derr != nil {
			task.append(LogLevel("error"), "下载失败: "+derr.Error())
			task.finish(TaskFailed, derr.Error())
			return
		}

		if res.Verified {
			task.append(LogLevel("info"), "校验和验证通过")
		}
		task.append(LogLevel("info"), "下载完成")

		// 提取图标供商店列表使用
		s.icons.ExtractFromPackage(appKey, res.Path)

		task.setResult(s.buildDownloadResult(res.Path, *target))
		task.finish(TaskSuccess, "")
	}()

	writeJSON(w, http.StatusAccepted, map[string]any{
		"taskId": task.ID,
		"name":   target.Name,
	})
}

// buildDownloadResult 组装下载任务的结果载荷（本地路径 + 包内向导）
func (s *Server) buildDownloadResult(pkgPath string, target RemoteApp) map[string]any {
	res := map[string]any{
		"path":    pkgPath,
		"name":    target.Name,
		"appname": remoteAppKey(target),
		"version": target.Version,
	}

	// 从包里读安装向导，供前端决定是否弹向导
	if data, err := fpk.ReadFileFromPackage(pkgPath, "wizard/install"); err == nil {
		if steps, perr := app.ParseWizard(data); perr == nil && len(steps) > 0 {
			dict := fpk.ReadI18nFromPackage(pkgPath)
			if len(dict) > 0 {
				steps = app.ResolveWizardI18nWithDict(steps, dict)
			}
			resp := buildWizardResponse(app.WizardInstall, steps, nil)
			res["wizard"] = resp
		}
	}
	return res
}

// handleIcon GET /api/icons/{key}
// 返回应用图标：优先包内 ICON.PNG，其次已安装目录，最后 302 到远程源图标。
func (s *Server) handleIcon(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "仅支持 GET")
		return
	}

	raw := strings.TrimPrefix(r.URL.Path, "/api/icons/")
	raw, _ = url.PathUnescape(raw)
	raw = strings.Trim(raw, "/")
	if raw == "" {
		http.NotFound(w, r)
		return
	}

	// 已安装应用：直接用安装目录里的图标
	if _, err := os.Stat(s.cfg.AppManifestFile(raw)); err == nil {
		if p := s.icons.Lookup(raw, s.cfg.AppDir(raw)); p != "" {
			serveIconFile(w, r, p)
			return
		}
	}

	// 本地已下载的包
	pkgName := raw + "-"
	for _, dir := range []string{downloadDir(s.cfg), s.cfg.AppsDir} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".fpk") {
				continue
			}
			if !strings.HasPrefix(e.Name(), pkgName) {
				continue
			}
			if p := s.icons.ExtractFromPackage(raw, filepath.Join(dir, e.Name())); p != "" {
				serveIconFile(w, r, p)
				return
			}
		}
	}

	// 回退：从应用源拉取远程图标并缓存到本地。
	// 这样前端只需一个地址，且第三方图床不可达时仍可用已缓存的图标。
	if p := s.fetchRemoteIcon(raw); p != "" {
		serveIconFile(w, r, p)
		return
	}

	http.NotFound(w, r)
}

// lookupRemoteApp 在应用源索引中按 id 或名称查找应用
func (s *Server) lookupRemoteApp(key string) (RemoteApp, bool) {
	apps, _, err := s.catalog.List(false)
	if err != nil {
		return RemoteApp{}, false
	}
	for _, a := range apps {
		if a.ID == key || remoteAppKey(a) == key || a.Name == key {
			return a, true
		}
	}
	return RemoteApp{}, false
}

// fetchRemoteIcon 下载并缓存远程图标，返回本地缓存路径。
// 失败返回空字符串；已缓存过则直接复用。
func (s *Server) fetchRemoteIcon(key string) string {
	cachePath := filepath.Join(s.cfg.BaseDir, "icons", sanitizeFile(key)+".png")

	// 已有缓存：直接返回，避免重复请求外部图床
	if fileExistsSimple(cachePath) {
		return cachePath
	}

	app, ok := s.lookupRemoteApp(key)
	if !ok || strings.TrimSpace(app.Icon) == "" {
		return ""
	}

	// 图标必须来自 http(s)，防止 file:// 之类的本地路径注入
	iconURL := strings.TrimSpace(app.Icon)
	if !strings.HasPrefix(iconURL, "http://") && !strings.HasPrefix(iconURL, "https://") {
		logger.Warn("忽略非法的图标地址: %s", iconURL)
		return ""
	}

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest(http.MethodGet, iconURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "fpkm/"+Version)

	resp, err := client.Do(req)
	if err != nil {
		logger.Debug("拉取远程图标失败 %s: %v", iconURL, err)
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Debug("拉取远程图标失败 %s: HTTP %d", iconURL, resp.StatusCode)
		return ""
	}

	if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
		return ""
	}
	out, err := os.OpenFile(cachePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return ""
	}
	defer out.Close()

	// 限制体积；同时校验确实是 PNG/JPEG，避免把 HTML 错误页当图片存下来
	head := make([]byte, 512)
	n, _ := io.ReadFull(resp.Body, head)
	if n < 8 || !looksLikeImage(head[:n]) {
		os.Remove(cachePath)
		logger.Debug("远程图标不是有效图片: %s", iconURL)
		return ""
	}
	if _, err := out.Write(head[:n]); err != nil {
		os.Remove(cachePath)
		return ""
	}
	if _, err := io.Copy(out, io.LimitReader(resp.Body, maxIconBytes)); err != nil {
		os.Remove(cachePath)
		return ""
	}

	logger.Debug("已缓存远程图标: %s", iconURL)
	return cachePath
}

// looksLikeImage 通过文件头判断是否为常见图片格式
func looksLikeImage(b []byte) bool {
	if len(b) < 4 {
		return false
	}
	// PNG
	if b[0] == 0x89 && b[1] == 0x50 && b[2] == 0x4E && b[3] == 0x47 {
		return true
	}
	// JPEG
	if b[0] == 0xFF && b[1] == 0xD8 {
		return true
	}
	// GIF
	if b[0] == 'G' && b[1] == 'I' && b[2] == 'F' {
		return true
	}
	// WEBP (RIFF....WEBP)
	if len(b) >= 12 && b[0] == 'R' && b[1] == 'I' && b[2] == 'F' && b[3] == 'F' {
		return true
	}
	return false
}

// serveIconFile 输出缓存中的图标文件
func serveIconFile(w http.ResponseWriter, r *http.Request, path string) {
	w.Header().Set("Content-Type", "image/png")
	// 图标基本不变，可长期缓存
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, path)
}

// remoteAppKey 远程应用的本地应用名。
// 飞牛第三方源的 id 有时是域名式（com.fnnas.xxx），
// 安装后 manifest 里的 appname 可能与 id 不同，这里优先用 id。
func remoteAppKey(app RemoteApp) string {
	if strings.TrimSpace(app.ID) != "" {
		return strings.TrimSpace(app.ID)
	}
	return sanitizeFile(app.Name)
}

// archName 当前架构（与 manifest 的 platform 字段对应）
func archName() string {
	return goArch()
}

// platformSupported 判断平台的兼容性。
// manifest/platform 常见取值：all / x86 / arm / amd64 / arm64
func platformSupported(platform, arch string) bool {
	p := strings.ToLower(strings.TrimSpace(platform))
	if p == "" || p == "all" {
		return true
	}
	switch arch {
	case "amd64":
		return strings.Contains(p, "x86") || strings.Contains(p, "amd64") || strings.Contains(p, "x64")
	case "arm64":
		return strings.Contains(p, "arm")
	}
	// 未知架构时不拦截，交给后续二进制兼容性检查
	return true
}

// archFilter 决定某个商店条目能否在当前机器上安装。
//
// 判断优先级：
//  1. **已下载到本地** → 直接扫描包内二进制架构（最可靠）；
//  2. 否则看 `platform` 标签。
//
// 之所以不能只信标签：应用源里的 platform 经常填错 ——
// 实测 CloudSaver / Cloudimgs / guandan 被标成 arm，
// 但包内没有任何原生二进制（纯 Node/脚本/前端），在 x86 上可以正常运行。
// 而 hand.nas.pro 标 arm 且确实含 aarch64 二进制，必须拦截。
type archFilter struct {
	// downloaded 已下载包的路径（按 appname 索引），用于精确探测
	downloaded map[string]string
	// cache 探测结果缓存（appname → 是否可用 + 原因）
	cache map[string]archVerdict
}

func newArchFilter(downloaded map[string]string) *archFilter {
	return &archFilter{downloaded: downloaded, cache: map[string]archVerdict{}}
}

// archVerdict 架构判断结果。
//
// 分三档，关键是区分"确定不支持"与"可能不支持"：
//   - ok=true, warning=""      → 可用
//   - ok=true, warning!=""     → 可以装，但平台标签看起来不匹配（软提示，不拦截）
//   - ok=false, reason!=""     → 确定不可用（已核实包内含其他架构的原生二进制）
type archVerdict struct {
	ok      bool
	warning string
	reason  string
}

// check 判断条目是否可安装。
//
// 只有在**已核实包内二进制确实不匹配**时才拦截；
// 仅凭 platform 标签不匹配时只给出提示，因为标签经常填错
// （实测 CloudSaver 等纯 Node 应用被标成 arm，实际可跨架构运行）。
func (f *archFilter) check(a RemoteApp) archVerdict {
	appKey := remoteAppKey(a)

	// 1) 有本地包 → 按真实二进制判断（唯一可硬拦截的依据）
	if pkgPath, ok := f.downloaded[appKey]; ok && pkgPath != "" {
		if v, hit := f.cache[appKey]; hit {
			return v
		}
		var verdict archVerdict
		report, err := fpk.InspectPackageArch(pkgPath)
		switch {
		case err != nil || report == nil:
			// 探测失败：退回标签，但只提示不拦截
			verdict = softVerdict(a)
		case report.ScriptOnly:
			// 纯脚本/前端：与架构无关，任何机器都能装
			verdict = archVerdict{ok: true}
		case report.UnsupportedReason != "":
			// 确有二进制且不匹配 → 硬拦截
			verdict = archVerdict{ok: false, reason: formatArchReason(report, a)}
		default:
			verdict = archVerdict{ok: true}
		}
		f.cache[appKey] = verdict
		return verdict
	}

	// 2) 没有本地包 → 只能依据标签，且**不拦截**，只给软提示
	return softVerdict(a)
}

// softVerdict 依据 platform 标签给出"可安装 + 可能不兼容"的软提示
func softVerdict(a RemoteApp) archVerdict {
	if platformSupported(a.Platform, archName()) {
		return archVerdict{ok: true}
	}
	p := strings.TrimSpace(a.Platform)
	return archVerdict{
		ok:      true,
		warning: fmt.Sprintf("应用源标注为 %s 架构，可能无法在当前机器（%s）上运行；安装时会按包内实际二进制校验", p, archName()),
	}
}

// formatArchReason 生成"不支持"的说明文案
func formatArchReason(report *fpk.PackageArchReport, a RemoteApp) string {
	archs := report.UnsupportedReason
	if archs == "" {
		archs = strings.TrimSpace(a.Platform)
	}
	return fmt.Sprintf("安装包内仅含 %s 架构的二进制", archs)
}

// scanDownloadedPackages 扫描本地已下载的安装包，建立 appname → 路径 索引。
// 用于按包内真实架构判断是否支持当前机器。
func (s *Server) scanDownloadedPackages() map[string]string {
	out := map[string]string{}
	dir := downloadDir(s.cfg)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".fpk") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		info, ierr := fpk.InspectPackage(full, false)
		if ierr != nil || info.Appname == "" {
			continue
		}
		out[info.Appname] = full
	}
	return out
}

// splitTags 拆分逗号分隔的标签
func splitTags(s string) []string {
	var out []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

// archStrings 把架构列表转成字符串切片（用于日志）
func archStrings(archs []fpk.Arch) []string {
	out := make([]string, 0, len(archs))
	for _, a := range archs {
		out = append(out, string(a))
	}
	return out
}
