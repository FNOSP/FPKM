package gateway

import (
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"fpkm/internal/config"
	"fpkm/internal/logger"
	"fpkm/internal/session"
)

// 捕获日志的辅助：返回收集函数与结束还原
func captureLogs(t *testing.T) func() []struct {
	Level logger.LogLevel
	Msg   string
} {
	t.Helper()
	mu := &sync.Mutex{}
	var collected []struct {
		Level logger.LogLevel
		Msg   string
	}
	logger.SetSink(func(l logger.LogLevel, _, msg string) {
		mu.Lock()
		defer mu.Unlock()
		collected = append(collected, struct {
			Level logger.LogLevel
			Msg   string
		}{l, msg})
	})
	t.Cleanup(func() { logger.SetSink(nil) })
	return func() []struct {
		Level logger.LogLevel
		Msg   string
	} {
		mu.Lock()
		defer mu.Unlock()
		out := make([]struct {
			Level logger.LogLevel
			Msg   string
		}, len(collected))
		copy(out, collected)
		return out
	}
}

// 回归：一次 HTTP 请求走完网关，只应产生 Debug 及以下日志。
//
// 背景：此前每个请求会打十几行 Info（[GATEWAY] -->/转发/完成、
// [BUFFER] 逐个响应头、[IDLE] 每次读取……），正常使用时日志刷屏，
// 真正有用的事件（路由增删、错误）被淹没。逐请求日志已全部降级为 Debug。
func TestPerRequestLogsAreNotInfo(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "app.sock")

	// Unix socket 后端：返回一个普通响应
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	srv := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Vary", "Accept-Encoding")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	st, err := session.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	g := &Gateway{
		cfg:      &config.Config{BaseDir: dir},
		port:     DefaultPort,
		routes:   map[string]*Route{},
		gate:     true,
		sessions: st,
		embedded: true,
	}
	g.AddRoute("demo", "/app/demo", sockPath)

	handler := g.Mount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))

	tok, _, err := st.Issue()
	if err != nil {
		t.Fatal(err)
	}

	get := func() []struct {
		Level logger.LogLevel
		Msg   string
	} {
		logs := captureLogs(t)
		req := httptest.NewRequest(http.MethodGet, "http://nas/app/demo/api/x", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tok})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("后端应返回 200，实际 %d", w.Code)
		}
		return logs()
	}

	// 多发几次（含 404 与未授权路径），覆盖各分支
	get()
	get()

	// 404：未注册路由
	{
		logs := captureLogs(t)
		req := httptest.NewRequest(http.MethodGet, "http://nas/app/nope/x", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tok})
		handler.ServeHTTP(httptest.NewRecorder(), req)
		logs()
	}

	// 未登录（登录跳转路径）
	{
		logs := captureLogs(t)
		req := httptest.NewRequest(http.MethodGet, "http://nas/app/demo/api/y", nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)
		logs()
	}

	// 关键断言：没有任何 Info/Warn 级别的逐请求日志
	all := append(get(), get()...)
	for _, l := range all {
		if l.Level == logger.LevelInfo {
			t.Errorf("请求不应产生 Info 日志，实际: %s", l.Msg)
		}
	}

}
