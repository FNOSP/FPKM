package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"fpkm/internal/logger"
)

// UIConfigEntry 是 app/ui/config 中 ".url" 下的一个应用条目。
//
// 飞牛的这个文件描述"如何在桌面/应用中心里打开这个应用"，关键字段：
//   - GatewayPrefix：网关路由前缀，用于把请求转发到应用（如 /app/fnchat）
//   - URL：桌面入口实际打开的地址，可能是前缀加尾斜杠、子路径，
//     甚至带查询串（如 /app/fygo-browser/browser.html?v=1.0.1-fnos）
//   - GatewaySocket：Unix socket 文件名，存在即为统一网关应用
//   - Port：端口应用监听的端口
//
// GatewayPrefix 与 URL 是两个不同的概念，不能互相替代：
// 前者用于匹配/转发（不带尾斜杠），后者用于"打开应用"（可能带尾斜杠或子路径）。
type UIConfigEntry struct {
	Key           string
	GatewayPrefix string
	URL           string
	GatewaySocket string
	Port          string
	Title         string
	// Type 打开方式：iframe（桌面窗口内打开）或 url（浏览器标签页打开）。
	// 见飞牛文档「应用入口」：iframe → 桌面窗口；url → 浏览器标签/外部视图。
	Type string
	// Protocol http / https / 空（交给系统自适应）
	Protocol string
	// AllUsers 是否对所有用户可见
	AllUsers bool
	// NoDisplay 在桌面隐藏该入口
	NoDisplay bool
}

// uiconfigShape 对应 ui/config 的嵌套结构：{".url": {appKey: {...}}}
type uiconfigShape map[string]map[string]map[string]interface{}

// ParseUIConfig 解析 ui/config，返回其中的应用条目（保持文件中的顺序不确定，
// 调用方通常只需要第一个有效条目）。
func ParseUIConfig(data []byte) []UIConfigEntry {
	if len(data) == 0 {
		return nil
	}
	cleaned := cleanJSON(data)

	var parsed uiconfigShape
	if err := json.Unmarshal(cleaned, &parsed); err != nil {
		// 非标准 JSON（尾逗号等）走正则兜底
		return parseUIConfigByRegex(data)
	}

	var out []UIConfigEntry
	for _, appKey := range parsed {
		for key, appConfig := range appKey {
			entry := UIConfigEntry{
				Key:           key,
				GatewayPrefix: stringField(appConfig, "gatewayPrefix"),
				URL:           stringField(appConfig, "url"),
				GatewaySocket: stringField(appConfig, "gatewaySocket"),
				Title:         stringField(appConfig, "title"),
				Type:          stringField(appConfig, "type"),
				Protocol:      stringField(appConfig, "protocol"),
				AllUsers:      boolField(appConfig, "allUsers"),
				NoDisplay:     boolField(appConfig, "noDisplay"),
			}
			if port, ok := appConfig["port"]; ok {
				switch v := port.(type) {
				case string:
					entry.Port = v
				case float64:
					entry.Port = trimFloat(v)
				}
			}
			out = append(out, entry)
		}
	}
	if len(out) == 0 {
		return parseUIConfigByRegex(data)
	}
	return out
}

// parseUIConfigByRegex 在 JSON 解析失败时用正则提取关键字段。
// 飞牛应用中存在尾随逗号等非标准写法，必须保留这条兜底路径。
func parseUIConfigByRegex(data []byte) []UIConfigEntry {
	s := string(data)
	entry := UIConfigEntry{
		GatewayPrefix: firstSubmatch(s, `"gatewayPrefix"\s*:\s*"([^"]+)"`),
		URL:           firstSubmatch(s, `"url"\s*:\s*"([^"]+)"`),
		GatewaySocket: firstSubmatch(s, `"gatewaySocket"\s*:\s*"([^"]+)"`),
		Title:         firstSubmatch(s, `"title"\s*:\s*"([^"]+)"`),
		Type:          firstSubmatch(s, `"type"\s*:\s*"([^"]+)"`),
		Protocol:      firstSubmatch(s, `"protocol"\s*:\s*"([^"]*)"`),
	}
	if strings.Contains(s, `"noDisplay": true`) || strings.Contains(s, `"noDisplay":true`) {
		entry.NoDisplay = true
	}
	if p := firstSubmatch(s, `"port"\s*:\s*"?(\d+)"?`); p != "" {
		entry.Port = p
	}
	if entry.GatewayPrefix == "" && entry.URL == "" && entry.GatewaySocket == "" && entry.Port == "" {
		return nil
	}
	return []UIConfigEntry{entry}
}

// firstSubmatch 返回首个捕获组，未匹配返回空串
func firstSubmatch(s, pattern string) string {
	re := regexp.MustCompile(pattern)
	if m := re.FindStringSubmatch(s); len(m) >= 2 {
		return m[1]
	}
	return ""
}

// boolField 读取布尔字段（不存在时返回 false）
func boolField(m map[string]interface{}, key string) bool {
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// stringField 读取字符串字段
func stringField(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// trimFloat 把 JSON 数字转成不带小数点的字符串（8033 → "8033"）
func trimFloat(f float64) string {
	if f == float64(int64(f)) {
		return itoaSimple(int(f))
	}
	b, _ := json.Marshal(f)
	return string(b)
}

// ReadUIConfigEntry 读取并解析应用的 ui/config，返回第一个有效条目。
//
// 查找顺序与飞牛一致：先看 target/ui/config（TRIM_APPDEST 下），
// 再看应用根目录及 target 下的兼容位置。
func ReadUIConfigEntry(targetDir, appDir string) (UIConfigEntry, bool) {
	data := readUIConfigData(targetDir, appDir)
	if data == nil {
		return UIConfigEntry{}, false
	}
	entries := ParseUIConfig(data)
	if len(entries) == 0 {
		return UIConfigEntry{}, false
	}
	return entries[0], true
}

// ResolveGatewayPrefix 计算应用在统一网关上的路由前缀。
//
// 优先级：ui/config 的 gatewayPrefix > url 推导 > /app/{appname}
// 注意 gatewayPrefix 用于路由匹配，不带尾斜杠。
func ResolveGatewayPrefix(targetDir, appDir, appname string) string {
	if entry, ok := ReadUIConfigEntry(targetDir, appDir); ok {
		if p := normalizePrefix(entry.GatewayPrefix); p != "" {
			return p
		}
		// 没有 gatewayPrefix 时从 url 推导（去掉查询串与尾斜杠）
		if p := prefixFromURL(entry.URL); p != "" {
			return p
		}
	}
	return "/app/" + appname
}

// ResolveLaunchPath 计算"打开应用"时使用的路径。
//
// 这是用户在桌面上看到的地址，与路由前缀不同：
//   - fnchat          gatewayPrefix=/app/fnchat   url=/app/fnchat/     → 用 /app/fnchat/
//   - file-collector  gatewayPrefix=/app/...      url=.../gateway      → 用子路径
//   - fygo-browser    url 带查询串                                      → 原样保留
//
// 若 ui/config 未提供 url，则退回网关前缀（补尾斜杠，保证相对资源解析正确）。
func ResolveLaunchPath(targetDir, appDir, appname string) string {
	prefix := ResolveGatewayPrefix(targetDir, appDir, appname)

	entry, ok := ReadUIConfigEntry(targetDir, appDir)
	if !ok || strings.TrimSpace(entry.URL) == "" {
		return ensureTrailingSlash(prefix)
	}

	url := strings.TrimSpace(entry.URL)

	// 相对路径（不含前导 /）按网关前缀拼成绝对路径
	if !strings.HasPrefix(url, "/") {
		return ensureTrailingSlash(prefix) + url
	}

	return url
}

// LaunchMode 应用的打开方式（决定前端用子窗口还是新标签页）
type LaunchMode string

const (
	// LaunchIframe 在桌面窗口内打开（ui/config 的 type=iframe）
	LaunchIframe LaunchMode = "iframe"
	// LaunchTab 在新浏览器标签页打开（type=url，或没有 ui/config）
	LaunchTab LaunchMode = "tab"
)

// ResolveLaunchMode 由 ui/config 的 type 字段决定打开方式。
//
// 飞牛文档：type=iframe 在桌面窗口内打开；type=url 在浏览器标签页打开。
// 若应用没有 ui/config（或字段缺失），退化为新标签页打开 —— 此时无法
// 保证对方允许被 iframe 嵌入（很多应用会设置 X-Frame-Options）。
func ResolveLaunchMode(targetDir, appDir string) LaunchMode {
	entry, ok := ReadUIConfigEntry(targetDir, appDir)
	if !ok {
		return LaunchTab
	}
	if strings.EqualFold(strings.TrimSpace(entry.Type), "iframe") {
		return LaunchIframe
	}
	return LaunchTab
}

// normalizePrefix 归一化网关前缀：保证以 / 开头、不以 / 结尾
func normalizePrefix(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return strings.TrimRight(p, "/")
}

// prefixFromURL 从 url 推导路由前缀：去掉查询串、锚点与尾斜杠
func prefixFromURL(url string) string {
	url = strings.TrimSpace(url)
	if url == "" {
		return ""
	}
	// 去掉 ?query 和 #fragment
	if i := strings.IndexAny(url, "?#"); i >= 0 {
		url = url[:i]
	}
	if !strings.HasPrefix(url, "/") {
		return ""
	}
	return strings.TrimRight(url, "/")
}

// ensureTrailingSlash 保证路径以 / 结尾。
// 网关应用多为前端单页应用，缺少尾斜杠会导致相对资源路径解析错误。
func ensureTrailingSlash(p string) string {
	if p == "" {
		return "/"
	}
	if strings.HasSuffix(p, "/") {
		return p
	}
	return p + "/"
}

// EnsureTrailingSlash 是 ensureTrailingSlash 的导出形式，供其他包（web 层）复用
func EnsureTrailingSlash(p string) string { return ensureTrailingSlash(p) }

// readUIConfigData 读取 ui/config 文件内容
func readUIConfigData(targetDir, appDir string) []byte {
	configPaths := []string{
		filepath.Join(targetDir, "ui", "config"),
		filepath.Join(appDir, "ui", "config"),
		filepath.Join(appDir, "target", "ui", "config"),
	}

	for _, p := range configPaths {
		if data, err := os.ReadFile(p); err == nil {
			logger.Debug("找到 ui/config: %s", p)
			return data
		}
	}
	return nil
}

// launchPathOf 返回环境变量中记录的"打开路径"，
// 老版本安装的应用没有 LAUNCH_PATH 时回退到网关前缀（补尾斜杠）。
func launchPathOf(env EnvMap) string {
	if env == nil {
		return ""
	}
	if p := strings.TrimSpace(env["LAUNCH_PATH"]); p != "" {
		return p
	}
	return ensureTrailingSlash(env["GATEWAY_PREFIX"])
}
