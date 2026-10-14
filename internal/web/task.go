package web

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"fpkm/internal/logger"
)

// TaskState 任务状态
type TaskState string

const (
	TaskRunning TaskState = "running"
	TaskSuccess TaskState = "success"
	TaskFailed  TaskState = "failed"
)

// LogLine 一行任务日志
type LogLine struct {
	Seq   int    `json:"seq"`
	Level string `json:"level"`
	Text  string `json:"text"`
	TS    int64  `json:"ts"`
}

// TaskProgress 下载类任务的进度
type TaskProgress struct {
	Done  int64 `json:"done"`
	Total int64 `json:"total"`
	// Percent 0-100；Total 未知时为 -1
	Percent int `json:"percent"`
}

// Task 一次长耗时操作（下载/安装/卸载等）的可观测记录
type Task struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"`
	Title   string    `json:"title"`
	State   TaskState `json:"state"`
	Error   string    `json:"error,omitempty"`
	Started int64     `json:"started"`
	Ended   int64     `json:"ended,omitempty"`

	// Progress 下载进度（仅 kind=download 时有意义）
	Progress *TaskProgress `json:"progress,omitempty"`
	// Result 任务成功后的结构化结果（如下载得到的包路径与向导）
	Result map[string]any `json:"result,omitempty"`

	mu       sync.Mutex
	lines    []LogLine
	subs     map[chan LogLine]struct{}
	finished chan struct{}
	seq      int
}

// setProgress 更新下载进度并广播（供 SSE 实时显示）
func (t *Task) setProgress(done, total int64) {
	pct := -1
	if total > 0 {
		pct = int(done * 100 / total)
		if pct > 100 {
			pct = 100
		}
	}
	t.mu.Lock()
	t.Progress = &TaskProgress{Done: done, Total: total, Percent: pct}
	t.mu.Unlock()
}

// setResult 记录任务结果（在 finish 之前调用）
func (t *Task) setResult(res map[string]any) {
	t.mu.Lock()
	t.Result = res
	t.mu.Unlock()
}

// append 记录一行日志并广播给订阅者
func (t *Task) append(level LogLevel, text string) {
	t.mu.Lock()
	t.seq++
	line := LogLine{Seq: t.seq, Level: string(level), Text: text, TS: nowMillis()}
	t.lines = append(t.lines, line)
	subs := make([]chan LogLine, 0, len(t.subs))
	for ch := range t.subs {
		subs = append(subs, ch)
	}
	t.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- line:
		default:
			// 订阅者消费过慢时丢弃该行，避免阻塞任务本身
		}
	}
}

// finish 结束任务
func (t *Task) finish(state TaskState, errMsg string) {
	t.mu.Lock()
	t.State = state
	t.Error = errMsg
	t.Ended = nowMillis()
	close(t.finished)
	t.mu.Unlock()
}

// Lines 返回已有日志快照及下一条序号
func (t *Task) Lines() ([]LogLine, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]LogLine, len(t.lines))
	copy(out, t.lines)
	return out, t.seq + 1
}

// Snapshot 返回任务状态快照
func (t *Task) Snapshot() map[string]any {
	t.mu.Lock()
	defer t.mu.Unlock()
	m := map[string]any{
		"id":      t.ID,
		"kind":    t.Kind,
		"title":   t.Title,
		"state":   t.State,
		"error":   t.Error,
		"started": t.Started,
		"ended":   t.Ended,
		"lines":   len(t.lines),
	}
	if t.Progress != nil {
		m["progress"] = t.Progress
	}
	if t.Result != nil {
		m["result"] = t.Result
	}
	return m
}

// subscribe 订阅后续日志
func (t *Task) subscribe() chan LogLine {
	ch := make(chan LogLine, 256)
	t.mu.Lock()
	t.subs[ch] = struct{}{}
	t.mu.Unlock()
	return ch
}

func (t *Task) unsubscribe(ch chan LogLine) {
	t.mu.Lock()
	delete(t.subs, ch)
	t.mu.Unlock()
}

// Done 任务结束信号
func (t *Task) Done() <-chan struct{} { return t.finished }

// LogLevel 日志级别（用于任务日志着色）
type LogLevel = string

// TaskManager 管理进行中的任务，同时负责把 logger 输出接到当前任务
type TaskManager struct {
	mu    sync.Mutex
	tasks map[string]*Task
	order []string
	cur   *Task // 当前正在执行的任务（用于 logger 旁路）
}

// NewTaskManager 创建任务管理器
func NewTaskManager() *TaskManager {
	tm := &TaskManager{tasks: map[string]*Task{}}
	// 把 logger 的输出旁路到当前任务
	logger.SetSink(func(level logger.LogLevel, prefix, msg string) {
		tm.mu.Lock()
		cur := tm.cur
		tm.mu.Unlock()
		if cur != nil {
			cur.append(LogLevel(levelName(level)), msg)
		}
	})
	return tm
}

// TryBegin 原子地占用执行权并创建任务。
// 若已有任务在执行则返回该任务与 false，调用方可据此拒绝请求。
//
// 安装类操作必须用它来串行化：Manager 的安装流程会创建/移动同一批目录，
// 并发执行会互相破坏。仅靠 Busy() 判断存在 TOCTOU 竞态。
func (tm *TaskManager) TryBegin(kind, title string) (*Task, bool) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if tm.cur != nil && tm.cur.State == TaskRunning {
		return tm.cur, false
	}
	t := tm.newTaskLocked(kind, title)
	tm.cur = t
	return t, true
}

// newTaskLocked 在已持有 tm.mu 的前提下构造并登记任务
func (tm *TaskManager) newTaskLocked(kind, title string) *Task {
	t := &Task{
		ID:       fmt.Sprintf("%d-%s", time.Now().UnixNano(), kind),
		Kind:     kind,
		Title:    title,
		State:    TaskRunning,
		Started:  nowMillis(),
		subs:     map[chan LogLine]struct{}{},
		finished: make(chan struct{}),
	}
	tm.tasks[t.ID] = t
	tm.order = append(tm.order, t.ID)
	// 仅保留最近 50 条任务记录
	if len(tm.order) > 50 {
		drop := tm.order[0]
		tm.order = tm.order[1:]
		delete(tm.tasks, drop)
	}
	return t
}

// Clear 解除当前任务绑定（任务结束或让出）
func (tm *TaskManager) Clear(t *Task) {
	tm.mu.Lock()
	if tm.cur == t {
		tm.cur = nil
	}
	tm.mu.Unlock()
}

// Get 按 ID 取任务
func (tm *TaskManager) Get(id string) *Task {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.tasks[id]
}

// Busy 是否有任务在执行。
// 仅用于展示/诊断；要占用执行权请使用 TryBegin（本方法单独使用存在竞态）。
func (tm *TaskManager) Busy() (*Task, bool) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if tm.cur != nil && tm.cur.State == TaskRunning {
		return tm.cur, true
	}
	return nil, false
}

// Current 返回当前任务（可能为 nil）
func (tm *TaskManager) Current() *Task {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return tm.cur
}

func levelName(l logger.LogLevel) string {
	switch l {
	case logger.LevelDebug:
		return "debug"
	case logger.LevelInfo:
		return "info"
	case logger.LevelWarn:
		return "warn"
	case logger.LevelError:
		return "error"
	}
	return "info"
}

// sseWriter 便捷的 SSE 写出封装
type sseWriter struct {
	w io.Writer
	f interface{ Flush() }
}

func newSSEWriter(w io.Writer, f interface{ Flush() }) *sseWriter {
	return &sseWriter{w: w, f: f}
}

func (s *sseWriter) event(name string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", name, data); err != nil {
		return err
	}
	if s.f != nil {
		s.f.Flush()
	}
	return nil
}
