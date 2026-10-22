import { useEffect, useState } from 'react';

/**
 * 窄屏断点：与外壳侧栏一致（< 768px 视为手机）。
 */
export const NARROW_BREAKPOINT = 768;

/** 浮层最小可用宽度：再窄也要给内容留出可读宽度 */
const MIN_POPUP_WIDTH = 280;

export interface Viewport {
  /** 视口宽度（px） */
  width: number;
  /** 视口高度（px） */
  height: number;
  /** 是否窄屏（手机竖屏 / 窄窗口） */
  narrow: boolean;
}

function readViewport(): Viewport {
  const width = typeof window === 'undefined' ? 1024 : window.innerWidth;
  const height = typeof window === 'undefined' ? 768 : window.innerHeight;
  return { width, height, narrow: width < NARROW_BREAKPOINT };
}

/**
 * 订阅视口尺寸。
 *
 * 背景：Semi 的 Modal / SideSheet 通过 portal 挂到 document.body 上，
 * 不在 `.fpkm-shell` 内，因此外壳的 `.fpkm-shell-narrow` 类名管不到它们。
 * 这些浮层要适配手机屏，只能由 JS 读取**视口**尺寸后再决定宽度。
 */
export function useViewport(): Viewport {
  const [vp, setVp] = useState<Viewport>(readViewport);

  useEffect(() => {
    const onChange = () => setVp(readViewport());
    window.addEventListener('resize', onChange);
    // 部分 WebView / 横竖屏切换只触发 orientationchange，补一档
    window.addEventListener('orientationchange', onChange);
    return () => {
      window.removeEventListener('resize', onChange);
      window.removeEventListener('orientationchange', onChange);
    };
  }, []);

  return vp;
}

/**
 * 把浮层的设计宽度收敛到屏幕内：宽屏保持设计宽度，窄屏左右各留 margin。
 *
 * @param desired 宽屏下的设计宽度（px）
 * @param vp      当前视口
 * @param margin  窄屏时左右留白合计（px），默认 24
 */
export function fitPopupWidth(desired: number, vp: Viewport, margin = 24): number {
  const usable = Math.max(MIN_POPUP_WIDTH, vp.width - margin);
  return Math.round(Math.min(desired, usable));
}

/**
 * 依据视口高度计算日志面板高度：保证弹窗「标题 + 状态 + 日志 + 按钮」
 * 能在一屏内放下，超出的日志改为面板内部滚动而不是把弹窗撑出屏幕。
 */
export function fitLogHeight(desired: number, vp: Viewport): number {
  if (!vp.narrow) return desired;
  const byHeight = Math.round(vp.height * 0.4);
  return Math.max(140, Math.min(desired, byHeight));
}