package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
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

// Logger 日志记录器
type Logger struct {
	level   LogLevel
	fileLog *log.Logger
	console *log.Logger
	logFile *os.File
}

var instance *Logger

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
func Init(logDir string, level LogLevel) error {
	if instance != nil {
		instance.Close()
	}

	logFile, err := os.OpenFile(
		filepath.Join(logDir, fmt.Sprintf("fpkm-%s.log", time.Now().Format("2006-01-02"))),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}

	instance = &Logger{
		level:   level,
		fileLog: log.New(io.MultiWriter(logFile), "", log.LstdFlags|log.Lshortfile),
		console: log.New(os.Stdout, "", 0),
		logFile: logFile,
	}
	return nil
}

// Close 关闭日志
func Close() {
	if instance != nil {
		instance.Close()
	}
}

func (l *Logger) Close() {
	if l.logFile != nil {
		l.logFile.Close()
	}
}

func (l *Logger) log(level LogLevel, prefix string, format string, args ...interface{}) {
	if level < l.level {
		return
	}
	msg := fmt.Sprintf(format, args...)
	l.fileLog.Printf("[%s] %s", prefix, msg)
	l.console.Printf("%s%s", prefix, msg)
	emit(level, prefix, msg)
}

// Debug 调试日志
func Debug(format string, args ...interface{}) {
	if instance != nil {
		instance.log(LevelDebug, "[DEBUG] ", format, args...)
	}
}

// Info 信息日志
func Info(format string, args ...interface{}) {
	if instance != nil {
		instance.log(LevelInfo, "[INFO]  ", format, args...)
	} else {
		fmt.Printf("[INFO]  "+format+"\n", args...)
	}
}

// Warn 警告日志
func Warn(format string, args ...interface{}) {
	if instance != nil {
		instance.log(LevelWarn, "[WARN]  ", format, args...)
	} else {
		fmt.Printf("[WARN]  "+format+"\n", args...)
	}
}

// Error 错误日志
func Error(format string, args ...interface{}) {
	if instance != nil {
		instance.log(LevelError, "[ERROR] ", format, args...)
	} else {
		fmt.Printf("[ERROR] "+format+"\n", args...)
	}
}

// Success 成功提示
func Success(format string, args ...interface{}) {
	if instance != nil {
		instance.console.Printf("[OK]    "+format, args...)
		instance.fileLog.Printf("[OK]    "+format, args...)
	} else {
		fmt.Printf("[OK]    "+format+"\n", args...)
	}
}

// Fatal 致命错误，输出后退出
func Fatal(format string, args ...interface{}) {
	Error(format, args...)
	os.Exit(1)
}
