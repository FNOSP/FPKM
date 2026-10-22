//go:build windows

package web

// readDiskStats 在 Windows 上没有 /proc/mounts 与 Statfs，
// 直接返回空列表（FPKM 只发布 Linux / Android 版本，
// 这里保留实现是为了让开发机（Windows）能编译并运行 internal/web 的单元测试）。
func readDiskStats() []DiskStats { return nil }
