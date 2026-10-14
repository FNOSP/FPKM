import { Tooltip } from '@douyinfe/semi-ui';
import {
  IconGridView,
  IconMinus,
  IconClose,
  IconAppStore,
} from './icons';
import type { AppWindow } from './AppWindowFrame';

interface Props {
  windows: AppWindow[];
  activeId: string | null;
  onOpenStore: () => void;
  onFocusWindow: (id: string) => void;
  onRestoreAll: () => void;
  onCloseAll: () => void;
}

/** 玻璃质感任务栏：中间是程序按钮，右侧是窗口列表 */
export default function Taskbar({
  windows,
  activeId,
  onOpenStore,
  onFocusWindow,
  onRestoreAll,
  onCloseAll,
}: Props) {
  const hasMinimized = windows.some((w) => w.minimized);

  return (
    <div className="fpkm-taskbar">
      <div className="fpkm-taskbar-inner">
        {/* 左侧：应用商店入口 */}
        <div className="fpkm-taskbar-left">
          <Tooltip content="应用商店" position="top">
            <button className="fpkm-taskbar-btn" onClick={onOpenStore} aria-label="应用商店">
              <IconAppStore />
            </button>
          </Tooltip>
        </div>

        {/* 中间：已打开的窗口 */}
        <div className="fpkm-taskbar-center">
          {windows.length === 0 ? (
            <span className="fpkm-taskbar-hint">双击桌面图标打开应用</span>
          ) : (
            windows.map((w) => (
              <Tooltip key={w.id} content={w.title} position="top">
                <button
                  className={`fpkm-taskbar-task ${w.id === activeId && !w.minimized ? 'fpkm-taskbar-task-active' : ''}`}
                  onClick={() => onFocusWindow(w.id)}
                >
                  <img src={w.icon} alt="" className="fpkm-taskbar-task-icon" />
                  <span className="fpkm-taskbar-task-label">{w.title}</span>
                </button>
              </Tooltip>
            ))
          )}
        </div>

        {/* 右侧：窗口操作 */}
        <div className="fpkm-taskbar-right">
          {hasMinimized ? (
            <Tooltip content="显示全部窗口" position="top">
              <button className="fpkm-taskbar-btn" onClick={onRestoreAll} aria-label="显示全部窗口">
                <IconGridView />
              </button>
            </Tooltip>
          ) : null}
          {windows.length > 0 ? (
            <Tooltip content="最小化全部" position="top">
              <button className="fpkm-taskbar-btn" onClick={onCloseAll} aria-label="最小化全部">
                <IconMinus />
              </button>
            </Tooltip>
          ) : null}
        </div>
      </div>
    </div>
  );
}
