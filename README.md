# FPKM · 飞牛应用包管理器

**中文** · [English](README.en.md)

<p align="center">
  <img src="docs/banner.png" alt="FPKM × FNOSP" width="760">
</p>

<p align="center">
  <a href="https://github.com/FNOSP/FPKM/stargazers"><img src="https://img.shields.io/github/stars/FNOSP/FPKM?style=flat-square&logo=github&label=Stars&color=2b6cff" alt="Stars"></a>
  <a href="https://github.com/FNOSP/FPKM/network/members"><img src="https://img.shields.io/github/forks/FNOSP/FPKM?style=flat-square&logo=github&label=Forks&color=2b6cff" alt="Forks"></a>
  <a href="https://github.com/FNOSP/FPKM/issues"><img src="https://img.shields.io/github/issues/FNOSP/FPKM?style=flat-square&logo=github&label=Issues&color=e3b341" alt="Issues"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/FNOSP/FPKM?style=flat-square&label=License&color=e05d44" alt="License"></a>
  <img src="https://img.shields.io/badge/Go-1.24+-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go">
  <img src="https://img.shields.io/badge/React-18-61DAFB?style=flat-square&logo=react&logoColor=black" alt="React">
  <img src="https://img.shields.io/badge/Platform-Linux%20%7C%20Windows%20%7C%20macOS-2b6cff?style=flat-square&logo=linux&logoColor=white" alt="Platform">
</p>

<p align="center">
  <b>让 <code>.fpk</code> 离线安装包跑在任何 Linux 上 —— 不需要 fnOS</b>
</p>

---

FPKM（FeiNiu PacKage Manager）是一个用 Go 编写的 `.fpk` 应用包管理器。
它复刻了 fnOS 的应用安装与运行环境，使飞牛生态里的应用包可以在普通
Linux 发行版、容器、甚至开发机上直接安装和运行。

```
你的 Arch / Ubuntu / Debian / CentOS  … 装的是 .fpk 应用包
```

![FPKM 桌面](docs/screenshots/desktop.webp)


## 为什么需要 FPKM

飞牛 fnOS 的应用以 `.fpk` 包分发，包内是一个完整的运行时约定：
`manifest` 描述元数据、`cmd/main` 管理生命周期、`app.tgz` 存放程序本体，
运行期依赖一整套 `TRIM_*` 环境变量、`/var/apps/{appname}` 之类的固定路径，
以及一个统一的 Unix Socket 网关。

这套约定只存在于 fnOS 上。一旦离开 fnOS：

- 直接解压 `.fpk` 跑不起来（缺环境变量、缺路径、缺网关）
- 大量应用把安装路径、socket 位置硬编码在二进制里
- 网关负责鉴权并注入用户身份头，没有它应用会拒绝服务

**FPKM 的工作就是把这套运行环境完整地在普通 Linux 上重建出来。**

---

## 核心特性

### 完整的 `.fpk` 生命周期

```bash
fpkm install  app.fpk      # 安装
fpkm start    myapp        # 启动
fpkm status   myapp        # 状态
fpkm stop     myapp        # 停止
fpkm upgrade  app.fpk      # 升级（保留数据）
fpkm uninstall myapp       # 卸载
fpkm list                  # 列出已安装
```

### 自动识别六种应用形态

`.fpk` 包内部结构千差万别，FPKM 会自动探测入口类型：

| 类型 | 说明 |
|------|------|
| 原生二进制 | `bin/` 下的 ELF，最常见 |
| `cmd/main` 脚本 | 由脚本管理进程与 PID |
| Node.js | 自动执行 `npm install --omit=dev` 安装依赖 |
| Python | 调用 `python3` 运行脚本（依赖需自行准备） |
| 静态站点 | 内置静态文件服务器 |
| Docker | 检测并提示 Rootless Docker 要求 |

### 兼容飞牛原生路径（关键能力）

许多飞牛应用的二进制里**硬编码**了 fnOS 的路径，无法通过改代码修正。
FPKM 会在安装/启动时自动建立符号链接，把硬编码路径重定向到实际位置：

```
/var/apps/{appname}                       → 安装目录
/vol1/@appdata/{appname}                  → 数据目录
/vol1/@appcenter/{appname}                → 程序目录
```

安装时还会重写 `cmd/main` 等脚本里的硬编码路径，让原版脚本无需修改即可运行。

### 统一网关

复刻飞牛的统一网关：把 `/app/{appname}` 的 HTTP / WebSocket / FastCGI 请求
转发到应用监听的 Unix Socket。

- 内置 Go 实现，**零外部依赖**，不需要 nginx
- **受 FPKM 登录保护**：未登录访问 `/app/*` 会被拦到登录页，
  因此应用不会绕过鉴权被匿名访问
- 请求注入 `X-Trim-Userid` / `X-Trim-Username` / `X-Trim-Isadmin` 身份头
- 支持应用启动前等待 Socket 就绪，并给出可诊断的超时原因

### 数据自包含

所有数据存放在**可执行文件旁的 `data/` 目录**：

```
./fpkm
./data/
├── apps/{应用名}/     应用安装目录
├── password.json      管理密码（PBKDF2 加盐）
├── sources.json       应用源
└── logs/
```

数据跟着程序走，不依赖 `$HOME`。换用户、用 `sudo`、放进容器，
看到的都是同一份数据 —— 不会出现"换个身份启动，应用全消失"。

### 跨平台

Windows / macOS / Linux 均可运行（`fpkm.exe`、`fpkm` 同名命令）。
架构识别覆盖 `amd64` / `arm64`，并能在安装前校验二进制是否与当前平台兼容。

---

## 两种使用方式

### 命令行

```bash
./fpkm install  ~/Downloads/fnos-quark.fpk
./fpkm start    fnos-quark
./fpkm status   fnos-quark
```

### Web 界面（桌面形态）

```bash
./fpkm web --bind 0.0.0.0 --port 7666
```

打开浏览器即可获得一个完整的桌面环境，内置应用商店与已安装应用管理：

- 浏览远程应用源、一键安装
- 从本地文件或上传 `.fpk` 安装
- 应用以子窗口（`iframe`）或新标签页打开，按应用自身配置决定
- 每个应用可单独设置接入方式：**端口直连 / 统一网关 / CGI**

**应用商店** —— 浏览应用源、一键安装，已安装应用可直接打开：

![应用商店](docs/screenshots/store.webp)

**多窗口并行** —— 多个应用可同时以子窗口打开，自由拖动与缩放，
任务栏始终在最顶层：

![多窗口与任务栏](docs/screenshots/windows.webp)

**登录保护** —— 首次访问设置管理密码，之后凭密码登录：

<img src="docs/screenshots/login.webp" alt="登录页" width="760">

---

## 快速开始

### 从源码构建

需要 Go 1.24+ 与 Node.js（仅构建 Web 界面时需要）。

```bash
git clone <repo-url> fpkm
cd fpkm

# 构建前端（产物会内嵌进二进制）
./scripts/build-web.sh

# 构建二进制
./build.sh native
```

产物在 `dist/fpkm`，`data/` 会自动创建在它旁边。

### 运行

```bash
cd dist
./fpkm web --bind 0.0.0.0 --port 7666
```

首次访问会让你设置管理密码，之后即可安装应用。

---

## 兼容性说明

FPKM 的目标是**尽可能**跑起真实的 `.fpk` 包，但飞牛生态中存在一些
厂商私有机制，属于已知边界：

| 情况 | 说明 |
|------|------|
| 硬编码 fnOS 路径 | ✅ 通过符号链接自动兼容 |
| 需要 fnOS 网关签名 | ⚠️ 部分应用校验飞牛私有签名，可用「端口直连」方式绕开 |
| 依赖 fnOS 系统服务 | ⚠️ 极少数应用（如需调用 fnOS API）无法完全模拟 |
| Rootless Docker | ⚠️ Docker 类应用需自行准备运行环境 |

遇到应用跑不起来时，Web 界面会直接给出原因（缺 `manifest`、权限不足、
Socket 超时等），而不是静默失败。

---

## 项目结构

```
cmd/                CLI 命令（cobra）
internal/
├── app/            应用生命周期：安装、启动、进程、入口探测、路径兼容
├── fpk/            .fpk 包解析：manifest、解压、i18n、架构校验
├── gateway/        统一网关：HTTP / WebSocket / FastCGI 反向代理
├── web/            Web 服务与 REST API
├── config/         配置与数据目录
├── session/        HMAC 会话令牌
└── logger/         日志
web/                React + TypeScript + Semi Design 前端
scripts/            构建脚本
```

---

## 安全提示

FPKM 会安装并运行任意程序，等同于在本机执行代码。

- 只安装来源可信的 `.fpk` 包
- 不要把管理端口直接暴露到公网，建议配合反向代理与 HTTPS
- 管理密码以 PBKDF2-HMAC-SHA256 加盐存储，但仍请使用强密码

---

## 致谢

飞牛 fnOS 提供了 `.fpk` 这套应用分发格式与配套的运行时约定，
FPKM 只是把这套约定在飞牛之外重新实现了一遍。

---

## License

[Apache License 2.0](LICENSE) © 2026 FPKM contributors

可自由使用、修改、分发（含商业用途），需保留版权与许可声明。
