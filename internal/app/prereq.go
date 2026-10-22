package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
)

// RuntimePrereq 描述 cmd/main 里可能出现的外部命令依赖。
//
// 动机：飞牛 FPK 应用的 cmd/main 通过调用外部运行时来启动（python3 / node 等）。
// 在完整 Linux（fnOS）上这些运行时都存在；在精简环境（Android 只有 toybox）
// 中则缺失，应用会以退出码 1 结束，而报错信息只有脚本自己打印的一行，
// 用户看不出"到底缺什么、要补什么文件"。
//
// 这里把这类依赖显式探测出来，缺失时给出明确的运行时名称与需要额外提供的文件说明。
type RuntimePrereq struct {
	Cmd string // 被调用的外部命令名
	Use string // 缺失时需要额外提供的文件
}

// runtimePrereqs 是需要探测的外部命令白名单。
//
// 只列"应用自身不会打包、必须由运行环境提供"的命令；像 sed/grep/awk/tar
// 这类在 Android toybox 里都有的基础命令不列入，避免产生无意义的告警。
func runtimePrereqs() []RuntimePrereq {
	return []RuntimePrereq{
		{Cmd: "python3", Use: "Python 3 运行时（aarch64 版本，含标准库；.pyc 需与 Python 小版本一致，如 cpython-311）"},
		{Cmd: "python", Use: "Python 运行时（aarch64 版本）"},
		{Cmd: "node", Use: "Node.js 运行时（aarch64 版本）"},
		{Cmd: "npm", Use: "npm（随 Node.js 一起提供）"},
		{Cmd: "java", Use: "Java 运行时（JRE/JDK，aarch64 版本）"},
		{Cmd: "ffmpeg", Use: "ffmpeg 可执行文件及其编解码依赖库"},
		{Cmd: "pip", Use: "pip（随 Python 提供）"},
		{Cmd: "pip3", Use: "pip3（随 Python 提供）"},
		{Cmd: "netstat", Use: "net-tools 的 netstat（或与 -tln 参数兼容的实现）"},
		{Cmd: "php", Use: "PHP 运行时"},
		{Cmd: "ruby", Use: "Ruby 运行时"},
		{Cmd: "perl", Use: "Perl 运行时"},
		{Cmd: "docker", Use: "Docker 运行时（Android 不支持）"},
	}
}

// DetectRuntimePrereqs 读取 cmd/main，返回其中引用的外部命令，以及当前环境缺失的部分。
// cmd/main 不存在或读取失败时返回空结果（不产生告警）。
func DetectRuntimePrereqs(cmdMain string) (required []string, missing []string) {
	return detectRuntimePrereqs(cmdMain, exec.LookPath)
}

// detectRuntimePrereqs 抽出查找函数便于单测覆盖"缺失判定"这条关键路径。
func detectRuntimePrereqs(cmdMain string, look func(string) (string, error)) ([]string, []string) {
	data, err := os.ReadFile(cmdMain)
	if err != nil {
		return nil, nil
	}
	text := string(data)

	var required, missing []string
	for _, p := range runtimePrereqs() {
		if !referencesCommand(text, p.Cmd) {
			continue
		}
		required = append(required, p.Cmd)
		if look == nil {
			continue
		}
		if _, err := look(p.Cmd); err != nil {
			missing = append(missing, p.Cmd)
		}
	}
	return required, missing
}

// referencesCommand 判断脚本是否把某命令当作「外部命令」调用。
//
// 只看真正的执行位置：行首，或紧跟在 nohup / exec / env / time / xargs 之后，
// 或位于 ; && | 与命令替换 $( 之后。注释行与 shebang 行一律跳过，
// 且不匹配出现在引号字符串或纯赋值里的命令名（避免把说明文字当成依赖）。
//
// `command -v <cmd>` / `type -p <cmd>` / `which <cmd>` 这类可用性判断同样算依赖，
// 因为这类判断失败时脚本通常会直接退出（云抽奖就是这么写的）。
func referencesCommand(text, cmd string) bool {
	quoted := regexp.QuoteMeta(cmd)
	callRe := regexp.MustCompile(`(?m)(?:^\s*|(?:\bnohup\s+|\bexec\s+|\benv\s+|\btime\s+|\bxargs\s+|\bif\s+|\belif\s+|\bwhile\s+|\buntil\s+|\bthen\s+|\bdo\s+|\bsudo\s+)|;\s*|&&\s*|\|\s*|\$\(\s*)` +
		quoted + `(?:\s|$)`)
	probeRe := regexp.MustCompile(`(?:\bcommand\s+-[vV]\s+|\btype\s+-p\s+|\bwhich\s+)` + quoted + `(?:\s|$)`)

	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if callRe.MatchString(line) || probeRe.MatchString(line) {
			return true
		}
	}
	return false
}

// RuntimePrereqError 依据缺失清单生成启动前的报错信息；无缺失时返回 nil。
//
// 报错里同时给出"缺哪个命令"和"要补什么文件"，并按当前平台补充对应建议，
// 让用户能据此判断该应用在当前系统上是否可运行。
func RuntimePrereqError(missing []string) error {
	if len(missing) == 0 {
		return nil
	}

	useOf := make(map[string]string)
	for _, p := range runtimePrereqs() {
		useOf[p.Cmd] = p.Use
	}

	var sb strings.Builder
	sb.WriteString("该应用依赖当前系统未提供的运行时，无法启动: ")
	sb.WriteString(strings.Join(missing, ", "))
	sb.WriteString("\n需要额外提供的文件:")
	for _, cmd := range missing {
		use := useOf[cmd]
		if use == "" {
			use = "对应的可执行文件"
		}
		sb.WriteString(fmt.Sprintf("\n  - %s: %s", cmd, use))
	}
	sb.WriteString("\n")
	sb.WriteString(runtimePrereqTail())
	return errors.New(sb.String())
}

// runtimePrereqTail 按平台给出补充说明。
func runtimePrereqTail() string {
	switch runtime.GOOS {
	case "android":
		return "Android 系统自身不提供这些运行时。要运行此类应用，需要自行提供 aarch64 版 Linux 运行时" +
			"（例如 Python 3.11 / Node.js）并加入 PATH；依赖 glibc 的动态可执行文件还需自带" +
			" ld-linux-aarch64.so.1 与对应 .so 依赖，或改用自带静态二进制的应用。"
	case "windows":
		return "请先在系统中安装上述运行时，或改用自带运行时的应用。"
	default:
		return "请先安装上述运行时后重试。"
	}
}
