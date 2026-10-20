//go:build windows

package app

import (
	"os/exec"
	"syscall"
)

// setProcessGroup 设置进程组（Windows）
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

// isProcessAlive 检查进程是否存活（Windows）
func isProcessAlive(pid int) bool {
	// Windows 上使用 tasklist 检查进程
	cmd := exec.Command("tasklist", "/FI", "PID eq "+itoa(pid), "/NH")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	// 如果输出中包含 PID，说明进程存在
	return len(output) > 0
}

// terminateProcess 优雅终止进程（Windows）
func (pm *ProcessManager) terminateProcess(pid int) {
	// Windows 上使用 taskkill 发送终止信号
	exec.Command("taskkill", "/PID", itoa(pid), "/T").Run()
}

// killProcess 强制杀死进程（Windows）
func (pm *ProcessManager) killProcess(pid int) error {
	return exec.Command("taskkill", "/F", "/T", "/PID", itoa(pid)).Run()
}

// itoa 简单的 int 转字符串
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
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
