package web

import (
	"encoding/json"
	"os"
	"sync"

	"fpkm/internal/config"
	"fpkm/internal/logger"
)

// autostartStore 持久化"应当保持运行"的应用列表。
//
// 设计要点：
//   - 记录的是**用户意图**（用户点过启动，且没有点停止/卸载），
//     不是"进程当时是否活着"。因为 fpkm 退出时子进程必然结束，
//     用进程状态做判据会导致永远重启不了。
//   - 幂等写入：同一应用重复标记不会产生重复项。
//   - 读失败（文件不存在/损坏）时返回空集，不让 FPKM 启动失败。
type autostartStore struct {
	mu   sync.Mutex
	path string
}

func newAutostartStore(cfg *config.Config) *autostartStore {
	return &autostartStore{path: cfg.AutostartFile()}
}

// list 返回当前记录的应运行应用名（去重、保序）。
func (a *autostartStore) list() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.loadLocked()
}

func (a *autostartStore) loadLocked() []string {
	data, err := os.ReadFile(a.path)
	if err != nil {
		return nil
	}
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		// 文件损坏：记录告警并按空处理，避免阻塞启动
		logger.Warn("autostart 记录解析失败，已忽略: %v", err)
		return nil
	}
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// save 原子写盘（临时文件 + rename），避免掉电/并发读到半个文件。
func (a *autostartStore) save(names []string) error {
	data, err := json.MarshalIndent(names, "", "  ")
	if err != nil {
		return err
	}
	tmp := a.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, a.path)
}

// mark 记录某应用应保持运行。
func (a *autostartStore) mark(appname string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	names := a.loadLocked()
	for _, n := range names {
		if n == appname {
			return nil // 已记录
		}
	}
	return a.save(append(names, appname))
}

// unmark 移除记录（用户主动停止或卸载）。
func (a *autostartStore) unmark(appname string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	names := a.loadLocked()
	out := names[:0]
	changed := false
	for _, n := range names {
		if n == appname {
			changed = true
			continue
		}
		out = append(out, n)
	}
	if !changed {
		return nil
	}
	return a.save(out)
}

// prune 去掉已不存在的应用（卸载后残留的记录）。
func (a *autostartStore) prune(exists map[string]bool) []string {
	a.mu.Lock()
	defer a.mu.Unlock()

	names := a.loadLocked()
	out := names[:0]
	changed := false
	for _, n := range names {
		if !exists[n] {
			changed = true
			continue
		}
		out = append(out, n)
	}
	if changed {
		if err := a.save(out); err != nil {
			logger.Warn("清理 autostart 记录失败: %v", err)
		}
	}
	return out
}
