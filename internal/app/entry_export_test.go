package app

import (
	"os"
	"path/filepath"
	"testing"
)

// makeScriptApp 构造一个最小可识别的 .fpk 应用目录：
// appDir/cmd/main + appDir/target/bin/app
func makeScriptApp(t *testing.T, script string) (appDir, targetDir string) {
	t.Helper()
	appDir = t.TempDir()
	targetDir = filepath.Join(appDir, "target")
	if err := os.MkdirAll(filepath.Join(appDir, "cmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(targetDir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(targetDir, "bin", "app")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "cmd", "main"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return appDir, targetDir
}

// 分发脚本模板：含 case 分发 + 函数定义，才能被识别为完整 bash 脚本
func dispatcherScript(extra string) string {
	return `#!/bin/bash
PID_FILE="${TRIM_PKGVAR}/app.pid"
` + extra + `
CMD="${TRIM_APPDEST}/bin/app --serve"

start_process() {
    bash -c "${CMD}" 2>&1 &
    printf "%s" "$!" > ${PID_FILE}
}

stop_process() {
    kill -TERM $(cat ${PID_FILE})
}

case $1 in
start)
    start_process
    ;;
stop)
    stop_process
    ;;
*)
    exit 1
    ;;
esac
`
}

// scriptExportsEnvVars 应识别 export 语句
func TestScriptExportsEnvVars(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{"无 export", "#!/bin/bash\nCMD=x\n", false},
		{"普通 export", "#!/bin/bash\nexport FOO=$port\n", true},
		{"缩进 export", "#!/bin/bash\n  export BAR=1\n", true},
		{"注释里的 export 不算", "#!/bin/bash\n# export FOO=1\nCMD=x\n", false},
		{"导出已赋值变量", "#!/bin/bash\nHOME=/tmp\nexport HOME\n", true},
	}
	for _, c := range cases {
		p := filepath.Join(dir, "m")
		if err := os.WriteFile(p, []byte(c.content), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := scriptExportsEnvVars(p); got != c.want {
			t.Errorf("%s: 期望 %v，得到 %v", c.name, c.want, got)
		}
	}
}

// 关键回归：cmd/main 有 export 时必须执行脚本，不能提取 CMD 直接跑。
//
// 真实故障（wb2api 1.13.0）：提取 CMD 后脚本不运行，
// export WB2A_LISTEN / WB2A_API_KEY 全部丢失，
// 应用回退到 config.json 里写死的端口与示例密钥。
func TestDetectEntryPrefersScriptWhenExport(t *testing.T) {
	_, targetDir := makeScriptApp(t, dispatcherScript("export WB2A_LISTEN=$port\nexport WB2A_API_KEY=$pwd"))

	entry, err := DetectEntryWithEnv(targetDir, EnvMap{
		"TRIM_APPDEST": targetDir,
		"TRIM_PKGVAR":  filepath.Join(filepath.Dir(targetDir), "var"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if entry.Type != EntryBashScript {
		t.Fatalf("含 export 时应执行脚本(EntryBashScript)，实际 Type=%v Command=%s", entry.Type, entry.Command)
	}
	if entry.Command != "bash" {
		t.Errorf("应使用 bash 执行脚本，得到 %s", entry.Command)
	}
}

// 没有 export 时仍走提取捷径，保持既有优化不退化
func TestDetectEntryExtractsWhenNoExport(t *testing.T) {
	_, targetDir := makeScriptApp(t, dispatcherScript(""))

	entry, err := DetectEntryWithEnv(targetDir, EnvMap{
		"TRIM_APPDEST": targetDir,
		"TRIM_PKGVAR":  filepath.Join(filepath.Dir(targetDir), "var"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if entry.Type != EntryBinary {
		t.Fatalf("无 export 时应提取 CMD 直接执行(EntryBinary)，实际 Type=%v Command=%s", entry.Type, entry.Command)
	}
}

// TailLogHint 应返回日志尾部且不因大文件失控
func TestTailLogHint(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "app.log")
	content := ""
	for i := 0; i < 5000; i++ {
		content += "line-" + itoaTest(i) + "\n"
	}
	content += "http: listen tcp :7863: bind: address already in use\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got := TailLogHint(p, 3)
	if got == "" {
		t.Fatal("应返回日志尾部")
	}
	if !contains(got, "bind: address already in use") {
		t.Errorf("尾部应包含关键错误，实际: %s", got)
	}
	if TailLogHint("", 5) != "" {
		t.Error("空路径应返回空串")
	}
	if TailLogHint(filepath.Join(dir, "nope.log"), 5) != "" {
		t.Error("文件不存在应返回空串")
	}
}

func itoaTest(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
