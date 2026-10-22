package app

import (
	"io"
	"os"

	"fpkm/internal/logger"
)

// openAppLog 打开（或创建）应用日志文件，返回会在每行行首自动补上
// 日志发生时间的写入器。
//
// 应用子进程（主进程、预启动命令、附加进程、脚本）的 stdout/stderr
// 直接重定向到该写入器，于是落进 logs/app.log 的每一行都带发生时间。
func openAppLog(path string) (io.WriteCloser, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	return &stampedFile{f: f, w: logger.NewStampWriter(f)}, nil
}

// stampedFile 把底层 *os.File 与时间戳包装写入器合成一个 io.WriteCloser：
// 既可以作为子进程的输出目标，也保留按原语义关闭文件的能力。
type stampedFile struct {
	f *os.File
	w io.Writer
}

func (s *stampedFile) Write(p []byte) (int, error) { return s.w.Write(p) }

func (s *stampedFile) Close() error { return s.f.Close() }
