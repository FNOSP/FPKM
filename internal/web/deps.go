package web

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"fpkm/internal/app"
	"fpkm/internal/fpk"
	"fpkm/internal/logger"
)

// maxDepDepth 依赖递归深度上限。
//
// 依赖链理论上可以无限嵌套（A 依赖 B、B 依赖 C…），也可能出现环。
// 用固定深度兜底，避免异常 manifest 让安装流程无限递归。
const maxDepDepth = 5

// DepApp 描述一个依赖应用的展示信息。
//
// 依赖关系来自 manifest 的 install_dep_apps 字段，值是应用名
// （如 python312、nodejs_v24、bunjs），飞牛商店把这些运行时
// 单独做成应用分发。
type DepApp struct {
	Appname   string `json:"appname"`
	Display   string `json:"name"`
	Version   string `json:"version"`
	Category  string `json:"category"`
	Icon      string `json:"icon"`
	Desc      string `json:"desc,omitempty"`
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	// StoreID 是依赖在应用源里的条目 ID，前端据此发起安装
	StoreID string `json:"storeId,omitempty"`
	// Available 表示应用源里能找到它（否则只能提示手动安装）
	Available bool `json:"available"`
}

// splitDepApps 解析 manifest 的 install_dep_apps。
//
// 真实写法是逗号分隔的应用名，容忍多余空格与重复项。
func splitDepApps(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' '
	}) {
		name := strings.TrimSpace(part)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// resolveDepApps 把依赖应用名解析成可展示的信息。
//
// 展示优先级：已安装的本地 manifest（有真实版本/图标）→ 应用源条目。
// 两者都找不到时仍返回条目（Available=false），让界面能提示用户。
func (s *Server) resolveDepApps(names []string) []DepApp {
	if len(names) == 0 {
		return nil
	}

	// 应用源按 appname/ID 建索引，供依赖匹配
	remoteByKey := map[string]RemoteApp{}
	if s.catalog != nil {
		if apps, _, err := s.catalog.List(false); err == nil {
			for _, ra := range apps {
				remoteByKey[remoteAppKey(ra)] = ra
				if ra.Name != "" {
					remoteByKey[ra.Name] = ra
				}
			}
		}
	}

	out := make([]DepApp, 0, len(names))
	for _, name := range names {
		dep := DepApp{Appname: name, Display: name}

		// 已安装：本地 manifest 最准确
		if m, err := fpk.ParseManifest(s.cfg.AppManifestFile(name)); err == nil {
			dep.Installed = true
			dep.Version = m.Version
			if d := cleanDisplayName(m.DisplayName, name); d != "" {
				dep.Display = d
			}
			dep.Desc = cleanPlaceholderText(m.Desc)
			pm := app.NewProcessManager(s.cfg.AppPidFile(name), s.cfg.AppLogFile(name))
			dep.Running = pm.IsRunning()
		}

		// 应用源补充分类/图标/版本（未安装时主要靠它）
		if ra, ok := remoteByKey[name]; ok {
			dep.Available = true
			dep.StoreID = ra.ID
			if dep.Version == "" {
				dep.Version = ra.Version
			}
			if dep.Display == name {
				dep.Display = cleanDisplayName(ra.Name, name)
			}
			dep.Category = firstTag(ra.Tags)
			dep.Icon = ra.Icon
			if dep.Desc == "" {
				dep.Desc = cleanPlaceholderText(ra.Desc)
			}
		}

		out = append(out, dep)
	}
	return out
}

// firstTag 取第一个标签作为分类（依赖应用通常只有一个，如"开发工具"）。
func firstTag(tags string) string {
	for _, t := range strings.FieldsFunc(tags, func(r rune) bool {
		return r == ',' || r == ';' || r == ' '
	}) {
		if v := strings.TrimSpace(t); v != "" {
			return v
		}
	}
	return ""
}

// cachedPackageFor 在已下载的安装包里找某个应用的包。
//
// 依赖信息只能从 manifest 读到，而 manifest 在包内（gzip tar）靠后，
// 无法只取一小段。所以详情页只在包已缓存时展示依赖，避免为了看依赖
// 就下载几百 MB。
func (s *Server) cachedPackageFor(appname string) string {
	dir := downloadDir(s.cfg)
	matches, err := filepath.Glob(filepath.Join(dir, sanitizeFile(appname)+"-*.fpk"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	// 取最新的一份
	newest := matches[0]
	var newestMod int64
	for _, m := range matches {
		if st, serr := os.Stat(m); serr == nil && st.ModTime().UnixNano() > newestMod {
			newest, newestMod = m, st.ModTime().UnixNano()
		}
	}
	return newest
}

// depAppsForPackage 读取指定安装包的依赖应用名。
//
// 用于商店详情页：应用尚未安装时，若本地已有缓存的包，就能提前
// 展示它依赖哪些运行时。
func depAppsForPackage(pkgPath string) []string {
	data, err := fpk.ReadFileFromPackage(pkgPath, "manifest")
	if err != nil {
		return nil
	}
	m, err := fpk.ParseManifestContent(data)
	if err != nil {
		return nil
	}
	return splitDepApps(m.InstallDepApps)
}

// ensureDependencies 安装主应用前，自动安装缺失的依赖应用。
//
// 为什么要自动装：manifest 声明了 install_dep_apps 的应用，缺依赖时
// 通常启动即失败（找不到 python3 / node），用户看到的是"装好了却用不了"。
// 飞牛原版应用中心会连带安装依赖，这里对齐该行为。
//
// 策略：
//   - 已安装的依赖直接跳过（不重装、不动它的版本）
//   - 应用源里找不到的依赖给出明确警告，但不阻断主应用安装
//     （部分依赖可能由用户手动提供）
//   - 递归处理依赖的依赖，深度受 maxDepDepth 限制
func (s *Server) ensureDependencies(task *Task, depNames []string, depth int) {
	if len(depNames) == 0 {
		return
	}
	if depth > maxDepDepth {
		task.append(LogLevel("warn"),
			fmt.Sprintf("依赖层级超过 %d 层，停止继续解析", maxDepDepth))
		return
	}

	for _, name := range depNames {
		// 已安装则跳过
		if _, err := os.Stat(s.cfg.AppManifestFile(name)); err == nil {
			task.append(LogLevel("info"), fmt.Sprintf("依赖 %s 已安装，跳过", name))
			continue
		}

		ra, ok := s.findStoreApp(name)
		if !ok {
			task.append(LogLevel("warn"),
				fmt.Sprintf("依赖 %s 在应用源中未找到，请手动安装后再使用该应用", name))
			continue
		}

		task.append(LogLevel("info"),
			fmt.Sprintf("安装依赖 %s v%s…", cleanDisplayName(ra.Name, name), ra.Version))

		pkgPath, err := s.ensureDownloaded(ra)
		if err != nil {
			task.append(LogLevel("warn"),
				fmt.Sprintf("依赖 %s 下载失败: %v（可稍后手动安装）", name, err))
			continue
		}

		// 包内真实 appname 可能与应用源 ID 不同，以包内 manifest 为准
		info, ierr := fpk.InspectPackage(pkgPath, false)
		if ierr != nil {
			task.append(LogLevel("warn"),
				fmt.Sprintf("依赖 %s 安装包解析失败: %v", name, ierr))
			continue
		}

		// 先递归装它的依赖
		if sub := splitDepApps(info.InstallDepApps); len(sub) > 0 {
			s.ensureDependencies(task, sub, depth+1)
		}

		if err := s.mgr.Install(pkgPath, nil); err != nil {
			task.append(LogLevel("warn"),
				fmt.Sprintf("依赖 %s 安装失败: %v（可稍后手动安装）", name, err))
			continue
		}
		task.append(LogLevel("info"),
			fmt.Sprintf("依赖 %s 安装完成", cleanDisplayName(ra.Name, name)))
	}
}

// findStoreApp 在应用源里按应用名/ID 查找条目。
func (s *Server) findStoreApp(appname string) (RemoteApp, bool) {
	if s.catalog == nil {
		return RemoteApp{}, false
	}
	apps, _, err := s.catalog.List(false)
	if err != nil {
		return RemoteApp{}, false
	}
	for _, ra := range apps {
		if remoteAppKey(ra) == appname || ra.Name == appname {
			return ra, true
		}
	}
	return RemoteApp{}, false
}

// ensureDownloaded 确保应用包已在本地缓存，返回其路径。
//
// 已缓存的包会被复用（与商店下载逻辑一致），避免重复下载。
func (s *Server) ensureDownloaded(ra RemoteApp) (string, error) {
	appKey := remoteAppKey(ra)
	expected := filepath.Join(downloadDir(s.cfg),
		fmt.Sprintf("%s-%s.fpk", sanitizeFile(appKey), sanitizeFile(ra.Version)))
	if fileExistsSimple(expected) {
		if _, err := fpk.InspectPackage(expected, false); err == nil {
			return expected, nil
		}
	}

	res, err := s.catalog.Download(s.cfg, ra, nil)
	if err != nil {
		return "", err
	}
	logger.Debug("依赖包已下载: %s", res.Path)
	return res.Path, nil
}
