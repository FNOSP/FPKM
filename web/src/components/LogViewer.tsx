import { useEffect, useRef } from 'react';
import type { LogLine } from '../api/types';

interface Props {
  lines: LogLine[];
  height?: number | string;
  /** 是否自动滚动到底部 */
  autoScroll?: boolean;
}

// 日志面板使用深色背景，因此这里用固定的浅色前景，
// 不能复用 Semi 主题的 text token（浅色主题下那是接近黑色的，会看不见）。
const LEVEL_COLOR: Record<string, string> = {
  debug: '#8b949e',
  info: '#d4d4d4',
  warn: '#e3b341',
  error: '#ff7b72',
};

/** 终端风格的日志输出面板 */
export default function LogViewer({ lines, height = 320, autoScroll = true }: Props) {
  const boxRef = useRef<HTMLDivElement>(null);
  const pinnedRef = useRef(true);

  // 用户手动上滚时暂停自动滚动
  const onScroll = () => {
    const el = boxRef.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
    pinnedRef.current = atBottom;
  };

  useEffect(() => {
    const el = boxRef.current;
    if (!el || !autoScroll || !pinnedRef.current) return;
    el.scrollTop = el.scrollHeight;
  }, [lines, autoScroll]);

  return (
    <div
      ref={boxRef}
      onScroll={onScroll}
      className="fpkm-log"
      style={{ height, maxHeight: height }}
    >
      {lines.length === 0 ? (
        <div className="fpkm-log-line fpkm-log-dim">等待日志输出…</div>
      ) : (
        lines.map((l) => (
          <div key={l.seq} className="fpkm-log-line">
            <span className="fpkm-log-time">
              {new Date(l.ts).toLocaleTimeString('zh-CN', { hour12: false })}
            </span>
            <span className="fpkm-log-level" style={{ color: LEVEL_COLOR[l.level] ?? undefined }}>
              {l.level.toUpperCase().padEnd(5)}
            </span>
            <span className="fpkm-log-text" style={{ color: LEVEL_COLOR[l.level] ?? undefined }}>
              {l.text}
            </span>
          </div>
        ))
      )}
    </div>
  );
}
