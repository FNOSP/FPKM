import type { WindowRect } from './AppWindowFrame';

/**
 * 子窗口几何（位置 + 尺寸）的本地持久化。
 *
 * 需求：应用的子窗口大小和位置要记在浏览器本地存储里，
 * 下次打开同一个应用时回到上次的位置与大小。
 *
 * 设计要点：
 *   - 按窗口 id（图标 key）分别存储，互不影响；
 *   - 读取失败/数据损坏一律当作"没有记录"，回退到默认布局，
 *     绝不让脏数据把窗口摆到屏幕外；
 *   - 存储的是**普通窗口**的几何，最大化状态不写入
 *     （最大化时 rect 是整屏，写进去会让下次打开也变成整屏）。
 */

const KEY = 'fpkm.window.rects';

/** 窗口几何记录：id → rect */
type RectMap = Record<string, WindowRect>;

/** 合理的尺寸下界，避免脏数据把窗口压成一条线 */
const MIN_W = 320;
const MIN_H = 200;

function isFiniteNumber(v: unknown): v is number {
  return typeof v === 'number' && Number.isFinite(v);
}

/** 校验一条记录是否可用 */
function isValidRect(r: unknown): r is WindowRect {
  if (!r || typeof r !== 'object') return false;
  const o = r as Record<string, unknown>;
  return (
    isFiniteNumber(o.x) && isFiniteNumber(o.y) && isFiniteNumber(o.w) && isFiniteNumber(o.h)
  );
}

function readAll(): RectMap {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return {};
    const parsed: unknown = JSON.parse(raw);
    if (!parsed || typeof parsed !== 'object') return {};
    const out: RectMap = {};
    for (const [k, v] of Object.entries(parsed as Record<string, unknown>)) {
      if (k && isValidRect(v)) out[k] = v;
    }
    return out;
  } catch {
    // localStorage 不可用（隐私模式）或 JSON 损坏：当作没有记录
    return {};
  }
}

function writeAll(map: RectMap) {
  try {
    localStorage.setItem(KEY, JSON.stringify(map));
  } catch {
    /* 配额不足或被禁用：忽略，不影响使用 */
  }
}

/** 读取某个窗口上次保存的几何 */
export function loadWindowRect(id: string): WindowRect | null {
  const map = readAll();
  const r = map[id];
  if (!r) return null;
  return { x: r.x, y: r.y, w: Math.max(MIN_W, r.w), h: Math.max(MIN_H, r.h) };
}

/** 保存某个窗口的几何 */
export function saveWindowRect(id: string, rect: WindowRect) {
  if (!isValidRect(rect)) return;
  const map = readAll();
  map[id] = {
    x: Math.round(rect.x),
    y: Math.round(rect.y),
    w: Math.round(Math.max(MIN_W, rect.w)),
    h: Math.round(Math.max(MIN_H, rect.h)),
  };
  writeAll(map);
}

/** 删除某个窗口的记录（例如应用被卸载） */
export function forgetWindowRect(id: string) {
  const map = readAll();
  if (!(id in map)) return;
  delete map[id];
  writeAll(map);
}

/**
 * 把保存的几何约束到当前桌面范围内。
 *
 * 场景：上次在 1920 宽的屏幕上记下 x=1500，这次换了 1280 的窗口，
 * 直接套用会让窗口整个跑到屏幕外。这里把位置夹回可视范围，
 * 尺寸也不超过桌面。
 */
export function clampRect(
  rect: WindowRect,
  bounds: { w: number; h: number },
): WindowRect {
  const w = Math.min(Math.max(MIN_W, rect.w), Math.max(MIN_W, bounds.w));
  const h = Math.min(Math.max(MIN_H, rect.h), Math.max(MIN_H, bounds.h));
  return {
    w,
    h,
    x: Math.min(Math.max(0, rect.x), Math.max(0, bounds.w - w)),
    y: Math.min(Math.max(0, rect.y), Math.max(0, bounds.h - h)),
  };
}
