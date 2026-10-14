import { Dropdown } from '@douyinfe/semi-ui';
import type { DesktopIcon } from '../api/types';
import type { AppWindow } from './AppWindowFrame';

interface Props {
  /** 任务栏上固定的图标（商店 + 已安装应用，已排除用户移除的） */
  icons: DesktopIcon[];
  /** 当前已打开的窗口 */
  windows: AppWindow[];
  activeId: string | null;
  /** 单击图标：打开应用或聚焦其窗口 */
  onOpenIcon: (ic: DesktopIcon) => void;
  /** 右键任务栏图标 → 从任务栏移除 */
  onRemoveFromDock: (appName: string) => void;
  onOpenStore: () => void;
  onFocusWindow: (id: string) => void;
  onRestoreAll: () => void;
  onCloseAll: () => void;
}

/**
 * macOS 风格 Dock。
 *
 * - 已安装应用常驻显示（只显示图标，不显示文字）；
 * - 单击打开/聚焦；
 * - 右键可「从任务栏移除」（在桌面右键该图标可恢复）；
 * - 应用有打开的窗口时，图标下方显示一个小圆点。
 *
 * 注：windows / activeId 等 props 保留是为了将来做窗口预览，
 * 当前 Dock 只用它们计算小圆点与高亮，故未全部解构使用。
 */
export default function Taskbar({ icons, windows, activeId, onOpenIcon, onRemoveFromDock }: Props) {
  /** 该应用是否有打开的窗口（含已最小化） */
  const isOpen = (ic: DesktopIcon) => windows.some((w) => w.id === ic.key);

  /** 是否为当前活动窗口（用于高亮） */
  const isActive = (ic: DesktopIcon) =>
    windows.some((w) => w.id === ic.key && w.id === activeId && !w.minimized);

  return (
    <div className="fpkm-dock">
      <div className="fpkm-dock-inner">
        {icons.map((ic) => {
          const open = isOpen(ic);
          const btn = (
            <button
              className={`fpkm-dock-item ${isActive(ic) ? 'fpkm-dock-item-active' : ''}`}
              onClick={() => onOpenIcon(ic)}
              aria-label={ic.label}
              /* 用原生 title 而非 Tooltip：Semi 的 Tooltip 会渲染到 portal 里，
                 其 .semi-portal-inner 会盖住图标并截获鼠标事件，
                 导致右键菜单点不出来。原生 title 没有这个问题。 */
              title={ic.label}
            >
              <img src={ic.icon} alt={ic.label} className="fpkm-dock-icon" />
              {open ? <span className="fpkm-dock-dot" /> : null}
            </button>
          );

          // 商店是内置入口，不允许从任务栏移除
          if (ic.builtin === 'store' || !ic.appName) {
            return (
              <span key={ic.key} className="fpkm-dock-slot">
                {btn}
              </span>
            );
          }

          return (
            <Dropdown
              key={ic.key}
              trigger="contextMenu"
              position="top"
              clickToHide
              render={
                <Dropdown.Menu>
                  <Dropdown.Item onClick={() => onRemoveFromDock(ic.appName!)}>
                    从任务栏移除
                  </Dropdown.Item>
                </Dropdown.Menu>
              }
            >
              <span className="fpkm-dock-slot">{btn}</span>
            </Dropdown>
          );
        })}
      </div>
    </div>
  );
}
