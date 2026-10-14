import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Toast, Spin, Dropdown } from '@douyinfe/semi-ui';
import type { AppView, DesktopIcon, MetaInfo } from '../api/types';
import { getSystemStats, iconURL, type SystemStats } from '../api/client';
import { buildAccessURL } from '../utils/format';
import AppWindowFrame, { type AppWindow, type WindowRect } from './AppWindowFrame';
import Taskbar from './Taskbar';
import Widgets from './Widgets';

interface Props {
  apps: AppView[];
  meta: MetaInfo | null;
  loading: boolean;
  /** 打开应用商店（内部视图，供"返回桌面"等场景使用） */
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
  // 商店在桌面子窗口内打开（/?embed=store 只渲染商店外壳），
  // 这样关掉/最小化窗口就能回到桌面。
  launchMode: 'iframe',
  target: '/?embed=store',
  builtin: 'store',
};

/** 任务栏隐藏列表的存储键（用户从任务栏移除的应用） */
const DOCK_HIDDEN_KEY = 'fpkm.dock.hidden';

const DEFAULT_W = 960;
const DEFAULT_H = 620;

/** FPKM 桌面：壁纸 + 图标网格 + 子窗口 + 玻璃任务栏 + 小组件 */
export default function Desktop({ apps, meta, loading, onOpenStore, onStartApp, onOpenDetail }: Props) {
  const [windows, setWindows] = useState<AppWindow[]>([]);
  const [zTop, setZTop] = useState(10);
  const [selected, setSelected] = useState<string | null>(null);
  const [stats, setStats] = useState<SystemStats | null>(null);
  const desktopRef = useRef<HTMLDivElement>(null);
  const [bounds, setBounds] = useState({ w: 1280, h: 720 });

  /**
   * 任务栏（Dock）隐藏的应用。
   * 用户可以右键任务栏图标「从任务栏移除」，该应用名会记在这里；
   * 再在桌面右键该图标「保留在任务栏」即可恢复。
   * 持久化到 localStorage，刷新后保持。
   */
  const [dockHidden, setDockHidden] = useState<string[]>(() => {
    try {
      const raw = localStorage.getItem(DOCK_HIDDEN_KEY);
      const arr = raw ? JSON.parse(raw) : [];
      return Array.isArray(arr) ? arr.filter((x) => typeof x === 'string') : [];
    } catch {
      return [];
    }
  });

  const setDockHiddenPersist = useCallback((next: string[]) => {
    setDockHidden(next);
    try {
      localStorage.setItem(DOCK_HIDDEN_KEY, JSON.stringify(next));
    } catch {
      /* localStorage 不可用时忽略 */
    }
  }, []);

  /** 桌面可用区域（减去任务栏高度） */
  const measure = useCallback(() => {
    const el = desktopRef.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    setBounds({ w: Math.round(r.width), h: Math.round(r.height) });
  }, []);

  useEffect(() => {
    measure();
    window.addEventListener('resize', measure);
    return () => window.removeEventListener('resize', measure);
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

  /** 桌面图标：内置商店 + 已安装应用 */
  const icons: DesktopIcon[] = useMemo(() => {
    const list: DesktopIcon[] = [STORE_ICON];
    for (const a of apps) {
      const url = buildAccessURL(a.access, meta?.publicHost, meta?.publicScheme);
      list.push({
        key: `app:${a.name}`,
        label: a.access?.title || a.displayName || a.name,
        icon: iconURL(a.name),
        launchMode: (a.access?.launchMode as 'iframe' | 'tab') || 'tab',
        target: url,
        appName: a.name,
        running: a.running,
      });
    }
    return list;
  }, [apps, meta]);

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

  const updateRect = useCallback((id: string, rect: WindowRect) => {
    setWindows((ws) => ws.map((w) => (w.id === id ? { ...w, rect } : w)));
  }, []);

  const toggleMaximize = useCallback(
    (id: string) => {
      setWindows((ws) =>
        ws.map((w) => {
          if (w.id !== id) return w;
          if (w.maximized) {
            return { ...w, maximized: false, rect: w.restore ?? w.rect, restore: undefined };
          }
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
      const w = Math.min(DEFAULT_W, bounds.w - 40);
      const h = Math.min(DEFAULT_H, bounds.h - 40);
      const nz = zTop + 1;
      setZTop(nz);
      setWindows((ws) => [
        ...ws,
        {
          id: ic.key,
          title: ic.label,
          icon: ic.icon,
          url: ic.target ?? '',
          rect: {
            x: Math.max(12, (bounds.w - w) / 2 - 60),
            y: Math.max(8, (bounds.h - h) / 2 - 30),
            w,
            h,
          },
          z: nz,
          minimized: false,
          maximized: false,
          loading: true,
          failed: false,
        },
      ]);
    },
    [windows, bounds.w, bounds.h, zTop, focusWindow],
  );

  /** 打开一个图标：商店走内部视图，应用按 launchMode 开子窗口或新标签页 */
  const openIcon = useCallback(
    async (ic: DesktopIcon) => {
      // 内置应用商店：也在桌面子窗口里打开（target 已是 /?embed=store）
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

  /** 任务栏应显示的图标：排除用户移除的，且只显示已安装应用 + 商店 */
  const dockIcons = useMemo(
    () => icons.filter((ic) => ic.builtin === 'store' || !dockHidden.includes(ic.appName ?? '')),
    [icons, dockHidden],
  );

  /** 把应用从任务栏移除（右键任务栏图标） */
  const removeFromDock = useCallback(
    (appName: string) => {
      setDockHiddenPersist([...new Set([...dockHidden, appName])]);
      Toast.info('已从任务栏移除，可在桌面右键图标恢复');
    },
    [dockHidden, setDockHiddenPersist],
  );

  /** 恢复应用到任务栏（桌面右键图标） */
  const addToDock = useCallback(
    (appName: string) => {
      setDockHiddenPersist(dockHidden.filter((n) => n !== appName));
      Toast.success('已保留在任务栏');
    },
    [dockHidden, setDockHiddenPersist],
  );

  const activeId = useMemo(() => {
    const visible = windows.filter((w) => !w.minimized);
    if (!visible.length) return null;
    return visible.reduce((a, b) => (a.z > b.z ? a : b)).id;
  }, [windows]);

  return (
    <div className={`fpkm-desktop ${selected ? 'fpkm-desktop-selected' : ''}`}>
      {/* 桌面区域：图标网格 + 子窗口 */}
      <div className="fpkm-desktop-area" ref={desktopRef} onClick={() => setSelected(null)}>
        {/* 图标网格：先排满第一列再排第二列（grid-auto-flow: column） */}
        <div className="fpkm-desktop-icons">
          {icons.map((ic) => {
            const inDock = ic.builtin === 'store' || !dockHidden.includes(ic.appName ?? '');

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
                  {ic.running ? <span className="fpkm-desk-icon-dot" /> : null}
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

        {windows.map((w) => (
          <AppWindowFrame
            key={w.id}
            win={w}
            active={w.id === activeId}
            bounds={bounds}
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

        {/* 右侧小组件（像图片里那样贴在桌面右上） */}
        <Widgets stats={stats} apps={apps} />
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
