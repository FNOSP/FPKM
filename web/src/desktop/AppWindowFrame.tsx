import { useCallback, useEffect, useRef, useState } from 'react';
import { Button, Spin, Tooltip, Typography } from '@douyinfe/semi-ui';
import {
  IconMinus,
  IconMaximize,
  IconRestore,
  IconClose,
  IconRefresh,
} from '@douyinfe/semi-icons';

const { Text } = Typography;

/** 子窗口的几何信息 */
export interface WindowRect {
  x: number;
  y: number;
  w: number;
  h: number;
}

/** 一个已打开的应用窗口 */
export interface AppWindow {
  /** 唯一 id（用 icon key） */
  id: string;
  title: string;
  icon: string;
  url: string;
  rect: WindowRect;
  z: number;
  minimized: boolean;
  maximized: boolean;
  /** 最大化前的几何，用于还原 */
  restore?: WindowRect;
  loading: boolean;
  /** 加载失败（多为对方禁止被 iframe 嵌入） */
  failed: boolean;
}

interface Props {
  win: AppWindow;
  active: boolean;
  onFocus: () => void;
  onClose: () => void;
  onMinimize: () => void;
  onToggleMaximize: () => void;
  onMove: (rect: WindowRect) => void;
  onLoaded: () => void;
  onFailed: () => void;
  /** 桌面可用区域尺寸，用于约束拖动范围 */
  bounds: { w: number; h: number };
}

const MIN_W = 380;
const MIN_H = 260;

/** 可拖动、可缩放的 iframe 子窗口（参考飞牛桌面的应用窗口） */
export default function AppWindowFrame({
  win,
  active,
  onFocus,
  onClose,
  onMinimize,
  onToggleMaximize,
  onMove,
  onLoaded,
  onFailed,
  bounds,
}: Props) {
  const [dragging, setDragging] = useState(false);
  const [resizing, setResizing] = useState<string | null>(null);
  const dragRef = useRef<{ dx: number; dy: number } | null>(null);
  const resizeRef = useRef<{ startX: number; startY: number; rect: WindowRect; dir: string } | null>(null);
  const iframeRef = useRef<HTMLIFrameElement>(null);

  /** 指针移动：拖动或缩放 */
  const onPointerMove = useCallback(
    (e: PointerEvent) => {
      if (dragRef.current) {
        const { dx, dy } = dragRef.current;
        const nx = Math.min(Math.max(0, e.clientX - dx), Math.max(0, bounds.w - win.rect.w));
        const ny = Math.min(Math.max(0, e.clientY - dy), Math.max(0, bounds.h - win.rect.h));
        onMove({ ...win.rect, x: nx, y: ny });
        return;
      }
      const r = resizeRef.current;
      if (r) {
        const ddx = e.clientX - r.startX;
        const ddy = e.clientY - r.startY;
        let { x, y, w, h } = r.rect;

        if (r.dir.includes('e')) w = Math.max(MIN_W, r.rect.w + ddx);
        if (r.dir.includes('s')) h = Math.max(MIN_H, r.rect.h + ddy);
        if (r.dir.includes('w')) {
          const nw = Math.max(MIN_W, r.rect.w - ddx);
          x = r.rect.x + (r.rect.w - nw);
          w = nw;
        }
        if (r.dir.includes('n')) {
          const nh = Math.max(MIN_H, r.rect.h - ddy);
          y = r.rect.y + (r.rect.h - nh);
          h = nh;
        }
        // 不越出桌面
        w = Math.min(w, bounds.w - x);
        h = Math.min(h, bounds.h - y);
        onMove({ x, y, w, h });
      }
    },
    [bounds.w, bounds.h, win.rect, onMove],
  );

  const stop = useCallback(() => {
    dragRef.current = null;
    resizeRef.current = null;
    setDragging(false);
    setResizing(null);
  }, []);

  useEffect(() => {
    if (!dragging && !resizing) return;
    window.addEventListener('pointermove', onPointerMove);
    window.addEventListener('pointerup', stop);
    return () => {
      window.removeEventListener('pointermove', onPointerMove);
      window.removeEventListener('pointerup', stop);
    };
  }, [dragging, resizing, onPointerMove, stop]);

  const startDrag = (e: React.PointerEvent) => {
    if (win.maximized) return;
    onFocus();
    dragRef.current = { dx: e.clientX - win.rect.x, dy: e.clientY - win.rect.y };
    setDragging(true);
  };

  const startResize = (dir: string) => (e: React.PointerEvent) => {
    e.stopPropagation();
    onFocus();
    resizeRef.current = { startX: e.clientX, startY: e.clientY, rect: win.rect, dir };
    setResizing(dir);
  };

  const reload = () => {
    const el = iframeRef.current;
    if (el) el.src = win.url;
  };

  if (win.minimized) return null;

  return (
    <div
      className={`fpkm-win ${active ? 'fpkm-win-active' : ''}`}
      style={{
        left: win.rect.x,
        top: win.rect.y,
        width: win.rect.w,
        height: win.rect.h,
        zIndex: win.z,
      }}
      onPointerDown={onFocus}
    >
      {/* 标题栏：可拖动 */}
      <div className="fpkm-win-bar" onPointerDown={startDrag} onDoubleClick={onToggleMaximize}>
        <div className="fpkm-win-title">
          <img src={win.icon} alt="" className="fpkm-win-icon" />
          <Text ellipsis={{ showTooltip: true }} style={{ maxWidth: 320 }}>
            {win.title}
          </Text>
        </div>

        <div className="fpkm-win-actions" onPointerDown={(e) => e.stopPropagation()}>
          <Tooltip content="刷新">
            <Button
              size="small"
              theme="borderless"
              type="tertiary"
              icon={<IconRefresh />}
              onClick={reload}
              aria-label="刷新"
            />
          </Tooltip>
          <Tooltip content="最小化">
            <Button
              size="small"
              theme="borderless"
              type="tertiary"
              icon={<IconMinus />}
              onClick={onMinimize}
              aria-label="最小化"
            />
          </Tooltip>
          <Tooltip content={win.maximized ? '还原' : '最大化'}>
            <Button
              size="small"
              theme="borderless"
              type="tertiary"
              icon={win.maximized ? <IconRestore /> : <IconMaximize />}
              onClick={onToggleMaximize}
              aria-label="最大化"
            />
          </Tooltip>
          <Tooltip content="关闭">
            <Button
              size="small"
              theme="borderless"
              type="danger"
              icon={<IconClose />}
              onClick={onClose}
              aria-label="关闭"
            />
          </Tooltip>
        </div>
      </div>

      {/* 内容区：iframe */}
      <div className="fpkm-win-body">
        {win.loading && !win.failed ? (
          <div className="fpkm-win-loading">
            <Spin size="large" tip="正在打开应用…" />
          </div>
        ) : null}

        {win.failed ? (
          <div className="fpkm-win-failed">
            <Text strong>无法在窗口内打开该应用</Text>
            <Text type="tertiary" size="small" style={{ marginTop: 8, textAlign: 'center' }}>
              应用可能设置了禁止被嵌入（X-Frame-Options / CSP）。
              <br />
              可在新标签页中打开，或改为在应用设置里使用「新标签页」方式。
            </Text>
            <div style={{ marginTop: 16, display: 'flex', gap: 8 }}>
              <Button
                theme="solid"
                type="primary"
                onClick={() => window.open(win.url, '_blank', 'noopener')}
              >
                在新标签页打开
              </Button>
              <Button onClick={reload}>重试</Button>
            </div>
          </div>
        ) : (
          <iframe
            ref={iframeRef}
            className="fpkm-win-iframe"
            src={win.url}
            title={win.title}
            onLoad={onLoaded}
            onError={onFailed}
            allow="clipboard-read; clipboard-write; fullscreen"
            sandbox="allow-same-origin allow-scripts allow-forms allow-popups allow-modals allow-downloads allow-popups-to-escape-sandbox"
          />
        )}
      </div>

      {/* 八向缩放手柄 */}
      {!win.maximized
        ? (['n', 's', 'e', 'w', 'ne', 'nw', 'se', 'sw'] as const).map((dir) => (
            <div
              key={dir}
              className={`fpkm-win-resize fpkm-win-resize-${dir}`}
              onPointerDown={startResize(dir)}
            />
          ))
        : null}
    </div>
  );
}
