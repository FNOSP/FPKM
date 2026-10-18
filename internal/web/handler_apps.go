package web

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"fpkm/internal/app"
	"fpkm/internal/config"
	"fpkm/internal/fpk"
	"fpkm/internal/logger"
)

// AppView 返回给前端的应用信息
type AppView struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	DisplayName string `json:"displayName"`
	Desc        string `json:"desc"`
	Platform    string `json:"platform"`
	Dir         string `json:"dir"`
	HasUI       bool   `json:"hasUI"`
	IsDocker    bool   `json:"isDocker"`

	Running   bool   `json:"running"`
	PID       int    `json:"pid,omitempty"`
	Port      string `json:"port,omitempty"`
	EntryType string `json:"entryType,omitempty"`

	// Icon 远程源图标地址（本地提取失败时的兜底）
	Icon string `json:"icon"`
	// StoreID 商店条目编号（用于详情页/更新）
	StoreID string `json:"storeId,omitempty"`

	// Kind 应用接入方式：gateway(Unix Socket) / cgi / port / static / unknown
	Kind string `json:"kind"`
	// Access 访问参数：前端按当前页面的主机名拼出真实地址
	Access AccessInfo `json:"access"`

	// UpdateAvailable 当 packages 目录中存在同 appname 的更高版本时为 true
	UpdateAvailable bool   `json:"updateAvailable"`
	LatestVersion   string `json:"latestVersion,omitempty"`
	LatestPath      string `json:"latestPath,omitempty"`
}

// handleApps GET /api/apps
func (s *Server) handleApps(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "仅支持 GET")
		return
	}

	infos, err := s.mgr.List()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "读取应用列表失败: "+err.Error())
		return
	}

	// 扫描 packages 目录以推断可升级版本
	available, _ := s.scanPackages()

	// 商店索引：用 appname 建立映射，便于补充图标/条目 ID
	remoteByApp := map[string]RemoteApp{}
	if remoteApps, _, err := s.catalog.List(false); err == nil {
		for _, ra := range remoteApps {
			remoteByApp[remoteAppKey(ra)] = ra
			if ra.ID != "" {
				remoteByApp[ra.ID] = ra
			}
		}
	}

	views := make([]AppView, 0, len(infos))
	for _, info := range infos {
		v := AppView{
			Name:        info.Name,
			Version:     info.Version,
			DisplayName: info.DisplayName,
			Desc:        info.Desc,
			Platform:    info.Platform,
			Dir:         info.Dir,
			HasUI:       info.HasUI,
			IsDocker:    info.IsDocker,
		}
		// 部分第三方包的 display_name 是未解析的 i18n 占位符（如 ${common.displayName}），
		// 直接展示会很难看，这种情况回退到应用名。
		v.DisplayName = cleanDisplayName(v.DisplayName, info.Name)
		// desc 也可能是未解析的 i18n 占位符（如 ${common.desc}），一并清理
		v.Desc = cleanPlaceholderText(v.Desc)

		if st, err := s.mgr.Status(info.Name); err == nil {
			v.Running = st.Running
			v.PID = st.PID
			v.Port = st.Port
			v.EntryType = st.EntryType
		}

		env, _ := app.LoadEnvFile(s.cfg.AppEnvFile(info.Name))
		v.Access = describeAccessFor(s.cfg, info.Name, v.Port, env, s.cfg.AppDir(info.Name))
		v.Kind = v.Access.Kind

		// 从商店索引匹配远程图标与条目 ID，供前端作兜底显示与"更新"入口
		if remote, ok := remoteByApp[info.Name]; ok {
			v.Icon = remote.Icon
			v.StoreID = remote.ID
		}

		// 版本比对：仅在能解析出版本时提示
		if pkg, ok := available[info.Name]; ok && compareVersion(pkg.Version, info.Version) > 0 {
			v.UpdateAvailable = true
			v.LatestVersion = pkg.Version
			v.LatestPath = pkg.Path
		}

		views = append(views, v)
	}

	sort.Slice(views, func(i, j int) bool { return views[i].Name < views[j].Name })

	writeJSON(w, http.StatusOK, map[string]any{
		"apps":        views,
		"total":       len(views),
		"packagesDir": s.cfg.AppsDir,
	})
}

// handleAppAction 处理 POST /api/apps/{name}/{action}
// 支持 action: start | stop | uninstall | upgrade
func (s *Server) handleAppAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, "/api/apps/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		writeJSONError(w, http.StatusBadRequest, "路径格式应为 /api/apps/{应用名}/{动作}")
		return
	}

	name, err := safeAppName(parts[0])
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	action := parts[1]

	// 允许 upgrade 通过查询参数指定包路径
	pkgPathQuery := r.URL.Query().Get("path")

	// 卸载/升级可携带向导收集到的值；没有请求体时视为空
	wizardValues := map[string]string{}
	if action == "uninstall" || action == "upgrade" {
		var body struct {
			Values map[string]string `json:"values"`
		}
		// 解析失败（如没有 body）不报错，按空值处理
		_ = decodeJSONQuiet(r, &body)
		if body.Values != nil {
			wizardValues = body.Values
		}
	}

	switch action {
	case "start", "stop", "uninstall":
		// 原子占用执行权（TryBegin 内部已检查是否忙）
		task, ok := s.tasks.TryBegin(action, fmt.Sprintf("%s %s", actionLabel(action), name))
		if !ok {
			writeJSONError(w, http.StatusConflict, "已有操作正在执行（"+task.Title+"），请稍后再试")
			return
		}
		go func() {
			defer s.tasks.Clear(task)
			var opErr error
			switch action {
			case "start":
				opErr = s.mgr.Start(name)
				if opErr == nil {
					// 记录用户意图：FPKM 重启后应自动拉起该应用
					if aerr := s.autostart.mark(name); aerr != nil {
						logger.Warn("记录应用自启动失败（不影响运行）: %v", aerr)
					}
				}
			case "stop":
				opErr = s.mgr.Stop(name)
				// 用户主动停止：不再自动重启。即使 Stop 报错也要清记录，
				// 否则下次启动会违背用户意图把它拉起来。
				if uerr := s.autostart.unmark(name); uerr != nil {
					logger.Warn("清除应用自启动记录失败: %v", uerr)
				}
			case "uninstall":
				// 服务端按向导规则再校验一次（接口可被直接调用）
				if verr := validateWizardSubmission(s.cfg.AppDir(name), app.WizardUninstall, wizardValues); verr != nil {
					opErr = verr
					break
				}
				opErr = s.mgr.Uninstall(name, wizardValues)
				if opErr == nil {
					if uerr := s.autostart.unmark(name); uerr != nil {
						logger.Warn("清除应用自启动记录失败: %v", uerr)
					}
				}
			}
			if opErr != nil {
				task.append(LogLevel("error"), opErr.Error())
				task.finish(TaskFailed, opErr.Error())
				return
			}
			task.finish(TaskSuccess, "")
		}()
		writeJSON(w, http.StatusAccepted, map[string]any{"taskId": task.ID})

	case "upgrade":
		pkgPath := pkgPathQuery
		if pkgPath == "" {
			// 未指定则从 packages 目录自动挑选更高版本
			available, _ := s.scanPackages()
			pkg, ok := available[name]
			if !ok {
				writeJSONError(w, http.StatusBadRequest, "未在应用目录中找到该应用的新版本包，请指定 path 参数")
				return
			}
			pkgPath = pkg.Path
		}
		if err := s.validatePackagePath(pkgPath); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		task, ok := s.tasks.TryBegin("upgrade", "升级 "+name)
		if !ok {
			writeJSONError(w, http.StatusConflict, "已有操作正在执行（"+task.Title+"），请稍后再试")
			return
		}
		go func() {
			defer s.tasks.Clear(task)
			if err := s.mgr.Upgrade(pkgPath, wizardSetArgs(wizardValues)); err != nil {
				task.append(LogLevel("error"), err.Error())
				task.finish(TaskFailed, err.Error())
				return
			}
			task.finish(TaskSuccess, "")
		}()
		writeJSON(w, http.StatusAccepted, map[string]any{"taskId": task.ID})

	default:
		writeJSONError(w, http.StatusBadRequest, "不支持的动作: "+action)
	}
}

func actionLabel(a string) string {
	switch a {
	case "start":
		return "启动"
	case "stop":
		return "停止"
	case "uninstall":
		return "卸载"
	case "upgrade":
		return "升级"
	case "install":
		return "安装"
	}
	return a
}

// AccessInfo 描述应用的访问方式。
// 不返回写死 localhost 的完整 URL：网关类应用由浏览器按"当前页面"的
// 协议与主机名拼出地址（例如从 http://nas:7667 访问时，
// 应用地址应为 http://nas:7666/app/xxx，而不是 http://localhost:7666/app/xxx）。
type AccessInfo struct {
	Kind string `json:"kind"`
	// AccessPath 是"打开应用"应使用的路径，取自 ui/config 的 url 字段，
	// 可能与网关路由前缀不同（如 fnchat: 前缀 /app/fnchat，打开 /app/fnchat/）
	AccessPath string `json:"accessPath,omitempty"`
	// Prefix 是网关路由前缀（不带尾斜杠），主要用于展示与诊断
	Prefix string `json:"prefix,omitempty"`
	// AccessPort 是浏览器应访问的端口：网关类为网关端口，端口类为应用自身端口
	AccessPort int `json:"accessPort,omitempty"`
	// Scheme 目前固定 http（网关/应用均为明文 HTTP）
	Scheme string `json:"scheme,omitempty"`
	// LaunchMode 打开方式：iframe（桌面窗口内打开）或 tab（新标签页）。
	// 取自 ui/config 的 type 字段，见 app.ResolveLaunchMode。
	LaunchMode string `json:"launchMode,omitempty"`
	// Title ui/config 里的入口名（可作为桌面图标名）
	Title string `json:"title,omitempty"`
}

// describeAccess 判断应用的接入方式与访问参数。
// launch 由调用方通过 describeAccessFor 传入应用目录，用于读取 ui/config。
func describeAccess(cfg *config.Config, name, port string, env app.EnvMap) AccessInfo {
	return describeAccessFor(cfg, name, port, env, "")
}

// describeAccessFor 在 describeAccess 基础上读取 ui/config 的入口类型，
// 决定前端用子窗口（iframe）还是新标签页（tab）打开。
func describeAccessFor(cfg *config.Config, name, port string, env app.EnvMap, appDir string) AccessInfo {
	if env == nil {
		env = app.EnvMap{}
	}
	gatewayPort := cfg.GatewayPort
	if gatewayPort <= 0 {
		gatewayPort = 7666
	}

	// ui/config 的入口类型（缺省按新标签页处理）
	launchMode := string(app.LaunchTab)
	entryTitle := ""
	if appDir != "" {
		targetDir := filepath.Join(appDir, "target")
		launchMode = string(app.ResolveLaunchMode(targetDir, appDir))
		if entry, ok := app.ReadUIConfigEntry(targetDir, appDir); ok {
			entryTitle = strings.TrimSpace(entry.Title)
		}
	}

	// 网关 / CGI 应用：打开路径优先用 ui/config 的 url（env 中的 LAUNCH_PATH），
	// 老版本安装的应用没有该字段时回退到路由前缀并补尾斜杠。
	if env["GATEWAY_PREFIX"] != "" && (env["SOCKET_PATH"] != "" || env["CGI_PATH"] != "") {
		kind := "gateway"
		if env["CGI_PATH"] != "" {
			kind = "cgi"
		}
		path := env["LAUNCH_PATH"]
		if path == "" {
			path = app.EnsureTrailingSlash(env["GATEWAY_PREFIX"])
		}
		return AccessInfo{
			Kind:       kind,
			AccessPath: path,
			Prefix:     env["GATEWAY_PREFIX"],
			AccessPort: gatewayPort,
			Scheme:     "http",
			LaunchMode: launchMode,
			Title:      entryTitle,
		}
	}

	if port != "" {
		p, err := strconv.Atoi(port)
		if err != nil || p <= 0 {
			return AccessInfo{Kind: "unknown"}
		}
		return AccessInfo{
			Kind:       "port",
			AccessPath: "/",
			AccessPort: p,
			Scheme:     "http",
			LaunchMode: launchMode,
			Title:      entryTitle,
		}
	}
	return AccessInfo{Kind: "unknown"}
}

// cleanDisplayName 清理展示名：空值或未解析的占位符都回退到应用名
func cleanDisplayName(name, fallback string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return fallback
	}
	// 形如 ${common.displayName} / {0} / $VAR 的占位符
	if strings.Contains(name, "${") || strings.HasPrefix(name, "$") ||
		strings.Contains(name, "{{") {
		return fallback
	}
	return name
}

// cleanPlaceholderText 去掉纯占位符文本；含真实内容的文本原样保留
func cleanPlaceholderText(s string) string {
	t := strings.TrimSpace(s)
	if t == "" {
		return ""
	}
	// 整段就是占位符（可能多个拼接）时视为无内容
	trimmed := strings.NewReplacer("${", "", "}", "", "$", "", " ", "").Replace(t)
	if trimmed == "" {
		return ""
	}
	// 纯占位符形式：只包含 $ { } 字母数字和点
	isPlaceholderOnly := true
	for _, r := range t {
		if r == '$' || r == '{' || r == '}' || r == '.' || r == '_' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		isPlaceholderOnly = false
		break
	}
	if isPlaceholderOnly && strings.Contains(t, "${") {
		return ""
	}
	return t
}

// safeAppName 校验应用名，防止路径穿越
func safeAppName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("应用名不能为空")
	}
	if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return "", fmt.Errorf("非法的应用名")
	}
	return name, nil
}

// ---------- packages 目录扫描 ----------

// scanPackages 扫描应用目录与常见下载目录，索引可安装的 .fpk
// 返回 appname -> 版本最高的包
func (s *Server) scanPackages() (map[string]*fpk.PackageInfo, error) {
	dirs := []string{s.cfg.AppsDir}
	// 顺带扫描配置中记录的常用目录
	dirs = append(dirs, s.packageDirs()...)

	best := map[string]*fpk.PackageInfo{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".fpk") {
				continue
			}
			full := filepath.Join(dir, e.Name())
			info, err := fpk.InspectPackage(full, false)
			if err != nil || info.Appname == "" {
				continue
			}
			cur, ok := best[info.Appname]
			if !ok || compareVersion(info.Version, cur.Version) > 0 {
				best[info.Appname] = info
			}
		}
	}
	return best, nil
}

// packageDirs 返回额外的包扫描目录（去重、忽略不存在项）
func (s *Server) packageDirs() []string {
	candidates := []string{
		filepath.Join(s.cfg.BaseDir, "packages"),
		filepath.Join(s.cfg.BaseDir, "downloads"),
	}
	home, err := os.UserHomeDir()
	if err == nil {
		candidates = append(candidates,
			filepath.Join(home, "Downloads"),
			filepath.Join(home, "downloads"),
		)
	}
	var out []string
	seen := map[string]bool{}
	for _, c := range candidates {
		if c == "" || seen[c] {
			continue
		}
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// validatePackagePath 校验本地包路径可用性
func (s *Server) validatePackagePath(p string) error {
	if strings.TrimSpace(p) == "" {
		return fmt.Errorf("包路径不能为空")
	}
	st, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("文件不存在: %s", p)
	}
	if st.IsDir() {
		return fmt.Errorf("不能安装目录: %s", p)
	}
	if !strings.EqualFold(filepath.Ext(p), ".fpk") {
		return fmt.Errorf("仅支持 .fpk 文件")
	}
	return nil
}
