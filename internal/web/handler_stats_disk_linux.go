//go:build !windows

package web

import (
	"bufio"
	"os"
	"strings"
	"syscall"
)

// readDiskStats 读取主要挂载点的磁盘使用情况。
//
// 只取容量较大的真实挂载点（跳过 tmpfs/overlay 等虚拟文件系统），
// 避免小组件里出现一堆无意义的条目。
func readDiskStats() []DiskStats {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return nil
	}
	defer f.Close()

	seen := map[string]bool{}
	var out []DiskStats
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Fields(sc.Text())
		if len(parts) < 3 {
			continue
		}
		device, mount, fstype := parts[0], parts[1], parts[2]

		switch fstype {
		case "tmpfs", "devtmpfs", "overlay", "squashfs", "proc", "sysfs", "cgroup", "cgroup2", "ramfs":
			continue
		}
		if !strings.HasPrefix(device, "/dev/") {
			continue
		}
		// 只保留顶层挂载点，减少噪音
		if mount != "/" && strings.Count(mount, "/") > 2 {
			continue
		}
		if seen[mount] {
			continue
		}
		seen[mount] = true

		var st syscall.Statfs_t
		if err := syscall.Statfs(mount, &st); err != nil {
			continue
		}
		total := st.Blocks * uint64(st.Bsize)
		avail := st.Bavail * uint64(st.Bsize)
		if total == 0 {
			continue
		}
		used := total - st.Bfree*uint64(st.Bsize)
		d := DiskStats{
			Mount:     mount,
			Total:     total,
			Used:      used,
			Available: avail,
			Percent:   float64(used) / float64(total) * 100,
		}
		out = append(out, d)
		if len(out) >= 6 {
			break
		}
	}
	return out
}
