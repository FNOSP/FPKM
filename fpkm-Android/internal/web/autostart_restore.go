package web

import (
	"os"
	"sync"

	"fpkm/internal/app"
	"fpkm/internal/config"
	"fpkm/internal/logger"
)

// RestoreAutostart 在 FPKM 启动时拉起"上次处于运行状态"的应用。
//
// 为什么需要它：应用是 fpkm 拉起的子进程，fpkm 退出后它们随之结束。
// 用户希望重新打开 FPKM 桌面时，上次在跑的应用能自动回来。
//
// 实现要点：
//   - 记录的是用户意图（点过启动且没点停止），见 autostartStore；
//   - 逐个启动，单个失败不影响其它应用；
//   - 已不在安装目录里的记录会被清理（卸载残留）；
//   - 异步执行，不阻塞 Web 服务开始监听。
func RestoreAutostart(cfg *config.Config, srv *Server) {
	mgr := app.NewManager(cfg)

	store := newAutostartStore(cfg)
	names := store.list()
	if len(names) == 0 {
		return
	}

	go func() {
		// 先算出真实存在的应用集合，顺手清掉卸载残留
		infos, err := mgr.List()
		if err != nil {
			logger.Warn("读取已安装应用失败，跳过自启动恢复: %v", err)
			return
		}
		exists := make(map[string]bool, len(infos))
		running := make(map[string]bool, len(infos))
		for _, i := range infos {
			exists[i.Name] = true
			// AppInfo 只有静态元数据，实际运行状态要看 Status()
			if st, err := mgr.Status(i.Name); err == nil && st.Running {
				running[i.Name] = true
			}
		}

		targets := store.prune(exists)
		if len(targets) == 0 {
			return
		}

		logger.Info("正在恢复 %d 个应用的自启动…", len(targets))
		var wg sync.WaitGroup
		var mu sync.Mutex
		ok, failed := 0, 0

		for _, name := range targets {
			if running[name] {
				continue // 已在运行，无需重复启动
			}
			wg.Add(1)
			go func(name string) {
				defer wg.Done()
				if err := mgr.Start(name); err != nil {
					mu.Lock()
					failed++
					mu.Unlock()
					logger.Warn("自动恢复应用 %s 失败: %v", name, err)
					return
				}
				mu.Lock()
				ok++
				mu.Unlock()
				logger.Info("已自动恢复应用: %s", name)
			}(name)
		}
		wg.Wait()

		if srv != nil {
			srv.registerAppRoutes()
		}
		logger.Info("自启动恢复完成：成功 %d 个，失败 %d 个", ok, failed)
	}()
}

// registerAppRoutes 把已安装应用的网关路由注册到内嵌网关。
//
// 与 cmd/web.go 的启动扫描相同，但这里按需调用：
// 自启动恢复完成后 socket 才存在，需要重新登记一次，
// 否则 /app/* 会一直返回 404 直到下次重启。
func (s *Server) registerAppRoutes() {
	gw := s.Gateway()
	if gw == nil {
		return
	}
	infos, err := s.mgr.List()
	if err != nil {
		return
	}
	for _, info := range infos {
		env, err := app.LoadEnvFile(s.cfg.AppEnvFile(info.Name))
		if err != nil || env == nil {
			continue
		}
		prefix := env["GATEWAY_PREFIX"]
		if prefix == "" {
			continue
		}
		switch {
		case env["CGI_PATH"] != "":
			gw.AddRouteCGI(info.Name, prefix, env["CGI_PATH"])
		case env["SOCKET_PATH"] != "":
			if _, err := os.Stat(env["SOCKET_PATH"]); err == nil {
				gw.AddRoute(info.Name, prefix, env["SOCKET_PATH"])
			}
		}
	}
}
