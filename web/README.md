# FPKM 应用商店（Web 界面）

`fpkm web` 为 fpkm 提供图形化界面：在浏览器里浏览服务器上的 `.fpk` 包、上传安装包、
以及管理已安装应用（启动 / 停止 / 卸载 / 升级）。

前端基于 **React 18 + TypeScript + Vite + [Semi Design](https://semi.design/)**，
构建产物通过 `go:embed` 打进 fpkm 单二进制，**无需额外部署**。

---

## 快速开始

```bash
# 1. 构建（前端 + Go 单二进制）
./build.sh

# 2. 启动（仅本机访问）
./dist/fpkm web

# 3. 局域网访问
./dist/fpkm web --bind 0.0.0.0 --port 7667

# 4. 忘记密码时清除
./dist/fpkm web --reset-password
```

**首次运行**：浏览器打开页面会要求**设置一个管理密码**，之后用该密码登录。
密码以 PBKDF2-HMAC-SHA256（600000 次迭代 + 随机盐）存储于
`~/.local/fpk/password.json`，权限 `0600`，不保存明文。

### 常用参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--bind` | `127.0.0.1` | 监听地址。`0.0.0.0` 允许局域网访问 |
| `--port` | `7667` | 监听端口 |
| `--allow-fs` | `true` | 是否允许浏览服务器文件系统以选择本地包 |
| `--reset-password` |  | 清除管理密码后退出（忘记密码时的救援开关） |
| `--auto-start` | `true` | 安装/升级完成后自动启动应用 |
| `--public-host` | 空 | 对外主机名（含端口），用于反向代理场景 |
| `--public-scheme` | 空 | 对外协议 `http`/`https`，留空跟随浏览器 |

### 应用访问地址

界面上「打开」按钮的地址**按浏览器当前的主机名生成**，而不是写死 `localhost`：

| 访问界面的方式 | 生成的应用地址 |
|----------------|----------------|
| `http://127.0.0.1:7667` | `http://127.0.0.1:7666/app/xxx/` |
| `http://nas:7667` | `http://nas:7666/app/xxx/` |
| `http://192.168.1.10:7667` | `http://192.168.1.10:7666/app/xxx/` |

后端只返回 `accessPath` / `accessPort`，由前端拼接，因此从任何机器访问都不会
指向用户自己的电脑。若通过反向代理访问、且网关端口未直接暴露，可用
`--public-host nas.example.com --public-scheme https` 显式覆盖。

#### 路径取自 ui/config 的 url 字段

统一网关应用的**打开路径**必须读 `app/ui/config` 的 `url` 字段，不能拿
`gatewayPrefix` 代替 —— 两者含义不同：

| 字段 | 用途 | 形态 |
|------|------|------|
| `gatewayPrefix` | 网关路由匹配/转发 | 不带尾斜杠，如 `/app/fnchat` |
| `url` | 桌面「打开应用」的地址 | 可能带尾斜杠、子路径、查询串 |

真实差异（取自本机已安装应用）：

| 应用 | gatewayPrefix | url（实际打开） |
|------|---------------|------------------|
| fnchat | `/app/fnchat` | `/app/fnchat/` |
| EasyTier-Web | `/app/EasyTier-Web` | `/app/EasyTier-Web/` |
| file-collector | `/app/file-collector` | `/app/file-collector/gateway` |
| fygo-browser | `/app/fygo-browser` | `/app/fygo-browser/browser.html?v=1.0.1-fnos` |
| fn-appstores-client | `/app/fn-appstores-client` | `/app/fn-appstores-client` |

解析逻辑见 `internal/app/uiconfig.go`：

- `ResolveGatewayPrefix()` → 路由前缀（`gatewayPrefix`，缺失时从 `url` 推导）
- `ResolveLaunchPath()` → 打开路径（`url` 原样保留）

两者分别写入 env 的 `GATEWAY_PREFIX` 与 `LAUNCH_PATH`。网关注册路由时用前者
（不带尾斜杠），界面「打开」按钮用后者。由于网关按
`path == prefix || HasPrefix(path, prefix+"/")` 匹配，带尾斜杠的请求同样能命中路由。

`start` 时会以 `ui/config` 为准重新校准这两个值，因此旧版本安装的应用
下次启动即自动修正，无需重装。

---

## 功能

### 0. 应用商店（远程应用源）

界面按"应用商店"布局：左侧分类导航 + 顶部搜索 + 卡片网格。

**右上角**为 5 个无背景的图标按钮（悬停显示浅色底），依次是：

| 图标 | 作用 |
|------|------|
| 循环箭头 | 刷新应用源索引 |
| 文件夹 | 从服务器本地文件安装 |
| 上传 | 上传安装包安装 |
| **齿轮** | 打开设置面板（应用源 / 安全 / 关于） |
| 退出 | 退出登录 |

- **应用源**：默认内置 `FN软仓官方源`，可在「应用源」面板中添加/启用/删除多个源。
  索引格式与飞牛第三方源一致（`GET {源地址}/api/apps`，返回 `{"data":[...]}`）。
- **分类**：由应用源条目的 `tags` 字段聚合而成（如 工具 / 影音 / 网络 / 游戏）。
- **搜索**：按名称、作者、描述匹配。
- **一键安装**：点击「安装」→ 服务端下载（带进度日志）→ 校验包结构 →
  `sha256` 校验（源提供时）→ 复用安装流程 → 自动启动。
- **图标**：优先从 FPK 包内提取 `ICON.PNG`（缓存到 `~/.local/fpk/icons/`），
  失败时回退到源提供的远程图标（服务端代理并缓存），再失败显示名称首字母。
- **架构过滤**：源的 `platform` 字段与当前 CPU 架构不匹配时标记为"不支持"并禁用安装。

> 说明：应用源里的下载量/评分等字段由源提供，界面仅作展示；
> 应用是否真正可运行仍取决于包本身与该机器架构。

### 0.5 安装/卸载向导（wizard）

FPK 包可以带 `wizard/` 目录，用向导收集用户输入。fpkm 会读取并向用户展示：

| 文件 | 时机 |
|------|------|
| `wizard/install` | 安装前 |
| `wizard/upgrade` | 升级前 |
| `wizard/uninstall` | 卸载前 |
| `wizard/config` | 安装后从设置中修改 |

**流程顺序**（商店应用）：`下载（显示进度）→ 向导 → 安装日志窗口`。

- 点击安装后先弹**下载任务窗口**，实时显示下载百分比与字节数；
- 下载完成后自动收起，若有 `wizard/install` 则弹向导，否则直接安装；
- 向导填写完成并校验通过后才创建安装任务。

本地文件安装的流程为：`确认包信息 → 向导 → 安装日志窗口`。
无论哪种入口，**日志窗口都不会抢在向导前面出现**。

支持的字段类型（与飞牛文档一致）：

| 类型 | 说明 |
|------|------|
| `tips` | 只读富文本提示（**支持 HTML**，真实应用大量使用 `<br>`、`<strong>`、内联样式） |
| `text` | 短文本、端口、路径 |
| `password` | 密钥/令牌，输入框遮蔽 |
| `radio` | 少量互斥选项 |
| `checkbox` | 多选（以逗号拼接为单个环境变量） |
| `select` | 较长互斥列表 |
| `switch` | 布尔开关 |

校验规则支持 `required` / `min` / `max` / `len` / `pattern`，
前端即时校验 + **服务端二次校验**（接口可被直接调用，不能只信任前端）。

用户填写的值会作为**同名环境变量**传给生命周期脚本
（`install_callback` / `uninstall_callback` 等），与飞牛行为一致。
例如 file-collector 的 `wizard_admin_user` 会变成 `$wizard_admin_user`。

两个实现细节：

- **i18n 占位符**：部分应用的向导文案写成 `${install.stepTitle}`，
  实际文本在同包的 `i18n/zh-CN`（INI 风格）里。fpkm 会解析并替换；
  未命中的占位符原样保留，便于排查。
- **HTML 白名单**：向导文案来自第三方 FPK 包，渲染前会过滤
  `<script>`/`<iframe>` 等危险标签与所有 `on*` 事件属性，
  避免安装包借向导文案执行脚本。

### 0.63 桌面壁纸换成图片

壁纸改为用户提供的科幻场景图（机甲 + 云海城市），放在
`web/public/wallpaper.{webp,jpg}`，由 CSS `image-set` 让浏览器自行挑选：

```css
.fpkm-desktop {
  background-color: #0b1220;              /* 图片未加载时也不露白底 */
  background-image: image-set(
    url('/wallpaper.webp') type('image/webp'),
    url('/wallpaper.jpg')  type('image/jpeg')
  );
  background-size: cover;
  background-position: center;
}
```

**为什么同时放 WebP 与 JPEG**

源图 2732×1535。缩到 2560 宽后：WebP 304KB，JPEG 556KB（WebP 小约 45%）。
实测浏览器**只请求被选中的那一份**（当前环境只发出 1 个 `wallpaper.webp` 请求），
因此保留 JPEG 只增加少量二进制体积，不增加访问流量，作为老浏览器兜底。

**为什么加了暗色薄纱（scrim）**

这张壁纸右侧偏亮，直接铺底会让白色图标文字糊在背景里。用独立的
`::after` 伪元素叠加一层渐变暗纱（而不是把渐变写进 `background`，
以免干扰 `image-set`）：

```css
.fpkm-desktop::after {
  background:
    linear-gradient(180deg, rgba(6,12,24,.42), rgba(6,12,24,.28) 45%, rgba(6,12,24,.5)),
    radial-gradient(1200px 700px at 78% 8%, rgba(6,12,24,.28), transparent 65%);
  z-index: 0;
  pointer-events: none;
}
```

**分层（踩过的坑）**

`::after` 与桌面内容都是 `position:absolute` 且未设 `z-index` 时，
按 **DOM 顺序**绘制 —— 伪元素排在子元素之后，会把图标和窗口整个盖住。
因此显式分层：

```
背景图 → 网格 ::before (z0) → 暗纱 ::after (z0)
      → .fpkm-desktop-area (z1) → 窗口 (z2+) → Dock (z9000)
```

实测 `elementFromPoint` 命中图标与 Dock 均正常（未被遮挡）。

生成壁纸的命令（替换源图后重跑）：

```bash
python3 - <<'PY'
from PIL import Image
src = Image.open("源图.jpg").convert("RGB")
w = 2560
src = src.resize((w, round(src.height*w/src.width)), Image.LANCZOS)
src.save("web/public/wallpaper.webp", "WEBP", quality=86, method=6)
src.save("web/public/wallpaper.jpg",  "JPEG", quality=88, optimize=True, progressive=True)
PY
```

### 0.62 桌面修订（图标 / 商店加载 / Dock 分隔线 / 只显示运行中）

**1. 应用图标换成带透明通道的新图标**

原 `fpkm-logo.png` 是**不透明白底**（四角 alpha=255），在深色桌面上会看到
一个白色方块。新图标四角 alpha=0，已按同一方式重新生成全部尺寸：

```python
src = Image.open("fpkm-logo.webp").convert("RGBA")   # 保留 alpha
src.save("web/public/fpkm-logo.png")                  # ★ 不要 flatten 到白底
for s in (16,32,48,64,128,180,192,512):
    src.resize((s,s), Image.LANCZOS).save(f"web/public/icon-{s}.png")
src.save("web/public/favicon.ico", format="ICO", sizes=[...])
```

**2. 商店加载慢的原因与修复（重点）**

> 疑问："打开桌面时不就已经加载了商店的 HTML 吗？"

桌面启动时加载的是**主文档**。此前商店用 `<iframe src="/?embed=store">`
打开，而 **iframe 是一个独立文档** —— 浏览器不会复用主文档已执行的 JS 运行时，
必须重新下载、解析并执行整个 SPA（约 800KB JS + 700KB CSS），
再从头 boot 一次 React 并重跑 bootstrap 请求。这才是"加载很久"的原因。

修复：把商店改为**在同一个 React 树内渲染**，直接挂进窗口内容区：

- `App` 把外壳抽成 `buildShell(onBackToDesktop)`，通过 `renderStore` 传给 `Desktop`；
- `AppWindowFrame` 新增 `inlineContent`：有内容就直接渲染，不再创建 iframe；
- 窗口只记录 `builtin: 'store'` 标记，内容在**每次渲染时**生成
  （不能把 React 节点缓存在 state 里，否则会变成陈旧快照、交互失效）；
- `/?embed=store` 与 embed 模式已移除。

实测：点开商店 **0 个新增 asset 请求**（不再重新加载 SPA），DOM 就绪约 300ms，
二次打开约 120ms。

**3. Dock 商店图标右侧加分隔线**

`.fpkm-dock-sep`：1×34px 竖线，并在两侧加了额外外边距
（实测商店与首个普通应用间距由 6px 增至 30px），像 macOS 那样把
"特殊入口"和"普通应用"分开。

**4. 桌面只显示正在运行的应用**

在桌面上点未运行的应用本来也打不开，摆在桌面只会误导。现在：

- **桌面**：只显示 `running` 的应用（+ 内置商店图标）；
- **Dock**：仍然常驻显示**全部已安装**应用（保留 0.61 的行为）；
- 桌面图标上的绿点已移除（桌面上的应用必然在运行，绿点已无信息量）。

带来的连带问题与处理：未运行的应用不在桌面上，于是"从任务栏移除后如何恢复"
在桌面找不到入口。为此把 Dock 显隐状态抽成 `web/src/desktop/dock.ts`
（`useDock()`，基于 localStorage + 自定义事件跨组件同步），
并在商店的「已安装」卡片上新增**「移出任务栏 / 固定到任务栏」**按钮作为恢复入口。

**顺带修复：构建脚本不做类型检查**

`package.json` 的 `build` 只是 `vite build`，而 Vite 用 esbuild 转译时
**不进行类型检查** —— 类型错误被静默忽略并产出"能跑但有 bug"的产物。
`scripts/build-web.sh` 已在打包前显式执行 `npx tsc --noEmit`。

### 0.61 桌面交互修订

**1. 应用商店改为子窗口打开**

商店不再是"替换整个界面"，而是桌面上的一个普通应用窗口：

- 桌面图标 target 为 `/?embed=store`，前端识别 `embed=store` 后
  **只渲染商店外壳**（不渲染桌面），从而可被 iframe 内嵌；
- 关闭/最小化商店窗口即回到桌面（此前进入商店后没有任何返回入口）；
- 内嵌模式下隐藏侧边栏的「返回桌面」条目（窗口自带标题栏）。

**2. 图标网格改为列优先**

`grid-auto-flow: column` + `grid-template-rows: repeat(auto-fill, 100px)`：
先自上而下排满第一列，再排第二列（与真实桌面一致）。
实测在 420px 高视口下为「第1列: 商店/Lucky/文件收集器，第2列: FN软仓」。

**3. macOS 风格 Dock**

任务栏重做为 Dock（`.fpkm-dock`）：

| 行为 | 说明 |
|------|------|
| 常驻显示 | 已安装应用直接显示在 Dock 上，**只显示图标**（无文字） |
| 打开态指示 | 该应用有窗口时，图标**下方显示小圆点** |
| 单击 | 打开应用或聚焦已有窗口 |
| 右键 Dock 图标 | 「从任务栏移除」 |
| 右键桌面图标 | 「保留在任务栏」恢复（含「打开」「应用详情」） |
| 持久化 | 移除/恢复记录存 localStorage，刷新后保持 |

**4. 单击即打开**

桌面图标由「双击打开」改为**单击打开**（单击同时选中）。

**5. 修复：暗黑模式下窗口标题栏文字看不清**

窗口标题栏底色是本文件固定的深色渐变，但 Semi 会给其中元素直接上色：

```
.semi-typography                          { color: var(--semi-color-text-0) }  → 深色
.semi-button-tertiary.semi-button-borderless { color: var(--semi-color-text-1) }  → 深色
```

这些规则与原 `.fpkm-win-actions .semi-button`（同为 2 个类）权重相同，
而打包后的 CSS 顺序不保证与 import 顺序一致，Semi 的深色文字便盖在了
深色标题栏上。修复方式：提高选择器权重并显式写死颜色（不依赖主题变量）：

```css
.fpkm-desktop .fpkm-win-title,
.fpkm-desktop .fpkm-win-title .semi-typography { color: #e6edf7 !important; }
.fpkm-desktop .fpkm-win-actions .semi-button:not(.semi-button-danger) { color: #cbd5e1 !important; }
.fpkm-desktop .fpkm-win-actions .semi-button-danger { color: #ff6b5e !important; }
```

实测（`colorScheme: dark`）：标题亮度 236、控制按钮 212、关闭按钮为红色。

> 另一个同类坑：Dock 图标原用 Semi `Tooltip` 做悬浮提示，但 Tooltip 渲染到
> `.semi-portal-inner`，会盖住图标并**截获鼠标事件**，导致右键菜单点不出来。
> 已改用原生 `title` 属性（无 portal，不拦截事件）。

### 0.60 FPKM 桌面（分支 feature/desktop-shell）

把 FPKM 从"应用商店"升级为**桌面环境**：应用商店成为桌面上的一个内置图标。

**桌面组成**（`web/src/desktop/`）

| 组件 | 作用 |
|------|------|
| `Desktop.tsx` | 壁纸 + 图标网格 + 子窗口容器 + 小组件布局 |
| `AppWindowFrame.tsx` | 可拖动/八向缩放/最小化/最大化/刷新的 iframe 子窗口 |
| `Taskbar.tsx` | 玻璃质感（`backdrop-filter`）任务栏，管理已打开窗口 |
| `Widgets.tsx` | 右侧小组件：时钟、内存/负载、存储、应用统计 |
| `icons.ts` | Symbol 图标集中导出，便于后续换图标 |

**打开方式由 `ui/config` 的 `type` 决定**

飞牛文档《应用入口》：`iframe` 在桌面窗口内打开，`url` 在浏览器标签页打开。
后端新增 `app.ResolveLaunchMode` 读取该字段，并通过 `AccessInfo.launchMode`
下发给前端：

```
type=iframe → 桌面子窗口（若应用未运行，先启动，成功后再开窗）
type=url    → window.open 新标签页
无 ui/config → 退化为新标签页（无法保证对方允许被嵌入）
```

> 未运行的应用会先启动并**等待任务完成**再开窗 —— 否则端口尚未监听，
> iframe 只会加载到空白页。启动失败时提示用户查看任务日志，不打开坏窗口。

**新增接口**

`GET /api/system/stats` 返回内存/磁盘/负载/运行时长，供右侧小组件使用
（读取 `/proc/meminfo`、`/proc/loadavg`、`statfs`，跳过 tmpfs 等虚拟文件系统）。

**导航**

- 登录后默认进入桌面；
- 双击「应用商店」图标 → 切回原商店界面（侧边栏出现「返回桌面」）；
- 桌面图标右键 → 打开该应用的详情页。

**已知限制**

- 部分应用会设置 `X-Frame-Options`/CSP 禁止被嵌入，此时子窗口会显示
  「无法在窗口内打开」并提供「在新标签页打开」的兜底按钮；
- 沙箱策略为 `allow-same-origin allow-scripts ...`，若应用依赖
  `window.top` 跳转等能力可能受限。

### 0.50 修复：隐藏插入符的同时保留文字选中

**需求**：不显示闪烁的文本插入符，但文字仍然可以选中复制。

这两个目标由**不同的 CSS 属性**控制，不能混用：

| 属性 | 作用 |
|------|------|
| `cursor` | 鼠标指针形状（`default` 去掉 I 形，`pointer` 表示可点） |
| `user-select` | 能否选中文字（必须保持 `text`） |
| `caret-color` | 输入插入符的颜色（`transparent` = 不可见但输入正常） |

**⚠️ 上一版的错误**：用 `user-select: none` 去"消除插入符"，
结果把**文字选中也一起禁用了**（标题、元信息都复制不了）。
0.51 只解决了指针形状，没有解决选中被禁的问题。

**正确做法**：

```css
/* 详情页正文：指针是普通箭头，但文字保持可选中 */
.fpkm-detail,
.fpkm-detail .semi-typography,
.fpkm-detail .fpkm-detail-meta,
.fpkm-detail .fpkm-detail-desc {
  cursor: default;
  user-select: text;
}

/* 顶部搜索框：插入符不可见，但输入/选中/复制全部正常 */
.fpkm-top-search input { caret-color: transparent; }
```

另外在切换视图（进入应用详情）时主动 `blur()` 当前焦点，
避免焦点残留在搜索框上导致插入符出现在详情页。

**注意**：登录/设置密码的输入框**不隐藏**插入符 ——
那里需要它提示输入位置，隐藏反而造成困扰。

实测：搜索框 `caret-color: rgba(0,0,0,0)` 且仍能输入与 Ctrl+A 选中；
标题可选中（"FN软仓"）、元信息可选中（"晦华先生"）；
按钮仍为 `pointer`；密码框插入符保留。

### 0.51 修复：详情页出现文本插入符（I 形光标）

**现象**：进入应用详情后，鼠标划过应用标题/字段时光标变成文本插入符。

**原因**：浏览器对文本元素默认使用 `cursor: auto`，而 `auto` 在文本上
会渲染成 **I 形文本光标**。详情页是只读的浏览页面（没有输入框），
但标题、副标题、元信息等 25 个元素都是 `auto`，因此看起来"像可以输入"。

上一轮只修了图标容器（`cursor: default`），没有覆盖正文。

**修复**：在 `.fpkm-detail` 范围内统一设为 `cursor: default` 并禁用文本选择，
再单独给按钮/链接恢复 `cursor: pointer`：

```css
.fpkm-detail { cursor: default; }
.fpkm-detail .semi-typography,
.fpkm-detail .fpkm-detail-meta,
.fpkm-detail .fpkm-detail-desc { cursor: default; user-select: none; }

.fpkm-detail button,
.fpkm-detail a,
.fpkm-detail .semi-button { cursor: pointer; }
```

实测：详情页内 `auto` 元素从 25 个降为 0；按钮/下拉箭头/返回仍为 `pointer`；
商店卡片、已安装卡片、侧边栏导航的光标未受影响。

### 0.52 修复：应用图标左上角出现闪烁光标

**现象**：应用详情页的应用图标（左上角）一直有文本插入符在闪，
看起来像个输入框。

**原因**：图标区域是一个 flex 容器，里面只放了一个 `<img>`。
浏览器会把这种"只含行内内容"的块级元素视为**可插入文本**的容器 ——
`document.caretPositionFromPoint()` 在图标区域返回的是有效插入符位置，
因此点击或拖拽时会把文本光标放进该容器并闪烁。
图标本身是纯图形，这个插入符属于噪音。

**修复**（`.fpkm-detail-icon`）：

```css
user-select: none;        /* 不参与文本选择 */
caret-color: transparent; /* 即使获得焦点也不显示插入符 */
cursor: default;
```

并让 `<img>` 不接收指针事件（`pointer-events: none`）与拖拽
（`-webkit-user-drag: none`），避免图片被单独拖走。
标题与描述文字**保持可选中**，不影响复制应用名。

### 0.57 修复：纯脚本应用被误判为"不支持 ARM"

**现象**：应用列表里有些应用显示「不支持 arm」且安装按钮置灰，
但机器是 x86，实际上可以安装。

**原因**：应用源里的 `platform` 字段**不可靠**。实测：

| 应用 | 源里的 platform | 包内实际内容 | 在 x86 上 |
|------|----------------|--------------|-----------|
| CloudSaver | `arm` | **无任何原生二进制**（`node server.js`） | ✅ 可运行 |
| Cloudimgs | `arm` | 无 ELF | ✅ 可运行 |
| guandan | `arm` | 无 ELF | ✅ 可运行 |
| hand.nas.pro | `arm` | aarch64 ELF | ❌ 确实不能运行 |

早期实现直接信任 `platform` 标签并据此禁用按钮，因此把
前三类"纯 Node/脚本/前端"应用错误地拦住了。

**修复**：改为按**包内实际二进制架构**判断（`internal/fpk/arch.go`）：

1. 扫描包内所有文件头，识别 ELF 的 `e_machine`（x86_64/i386/aarch64/arm），
   也会进入内层 `app.tgz` 递归扫描；
2. **包内没有任何原生二进制** → 与架构无关，任何机器都可安装；
3. **确有二进制且不匹配** → 禁用安装，并给出「安装包内仅含 aarch64 架构的二进制」；
4. **尚未下载、只有标签可依据** → 只给**软提示**（标签显示「标注 arm」），
   不禁止安装；等下载后再按真实二进制校验。

这样既不误伤纯脚本应用，又不会让真正的 arm 包装上去跑不起来。

> 顺带修了一个隐藏 bug：`.fpk` 实际上是 **gzip 压缩的 tar**
> （`1f 8b` 开头），早期扫描代码直接按 tar 解析会报
> `invalid tar header` 并得出"包内无二进制"的错误结论 ——
> 正是它导致判断完全失效。现在会先尝试 gzip，失败再退回 tar。

### 0.53 图标与站点图标

- 侧边栏与登录页使用 FPKM 图标（`web/public/fpkm-logo.png`），
  不再用渐变底 + 字母「F」的占位样式；
- 站点图标（favicon）由同一张源图生成，放在 `web/public/`：

| 文件 | 用途 |
|------|------|
| `favicon.ico` | 多尺寸 ICO（16/24/32/48/64/128/256），兼容旧浏览器与 Windows |
| `icon-16/32/48/64/128/192/512.png` | 现代浏览器按尺寸选择 |
| `apple-touch-icon.png` | iOS 添加到主屏 |
| `fpkm-logo.png` | 页面内展示（侧边栏、登录页、mask-icon） |

这些文件由 `web/index.html` 的 `<link rel="icon">` 引用，
构建时 Vite 会从 `public/` 原样拷贝到产物根目录，随 `go:embed` 打进二进制。

替换图标后重新生成各尺寸：

```bash
python3 - <<'PY'
from PIL import Image
src = Image.open("web/public/fpkm-logo.png").convert("RGBA")
for s in (16, 32, 48, 64, 128, 180, 192, 512):
    src.resize((s, s), Image.LANCZOS).save(f"web/public/icon-{s}.png", "PNG", optimize=True)
src.save("web/public/favicon.ico", format="ICO",
         sizes=[(16,16),(24,24),(32,32),(48,48),(64,64),(128,128),(256,256)])
PY
```

### 0.54 已安装卡片：标签区固定两行

卡片头部的标签区（运行状态 / 接入方式 / Docker / 端口 / 可升级）
**固定为两行高度（46px）**：

- 端口信息作为标签显示在其中（`端口 16602`），不再放在底部按钮旁；
- **没有端口的应用，第二行留空**，因此所有卡片高度一致、按钮对齐；
- 端口标签使用中性配色 + 等宽字体，避免与「运行中」等状态标签抢注意力。

### 0.56 列表与详情的操作一致性

**应用列表（商店）**

- 已安装的应用**不再显示禁用的「已安装」**，主按钮直接是 **「打开」**，
  点击后在新标签页打开应用；
- 若同时存在新版本，旁边额外给一个「更新」次要按钮，两者都可操作。

为此 `/api/store/apps` 在应用已安装时会附带 `running` 与 `access`
（访问方式与路径），列表无需再请求一次就能直接打开应用。

**应用详情**

已安装应用的操作区是一个**分裂按钮**（Semi `SplitButtonGroup` + `Dropdown`）：

```
[ 打开 ▾ ]
```

- **主按钮**：应用运行中显示「打开」，未运行显示「启动」；
- **下拉菜单**（点击箭头展开）：
  - **停止** —— 仅在应用运行中时出现；
  - **卸载** —— 点击后弹二次确认。

这样「停止」与「卸载」不再常驻界面，避免误点；
未安装的应用仍直接显示「安装」按钮。

实现上有三处 Semi 默认样式需要覆盖（见 `.fpkm-split-btn` 规则），
否则两半拼不成一个完整椭圆：

| 默认行为 | 问题 | 处理 |
|----------|------|------|
| `.semi-button-size-small{height:24px}` | 箭头按钮被压回 24px，与主按钮不等高 | 用同等权重的选择器显式设 `height:30px` |
| `.semi-button-split{display:inline-block}` | 两按钮按基线对齐，箭头下沉 3px | 改为 `inline-flex` + `align-items:stretch` |
| `.semi-button-split .semi-button{margin-right:1px}` | 两半之间留 1px 缝 | `margin-right:0` |

下拉菜单由 portal 渲染、无法继承按钮宽度，因此给菜单单独设
`min-width` 与按钮整体对齐，并让菜单项拉满宽度。

### 0.55 按钮样式

**高度**：小尺寸 24→30px、常规 32→36px、大尺寸 40→42px（整体增高，更好点按）。

**圆角**：常规 10px、小尺寸 9px（Semi 默认约 3px，偏方）。

**层级配色**（从重到轻，`.fpkm-shell` 内统一）：

| 层级 | 场景 | 样式 |
|------|------|------|
| 主操作 | 打开 / 启动 / 安装卡片外 | 实心蓝 `#2b6cff` + 白字 |
| 次操作 | 安装 / 更新 | 淡蓝底 `#eef4ff` + 蓝字 |
| 一般操作 | 停止 | 白底 + 细边框（中性） |
| 提示操作 | 升级 | 蓝字 + 淡蓝描边 |
| 危险操作 | 卸载 | 淡红底 + 红字，**hover 才变实心红** |

要点：

- 「打开」是应用最主要的动作，因此用实心蓝；「停止」为中性样式，
  避免与主操作抢注意力。
- 卸载默认不再是高饱和红块，改为淡红底红字，降低视觉噪音，
  同时 hover 变实心红保留危险感（不会与普通按钮混淆）。
- 安装图标使用 `IconDownloadStroked`（向下箭头落入托盘）。

样式集中在 `web/src/styles/global.css` 的 `.fpkm-shell .semi-button*` 规则里。
注意只对 `semi-button-solid` 设背景色，否则 `light`/`borderless`
会变成"蓝底蓝字"导致文字不可见。

### 0.6 应用详情页

点击**商店卡片**或**已安装卡片**都会进入应用详情页，包含：

- 大图标、名称、分类标签
- 主操作按钮（安装 / 更新 / 打开 / 停止）与「应用设置」「卸载」
- 元信息行：开发者、发布者、下载数、大小、安装位置、当前版本、更新时间
- 运行状态标签（运行中 + PID / 已停止 / 端口 / 可更新）
- **应用介绍**：渲染 manifest / 应用源里的富文本（含图片）

已安装应用的详情会自动与商店索引合并，因此已安装的第三方应用同样能看到
下载量、分类、项目主页等信息，并识别是否有新版本。

### 1. 从本地文件安装
浏览服务器目录（逐级导航 + 面包屑 + 手动输入绝对路径），只展示目录与 `.fpk` 文件。
选中后弹出确认框，展示包的应用名、版本、维护者、大小，并自动判定
**新安装 / 升级 / 同版本重装 / 降级**，确认后才开始安装。

### 2. 上传安装包安装
拖拽或点击选择本机 `.fpk`，带真实上传进度（XHR progress），
上传完成后服务端会先校验包结构，再归档到 `~/.local/fpk/packages/`
（便于后续升级），然后进入安装流程。

### 3. 已安装应用管理
以卡片列表展示所有已安装应用，支持：
- 启动 / 停止 / 卸载（卸载有二次确认）
- 一键打开应用（网关类应用直接跳转 `http://host:7666/app/xxx`）
- 识别接入方式（统一网关 / CGI / 端口应用），显示 PID 与端口
- 自动发现可升级：若 `packages/` 中存在同 appname 的更高版本，显示「升级」按钮
- 搜索、按状态筛选（全部 / 运行中 / 已停止 / 可升级）

### 4. 实时安装日志
安装/启停过程通过 **SSE** 把 fpkm 的日志实时推到界面（终端风格、按级别着色），
结束后给出成功/失败结论与耗时，失败时展示错误原因。

---

## 安全说明

管理接口能够**安装并运行任意程序**，等同于服务器上的代码执行权限，因此：

- 默认只监听 `127.0.0.1`；绑定非本机地址时会在终端醒目提示；
- **首次运行强制设置管理密码**，未设置密码前所有业务接口返回 401；
- 密码使用 PBKDF2-HMAC-SHA256（600000 迭代）加盐存储，文件权限 `0600`；
- 登录失败按来源 IP 指数退避（连续 5 次失败后开始锁定），抵御在线爆破；
- 上传先落盘到临时文件、校验通过后才归档；
- 应用名与安装路径做校验，拒绝路径穿越；
- 上传体积上限 2 GiB；
- 安装类操作**串行执行**，避免并发写同一目录。

> 建议：局域网使用时配置强令牌，并通过反向代理加 HTTPS；
> 不要把该端口直接暴露到公网。

---

## 架构

```
web/                         前端工程（React + TS + Semi Design）
├── src/
│   ├── api/                 API 客户端与类型（token 注入、SSE、上传进度）
│   ├── components/          AppCard / StoreCard / AppDetailPage / WizardModal /
│   │                        Settings / Auth /
│   │                        FileBrowser / UploadInstaller / InstallConfirm /
│   │                        TaskModal / LogViewer / ErrorBoundary
│   ├── hooks/useTaskLog.ts  订阅任务日志
│   └── utils/format.ts      体积/时间/版本等格式化

internal/web/                Go 后端
├── server.go                路由、会话校验中间件、SPA 托管、go:embed
├── auth.go                  管理密码（PBKDF2 加盐）、会话、登录限速
├── catalog.go               应用源聚合、索引缓存、安装包下载与校验
├── handler_wizard.go        向导查询（已安装应用 / 未安装的包）
├── handler_detail.go        应用详情（已安装 / 商店条目）
├── handler_store.go         商店列表、远程下载/安装、应用源 CRUD、图标
├── icons.go                 FPK 包内图标提取与缓存
├── handler_apps.go          应用列表与启停卸载升级
├── handler_fs.go            服务器文件浏览器与包解析
├── handler_install.go       本地路径安装 + multipart 上传安装
├── handler_task.go          任务 SSE 日志流
├── task.go                  任务与日志广播（含 logger 旁路）
├── version.go               版本号比较
└── dist/                    前端构建产物（由 npm run build 生成）

cmd/web.go                   `fpkm web` 命令
```

后端通过 `internal/app.Manager` 复用 CLI 的完整能力，
因此界面与命令行行为完全一致，不存在两套实现。

### 请求流程

```
浏览器 ──HTTP──► internal/web ──► app.Manager ──► fpk / gateway
                     │
                     └── SSE 推送 logger 输出 ──► 安装进度弹窗
```

---

## 开发

```bash
# 前端热更新（自动代理 /api 到 127.0.0.1:7667）
cd web && npm install && npm run dev

# 另开一个终端启动后端
./dist/fpkm web --no-auth        # 开发时可关闭鉴权
```

其他脚本：

| 命令 | 说明 |
|------|------|
| `npm run build` | 构建前端到 `internal/web/dist` |
| `npm run typecheck` | TypeScript 类型检查 |
| `./build.sh <target>` | 构建单二进制（native/linux/linux-arm/windows/darwin） |

### 关于 Semi Design 样式

`@douyinfe/semi-ui` 的 `package.json` 中 `exports` 字段没有暴露 `dist/css/*`，
因此不能直接 `import '@douyinfe/semi-ui/dist/css/semi.min.css'`。
`web/vite.config.ts` 用别名 `@semi-css` 指向该文件来规避（已在 `main.tsx` 中引入）。

---

## 排查案例：统一网关应用报"Socket 不存在"

启动统一网关应用（`ui/config` 里有 `gatewaySocket`）后，打开页面可能遇到：

```
应用 fnchat 的 Socket 不存在（应用可能未启动）
```

fpkm 现在会在启动阶段就等待并校验 Socket，失败时直接给出真实原因（见
`internal/app/manager.go` 的 `waitForSocket`），而不是等到打开页面才报错。

### fnchat 实例

以 fnchat 为例，它自带两种形态：

| 版本 | `ui/config` | Socket 来源 |
|------|-------------|-------------|
| ≤1.0.3 | 只有 `port: 8033` | 纯端口应用 |
| 1.0.4 ~ 1.0.6 | `gatewaySocket: "fnchat.sock"` | 由 `gateway-proxy` 反向代理创建 |
| 1.0.7（x86） | `gatewaySocket: "app.sock"` | 由 `fnchat.py` 自己监听 |

两个具体原因：

1. **`gateway-proxy` 没被启动（≤1.0.6、ARM 版）**
   `cmd/main` 里的 `CMD` 是：
   ```bash
   ".../python" ".../fnchat.py" -c cfg & for i in $(seq 1 30); do nc -z 127.0.0.1 8033 && break; sleep 1; done; .../gateway-proxy -socket ".../fnchat.sock" ...
   ```
   旧版 fpkm 按 ` & ` 机械切分多进程，把第二段 `for ...` 当成一个进程，
   于是"命令名"成了 shell 关键字 `for` —— `gateway-proxy` 永远不会启动，
   socket 自然不存在。
   **修复**：`internal/app/shellparse.go` 用引号感知 + 块感知的切分器，
   过滤 shell 关键字，并在整段 CMD 中兜底查找 `gateway-proxy` 这类
   socket 代理命令。回归测试见 `internal/app/entry_fnchat_test.go`。

2. **端口 8033 被占用（1.0.7）**
   `fnchat.py` 同时监听 `0.0.0.0:8033` 与 unix socket，且 socket 先绑定成功。
   如果 8033 已被占用（例如飞牛系统里已装了同一个 fnchat），uvicorn 会
   在绑定 TCP 时失败并 `SystemExit(3)`，进程退出时把刚建好的 socket 一并清理。
   表现就是"socket 一闪而过 → 打开页面报 Socket 不存在"。
   **修复**：`waitForSocket` 在 socket 出现后增加稳定观察窗口，进程退出或
   socket 消失即判定失败，并从日志中提取 `address already in use` 这类
   关键行作为原因提示。

   遇到这种情况，先确认端口占用者：
   ```bash
   ss -tlnp | grep 8033
   ```
   若是飞牛自带的同名应用，需先停用它或改用其它端口（`fpkm install --set`）。

---

## 排查案例：脚本 CRLF 行尾导致"启动脚本失败: exit status 2"

部分第三方 FPK 包在 Windows 上打包，`cmd/` 下的 shell 脚本带 **CRLF 行尾**。
bash 执行时会报：

```
cmd/main: line 9: syntax error near unexpected token `$'{\r''
```

并以 **exit status 2** 退出，表现为应用"启动失败"—— 但脚本逻辑本身是正确的。

**实例**：便签 `note` 1.2.1，其 `cmd/` 下 9 个脚本全部是 CRLF（实测 CR=108）。

fpkm 在安装时统一把脚本行尾归一化为 LF（`normalizeLineEndings`），
与飞牛原版脚本无需修改即可运行。该处理与硬编码路径重映射一起在
`remapScriptPaths` 中完成，只作用于文本文件（二进制由 `isBinaryContent` 跳过）。

### 日志位置

诊断"启动后立即退出"时，fpkm 按以下顺序查找日志，取第一个非空文件
（见 `readAppLog`）：

1. `logs/app.log` —— fpkm 转发的子进程 stdout/stderr
2. `logs/error.log`
3. `var/info.log`、`var/app.log`、`var/error.log` —— **应用自己写的日志**
4. 兜底：`logs/`、`var/` 下最近修改的 `.log`

第 3 条很关键：不少飞牛应用（如便签 note）在 `cmd/main` 里把日志写到
`${TRIM_PKGVAR}/info.log`，`logs/app.log` 会一直是空的。早期版本只读后者，
报错时给不出任何原因。

---

## 排查案例：入口探测失败（case 语句写法差异）

启动时报：

```
探测入口失败: 无法从cmd/main中提取二进制路径
```

**实例**：9Router `0.5.55`。

原因是 fpkm 判断 `cmd/main` 是否"分发脚本"时，早期用固定字符串匹配，
只认这几种写法：

```bash
case "$1" in
case '$1' in
case $1 in
```

而 9router 用的是**带默认值的参数展开**：

```bash
case "${1:-start}" in
```

没被识别 → 脚本被当成"需要提取二进制路径的普通脚本" → 解析失败。

现已改为用正则匹配 `case <表达式> in` 的结构（`looksLikeDispatcherScript`），
覆盖上述全部写法以及 `case $1` 换行、`${ACTION:-start}` 等变体；
同时保留了"必须同时存在函数定义或 source 才算分发脚本"的约束，
避免把普通脚本误判（已用 8 个真实包 + 6 个反例验证）。

### 9Router 的另一个问题：包内缺少 node_modules（包自身缺陷）

修复入口探测后，9router 会正确执行 `bash cmd/main start`，但**仍然启动失败**：

```
Error: Cannot find module 'next'
```

原因**在包本身**，与 fpkm 无关：

1. 该包只包含 Next.js 的构建产物 `.next-cli-build`，**不带 `node_modules`**；
2. 它的 `install_callback` 逻辑是"若已含构建产物则跳过 `npm install`"，
   于是依赖永远不会被安装；
3. 即使手动 `npm install`（实测 594 个包）后，包里仍缺少
   `.next-cli-build/server/preview-props.json`，服务依然无法完成启动。

结论：**9Router 0.5.55 这个包发布不完整，需要维护者重新打包**。
可联系源内标注的发布者 `techysy`（<https://github.com/techysy/9router-fnos>）。

---

## 排查案例：预启动命令出现畸形内容（test 表达式被当命令）

启动日志里出现这样的行，然后"卡住"：

```
执行预启动命令: /root/.local/fpk/apps/fnos-port-scanner/target/server/fnos-port-scanner ! -x /root/.local/fpk/apps/fnos-port-scanner/target/server/fnos-port-scanner ]
```

**实例**：fnOS 端口扫描器 `fnos-port-scanner`。

原因：`cmd/main` 里有大量**只用于判断**的 test 表达式，例如

```bash
if [ ! -x "$APP_BIN" ]; then   # 只检查可执行文件是否存在
if [ ! -S "$SOCKET_FILE" ]; then
while [ ! -f "$PID_FILE" ]; do
```

旧版 `isBinaryCall` 只判断"这一行是否出现二进制路径/变量"，于是把这些
条件判断当成了"调用二进制"，整行被解析成一条预启动命令，
参数里混进 `! -x ... ]` 这类 test 运算符，产生上面的畸形输出。

同一类误判还包括提示文案，例如

```bash
echo "缺少应用可执行文件或不可执行: $APP_BIN" > "$TRIM_TEMP_LOGFILE"
```

会被解析成"用提示文案作为参数去启动二进制"。在用户机器上抓到的真实
卡死进程 argv 正是这样：

```
[0] .../fnos-port-scanner
[1] 缺少应用可执行文件或不可执行: .../fnos-port-scanner
```

由于该二进制被"当成服务"启动后不会退出，`exec.Command(...).Run()`
便永久阻塞 —— 这就是"一直卡住"的直接原因。

现已修正 `isBinaryCall`（`internal/app/entry.go`）：

1. 先排除 test 表达式（`[ ... ]`、`[[ ... ]]`、`test ...`，
   含 `if`/`while`/`elif` 前缀与 `; then`/`; do` 后缀）；
2. 排除注释行；
3. 再要求引用出现在**命令词位置**（行首，或 `if`/`&&`/`;`/`nohup`/`exec`
   等之后），而不是任意位置。

已用 6 个真实包验证：畸形预启动命令从"出现"变为 **0**；
同时确认 tailscale 的 `ip tuntap`/`ip link` 系统级初始化命令
（走 `extractSystemSetupCommands`，另一条路径）仍然被正确提取。

### 兜底：预启动命令超时

即使解析仍有遗漏，也不应让启动永久卡住。现在每条预启动命令都带
**60 秒超时**（`preStartTimeout`），超时即终止并继续启动主进程。

> 另外，该应用的 `case "${1:-}" in` 也正是"入口探测失败"那类写法，
> 已由 `looksLikeDispatcherScript` 正确识别为分发脚本，
> 因此正常情况下它应当走 `bash cmd/main start`，根本不会进入
> 预启动命令提取分支。若日志里出现 `脚本(cmd/main)`，说明用的是旧版本。

---

## 已知限制

- 文件浏览器会展示服务器目录结构（可用 `--allow-fs=false` 关闭，同时也会禁用按路径安装）。
- 应用图标目前使用统一占位图标，尚未从 FPK 包内提取 `ICON.PNG`。
- 前端产物是单个 ~720 KB 的 JS chunk（gzip 后约 215 KB），未做路由级代码分割。
- `fpkm service` 守护进程本身仍是空壳（`runDaemon` 只有心跳），与本界面无关。
