package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// handleTaskStream 分发任务相关请求：
//
//	GET /api/tasks/current      - 当前正在执行的任务（页面刷新后用于恢复进度弹窗）
//	GET /api/tasks/{id}         - 任务状态快照
//	GET /api/tasks/{id}/stream  - 以 SSE 推送任务日志，直到任务结束
func (s *Server) handleTaskStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "仅支持 GET")
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
	rest = strings.Trim(rest, "/")
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeJSONError(w, http.StatusBadRequest, "缺少任务 ID")
		return
	}
	taskID := parts[0]

	// 当前任务：页面刷新后据此恢复
	if taskID == "current" {
		cur := s.tasks.Current()
		if cur == nil {
			writeJSON(w, http.StatusOK, map[string]any{"task": nil})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"task": cur.Snapshot()})
		return
	}

	// 只要状态快照
	if len(parts) == 1 {
		task := s.tasks.Get(taskID)
		if task == nil {
			writeJSONError(w, http.StatusNotFound, "任务不存在或已过期")
			return
		}
		writeJSON(w, http.StatusOK, task.Snapshot())
		return
	}

	if parts[1] != "stream" {
		writeJSONError(w, http.StatusNotFound, "未知的任务子资源: "+parts[1])
		return
	}

	task := s.tasks.Get(taskID)
	if task == nil {
		writeJSONError(w, http.StatusNotFound, "任务不存在或已过期")
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSONError(w, http.StatusInternalServerError, "当前连接不支持流式输出")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	sse := newSSEWriter(w, flusher)

	// from 支持断线重连时续传
	from := 0
	if v := r.URL.Query().Get("from"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			from = n
		}
	}

	// 先补发历史日志（订阅前生成的）
	lines, nextSeq := task.Lines()
	for _, ln := range lines {
		if ln.Seq >= from {
			if err := sse.event("log", ln); err != nil {
				return
			}
		}
	}

	// 任务已结束则直接收尾
	if task.State != TaskRunning {
		_ = sse.event("done", task.Snapshot())
		return
	}

	_ = sse.event("state", task.Snapshot())

	ch := task.subscribe()
	defer task.unsubscribe(ch)

	// 心跳，避免中间代理断开空闲连接
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return

		case ln, open := <-ch:
			if !open {
				_ = sse.event("done", task.Snapshot())
				return
			}
			if ln.Seq < nextSeq {
				continue // 已补发过
			}
			if err := sse.event("log", ln); err != nil {
				return
			}

		case <-task.Done():
			// 任务结束：把订阅缓冲中剩余的日志吐完再收尾
			for {
				select {
				case ln := <-ch:
					if ln.Seq >= nextSeq {
						_ = sse.event("log", ln)
					}
					continue
				default:
				}
				break
			}
			_ = sse.event("done", task.Snapshot())
			return

		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// decodeJSON 解析请求体 JSON（限制体积）
func decodeJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	return dec.Decode(dst)
}

// decodeJSONQuiet 与 decodeJSON 类似，但允许空请求体。
// 用于"可选的请求体"场景（如卸载时可能不带向导参数）。
func decodeJSONQuiet(r *http.Request, dst any) error {
	if r.Body == nil {
		return nil
	}
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(dst); err != nil {
		if err == io.EOF {
			return nil // 空 body 不是错误
		}
		return err
	}
	return nil
}
