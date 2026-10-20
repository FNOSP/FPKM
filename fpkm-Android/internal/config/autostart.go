package config

import "path/filepath"

// AutostartFile 记录"用户主动启动过、需要在 FPKM 重启后自动恢复"的应用列表。
//
// 需求背景：应用是由 fpkm 拉起的子进程，fpkm 退出后它们也随之结束。
// 用户希望"上次正在跑的应用，下次打开 FPKM 桌面时自动重启"。
//
// 这里刻意只记录**用户意图**（哪些应用应处于运行状态），
// 而不是记录进程 PID —— PID 在重启后没有意义，且进程是否真的活着
// 由 app.Manager 在运行时判定。
func (c *Config) AutostartFile() string {
	return filepath.Join(c.BaseDir, "autostart.json")
}
