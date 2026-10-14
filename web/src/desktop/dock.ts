import { useCallback, useEffect, useState } from 'react';

/**
 * 任务栏（Dock）隐藏列表的共享状态。
 *
 * 为什么抽成模块而不是放在 Desktop 里：
 *   - 桌面只显示"正在运行"的应用，未运行的应用不在桌面上，
 *     因此"从任务栏移除后如何恢复"必须另有入口 ——
 *     商店的「已安装」列表就是那个入口；
 *   - 两处需要读写同一份数据并保持同步，用 localStorage + 自定义事件
 *     实现即可，无需层层传递 props。
 */

const KEY = 'fpkm.dock.hidden';
const EVENT = 'fpkm:dock-hidden-changed';

function read(): string[] {
  try {
    const raw = localStorage.getItem(KEY);
    const arr = raw ? JSON.parse(raw) : [];
    return Array.isArray(arr) ? arr.filter((x) => typeof x === 'string') : [];
  } catch {
    return [];
  }
}

function write(next: string[]) {
  try {
    localStorage.setItem(KEY, JSON.stringify(next));
  } catch {
    /* localStorage 不可用时忽略 */
  }
  // 通知同页面内其它使用该 hook 的组件
  window.dispatchEvent(new CustomEvent(EVENT, { detail: next }));
}

export interface DockControl {
  /** 该应用是否被隐藏（不在任务栏显示） */
  isHidden: (appName: string) => boolean;
  /** 从任务栏移除 */
  hide: (appName: string) => void;
  /** 恢复到任务栏 */
  show: (appName: string) => void;
  /** 切换 */
  toggle: (appName: string) => void;
}

/** 读取并操作"任务栏隐藏列表" */
export function useDock(): DockControl {
  const [hidden, setHidden] = useState<string[]>(read);

  useEffect(() => {
    const onChange = (e: Event) => {
      const detail = (e as CustomEvent<string[]>).detail;
      setHidden(Array.isArray(detail) ? detail : read());
    };
    // 监听同页其它实例的变更，以及其它标签页的 storage 事件
    window.addEventListener(EVENT, onChange);
    window.addEventListener('storage', onChange);
    return () => {
      window.removeEventListener(EVENT, onChange);
      window.removeEventListener('storage', onChange);
    };
  }, []);

  const hide = useCallback((appName: string) => {
    write([...new Set([...read(), appName])]);
  }, []);

  const show = useCallback((appName: string) => {
    write(read().filter((n) => n !== appName));
  }, []);

  const toggle = useCallback(
    (appName: string) => {
      if (read().includes(appName)) show(appName);
      else hide(appName);
    },
    [hide, show],
  );

  return {
    isHidden: (appName: string) => hidden.includes(appName),
    hide,
    show,
    toggle,
  };
}
