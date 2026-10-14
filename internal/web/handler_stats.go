package web

import (
	"bufio"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// SystemStats 桌面右侧小组件所需的系统信息
type SystemStats struct {
	Hostname string `json:"hostname"`
	Platform string `json:"platform"`
	Uptime   int64  `json:"uptime"` // 秒

	// 时间信息（前端用自己的时钟，这里提供服务器时间作为校准）
	ServerTime string `json:"serverTime"`

	CPU CPUStats `json:"cpu"`
	Mem MemStats `json:"mem"`

	// Disks 各挂载点使用情况
	Disks []DiskStats `json:"disks"`

	// Store 应用商店自身统计
	Store StoreStats `json:"store"`
}

// CPUStats CPU 负载
type CPUStats struct {
	// Load1/Load5/Load15 系统平均负载
	Load1  float64 `json:"load1"`
	Load5  float64 `json:"load5"`
	Load15 float64 `json:"load15"`
	// Cores 逻辑核心数
	Cores int `json:"cores"`
	// Usage 近似使用率（load1 / cores，0-100）
	Usage float64 `json:"usage"`
}

// MemStats 内存使用
type MemStats struct {
	Total     uint64  `json:"total"`
	Used      uint64  `json:"used"`
	Available uint64  `json:"available"`
	Percent   float64 `json:"percent"`
}

// DiskStats 单个挂载点
type DiskStats struct {
	Mount     string  `json:"mount"`
	Total     uint64  `json:"total"`
	Used      uint64  `json:"used"`
	Available uint64  `json:"available"`
	Percent   float64 `json:"percent"`
}

// StoreStats 应用统计
type StoreStats struct {
	Installed int `json:"installed"`
	Running   int `json:"running"`
	Total     int `json:"total"`
}

// handleSystemStats GET /api/system/stats
// 为桌面右侧小组件提供内存/存储/负载等信息。
func (s *Server) handleSystemStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "仅支持 GET")
		return
	}

	host, _ := os.Hostname()
	stats := SystemStats{
		Hostname:   host,
		Platform:   "linux",
		ServerTime: time.Now().Format(time.RFC3339),
		CPU:        readCPUStats(),
		Mem:        readMemStats(),
		Disks:      readDiskStats(),
		Store:      s.readStoreStats(),
	}
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			if v, err := strconv.ParseFloat(f[0], 64); err == nil {
				stats.Uptime = int64(v)
			}
		}
	}

	writeJSON(w, http.StatusOK, stats)
}

// readCPUStats 读取 /proc/loadavg 计算负载
func readCPUStats() CPUStats {
	c := CPUStats{Cores: 1}
	data, err := os.ReadFile("/proc/loadavg")
	if err == nil {
		f := strings.Fields(string(data))
		if len(f) >= 3 {
			c.Load1, _ = strconv.ParseFloat(f[0], 64)
			c.Load5, _ = strconv.ParseFloat(f[1], 64)
			c.Load15, _ = strconv.ParseFloat(f[2], 64)
		}
	}
	// 逻辑核心数（跳过 "cpuN" 行外的统计行）
	if f, err := os.Open("/proc/stat"); err == nil {
		sc := bufio.NewScanner(f)
		n := 0
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "cpu") && len(line) > 3 && line[3] >= '0' && line[3] <= '9' {
				n++
			}
		}
		f.Close()
		if n > 0 {
			c.Cores = n
		}
	}
	if c.Cores > 0 {
		c.Usage = c.Load1 / float64(c.Cores) * 100
		if c.Usage > 100 {
			c.Usage = 100
		}
	}
	return c
}

// readMemStats 读取 /proc/meminfo
func readMemStats() MemStats {
	var m MemStats
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return m
	}
	defer f.Close()

	vals := map[string]uint64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Fields(sc.Text())
		if len(parts) < 2 {
			continue
		}
		key := strings.TrimSuffix(parts[0], ":")
		v, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil {
			continue
		}
		vals[key] = v * 1024 // kB → B
	}

	m.Total = vals["MemTotal"]
	available := vals["MemAvailable"]
	if available == 0 {
		available = vals["MemFree"]
	}
	m.Available = available
	if m.Total > 0 {
		m.Used = m.Total - available
		m.Percent = float64(m.Used) / float64(m.Total) * 100
	}
	return m
}

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

// readStoreStats 应用统计（已安装 / 运行中 / 商店总数）
func (s *Server) readStoreStats() StoreStats {
	var st StoreStats
	if infos, err := s.mgr.List(); err == nil {
		st.Installed = len(infos)
		for _, info := range infos {
			if status, err := s.mgr.Status(info.Name); err == nil && status != nil && status.Running {
				st.Running++
			}
		}
	}
	if apps, _, err := s.catalog.List(false); err == nil {
		st.Total = len(apps)
	}
	return st
}
