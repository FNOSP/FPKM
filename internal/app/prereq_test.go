package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cloudLotteryCmdMain 是云抽奖 cloud-lottery 1.0.0 的 cmd/main 结构（节选关键部分）。
//
// 特征：有 case 分发，但既没有函数定义也没有 source；启动命令是
// `nohup python3 "$APP_ENTRY" &`，脚本里没有任何可提取的二进制路径。
// 这正是「探测入口失败: 无法从cmd/main中提取二进制路径」的真实来源。
const cloudLotteryCmdMain = `#!/bin/bash

ERROR_LOG="${TRIM_TEMP_LOGFILE:-/tmp/cloud-lottery-error.log}"
exec > /tmp/cloud-lottery-debug.log 2>&1

if [ -z "$TRIM_APPDEST" ]; then
    echo "ERROR: TRIM_APPDEST not set" > "$ERROR_LOG"
    exit 1
fi

export PORT="${TRIM_SERVICE_PORT:-5670}"

if ! command -v python3 &> /dev/null; then
    echo "ERROR: python3 not found" > "$ERROR_LOG"
    exit 1
fi

case "$1" in
    start)
        nohup python3 "$APP_ENTRY" >> /tmp/cloud-lottery-debug.log 2>&1 &
        PID=$!
        echo $PID > "$TRIM_PKGVAR/app.pid"
        if netstat -tlnp 2>/dev/null | grep -q ":${PORT}.*LISTEN"; then
            exit 0
        fi
        exit 1
        ;;
    stop)
        kill -TERM "$(cat "$TRIM_PKGVAR/app.pid")"
        ;;
esac
`

// TestCloudLotteryEntryFallsBackToScript 复现并验证修复：
// 该类 cmd/main 不应再报「无法从cmd/main中提取二进制路径」，而应回退为执行脚本。
func TestCloudLotteryEntryFallsBackToScript(t *testing.T) {
	targetDir, _ := writeTempApp(t, cloudLotteryCmdMain, map[string]string{
		"server/app.pyc": "PYC",
	})

	entry, err := DetectEntryWithEnv(targetDir, nil)
	if err != nil {
		t.Fatalf("仍报探测入口失败（正是 user 遇到的错）: %v", err)
	}
	if entry.Type != EntryBashScript {
		t.Fatalf("期望回退为脚本执行(EntryBashScript)，实际 %v", entry.Type)
	}
	if len(entry.Args) != 2 || entry.Args[1] != "start" {
		t.Fatalf("脚本参数应为 <cmd/main> start，实际 %v", entry.Args)
	}
	if !strings.HasSuffix(filepath.ToSlash(entry.Args[0]), "/cmd/main") {
		t.Fatalf("脚本路径异常: %v", entry.Args[0])
	}
}

// TestRuntimePrereqDetectsPython 验证能从真实脚本里识别出 python3 / netstat 依赖。
func TestRuntimePrereqDetectsPython(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "main")
	if err := os.WriteFile(p, []byte(cloudLotteryCmdMain), 0o755); err != nil {
		t.Fatal(err)
	}

	required, missing := detectRuntimePrereqs(p, func(cmd string) (string, error) {
		// 模拟精简环境：只有 netstat 可用，python3 缺失
		if cmd == "netstat" {
			return "/system/bin/netstat", nil
		}
		return "", os.ErrNotExist
	})

	joined := strings.Join(required, ",")
	if !strings.Contains(joined, "python3") {
		t.Fatalf("未识别出 python3 依赖: %v", required)
	}
	if !strings.Contains(joined, "netstat") {
		t.Fatalf("未识别出 netstat 依赖: %v", required)
	}
	if len(missing) != 1 || missing[0] != "python3" {
		t.Fatalf("缺少运行时应仅为 python3，实际 %v", missing)
	}
}

// TestRuntimePrereqIgnoresNonCalls 确认注释、字符串与 shebang 里的命令名不算依赖。
func TestRuntimePrereqIgnoresNonCalls(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"注释中的命令", "#!/bin/bash\n# 这里说明需要 python3 与 node\n./app\n"},
		{"字符串中的命令", "#!/bin/bash\nMSG=\"python3 is required\"\necho $MSG\n./app\n"},
		{"纯赋值", "#!/bin/bash\nPY=python3\n./app\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "main")
			if err := os.WriteFile(p, []byte(tc.body), 0o755); err != nil {
				t.Fatal(err)
			}
			required, _ := detectRuntimePrereqs(p, func(string) (string, error) {
				return "", os.ErrNotExist
			})
			if len(required) != 0 {
				t.Fatalf("不应识别出依赖: %v", required)
			}
		})
	}
}

// TestRuntimePrereqErrorMentionsFiles 验证报错信息既给出缺失命令，也给出要补的文件。
func TestRuntimePrereqErrorMentionsFiles(t *testing.T) {
	err := RuntimePrereqError([]string{"python3"})
	if err == nil {
		t.Fatal("有缺失项时应返回错误")
	}
	msg := err.Error()
	for _, want := range []string{"python3", "需要额外提供", "运行时"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("报错信息缺少 %q:\n%s", want, msg)
		}
	}
	if RuntimePrereqError(nil) != nil {
		t.Fatal("无缺失项时不应返回错误")
	}
}