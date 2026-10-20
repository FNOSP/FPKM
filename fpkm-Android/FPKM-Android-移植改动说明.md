# FPKM Android 版：相对官方上游的改动说明

本文记录把官方 FPKM 移植为 Android 应用（APK）过程中，对上游代码所做的全部改动、新增内容与配套文件，供后续维护与升级上游时对照。

- 第 3～6 章：按模块说明改动思路。
- **第 9 章：逐文件、逐处的代码明细（含「改动前 / 改动后」代码块）**，即当前正在维护的详细清单。

## 0. 上游基线与对比方法

| 项目 | 内容 |
| --- | --- |
| 上游仓库 | <https://github.com/FNOSP/FPKM> |
| 对比基线 | commit `8747f24`（docs: README 补充 docker run / docker compose 部署，新增应用兼容性章节） |
| 本地工程 | `fpkm\FPKM-main`（上游源码） |
| Android 移植层 | `fpkm\android-app`、`fpkm\build-apk.sh`（上游没有，全部为本项目新增） |
| 对比方法 | `git clone --depth 1` 拉取上游副本到 `tmp\FPKM-upstream` 后 `diff -rq` / `diff -u`（排除 `.git` / `node_modules` / 构建产物 `dist`） |
| 版本口径 | APK `versionName=1.0.1`、`versionCode=10001`，内置 fpkm 二进制的 `cmd.version` 同样注入 `1.0.1`，与上游保持一致，不自行递增 |
| 本轮对比时间 | 2026-10-20 |

## 1. 为什么必须改动（Android 沙箱限制）

上游 FPKM 面向 fnOS / Linux 服务器环境，直接搬到 Android 上会遇到四类硬限制：

1. **DNS 不可读**：Android 沙箱内不存在 `/etc/resolv.conf`，Go 的纯解析器会退化为查询 `127.0.0.1:53` / `[::1]:53`，报 `connection refused`，直接表现为「应用源加载失败」。
2. **可执行文件不能运行**：`/data` 分区以 `nosuid`/`noexec` 方式挂载，放在应用私有目录 `files/` 下的 ELF 执行时报 `error=13 Permission denied`；只有随 APK 安装到 `lib/<abi>/` 的 `lib*.so` 才能被 exec。
3. **目录不可写**：Android 下 `HOME` 常指向 `/sdcard` 且不可写，而上游默认把数据放在「可执行文件所在目录/data」，Android 的 native 库目录是只读挂载；两者都会导致 `mkdir` 失败、程序起不来。
4. **架构不匹配**：目标机是 arm64，而默认应用源索引里同一应用常同时存在 x86 与 arm 两条目，上游「先出现的优先」去重会让 arm64 设备大量落到 x86 条目上。

## 2. 改动总览

| 分类 | 文件 | 类型 | 说明 |
| --- | --- | --- | --- |
| 数据目录 | `internal/config/config.go` | 修改 | 新增 `FPKM_DATA_DIR` 环境变量优先级，数据目录可被壳应用重定向到应用私有目录 |
| 网络适配 | `fpkm_dns_override.go` | 新增 | Android 网络适配入口，`init()` 调用 `netadapt.InstallFromEnv()` |
| 网络适配 | `internal/netadapt/netadapt.go` | 新增 | DNS 覆盖、host 映射、默认 Transport 注入、启动自检 |
| 网络适配 | `internal/netadapt/netadapt_test.go` | 新增 | 覆盖「域名无法解析时靠 host 映射连通」「注入 DNS 后解析生效」两条关键路径 |
| 商店逻辑 | `internal/web/catalog.go` | 修改 | 多架构条目去重改为按当前运行架构匹配度优先（`archMatchScore`），并抽出可单测的 `archMatchScoreFor` |
| 商店接口 | `internal/web/handler_store.go` | 修改 | `/api/store/sources` 增加 `Cache-Control: no-store` 与耗时日志；新增 `archUsable()` 架构可用谓词，商店列表按架构隐藏条目并返回 `hidden` 计数 |
| 商店详情 | `internal/web/handler_detail.go` | 修改 | 详情接口返回 `platformSupported` / `unsupportedReason` / `platformWarning`，与列表**同一口径**（复用 `archUsable`） |
| 平台拆分 | `internal/web/handler_stats.go`（改）、`handler_stats_disk_linux.go` / `handler_stats_disk_windows.go`（新增） | 修改 / 新增 | 把 Linux 专属的 `readDiskStats()` 拆到按 `//go:build` 区分的文件，让 Windows 开发机能编译并测试 `internal/web` |
| 回归测试 | `internal/web/arch_visibility_test.go` | 新增 | `archUsable` 契约测试（12 用例）与「去重 + 过滤」组合测试 |
| 应用启动 | `internal/app/shell.go` | 新增 | shell 解释器解析：bash 优先，缺失时回退 sh，两者皆无保底 bash |
| 应用启动 | `internal/app/entry.go`、`internal/app/manager.go` | 修改 | 四处写死 `bash` 的解释器调用改用 `shellPath()` |
| 应用启动 | `internal/app/shell_test.go`、`internal/app/shell_entry_test.go` | 新增 | 回退链路回归测试（bash→sh→保底、探测结果使用解析后的解释器） |
| 前端 | `web/src/api/client.ts` | 修改 | 所有 API 请求加 15 秒超时兜底，避免 WebView 下 `fetch` 永不返回 |
| 前端 | `web/src/components/Settings.tsx` | 修改 | 应用源加载失败时显示明确错误提示，不再无限停留在「正在加载…」 |
| 前端 | `web/src/api/types.ts` | 修改 | `AppDetail` 增加 `platformSupported` / `unsupportedReason` / `platformWarning` 字段 |
| 前端 | `web/src/components/AppDetailPage.tsx` | 修改 | 安装按钮禁用条件改为**服务端结论**，并在不可安装时给出「不支持 xxx」标签与原因提示 |
| 前端 | `web/build-esbuild.mjs` | 新增 | 低内存前端构建脚本（esbuild 替代 `vite build`），产物形态与 vite 一致 |
| 前端 | `web/package-lock.json` | 修改 | `npm install` 产生的 `libc` 元数据差异，无功能影响 |
| Android 移植层 | `android-app/`（Manifest、MainActivity、res、assets、lib） | 新增 | 壳应用工程，负责以 native 库形式释放并启动 FPKM、注入运行环境；`targetSdkVersion` 固定为 **28** |
| Android 移植层 | `build-apk.sh` | 新增 | 手工打包脚本（aapt2 / javac / d8 / zipalign / apksigner），不依赖 Gradle |
| Android 移植层 | `fpkm-debug.keystore` | 新增 | 调试签名密钥库 |

## 3. 源码改动详解（Go 侧）

### 3.1 数据目录重定向 —— `internal/config/config.go`

`resolveBaseDir()` 的优先级改为：

1. 环境变量 `FPKM_DATA_DIR`（显式指定，壳应用注入 `/data/data/com.fnos.fpkm/files`），并先用 `dirUsable()` 校验可用；
2. 「可执行文件所在目录/data」（上游默认行为，不变）；
3. 回退 `$HOME/.local/fpk`（上游回退逻辑，不变）。

同时把原注释按新优先级重写，说明 Android 的 native 库目录只读、必须显式指定数据目录的原因。未注入该变量时行为与上游完全一致。

### 3.2 Android 网络适配层 —— 新增 `fpkm_dns_override.go` + `internal/netadapt/`

`fpkm_dns_override.go` 是移植入口，通过 `init()` 调用 `netadapt.InstallFromEnv()`；未注入环境变量时不做任何改动，保持上游默认行为。

`internal/netadapt` 读取壳应用注入的四个变量：

| 环境变量 | 作用 |
| --- | --- |
| `FPKM_DNS` | 逗号分隔的 DNS 服务器列表（可写 `host:port`，默认 53），由壳应用从 Android 系统网络配置中探测得到 |
| `FPKM_HOSTS` | `host=ip` 映射（多条用 `;` 或换行分隔），来自 Java 侧系统解析器，命中时直接连 IP，是最强兜底 |
| `FPKM_PROBE_HOST` | 启动自检域名 |
| `FPKM_SELFTEST_URL` | 启动自检 URL |

实现要点：`InstallHosts` 安装 host 映射并在 Dial 阶段改写目标地址；`InstallDNS` + `ServerUsable` / `PickServer` 从候选 DNS 中挑出真实可用者；`InstallDefaultTransport` 注入自定义 `DialContext`；`SelfTest` 在后台异步跑一次解析与请求并把结果写日志。日志统一加 `[fpkm-net]` 前缀输出到 stderr（壳应用会把它落到日志文件）。

### 3.3 多架构条目去重修正 —— `internal/web/catalog.go`

原逻辑用 `seen map[string]bool` 做「先出现的优先」去重。现改为 `kept map[string]int`（key → 在结果切片中的下标），并为每个条目计算架构匹配度：

- `archMatchScore(platform string) int` —— 2=匹配当前架构，1=中性（all/空/未知），0=其他架构；
- `archMatchScoreFor(platform, arch string) int` —— 同口径，但架构可显式指定，便于单测（本轮为写回归测试而抽出）。

同一 key 再次出现时，若新条目得分高于已保留条目，则原地替换。口径与 `platformSupported` 保持一致（`arm64` 认 `arm`，`amd64` 认 `x86`/`amd64`/`x64`）。

> 注意：去重只解决「同一应用保留哪一条」，不解决「标签错标」问题——若某应用**只有** x86 条目，去重后仍会剩下 x86。因此第 9.2 节在列表处又补了一层 `archUsable` 过滤。

### 3.4 应用源接口诊断与缓存控制 —— `internal/web/handler_store.go`

`handleSources` 的 GET 分支新增：

- `w.Header().Set("Cache-Control", "no-store")`：避免 WebView / 中间层缓存干扰；
- `logger.Debug("GET /api/store/sources done in %s, sources=%d", ...)`：记录处理耗时与源数量，便于真机日志里直接判断「请求是否到达后端、后端多久返回」。

### 3.5 启动脚本解释器回退 —— 新增 `internal/app/shell.go`，改 `entry.go`、`manager.go`

上游把执行 FPK 应用脚本的解释器写死为 `bash`。Android 系统只带 POSIX shell（`/system/bin/sh`，mksh），没有 bash，于是商店里启动应用会直接失败：

```
启动失败: 启动脚本失败: exec "bash": executable file not found in $PATH
```

新增 `shellPath()`（内部为可单测的 `resolveShell(look)`）：先查 `bash`，找不到则查 `sh`，两者都没有时保底返回 `"bash"`（保持上游报错语义）。四处硬编码调用全部改为使用它：

| 位置 | 原写法 | 现写法 |
| --- | --- | --- |
| `internal/app/entry.go` 分发脚本回退 | `Command: "bash"` | `Command: shellPath()` |
| `internal/app/manager.go` 通用脚本执行 | `exec.Command("bash", script)` | `exec.Command(shellPath(), script)` |
| `internal/app/manager.go` 停止脚本 | `exec.Command("bash", cmdMain, "stop")` | `exec.Command(sh, cmdMain, "stop")`（日志同步打印实际解释器） |
| `internal/app/manager.go` 安装回调 | `exec.Command("bash", installCallback)` | `exec.Command(shellPath(), installCallback)` |

## 4. 前端改动（`web/src`）

### 4.1 请求超时兜底 —— `web/src/api/client.ts`

新增常量 `REQUEST_TIMEOUT_MS = 15000`，`request()` 内部用 `AbortController` + `setTimeout` 构造超时信号，并把 `init.signal` 与超时信号串联；`finally` 中清理定时器。任何情况下 `fetch` 都会在 15 秒内结束，调用方的 `loading` 状态随之落地，不再出现「永远正在加载」。

### 4.2 加载失败明确提示 —— `web/src/components/Settings.tsx`

新增 `loadError` 状态：`refresh()` 失败时清空源列表并记录错误；`SourcesSection` 收到 `loadError` 后把顶部 Banner 换成 `danger` 类型并直接显示错误原因，空态标题由「正在加载…」变为「应用源加载失败」，描述提示重试方式。原先 `catch {}` 静默吞掉异常的行为被移除。

### 4.3 详情页安装按钮与列表同口径 —— `web/src/components/AppDetailPage.tsx`、`web/src/api/types.ts`

见第 9.1 节。要点：删除写死的 `disabled={busy || detail.platform === 'arm'}`，改为由服务端给出的 `platformSupported` 决定；不可安装时显示「不支持 xxx」标签，鼠标悬停展示 `unsupportedReason`。

## 5. Android 移植层（上游没有，全部新增）

### 5.1 壳应用工程 `android-app/`

- **Manifest**：包名 `com.fnos.fpkm`，`versionCode=10001`、`versionName=1.0.1`，`minSdk 24` / **`targetSdk 28`**（必须 ≤28，见 9.3 节），`extractNativeLibs="true"`、`usesCleartextTraffic="true"`，申请 `INTERNET` 与 `ACCESS_NETWORK_STATE`。
- **二进制内置方式**：FPKM 交叉编译产物以 `lib/arm64-v8a/libfpkm.so` 打进 APK，运行时由 Java 从 `ApplicationInfo.nativeLibraryDir` 取得真实路径再启动（绕开 `/data` 的 noexec 限制；直接放 `files/` 会报 `error=13 Permission denied`）。
- **`MainActivity.java`**：以 `ProcessBuilder` 启动 `fpkm web --bind 0.0.0.0 --port 7666`，工作目录设为应用私有数据目录，并注入 `FPKM_DATA_DIR`、`FPKM_DNS`、`FPKM_HOSTS`、`FPKM_PROBE_HOST`、`FPKM_SELFTEST_URL`、`HOME`、`XDG_DATA_HOME`、`XDG_CONFIG_HOME`、`TMPDIR`；stdout/stderr 合并追加到日志文件。绑定 `0.0.0.0` 使同局域网电脑也能打开管理页面（管理密码仍然生效）。
- **shell 兼容层**：启动服务前在应用私有目录创建 `files/bin/bash` 符号链接指向 `/system/bin/sh`，并把该目录插到子进程 `PATH` 最前面。FPKM 自身已按可用性选择解释器（3.5 节），但部分 FPK 应用的 `cmd/main` 脚本内部仍会再调用 `bash`，这一层让这类调用也能命中可用的 shell。用符号链接而非脚本副本，是因为执行时按最终目标文件（`/system/bin/sh`）判定权限，可绕开应用数据目录不可执行的限制；创建失败时静默忽略，不影响启动。
- **资源**：`res/` 提供自适应图标与主题，`res/values/strings.xml` 提供应用名；`assets/fpkm` 另留一份二进制副本，运行时并不使用（启动路径只取 `nativeLibraryDir/libfpkm.so`），仅备查。

### 5.2 打包脚本 `build-apk.sh`

八个步骤：准备 `lib/arm64-v8a/libfpkm.so` → `aapt2 compile` → `aapt2 link` → `javac` → `d8` 转 dex → Python 组装 APK（写入 `classes.dex` 与 `libfpkm.so`，并设置 `.so` 的 0755 权限）→ `zipalign` + `apksigner` → 验证签名。工具链取自 `tools`（JDK 17、Android SDK build-tools 35.0.0），不依赖 Gradle。

## 6. 构建与打包口径（可复现）

```bash
# 1) 前端构建（低内存：用 esbuild 脚本替代 vite build；输出到 embed 目录 internal/web/dist）
cd E:/ltby/fpkm/FPKM-main/web && npm run typecheck && node build-esbuild.mjs

# 2) 交叉编译 ARM64 二进制（内存紧张时必须用低并行度）
export PATH="/e/ltby/tools/go/bin:$PATH"
cd E:/ltby/fpkm/FPKM-main
GOFLAGS="-p=1" GOGC=40 GOMAXPROCS=2 CGO_ENABLED=0 GOOS=android GOARCH=arm64 \
  go build -gcflags=all=-c=1 -trimpath \
  -ldflags "-s -w -X fpkm/cmd.version=1.0.1" -o dist/fpkm-android-arm64 .

# 3) 打包 APK
cd E:/ltby/fpkm && bash build-apk.sh
# 产物：E:/ltby/fpkm/dist-android/fpkm.apk
```

注意事项：

- 构建机物理内存 8GB 且无分页文件，commit 空间常仅剩几百 MB。Go 编译需 `-p=1` / `-gcflags=all=-c=1`（构建前先 `go clean -cache` 释放缓存），前端改用 `web/build-esbuild.mjs`（esbuild 是 Go 程序，内存占用远低于 vite/Rollup）。
- 前端产物直接写入 `internal/web/dist`（由 Go `embed` 打包进二进制），因此**必须先构建前端再编译 Go**，否则内嵌的是旧前端。

## 7. 已有验证记录

| 项目 | 证据 |
| --- | --- |
| 网络适配层单测 | `go test ./internal/netadapt/` → `ok fpkm/internal/netadapt` |
| `internal/web` 全量单测 | `go test ./internal/web/` 全 PASS（含新增 `arch_visibility_test.go`） |
| 前端类型检查 | `tsc --noEmit` 无报错 |
| 前端低内存构建 | `node build-esbuild.mjs` 约 0.7s 成功，产出 `internal/web/dist/assets/index-722d02bf.js`（860 KB）与 `index-78908fef.css`（730 KB） |
| 架构去重 + 过滤（真实源索引模拟） | 用真实源索引（342 条）在 amd64 上模拟：唯一应用 211 个，架构分布由 `x86:202 / arm:9` 变为 `x86:77 / arm:134`；131 个双架构应用全部保留 arm 条目，0 误留 |
| 商店过滤端到端校验 | `tmp\e2e\verify_store_filter.py`（amd64 上验证 x86/arm 过滤、去重、详情 `platformSupported` / `reason`）全 PASS |
| **UI 渲染校验（真实浏览器 + 模拟接口）** | Edge headless + CDP 加载 esbuild 产物，走完「桌面 → 应用商店 → 详情」链路：商店列表 3 张卡片渲染正常；arm64 设备上的 `arm` 应用「安装」按钮**可用**、无「不支持」标签；`x86` 应用「安装」按钮**置灰**并显示「不支持 x86」标签。断言报告 `ui-report.json` 的 `assert_ok=true`；控制台仅 8 条 `/api/icons/*` 404（mock 未实现该接口，触发图标首字母兜底，属预期）。截图 `shot-1-store-list.png` / `shot-2-detail-supported.png` / `shot-3-detail-unsupported.png` |
| 解释器回退单测 | `go test ./internal/app/ -run 'TestResolveShell\|TestDetectEntryFallbackUsesResolvableShell\|TestShellPathFallsBackToBashWhenNothingFound'` → 全部 PASS |
| APK 重建静态校验 | `versionName=1.0.1`、`versionCode=10001`、`minSdkVersion=24` / `targetSdkVersion=28`；`lib/arm64-v8a/libfpkm.so` 与编译产物 MD5 一致；`classes.dex` 存在且含 `prepareShellShim` / `symlink`（shell 兼容层）+ `archUsable`（新过滤逻辑）字符串；签名方案 v2 / v3 验证通过 |
| 上游一致性对照 | `internal/app` 的 `xfs_test.go` 在 Windows 上失败（symlink/文件占用语义差异），在**未改动的上游副本**上以完全相同的用例复现同样失败，确认与本次改动无关 |

## 8. 已修复问题记录（简述）

### 8.1 FPK 应用启动失败（`exec "bash": executable file not found in $PATH`）

- **现象**：商店里启动应用（如 `fnos-quark`）报 `启动失败: 启动脚本失败: exec "bash": executable file not found in $PATH`，耗时 0.0 秒。
- **根因**：`cmd/main` 属于 start/stop/status 分发脚本且无法提取出可直接启动的命令时，上游回退为 `bash <cmd/main> start`，解释器写死 `bash`；Android 只有 `/system/bin/sh`。
- **修复**：3.5 节的四处解释器回退 + 5.1 节的 `bash → sh` 符号链接兼容层。

### 8.2 详情页「安装」按钮置灰但列表可安装 —— 见 9.1
### 8.3 arm64 设备仍显示 x86 应用 —— 见 9.2
### 8.4 FPK 应用（如 Terminal）启动报「没有执行权限」—— 见 9.3
### 8.5 开发机（Windows）无法编译 `internal/web` —— 见 9.4

## 9. 本轮修复：逐文件代码明细

> 本章按「问题 → 涉及文件 → 改动前 / 改动后代码」给出具体代码，方便逐处对照与后续升级上游时冲突定位。

### 9.1 详情页「安装」按钮置灰，与列表页不一致

**现象**：商店列表里能安装的 arm 应用，点进详情页后「安装」按钮是灰的、点不动。

**根因**：上游详情页把禁用条件写死为 `detail.platform === 'arm'`。这是为 x86 NAS 设想的老规则——在 arm64 手机上它会把**所有** `platform=arm` 的条目（恰好是最该安装的那批）判为不可安装。

**修复**：禁用条件改由服务端按「本地包实测优先、其次 platform 标签」给出的 `platformSupported` 决定，与商店列表**完全同一口径**（复用 `archUsable`）。

#### 9.1.1 文件：`web/src/components/AppDetailPage.tsx`

新增导入 `Tooltip`：

```diff
   Modal,
   Dropdown,
   SplitButtonGroup,
+  Tooltip,
 } from '@douyinfe/semi-ui';
```

未安装分支的按钮（改动前）：

```tsx
    if (!detail.installed) {
      return (
        <Button
          size="small"
          theme="light"
          type="primary"
          icon={<IconDownloadStroked />}
          disabled={busy || detail.platform === 'arm'}   // ← 罪魁：arm64 上把所有 arm 应用置灰
          onClick={() => detail.storeId && onInstall(detail.storeId)}
        >
          安装
        </Button>
      );
    }
```

改动后：

```tsx
    if (!detail.installed) {
      const unsupported = detail.platformSupported === false;   // ← 服务端结论
      return (
        <Space spacing={8} wrap>
          <Tooltip content={detail.unsupportedReason || ''} position="top">
            <Button
              size="small"
              theme="light"
              type="primary"
              icon={<IconDownloadStroked />}
              disabled={busy || unsupported}
              onClick={() => detail.storeId && onInstall(detail.storeId)}
            >
              安装
            </Button>
          </Tooltip>
          {unsupported ? (
            <Tag size="small" color="grey">
              不支持 {detail.platform}
            </Tag>
          ) : detail.platformWarning ? (
            <Tooltip content={detail.platformWarning}>
              <Tag size="small" color="orange">
                标注 {detail.platform}
              </Tag>
            </Tooltip>
          ) : null}
        </Space>
      );
    }
```

#### 9.1.2 文件：`web/src/api/types.ts`（`AppDetail` 接口）

```diff
   storeId?: string;
+
+  /**
+   * 架构判定，与商店列表页同一口径：
+   * false 表示当前机器确定无法运行，安装按钮应置灰。
+   */
+  platformSupported?: boolean;
+  /** 不可运行的原因（为空表示可运行） */
+  unsupportedReason?: string;
+  /** 软提示：平台标签可能不匹配，但不阻止安装 */
+  platformWarning?: string;
 }
```

#### 9.1.3 文件：`internal/web/handler_detail.go`

Struct 新增三个字段（`import` 增加 `fmt`）：

```diff
 import (
+	"fmt"
 	"net/http"
 	"strings"
```

```diff
 	// ID 商店条目的编号（用于安装）；已安装应用为空
 	StoreID string `json:"storeId,omitempty"`
+
+	// 架构判定：与商店列表页使用**同一口径**（archFilter），
+	// 保证"列表能装、详情页却置灰"这类不一致不再出现。
+	// PlatformSupported 当前机器能否运行
+	PlatformSupported bool `json:"platformSupported"`
+	// UnsupportedReason 确定不可运行的原因（为空表示可运行）
+	UnsupportedReason string `json:"unsupportedReason,omitempty"`
+	// PlatformWarning 软提示：平台标签看起来不匹配，但未核实，不阻止安装
+	PlatformWarning string `json:"platformWarning,omitempty"`
 }
```

已安装应用分支（`detailForInstalled`）追加一致性标记：

```diff
 	detail := AppDetail{
 		...
 		InstallPath:      s.cfg.AppDir(name),
 	}
+
+	// 已安装应用不会渲染"安装"按钮，这里只按 manifest 标签给出一致性标记，
+	// 供详情页展示（不用于禁用任何操作）。
+	detail.PlatformSupported = platformSupported(manifest.Platform, archName())
```

商店条目分支（`detailForStore`）追加判定（与列表共用 `archUsable`，未打包/非本地条目也能得到正确结论）：

```diff
 	}
 
+	// 架构判定：复用商店列表的 archFilter（含"本地包实测优先"规则），
+	// 并且同样把"标签明确指向其他架构且无本地证据"的条目判为不可安装 ——
+	// 这类条目已经不会出现在列表里，直接打开详情链接时也不能装。
+	verdict := newArchFilter(s.scanDownloadedPackages()).check(a)
+	detail.PlatformSupported = archUsable(a, verdict, archName())
+	detail.PlatformWarning = verdict.warning
+	if !detail.PlatformSupported {
+		if verdict.reason != "" {
+			detail.UnsupportedReason = verdict.reason
+		} else {
+			detail.UnsupportedReason = fmt.Sprintf(
+				"应用源标注为 %s 架构，当前机器为 %s，无法运行", strings.TrimSpace(a.Platform), archName())
+		}
+	}
+
 	// 合并本地已安装状态
 	if manifest, err := fpk.ParseManifest(s.cfg.AppManifestFile(appKey)); err == nil {
```

### 9.2 arm64 设备仍然显示 x86 应用

**现象**：检测到 arm64，但商店列表里仍有 `platform=x86` 的条目（点进去装上也跑不起来）。

**根因**：3.3 节的去重只解决「同一应用保留哪一条」，对**只有 x86 条目**的应用无能为力；列表侧上游没有任何按架构「隐藏」的逻辑。

**修复**：新增单一谓词 `archUsable()`，列表（`handleStoreApps`）与详情（`detailForStore`）共用；不匹配且无本地包实测证据的条目从列表剔除，并计入响应的 `hidden` 字段。

**判定口径**：`archUsable = verdict.ok && (platformSupported(platform, arch) || verdict.proven)`
- `platformSupported`：标签与当前架构匹配（含 `all`/空）；
- `verdict.proven`：结论来自对**本地已下载安装包**的真实探测（纯脚本应用即便标签写 `x86`，实测也是通用的，允许放行）。

#### 9.2.1 文件：`internal/web/handler_store.go`

新增 `archUsable()` 谓词：

```go
// archUsable 判断商店条目在当前架构下"装得起来、跑得动"。
//
// 商店列表的展示过滤与详情页的安装按钮状态共用同一结论，
// 避免出现"列表能装、详情页却灰着点不动"这类不一致：
//   - platform 标签与当前架构匹配（含 all / 空）→ 可用；
//   - 标签不匹配，但**本地已下载的包**实测证明可运行（纯脚本等）→ 可用；
//   - 本地包实测证明含其他架构二进制（verdict.ok=false）→ 不可用；
//   - 标签明确指向其他架构、又没有本地包可实测 → 不可用
//     （装上也跑不起来，列表直接不展示）。
func archUsable(a RemoteApp, verdict archVerdict, arch string) bool {
	return verdict.ok && (platformSupported(a.Platform, arch) || verdict.proven)
}
```

`archVerdict` 结构新增 `proven` 字段，并在 `check()` 的各分支标注是否来自本地实测：

```diff
 	ok      bool
 	warning string
 	reason  string
+	// proven 表示该结论来自对**本地已下载安装包**的真实探测，
+	// 而不是依据应用源的 platform 标签推断。
+	// 只有 proven 的结果才允许"顶着标签不匹配也照常展示/安装"。
+	proven bool
 }
```

```diff
 		case report.ScriptOnly:
 			// 纯脚本/前端：与架构无关，任何机器都能装
-			verdict = archVerdict{ok: true}
+			verdict = archVerdict{ok: true, proven: true}
 		case report.UnsupportedReason != "":
 			// 确有二进制且不匹配 → 硬拦截
-			verdict = archVerdict{ok: false, reason: formatArchReason(report, a)}
+			verdict = archVerdict{ok: false, reason: formatArchReason(report, a), proven: true}
 		default:
-			verdict = archVerdict{ok: true}
+			verdict = archVerdict{ok: true, proven: true}
 		}
```

列表 `handleStoreApps` 增加过滤与 `hidden` 计数：

```diff
 	out := make([]StoreApp, 0, len(apps))
+	hidden := 0
 	for _, a := range apps {
 		...
 		verdict := filter.check(a)
+
+		// 只展示与当前机器架构匹配的条目。
+		if !archUsable(a, verdict, arch) {
+			hidden++
+			logger.Debug("按架构过滤隐藏商店条目: %s (platform=%s, 当前架构=%s)",
+				a.ID, strings.TrimSpace(a.Platform), arch)
+			continue
+		}
+
 		v.PlatformSupported = verdict.ok
```

```diff
+	if hidden > 0 {
+		logger.Debug("商店列表按架构过滤：保留 %d 个，隐藏 %d 个（当前架构 %s）", len(out), hidden, arch)
+	}
+
 	writeJSON(w, http.StatusOK, map[string]any{
 		"apps":    out,
 		"total":   len(out),
 		"warning": warn,
 		"arch":    arch,
+		"hidden":  hidden,
 	})
```

#### 9.2.2 文件：`internal/web/catalog.go`

去重改为「按架构匹配度替换已保留条目」，并抽出可单测的 `archMatchScoreFor`：

```diff
 	var (
 		all     []RemoteApp
 		errMsgs []string
-		seen    = map[string]bool{}
+		kept    = map[string]int{} // key -> 在 all 中的下标
 	)
```
```diff
-			// 同一应用出现在多个源时，先出现的优先
+			// 同一应用可能出现多架构条目（同源内 x86/arm 各一条），
+			// 优先保留与当前运行架构最匹配的条目，避免 arm64 手机上误留 x86。
 			key := apps[i].ID
 			if key == "" {
 				key = apps[i].Name
 			}
-			if seen[key] {
+			score := archMatchScore(apps[i].Platform)
+			if idx, ok := kept[key]; ok {
+				if score > archMatchScore(all[idx].Platform) {
+					all[idx] = apps[i]
+				}
 				continue
 			}
-			seen[key] = true
+			kept[key] = len(all)
 			all = append(all, apps[i])
```

```go
func archMatchScore(platform string) int {
	return archMatchScoreFor(platform, goArch())
}

// archMatchScoreFor 与 archMatchScore 同口径，但架构可显式指定（便于单测）。
func archMatchScoreFor(platform, arch string) int {
	p := strings.ToLower(strings.TrimSpace(platform))
	if p == "" || p == "all" {
		return 1
	}
	switch arch {
	case "amd64":
		if strings.Contains(p, "x86") || strings.Contains(p, "amd64") || strings.Contains(p, "x64") {
			return 2
		}
	case "arm64":
		if strings.Contains(p, "arm") {
			return 2
		}
	}
	return 0
}
```

#### 9.2.3 文件：`internal/web/arch_visibility_test.go`（新增）

把 `archUsable` 当契约测试：12 个表驱动用例覆盖「arm64/amd64 × 各标签 × 本地实测结论」，另有 1 个组合用例验证「先去重、再过滤」后 arm64 设备只保留并展示 arm 条目。核心用例：

```go
{"arm64 设备 + arm 标签", "arm", archVerdict{ok: true}, "arm64", true},
{"arm64 设备 + x86 标签 → 隐藏", "x86", archVerdict{ok: true, warning: "可能不兼容"}, "arm64", false},
{"arm64 设备 + 本地包实测纯脚本 → 仍显示", "x86", archVerdict{ok: true, proven: true}, "arm64", true},
{"arm64 设备 + 本地包实测架构不符 → 隐藏", "arm", archVerdict{ok: false, reason: "仅含 x86_64 二进制", proven: true}, "arm64", false},
{"amd64 设备 + arm 标签 → 隐藏", "arm", archVerdict{ok: true, warning: "可能不兼容"}, "amd64", false},
```

### 9.3 FPK 应用（如 Terminal）启动报「没有执行权限」

**现象**：点 Terminal 的应用商店条目启动，报无执行权限（`permission denied`）——与 8.1 的 `bash not found` 是**两个不同问题**。

**根因**：壳 APK 原 `targetSdkVersion=35`。Android 10（API 29）起，`targetSdk≥29` 的应用被 SELinux 禁止 `execve()` 自己数据目录下的文件（属 `untrusted_app_29+` 域限制，`chmod +x` 无法绕过）。而 FPK 应用（如 Terminal）会把自带二进制解包到 `files/fpkm-data/apps/<App>/target/bin/` 再启动，在该限制下必然失败。

**修复**：把壳 APK 的 `targetSdkVersion` 降到 `28`，进程落在 `untrusted_app_27` 域，重新允许执行应用数据目录内的可执行文件（与 Termux 的长期做法一致）。

#### 9.3.1 文件：`android-app/AndroidManifest.xml`

```diff
+    <!--
+      targetSdkVersion 必须保持 <= 28：
+      Android 10(API 29) 起，targetSdk 29+ 的应用被禁止 execve() 自己数据目录
+      (/data/user/0/<pkg>/files/...) 下的文件 —— 这是 SELinux 层面的限制，
+      chmod +x 无法绕过。而 FPK 应用（如 Terminal）是把自带二进制解包到
+      files/fpkm-data/apps/<App>/target/bin/ 后再启动的，targetSdk 35 下必然
+      报 "启动失败：文件没有执行权限"。
+      targetSdk<=28 时进程落在 untrusted_app_27 域，仍允许执行应用数据目录内的
+      可执行文件（Termux 等终端类应用长期采用同一方案）。
+    -->
-    <uses-sdk android:minSdkVersion="24" android:targetSdkVersion="35" />
+    <uses-sdk android:minSdkVersion="24" android:targetSdkVersion="28" />
```

> 影响面评估：降 `targetSdk` 主要影响系统对权限、后台、存储等的分区（scoped storage）行为；本应用只在私有目录运行服务并通过局域网访问，不依赖新版 targetSdk 才有的运行时行为，因此降到 28 无功能损失。若后续要上架 Google Play（要求较高 targetSdk），需改用「不 exec 数据目录、改走 native 库目录」的方案替代。

### 9.4 开发机（Windows）无法编译 `internal/web`（复编译支撑）

**现象**：在 Windows 上 `go test ./internal/web/` 编译失败，报 `undefined: syscall.Statfs_t`。

**根因**：`handler_stats.go` 里的 `readDiskStats()` 依赖 Linux 的 `/proc/mounts` 与 `syscall.Statfs`，Windows 无对应符号。

**修复**：把 `readDiskStats()` 连同 `syscall` 依赖拆到按 `//go:build` 区分的两个文件，`handler_stats.go` 只保留与平台无关的逻辑。这样 Windows 开发机能编译并运行 `internal/web` 的单测（FPKM 实际只发布 Linux / Android 版本，Windows 实现仅为开发期）。

#### 9.4.1 文件：`internal/web/handler_stats.go`

```diff
 import (
 	"bufio"
 	"os"
 	"strconv"
 	"strings"
-	"syscall"
 	"time"
 )
```
（并删除文件内 68 行的 `readDiskStats()` 实现，见下方新增文件）

#### 9.4.2 文件：`internal/web/handler_stats_disk_linux.go`（新增）

```go
//go:build !windows

package web

import (
	"bufio"
	"os"
	"strings"
	"syscall"
)

// readDiskStats 读取主要挂载点的磁盘使用情况。
// 只取容量较大的真实挂载点（跳过 tmpfs/overlay 等虚拟文件系统）。
func readDiskStats() []DiskStats {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return nil
	}
	defer f.Close()
	// ...（其余逻辑与上游 readDiskStats 完全一致）
	var st syscall.Statfs_t
	if err := syscall.Statfs(mount, &st); err != nil {
		continue
	}
	// ...
}
```

#### 9.4.3 文件：`internal/web/handler_stats_disk_windows.go`（新增）

```go
//go:build windows

package web

// readDiskStats 在 Windows 上没有 /proc/mounts 与 Statfs，
// 直接返回空列表（FPKM 只发布 Linux / Android 版本，
// 这里保留实现是为了让开发机（Windows）能编译并运行 internal/web 的单元测试）。
func readDiskStats() []DiskStats { return nil }
```

### 9.5 低内存前端构建 —— `web/build-esbuild.mjs`（新增）

**根因**：本机物理内存 8GB 且无分页文件，commit 空间常仅剩几百 MB，而 `vite build`（Rollup + V8）需 >320MB JS 堆，直接以 `memory allocation of xxx bytes failed` / `JavaScript heap out of memory` 失败。esbuild 是 Go 程序，内存占用低得多。

**产物形态**与 vite 保持一致（`internal/web/dist/index.html` + `assets/index-<hash>.js` + `assets/index-<hash>.css` + public 资源）。关键点：

- `charset: 'utf8'`：中文文案不转义成 `\uXXXX`；
- `jsx: 'automatic'`：适配 React 17+ 的新 JSX 运行时；
- `alias` 处理 `@` 与 `@semi-css`；
- 插件把 `/` 开头的绝对路径（public 资源，vite 约定）标记为 external，原样保留；
- 自算 md5 前 8 位作为文件名 hash，并改写 `index.html` 里的 `<script>` / `<link>` 标签。

```js
await build({
  entryPoints: [path.resolve(__dirname, 'src/main.tsx')],
  outdir: tmpDir, bundle: true, minify: true, format: 'esm', target: 'es2020',
  jsx: 'automatic',
  charset: 'utf8',                 // 中文不转义
  legalComments: 'none',
  define: { 'process.env.NODE_ENV': '"production"' },
  alias: { '@': path.resolve(__dirname, 'src'),
           '@semi-css': path.resolve(__dirname, 'node_modules/@douyinfe/semi-ui/dist/css/semi.min.css') },
  loader: { '.png': 'file', '.jpg': 'file', '.jpeg': 'file', '.svg': 'file', '.webp': 'file' },
  plugins: [{ name: 'public-absolute-external', setup(b) {
    b.onResolve({ filter: /^\// }, (args) => ({ path: args.path, external: true }));
  }}],
  entryNames: 'index', assetNames: 'asset-[name]-[hash]', logLevel: 'info',
});
```

## 10. 真机验证要点与已知局限

**真机验证要点（安装 `dist-android/fpkm.apk` 后逐项确认）**：

1. **列表不再出现 x86 条目**：arm64 真机打开应用商店，`platform=x86` 且无本地包证据的条目应全部消失；日志 `[fpkm] 商店列表按架构过滤：保留 X 个，隐藏 Y 个`，接口响应 `hidden` 字段 > 0。
2. **详情页按钮不灰**：arm64 真机上点进 `platform=arm` 的应用详情，若架构匹配，按钮为可用蓝色（应能点击安装）；仅当服务端判定不可运行时才置灰，并显示「不支持 xxx」标签。
3. **Terminal 可启动**：启动 Terminal 类应用，不再报「没有执行权限」；若仍报错，应是具体应用脚本问题（见下方局限），日志会给出脚本名与报错。
4. **不再「正在加载」**：应用源加载失败时 15 秒内落到明确错误提示，而非无限加载。

**已知局限**：

- 架构过滤依赖应用源的 `platform` 标签质量；标签错标且无本地包可实测时，可能被误隐藏或误放行（`platformWarning` 会在详情页给出软提示）。
- 降 `targetSdk` 到 28 是为执行应用数据目录内二进制所必需；若未来需满足 Google Play 的高 targetSdk 要求，需改用把自带二进制放到 native 库目录的方案。
- 若某个 FPK 应用的脚本使用 bash 专有语法（数组、`shopt` 等），在 mksh（`/system/bin/sh`）下仍可能失败；届时日志会显示具体脚本报错，可按应用单独处理（例如为其内置静态 bash）。