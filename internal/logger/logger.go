package logger

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LogLevel 日志级别
type LogLevel int

const (
	LevelDebug LogLevel = iota
	LevelInfo
	LevelWarn
	LevelError
)

// TimeFormat 是日志时间戳格式，记录日志的**发生时间**（本地时区）。
//
// 文件日志、控制台日志、网络适配诊断日志与应用子进程输出（logs/app.log）
// 统一使用该格式：每行日志都以发生时间开头，便于事后按时间检索与比对。
const TimeFormat = "2006-01-02 15:04:05"

// logDateLayout 是日志文件按日期切分时使用的日期格式。
const logDateLayout = "2006-01-02"

// nowFn 返回当前时间。抽出为变量以便单元测试模拟跨天（默认即 time.Now）。
var nowFn = time.Now

// NowStamp 返回当前时刻的日志时间戳字符串。
func NowStamp() string { return nowFn().Format(TimeFormat) }

// dateWriter 让 FPKM 自身日志文件按「当天日期」自动切分。
//
// 每次写入前都比对一次本地日期：日期未变则写回当前文件；日期一变（跨天）
// 就关闭旧文件、按新日期新建 fpkm-<YYYY-MM-DD>.log 再写入。因此跨天后的
// 第一行日志必然落到新日期文件，旧日期文件不会被再追加任何内容。
type dateWriter struct {
	mu   sync.Mutex
	dir  string
	perm os.FileMode
	day  string // 当前文件对应的本地日期 YYYY-MM-DD
	file *os.File
}

func newDateWriter(dir string, perm os.FileMode) *dateWriter {
	return &dateWriter{dir: dir, perm: perm}
}

// filePath 返回指定日期对应的日志文件路径。
func (d *dateWriter) filePath(day string) string {
	return filepath.Join(d.dir, fmt.Sprintf("fpkm-%s.log", day))
}

// open 建立（或切换到）指定日期的日志文件。已打开其他日期的文件会被关闭。
//
// 先成功打开新文件再关闭旧文件，避免新文件创建失败时把可用句柄一起弄丢；
// 打开失败时保持原状并返回错误，调用方不得回写旧日期文件。
func (d *dateWriter) open(day string) error {
	f, err := os.OpenFile(d.filePath(day), os.O_CREATE|os.O_WRONLY|os.O_APPEND, d.perm)
	if err != nil {
		return err
	}
	if d.file != nil {
		d.file.Close()
	}
	d.file = f
	d.day = day
	return nil
}

// currentPath 返回当前日志文件路径（尚未创建文件时返回空串）。
func (d *dateWriter) currentPath() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.file == nil {
		return ""
	}
	return d.filePath(d.day)
}

// Write 实现 io.Writer：写入前按需切换到当天日期的日志文件。
func (d *dateWriter) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	today := nowFn().Format(logDateLayout)
	if d.file == nil || d.day != today {
		if err := d.open(today); err != nil {
			return 0, err
		}
	}
	return d.file.Write(p)
}

// Close 关闭当前日志文件。
func (d *dateWriter) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.file == nil {
		return nil
	}
	err := d.file.Close()
	d.file = nil
	return err
}

// NewStampWriter 返回一个在每行行首插入日志发生时间的写入器。
//
// 应用子进程的 stdout/stderr 是原始字节流，本身不带时间信息；用本包装器
// 接入日志文件后，落到 logs/app.log 的每一行都会带上发生时间。
func NewStampWriter(w io.Writer) io.Writer { return newStampWriter(w) }

// stampWriter 在每行行首插入日志发生时间。
//
// 日志行可能被拆成多次 Write 写入（子进程无缓冲地输出），因此只在
// 「行首」补时间戳：首次写入、或上一次写入以换行结束时插入；行中的续写
// 片段不重复插入。这样既不缓冲（实时输出不被延迟），也不会把一行拆出
// 多个时间戳。
type stampWriter struct {
	mu          sync.Mutex
	w           io.Writer
	atLineStart bool
}

func newStampWriter(w io.Writer) *stampWriter {
	return &stampWriter{w: w, atLineStart: true}
}

// Write 实现 io.Writer，逐行补时间戳后转发给底层写入器。
func (s *stampWriter) Write(p []byte) (int, error) {
	total := len(p)

	s.mu.Lock()
	defer s.mu.Unlock()

	var buf bytes.Buffer
	for len(p) > 0 {
		if s.atLineStart {
			buf.WriteString(NowStamp())
			buf.WriteByte(' ')
			s.atLineStart = false
		}
		if i := bytes.IndexByte(p, '\n'); i >= 0 {
			buf.Write(p[:i+1])
			p = p[i+1:]
			s.atLineStart = true
		} else {
			buf.Write(p)
			p = nil
		}
	}

	if buf.Len() == 0 {
		return total, nil
	}
	if _, err := s.w.Write(buf.Bytes()); err != nil {
		return 0, err
	}
	return total, nil
}

// adaptWriter 是「网络适配诊断日志」的输出目标（供 internal/netadapt 使用）。
//
// 它存在的原因是**时序**：`internal/netadapt` 在包 `init` 阶段就安装 DNS 适配
// 并写诊断日志（见 fpkm_dns_override.go），那时 `Init` 还没被调用、日志文件
// 句柄还不存在。若直接写 stderr，这些行只会出现在控制台，永远不会落进
// `logs/fpkm-<日期>.log`（表现为「[fpkm-net] 只在控制台可见、日志文件里没有」）。
//
// 因此本写入器：
//   - `Init` 之前：把整行缓存起来（上限 adaptBufferMax 行，超出丢弃最早的），
//     同时照旧写到 stderr，控制台行为不变；
//   - `Init` 之后：先把缓存的行补写进当天日志文件，再逐行写文件并镜像到 stderr。
//
// 每行行首补「发生时间」，格式与其它日志完全一致（本地时区），且与 logger
// 自身的 stampWriter 相互独立，不会重复打时间戳。
type adaptWriter struct {
	mu          sync.Mutex
	atLineStart bool
	pending     []string
}

// adaptBufferMax 是 Init 之前最多缓存的诊断日志行数。
const adaptBufferMax = 256

// AdaptWriter 返回网络适配诊断日志的输出目标。
func AdaptWriter() io.Writer { return &adaptWriter{atLineStart: true} }

// Write 实现 io.Writer：补行首时间戳后，写日志文件（Init 之后）并镜像到 stderr。
func (a *adaptWriter) Write(p []byte) (int, error) {
	total := len(p)

	a.mu.Lock()
	defer a.mu.Unlock()

	var buf bytes.Buffer
	for len(p) > 0 {
		if a.atLineStart {
			buf.WriteString(NowStamp())
			buf.WriteByte(' ')
			a.atLineStart = false
		}
		if i := bytes.IndexByte(p, '\n'); i >= 0 {
			buf.Write(p[:i+1])
			p = p[i+1:]
			a.atLineStart = true
		} else {
			buf.Write(p)
			p = nil
		}
	}
	if buf.Len() == 0 {
		return total, nil
	}

	inst := current()
	if inst == nil {
		// 日志系统尚未初始化：缓存整行，等 Init 后补写进日志文件。
		a.buffer(buf.String())
		_, _ = os.Stderr.Write(buf.Bytes())
		return total, nil
	}

	// 补写 Init 之前缓存的诊断行，保证它们在文件里的先后顺序与发生顺序一致。
	if len(a.pending) > 0 {
		if _, err := inst.writer.Write([]byte(strings.Join(a.pending, ""))); err != nil {
			// 文件写失败不吞掉控制台输出，下面继续写 stderr
		}
		a.pending = nil
	}
	if _, err := inst.writer.Write(buf.Bytes()); err != nil {
		// 同上：文件不可写时仍保证控制台可见
	}
	_, _ = os.Stderr.Write(buf.Bytes())
	return total, nil
}

// buffer 按整行缓存诊断日志，超过上限时丢弃最早的行。
// （诊断日志只用于事后排查，不需要无限增长。）
func (a *adaptWriter) buffer(s string) {
	for len(s) > 0 {
		var line string
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			line, s = s[:i+1], s[i+1:]
		} else {
			line, s = s, ""
		}
		a.pending = append(a.pending, line)
	}
	if len(a.pending) > adaptBufferMax {
		a.pending = append([]string(nil), a.pending[len(a.pending)-adaptBufferMax:]...)
	}
}

// Logger 日志记录器
type Logger struct {
	level   LogLevel
	fileLog *log.Logger
	console *log.Logger
	writer  *dateWriter
}

var (
	// instanceMu 保护 instance：Init 在启动早期赋值，而 adaptWriter 可能被
	// 后台 goroutine（如 netadapt 的 DNS 预探测）并发读写。
	instanceMu sync.RWMutex
	instance   *Logger
)

// current 返回当前已初始化的日志记录器；未初始化时返回 nil。
func current() *Logger {
	instanceMu.RLock()
	defer instanceMu.RUnlock()
	return instance
}

// sink 是一个可选的额外日志输出目标。
// 供 Web 界面实时捕获安装/启停过程的日志（见 internal/web/task.go）。
var (
	sinkMu sync.RWMutex
	sinkFn func(level LogLevel, prefix, msg string)
)

// SetSink 设置日志旁路输出。传入 nil 取消。
// 注意：同一时刻只应有一个消费者（Web 层通过串行化操作来保证）。
func SetSink(fn func(level LogLevel, prefix, msg string)) {
	sinkMu.Lock()
	defer sinkMu.Unlock()
	sinkFn = fn
}

// emit 将日志投递给旁路消费者
func emit(level LogLevel, prefix, msg string) {
	sinkMu.RLock()
	fn := sinkFn
	sinkMu.RUnlock()
	if fn != nil {
		fn(level, prefix, msg)
	}
}

// Init 初始化全局日志
//
// 日志文件按本地日期切分：启动时落到 fpkm-<当天>.log，跨天后自动改写到
// fpkm-<新日期>.log，旧日期文件不再被追加（见 dateWriter）。
func Init(logDir string, level LogLevel) error {
	if old := current(); old != nil {
		old.Close()
	}

	writer := newDateWriter(logDir, 0644)
	// 启动时就建立当天文件，便于外部在程序刚起来时即可看到日志文件；
	// 之后每次写入都会再次核对日期，跨天即切换。
	if err := writer.open(nowFn().Format(logDateLayout)); err != nil {
		return err
	}

	// 文件与控制台都不再依赖 log 包自身的 flags：时间戳由 stampWriter 统一
	// 插入，保证两种输出的行首格式完全一致，均为「发生时间 级别 内容」。
	instanceMu.Lock()
	instance = &Logger{
		level:   level,
		fileLog: log.New(newStampWriter(writer), "", 0),
		console: log.New(newStampWriter(os.Stdout), "", 0),
		writer:  writer,
	}
	instanceMu.Unlock()
	return nil
}

// Close 关闭日志。
//
// 关闭后把 `instance` 归零：这样后续诊断日志（netadapt 的 AdaptWriter）
// 会回到「初始化前」的行为——缓存并只写 stderr，而不会写向已关闭的句柄
// 或让 dateWriter 重新打开一份旧日期的文件。
func Close() {
	instanceMu.Lock()
	inst := instance
	instance = nil
	instanceMu.Unlock()
	if inst != nil {
		inst.Close()
	}
}

func (l *Logger) Close() {
	if l.writer != nil {
		l.writer.Close()
	}
}

// filePath 返回当前日志文件路径；日志未初始化或文件未建立时返回空串。
// 供内部与测试查询「此刻写入的是哪一天的文件」。
func (l *Logger) filePath() string {
	if l.writer == nil {
		return ""
	}
	return l.writer.currentPath()
}

func (l *Logger) log(level LogLevel, prefix string, format string, args ...interface{}) {
	if level < l.level {
		return
	}
	msg := fmt.Sprintf(format, args...)
	l.fileLog.Printf("%s%s", prefix, msg)
	l.console.Printf("%s%s", prefix, msg)
	emit(level, prefix, msg)
}

// printfStamp 在日志系统尚未初始化时直接输出带时间戳的日志行。
func printfStamp(prefix, format string, args ...interface{}) {
	fmt.Printf("%s "+prefix+format+"\n", append([]interface{}{NowStamp()}, args...)...)
}

// Debug 调试日志
func Debug(format string, args ...interface{}) {
	if inst := current(); inst != nil {
		inst.log(LevelDebug, "[DEBUG] ", format, args...)
	}
}

// Info 信息日志
func Info(format string, args ...interface{}) {
	if inst := current(); inst != nil {
		inst.log(LevelInfo, "[INFO]  ", format, args...)
	} else {
		printfStamp("[INFO]  ", format, args...)
	}
}

// Warn 警告日志
func Warn(format string, args ...interface{}) {
	if inst := current(); inst != nil {
		inst.log(LevelWarn, "[WARN]  ", format, args...)
	} else {
		printfStamp("[WARN]  ", format, args...)
	}
}

// Error 错误日志
func Error(format string, args ...interface{}) {
	if inst := current(); inst != nil {
		inst.log(LevelError, "[ERROR] ", format, args...)
	} else {
		printfStamp("[ERROR] ", format, args...)
	}
}

// Success 成功提示
func Success(format string, args ...interface{}) {
	if inst := current(); inst != nil {
		msg := fmt.Sprintf("[OK]    "+format, args...)
		inst.console.Println(msg)
		inst.fileLog.Println(msg)
	} else {
		printfStamp("[OK]    ", format, args...)
	}
}

// Fatal 致命错误，输出后退出
func Fatal(format string, args ...interface{}) {
	Error(format, args...)
	os.Exit(1)
}
