package web

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"fpkm/internal/fpk"
)

// FSEntry 文件系统条目
type FSEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
	Size  int64  `json:"size"`
	IsFPK bool   `json:"isFpk"`
	MTime int64  `json:"mtime"`
}

// FSListResult 目录浏览结果
type FSListResult struct {
	Path       string    `json:"path"`
	Parent     string    `json:"parent"`
	Entries    []FSEntry `json:"entries"`
	FPKCount   int       `json:"fpkCount"`
	Permission string    `json:"permission,omitempty"`
}

// handleFSList GET /api/fs/list?path=/xx
// 逐级浏览服务器文件系统，用于挑选本地 .fpk 文件
func (s *Server) handleFSList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "仅支持 GET")
		return
	}
	if !s.opts.AllowFS {
		writeJSONError(w, http.StatusForbidden, "服务器文件浏览已在启动参数中禁用")
		return
	}

	dir := r.URL.Query().Get("path")
	if dir == "" {
		// 默认从用户主目录开始
		home, err := os.UserHomeDir()
		if err != nil {
			home = s.cfg.BaseDir
		}
		dir = home
	}
	dir = filepath.Clean(dir)
	if !filepath.IsAbs(dir) {
		writeJSONError(w, http.StatusBadRequest, "必须是绝对路径")
		return
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("无法读取目录 %s: %v", dir, err))
		return
	}

	result := FSListResult{Path: dir, Parent: filepath.Dir(dir)}
	if result.Parent == dir {
		result.Parent = ""
	}
	// 初始化为空切片而非 nil：nil 会被 encoding/json 序列化成 null，
	// 前端对 null 做 .length 会抛错。
	result.Entries = make([]FSEntry, 0)

	for _, e := range entries {
		name := e.Name()
		// 跳过隐藏文件，减少噪音（仍可通过直接输入路径访问）
		if strings.HasPrefix(name, ".") {
			continue
		}

		full := filepath.Join(dir, name)
		entry := FSEntry{Name: name, Path: full, IsDir: e.IsDir()}

		// 目录也可能是符号链接，需要判断真实类型
		info, err := e.Info()
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				if st, serr := os.Stat(full); serr == nil {
					entry.IsDir = st.IsDir()
					entry.Size = st.Size()
					entry.MTime = st.ModTime().UnixMilli()
				}
			} else {
				entry.Size = info.Size()
				entry.MTime = info.ModTime().UnixMilli()
			}
		}

		if !entry.IsDir {
			if !strings.EqualFold(filepath.Ext(name), ".fpk") {
				continue // 非目录且非 .fpk 的项不展示，聚焦安装场景
			}
			entry.IsFPK = true
			result.FPKCount++
		}

		result.Entries = append(result.Entries, entry)
	}

	// 目录在前，同类按名称排序
	sort.Slice(result.Entries, func(i, j int) bool {
		a, b := result.Entries[i], result.Entries[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})

	writeJSON(w, http.StatusOK, result)
}

// handleFSInspect GET /api/fs/inspect?path=/xx/a.fpk
// 解析 .fpk 元信息（安装前预览），同时给出是否已安装/可升级
func (s *Server) handleFSInspect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "仅支持 GET")
		return
	}
	if !s.opts.AllowFS {
		writeJSONError(w, http.StatusForbidden, "服务器文件浏览已禁用")
		return
	}

	path := r.URL.Query().Get("path")
	if err := s.validatePackagePath(path); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	info, err := fpk.InspectPackage(path, false)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "解析安装包失败: "+err.Error())
		return
	}

	installedVersion := ""
	if m, err := fpk.ParseManifest(s.cfg.AppManifestFile(info.Appname)); err == nil && m != nil {
		installedVersion = m.Version
	}

	resp := map[string]any{
		"package":          info,
		"installed":        installedVersion != "",
		"installedVersion": installedVersion,
	}
	if installedVersion != "" {
		cmp := compareVersion(info.Version, installedVersion)
		resp["isDowngrade"] = cmp < 0
		resp["isSameVersion"] = cmp == 0
		resp["isUpgrade"] = cmp > 0
	}
	writeJSON(w, http.StatusOK, resp)
}
