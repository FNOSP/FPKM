package logger

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// stampRe 匹配行首的日志发生时间：YYYY-MM-DD HH:MM:SS
var stampRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} `)

// TestStampWriterPrefixesEachLine 校验：无论内容被拆成多少次写入，
// 每一行都且只有行首带一个时间戳。
func TestStampWriterPrefixesEachLine(t *testing.T) {
	var buf bytes.Buffer
	w := NewStampWriter(&buf)

	// 模拟子进程无缓冲输出：一行被拆成多次 Write
	w.Write([]byte("first line\n"))
	w.Write([]byte("second "))
	w.Write([]byte("line\n"))
	w.Write([]byte("third line")) // 末行无换行

	got := buf.String()
	want := "first line\nsecond line\nthird line"
	lines := splitLines(got)
	if len(lines) != 3 {
		t.Fatalf("期望 3 行，实际 %d 行: %q", len(lines), got)
	}
	for i, line := range lines {
		if !stampRe.MatchString(line) {
			t.Errorf("第 %d 行缺少时间戳前缀: %q", i+1, line)
		}
		body := stampRe.ReplaceAllString(line, "")
		wantLine := splitLines(want)[i]
		if body != wantLine {
			t.Errorf("第 %d 行内容不符: 得到 %q，期望 %q", i+1, body, wantLine)
		}
	}
}

// TestStampWriterKeepsContinuationSingleTimestamp 校验跨写入的续写片段
// 不会被再次插入时间戳（避免一行出现多个时间）。
func TestStampWriterKeepsContinuationSingleTimestamp(t *testing.T) {
	var buf bytes.Buffer
	w := NewStampWriter(&buf)
	w.Write([]byte("a"))
	w.Write([]byte("b"))
	w.Write([]byte("c\n"))

	line := buf.String()
	if n := len(stampRe.FindAllString(line, -1)); n != 1 {
		t.Errorf("期望恰好 1 个时间戳，实际 %d 个: %q", n, line)
	}
	if body := strings.TrimRight(stampRe.ReplaceAllString(line, ""), "\n"); body != "abc" {
		t.Errorf("内容不符: %q", body)
	}
}

// TestNowStampFormat 校验时间戳格式固定为 YYYY-MM-DD HH:MM:SS。
func TestNowStampFormat(t *testing.T) {
	got := NowStamp()
	if len(got) != len("2006-01-02 15:04:05") || !stampRe.MatchString(got+" ") {
		t.Errorf("时间戳格式不符: %q", got)
	}
}

// withFakeClock 把 nowFn 替换为可推进的假时钟，并在测试结束时恢复。
// 返回一个推进到指定时刻的函数。
func withFakeClock(t *testing.T, start time.Time) func(time.Time) {
	t.Helper()
	old := nowFn
	cur := start
	nowFn = func() time.Time { return cur }
	t.Cleanup(func() { nowFn = old })
	return func(next time.Time) { cur = next }
}

// readFileOrFail 读取文件内容，失败时直接终止测试。
func readFileOrFail(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", path, err)
	}
	return string(b)
}

// TestLoggerSplitsByLocalDate 校验核心诉求：日志按当天日期创建文件，
// 跨天后写入新日期文件，且绝不回写旧日期文件。
func TestLoggerSplitsByLocalDate(t *testing.T) {
	dir := t.TempDir()
	advance := withFakeClock(t, time.Date(2026, 10, 21, 23, 58, 0, 0, time.Local))
	t.Cleanup(Close)

	if err := Init(dir, LevelDebug); err != nil {
		t.Fatalf("Init 失败: %v", err)
	}
	l := instance

	day1 := filepath.Join(dir, "fpkm-2026-10-21.log")
	day2 := filepath.Join(dir, "fpkm-2026-10-22.log")

	if got := l.filePath(); got != day1 {
		t.Fatalf("初始化后当前文件应为 %s，实际 %s", day1, got)
	}

	Info("before midnight")
	if c := readFileOrFail(t, day1); !strings.Contains(c, "before midnight") {
		t.Fatalf("21 号文件应包含跨天前的日志，实际 %q", c)
	}
	if _, err := os.Stat(day2); !os.IsNotExist(err) {
		t.Fatalf("跨天前不应出现 22 号文件，stat err=%v", err)
	}

	// 跨过零点：22 号的第一行日志不得写入 21 号文件
	advance(time.Date(2026, 10, 22, 0, 30, 0, 0, time.Local))
	Info("after midnight")

	if got := l.filePath(); got != day2 {
		t.Fatalf("跨天后当前文件应为 %s，实际 %s", day2, got)
	}
	c2 := readFileOrFail(t, day2)
	if !strings.Contains(c2, "after midnight") {
		t.Fatalf("22 号文件应包含跨天后的日志，实际 %q", c2)
	}
	if strings.Contains(c2, "before midnight") {
		t.Fatalf("22 号文件不应包含跨天前的日志，实际 %q", c2)
	}
	c1 := readFileOrFail(t, day1)
	if strings.Contains(c1, "after midnight") {
		t.Fatalf("22 号日志被回写进了 21 号文件: %q", c1)
	}
	if !strings.Contains(c1, "before midnight") {
		t.Fatalf("21 号文件原有内容被破坏: %q", c1)
	}
}

// TestLoggerSameDayAppends 校验同一天内多次写入始终追加到同一个文件，
// 且不会因为重复写入而重建（截断）文件。
func TestLoggerSameDayAppends(t *testing.T) {
	dir := t.TempDir()
	withFakeClock(t, time.Date(2026, 10, 21, 9, 0, 0, 0, time.Local))
	t.Cleanup(Close)

	if err := Init(dir, LevelDebug); err != nil {
		t.Fatalf("Init 失败: %v", err)
	}
	Info("first")
	Info("second")
	Info("third")

	content := readFileOrFail(t, filepath.Join(dir, "fpkm-2026-10-21.log"))
	for _, want := range []string{"first", "second", "third"} {
		if !strings.Contains(content, want) {
			t.Errorf("同一天日志应累计写入，缺少 %q（实际 %q）", want, content)
		}
	}
	if len(splitLines(strings.TrimRight(content, "\n"))) != 3 {
		t.Errorf("期望 3 行，实际 %q", content)
	}
	if _, err := os.Stat(filepath.Join(dir, "fpkm-2026-10-22.log")); !os.IsNotExist(err) {
		t.Errorf("同一天不应生成其他日期文件，stat err=%v", err)
	}
}

// TestLoggerRotatesWithoutReinit 校验跨天切换完全由写入路径触发，
// 无需重新调用 Init；连续多次跨天也能逐日落到各自文件。
func TestLoggerRotatesWithoutReinit(t *testing.T) {
	dir := t.TempDir()
	advance := withFakeClock(t, time.Date(2026, 10, 21, 23, 0, 0, 0, time.Local))
	t.Cleanup(Close)

	if err := Init(dir, LevelDebug); err != nil {
		t.Fatalf("Init 失败: %v", err)
	}

	advance(time.Date(2026, 10, 22, 1, 0, 0, 0, time.Local))
	Info("day22")
	advance(time.Date(2026, 10, 23, 1, 0, 0, 0, time.Local))
	Info("day23")

	if c := readFileOrFail(t, filepath.Join(dir, "fpkm-2026-10-22.log")); strings.Contains(c, "day23") || !strings.Contains(c, "day22") {
		t.Errorf("22 号文件内容不符: %q", c)
	}
	if c := readFileOrFail(t, filepath.Join(dir, "fpkm-2026-10-23.log")); !strings.Contains(c, "day23") || strings.Contains(c, "day22") {
		t.Errorf("23 号文件内容不符: %q", c)
	}
}

// TestDateWriterRotatesOnWrite 直接校验 dateWriter：日期未变时复用句柄，
// 日期变化时切到新文件，旧文件不再被追加。
func TestDateWriterRotatesOnWrite(t *testing.T) {
	dir := t.TempDir()
	advance := withFakeClock(t, time.Date(2026, 3, 1, 10, 0, 0, 0, time.Local))

	w := newDateWriter(dir, 0644)
	defer w.Close()

	if _, err := w.Write([]byte("a\n")); err != nil {
		t.Fatalf("首次写入失败: %v", err)
	}
	if _, err := w.Write([]byte("b\n")); err != nil {
		t.Fatalf("同日第二次写入失败: %v", err)
	}
	day1 := filepath.Join(dir, "fpkm-2026-03-01.log")
	if c := readFileOrFail(t, day1); c != "a\nb\n" {
		t.Fatalf("3 月 1 日文件内容不符: %q", c)
	}

	advance(time.Date(2026, 3, 2, 0, 0, 1, 0, time.Local))
	if _, err := w.Write([]byte("c\n")); err != nil {
		t.Fatalf("跨天写入失败: %v", err)
	}
	if got := w.currentPath(); got != filepath.Join(dir, "fpkm-2026-03-02.log") {
		t.Fatalf("跨天后当前文件不符: %s", got)
	}
	if c := readFileOrFail(t, day1); c != "a\nb\n" {
		t.Fatalf("旧日期文件不应被追加: %q", c)
	}
	if c := readFileOrFail(t, filepath.Join(dir, "fpkm-2026-03-02.log")); c != "c\n" {
		t.Fatalf("3 月 2 日文件内容不符: %q", c)
	}
}

// TestAdaptWriterFlushesPreInitLinesIntoLogFile 校验核心诉求：
// logger 初始化**之前**写入的诊断日志（netadapt 在包 init 阶段产生的
// `[fpkm-net] ...` 行）不会丢，而是先被缓存，Init 后补写进当天日志文件，
// 且每行只带一个时间戳、顺序与发生顺序一致。
func TestAdaptWriterFlushesPreInitLinesIntoLogFile(t *testing.T) {
	dir := t.TempDir()
	withFakeClock(t, time.Date(2026, 10, 22, 10, 36, 26, 0, time.Local))
	t.Cleanup(Close)

	w := AdaptWriter()

	// Init 之前：模拟 netadapt 在包 init 阶段的诊断日志
	if _, err := w.Write([]byte("[fpkm-net] 公共 DNS 注入已生效: 223.5.5.5\n")); err != nil {
		t.Fatalf("Init 前的写入不应报错: %v", err)
	}
	if _, err := w.Write([]byte("[fpkm-net] DNS 候选已注入\n")); err != nil {
		t.Fatalf("Init 前的写入不应报错: %v", err)
	}

	dayFile := filepath.Join(dir, "fpkm-2026-10-22.log")
	if _, err := os.Stat(dayFile); !os.IsNotExist(err) {
		t.Fatalf("Init 之前不应存在日志文件，stat err=%v", err)
	}

	if err := Init(dir, LevelDebug); err != nil {
		t.Fatalf("Init 失败: %v", err)
	}

	// Init 之后的第一行：应先把缓存的两行补写进文件，再写本行
	if _, err := w.Write([]byte("[fpkm-net] 公共 DNS 预探测通过\n")); err != nil {
		t.Fatalf("Init 后的写入不应报错: %v", err)
	}

	content := readFileOrFail(t, dayFile)
	lines := splitLines(strings.TrimRight(content, "\n"))
	want := []string{
		"[fpkm-net] 公共 DNS 注入已生效: 223.5.5.5",
		"[fpkm-net] DNS 候选已注入",
		"[fpkm-net] 公共 DNS 预探测通过",
	}
	if len(lines) != len(want) {
		t.Fatalf("期望 %d 行，实际 %d 行: %q", len(want), len(lines), content)
	}
	for i, wl := range want {
		if n := len(stampRe.FindAllString(lines[i], -1)); n != 1 {
			t.Errorf("第 %d 行应恰好 1 个时间戳，实际 %d 个: %q", i+1, n, lines[i])
		}
		if body := strings.TrimRight(stampRe.ReplaceAllString(lines[i], ""), "\n"); body != wl {
			t.Errorf("第 %d 行内容不符: 得到 %q，期望 %q", i+1, body, wl)
		}
	}
}

// TestAdaptWriterBufferIsBounded 校验 Init 之前的缓存有上限：
// 超出上限时丢弃最早的行，不因长期未初始化而无限增长。
func TestAdaptWriterBufferIsBounded(t *testing.T) {
	dir := t.TempDir()
	withFakeClock(t, time.Date(2026, 10, 22, 11, 0, 0, 0, time.Local))
	t.Cleanup(Close)

	w := AdaptWriter()
	// Init 之前写过量的行（模拟极端情况下启动很晚）
	for i := 0; i < adaptBufferMax+5; i++ {
		if _, err := w.Write([]byte(fmt.Sprintf("[fpkm-net] n-%03d\n", i))); err != nil {
			t.Fatalf("写入失败: %v", err)
		}
	}

	if err := Init(dir, LevelDebug); err != nil {
		t.Fatalf("Init 失败: %v", err)
	}
	if _, err := w.Write([]byte("[fpkm-net] tail\n")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	content := readFileOrFail(t, filepath.Join(dir, "fpkm-2026-10-22.log"))
	if strings.Contains(content, "n-000") {
		t.Errorf("超出上限的最早行应被丢弃，实际仍存在: %q", content)
	}
	if !strings.Contains(content, fmt.Sprintf("n-%03d", adaptBufferMax+4)) {
		t.Errorf("最新的行不应被丢弃: %q", content)
	}
	if !strings.Contains(content, "[fpkm-net] tail") {
		t.Errorf("Init 之后写入的行应落盘: %q", content)
	}
	if got := len(splitLines(strings.TrimRight(content, "\n"))); got != adaptBufferMax+1 {
		t.Errorf("期望 %d 行（缓存上限 + 1），实际 %d 行", adaptBufferMax+1, got)
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
