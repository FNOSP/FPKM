package app

import (
	"os"
	"path/filepath"
	"testing"
)

// router9CmdMain 是 9router 0.5.55 真实 cmd/main 的结构（节选关键部分）。
//
// 与常见 fnOS 应用的区别：它的 case 语句用了"带默认值"的参数展开
//
//	case "${1:-start}" in
//
// 而不是 `case "$1" in`。fpkm 早期只识别后者，于是把它当成普通脚本解析，
// 最终报 "探测入口失败: 无法从cmd/main中提取二进制路径"。
const router9CmdMain = `#!/bin/bash
set -euo pipefail

APP_NAME="${TRIM_APPNAME:-9router}"
APP_DIR="${TRIM_APPDEST:-/var/apps/${APP_NAME}}"

if [ -d "${APP_DIR}/server" ]; then
    SRC_DIR="${APP_DIR}/server"
elif [ -d "${APP_DIR}/target/server" ]; then
    SRC_DIR="${APP_DIR}/target/server"
else
    echo "ERROR: server dir not found" >&2
    exit 1
fi

PORT="${TRIM_SERVICE_PORT:-20128}"

start() {
    cd "${SRC_DIR}"
    nohup "node" --max-old-space-size=4096 custom-server.js >> "${LOG}" 2>&1 &
    echo $! > "${PID_FILE}"
}

stop() {
    pkill -f "${SRC_DIR}/custom-server.js" 2>/dev/null || true
}

status() {
    return 1
}

case "${1:-start}" in
    start) start ;;
    stop) stop ;;
    status) status ;;
    restart) stop; sleep 1; start ;;
    *) echo "Usage: $0 {start|stop|status|restart}"; exit 1 ;;
esac
`

// TestIsFullBashScriptWithDefaultParam 覆盖 case 语句的各种真实写法。
//
// 9router 用 `case "${1:-start}" in`（带默认值），必须被识别为完整 bash 脚本，
// 否则 fpkm 会走"解析脚本提取二进制"的分支并失败。
func TestIsFullBashScriptWithDefaultParam(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "带默认值的参数展开（9router）",
			body: "start() {\n  echo hi\n}\ncase \"${1:-start}\" in\n  start) start ;;\nesac\n",
			want: true,
		},
		{
			name: "带默认值且参数名不同",
			body: "start() {\n  echo hi\n}\ncase \"${ACTION:-start}\" in\n  start) start ;;\nesac\n",
			want: true,
		},
		{
			name: "经典 case \"$1\"",
			body: "start() {\n  echo hi\n}\ncase \"$1\" in\n  start) start ;;\nesac\n",
			want: true,
		},
		{
			name: "无引号 case $1",
			body: "start() {\n  echo hi\n}\ncase $1 in\n  start) start ;;\nesac\n",
			want: true,
		},
		{
			name: "case 后换行的无引号形式",
			body: "start() {\n  echo hi\n}\ncase $1\nin\n  start) start ;;\nesac\n",
			want: true,
		},
		{
			name: "只有 case 没有函数/source —— 不算完整脚本",
			body: "case \"$1\" in\n  start) echo x ;;\nesac\n",
			want: false,
		},
		{
			name: "只有函数没有 case —— 不算完整脚本",
			body: "start() {\n  echo hi\n}\n./server\n",
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTempScript(t, tc.body)
			if got := isFullBashScript(path); got != tc.want {
				t.Errorf("isFullBashScript = %v, 期望 %v\n脚本:\n%s", got, tc.want, tc.body)
			}
		})
	}
}

// TestRouter9EntryDetection 复现并验证修复：
// 9router 的 cmd/main 应被识别为完整 bash 脚本，走
// "bash cmd/main start" 路径，而不是尝试提取二进制路径。
func TestRouter9EntryDetection(t *testing.T) {
	targetDir, appDir := writeTempApp(t, router9CmdMain,
		map[string]string{
			// 应用实际入口是 node 脚本，不是可执行二进制
			"server/custom-server.js": "// next custom server\n",
			"server/package.json":     `{"name":"9router"}`,
		})

	entry, err := DetectEntry(targetDir)
	if err != nil {
		t.Fatalf("探测入口失败（正是 9router 报的错）: %v", err)
	}

	// 应为完整 bash 脚本或 Node 应用，而不是从 cmd/main 硬提二进制路径
	switch entry.Type {
	case EntryBashScript:
		// 解释器由 shellPath() 按可用性解析：fnOS 上是 bash，Android 上回退 sh，
		// Windows 构建机上会解析到 bash 的绝对路径；这里只校验解释器种类，
		// 不要求恰好等于字面量 "bash"。
		name := filepath.Base(entry.Command)
		if name != "bash" && name != "bash.exe" && name != "sh" && name != "sh.exe" {
			t.Errorf("BashScript 类型的命令应为 bash/sh 解释器，实际 %q", entry.Command)
		}
	case EntryNodeJS:
		// 也算合理：能识别出 node 入口
	default:
		t.Logf("入口类型 = %s, command=%s, args=%v", entry.Type, entry.Command, entry.Args)
	}

	if entry.Command == "" {
		t.Error("启动命令为空")
	}
	_ = appDir
}

// ---------- 测试辅助 ----------

// writeTempScript 把脚本写入临时文件并返回路径
func writeTempScript(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "main")
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// writeTempApp 构造一个最小应用目录：appDir/cmd/main + targetDir/...
// 返回 (targetDir, appDir)
func writeTempApp(t *testing.T, cmdMain string, files map[string]string) (string, string) {
	t.Helper()
	root := t.TempDir()
	appDir := filepath.Join(root, "apps", "9router")
	targetDir := filepath.Join(appDir, "target")

	if err := os.MkdirAll(filepath.Join(appDir, "cmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "cmd", "main"), []byte(cmdMain), 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, content := range files {
		full := filepath.Join(targetDir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return targetDir, appDir
}

// TestNoFalsePositiveOnPlainScripts 确认普通脚本不会被误判为分发脚本。
//
// 这很关键：误判会让本该"提取二进制直接启动"的应用改成
// "bash cmd/main start"，有些脚本自身不做后台守护，会导致启动语义变化。
func TestNoFalsePositiveOnPlainScripts(t *testing.T) {
	plain := []struct{ name, body string }{
		{"直接启动二进制", "#!/bin/bash\ncd $TRIM_APPDEST\nexec ./server\n"},
		{"简单启动", "#!/bin/bash\nnohup ./bin/app --port 8080 &\n"},
		{"python 启动", "#!/bin/bash\npython3 main.py\n"},
		{"带 case 但无函数", "#!/bin/bash\ncase \"$1\" in\n start) ./app ;;\nesac\n"},
		{"case 仅在注释中", "#!/bin/bash\n# 这个 case 只是注释\necho \"in case of error\"\n./app\n"},
		{"case 仅在字符串中", "#!/bin/bash\nMSG=\"case closed\"\necho $MSG\n./app\n"},
	}
	for _, tc := range plain {
		t.Run(tc.name, func(t *testing.T) {
			if looksLikeDispatcherScript(tc.body) {
				t.Errorf("被误判为分发脚本:\n%s", tc.body)
			}
		})
	}
}
