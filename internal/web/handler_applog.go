package web

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// AppLogFileView 单个日志文件的内容视图。
type AppLogFileView struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	ModTime   string `json:"modTime,omitempty"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

// AppLogsView 是「查看所有日志」的返回体：列出应用日志目录下的全部日志文件，
// 每个文件带回尾部内容（超过上限时从完整行开始截断）。
type AppLogsView struct {
	Name      string           `json:"name"`
	Dir       string           `json:"dir"`
	TailBytes int64            `json:"tailBytes"`
	Files     []AppLogFileView `json:"files"`
	Missing   bool             `json:"missing,omitempty"`
	Hint      string           `json:"hint,omitempty"`

	// TZOffsetSeconds 是**写入日志的宿主**当前时区相对 UTC 的偏移秒数（东为正）。
	//
	// 日志行首的发生时间由宿主进程按其本地时区写入（见 logger.NowStamp 使用
	// time.Now()），而宿主时区未必等于浏览者所在时区（例如 NAS 上以 UTC 运行，
	// 用户却在东八区）。前端需要这个偏移量才能把行首时间换算成**访问者本地时间**，
	// 因此随日志内容一并返回。
	TZOffsetSeconds int `json:"tzOffsetSeconds"`
}

const (
	// appLogDefaultTail 默认返回每个文件的尾部字节数（256KiB）
	appLogDefaultTail = 256 << 10
	// appLogMaxTail 单文件返回上限（4MiB），避免把超大日志整体读进内存
	appLogMaxTail = 4 << 20
	// appLogMaxFiles 最多返回的日志文件数
	appLogMaxFiles = 20
)

// handleAppLog 处理 GET /api/apps/{应用名}/log?tail=<字节数>
//
// 用于前端「查看所有日志」：把应用日志目录（<应用目录>/logs）下的所有日志文件
// 一并返回，前端可以分文件查看、过滤与下载。
func (s *Server) handleAppLog(w http.ResponseWriter, r *http.Request, name string) {
	tail := parseAppLogTail(r.URL.Query().Get("tail"))
	writeJSON(w, http.StatusOK, buildAppLogsView(name, s.cfg.AppDir(name), tail))
}

// handleFpkmLog 处理 GET /api/logs?tail=<字节数>
//
// 用于前端「FPKM 日志」页：返回 FPKM **程序自身**的日志目录
// （<数据目录>/logs，如 gateway.log 与按日期切分的日志）下的全部日志文件。
//
// 与「应用日志」（GET /api/apps/{应用名}/log）分开成两个接口，是因为两者
// 目录完全不同、用途也不同：应用日志用于排查某个应用启动失败；
// FPKM 日志用于排查程序自身（网关、宿主进程、安装流程）的问题。
func (s *Server) handleFpkmLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "仅支持 GET")
		return
	}
	tail := parseAppLogTail(r.URL.Query().Get("tail"))
	writeJSON(w, http.StatusOK, buildLogsView(
		"fpkm",
		s.cfg.LogDir,
		tail,
		"FPKM 日志目录不存在，程序可能尚未写入过日志。",
		"FPKM 日志目录为空，暂无可查看的日志。",
	))
}

// parseAppLogTail 解析 tail 参数：非法或未提供时用默认值，并夹到上限。
func parseAppLogTail(v string) int64 {
	if v == "" {
		return appLogDefaultTail
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return appLogDefaultTail
	}
	if n > appLogMaxTail {
		return appLogMaxTail
	}
	return n
}

// buildAppLogsView 读取应用日志目录（<应用目录>/logs），返回全部日志文件及其尾部内容。
//
// 目录不存在时返回 Missing=true（前端据此提示"暂无日志"），不视为错误：
// 应用从未启动过、或日志被清理掉，都是正常情况。
func buildAppLogsView(name, appDir string, tail int64) AppLogsView {
	logsDir := filepath.Join(appDir, "logs")
	return buildLogsView(
		name,
		logsDir,
		tail,
		"应用日志目录不存在，应用可能尚未启动过。",
		"日志目录为空，应用可能尚未输出过日志。",
	)
}

// buildLogsView 是日志目录读取的通用实现：给定日志目录，返回其下全部日志
// 文件及各自的尾部内容。应用日志（<应用目录>/logs）与 FPKM 自身日志
// （<数据目录>/logs）共用此实现，差别只在与目录与提示文案。
//
// hintMissing/hintEmpty 分别用于"目录不存在"和"目录存在但没有日志文件"两种
// 正常情况的提示；两者都会置 Missing=true，不视为错误。
func buildLogsView(name, logsDir string, tail int64, hintMissing, hintEmpty string) AppLogsView {
	// 宿主时区偏移：供前端把日志行首时间换算成访问者本地时间
	_, offsetSec := time.Now().Zone()
	view := AppLogsView{Name: name, Dir: logsDir, TailBytes: tail, TZOffsetSeconds: offsetSec}

	entries, err := os.ReadDir(logsDir)
	if err != nil {
		view.Missing = true
		view.Hint = hintMissing
		return view
	}

	type fileInfo struct {
		path    string
		name    string
		size    int64
		modTime time.Time
	}
	var files []fileInfo
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		// 跳过隐藏文件（如 .keep），日志文件通常不需要隐藏项
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, fileInfo{
			path:    filepath.Join(logsDir, e.Name()),
			name:    e.Name(),
			size:    info.Size(),
			modTime: info.ModTime(),
		})
	}

	// 最近修改的排前面：用户最关心的通常是刚产生的那一个
	sort.Slice(files, func(i, j int) bool {
		if files[i].modTime.Equal(files[j].modTime) {
			return files[i].name < files[j].name
		}
		return files[i].modTime.After(files[j].modTime)
	})
	if len(files) > appLogMaxFiles {
		files = files[:appLogMaxFiles]
	}

	view.Files = make([]AppLogFileView, 0, len(files))
	for _, f := range files {
		content, truncated, err := readFileTail(f.path, tail)
		if err != nil {
			continue
		}
		item := AppLogFileView{
			Name:      f.name,
			Path:      f.path,
			Size:      f.size,
			ModTime:   f.modTime.Format(time.RFC3339),
			Content:   content,
			Truncated: truncated,
		}
		view.Files = append(view.Files, item)
	}

	if len(view.Files) == 0 {
		view.Missing = true
		view.Hint = hintEmpty
	}
	return view
}

// readFileTail 读取文件尾部最多 limit 字节；截断时丢弃首个不完整行，
// 保证返回内容从一条完整日志行开始。
func readFileTail(path string, limit int64) (content string, truncated bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", false, err
	}

	size := info.Size()
	start := int64(0)
	if size > limit {
		start = size - limit
		truncated = true
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return "", false, err
	}
	data, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return "", false, err
	}

	text := string(data)
	if truncated {
		// 丢弃被截断的首行残片
		if idx := strings.IndexByte(text, '\n'); idx >= 0 {
			text = text[idx+1:]
		}
	}
	return text, truncated, nil
}