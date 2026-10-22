import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Toast, Spin, Dropdown } from '@douyinfe/semi-ui';
import type { AppView, DesktopIcon, MetaInfo } from '../api/types';
import { getSystemStats, iconURL, type SystemStats } from '../api/client';
import { buildAccessURL } from '../utils/format';
import AppWindowFrame, { type AppWindow, type WindowRect } from './AppWindowFrame';
import Taskbar from './Taskbar';
import { useDock } from './dock';
import { clampRect, loadWindowRect, saveWindowRect } from './windowRect';
import Widgets from './Widgets';

interface Props {
  apps: AppView[];
  meta: MetaInfo | null;
  loading: boolean;
  /**
   * 渲染应用商店内容。
   *
   * 由 App 传入，**在同一个 React 树内**渲染（不是 iframe）——
   * iframe 是独立文档，会重新下载并执行整个 SPA，打开很慢。
   * 参数是"关闭商店窗口"的回调，供商店内的「返回桌面」按钮使用。
   */
  renderStore: (
    closeStoreWindow: () => void,
    /** 按应用名打开（商店里的「打开」按钮复用桌面的打开逻辑） */
    onOpenApp: (appName: string) => void,
  ) => React.ReactNode;
  /** 全屏打开应用商店（兜底入口） */
  onOpenStore: () => void;
  /** 启动应用；resolve 为 true 表示启动成功 */
  onStartApp: (name: string) => Promise<boolean>;
  /** 打开应用详情 */
  onOpenDetail: (name: string) => void;
}

const STORE_ICON: DesktopIcon = {
  key: 'builtin:store',
  label: '应用商店',
  icon: '/fpkm-logo.png',
  // 商店不是 iframe，而是在桌面窗口内**直接挂载 React 组件**。
  // target 仅作为占位（窗口组件用它判断是否为内置内容）。
  launchMode: 'iframe',
  target: null,
  builtin: 'store',
};


const DEFAULT_W = 960;
const DEFAULT_H = 620;

/** 任务栏（Dock）高度兜底值：与 desktop.css 的 .fpkm-dock height 一致（76px） */
const DOCK_FALLBACK_H = 76;

/** 窗口可用区域的最小高度：极矮窗口下不把可用区算成 0 或负数 */
const MIN_USABLE_H = 200;

/**
 * 应用商店窗口的初始尺寸（目标是应用列表稳排 3 列）。
 *
 * 实测出来的数字，别凭理论拍：
 *   .fpkm-grid 用 repeat(auto-fill, minmax(320px, 1fr))，gap 14px。
 *   理论 3 列需 3×320 + 2×14 = 988px，但浏览器取整后
 *   **网格宽度必须 ≥ 1000px 才真的排 3 列**（986px 仍是 2 列）。
 *
 * 窗口内还要减去：
 *   208px 侧边栏 + 内容区左右 padding（约 44px）
 *
 * 因此窗口宽度需 ≥ 1000 + 208 + 44 ≈ 1252，这里取 1320 留些余量
 *（也考虑到滚动条占位）。默认 960 宽只能排 2 列，故商店单独给更宽初值。
 */
const STORE_W = 1320;
const STORE_H = 820;

/** 级联偏移：每多开一个窗口，右上向下错开的距离 */
const CASCADE_STEP = 30;
/** 级联错开到第几个后回到起点，避免一直往右下跑出屏幕 */
const CASCADE_WRAP = 8;

/** FPKM 桌面：壁纸 + 图标网格 + 子窗口 + 玻璃任务栏 + 小组件 */
export default function Desktop({
  apps,
  meta,
  loading,
  renderStore,
  onOpenStore,
  onStartApp,
  onOpenDetail,
}: Props) {
  const [windows, setWindows] = useState<AppWindow[]>([]);
  const [zTop, setZTop] = useState(10);
  const [selected, setSelected] = useState<string | null>(null);
  const [stats, setStats] = useState<SystemStats | null>(null);
  const desktopRef = useRef<HTMLDivElement>(null);
  /** 整个桌面（含任务栏区域）：窗口的可拖放边界 */
  const rootRef = useRef<HTMLDivElement>(null);
  const [bounds, setBounds] = useState({ w: 1280, h: 720 });

  /* 任务栏（Dock）隐藏列表：与商店「已安装」列表共享同一份状态，
     这样从任务栏移除后仍可在商店里恢复。 */
  const dock = useDock();

  /**
   * 窗口可用区域 = 整个桌面**减去任务栏（Dock）占用的底部高度**。
   *
   * 历史与取舍：
   *   - 最初用 .fpkm-desktop-area（inset 0 0 76px 0）当 bounds，但该容器
   *     overflow:hidden 会把窗口超出部分裁掉；
   *   - 后来改成"bounds = 整个桌面"，让窗口可以盖到任务栏位置。任务栏
   *     z-index（9000）高于窗口层，表面上没问题，但窗口底部约一栏高的区域
   *     永远压在任务栏下面：内容滚到底也翻不出来，表现为「手机窄屏 /
   *     小窗口下，任务栏把日志正文的最后几行盖住」。
   *
   * 现在的约定：
   *   - 窗口层与任务栏仍是 .fpkm-desktop 的同级子元素（不受父级 overflow 裁剪）；
   *   - bounds 高度 = 桌面高度 − 任务栏实测高度，最大化 / 贴底的窗口不会
   *     再伸到任务栏下面；
   *   - 任务栏 z-index 依旧高于窗口层，保证它**永远在最顶层**。
   */
  const measure = useCallback(() => {
    const el = rootRef.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    // 任务栏高度按 DOM 实测：CSS 里固定 76px，将来窄屏样式若调整，
    // 这里跟着变，不会出现"算出来的可用区与实际不一致"。
    const dock = el.querySelector('.fpkm-dock') as HTMLElement | null;
    const dockH = dock ? Math.round(dock.getBoundingClientRect().height) : DOCK_FALLBACK_H;
    setBounds({
      w: Math.round(r.width),
      h: Math.max(MIN_USABLE_H, Math.round(r.height - dockH)),
    });
  }, []);

  useEffect(() => {
    measure();
    window.addEventListener('resize', measure);
    // 部分 WebView / 手机横竖屏切换只触发 orientationchange（不触发 resize），补一档
    window.addEventListener('orientationchange', measure);
    return () => {
      window.removeEventListener('resize', measure);
      window.removeEventListener('orientationchange', measure);
    };
  }, [measure]);

  /** 系统状态轮询（小组件）：进桌面后每 5 秒刷新一次 */
  useEffect(() => {
    let alive = true;
    const tick = () => {
      getSystemStats()
        .then((s) => {
          if (alive) setStats(s);
        })
        .catch(() => undefined);
    };
    tick();
    const t = window.setInterval(tick, 5000);
    return () => {
      alive = false;
      window.clearInterval(t);
    };
  }, []);

  /**
   * 全部图标：内置商店 + 所有已安装应用。
   * Dock 用它（macOS 的 Dock 常驻显示已安装应用）。
   */
  const allIcons: DesktopIcon[] = useMemo(() => {
    const list: DesktopIcon[] = [STORE_ICON];
    for (const a of apps) {
      // 运行时依赖（python312 / nodejs_v24 / bunjs 等）只提供解释器，
      // 没有可打开的界面。放进桌面或任务栏只会得到一个点开空白的图标，
      // 因此直接跳过；它们仍在商店「已安装」列表中可管理。
      if (a.runtimeDep) continue;
      list.push({
        key: `app:${a.name}`,
        label: a.access?.title || a.displayName || a.name,
        icon: iconURL(a.name),
        launchMode: (a.access?.launchMode as 'iframe' | 'tab') || 'tab',
        target: buildAccessURL(a.access, meta?.publicHost, meta?.publicScheme),
        appName: a.name,
        running: a.running,
      });
    }
    return list;
  }, [apps, meta]);

  /**
   * 桌面图标：内置商店 + **正在运行**的应用。
   *
   * 只显示运行中的应用 —— 未成功运行的应用点开也没有内容，
   * 摆在桌面上只会让人以为能直接打开。未运行的应用仍在 Dock 与
   * 商店的「已安装」列表里可以找到并启动。
   */
  const icons: DesktopIcon[] = useMemo(
    () => allIcons.filter((ic) => ic.builtin === 'store' || ic.running),
    [allIcons],
  );

  const focusWindow = useCallback((id: string) => {
    setZTop((z) => {
      const nz = z + 1;
      setWindows((ws) => ws.map((w) => (w.id === id ? { ...w, z: nz, minimized: false } : w)));
      return nz;
    });
  }, []);

  const closeWindow = useCallback((id: string) => {
    setWindows((ws) => ws.filter((w) => w.id !== id));
  }, []);

  /**
   * 更新窗口几何，并持久化到 localStorage。
   *
   * 只在"非最大化"时保存：最大化状态下 rect 是整屏尺寸，
   * 写进去会导致下次打开该应用也变成全屏。
   */
  const updateRect = useCallback((id: string, rect: WindowRect) => {
    setWindows((ws) => {
      const target = ws.find((w) => w.id === id);
      if (target && !target.maximized) {
        saveWindowRect(id, rect);
      }
      return ws.map((w) => (w.id === id ? { ...w, rect } : w));
    });
  }, []);

  const toggleMaximize = useCallback(
    (id: string) => {
      setWindows((ws) =>
        ws.map((w) => {
          if (w.id !== id) return w;
          if (w.maximized) {
            // 还原：把还原后的几何写回存储
            const back = w.restore ?? w.rect;
            saveWindowRect(id, back);
            return { ...w, maximized: false, rect: back, restore: undefined };
          }
          // 进入最大化前，记下"普通状态"的几何，
          // 这样下次打开仍是用户调整过的大小
          saveWindowRect(id, w.rect);
          return {
            ...w,
            maximized: true,
            restore: w.rect,
            rect: { x: 0, y: 0, w: bounds.w, h: bounds.h },
          };
        }),
      );
    },
    [bounds.w, bounds.h],
  );

  /** 在桌面上打开（或聚焦）一个应用窗口 */
  const openWindow = useCallback(
    (ic: DesktopIcon) => {
      const existing = windows.find((w) => w.id === ic.key);
      if (existing) {
        focusWindow(ic.key);
        return;
      }

      const isStore = ic.builtin === 'store';
      // 商店用更宽的初始尺寸，保证应用列表能排 3 列；其它应用用默认值
      const wantW = isStore ? STORE_W : DEFAULT_W;
      const wantH = isStore ? STORE_H : DEFAULT_H;
      const w = Math.min(wantW, bounds.w - 40);
      const h = Math.min(wantH, bounds.h - 40);

      // 优先用上次保存的几何（同一应用再次打开时回到原位置）
      const saved = loadWindowRect(ic.key);

      /**
       * 级联定位：不重叠 + 固定错开量。
       *
       * 只"按各自尺寸居中 + 加偏移"是不够的：不同尺寸的窗口
       * （如商店 1320 宽、普通应用 960 宽）居中基线本身就不同，
       * 叠加偏移后错开量会变成 200px 以上，看起来不像级联。
       *
       * 这里改为：以**第一个窗口的位置**为基准，第 n 个窗口
       * 相对它固定错开 n×30px。没有已开窗口时才用居中基线。
       */
      const step = (windows.length % CASCADE_WRAP) * CASCADE_STEP;

      // 已有窗口时，以最早那个窗口为锚点，保证错开量稳定
      const anchor = windows.length > 0 ? windows[0].rect : null;
      const baseX = anchor
        ? anchor.x
        : Math.max(12, (bounds.w - w) / 2 - 60);
      const baseY = anchor
        ? anchor.y
        : Math.max(8, (bounds.h - h) / 2 - 30);

      const x = Math.min(Math.max(0, baseX + step), Math.max(0, bounds.w - w));
      const y = Math.min(Math.max(0, baseY + step), Math.max(0, bounds.h - h));

      const rect = saved ? clampRect(saved, bounds) : { x, y, w, h };

      // 打开即记录：需求要求"子窗口的大小和位置"被记住，
      // 不能只在用户拖动/缩放之后才写。已有记录的窗口打开时
      // 写入的是同一个值（幂等），不会产生抖动。
      saveWindowRect(ic.key, rect);

      const nz = zTop + 1;
      setZTop(nz);
      setWindows((ws) => [
        ...ws,
        {
          id: ic.key,
          title: ic.label,
          icon: ic.icon,
          url: ic.target ?? '',
          // 商店：打标记，渲染时再生成内容（避免缓存成陈旧快照）
          builtin: isStore ? 'store' : undefined,
          rect,
          z: nz,
          minimized: false,
          maximized: false,
          loading: !isStore,
          failed: false,
        },
      ]);
    },
    [windows, bounds.w, bounds.h, zTop, focusWindow],
  );

  /** 打开一个图标：商店走内部视图，应用按 launchMode 开子窗口或新标签页 */
  const openIcon = useCallback(
    async (ic: DesktopIcon) => {
      // 内置应用商店：在桌面窗口内直接渲染（非 iframe）
      if (ic.builtin === 'store') {
        openWindow(ic);
        return;
      }
      if (!ic.appName) return;

      if (!ic.target) {
        Toast.warning('该应用暂无可访问地址');
        return;
      }

      const app = apps.find((a) => a.name === ic.appName);
      const notRunning = !!app && !app.running;

      // ---- type=url：新浏览器标签页 ----
      if (ic.launchMode === 'tab') {
        if (notRunning) {
          // 必须同步 window.open，否则会被弹窗拦截器拦下；
          // 因此这里先启动，让用户稍后再点一次。
          Toast.info(`正在启动 ${ic.label}，完成后请再次点击打开`);
          void onStartApp(ic.appName);
          return;
        }
        window.open(ic.target, '_blank', 'noopener');
        return;
      }

      // ---- type=iframe：桌面子窗口 ----
      if (notRunning) {
        Toast.info(`正在启动 ${ic.label}…`);
        const ok = await onStartApp(ic.appName);
        if (!ok) {
          Toast.error(`${ic.label} 启动失败，请查看任务日志`);
          return;
        }
      }

      openWindow(ic);
    },
    [apps, windows, focusWindow, openWindow, onStartApp],
  );

  /**
   * 按应用名打开，供商店里的「打开」按钮复用。
   *
   * 这样商店与桌面图标走**同一条**打开逻辑：
   *   - ui/config 里 type=iframe → 桌面子窗口
   *   - type=url                → 新浏览器标签页
   * 而不是商店里一律 window.open（那样会把 iframe 应用开成独立标签页）。
   */
  const openAppByName = useCallback(
    (appName: string) => {
      const ic = allIcons.find((x) => x.appName === appName);
      if (!ic) {
        Toast.warning('未找到该应用，请稍后重试');
        return;
      }
      void openIcon(ic);
    },
    [allIcons, openIcon],
  );

  /** 任务栏应显示的图标：排除用户移除的，且只显示已安装应用 + 商店 */
  const dockIcons = useMemo(
    () => allIcons.filter((ic) => ic.builtin === 'store' || !dock.isHidden(ic.appName ?? '')),
    [allIcons, dock],
  );

  /** 把应用从任务栏移除（右键任务栏图标） */
  const removeFromDock = useCallback(
    (appName: string) => {
      dock.hide(appName);
      Toast.info('已从任务栏移除，可在应用商店的「已安装」里恢复');
    },
    [dock],
  );

  /** 恢复应用到任务栏 */
  const addToDock = useCallback(
    (appName: string) => {
      dock.show(appName);
      Toast.success('已保留在任务栏');
    },
    [dock],
  );

  const activeId = useMemo(() => {
    const visible = windows.filter((w) => !w.minimized);
    if (!visible.length) return null;
    return visible.reduce((a, b) => (a.z > b.z ? a : b)).id;
  }, [windows]);

  return (
    <div ref={rootRef} className={`fpkm-desktop ${selected ? 'fpkm-desktop-selected' : ''}`}>
      {/* 桌面区域：图标网格 + 子窗口 */}
      <div className="fpkm-desktop-area" ref={desktopRef} onClick={() => setSelected(null)}>
        {/* 图标网格：先排满第一列再排第二列（grid-auto-flow: column） */}
        <div className="fpkm-desktop-icons">
          {icons.map((ic) => {
            const inDock = ic.builtin === 'store' || !dock.isHidden(ic.appName ?? '');

            const btn = (
              <button
                className={`fpkm-desk-icon ${selected === ic.key ? 'fpkm-desk-icon-selected' : ''}`}
                onClick={(e) => {
                  e.stopPropagation();
                  setSelected(ic.key);
                  // 单击直接打开（符合桌面习惯）
                  void openIcon(ic);
                }}
                title={ic.builtin === 'store' ? '应用商店' : ic.label}
              >
                <span className="fpkm-desk-icon-img">
                  <img src={ic.icon} alt={ic.label} loading="lazy" />
                </span>
                <span className="fpkm-desk-icon-label">{ic.label}</span>
              </button>
            );

            // 商店没有应用名，右键只给"打开"
            if (ic.builtin === 'store' || !ic.appName) {
              return (
                <Dropdown
                  key={ic.key}
                  trigger="contextMenu"
                  position="bottomLeft"
                  clickToHide
                  render={
                    <Dropdown.Menu>
                      <Dropdown.Item onClick={() => void openIcon(ic)}>打开</Dropdown.Item>
                    </Dropdown.Menu>
                  }
                >
                  <span className="fpkm-desk-slot">{btn}</span>
                </Dropdown>
              );
            }

            return (
              <Dropdown
                key={ic.key}
                trigger="contextMenu"
                position="bottomLeft"
                clickToHide
                render={
                  <Dropdown.Menu>
                    <Dropdown.Item onClick={() => void openIcon(ic)}>打开</Dropdown.Item>
                    {/* 从任务栏移除后，这里变成恢复入口 */}
                    {inDock ? (
                      <Dropdown.Item onClick={() => removeFromDock(ic.appName!)}>
                        从任务栏移除
                      </Dropdown.Item>
                    ) : (
                      <Dropdown.Item onClick={() => addToDock(ic.appName!)}>
                        保留在任务栏
                      </Dropdown.Item>
                    )}
                    <Dropdown.Item onClick={() => onOpenDetail(ic.appName!)}>
                      应用详情
                    </Dropdown.Item>
                  </Dropdown.Menu>
                }
              >
                <span className="fpkm-desk-slot">{btn}</span>
              </Dropdown>
            );
          })}
        </div>

        {loading && apps.length === 0 ? (
          <div className="fpkm-desktop-loading">
            <Spin size="large" tip="正在加载应用…" />
          </div>
        ) : null}


        {/* 右侧小组件（像图片里那样贴在桌面右上） */}
        <Widgets stats={stats} apps={apps} />
      </div>

      {/* 子窗口层：与任务栏同级（不在 .fpkm-desktop-area 内）。
          原因：
            1. 那个容器 overflow:hidden，会把拖到底部的窗口裁掉；
            2. 本层铺满桌面（inset:0），窗口位置由 bounds 约束 ——
               bounds 已扣除任务栏高度，因此窗口（含最大化）不会伸到
               任务栏下面，任务栏也就不会盖住窗口内容。
          任务栏的 z-index 高于本层，始终在最顶层。 */}
      <div className="fpkm-window-layer">
        {windows.map((w) => (
          <AppWindowFrame
            key={w.id}
            win={w}
            active={w.id === activeId}
            bounds={bounds}
            inlineContent={
              w.builtin === 'store'
                ? renderStore(() => closeWindow(w.id), openAppByName)
                : undefined
            }
            onFocus={() => focusWindow(w.id)}
            onClose={() => closeWindow(w.id)}
            onMinimize={() => setWindows((ws) => ws.map((x) => (x.id === w.id ? { ...x, minimized: true } : x)))}
            onToggleMaximize={() => toggleMaximize(w.id)}
            onMove={(r) => updateRect(w.id, r)}
            onLoaded={() =>
              setWindows((ws) => ws.map((x) => (x.id === w.id ? { ...x, loading: false } : x)))
            }
            onFailed={() =>
              setWindows((ws) => ws.map((x) => (x.id === w.id ? { ...x, loading: false, failed: true } : x)))
            }
          />
        ))}
      </div>

      {/* 玻璃质感任务栏 */}
      <Taskbar
        icons={dockIcons}
        windows={windows}
        activeId={activeId}
        onOpenIcon={openIcon}
        onRemoveFromDock={removeFromDock}
        onOpenStore={onOpenStore}
        onFocusWindow={focusWindow}
        onRestoreAll={() => setWindows((ws) => ws.map((w) => ({ ...w, minimized: false })))}
        onCloseAll={() => setWindows([])}
      />
    </div>
  );
}
