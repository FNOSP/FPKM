package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fpkm/internal/config"
	"fpkm/internal/fpk"
)

// fnchatCmdMain 是 fnchat 1.0.7 真实 cmd/main 中启动命令的等价文本。
// 关键点：CMD 里在 python 主进程之后，用 " & " 拼接了一段 shell 控制结构
// （for/do/done 轮询 8033 端口），最后才启动 gateway-proxy —— 而正是
// gateway-proxy 负责创建 fnchat.sock。
const fnchatCmdMain = `#!/bin/bash
LOG_FILE="${TRIM_PKGVAR}/info.log"
PID_FILE="${TRIM_PKGVAR}/app.pid"
CONFIG_PATH="${TRIM_APPDEST_VOL}/@appshare/${TRIM_APPNAME}/config.json"
APP_SRC_DIR="${TRIM_APPDEST}/server"
VENV_DIR="${TRIM_PKGVAR}/.venv"

start_process() {
  if status; then
    return 0
  fi
  VENV_PYTHON="${VENV_DIR}/bin/python"
  CMD="\"${VENV_PYTHON}\" \"${APP_SRC_DIR}/fnchat.py\" -c ${CONFIG_PATH} & for i in \$(seq 1 30); do nc -z 127.0.0.1 8033 2>/dev/null && break; sleep 1; done; ${TRIM_APPDEST}/server/gateway-proxy -socket \"/var/apps/fnchat/target/fnchat.sock\" -target \"http://127.0.0.1:8033\" -prefix \"/app/fnchat\""
  bash -c "${CMD}" >>"${LOG_FILE}" 2>&1 &
  printf "%s" "$!" >"${PID_FILE}"
  return 0
}

stop_process() {
  return 0
}

status() {
  return 1
}

case $1 in
  start)
    start_process
    ;;
  stop)
    stop_process
    ;;
esac
`

// TestFnchatGatewayProxyNotExtracted 复现 fnchat "Socket 不存在" 问题。
//
// fnchat 是统一网关应用，fnchat.sock 由 gateway-proxy 创建。
// cmd/main 的 CMD 形如：
//
//	python fnchat.py -c cfg & for i in $(seq 1 30); do nc -z ...; done; gateway-proxy -socket .../fnchat.sock ...
//
// fpkm 的 extractCmdFromBashScript 按 " & " 切分多进程，会把第二段
// 当作可执行文件，于是第一个 token "for" 被当成命令 —— gateway-proxy
// 不会被启动，socket 自然不存在。
func TestFnchatGatewayProxyNotExtracted(t *testing.T) {
	base := t.TempDir()
	cfg := &config.Config{
		BaseDir:     base,
		AppsDir:     filepath.Join(base, "apps"),
		BinDir:      filepath.Join(base, "bin"),
		LogDir:      filepath.Join(base, "logs"),
		GatewayPort: 7666,
	}

	appDir := cfg.AppDir("fnchat")
	targetDir := cfg.AppTargetDir("fnchat")
	for _, d := range []string{
		appDir,
		targetDir,
		filepath.Join(appDir, "cmd"),
		filepath.Join(targetDir, "server"),
		filepath.Join(appDir, "var", ".venv", "bin"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// 伪造 venv 中的 python，使 fpkm 认为主命令存在从而走"提取命令"分支
	venvPython := filepath.Join(appDir, "var", ".venv", "bin", "python")
	if err := os.WriteFile(venvPython, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 真实的 gateway-proxy 可执行文件
	gwProxy := filepath.Join(targetDir, "server", "gateway-proxy")
	if err := os.WriteFile(gwProxy, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// 写入 cmd/main，并按 fpkm 安装时的行为重映射 /var/apps/fnchat
	remapped := strings.ReplaceAll(fnchatCmdMain, "/var/apps/fnchat", appDir)
	if err := os.WriteFile(filepath.Join(appDir, "cmd", "main"), []byte(remapped), 0o755); err != nil {
		t.Fatal(err)
	}

	manifest := &fpk.Manifest{
		Appname:     "fnchat",
		Version:     "1.0.7",
		CheckPort:   "true",
		ServicePort: "",
		Raw:         map[string]string{},
	}
	env, err := BuildEnv(cfg, manifest)
	if err != nil {
		t.Fatal(err)
	}

	entry, err := DetectEntryWithEnv(targetDir, env)
	if err != nil {
		t.Fatalf("探测入口失败: %v", err)
	}

	t.Logf("Type      = %s", entry.Type)
	t.Logf("Command   = %s", entry.Command)
	t.Logf("Args      = %v", entry.Args)
	for i, ec := range entry.ExtraCmds {
		t.Logf("ExtraCmd[%d] = %q %v", i, ec.Command, ec.Args)
	}

	// 断言：必须有一个附加进程真正启动 gateway-proxy
	found := false
	for _, ec := range entry.ExtraCmds {
		if strings.HasSuffix(ec.Command, "gateway-proxy") {
			found = true
		}
	}
	if !found {
		t.Errorf("未提取到 gateway-proxy 启动命令 —— fnchat.sock 不会被创建，网关将报“Socket 不存在”")
	}
}
