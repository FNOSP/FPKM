package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"fpkm/internal/fpk"
	"fpkm/internal/logger"
)

// WizardRule 是向导字段的一条校验规则。
//
// 对应飞牛文档中的 rules 数组，常见字段：
//   - required          必填
//   - min / max         长度或数值范围
//   - len               固定长度
//   - pattern           正则
type WizardRule struct {
	Required bool   `json:"required"`
	Min      *int   `json:"min"`
	Max      *int   `json:"max"`
	Len      *int   `json:"len"`
	Pattern  string `json:"pattern"`
	Message  string `json:"message"`
}

// WizardOption 是 radio / checkbox / select 的选项
type WizardOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// WizardItem 是向导中的一个表单项
type WizardItem struct {
	Type        string         `json:"type"`
	Field       string         `json:"field"`
	Label       string         `json:"label"`
	HelpText    string         `json:"helpText"`
	Placeholder string         `json:"placeholder"`
	InitValue   string         `json:"initValue"`
	Options     []WizardOption `json:"options"`
	Rules       []WizardRule   `json:"rules"`
}

// WizardStep 是向导中的一步
type WizardStep struct {
	StepTitle string       `json:"stepTitle"`
	Items     []WizardItem `json:"items"`
}

// WizardFile 是四种向导文件的类型
type WizardFile string

const (
	WizardInstall   WizardFile = "install"
	WizardUpgrade   WizardFile = "upgrade"
	WizardUninstall WizardFile = "uninstall"
	WizardConfig    WizardFile = "config"
)

// WizardKind 校验并归一化向导类型
func WizardKind(kind string) (WizardFile, bool) {
	switch WizardFile(strings.ToLower(strings.TrimSpace(kind))) {
	case WizardInstall:
		return WizardInstall, true
	case WizardUpgrade:
		return WizardUpgrade, true
	case WizardUninstall:
		return WizardUninstall, true
	case WizardConfig:
		return WizardConfig, true
	}
	return "", false
}

// HasWizard 判断应用目录下是否存在指定向导文件
func HasWizard(appDir string, kind WizardFile) bool {
	info, err := os.Stat(filepath.Join(appDir, "wizard", string(kind)))
	return err == nil && !info.IsDir()
}

// LoadWizard 读取并解析向导文件。
// 返回 nil 表示该应用没有这个向导（属于正常情况，不是错误）。
func LoadWizard(appDir string, kind WizardFile) ([]WizardStep, error) {
	path := filepath.Join(appDir, "wizard", string(kind))
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取向导文件失败: %w", err)
	}

	steps, err := ParseWizard(data)
	if err != nil {
		return nil, fmt.Errorf("解析向导 %s 失败: %w", kind, err)
	}
	return steps, nil
}

// ParseWizard 解析向导 JSON。
//
// 真实世界的向导文件有两个必须处理的兼容点：
//  1. 部分文件是空数组或只有 tips，仍然要能正常解析
//  2. 文案里常内嵌 HTML（<br>、<strong>、<code> 等），需原样保留交给前端渲染
func ParseWizard(data []byte) ([]WizardStep, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "[]" || trimmed == "null" {
		return nil, nil
	}

	// 真实文件里存在尾随逗号等非标准写法，先做一次清理
	cleaned := cleanJSON(data)

	var steps []WizardStep
	if err := json.Unmarshal(cleaned, &steps); err != nil {
		return nil, err
	}

	// 规范化：去掉空步骤、修掉未知类型
	out := make([]WizardStep, 0, len(steps))
	for _, s := range steps {
		items := make([]WizardItem, 0, len(s.Items))
		for _, it := range s.Items {
			it.Type = strings.ToLower(strings.TrimSpace(it.Type))
			if it.Type == "" {
				// 没有 type 但有 field 的按 text 处理；否则视为 tips
				if strings.TrimSpace(it.Field) != "" {
					it.Type = "text"
				} else {
					it.Type = "tips"
				}
			}
			if !knownWizardType(it.Type) {
				logger.Debug("向导中出现未知字段类型 %q，按 tips 处理", it.Type)
				if strings.TrimSpace(it.HelpText) == "" {
					// 未知类型且无文案，直接丢弃
					continue
				}
				it.Type = "tips"
			}
			items = append(items, it)
		}
		if len(items) == 0 {
			continue
		}
		out = append(out, WizardStep{StepTitle: s.StepTitle, Items: items})
	}
	return out, nil
}

// knownWizardType 是否为文档定义的表单类型
func knownWizardType(t string) bool {
	switch t {
	case "tips", "text", "password", "radio", "checkbox", "select", "switch":
		return true
	}
	return false
}

// ResolveWizardI18n 用 i18n 文件补全向导中的 ${section.key} 占位符。
//
// 飞牛应用把文案抽到 i18n/ 目录（INI 风格，按语言分文件），
// 向导里以 ${install.stepTitle}、${uninstall.tips1.helpText} 引用。
// 这里按 zh-CN → en-US → 任意可用语言的优先级加载。
func ResolveWizardI18n(appDir string, kind WizardFile, steps []WizardStep) []WizardStep {
	dict := loadI18nDict(appDir)
	if len(dict) == 0 {
		return steps
	}

	replace := func(s string) string { return expandI18n(s, dict) }

	for i := range steps {
		steps[i].StepTitle = replace(steps[i].StepTitle)
		for j := range steps[i].Items {
			it := &steps[i].Items[j]
			it.Label = replace(it.Label)
			it.HelpText = replace(it.HelpText)
			it.Placeholder = replace(it.Placeholder)
			for k := range it.Options {
				it.Options[k].Label = replace(it.Options[k].Label)
			}
			for k := range it.Rules {
				it.Rules[k].Message = replace(it.Rules[k].Message)
			}
		}
	}
	return steps
}

// ResolveWizardI18nWithDict 用外部提供的文案字典替换占位符。
// 用于从 .fpk 包内读取 i18n 后解析（此时没有已解压的应用目录）。
func ResolveWizardI18nWithDict(steps []WizardStep, dict map[string]string) []WizardStep {
	if len(dict) == 0 {
		return steps
	}
	replace := func(s string) string { return expandI18n(s, dict) }
	for i := range steps {
		steps[i].StepTitle = replace(steps[i].StepTitle)
		for j := range steps[i].Items {
			it := &steps[i].Items[j]
			it.Label = replace(it.Label)
			it.HelpText = replace(it.HelpText)
			it.Placeholder = replace(it.Placeholder)
			for k := range it.Options {
				it.Options[k].Label = replace(it.Options[k].Label)
			}
			for k := range it.Rules {
				it.Rules[k].Message = replace(it.Rules[k].Message)
			}
		}
	}
	return steps
}

// i18n 语言优先级：简体中文 → 英文 → 其他
var i18nPreferred = []string{"zh-CN", "zh_CN", "zh-cn", "zh-Hans", "en-US", "en_US", "en"}

// loadI18nDict 加载 i18n 目录，返回 section.key → 文案 的映射
func loadI18nDict(appDir string) map[string]string {
	dir := filepath.Join(appDir, "i18n")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	// 按优先级挑选第一个可读的语言文件
	pick := func(names []string) string {
		for _, want := range names {
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				if strings.EqualFold(e.Name(), want) {
					return filepath.Join(dir, e.Name())
				}
			}
		}
		return ""
	}

	path := pick(i18nPreferred)
	if path == "" {
		// 没有偏好语言时取第一个普通文件
		for _, e := range entries {
			if !e.IsDir() {
				path = filepath.Join(dir, e.Name())
				break
			}
		}
	}
	if path == "" {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return fpk.ParseI18nINI(string(data))
}

// expandI18n 替换 ${a.b} / ${a.b.c} 形式的占位符
func expandI18n(s string, dict map[string]string) string {
	if !strings.Contains(s, "${") {
		return s
	}
	var b strings.Builder
	for {
		start := strings.Index(s, "${")
		if start < 0 {
			b.WriteString(s)
			break
		}
		end := strings.Index(s[start:], "}")
		if end < 0 {
			b.WriteString(s)
			break
		}
		end += start

		key := s[start+2 : end]
		b.WriteString(s[:start])
		if v, ok := dict[key]; ok {
			b.WriteString(v)
		} else {
			// 找不到就保留原样，便于排查
			b.WriteString(s[start : end+1])
		}
		s = s[end+1:]
	}
	return b.String()
}

// WizardDefaults 汇总向导中所有字段的默认值。
// 用于在没有界面交互时（如 CLI 安装）提供兜底值。
func WizardDefaults(steps []WizardStep) map[string]string {
	out := map[string]string{}
	for _, s := range steps {
		for _, it := range s.Items {
			if it.Field == "" {
				continue
			}
			if _, exists := out[it.Field]; exists {
				continue
			}
			val := it.InitValue
			// 无默认值时给一个合理兜底，避免脚本拿到空值
			if val == "" {
				switch it.Type {
				case "radio", "select":
					if len(it.Options) > 0 {
						val = it.Options[0].Value
					}
				case "switch", "checkbox":
					val = "false"
				}
			}
			out[it.Field] = val
		}
	}
	return out
}

// ValidateWizardValues 按向导规则校验提交值，返回 field → 错误信息。
//
// 前端也会做校验以保证即时反馈，服务端再校验一次是必要的：
// 接口可被直接调用，且向导值会作为环境变量传给安装脚本。
func ValidateWizardValues(steps []WizardStep, values map[string]string) map[string]string {
	errs := map[string]string{}

	for _, s := range steps {
		for _, it := range s.Items {
			if it.Field == "" || it.Type == "tips" {
				continue
			}
			val := strings.TrimSpace(values[it.Field])

			for _, rule := range it.Rules {
				msg := rule.Message
				if msg == "" {
					msg = "该字段不合法"
				}

				if rule.Required && val == "" {
					errs[it.Field] = msg
					break
				}
				// 空值且非必填：跳过其余规则
				if val == "" {
					continue
				}

				// 长度/数值范围
				if rule.Len != nil && len([]rune(val)) != *rule.Len {
					errs[it.Field] = msg
					break
				}
				if rule.Min != nil && len([]rune(val)) < *rule.Min {
					errs[it.Field] = msg
					break
				}
				if rule.Max != nil && len([]rune(val)) > *rule.Max {
					errs[it.Field] = msg
					break
				}

				// 正则
				if rule.Pattern != "" {
					re, err := compileWizardPattern(rule.Pattern)
					if err != nil {
						logger.Debug("向导规则正则无效 %q: %v", rule.Pattern, err)
						continue
					}
					if !re.MatchString(val) {
						errs[it.Field] = msg
						break
					}
				}
			}
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return errs
}

// wizardFieldNames 返回向导中所有可提交字段名（用于过滤请求体）
func wizardFieldNames(steps []WizardStep) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range steps {
		for _, it := range s.Items {
			if it.Field == "" || it.Type == "tips" {
				continue
			}
			if seen[it.Field] {
				continue
			}
			seen[it.Field] = true
			out = append(out, it.Field)
		}
	}
	sort.Strings(out)
	return out
}

// compileWizardPattern 编译正则。
// 向导里的 pattern 常常写成 JSON 转义后的形式（如 \\d），
// 这里做一次常见转义归一化，避免因为写法差异误判。
func compileWizardPattern(pattern string) (*regexp.Regexp, error) {
	p := strings.TrimSpace(pattern)
	if p == "" {
		return nil, fmt.Errorf("空正则")
	}
	re, err := regexp.Compile(p)
	if err == nil {
		return re, nil
	}
	// 兜底：尝试把双重转义还原一次
	alt := strings.ReplaceAll(p, `\\`, `\`)
	if alt != p {
		return regexp.Compile(alt)
	}
	return nil, err
}
