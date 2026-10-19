package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"fpkm/internal/app"
)

// handleAccessGet 返回某个应用当前的接入方式设置。
//
//	GET /api/access?app={name}
//
// 返回 mode 为空表示"跟随 ui/config"（未覆盖）。
func (s *Server) handleAccessGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "仅支持 GET")
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("app"))
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "缺少 app 参数")
		return
	}
	if _, err := safeAppName(name); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	ov := app.GetAccessOverride(s.cfg, name)
	writeJSON(w, http.StatusOK, map[string]any{
		"appName": name,
		"mode":    string(ov.Mode),
		"port":    ov.Port,
		"path":    ov.Path,
	})
}

// handleAccessSet 设置应用的接入方式。
//
//	POST /api/access/set
//	{"app":"Lucky","mode":"port","port":16601,"path":"/"}
//
// mode 取值：auto（清除覆盖）/ port / gateway / cgi
func (s *Server) handleAccessSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "仅支持 POST")
		return
	}

	var body struct {
		App  string `json:"app"`
		Mode string `json:"mode"`
		// Port 允许传数字或字符串（前端输入框是文本）
		Port json.RawMessage `json:"port"`
		Path string          `json:"path"`
	}
	if err := decodeJSONQuiet(r, &body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "请求体格式错误")
		return
	}

	name := strings.TrimSpace(body.App)
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "缺少 app 参数")
		return
	}
	if _, err := safeAppName(name); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	mode := app.AccessMode(strings.TrimSpace(body.Mode))
	switch mode {
	case app.AccessAuto, app.AccessPort, app.AccessGateway, app.AccessCGI:
	default:
		writeJSONError(w, http.StatusBadRequest, "mode 只能是 auto / port / gateway / cgi")
		return
	}

	port := parseLooseInt(body.Port)
	if mode == app.AccessPort {
		if port <= 0 || port > 65535 {
			writeJSONError(w, http.StatusBadRequest, "端口应用需要填写 1-65535 的端口")
			return
		}
	}

	path := strings.TrimSpace(body.Path)
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	ov := app.AccessOverride{Mode: mode, Port: port, Path: path}
	if mode == app.AccessAuto {
		ov = app.AccessOverride{}
	}
	if err := app.SetAccessOverride(s.cfg, name, ov); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "保存设置失败: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"appName": name,
		"mode":    string(mode),
		"port":    ov.Port,
		"path":    ov.Path,
	})
}

// parseLooseInt 解析可能是数字字面量或带引号字符串的整数。
// 前端输入框传字符串更自然，这里两种都接受。
func parseLooseInt(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var n int
	if err := json.Unmarshal(raw, &n); err == nil {
		return n
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		s = strings.TrimSpace(s)
		// 只取前导数字，容忍用户输入 "16601 " 之类
		if v, err := strconv.Atoi(s); err == nil {
			return v
		}
	}
	return 0
}
