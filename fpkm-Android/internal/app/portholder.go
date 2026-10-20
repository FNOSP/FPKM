package app

import (
	"os/exec"
	"regexp"
	"strings"
)

// portHolderPattern 从 `ss -tlnp` / `lsof` 输出中提取进程信息
var portHolderPattern = regexp.MustCompile(`users:\(\("([^"]+)",pid=(\d+)`)

// describePortHolder 尝试说明是谁占用了端口，用于给出可操作的提示。
// 查找失败时返回空字符串（调用方不应依赖此信息）。
//
// 实现上优先用 ss（多数 Linux 自带），退回 lsof；两者都没有则放弃。
// appname 仅用于日志上下文，当前不参与过滤。
func (m *Manager) describePortHolder(port int, appname string) string {
	// ss -tlnp 输出示例：
	//   LISTEN 0 2048 0.0.0.0:8033 0.0.0.0:* users:(("python",pid=7514,fd=12))
	if out, err := exec.Command("ss", "-tlnp").Output(); err == nil {
		if desc := matchPortHolder(string(out), port); desc != "" {
			return desc
		}
	}

	// lsof 兜底：lsof -ti :PORT 只给 PID，再用 ps 查名字
	if out, err := exec.Command("lsof", "-ti", "tcp:"+itoaSimple(port), "-sTCP:LISTEN").Output(); err == nil {
		pid := strings.TrimSpace(strings.Split(strings.TrimSpace(string(out)), "\n")[0])
		if pid != "" {
			name := processName(pid)
			if name != "" {
				return name + " (PID: " + pid + ")"
			}
			return "PID: " + pid
		}
	}

	return ""
}

// matchPortHolder 在 ss 输出中找出监听指定端口的进程
func matchPortHolder(output string, port int) string {
	suffix := ":" + itoaSimple(port)
	for _, line := range strings.Split(output, "\n") {
		if !strings.Contains(line, "LISTEN") {
			continue
		}
		// 本地地址列的形状是 0.0.0.0:8033 或 [::]:8033
		fields := strings.Fields(line)
		listening := false
		for _, f := range fields {
			if strings.HasSuffix(f, suffix) {
				listening = true
				break
			}
		}
		if !listening {
			continue
		}
		if mm := portHolderPattern.FindStringSubmatch(line); len(mm) == 3 {
			return mm[1] + " (PID: " + mm[2] + ")"
		}
		return "其他进程"
	}
	return ""
}

// processName 通过 ps 获取进程名
func processName(pid string) string {
	out, err := exec.Command("ps", "-p", pid, "-o", "comm=").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// itoaSimple 轻量整数转字符串（避免为一个小工具引入额外依赖）
func itoaSimple(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
