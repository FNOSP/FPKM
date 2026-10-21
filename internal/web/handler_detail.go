package web

import (
	"net/http"
	"os"
	"strings"

	"fpkm/internal/app"
	"fpkm/internal/fpk"
)

// AppDetail 应用详情（已安装应用与应用商店条目共用同一结构）
type AppDetail struct {
	// Name 为应用标识（appname），详情页标题优先用 DisplayName
	Name        string   `json:"name"`
	DisplayName string   `json:"displayName"`
	Version     string   `json:"version"`
	Desc        string   `json:"desc"`
	Author      string   `json:"author"`
	Platform    string   `json:"platform"`
	Tags        []string `json:"tags"`

	// 展示元信息（商店来源才有）
	Downloads   int     `json:"downloads"`
	RatingAvg   float64 `json:"ratingAvg"`
	RatingCount int     `json:"ratingCount"`
	Size        int64   `json:"size"`
	UpdatedAt   string  `json:"updatedAt"`
	Repo        string  `json:"repo"`
	SourceName  string  `json:"sourceName"`
	Icon        string  `json:"icon"`

	// 本地状态
	// Dependencies 是 manifest 的 install_dep_apps 解析结果
	// （node / python / bun 等运行时依赖）。
	// 只有本地能读到 manifest 时才有值：已安装，或包已下载缓存。
	Dependencies []DepApp `json:"dependencies,omitempty"`

	Installed        bool       `json:"installed"`
	InstalledVersion string     `json:"installedVersion"`
	UpdateAvailable  bool       `json:"updateAvailable"`
	Running          bool       `json:"running"`
	PID              int        `json:"pid,omitempty"`
	Port             string     `json:"port,omitempty"`
	InstallPath      string     `json:"installPath"`
	Kind             string     `json:"kind"`
	Access           AccessInfo `json:"access"`

	// ID 商店条目的编号（用于安装）；已安装应用为空
	StoreID string `json:"storeId,omitempty"`
}

// handleAppDetail GET /api/detail?app=<name> 或 ?id=<storeId>
//
// 已安装应用用 app 查询；商店应用用 id 查询。
// 两者返回同一结构，方便详情页复用。
func (s *Server) handleAppDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "仅支持 GET")
		return
	}

	appname := strings.TrimSpace(r.URL.Query().Get("app"))
	storeID := strings.TrimSpace(r.URL.Query().Get("id"))

	switch {
	case appname != "":
		s.detailForInstalled(w, appname)
	case storeID != "":
		s.detailForStore(w, storeID)
	default:
		writeJSONError(w, http.StatusBadRequest, "需要提供 app 或 id 参数")
	}
}

// detailForInstalled 已安装应用的详情
func (s *Server) detailForInstalled(w http.ResponseWriter, name string) {
	name, err := safeAppName(name)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	manifest, err := fpk.ParseManifest(s.cfg.AppManifestFile(name))
	if err != nil {
		writeJSONError(w, http.StatusNotFound, "应用未安装或 manifest 缺失: "+name)
		return
	}
	deps := splitDepApps(manifest.InstallDepApps)

	detail := AppDetail{
		Name:             name,
		DisplayName:      cleanDisplayName(manifest.DisplayName, name),
		Version:          manifest.Version,
		Desc:             cleanPlaceholderText(manifest.Desc),
		Author:           manifest.Maintainer,
		Platform:         manifest.Platform,
		Installed:        true,
		InstalledVersion: manifest.Version,
		Dependencies:     s.resolveDepApps(deps),
		InstallPath:      s.cfg.AppDir(name),
	}

	if st, err := s.mgr.Status(name); err == nil && st != nil {
		detail.Running = st.Running
		detail.PID = st.PID
		detail.Port = st.Port
	}

	env, _ := app.LoadEnvFile(s.cfg.AppEnvFile(name))
	detail.Access = describeAccessFor(s.cfg, name, detail.Port, env, s.cfg.AppDir(name))
	detail.Kind = detail.Access.Kind

	// 与商店对应条目合并展示信息（下载量、图标、仓库等），并判断可升级
	if apps, _, err := s.catalog.List(false); err == nil {
		for _, a := range apps {
			if !storeAppMatches(a, name) {
				continue
			}
			detail.StoreID = a.ID
			detail.Downloads = a.Downloads
			detail.RatingAvg = a.RatingAvg
			detail.RatingCount = a.RatingCount
			detail.Size = a.Size
			detail.UpdatedAt = a.UpdatedAt
			detail.Repo = a.Repo
			detail.SourceName = a.SourceName
			detail.Icon = a.Icon
			detail.Tags = splitTags(a.Tags)
			if detail.Desc == "" {
				detail.Desc = cleanPlaceholderText(a.Desc)
			}
			if detail.Author == "" {
				detail.Author = a.Author
			}
			if compareVersion(a.Version, detail.InstalledVersion) > 0 {
				detail.UpdateAvailable = true
				detail.Version = a.Version
			}
			break
		}
	}

	writeJSON(w, http.StatusOK, detail)
}

// detailForStore 商店条目的详情（可能尚未安装）
func (s *Server) detailForStore(w http.ResponseWriter, storeID string) {
	apps, _, err := s.catalog.List(false)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}

	var found *RemoteApp
	for i := range apps {
		if apps[i].ID == storeID {
			found = &apps[i]
			break
		}
	}
	if found == nil {
		writeJSONError(w, http.StatusNotFound, "在应用源中未找到该应用")
		return
	}

	a := *found
	appKey := remoteAppKey(a)

	detail := AppDetail{
		Name:        appKey,
		StoreID:     a.ID,
		DisplayName: cleanDisplayName(a.Name, appKey),
		Version:     a.Version,
		Desc:        cleanPlaceholderText(a.Desc),
		Author:      a.Author,
		Platform:    a.Platform,
		Tags:        splitTags(a.Tags),
		Downloads:   a.Downloads,
		RatingAvg:   a.RatingAvg,
		RatingCount: a.RatingCount,
		Size:        a.Size,
		UpdatedAt:   a.UpdatedAt,
		Repo:        a.Repo,
		SourceName:  a.SourceName,
		Icon:        a.Icon,
	}

	// 未安装时若本地已缓存该包，提前展示依赖（安装前就知道要装哪些运行时）。
	// manifest 位于 gzip tar 内靠后位置，无法只取一小段，所以只在已缓存时读取，
	// 避免为了看依赖而下载整个包。
	if _, statErr := os.Stat(s.cfg.AppManifestFile(appKey)); statErr != nil {
		if pkg := s.cachedPackageFor(appKey); pkg != "" {
			if deps := depAppsForPackage(pkg); len(deps) > 0 {
				detail.Dependencies = s.resolveDepApps(deps)
			}
		}
	}

	// 合并本地已安装状态
	if manifest, err := fpk.ParseManifest(s.cfg.AppManifestFile(appKey)); err == nil {
		detail.Installed = true
		detail.InstalledVersion = manifest.Version
		detail.InstallPath = s.cfg.AppDir(appKey)
		detail.UpdateAvailable = compareVersion(a.Version, manifest.Version) > 0

		if st, err := s.mgr.Status(appKey); err == nil && st != nil {
			detail.Running = st.Running
			detail.PID = st.PID
			detail.Port = st.Port
		}
		env, _ := app.LoadEnvFile(s.cfg.AppEnvFile(appKey))
		detail.Access = describeAccessFor(s.cfg, appKey, detail.Port, env, s.cfg.AppDir(appKey))
		detail.Kind = detail.Access.Kind
	}

	writeJSON(w, http.StatusOK, detail)
}

// storeAppMatches 判断商店条目是否对应某个已安装应用。
// 源里的 id 与 manifest 的 appname 多数一致，但也存在差异，因此两种都比对。
func storeAppMatches(a RemoteApp, appname string) bool {
	if a.ID == appname || remoteAppKey(a) == appname {
		return true
	}
	// 用包里读出的 appname 兜底比对一次（源条目的 Name 有时是展示名）
	if strings.EqualFold(strings.TrimSpace(a.Name), appname) {
		return true
	}
	return false
}
