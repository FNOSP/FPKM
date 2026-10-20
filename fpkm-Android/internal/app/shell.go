package app

import "os/exec"

// shellPath 返回用于执行 FPK 应用启动/停止/安装脚本的 shell 解释器路径。
//
// 上游把解释器写死为 bash（fnOS、常见 Linux 发行版都自带）。Android 等精简
// 环境只有 POSIX shell（/system/bin/sh，mksh）而没有 bash，写死会导致：
//
//	exec: "bash": executable file not found in $PATH
//
// 进而表现为商店里启动应用时报「启动失败: 启动脚本失败」。
// 这里按可用性选择：bash → sh → 保底返回 "bash"（保持上游的报错语义）。
func shellPath() string { return resolveShell(exec.LookPath) }

// resolveShell 抽出查找逻辑便于单测覆盖「无 bash 时回退 sh」这条关键路径。
func resolveShell(look func(string) (string, error)) string {
	if p, err := look("bash"); err == nil && p != "" {
		return p
	}
	if p, err := look("sh"); err == nil && p != "" {
		return p
	}
	return "bash"
}
