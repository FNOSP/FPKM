package web

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"fpkm/internal/app"
	"fpkm/internal/fpk"
	"fpkm/internal/logger"
)

// WizardItemView 是返回给前端的向导字段
type WizardItemView struct {
	Type        string             `json:"type"`
	Field       string             `json:"field"`
	Label       string             `json:"label"`
	HelpText    string             `json:"helpText"`
	Placeholder string             `json:"placeholder"`
	InitValue   string             `json:"initValue"`
	Options     []app.WizardOption `json:"options"`
	Rules       []app.WizardRule   `json:"rules"`
}

// WizardStepView 是返回给前端的一步
type WizardStepView struct {
	StepTitle string           `json:"stepTitle"`
	Items     []WizardItemView `json:"items"`
}

// WizardResponse 向导查询结果
type WizardResponse struct {
	// Exists 该应用是否存在该向导文件
	Exists bool             `json:"exists"`
	Kind   string           `json:"kind"`
	Title  string           `json:"title"`
	Steps  []WizardStepView `json:"steps"`
	// Defaults 所有字段的默认值（前端可直接用作表单初值）
	Defaults map[string]string `json:"defaults"`
}

// handleWizard GET /api/wizard?app=<name>&kind=install|uninstall|config|upgrade
//
// 也支持对尚未安装的本地包查询：?path=/xx/a.fpk&kind=install
// 前端据此在"安装确认"之后决定是否弹向导。
func (s *Server) handleWizard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "仅支持 GET")
		return
	}

	appname := strings.TrimSpace(r.URL.Query().Get("app"))
	pkgPath := strings.TrimSpace(r.URL.Query().Get("path"))
	kindRaw := strings.TrimSpace(r.URL.Query().Get("kind"))
	if kindRaw == "" {
		kindRaw = "install"
	}

	kind, ok := app.WizardKind(kindRaw)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "不支持的向导类型: "+kindRaw)
		return
	}

	// 确认应用/包的来源
	var appDir string
	switch {
	case pkgPath != "":
		if !s.opts.AllowFS {
			writeJSONError(w, http.StatusForbidden, "服务器文件浏览已禁用")
			return
		}
		if err := s.validatePackagePath(pkgPath); err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		// 未安装的包：从归档中读取向导文件
		s.wizardFromPackage(w, pkgPath, kind)
		return

	case appname != "":
		name, err := safeAppName(appname)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		if _, err := os.Stat(s.cfg.AppManifestFile(name)); err != nil {
			writeJSONError(w, http.StatusNotFound, "应用未安装: "+name)
			return
		}
		appDir = s.cfg.AppDir(name)

	default:
		writeJSONError(w, http.StatusBadRequest, "需要提供 app 或 path 参数")
		return
	}

	steps, err := app.LoadWizard(appDir, kind)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(steps) == 0 {
		writeJSON(w, http.StatusOK, WizardResponse{Exists: false, Kind: string(kind)})
		return
	}

	steps = app.ResolveWizardI18n(appDir, kind, steps)
	writeJSON(w, http.StatusOK, buildWizardResponse(kind, steps, s.wizardPreset(appDir, kind)))
}

// wizardFromPackage 从 .fpk 归档中读取向导文件（用于安装前的向导）
func (s *Server) wizardFromPackage(w http.ResponseWriter, pkgPath string, kind app.WizardFile) {
	data, err := fpk.ReadFileFromPackage(pkgPath, "wizard/"+string(kind))
	if err != nil {
		logger.Debug("读取包内向导失败 %s: %v", pkgPath, err)
		writeJSON(w, http.StatusOK, WizardResponse{Exists: false, Kind: string(kind)})
		return
	}
	steps, err := app.ParseWizard(data)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "解析向导失败: "+err.Error())
		return
	}
	if len(steps) == 0 {
		writeJSON(w, http.StatusOK, WizardResponse{Exists: false, Kind: string(kind)})
		return
	}

	// 包内没有独立的 i18n 可用目录，尝试从归档读取 i18n 文案
	dict := fpk.ReadI18nFromPackage(pkgPath)
	if len(dict) > 0 {
		steps = app.ResolveWizardI18nWithDict(steps, dict)
	}

	writeJSON(w, http.StatusOK, buildWizardResponse(kind, steps, nil))
}

// wizardPreset 为已安装应用的向导提供"当前值"预填。
// 例如 Lucky 的 config 向导要求填入当前端口，用已保存的 env 值最合适。
func (s *Server) wizardPreset(appDir string, kind app.WizardFile) map[string]string {
	if kind != app.WizardConfig && kind != app.WizardUpgrade {
		return nil
	}
	appname := filepath.Base(appDir)
	env, err := app.LoadEnvFile(s.cfg.AppEnvFile(appname))
	if err != nil || env == nil {
		return nil
	}
	// 仅保留向导会用到的那部分，避免把内部变量泄露到界面
	out := map[string]string{}
	for _, k := range []string{"port", "safety", "wizard_app_port", "wizard_admin_user"} {
		if v, ok := env[k]; ok && v != "" {
			out[k] = v
		}
	}
	return out
}

// buildWizardResponse 组装响应
func buildWizardResponse(kind app.WizardFile, steps []app.WizardStep, preset map[string]string) WizardResponse {
	views := make([]WizardStepView, 0, len(steps))
	for _, s := range steps {
		items := make([]WizardItemView, 0, len(s.Items))
		for _, it := range s.Items {
			items = append(items, WizardItemView{
				Type:        it.Type,
				Field:       it.Field,
				Label:       it.Label,
				HelpText:    it.HelpText,
				Placeholder: it.Placeholder,
				InitValue:   it.InitValue,
				Options:     it.Options,
				Rules:       it.Rules,
			})
		}
		views = append(views, WizardStepView{StepTitle: s.StepTitle, Items: items})
	}

	defaults := app.WizardDefaults(steps)
	// 已安装应用：用当前配置覆盖默认值
	for k, v := range preset {
		if _, ok := defaults[k]; ok {
			defaults[k] = v
		}
	}

	title := "安装向导"
	switch kind {
	case app.WizardUninstall:
		title = "卸载向导"
	case app.WizardUpgrade:
		title = "升级向导"
	case app.WizardConfig:
		title = "应用配置"
	}

	return WizardResponse{
		Exists:   true,
		Kind:     string(kind),
		Title:    title,
		Steps:    views,
		Defaults: defaults,
	}
}

// validateWizardSubmission 服务端校验向导值（前端也会校验，这里是第二道防线）
func validateWizardSubmission(appDir string, kind app.WizardFile, values map[string]string) error {
	steps, err := app.LoadWizard(appDir, kind)
	if err != nil || len(steps) == 0 {
		return nil // 没有向导就不校验
	}
	steps = app.ResolveWizardI18n(appDir, kind, steps)

	if errs := app.ValidateWizardValues(steps, values); len(errs) > 0 {
		// 汇总成一条可读的错误信息
		var parts []string
		for field, msg := range errs {
			parts = append(parts, fmt.Sprintf("%s: %s", field, msg))
		}
		return fmt.Errorf("向导参数校验失败（%s）", strings.Join(parts, "；"))
	}
	return nil
}

// wizardSetArgs 把向导值转成 Install/Upgrade 需要的 key=value 列表
func wizardSetArgs(values map[string]string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for k, v := range values {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		out = append(out, k+"="+v)
	}
	return out
}
