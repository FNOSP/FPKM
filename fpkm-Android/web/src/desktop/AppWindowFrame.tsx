import { memo, useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
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
  /**
   * 内置内容标记。'store' 表示这扇窗口承载应用商店，
   * 由 Desktop 在**每次渲染时**重新生成内容（不能把节点缓存在 state 里，
   * 否则会变成陈旧快照，商店内的交互不会更新）。
   */
  builtin?: 'store';
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
function AppWindowFrameInner({
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
  inlineContent,
}: Props & { inlineContent?: ReactNode }) {
  const [dragging, setDragging] = useState(false);
  const [resizing, setResizing] = useState<string | null>(null);
  const dragRef = useRef<{ dx: number; dy: number } | null>(null);
  const resizeRef = useRef<{ startX: number; startY: number; rect: WindowRect; dir: string } | null>(null);
  const iframeRef = useRef<HTMLIFrameElement>(null);
  /** 窗口根元素：拖动/缩放时直接改它的 style，绕过 React 重渲染 */
  const boxRef = useRef<HTMLDivElement>(null);
  /** 拖动/缩放过程中的实时几何，松手时一次性提交 */
  const liveRef = useRef<WindowRect | null>(null);

  /**
   * 指针移动：拖动或缩放。
   *
   * 性能要点（修复"拖动卡顿"）：
   * 直接用 ref 改 DOM 的 transform / width·height，**不调用 setState**。
   *
   * 之前每次 pointermove 都走 onMove → Desktop 的 setWindows →
   * 整个桌面重渲染；而商店窗口的 inlineContent 是每次渲染重新构建的
   * 整棵组件树（Desktop.tsx 的 renderStore），等于每帧重建一次大型子树，
   * 窗口一多就明显掉帧。
   *
   * 现在拖动过程中只做两件廉价操作：
   *   - 改 transform（合成层，不触发 layout 重排）；
   *   - 缩放时改 width/height（只影响该窗口自身）。
   * 松手时才把最终几何提交给 React（onMove 一次）。
   */
  const onPointerMove = useCallback(
    (e: PointerEvent) => {
      const el = boxRef.current;
      if (!el) return;

      const d = dragRef.current;
      if (d) {
        const nx = Math.min(Math.max(0, e.clientX - d.dx), Math.max(0, bounds.w - win.rect.w));
        const ny = Math.min(Math.max(0, e.clientY - d.dy), Math.max(0, bounds.h - win.rect.h));
        liveRef.current = { ...win.rect, x: nx, y: ny };
        // 用 translate 表现位移，避免 left/top 触发重排
        el.style.transform = `translate3d(${nx - win.rect.x}px, ${ny - win.rect.y}px, 0)`;
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
        w = Math.min(w, bounds.w - x);
        h = Math.min(h, bounds.h - y);

        liveRef.current = { x, y, w, h };
        el.style.transform = `translate3d(${x - r.rect.x}px, ${y - r.rect.y}px, 0)`;
        el.style.width = `${w}px`;
        el.style.height = `${h}px`;
      }
    },
    [bounds.w, bounds.h, win.rect],
  );

  const stop = useCallback(() => {
    const el = boxRef.current;
    const live = liveRef.current;

    dragRef.current = null;
    resizeRef.current = null;
    liveRef.current = null;

    /**
     * 提交最终几何。
     *
     * 关键顺序（曾经在这里出过 bug）：**必须先让 React 拿到新几何，
     * 再清除内联样式**。早期实现先清 `width/height/transform`、
     * 后调 `onMove`，中间会出现一帧"窗口按内容收缩"的状态 ——
     * 因为 .fpkm-win 没有固定宽高，清掉内联样式后它会缩到内容尺寸
     * （实测从 960×620 缩成 302×192，看起来像"窗口被缩小了"）。
     *
     * 现在：先把最终几何写进 state，再在下一帧清除内联覆盖，
     * 这样 React 渲染出的 rect 已经与之一致，不会闪缩。
     */
    if (el && live) {
      /**
       * 只清 transform，**绝不能清 width/height**。
       *
       * 踩过的坑：早期在松手时把 width/height 一起清成 ''，
       * 想"交还给 React 的 style"。但 .fpkm-win 本身没有固定宽高，
       * 一旦内联宽高被清掉、而 React 的下一帧还没到，
       * 窗口就会按内容收缩到很小（实测 960×620 → 302×192），
       * 看起来就像"一放手窗口就被缩小了"。
       *
       * 现在只清 transform（拖动期间的位移补偿），
       * 宽高交给 React 的 style（win.rect.w / win.rect.h）维护。
       */
      el.style.transform = '';
      onMove(live);
    }
    setDragging(false);
    setResizing(null);
  }, [onMove]);

  /**
   * 拖动/缩放期间监听全局指针事件。
   *
   * 关键：必须同时监听 pointerup 与 pointercancel，并在捕获阶段处理。
   *
   * 为什么之前"松手后窗口还在跟鼠标"：
   * 鼠标拖过窗口内的 <iframe> 时，事件进入 iframe 的**独立文档**，
   * 父窗口收不到 pointerup —— dragRef 永不清空，于是继续跟随。
   *
   * 三重保障：
   *   1. setPointerCapture（在 startDrag 里）把指针事件锁定到标题栏元素；
   *   2. 拖动期间给 iframe 盖上透明遮罩（pointer-events 归父窗口）；
   *   3. 监听 pointercancel / blur，窗口失焦时也结束拖动。
   */
  useEffect(() => {
    if (!dragging && !resizing) return;

    const onUp = () => stop();
    const onCancel = () => stop();
    const onBlur = () => stop();

    window.addEventListener('pointermove', onPointerMove);
    window.addEventListener('pointerup', onUp, true);
    window.addEventListener('pointercancel', onCancel, true);
    window.addEventListener('blur', onBlur);
    return () => {
      window.removeEventListener('pointermove', onPointerMove);
      window.removeEventListener('pointerup', onUp, true);
      window.removeEventListener('pointercancel', onCancel, true);
      window.removeEventListener('blur', onBlur);
    };
  }, [dragging, resizing, onPointerMove, stop]);

  const startDrag = (e: React.PointerEvent) => {
    if (win.maximized) return;
    if (e.button !== 0) return; // 仅左键
    onFocus();
    // 锁定指针：拖到 iframe 上方也能继续收到 pointermove/pointerup
    try {
      (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    } catch {
      /* 某些浏览器/合成事件不支持，忽略即可（还有遮罩兜底） */
    }
    dragRef.current = { dx: e.clientX - win.rect.x, dy: e.clientY - win.rect.y };
    setDragging(true);
  };

  const startResize = (dir: string) => (e: React.PointerEvent) => {
    e.stopPropagation();
    if (e.button !== 0) return;
    onFocus();
    try {
      (e.currentTarget as HTMLElement).setPointerCapture(e.pointerId);
    } catch {
      /* 忽略 */
    }
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
      ref={boxRef}
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
        {/* 拖动/缩放时覆盖一层透明遮罩。
            否则鼠标划过 iframe 时事件进入其独立文档，
            父窗口收不到 pointermove，拖动会"丢帧"甚至卡住。 */}
        {dragging || resizing ? <div className="fpkm-win-dragshield" /> : null}
        {win.loading && !win.failed && !inlineContent ? (
          <div className="fpkm-win-loading">
            <Spin size="large" tip="正在打开应用…" />
          </div>
        ) : null}

        {inlineContent ? (
          <div className="fpkm-win-inline">{inlineContent}</div>
        ) : win.failed ? (
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
        ) : inlineContent ? null : (
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

/**
 * 用 memo 包一层，避免桌面任一状态变化（例如拖动另一个窗口）时
 * 所有窗口都跟着重渲染。
 *
 * 注意 inlineContent 每次渲染都是新对象，不能直接浅比较；
 * 这里只比较真正影响渲染的字段，窗口内容由 React 内部的 key 保持不变。
 */
const AppWindowFrame = memo(AppWindowFrameInner, (a, b) => {
  return (
    a.win === b.win &&
    a.active === b.active &&
    a.bounds.w === b.bounds.w &&
    a.bounds.h === b.bounds.h &&
    a.inlineContent === b.inlineContent
  );
});

export default AppWindowFrame;
