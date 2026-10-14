package app

import (
	"regexp"
	"strings"
)

// shellKeywords 是 shell 保留字，绝不可能是"要启动的后台进程"。
// CMD 里用 " & " / ";" 拼接的不只是进程，还可能是 shell 控制结构，
// 例如 fnchat：
//
//	python fnchat.py -c cfg & for i in $(seq 1 30); do nc -z ...; done; gateway-proxy ...
//
// 其中 `for ... done` 只是等待端口就绪的轮询，不是要启动的进程。
var shellKeywords = map[string]bool{
	"for": true, "while": true, "until": true, "if": true, "then": true,
	"else": true, "elif": true, "fi": true, "do": true, "done": true,
	"case": true, "esac": true, "in": true, "select": true, "function": true,
	"{": true, "}": true, "(": true, ")": true, "!": true, "time": true,
}

// shellBuiltins 是 shell 内建命令，同样不应作为独立后台进程启动。
var shellBuiltins = map[string]bool{
	"cd": true, "export": true, "source": true, ".": true, "set": true,
	"unset": true, "shift": true, "eval": true, "exec": true, "trap": true,
	"wait": true, "return": true, "exit": true, "break": true, "continue": true,
	"local": true, "read": true, "test": true, "[": true, "[[": true,
	"ulimit": true, "umask": true, "alias": true, "let": true,
	"true": true, "false": true, ":": true,
}

// isShellKeyword 判断 token 是否为 shell 关键字或内建命令
func isShellKeyword(tok string) bool {
	return shellKeywords[tok] || shellBuiltins[tok]
}

// splitShellCommands 切分出"需要作为进程启动"的命令片段。
//
// 与简单按 " & " 切分不同，这里：
//   - 引号感知（不会切到字符串内部的 & ; |）
//   - 保护 $(...) 命令替换内部的分隔符
//   - 把 for/while/until/select、if、case 块当作一个整体，
//     整块不以进程形式启动（它们只是内联等待/判断）
//   - 过滤 shell 关键字与内建命令
func splitShellCommands(cmd string) []string {
	var out []string
	for _, seg := range splitSegments(cmd) {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		if isShellKeyword(firstToken(seg)) {
			continue
		}
		out = append(out, seg)
	}
	return out
}

// splitSegments 以块感知的方式把复合命令切成顶层语句。
// 控制流块（for...done / if...fi / case...esac）内部的分隔符不会被切开。
func splitSegments(cmd string) []string {
	var (
		segs     []string
		cur      strings.Builder
		word     strings.Builder
		inQuote  byte
		escaped  bool
		blockDep int
		parenDep int
	)

	flushWord := func() {
		if word.Len() == 0 {
			return
		}
		w := word.String()
		word.Reset()
		switch w {
		case "for", "while", "until", "select", "if", "case":
			blockDep++
		case "done", "fi", "esac":
			if blockDep > 0 {
				blockDep--
			}
		}
	}

	flushSeg := func() {
		flushWord()
		if s := strings.TrimSpace(cur.String()); s != "" {
			segs = append(segs, s)
		}
		cur.Reset()
	}

	for i := 0; i < len(cmd); i++ {
		c := cmd[i]

		if escaped {
			cur.WriteByte(c)
			word.WriteByte(c)
			escaped = false
			continue
		}
		if c == '\\' && inQuote != '\'' {
			cur.WriteByte(c)
			escaped = true
			continue
		}
		if inQuote != 0 {
			cur.WriteByte(c)
			word.WriteByte(c)
			if c == inQuote {
				inQuote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			inQuote = c
			cur.WriteByte(c)
			word.WriteByte(c)
			continue
		}

		switch c {
		case ' ', '\t':
			flushWord()
			cur.WriteByte(c)
			continue
		case '(':
			parenDep++
			cur.WriteByte(c)
			word.WriteByte(c)
			continue
		case ')':
			if parenDep > 0 {
				parenDep--
			}
			cur.WriteByte(c)
			word.WriteByte(c)
			continue
		case '&', ';', '|':
			flushWord()
			// 吞掉重复分隔符（&&、||）
			for i+1 < len(cmd) && (cmd[i+1] == '&' || cmd[i+1] == '|') {
				i++
			}
			if blockDep == 0 && parenDep == 0 {
				flushSeg()
			} else {
				// 块内部/命令替换内部：保留为同一片段
				cur.WriteByte(' ')
			}
			continue
		}

		cur.WriteByte(c)
		word.WriteByte(c)
	}
	flushSeg()
	return segs
}

// splitTopLevel 不区分控制流，按 & ; | 在引号之外全部切开。
// 用于在整段 CMD 中搜索特定命令（如 gateway-proxy）。
func splitTopLevel(cmd string) []string {
	var (
		segs    []string
		cur     strings.Builder
		inQuote byte
		escaped bool
	)
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			segs = append(segs, s)
		}
		cur.Reset()
	}
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		if escaped {
			cur.WriteByte(c)
			escaped = false
			continue
		}
		if c == '\\' && inQuote != '\'' {
			cur.WriteByte(c)
			escaped = true
			continue
		}
		if inQuote != 0 {
			cur.WriteByte(c)
			if c == inQuote {
				inQuote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			inQuote = c
			cur.WriteByte(c)
			continue
		}
		if c == '&' || c == ';' || c == '|' {
			for i+1 < len(cmd) && (cmd[i+1] == '&' || cmd[i+1] == '|') {
				i++
			}
			flush()
			continue
		}
		cur.WriteByte(c)
	}
	flush()
	return segs
}

// firstToken 返回命令的首个 token（已解析引号）
func firstToken(s string) string {
	parts := parseShellArgs(strings.TrimSpace(s))
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

// gatewayProxyPattern 匹配 gateway-proxy 这类"网关前置代理"命令名
var gatewayProxyPattern = regexp.MustCompile(`(^|/)(gateway-proxy|gateway_proxy)$`)

// isGatewayProxyCommand 判断命令是否是网关 socket 代理。
// 这类进程负责创建应用的 Unix Socket，必须被启动。
func isGatewayProxyCommand(cmd string) bool {
	return gatewayProxyPattern.MatchString(strings.TrimSpace(cmd))
}

// extractSocketProxyCommands 在整段 CMD 中兜底查找 gateway-proxy 启动命令。
//
// 即使常规切分只认出了主进程，也必须保证负责创建 socket 的 gateway-proxy
// 不被漏掉 —— 否则应用启动后网关会报 "Socket 不存在（应用可能未启动）"。
func extractSocketProxyCommands(cmd string) []ExtraCommand {
	var out []ExtraCommand
	seen := map[string]bool{}

	for _, seg := range splitTopLevel(cmd) {
		for _, piece := range strings.Split(seg, ";") {
			piece = strings.TrimSpace(piece)
			if piece == "" {
				continue
			}
			// 去掉 `do` / `then` 之类的前导关键字
			for {
				tok := firstToken(piece)
				if tok == "do" || tok == "then" || tok == "else" {
					piece = strings.TrimSpace(strings.TrimPrefix(piece, tok))
					continue
				}
				break
			}
			piece, _ = extractPrefixEnv(piece)
			parts := parseShellArgs(piece)
			if len(parts) == 0 || !isGatewayProxyCommand(parts[0]) {
				continue
			}
			key := parts[0] + " " + strings.Join(parts[1:], " ")
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, ExtraCommand{Command: parts[0], Args: parts[1:]})
		}
	}
	return out
}
