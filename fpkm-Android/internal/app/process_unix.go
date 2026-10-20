//go:build !windows

package app

import (
	"fmt"
	"os/exec"
	"syscall"
)

// setProcessGroup 设置进程组（Unix）
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
}

// isProcessAlive 检查进程是否存活（Unix）
func isProcessAlive(pid int) bool {
	// 发送信号0检测进程是否存在
	if err := syscall.Kill(pid, 0); err != nil {
		return false
	}
	return true
}

// terminateProcess 优雅终止进程（Unix）
func (pm *ProcessManager) terminateProcess(pid int) {
	// 向进程组发送 SIGTERM
	syscall.Kill(-pid, syscall.SIGTERM)
	syscall.Kill(pid, syscall.SIGTERM)
}

// killProcess 强制杀死进程（Unix）
func (pm *ProcessManager) killProcess(pid int) error {
	// 向进程组发送 SIGKILL
	err1 := syscall.Kill(-pid, syscall.SIGKILL)
	err2 := syscall.Kill(pid, syscall.SIGKILL)
	if err1 != nil && err2 != nil {
		return fmt.Errorf("kill failed: %v / %v", err1, err2)
	}
	return nil
}
