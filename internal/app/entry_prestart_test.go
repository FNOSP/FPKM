package app

import (
	"strings"
	"testing"
)

// plainScriptWithGuards 是一个"非分发脚本"（没有 case+函数组合，
// 或 case 缺失），会走 parseCmdMain 分支。
//
// 关键点：脚本里出现了 `[ ! -x "$APP_BIN" ]` 这类 **test 表达式**，
// 其中包含了二进制路径/变量。旧的 isBinaryCall 只要"行里含有二进制路径"
// 就认为是调用，于是把整行 test 表达式当成预启动命令，
// 产生形如：
//
//	执行预启动命令: /path/to/bin ! -x /path/to/bin ] ...
//
// 这种畸形命令（"卡住"的观感就来自这里：命令内容被拼接错乱）。
const plainScriptWithGuards = `#!/bin/bash
set -u

APP_BIN="${TRIM_APPDEST}/server/scanner"
PID_FILE="${TRIM_PKGVAR}/scanner.pid"

main() {
  if [ ! -x "$APP_BIN" ]; then
    echo "missing binary" >&2
    return 1
  fi
  if [ ! -f "$PID_FILE" ]; then
    nohup "$APP_BIN" >> "${TRIM_PKGVAR}/scanner.log" 2>&1 &
    echo $! > "$PID_FILE"
  fi
}

main
`

// TestPreStartIgnoresTestExpressions 确保 test 表达式不会被当成预启动命令。
//
// 复现用户报告的现象：日志里出现
//
//	执行预启动命令: /…/fnos-port-scanner ! -x /…/fnos-port-scanner ] …
//
// 说明 `[ ! -x "$BIN" ]` 被错误解析成了"执行 $BIN，参数为 ! -x … ]"。
func TestPreStartIgnoresTestExpressions(t *testing.T) {
	targetDir, _ := writeTempApp(t, plainScriptWithGuards, map[string]string{
		"server/scanner": "#!/bin/sh\n",
	})

	entry, err := DetectEntry(targetDir)
	if err != nil {
		t.Fatalf("探测入口失败: %v", err)
	}

	t.Logf("Type=%s Command=%q Args=%v", entry.Type, entry.Command, entry.Args)
	for i, pc := range entry.PreStartCmds {
		t.Logf("PreStart[%d]: Command=%q Args=%v", i, pc.Command, pc.Args)
	}

	for _, pc := range entry.PreStartCmds {
		// 命令行不应以 test 的方括号开头
		if strings.TrimSpace(pc.Command) == "[" ||
			strings.TrimSpace(pc.Command) == "test" {
			t.Errorf("test 表达式被当成预启动命令: %q %v", pc.Command, pc.Args)
		}
		// 参数里不应混入 test 运算符
		for _, a := range pc.Args {
			switch a {
			case "!", "-x", "-f", "-d", "-S", "]", "[":
				t.Errorf("预启动命令参数中混入了 test 运算符 %q：%q %v",
					a, pc.Command, pc.Args)
			}
		}
	}

	// 真正要执行的主命令仍应是二进制本身
	if !strings.Contains(entry.Command, "scanner") {
		t.Errorf("主命令不正确: %q", entry.Command)
	}
}

// TestIsBinaryCallRejectsTestExpressions 单元层面验证 isBinaryCall
func TestIsBinaryCallRejectsTestExpressions(t *testing.T) {
	bin := "/app/target/server/scanner"
	vars := []string{"$APP_BIN", "${APP_BIN}"}

	shouldReject := []string{
		`if [ ! -x "$APP_BIN" ]; then`,
		`[ -f "$APP_BIN" ]`,
		`[ ! -S "${APP_BIN}" ] && echo x`,
		`if [ "$APP_BIN" = "" ]; then`,
		`while [ ! -x "$APP_BIN" ]; do`,
		`if test -x "$APP_BIN"; then`,
	}
	for _, line := range shouldReject {
		if isBinaryCall(line, vars, bin) {
			t.Errorf("test 表达式被误判为二进制调用: %q", line)
		}
	}

	shouldAccept := []string{
		`"$APP_BIN" --version`,
		`"$APP_BIN" serve &`,
		`nohup "$APP_BIN" >> log 2>&1 &`,
		bin + ` --help`,
	}
	for _, line := range shouldAccept {
		if !isBinaryCall(line, vars, bin) {
			t.Errorf("真实的二进制调用被漏判: %q", line)
		}
	}
}

// TestEchoLineNotTreatedAsBinaryCall 覆盖另一类误判：提示文案里出现二进制路径。
//
// 用户机器上真实卡住的进程 argv 为：
//
//	[0] .../fnos-port-scanner
//	[1] 缺少应用可执行文件或不可执行: .../fnos-port-scanner
//
// 说明 `echo "缺少...: $APP_BIN" > "$LOG"` 被当成了"调用二进制"，
// 把提示文案当参数去启动程序 —— 与 test 表达式属同一类误判。
func TestEchoLineNotTreatedAsBinaryCall(t *testing.T) {
	bin := "/app/target/server/fnos-port-scanner"
	vars := []string{"$APP_BIN", "${APP_BIN}"}

	bad := []string{
		`echo "缺少应用可执行文件或不可执行: $APP_BIN" > "$TRIM_TEMP_LOGFILE"`,
		`echo "应用服务启动失败" > "$TRIM_TEMP_LOGFILE"`,
		`echo "socket: ${APP_BIN}"`,
		`log_msg "starting $APP_BIN"`,
		`printf '%s\n' "$APP_BIN"`,
	}
	for _, line := range bad {
		if isBinaryCall(line, vars, bin) {
			t.Errorf("提示文案被误判为调用: %q", line)
		}
	}

	good := []string{
		`"$APP_BIN" serve &`,
		`nohup "$APP_BIN" >> "$LOG_FILE" 2>&1 &`,
		`exec "$APP_BIN" --port 8080`,
		`if "$APP_BIN" --check; then`,
	}
	for _, line := range good {
		if !isBinaryCall(line, vars, bin) {
			t.Errorf("真实调用被漏判: %q", line)
		}
	}
}
