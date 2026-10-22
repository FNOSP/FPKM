package app

import (
	"encoding/binary"
	"fmt"
	"fpkm/internal/logger"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// EntryType 应用入口类型
type EntryType int

const (
	EntryNodeJS     EntryType = iota // Node.js 应用
	EntryBinary                      // 原生二进制
	EntryStatic                      // 静态文件
	EntryPython                      // Python 应用
	EntryDocker                      // Docker 应用
	EntryScript                      // cmd/main 脚本
	EntryBashScript                  // 完整 bash 脚本（直接执行 cmd/main start）
	EntryUnknown                     // 未知类型
)

// PreStartCommand 预启动命令（在主进程启动前执行）
type PreStartCommand struct {
	Command  string   // 二进制路径
	Args     []string // 命令参数（不含二进制路径本身）
	RunOnce  bool     // 是否仅执行一次（有标记文件保护）
	FlagFile string   // 标记文件路径（RunOnce=true 时有效）
	Desc     string   // 命令描述（从附近注释提取）
}

// ExtraCommand 附加进程（与主进程同时运行）
type ExtraCommand struct {
	Command string   // 二进制路径
	Args    []string // 命令参数
	WorkDir string   // 工作目录
}

// EntryInfo 入口信息
type EntryInfo struct {
	Type         EntryType
	Command      string            // 启动命令
	Args         []string          // 启动参数
	WorkDir      string            // 工作目录
	NeedsInstall bool              // 是否需要先执行安装（如 npm install）
	BinaryPath   string            // 二进制路径（用于兼容性检查）
	PreStartCmds []PreStartCommand // 预启动初始化命令
	ExtraCmds    []ExtraCommand    // 附加进程（多进程应用）
	CmdEnv       map[string]string // CMD 中提取的环境变量（如 PORT=3388）
}

// String 返回入口类型描述
func (t EntryType) String() string {
	switch t {
	case EntryNodeJS:
		return "Node.js"
	case EntryBinary:
		return "二进制程序"
	case EntryStatic:
		return "静态文件"
	case EntryPython:
		return "Python"
	case EntryDocker:
		return "Docker"
	case EntryScript:
		return "脚本(cmd/main)"
	case EntryBashScript:
		return "Bash脚本(cmd/main start)"
	default:
		return "未知"
	}
}

// DetectEntry 自动探测应用入口
// targetDir 对应 TRIM_APPDEST，即应用的可执行文件目录
func DetectEntry(targetDir string) (*EntryInfo, error) {
	return DetectEntryWithEnv(targetDir, nil)
}

// DetectEntryWithEnv 自动探测应用入口，支持传入环境变量
// 环境变量用于解析 bash 脚本中的变量引用（如 ${TRIM_PKGVAR}）
func DetectEntryWithEnv(targetDir string, env EnvMap) (*EntryInfo, error) {
	// 0. 检测 cmd/main 脚本（在 targetDir 的上级目录）
	// targetDir = appDir/target，cmd/main 在 appDir/cmd/main
	appDir := filepath.Dir(targetDir)
	cmdMain := filepath.Join(appDir, "cmd", "main")
	if fileExists(cmdMain) {
		// 检测是否是完整 bash 脚本（含 case 语句和函数定义）
		if isFullBashScript(cmdMain) {
			// 关键：cmd/main 若通过 export 设置启动命令依赖的环境变量，
			// 绝不能走「提取 CMD 行直接执行」的捷径 —— 脚本不运行，
			// 所有 export 都会丢失。
			//
			// 真实故障（wb2api 1.13.0）：
			//	export WB2A_LISTEN=$port     # 端口来自安装向导
			//	export WB2A_API_KEY=$pwd     # 密码来自安装向导
			//	CMD="${TRIM_APPDEST}/bin/wb2api -config ..."
			// 提取 CMD 后脚本没跑，这两个变量消失，应用回退到自带
			// config.json 里写死的端口与示例密钥：
			//   - 向导设置的端口、密码完全失效（密钥退回 "test_key"）
			//   - 应用实际监听端口与 fpkm 公布的访问端口不一致
			//   - 用户打开页面即 404 / 无法连接
			if scriptExportsEnvVars(cmdMain) {
				logger.Debug("cmd/main 通过 export 设置环境变量，改为执行脚本以保留这些变量")
				return &EntryInfo{
					Type:    EntryBashScript,
					Command: "bash",
					Args:    []string{cmdMain, "start"},
					WorkDir: targetDir,
				}, nil
			}

			// 尝试从 bash 脚本中提取启动命令（如 CMD= 行）
			// 这样可以避免执行整个 bash 脚本，直接启动目标进程
			// 对于多进程应用（CMD 含 & 分隔），会拆分为主进程 + 附加进程
			command, args, extras, cmdEnv, ok := extractCmdFromBashScript(cmdMain, env)
			if ok {
				logger.Debug("从 bash 脚本提取到启动命令: %s %s", command, strings.Join(args, " "))
				for i, extra := range extras {
					logger.Debug("附加进程 %d: %s %s", i+1, extra.Command, strings.Join(extra.Args, " "))
				}
				// 检查启动命令中的文件是否存在（如 venv 中的 Python）
				// 如果文件存在，直接启动；否则回退到执行 bash 脚本
				fileExistsCheck := fileExists(command)
				pathExistsCheck := isCommandInPath(command, env)
				if fileExistsCheck || pathExistsCheck {
					entryType := EntryBinary
					if strings.HasSuffix(command, "python") || strings.Contains(command, "/python") || strings.Contains(command, "python3") {
						entryType = EntryPython
					}
					// 提取系统级设置命令（如 ip tuntap、ip link）作为预启动命令
					// 这些命令必须在主进程启动前执行
					preStartCmds := extractSystemSetupCommands(cmdMain)
					if len(preStartCmds) > 0 {
						logger.Info("提取到 %d 个系统级预启动命令（TUN 设备创建等）", len(preStartCmds))
					}
					return &EntryInfo{
						Type:         entryType,
						Command:      command,
						Args:         args,
						WorkDir:      targetDir,
						ExtraCmds:    extras,
						CmdEnv:       cmdEnv,
						PreStartCmds: preStartCmds,
					}, nil
				}
				logger.Debug("启动命令文件不存在，回退到 bash 脚本: %s (fileExists=%v, inPath=%v)", command, fileExistsCheck, pathExistsCheck)
			} else {
				logger.Debug("无法从 bash 脚本提取启动命令，回退到执行 bash 脚本")
			}
			// 无法提取启动命令或文件不存在，回退到执行 shell 脚本
			// （bash 优先，Android 等只有 POSIX sh 的环境回退 sh）
			return &EntryInfo{
				Type:    EntryBashScript,
				Command: shellPath(),
				Args:    []string{cmdMain, "start"},
				WorkDir: targetDir,
			}, nil
		}
		entry, err := parseCmdMain(cmdMain, appDir, targetDir)
		if err == nil {
			return entry, nil
		}

		// 解析脚本提取二进制失败时，回退为「直接执行脚本」。
		//
		// 真实故障：云抽奖 cloud-lottery 1.0.0 的 cmd/main 有 case 分发，
		// 但既没有函数定义也没有 source，因此不会被 looksLikeDispatcherScript
		// 判定为分发脚本，转而走 parseCmdMain；而它的启动命令是
		// `nohup python3 "$APP_ENTRY"`，脚本里没有任何可提取的二进制路径，
		// 最终报「探测入口失败: 无法从cmd/main中提取二进制路径」，应用完全无法启动。
		//
		// 脚本模式下该脚本自身负责环境准备与后台启动（含 start/stop 分支），
		// 直接交给 shell 执行才是正确语义；真正的缺失依赖（如 python3）会由
		// 脚本或运行时诊断给出明确原因。
		// 这里是**正常的回退路径**，不是错误：python312 / nodejs_v24 这类
		// 应用的 cmd/main 本来就没有可提取的二进制（它调用外部解释器），
		// 交给 shell 执行脚本才是正确语义。
		//
		// 因此用 Debug 记录：入口探测会被状态查询反复调用（桌面小组件、
		// 商店列表轮询），用 Warn 会在日志里刷屏。真正的问题（脚本执行失败、
		// 缺依赖）由启动结果与脚本自身输出暴露。
		logger.Debug("cmd/main 无可提取的二进制，按脚本执行: %s start（原因: %v）", cmdMain, err)
		return &EntryInfo{
			Type:    EntryBashScript,
			Command: shellPath(),
			Args:    []string{cmdMain, "start"},
			WorkDir: targetDir,
		}, nil
	}

	// 1. 检测 Docker 应用
	dockerCompose := filepath.Join(targetDir, "docker", "docker-compose.yaml")
	if fileExists(dockerCompose) {
		return &EntryInfo{
			Type:    EntryDocker,
			Command: "docker",
			Args:    []string{"compose", "-f", dockerCompose, "up", "-d"},
			WorkDir: filepath.Dir(dockerCompose),
		}, nil
	}

	// 2. 检测 bin/ 目录下的二进制（最常见：$TRIM_APPDEST/bin/xxx）
	binDir := filepath.Join(targetDir, "bin")
	if entries, err := os.ReadDir(binDir); err == nil {
		for _, entry := range entries {
			if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			binPath := filepath.Join(binDir, entry.Name())
			if isExecutable(binPath) {
				return &EntryInfo{
					Type:       EntryBinary,
					Command:    binPath,
					Args:       []string{"server"},
					WorkDir:    targetDir,
					BinaryPath: binPath,
				}, nil
			}
		}
	}

	// 3. 检测 Node.js 应用
	serverJS := filepath.Join(targetDir, "server", "server.js")
	if fileExists(serverJS) {
		return &EntryInfo{
			Type:         EntryNodeJS,
			Command:      "node",
			Args:         []string{"server.js"},
			WorkDir:      filepath.Dir(serverJS),
			NeedsInstall: !fileExists(filepath.Join(filepath.Dir(serverJS), "node_modules")),
		}, nil
	}
	pkgJSON := filepath.Join(targetDir, "package.json")
	if fileExists(pkgJSON) {
		return &EntryInfo{
			Type:         EntryNodeJS,
			Command:      "node",
			Args:         []string{"server.js"},
			WorkDir:      targetDir,
			NeedsInstall: !fileExists(filepath.Join(targetDir, "node_modules")),
		}, nil
	}

	// 4. 检测 Python 应用
	for _, main := range []string{"main.py", "app.py", "server.py", "manage.py"} {
		pyFile := filepath.Join(targetDir, main)
		if fileExists(pyFile) {
			return &EntryInfo{
				Type:    EntryPython,
				Command: "python3",
				Args:    []string{main},
				WorkDir: targetDir,
			}, nil
		}
	}

	// 5. 检测原生二进制 (target/main, target/app)
	for _, name := range []string{"main", "app", "start"} {
		binPath := filepath.Join(targetDir, name)
		if isExecutable(binPath) {
			return &EntryInfo{
				Type:       EntryBinary,
				Command:    binPath,
				Args:       []string{},
				WorkDir:    targetDir,
				BinaryPath: binPath,
			}, nil
		}
	}

	// 6. 检测静态文件
	for _, idx := range []string{
		filepath.Join(targetDir, "www", "index.html"),
		filepath.Join(targetDir, "ui", "index.html"),
	} {
		if fileExists(idx) {
			return &EntryInfo{
				Type:    EntryStatic,
				Command: "__builtin_static__",
				Args:    []string{filepath.Dir(idx)},
				WorkDir: filepath.Dir(idx),
			}, nil
		}
	}

	return nil, fmt.Errorf("无法识别应用入口，请检查应用结构")
}

// parseCmdMain 解析 cmd/main 脚本提取启动命令
func parseCmdMain(scriptPath, appDir, targetDir string) (*EntryInfo, error) {
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		return nil, err
	}
	content := string(data)

	// 1. 解析脚本中的变量定义（VAR="value" 或 VAR=value）
	shellVars := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		idx := strings.Index(line, "=")
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		val = strings.Trim(val, `"'`)
		// 处理 bash 转义引号（\" → "）
		val = strings.ReplaceAll(val, `\"`, `"`)
		val = strings.ReplaceAll(val, `\'`, `'`)
		// 跳过命令替换（如 $(get_uiport)），运行时从环境变量获取
		if strings.HasPrefix(val, "$(") {
			continue
		}
		// 解析变量引用 $VAR 或 ${VAR}
		val = resolveShellVars(val, shellVars, appDir, targetDir)
		shellVars[key] = val
	}

	// 2. 提取二进制路径和启动参数
	binaryPath := ""
	args := []string{}
	var extraCmds []ExtraCommand

	// 模式A: DUFS_BIN 变量 + nohup 启动（如 OpenList）
	if v, ok := shellVars["DUFS_BIN"]; ok {
		binaryPath = v
	}
	// 模式B: 搜索包含 /bin/ 或 /server/ 的变量
	if binaryPath == "" {
		for _, v := range shellVars {
			if (strings.Contains(v, "/bin/") || strings.Contains(v, "/server/")) && fileExists(v) {
				binaryPath = v
				break
			}
		}
	}

	// 模式C: CMD 变量包含完整启动命令（如 EasyTier-Web）
	cmdVar := ""
	if v, ok := shellVars["CMD"]; ok {
		cmdVar = v
	}

	var cmdEnv map[string]string
	if binaryPath == "" && cmdVar != "" {
		// 从 CMD 变量中提取主进程和附加进程
		binaryPath, args, extraCmds, cmdEnv = extractFromCMD(cmdVar, shellVars, appDir, targetDir)
	}

	// 3. 提取启动参数（从 nohup 行或 bash -c 行）
	if len(args) == 0 {
		args = []string{"server"} // 默认参数
	}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		// 模式A: nohup 启动
		if strings.Contains(line, "nohup") && binaryPath != "" {
			nohupIdx := strings.Index(line, "nohup")
			rest := line[nohupIdx+5:]
			rest = strings.TrimSpace(rest)
			parts := parseShellArgs(rest)
			if len(parts) > 1 {
				resolved := make([]string, 0, len(parts)-1)
				skipNext := false
				for _, p := range parts[1:] {
					if p == ">>" || p == ">" {
						skipNext = true
						continue
					}
					if skipNext {
						skipNext = false
						continue
					}
					if p == "2>&1" || p == "&" || p == "1>&2" || p == ";" {
						continue
					}
					p = resolveShellVars(p, shellVars, appDir, targetDir)
					if p != "" {
						resolved = append(resolved, p)
					}
				}
				if len(resolved) > 0 {
					args = resolved
				}
			}
			break
		}
		// 模式C: bash -c "${CMD}" 启动 —— args 已从 CMD 提取，无需再解析
		if strings.Contains(line, `bash -c`) && cmdVar != "" {
			break
		}
	}

	// 4. 提取工作目录（cd "$DATA_DIR"）
	workDir := targetDir
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "cd ") {
			cdPath := strings.TrimSpace(line[3:])
			// 在 shell 操作符处截断（||, &&, ;, 空格后跟操作符）
			for _, sep := range []string{" ||", " &&", " ;", ";&"} {
				if idx := strings.Index(cdPath, sep); idx >= 0 {
					cdPath = cdPath[:idx]
				}
			}
			cdPath = strings.Trim(cdPath, `"'`)
			cdPath = resolveShellVars(cdPath, shellVars, appDir, targetDir)
			if cdPath != "" {
				workDir = cdPath
			}
			break
		}
	}

	// 5. 通用提取：nohup 之前所有调用应用二进制的预启动命令
	preStartCmds := parsePreStartCommands(content, binaryPath, shellVars, appDir, targetDir)

	// 6. 回退检测
	if binaryPath == "" {
		binDir := filepath.Join(targetDir, "bin")
		if entries, err := os.ReadDir(binDir); err == nil {
			for _, entry := range entries {
				if !entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") {
					binaryPath = filepath.Join(binDir, entry.Name())
					break
				}
			}
		}
	}

	if binaryPath == "" {
		return nil, fmt.Errorf("无法从cmd/main中提取二进制路径")
	}

	// 确保二进制有执行权限（Unix）
	if runtime.GOOS != "windows" {
		os.Chmod(binaryPath, 0755)
	}

	return &EntryInfo{
		Type:         EntryScript,
		Command:      binaryPath,
		Args:         args,
		WorkDir:      workDir,
		BinaryPath:   binaryPath,
		PreStartCmds: preStartCmds,
		ExtraCmds:    extraCmds,
		CmdEnv:       cmdEnv,
	}, nil
}

// extractFromCMD 从 CMD 变量中提取主进程和附加进程
// CMD 格式: "binary1 args & sudo binary2 args & binary3 args"
// 也支持前缀环境变量赋值: "PORT=3388 node index.js"
// sudo 前缀会被去除，第一个进程作为主进程，其余作为附加进程。
//
// CMD 中还可能混有 shell 控制结构（轮询循环等），切分时会过滤掉，
// 并确保 gateway-proxy 这类 socket 代理进程被保留。
func extractFromCMD(cmdVar string, shellVars map[string]string, appDir, targetDir string) (string, []string, []ExtraCommand, map[string]string) {
	// 解析 CMD 中的变量引用
	resolvedCMD := resolveShellVars(cmdVar, shellVars, appDir, targetDir)

	// 按 shell 语义切分，过滤控制流关键字
	processes := splitShellCommands(resolvedCMD)

	var mainBinary string
	var mainArgs []string
	var extras []ExtraCommand
	cmdEnv := map[string]string{}

	firstFound := false
	for _, proc := range processes {
		proc = strings.TrimSpace(proc)
		if proc == "" {
			continue
		}
		// 去除 sudo 前缀（在非 root 环境下直接运行）
		proc = strings.TrimPrefix(proc, "sudo ")
		proc = strings.TrimSpace(proc)

		// 提取前缀环境变量赋值（如 PORT=3388 node ... → env PORT=3388, cmd=node ...）
		proc, procEnv := extractPrefixEnv(proc)
		for k, v := range procEnv {
			cmdEnv[k] = v
		}

		parts := parseShellArgs(proc)
		if len(parts) == 0 {
			continue
		}
		if isShellKeyword(parts[0]) {
			continue
		}

		if !firstFound {
			mainBinary = parts[0]
			mainArgs = parts[1:]
			firstFound = true
		} else {
			extras = append(extras, ExtraCommand{
				Command: parts[0],
				Args:    parts[1:],
			})
		}
	}

	// 兜底：确保 socket 代理进程不被遗漏
	for _, gw := range extractSocketProxyCommands(resolvedCMD) {
		dup := false
		for _, ec := range extras {
			if ec.Command == gw.Command && strings.Join(ec.Args, " ") == strings.Join(gw.Args, " ") {
				dup = true
				break
			}
		}
		if !dup && !(mainBinary == gw.Command && strings.Join(mainArgs, " ") == strings.Join(gw.Args, " ")) {
			extras = append(extras, gw)
		}
	}

	return mainBinary, mainArgs, extras, cmdEnv
}

// extractPrefixEnv 提取命令前的环境变量赋值
// "PORT=3388 node index.js" → ("node index.js", {PORT: "3388"})
func extractPrefixEnv(cmd string) (string, map[string]string) {
	env := map[string]string{}
	tokens := strings.Fields(cmd)
	i := 0
	for i < len(tokens) {
		token := tokens[i]
		// 匹配 KEY=VALUE 格式
		if idx := strings.Index(token, "="); idx > 0 {
			key := token[:idx]
			val := token[idx+1:]
			// key 必须是合法的环境变量名
			if isValidEnvName(key) {
				env[key] = val
				i++
				continue
			}
		}
		break
	}
	// 剩余部分是实际命令
	rest := strings.TrimSpace(strings.Join(tokens[i:], " "))
	return rest, env
}

// isValidEnvName 检查是否是合法的环境变量名
func isValidEnvName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	// 首字符不能是数字
	if name[0] >= '0' && name[0] <= '9' {
		return false
	}
	return true
}

// resolveShellVars 解析 shell 变量引用 ($VAR, ${VAR}, ${VAR:-default})
// 将 TRIM_APPDEST -> targetDir, TRIM_PKGVAR -> appDir/var 等映射为实际路径
func resolveShellVars(val string, shellVars map[string]string, appDir, targetDir string) string {
	// 先处理 ${VAR:-default} 语法（带默认值的变量引用）
	// 如果 VAR 在 shellVars 中已定义且非空，使用其值；否则保留原样让运行时处理
	defaultVarPattern := regexp.MustCompile(`\$\{(\w+):-(\w+)\}`)
	val = defaultVarPattern.ReplaceAllStringFunc(val, func(match string) string {
		sub := defaultVarPattern.FindStringSubmatch(match)
		varName := sub[1]
		if v, ok := shellVars[varName]; ok && v != "" {
			return v
		}
		return match // 保留原样
	})

	// 解析已定义的 shell 变量
	for k, v := range shellVars {
		val = strings.ReplaceAll(val, "$"+k, v)
		val = strings.ReplaceAll(val, "${"+k+"}", v)
	}

	// 解析 TRIM_* 变量
	trimMap := map[string]string{
		"${TRIM_APPDEST}":          targetDir,
		"$TRIM_APPDEST":            targetDir,
		"${TRIM_PKGVAR}":           filepath.Join(appDir, "var"),
		"$TRIM_PKGVAR":             filepath.Join(appDir, "var"),
		"${TRIM_PKGETC}":           filepath.Join(appDir, "etc"),
		"$TRIM_PKGETC":             filepath.Join(appDir, "etc"),
		"${TRIM_PKGHOME}":          filepath.Join(appDir, "home"),
		"$TRIM_PKGHOME":            filepath.Join(appDir, "home"),
		"${TRIM_PKGTMP}":           filepath.Join(appDir, "tmp"),
		"$TRIM_PKGTMP":             filepath.Join(appDir, "tmp"),
		"${TRIM_APPDEST_VOL}":      appDir,
		"$TRIM_APPDEST_VOL":        appDir,
		"${TRIM_DATA_SHARE_PATHS}": filepath.Join(appDir, "shares"),
		"$TRIM_DATA_SHARE_PATHS":   filepath.Join(appDir, "shares"),
	}
	// TRIM_APPNAME 从目录名推断
	appName := filepath.Base(appDir)
	trimMap["${TRIM_APPNAME}"] = appName
	trimMap["$TRIM_APPNAME"] = appName
	for k, v := range trimMap {
		val = strings.ReplaceAll(val, k, v)
	}

	// @appshare 映射到 shares 目录（飞牛的 @appshare 在 fpkm 中对应 shares）
	val = strings.ReplaceAll(val, "/@appshare/", "/shares/")

	return val
}

// parsePreStartCommands 通用解析器：提取 nohup 之前所有调用应用二进制的预启动命令
// 识别两种模式：
//  1. 标记文件保护的一次性命令: if [ ! -f "$FLAG" ]; then ... "$BIN" cmd ... touch "$FLAG"; fi
//  2. 每次启动都执行的命令: "$BIN" cmd args
//
// 也支持命令在 if 条件中的模式: if "$BIN" cmd args; then
func parsePreStartCommands(content, binaryPath string, shellVars map[string]string, appDir, targetDir string) []PreStartCommand {
	var cmds []PreStartCommand

	lines := strings.Split(content, "\n")

	// 合并 shellVars 和脚本内的 local 变量（如 local admin_flag="..."）
	allVars := make(map[string]string)
	for k, v := range shellVars {
		allVars[k] = v
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// 匹配 local VAR="value" 或 VAR="value"
		if strings.HasPrefix(line, "local ") {
			line = strings.TrimPrefix(line, "local ")
		}
		if idx := strings.Index(line, "="); idx > 0 {
			varName := strings.TrimSpace(line[:idx])
			val := strings.TrimSpace(line[idx+1:])
			val = strings.Trim(val, `"'`)
			if varName != "" && !strings.Contains(varName, " ") {
				// 只添加 shellVars 中不存在的变量（避免覆盖已解析的值）
				if _, exists := allVars[varName]; !exists {
					allVars[varName] = val
				}
			}
		}
	}

	// 找到 nohup 行的位置，只处理 nohup 之前的代码
	nohupLineIdx := -1
	for i, line := range lines {
		if strings.Contains(strings.TrimSpace(line), "nohup") {
			nohupLineIdx = i
			break
		}
	}
	if nohupLineIdx < 0 {
		return cmds
	}

	// 识别所有调用应用二进制的变量名（如 $DUFS_BIN, $BIN, $APP_BIN）
	binVarNames := []string{}
	for varName, val := range allVars {
		if val == binaryPath {
			binVarNames = append(binVarNames, "$"+varName, "${"+varName+"}")
		}
	}

	// 状态机：跟踪 if 块中的标记文件和嵌套深度
	flagGuardActive := false
	currentFlagFile := ""
	ifDepth := 0        // 总 if 嵌套深度
	flagGuardDepth := 0 // 标记文件保护块的起始深度
	lastComment := ""

	for i := 0; i < nohupLineIdx; i++ {
		line := strings.TrimSpace(lines[i])

		// 记录注释作为命令描述
		if strings.HasPrefix(line, "#") {
			lastComment = strings.TrimSpace(strings.TrimPrefix(line, "#"))
			continue
		}

		// 检测标记文件保护块开始: if [ ! -f "$FLAG" ]; then
		if strings.Contains(line, "if") && strings.Contains(line, "! -f") {
			ifDepth++
			flagGuardActive = true
			flagGuardDepth = ifDepth
			currentFlagFile = extractFlagPath(line, allVars, appDir, targetDir)
			continue
		}

		// 检测普通 if 块开始（含 if "$BIN" cmd; then 模式）
		if strings.HasPrefix(line, "if ") {
			ifDepth++
			// 处理 if "$BIN" cmd args; then 模式 —— 命令在 if 条件中
			if isBinaryCall(line, binVarNames, binaryPath) {
				cleaned := stripIfThen(line)
				if cleaned != "" {
					extractAndAddCommand(&cmds, cleaned, binaryPath, allVars, appDir, targetDir,
						flagGuardActive, currentFlagFile, lastComment)
				}
			}
			continue
		}

		// 检测 fi 结束
		if line == "fi" || line == "fi;" {
			if flagGuardActive && ifDepth == flagGuardDepth {
				flagGuardActive = false
				currentFlagFile = ""
			}
			ifDepth--
			continue
		}

		// 在标记文件保护块内，跳过 touch 命令
		if flagGuardActive && strings.HasPrefix(line, "touch ") {
			continue
		}

		// 跳过控制流关键字、变量赋值、函数定义等
		if strings.HasPrefix(line, "elif ") || strings.HasPrefix(line, "then") ||
			strings.HasPrefix(line, "else") || strings.HasPrefix(line, "for ") ||
			strings.HasPrefix(line, "while ") || strings.HasPrefix(line, "do") ||
			strings.HasPrefix(line, "done") || strings.HasPrefix(line, "nohup") ||
			strings.HasPrefix(line, "local ") || strings.HasPrefix(line, "function ") ||
			strings.Contains(line, "()") || strings.HasPrefix(line, "return ") {
			continue
		}

		// 检测调用应用二进制的行
		if !isBinaryCall(line, binVarNames, binaryPath) {
			continue
		}

		extractAndAddCommand(&cmds, line, binaryPath, allVars, appDir, targetDir,
			flagGuardActive, currentFlagFile, lastComment)
		lastComment = ""
	}

	return cmds
}

// stripIfThen 从 "if CMD; then" 中提取 CMD
func stripIfThen(line string) string {
	s := strings.TrimPrefix(line, "if ")
	s = strings.TrimSpace(s)
	// 去掉末尾的 ; then 或 then
	if strings.HasSuffix(s, "; then") {
		s = strings.TrimSuffix(s, "; then")
	} else if strings.HasSuffix(s, ";then") {
		s = strings.TrimSuffix(s, ";then")
	} else if strings.HasSuffix(s, " then") {
		s = strings.TrimSuffix(s, " then")
	}
	return strings.TrimSpace(s)
}

// extractAndAddCommand 从命令行提取参数并添加到预启动命令列表
func extractAndAddCommand(cmds *[]PreStartCommand, line, binaryPath string,
	allVars map[string]string, appDir, targetDir string,
	flagGuardActive bool, currentFlagFile, desc string) {

	parts := parseShellArgs(line)
	if len(parts) < 2 {
		return
	}

	// parts[0] 是二进制变量名（如 $DUFS_BIN）或路径
	// parts[1:] 是实际参数
	resolved := make([]string, 0, len(parts)-1)
	skipNext := false
	for j := 1; j < len(parts); j++ {
		p := parts[j]
		// 跳过重定向符号及其目标文件
		if p == ">>" || p == ">" {
			skipNext = true
			continue
		}
		if skipNext {
			skipNext = false
			continue
		}
		if p == "2>&1" || p == "&" || p == "1>&2" || p == ";" {
			continue
		}
		p = resolveShellVars(p, allVars, appDir, targetDir)
		if p != "" {
			resolved = append(resolved, p)
		}
	}

	if len(resolved) == 0 {
		return
	}

	cmd := PreStartCommand{
		Command:  binaryPath,
		Args:     resolved,
		RunOnce:  flagGuardActive && currentFlagFile != "",
		FlagFile: currentFlagFile,
		Desc:     desc,
	}
	*cmds = append(*cmds, cmd)
}

// isBinaryCall 检查行是否真正"调用"了应用二进制。
//
// 关键点：不能只判断"行里是否出现二进制路径/变量"。真实脚本中大量存在
// 仅用于判断的 test 表达式，例如：
//
//	if [ ! -x "$APP_BIN" ]; then            # 只检查可执行文件是否存在
//	if [ ! -S "${APP_BIN}" ]; then          # 只检查 socket 是否存在
//	while [ ! -f "$PID_FILE" ]; do
//	if test -x "$APP_BIN"; then
//
// 这些行并不启动任何进程。旧实现把它们当成调用，导致整行被解析成
// 预启动命令，产生畸形输出（用户可见现象）：
//
//	执行预启动命令: /…/fnos-port-scanner ! -x /…/fnos-port-scanner ] …
//
// 因此这里先排除 test 表达式，再判断是否真的是命令调用。
func isBinaryCall(line string, binVarNames []string, binaryPath string) bool {
	trimmed := strings.TrimSpace(line)

	// 1) test 表达式（[ ... ] / [[ ... ]] / test ...）不是命令调用
	if isTestExpression(trimmed) {
		return false
	}

	// 2) 仅出现在注释里的不算
	if strings.HasPrefix(trimmed, "#") {
		return false
	}

	// 3) 该行是否引用了二进制（变量名或直接路径）
	refers := false
	for _, varName := range binVarNames {
		if strings.Contains(trimmed, varName) {
			refers = true
			break
		}
	}
	if !refers && binaryPath != "" && strings.Contains(trimmed, binaryPath) {
		refers = true
	}
	if !refers {
		return false
	}

	// 4) 引用位置必须是"命令词"：即该 token 出现在行首，
	//    或紧跟在 if/while/until/&&/||/; 之后（shell 中这些位置才是命令位置）。
	return referencesAsCommand(trimmed, binVarNames, binaryPath)
}

// isTestExpression 判断整行是否是 shell 的 test/条件表达式。
//
// 覆盖 `[ ... ]`、`[[ ... ]]`、`test ...` 三种形式，
// 以及 `if`/`while`/`elif` 前缀与 `; then`/`; do` 后缀。
func isTestExpression(line string) bool {
	s := strings.TrimSpace(line)

	// 去掉 if / while / until / elif 前缀
	for _, kw := range []string{"if ", "while ", "until ", "elif "} {
		if strings.HasPrefix(s, kw) {
			s = strings.TrimSpace(s[len(kw):])
			break
		}
	}

	// 去掉结尾的 ; then / ; do / then / do
	for _, suf := range []string{"; then", ";then", "; do", ";do", " then", " do"} {
		if strings.HasSuffix(s, suf) {
			s = strings.TrimSpace(s[:len(s)-len(suf)])
			break
		}
	}

	// test 命令
	if strings.HasPrefix(s, "test ") {
		return true
	}
	// [ ... ] / [[ ... ]]
	if strings.HasPrefix(s, "[ ") || strings.HasPrefix(s, "[[ ") || s == "[" || s == "[[" {
		return true
	}
	return false
}

// commandPositions 是 shell 中"命令词"可出现的位置前缀
func referencesAsCommand(line string, binVarNames []string, binaryPath string) bool {
	// 收集所有可能的引用形式
	var refs []string
	refs = append(refs, binVarNames...)
	if binaryPath != "" {
		refs = append(refs, binaryPath)
	}

	for _, ref := range refs {
		if ref == "" {
			continue
		}
		idx := strings.Index(line, ref)
		for idx >= 0 {
			// 已是行首？
			if idx == 0 {
				return true
			}
			before := strings.TrimRight(line[:idx], " \t")
			// 引号包裹也可，如 `"$BIN"` —— 向前跳过引号
			before = strings.TrimRight(before, `"'`)
			before = strings.TrimRight(before, " \t")

			if before == "" {
				return true
			}
			// 命令行位置的前导记号
			for _, p := range []string{"if", "while", "until", "elif", "then", "do", "&&", "||", ";", "|", "&", "nohup", "exec", "sudo"} {
				if strings.HasSuffix(before, p) {
					// 排除 "iffoo" 这类误匹配：要求前面是分隔符或行首
					if len(before) == len(p) {
						return true
					}
					prev := before[len(before)-len(p)-1]
					if prev == ' ' || prev == '\t' || prev == ';' || prev == '&' || prev == '|' {
						return true
					}
				}
			}
			// 继续找下一个引用位置
			next := strings.Index(line[idx+len(ref):], ref)
			if next < 0 {
				break
			}
			idx = idx + len(ref) + next
		}
	}
	return false
}

// extractFlagPath 从 if [ ! -f "$FLAG" ] 行提取标记文件路径
func extractFlagPath(line string, shellVars map[string]string, appDir, targetDir string) string {
	// 匹配 "$FLAG" 或 ${FLAG} 模式
	start := strings.Index(line, `"$`)
	if start < 0 {
		start = strings.Index(line, `"${`)
	}
	if start < 0 {
		return ""
	}
	start++ // skip leading quote

	// 找到结束引号
	end := strings.Index(line[start:], `"`)
	if end < 0 {
		return ""
	}
	varRef := line[start : start+end]

	resolved := resolveShellVars(varRef, shellVars, appDir, targetDir)
	return resolved
}

// parseShellArgs 简单解析 shell 命令参数
// 支持 \ 转义字符（如 \" 不会结束引号）
func parseShellArgs(line string) []string {
	var parts []string
	current := ""
	inQuote := false
	quoteChar := byte(0)
	escaped := false

	for i := 0; i < len(line); i++ {
		c := line[i]
		if escaped {
			current += string(c)
			escaped = false
			continue
		}
		// 反斜杠转义下一个字符（单引号内不转义）
		if c == '\\' && quoteChar != '\'' {
			escaped = true
			continue
		}
		if inQuote {
			if c == quoteChar {
				inQuote = false
			} else {
				current += string(c)
			}
		} else {
			if c == '"' || c == '\'' {
				inQuote = true
				quoteChar = c
			} else if c == ' ' || c == '\t' {
				if current != "" {
					parts = append(parts, current)
					current = ""
				}
			} else {
				current += string(c)
			}
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}

// CheckBinaryCompat 检查二进制文件兼容性
// 返回是否可运行，以及原因说明
func CheckBinaryCompat(binaryPath string) (bool, string) {
	file, err := os.Open(binaryPath)
	if err != nil {
		return false, "无法读取二进制文件"
	}
	defer file.Close()

	header := make([]byte, 20)
	n, err := file.Read(header)
	if err != nil || n < 4 {
		return false, "无法读取文件头"
	}

	// ELF (Linux)
	if header[0] == 0x7F && header[1] == 0x45 && header[2] == 0x4C && header[3] == 0x46 {
		if runtime.GOOS == "linux" {
			// 检查架构
			if n >= 20 {
				// ELF header: byte 18-19 = e_machine
				// 0x3E = x86_64, 0xB7 = aarch64, 0x03 = x86
				machine := binary.LittleEndian.Uint16(header[18:20])
				goArch := runtime.GOARCH
				if (machine == 0x3E && goArch == "amd64") ||
					(machine == 0x03 && goArch == "386") ||
					(machine == 0xB7 && goArch == "arm64") {
					return true, "Linux ELF (兼容)"
				}
				return false, fmt.Sprintf("Linux ELF (架构不匹配: 二进制=0x%x, 当前=%s)", machine, goArch)
			}
			return true, "Linux ELF"
		}
		return false, "Linux ELF 二进制，无法在 " + runtime.GOOS + " 上运行"
	}

	// PE (Windows)
	if header[0] == 0x4D && header[1] == 0x5A { // "MZ"
		if runtime.GOOS == "windows" {
			return true, "Windows PE (兼容)"
		}
		return false, "Windows PE 二进制，无法在 " + runtime.GOOS + " 上运行"
	}

	// Mach-O (macOS)
	if (header[0] == 0xFE && header[1] == 0xED && header[2] == 0xFA) ||
		(header[0] == 0xCF && header[1] == 0xFA && header[2] == 0xED) {
		if runtime.GOOS == "darwin" {
			return true, "macOS Mach-O (兼容)"
		}
		return false, "macOS Mach-O 二进制，无法在 " + runtime.GOOS + " 上运行"
	}

	// 脚本文件
	if header[0] == '#' && header[1] == '!' {
		return true, "脚本文件"
	}

	return true, "未知格式（可能兼容）"
}

// IsRootScript 检查脚本是否包含需要 Root 权限的命令
func IsRootScript(scriptPath string) bool {
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		return false
	}
	content := strings.ToLower(string(data))
	keywords := []string{"systemctl", "useradd", "usermod", "groupadd", "chkconfig", "service "}
	for _, kw := range keywords {
		if strings.Contains(content, kw) {
			return true
		}
	}
	return false
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// isExecutable 检查文件是否可执行
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	// Unix: 检查执行权限
	if runtime.GOOS != "windows" {
		return info.Mode()&0111 != 0
	}
	// Windows: 检查扩展名
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".exe" || ext == ".bat" || ext == ".cmd" || ext == ""
}

// isFullBashScript 检测 cmd/main 是否是完整的 bash 脚本
// 完整 bash 脚本特征：包含 case 语句、函数定义等复杂结构
// 这类脚本不适合解析提取命令，应直接执行 bash cmd/main start
// extractSystemSetupCommands 从 bash 脚本中提取系统级设置命令
// 这些命令需要在主进程启动前执行，例如创建 TUN 设备、启用网络接口等
func extractSystemSetupCommands(scriptPath string) []PreStartCommand {
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		return nil
	}

	var cmds []PreStartCommand
	lines := strings.Split(string(data), "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// 检查是否是系统级设置命令
		systemCmdPrefixes := []string{
			"ip tuntap add",   // 创建 TUN/TAP 设备
			"ip link set dev", // 启用网络设备
			"ip link add",     // 创建网络设备
			"modprobe ",       // 加载内核模块
			"sysctl -w ",      // 修改内核参数
		}

		isSystemCmd := false
		for _, prefix := range systemCmdPrefixes {
			if strings.Contains(line, prefix) {
				isSystemCmd = true
				break
			}
		}
		if !isSystemCmd {
			continue
		}

		// 跳过条件判断中的命令（如 if [ ... ]; then ip tuntap ...）
		// 只提取实际执行的命令行
		if strings.HasPrefix(line, "if ") || strings.HasPrefix(line, "while ") ||
			strings.HasPrefix(line, "for ") || strings.HasPrefix(line, "case ") {
			continue
		}

		// 解析命令和参数
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}

		// 跳过 echo/log 语句中的文本
		if parts[0] == "echo" || parts[0] == "log_msg" || parts[0] == "printf" {
			continue
		}

		// 替换脚本变量为环境变量值（简化处理：保留原样，由 shell 解释）
		// 但 ip tuntap/ip link 命令中的变量需要替换
		// 由于我们在 Go 中直接执行，不走 shell，需要替换变量
		// 这里先简单处理：如果命令包含 $ 变量，用 sh -c 执行
		if strings.Contains(line, "$") {
			cmds = append(cmds, PreStartCommand{
				Command: "sh",
				Args:    []string{"-c", line},
			})
		} else {
			cmds = append(cmds, PreStartCommand{
				Command: parts[0],
				Args:    parts[1:],
			})
		}
	}

	return cmds
}

func isFullBashScript(scriptPath string) bool {
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		return false
	}
	return looksLikeDispatcherScript(string(data))
}

// scriptExportsEnvVars 判断 cmd/main 是否用 export 设置了环境变量。
//
// 为什么这个判断决定「执行脚本」还是「提取 CMD 行」：
//
//	extractCmdFromBashScript 只解析 CMD= 这一行，脚本其余部分不会运行。
//	于是 `export FOO=$bar` 这类赋值对最终进程完全不可见。
//	当应用依赖这些变量（端口、密钥、数据目录…）时，进程会带着错误配置
//	启动 —— 典型表现是「装得上、进程也在，但页面访问不了」。
//
// 只认行首的 export（允许缩进），避免把注释或字符串里的 export 误判。
// 形如 `export FOO`（导出已赋值变量）同样计入，因为值来自脚本内部计算。
func scriptExportsEnvVars(scriptPath string) bool {
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "#") {
			continue
		}
		if strings.HasPrefix(t, "export ") || t == "export" {
			return true
		}
	}
	return false
}

// looksLikeDispatcherScript 判断 cmd/main 是否是"分发脚本"：
// 即根据第一个参数在 start/stop/status 之间分发，而不是直接启动某个二进制。
//
// 这类脚本不应被"提取二进制路径"的逻辑处理，而应直接
// `bash cmd/main start` 执行，由脚本自己负责环境准备与后台启动。
//
// 判定条件：存在 case 分发 + 存在函数定义或 source。
//
// case 语句的写法在真实应用里非常多变，早期用固定字符串匹配
// （只认 `case "$1"`）会漏掉合法写法，例如 9router 用的
// `case "${1:-start}" in`（带默认值的参数展开），
// 导致脚本被误判为普通脚本、随后报"无法从cmd/main中提取二进制路径"。
// 这里改为用正则匹配 `case <表达式> in` 的结构。
func looksLikeDispatcherScript(content string) bool {
	hasCase := hasCaseDispatch(content)
	if !hasCase {
		return false
	}
	return hasFunctionDef(content) || hasSourceDirective(content)
}

// caseDispatchRe 匹配 shell 的 case 语句开头：case <expr> in
//
// 覆盖的真实写法：
//
//	case "$1" in
//	case $1 in
//	case "${1:-start}" in
//	case "${ACTION:-start}" in
//	case $1 <换行> in
func caseDispatchRe() *regexp.Regexp {
	return regexp.MustCompile(`(?m)^\s*case\s+\S.*?\s+in\s*(?:[;#].*)?$`)
}

// hasCaseDispatch 判断脚本是否包含 case 分发语句
func hasCaseDispatch(content string) bool {
	// 快速排除：绝大多数普通脚本连 case 关键字都没有
	if !strings.Contains(content, "case") {
		return false
	}
	return caseDispatchRe().MatchString(content)
}

// funcDefRe 匹配 shell 函数定义：name() { 或 function name {
func funcDefRe() *regexp.Regexp {
	return regexp.MustCompile(`(?m)^\s*(?:function\s+)?[A-Za-z_][A-Za-z0-9_]*\s*\(\s*\)\s*\{`)
}

// hasFunctionDef 判断脚本是否定义了 shell 函数
func hasFunctionDef(content string) bool {
	return funcDefRe().MatchString(content)
}

// hasSourceDirective 判断脚本是否 source 了其他文件。
//
// 这类脚本的函数定义在被 source 的文件里（例如 hermes 的
// cmd/main source 了 config/common/service），cmd/main 本身
// 只有 case 语句和函数调用。
func hasSourceDirective(content string) bool {
	sourceRe := regexp.MustCompile(`(?m)^\s*(?:\.|source)\s+\S+`)
	return sourceRe.MatchString(content)
}

// extractCmdFromBashScript 从完整 bash 脚本中提取启动命令
// 解析脚本中的变量定义和 CMD= 行，进行变量替换
// 返回启动命令、参数、附加进程、CMD环境变量和是否成功
// 当 CMD 包含 & 分隔的多进程时，第一个作为主进程，其余作为附加进程
// 当变量被替换为空（依赖函数调用获取值）时，返回 ok=false 让调用方回退到 bash 脚本执行
func extractCmdFromBashScript(scriptPath string, env EnvMap) (command string, args []string, extras []ExtraCommand, cmdEnv map[string]string, ok bool) {
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		return "", nil, nil, nil, false
	}
	content := string(data)

	// 1. 初始化变量表，添加环境变量
	vars := make(map[string]string)
	for k, v := range env {
		vars[k] = v
	}

	// 2. 解析变量定义（VAR="value" 或 VAR=value）
	// 包括函数内部的变量定义（如 VENV_PYTHON、CMD）
	varDefPattern := regexp.MustCompile(`^\s*([A-Za-z_][A-Za-z0-9_]*)=(.*)$`)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		// 跳过注释和条件语句
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, "if ") ||
			strings.HasPrefix(line, "for ") || strings.HasPrefix(line, "while ") ||
			strings.HasPrefix(line, "elif ") || strings.HasPrefix(line, "else") {
			continue
		}
		matches := varDefPattern.FindStringSubmatch(line)
		if matches != nil {
			key := matches[1]
			value := matches[2]
			// 跳过命令替换（如 VAR=$(find_python)）
			if strings.Contains(value, "$(") {
				continue
			}
			// 去除引号
			if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
				value = value[1 : len(value)-1]
			} else if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
				value = value[1 : len(value)-1]
			}
			vars[key] = value
		}
	}

	// 3. 递归解析变量引用
	resolveVar := func(s string) string {
		for i := 0; i < 10; i++ {
			original := s
			// 解析 ${VAR}
			s = regexp.MustCompile(`\$\{([a-zA-Z_][a-zA-Z0-9_]*)\}`).ReplaceAllStringFunc(s, func(m string) string {
				varName := m[2 : len(m)-1]
				if v, ok := vars[varName]; ok {
					return v
				}
				return m
			})
			// 解析 $VAR
			s = regexp.MustCompile(`\$([a-zA-Z_][a-zA-Z0-9_]*)`).ReplaceAllStringFunc(s, func(m string) string {
				varName := m[1:]
				if v, ok := vars[varName]; ok {
					return v
				}
				return m
			})
			if s == original {
				break
			}
		}
		return s
	}

	// 4. 重新解析变量定义，进行变量替换
	for k, v := range vars {
		vars[k] = resolveVar(v)
	}

	// 5. 查找 CMD= 行（在函数内部，所以需要扫描所有行）
	var cmdValue string
	var originalCmdValue string // 变量替换前的原始值，用于检测空变量替换
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "CMD=") {
			cmdValue = strings.TrimSpace(trimmed[4:])
			// 去除外层引号
			if len(cmdValue) >= 2 && cmdValue[0] == '"' && cmdValue[len(cmdValue)-1] == '"' {
				cmdValue = cmdValue[1 : len(cmdValue)-1]
			} else if len(cmdValue) >= 2 && cmdValue[0] == '\'' && cmdValue[len(cmdValue)-1] == '\'' {
				cmdValue = cmdValue[1 : len(cmdValue)-1]
			}
			// 将 \" 转换为 "（bash 转义规则）
			cmdValue = strings.ReplaceAll(cmdValue, `\"`, `"`)
			originalCmdValue = cmdValue
			break
		}
	}

	if cmdValue == "" {
		return "", nil, nil, nil, false
	}

	// 6. 进行变量替换
	cmdValue = resolveVar(cmdValue)

	// 7. 检查是否还有未解析的变量
	if strings.Contains(cmdValue, "${") || regexp.MustCompile(`\$[a-zA-Z_]`).MatchString(cmdValue) {
		return "", nil, nil, nil, false
	}

	// 7.1 检查变量是否被替换为空（如 UI_PORT 在 env 中为空字符串）
	// 如果原始 CMD 中引用了 ${VAR}，但 VAR 在 vars 中为空字符串
	// 说明该变量依赖函数调用（如 UI_PORT=$(get_uiport)），应该回退到 bash 脚本执行
	// 让 bash 执行函数获取真实值
	varRefPattern := regexp.MustCompile(`\$\{([a-zA-Z_][a-zA-Z0-9_]*)\}|\$([a-zA-Z_][a-zA-Z0-9_]*)`)
	for _, match := range varRefPattern.FindAllStringSubmatch(originalCmdValue, -1) {
		varName := match[1]
		if varName == "" {
			varName = match[2]
		}
		if v, ok := vars[varName]; ok && v == "" {
			logger.Debug("变量 %s 在脚本中为空（可能依赖函数调用如 $(get_uiport)），回退到 bash 脚本执行", varName)
			return "", nil, nil, nil, false
		}
	}

	// 8. 切分多进程命令
	// CMD 里用 " & " 拼接的可能是 shell 控制结构而非独立进程
	// （如 fnchat：`python fnchat.py & for i in ...; do nc -z ...; done; gateway-proxy ...`），
	// 因此使用引号感知 + 关键字过滤的切分器，并把轮询循环内的真实命令提取出来。
	cmdEnv = map[string]string{}
	processes := splitShellCommands(cmdValue)

	firstFound := false
	for _, proc := range processes {
		proc = strings.TrimSpace(proc)
		if proc == "" {
			continue
		}
		// 去除 sudo 前缀（在非 root 环境下直接运行）
		proc = strings.TrimPrefix(proc, "sudo ")
		proc = strings.TrimSpace(proc)

		// 提取前缀环境变量赋值（如 PORT=3388 node ... → env PORT=3388, cmd=node ...）
		proc, procEnv := extractPrefixEnv(proc)
		for k, v := range procEnv {
			cmdEnv[k] = v
		}

		parts := parseShellArgs(proc)
		if len(parts) == 0 {
			continue
		}
		// 再次防御：首 token 是 shell 关键字时不是可启动进程
		if isShellKeyword(parts[0]) {
			continue
		}

		if !firstFound {
			command = parts[0]
			args = parts[1:]
			firstFound = true
		} else {
			extras = append(extras, ExtraCommand{
				Command: parts[0],
				Args:    parts[1:],
			})
		}
	}

	if command == "" {
		return "", nil, nil, nil, false
	}

	// 8.1 兜底：确保负责创建 Unix Socket 的 gateway-proxy 一定被启动。
	// 统一网关应用的 socket 由 gateway-proxy 创建，一旦漏掉，应用启动后
	// 网关会报 "Socket 不存在（应用可能未启动）"。
	for _, gw := range extractSocketProxyCommands(cmdValue) {
		dup := false
		for _, ec := range extras {
			if ec.Command == gw.Command && strings.Join(ec.Args, " ") == strings.Join(gw.Args, " ") {
				dup = true
				break
			}
		}
		if !dup && !(command == gw.Command && strings.Join(args, " ") == strings.Join(gw.Args, " ")) {
			extras = append(extras, gw)
			logger.Debug("补充提取网关 socket 代理进程: %s %s", gw.Command, strings.Join(gw.Args, " "))
		}
	}

	return command, args, extras, cmdEnv, true
}

// isCommandInPath 检查命令是否在 PATH 中
func isCommandInPath(command string, env EnvMap) bool {
	// 如果是绝对路径，不需要检查 PATH
	if filepath.IsAbs(command) {
		return fileExists(command)
	}

	// 获取 PATH 环境变量
	path := ""
	if v, ok := env["PATH"]; ok {
		path = v
	} else {
		path = os.Getenv("PATH")
	}

	// 检查 PATH 中的每个目录
	for _, dir := range strings.Split(path, string(os.PathListSeparator)) {
		if dir == "" {
			continue
		}
		fullPath := filepath.Join(dir, command)
		if fileExists(fullPath) {
			return true
		}
	}

	return false
}
