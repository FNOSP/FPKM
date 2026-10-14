package fpk

import "strings"

// ParseI18nINI 解析飞牛 i18n 语言文件（INI 风格）：
//
//	[install]
//	stepTitle="欢迎安装"
//	tips1.helpText="..."
//
// 返回 section.key → 文案 的映射。
func ParseI18nINI(content string) map[string]string {
	dict := map[string]string{}
	section := ""
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.Trim(line, "[]")
			continue
		}
		idx := strings.Index(line, "=")
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		// 去掉包裹的引号
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') ||
				(val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		full := key
		if section != "" {
			full = section + "." + key
		}
		dict[full] = val
	}
	return dict
}
