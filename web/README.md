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

### 0.71 图标文字去背景 / 商店窗口更宽 / 窗口几何持久化 / 新窗口级联

#### 1. 桌面图标文字不加背景色

上一版给标签加了 `background: rgba(6,12,24,.45)` 圆角暗底来保证可读性，
视觉上是个色块。现在去掉背景，改由**多层文字阴影**提供对比：

```css
text-shadow:
  0 1px 2px rgba(0, 0, 0, 0.9),   /* 近处暗边 */
  0 0 3px rgba(0, 0, 0, 0.75),
  0 0 6px rgba(0, 0, 0, 0.45);    /* 远处柔和衬托 */
```

在明亮云海区域仍能读清白字，且没有矩形色块。

#### 2. 商店子窗口初始尺寸加大（应用列表排 3 列）

`.fpkm-grid` 用 `repeat(auto-fill, minmax(320px, 1fr))`，`gap: 14px`。

**关键实测数字**：理论 3 列需 3×320+2×14 = 988px，但浏览器取整后
**网格宽度必须 ≥ 1000px 才真的排 3 列**（986px 实测仍是 2 列）。

窗口内还需减去：208px 侧边栏 + 内容区左右 padding（约 44px）。

因此商店初值取 **1320×820**（1252 是底线，留了滚动条与取整余量）。
其它应用仍是 960×620。

#### 3. 窗口几何持久化到 localStorage

新增 `src/desktop/windowRect.ts`，按窗口 id 存储 `{x,y,w,h}`：

```
localStorage['fpkm.window.rects'] = {"builtin:store":{"x":200,"y":120,"w":1120,"h":700}}
```

要点（都有实测覆盖）：

- **最大化不写入**：最大化时 rect 是整屏，写进去会让下次打开也全屏。
  进入最大化前先保存"普通状态"几何，还原后再保存还原后的几何；
- **越界夹回**：上次在 1920 宽的屏幕记下 x=1500，换到 1024 的窗口时
  用 `clampRect()` 夹回可视范围（实测 1500,900 → 0,0 且尺寸收到 1024×700）；
- **脏数据容错**：JSON 损坏、字段非数字一律当作"没有记录"，回退默认布局；
- 尺寸下界 320×200，避免脏数据把窗口压成一条线。

#### 4. 新开窗口级联错开，不重叠

原来所有窗口用同一个居中公式，**第二个窗口会完全盖住第一个**。

改为级联：

```
以第一个窗口位置为锚点，第 n 个窗口错开 n×30px（每 8 个回头）
```

为什么用"锚点"而不是"各自居中 + 偏移"：不同尺寸窗口
（商店 1320 宽 vs 普通应用 960 宽）居中基线本身就差很多，
叠加偏移后错开量会变成 200px 以上，不像级联。用锚点后实测
偏移稳定为 **dx=30 dy=30**。

### 0.70 新壁纸 / 窗口可移到任务栏 / 页面标题区分桌面与商店

#### 1. 壁纸

换成 2732×1535 的新图（WebP 420KB / JPEG 621KB），
桌面与登录页同时生效（两处共用 `/wallpaper.webp`）。

#### 2. 窗口可以移动到任务栏位置，任务栏恒在最顶层

**原来为什么拖不进去**：窗口是 `.fpkm-desktop-area` 的子元素，而该容器

```css
.fpkm-desktop-area { inset: 0 0 76px 0; overflow: hidden; }
```

- 高度不含任务栏 → 拖动 `bounds` 永远到不了任务栏；
- `overflow: hidden` → 即使越界也会被裁掉。

**改法**：把窗口移到与任务栏**同级**的独立层 `.fpkm-window-layer`，
`bounds` 改测整个 `.fpkm-desktop`。

层级关系（从下到上）：

| 层 | z-index | 说明 |
|----|---------|------|
| `.fpkm-desktop-area` | 1 | 图标网格 |
| `.fpkm-window-layer` | 2 | 子窗口（窗口自身再叠 10+） |
| `.fpkm-dock` | 9000 | 任务栏 |

任务栏 z-index 远高于窗口层，因此窗口可以盖到任务栏位置、
但**永远盖不住任务栏**（任务栏始终可见可点，拖动时也不会被窗口挡住）。

窗口层用 `pointer-events: none`、窗口自身 `pointer-events: auto`，
空白处不挡住桌面图标点击。

实测（1400×900，任务栏占底部 76px）：

- 窗口底边可拖到 900（越过任务栏顶部 824）✅
- 任务栏中心 `elementFromPoint` 命中 `fpkm-dock-inner` ✅
- 窗口层 `overflow: visible`，不被裁剪 ✅
- 桌面图标仍可点击 ✅

#### 3. 页面标题

| 场景 | 标题 |
|------|------|
| 登录页 | FPKM 桌面 |
| 桌面 | FPKM 桌面 |
| 桌面上开着商店窗口 | FPKM 桌面（不跳动） |
| 桌面右键 →「应用详情」 | FPKM 桌面 |
| 商店作为独立应用打开（`mode==='store'`） | FPKM 应用商店 |

两个容易漏掉的点：

1. 桌面上开着商店**窗口**时标题保持"FPKM 桌面"，
   否则浏览器标签页会随开关窗口来回跳字；
2. 桌面右键 →「应用详情」会复用商店外壳渲染（`mode` 变成 `store`），
   但用户心理上仍在桌面，因此用 `detailFrom === 'desktop'` 区分，
   标题仍是"FPKM 桌面"（此前 `setDetailFrom('desktop' as View)`
   用 `as` 强转了一个 `View` 之外的值，现在补成显式的 `DetailOrigin` 类型）。

### 0.69 移除任务栏按钮 / 应用自启动恢复 / 商店打开行为统一

#### 1. 删除「移出任务栏」按钮

商店「已安装」列表里的「移出任务栏 / 固定到任务栏」按钮已删除。

保留的入口：**右键桌面图标**与**右键任务栏图标**仍可切换（右键菜单里的
「从任务栏移除 / 保留在任务栏」），因此不会出现"移除后无法找回"。

#### 2. 应用自启动恢复

**需求**：应用启动后记录下来；fpkm 退出后，下次打开桌面自动重启它们。

**关键设计**：记录的是**用户意图**，不是进程状态。

应用是 fpkm 拉起的子进程，fpkm 退出时它们必然结束。若用"进程是否活着"
做判据，重启后永远是"都没在跑"，就永远不会恢复。所以：

- `start`（用户点击、安装后自动启动）→ 写入记录；
- `stop`（用户主动停止）、`uninstall` → 清除记录；
- fpkm 启动时读记录，逐个 `Start()`。

存储：`$BaseDir/autostart.json`，内容是应用名数组：

```json
["fn-appstores-client", "lucky"]
```

实现细节（`internal/web/autostart.go`）：

- 原子写（`.tmp` + `rename`），避免掉电后读到半个文件；
- 幂等：重复 `mark` 不产生重复项；
- 文件损坏时按空集处理并告警，不让 fpkm 启动失败；
- `prune()` 清理已卸载应用的残留记录。

恢复流程（`internal/web/autostart_restore.go`）：

1. 读取记录；
2. 用 `Manager.List()` + `Status()` 得到"存在"与"已在运行"两集合；
3. `prune` 掉不存在的；
4. 对未运行的并发 `Start()`，单个失败不影响其它；
5. 重新注册网关路由（socket 此时才出现，否则 `/app/*` 会 404）。

顺带修掉一个隐患：`Manager.Start` 原本**没有幂等保护**，
对已在运行的应用再调一次会拉起第二个进程并覆盖 PID 文件，
导致旧进程失控。现在开头就检查 `IsRunning()` 并直接返回。

#### 3. 商店里的「打开」与桌面图标行为一致

**需求**：桌面模式下，商店里所有「打开」按钮都要像桌面图标那样，
按 `ui/config` 决定开子窗口还是新标签页。

原先三处「打开」都是 `window.open(accessURL, '_blank')`，
会把 `type=iframe` 的应用开成独立浏览器标签页。

改法：桌面把 `openAppByName` 传给商店（`renderStore(close, onOpenApp)`），
三处按钮统一走它，最终复用桌面的 `openIcon()` —— 
与桌面图标**完全同一条路径**：

| ui/config | 行为 |
|-----------|------|
| `type: iframe` | 桌面内子窗口（未运行会先启动） |
| `type: url` | 新浏览器标签页 |

涉及三处：`AppCard`（已安装列表）、`StoreCard`（商店卡片）、
`AppDetailPage`（详情页）。全屏兜底模式没有桌面容器，
保留 `window.open` 作为降级。

### 0.68 壁纸恢复正常亮度 + 侧边栏滚动条精细化

#### 1. 壁纸被压暗的问题

0.66 给桌面加了一层"暗色薄纱"（scrim）：

```css
/* 旧值：整屏 0.28~0.5 的黑，把明亮的云海壁纸压成灰黑色 */
background: linear-gradient(180deg, rgba(6,12,24,.42), rgba(6,12,24,.28) 45%, rgba(6,12,24,.5)), ...;
```

实测桌面中部亮度只有 **111**（原图约 137），确实明显发黑。

改为**很轻的整体微压**（0.12 → 0.02 → 0.14），实测亮度回到 **151**，
壁纸恢复通透感。

> 代价是图标标签在亮云区域对比度下降。解决方式不是再压暗壁纸，
> 而是给标签加局部暗底（`background: rgba(6,12,24,.45)` + 圆角 + 内边距），
> 只压文字周围那一小块，兼顾壁纸亮度与可读性。

#### 2. 侧边栏滚动条

应用商店在子窗口里高度有限，侧边栏会出现滚动条。原来的原生滚动条又粗又深。

目标：**平时隐藏、悬停显现、6px 宽、半透明灰**。

实现要点（踩过的坑）：

```css
/* ✗ 错误做法：同时写标准属性与 WebKit 伪元素 */
.fpkm-side { scrollbar-width: thin; scrollbar-color: transparent transparent; }
.fpkm-side::-webkit-scrollbar-thumb { background-color: transparent; }
.fpkm-side:hover::-webkit-scrollbar-thumb { background-color: rgba(0,0,0,.22); }
```

Chrome 一旦读到 `scrollbar-color` 就切换到**标准滚动条渲染**，
那套渲染不认 `::-webkit-scrollbar-thumb` 的悬停规则，
导致滑块恒为透明 —— 实测悬停后 `scrollbar-color` 变了、像素却没变化。

正确做法：**Chrome/Safari 只用 WebKit 伪元素**，
标准属性放进 `@supports not selector(::-webkit-scrollbar)` 的 Firefox 分支：

```css
@supports not selector(::-webkit-scrollbar) {
  .fpkm-side { scrollbar-width: thin; scrollbar-color: transparent transparent; }
  .fpkm-side:hover { scrollbar-color: rgba(0,0,0,.22) transparent; }
}

.fpkm-side::-webkit-scrollbar { width: 6px; }
.fpkm-side::-webkit-scrollbar-thumb { background-color: transparent; border-radius: 3px; }
.fpkm-side:hover::-webkit-scrollbar-thumb { background-color: rgba(0,0,0,.22); }
.fpkm-side::-webkit-scrollbar-thumb:hover { background-color: rgba(0,0,0,.35); }
```

实测（Chromium）：滚动条宽度 `6px`、空闲滑块 `rgba(0,0,0,0)`、
标准属性保持 `auto`（说明走的是 WebKit 分支）、悬停后滑块变为半透明灰。

### 0.67 修复：拖动窗口后窗口突然变小

**现象**：拖动子窗口，一松手窗口就缩成很小一块（约 300×190），
看起来像"最小化"，实际是**尺寸被破坏**。

这是 0.66 引入的回归，有**两个叠加原因**：

**原因 A：窗口顶部 3px 落在缩放手柄上，而不是标题栏**

`.fpkm-win-resize-n` 原本 `top:-3px; height:6px; z-index:5`，
而标题栏 `z-index:3` —— 窗口顶沿的 3px 实际属于"从顶部缩放"。
用户从标题栏最上沿按下想拖动，实际执行的是缩放，往下拖就越缩越小。

修法：

- 顶部手柄改为 `top:40px`（让出标题栏高度），不再与标题栏重叠；
- 标题栏 `position:relative; z-index:6`，高于手柄，保证标题栏区域始终可拖动；
- 手柄保持 `z-index:5`，边角仍可抓（右下角缩放实测有效）。

**原因 B：松手时把 width/height 一起清空了**

0.66 在 `stop()` 里把 `el.style.width/height` 清成 `''`，
想让"宽高交还给 React 的 style"。但 `.fpkm-win` 本身没有固定宽高，
内联宽高被清掉、React 下一帧尚未到达时，窗口会**按内容收缩**
（实测 960×620 → 302×192）。

修法：松手时**只清 `transform`**（拖动期间的位移补偿），
宽高交由 React 的 `win.rect.w/h` 维护：

```ts
el.style.transform = '';   // 只清位移
onMove(live);              // 宽高由 state 提供
```

实测（1400×900 视口）：

| 场景 | 结果 |
|------|------|
| 从标题栏最顶沿拖动 | 窗口移动，尺寸保持 960×620 |
| 常规拖动 | 尺寸保持 960×620 |
| 松手后再移动鼠标 | 不再跟随 |
| 右下角缩放 | 960×620 → 810×520（仍可用，且不低于 380×260） |

### 0.66 壁纸统一 + 修复多窗口拖动卡顿/粘连

#### 1. 登录页也使用桌面壁纸

`.fpkm-login-wrap` 复用同一张 `/wallpaper.webp`（`image-set`），
并叠一层暗色薄纱保证表单可读 —— 这张壁纸天空偏亮，
不压暗的话白色卡片与浅色文字会糊在一起。卡片通过 `z-index:1` 浮在薄纱之上。

#### 2. 拖动卡顿与"松手后仍跟随"

这是**两个独立缺陷**：

**缺陷 A：松手后窗口继续跟随鼠标**

`pointerup` 监听在 `window` 上，但鼠标拖过窗口内的 `<iframe>` 时，
事件进入 iframe 的**独立文档**，父窗口收不到 `pointerup` ——
`dragRef` 永不清空，于是窗口一直跟着鼠标。

三重保障：

1. `setPointerCapture(pointerId)`：把指针事件锁定到拖动的标题栏元素；
2. 拖动/缩放期间在 iframe 上覆盖透明遮罩 `.fpkm-win-dragshield`，
   把指针事件留在父窗口；
3. 同时监听 `pointercancel` 与 `window.blur`，失焦也结束拖动。

**缺陷 B：拖动卡顿**

原先每次 `pointermove` 都调用 `onMove` → `Desktop` 的 `setWindows`，
触发整个桌面重渲染；而商店窗口的 `inlineContent` 是每次渲染重新构建的
**整棵商店组件树**，等于每帧重建一次大型子树，窗口一多就明显掉帧。

改为拖动期间**完全不触发 React**：

```ts
// 只改窗口自身的 style，走合成层，不触发 layout / React 渲染
el.style.transform = `translate3d(${dx}px, ${dy}px, 0)`;
// 松手时一次性提交最终几何
onMove(liveRef.current)
```

另外用 `memo` 包裹 `AppWindowFrame`，避免拖动一个窗口时
其它窗口跟着重渲染。

实测（两个窗口，其中一个含大型商店子树）：

- 拖动期间 40 步 DOM 变更 **42 次**（仅窗口自身 transform/style）；
- 松手后位置稳定，不再跟随鼠标；
- 在 iframe 区域上方松手也不会"粘住"。

### 0.65 统一端口：Web 界面与网关共用 7666

此前是两个进程、两个端口：

```
:7667 → fpkm web      （UI + /api/*）
:7666 → fpkm gateway  （/app/* 转发 + /__gateway__/* 注册接口）
```

现在合并为**一个进程、一个端口**（默认 7666）。

#### 实现方式：把网关挂到 Web 的 Handler 上

网关新增 `Mount(fallback)`，Web 的 `/` 路由改为：

```
/__gateway__/*           → 网关管理接口（仅本机）
/app/*（已注册路由）      → 网关代理（含登录校验、禁止缓存）
/app/*（未注册）          → 404，明确提示"未注册/未启动"
其余路径                  → 前端 SPA
```

要点：

- **未注册的 `/app/*` 不回落到 SPA** —— 否则在 iframe 里打开未启动的应用
  会显示整个桌面，令人困惑；
- `NewEmbedded()` 让登录引导页直接用同源 `href="/"`，
  不再需要跨端口拼地址（`web.port` 机制仅保留给独立网关模式）；
- `X-Frame-Options: SAMEORIGIN` 现在**不再妨碍** iframe：
  桌面与 `/app/*` 同源。

#### 关键一致性：cfg.GatewayPort 必须等于实际监听端口

`app.Manager` 注册路由的方式是「HTTP POST 到 `127.0.0.1:cfg.GatewayPort`」，
`describeAccess` 也用它生成应用访问地址。因此启动时：

```go
if webPort > 0 { cfg.GatewayPort = webPort }   // 与监听端口对齐
// --port 0 时在 Listen 成功后回写真实端口
cfg.GatewayPort = tcpAddr.Port
```

否则用 `--port 8000` 启动会让注册请求打到错误端口，
应用启动后 `/app/*` 无法访问。

#### 启动时注册已有应用

网关不再独立扫描应用目录（它已并入本进程），
因此 `fpkm web` 启动时会扫描一次已安装应用并注册 socket/CGI 路由，
否则重启后已安装应用的 `/app/*` 会 404。

#### 升级注意

端口被占用时给出可操作的提示（旧版独立网关可能仍占着 7666）：

```
fpkm gateway --stop      # 停止旧网关
fpkm web --bind 0.0.0.0 --port 7666
```

`fpkm gateway --status` 会返回 `embedded: true`，
此时 `gateway --stop` 只提示不执行 —— 杀掉该 PID 会连带停掉 Web 界面。

### 0.64 统一网关 / CGI 应用需要登录 FPKM 才能访问

与飞牛保持一致：统一网关与 CGI 应用**必须登录后才能访问**。

#### 难点：网关是独立进程

`fpkm web` 与 `fpkm gateway --run` 是**两个进程**，而原会话表是 Web 进程的
**内存态**（`AuthManager.sessions`），网关根本读不到。同时原鉴权只认
Header / query，**没有 Cookie** —— 桌面用 iframe 打开应用时请求的是
另一个端口，Header 无法跨文档传递，等于完全没有凭证。

#### 方案：HMAC 签名的自包含令牌 + 共享密钥

新增 `internal/session`：令牌不再依赖任何进程的内存，而是
**可独立校验的签名串**：

```
<payload>.<signature>
payload = {"e":<过期时间>,"i":"<随机id>"}
signature = base64url(HMAC-SHA256(secret, payload))
```

- 密钥存放在 `$BaseDir/session.key`（**0600**），两个进程读取同一份；
- 网关本地验签，无需回调 Web 进程；
- 比较用 `subtle.ConstantTimeCompare`，避免时序侧信道。

#### Cookie 是跨端口的关键

`Cookie` **不区分端口**（只按域名/路径匹配），因此 `:8080` 登录后下发的
Cookie 会被 `:7666` 的请求一并携带。登录/设置密码时下发：

```
Set-Cookie: fpkm_session=<token>; Path=/; HttpOnly; SameSite=Lax
```

`HttpOnly` 防 XSS 窃取；`SameSite=Lax` 跨端口可用同时防御大部分 CSRF；
`Secure` 仅在 HTTPS 下置位（本服务默认 HTTP，置位会导致 Cookie 不被发送）。

令牌来源优先级：`Authorization: Bearer` → `X-FPKM-Session` →
**Cookie** → `?session=`。

#### 拦截点

鉴权门插在**路由匹配之后、任何代理/执行动作之前**，因此
HTTP / FastCGI / CGI / WebSocket 四条转发路径全部覆盖。
CGI 尤其重要：它会产生副作用，未登录绝不能执行。

未登录时的响应按请求类型区分：

| 请求类型 | 响应 |
|----------|------|
| 浏览器导航（`Accept: text/html`） | 401 + 引导页（含「前往登录」，`target="_top"` 避免在 iframe 内打转） |
| 接口调用（fetch/XHR） | 401 + JSON |
| 非 GET | 不返回 HTML 引导页 |

> 引导页的锁图标用**内联 SVG** 而非 emoji：emoji 依赖系统字体，
> 在缺少 emoji 字体的精简系统上会渲染成豆腐块（实测已复现）。

#### 顺带修掉的两个安全问题

1. **`/__gateway__/*` 管理接口原先无任何限制**。其中
   `POST /__gateway__/routes` 可以任意改写路由表（把任意路径指向任意
   socket），属于提权风险。现已用 `localOnly` 限制为**仅本机**可访问。
2. **登出后网关仍放行**（实测发现的真实缺陷）。早期实现只在启动时读一次
   吊销列表，于是 Web 进程登出后网关继续返回 200 并泄露应用内容，
   直到网关重启。现在每次校验都重新读取吊销文件，登出**立刻生效**。

其余保障：改密后调用 `RevokeAll()` **轮换签名密钥**，所有旧令牌同时失效；
若会话存储不可用，网关选择**拒绝**而不是静默放行。

#### 前端改为完全依赖 Cookie

令牌由 HttpOnly Cookie 承载后，前端**不再把令牌写进 localStorage**，
也不再用 `Authorization` 头（`src/api/client.ts`）：

- `fetch`（同源默认 `credentials: same-origin`）、`XHR`、`EventSource`、
  `<img src>` 都会自动携带 Cookie，因此无需手工附加；
- 移除 `?session=` 查询参数（原先贴在图标 URL 与 SSE 上）；
- `setSession()` 保留为空实现，仅为兼容既有调用点。

> **这修正了先前的一处不实描述**：早先文档写了"HttpOnly 防 XSS 窃取"，
> 但当时前端仍把同一个令牌存在 localStorage 里 —— JS 照样读得到，
> 该说法并不成立。现在 localStorage 中不再有令牌，这句才真正成立。
> 实测：0 个请求携带 `Authorization` 头，0 个 401，图标与商店数据均正常。

#### 顺带修复：退出登录失效

把 `setSession()` 改成空实现后，退出按钮（只做 `setSession('')`）变成**空操作** ——
点击后服务端会话依然有效（实测 `/api/apps` 仍返回 200）。已改为调用
`logout()` 接口，由服务端吊销令牌并清除 Cookie。

> 注意：退出入口目前在**商店界面**（桌面 → 打开应用商店窗口 → 右上角退出）。
> 桌面任务栏本身尚无退出按钮。

#### 修复：登出后仍能看到应用内容（浏览器缓存绕过鉴权）

**这是实测发现的第二个独立缺陷**，与"网关跑旧二进制"无关，重启也修不掉。

应用常返回 `Cache-Control: max-age=3600, public`，浏览器会把整份响应存进
HTTP 缓存。**登出后再访问时浏览器根本不发请求**，于是网关的鉴权逻辑
没有机会执行：

```
登出前：HTTP 200  "CACHED-APP-CONTENT"
登出后：HTTP 200  "CACHED-APP-CONTENT"   ← 命中缓存，绕过了网关
        cache-control: max-age=3600, public
```

鉴权只有在"**每个请求都经过网关**"时才有意义。修复方式是在网关入口
对已鉴权内容统一压掉缓存：

```go
func (g *Gateway) setNoCacheHeaders(w http.ResponseWriter, r *http.Request) {
    if isWebSocketRequest(r) { return }   // 升级请求不涉及缓存
    w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
    w.Header().Set("Pragma", "no-cache")
    w.Header().Set("Expires", "0")
}
```

放在入口处（而非各代理实现内）可一次覆盖 HTTP / FastCGI / CGI 三条路径；
HTTP 反向代理另外用 `ModifyResponse` 覆盖后端自带的 `ETag` / `Last-Modified`。

修复后实测：登出再访问得到 **401**，`cache-control: no-store`。

#### 修复：登录引导页的链接指向错误端口

网关与 Web 是**不同端口**的两个进程（实测部署为 gateway=7666、web=7667）。
引导页原先硬编码 `href="/"`，用户点「前往登录」会落到**网关根路径**，
得到 404「未找到匹配的应用路由」，等于登不进去。

修复：Web 进程在监听成功后把**真实端口**写入 `$BaseDir/web.port`
（`ListenAndServe` 支持 `--port 0`，因此取监听后的实际值），
网关读取后拼出完整地址：

```
href="http://<host>:<web端口>/"
```

未记录端口时退回 `"/"`；写入 HTML 属性前做 `html.EscapeString`，
避免 `r.Host` 被构造成注入点。

#### 部署前提

两个进程需**同一用户**运行（相同 `HOME` → 相同 `$BaseDir`），
否则读不到同一份密钥。当前部署中两者均为 root，满足该条件。

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
