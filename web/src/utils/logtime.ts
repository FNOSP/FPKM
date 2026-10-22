/**
 * 日志时间的本地化处理。
 *
 * 背景：日志行首的「发生时间」由**写入日志的宿主进程**按其本地时区写成
 * （后端 `logger.NowStamp` 用的是 `time.Now()`，不会强制 UTC），但宿主时区
 * 未必等于浏览者所在时区——例如 NAS 上以 UTC 运行、用户在东八区时，日志正文
 * 里的时间会比用户本地时间早 8 小时。
 *
 * 因此展示前需要把行首时间从「宿主时区」换算到「访问者本地时区」。接口随日志
 * 内容返回宿主的时区偏移（`tzOffsetSeconds`，东为正），这里据此平移。
 * 只改写行首的时间戳，其余内容（级别、文件行号、正文）一律原样保留。
 */

/** 行首时间戳：`YYYY-MM-DD HH:mm:ss` 与旧版 Go log 的 `YYYY/MM/DD HH:mm:ss` */
const LEADING_TIME = /^(\d{4})([-/])(\d{2})\2(\d{2}) (\d{2}):(\d{2}):(\d{2})(?=\s|$)/;

/** 时区偏移秒数（东为正）转可读文案，如 `UTC+08:00` */
export function formatTZOffset(seconds: number): string {
  const sign = seconds < 0 ? '-' : '+';
  const abs = Math.abs(seconds);
  const h = Math.floor(abs / 3600);
  const m = Math.floor((abs % 3600) / 60);
  return `UTC${sign}${String(h).padStart(2, '0')}:${String(m).padStart(2, '0')}`;
}

/** 访问者本地时区相对 UTC 的偏移秒数（东为正） */
export function viewerOffsetSeconds(): number {
  return -new Date().getTimezoneOffset() * 60;
}

/**
 * 把日志正文行首的时间从宿主时区换算到访问者本地时区。
 *
 * @param content      日志正文（可能多行）
 * @param deltaSeconds 访问者时区偏移 − 宿主时区偏移（秒）。为 0 时原样返回。
 */
export function localizeLogTimes(content: string, deltaSeconds: number): string {
  if (!content || !deltaSeconds) return content;
  const pad = (n: number) => String(n).padStart(2, '0');
  return content
    .split('\n')
    .map((line) => {
      const m = LEADING_TIME.exec(line);
      if (!m) return line;
      const [, year, sep, month, day, hour, minute, second] = m;
      // 把行首时间先当作「宿主时区」的墙上时间：换成 UTC 瞬时后，
      // 再叠加时差即得访问者本地的墙上时间。
      const asUTC = Date.UTC(+year, +month - 1, +day, +hour, +minute, +second);
      const t = new Date(asUTC + deltaSeconds * 1000);
      const stamp =
        `${t.getUTCFullYear()}${sep}${pad(t.getUTCMonth() + 1)}${sep}${pad(t.getUTCDate())} ` +
        `${pad(t.getUTCHours())}:${pad(t.getUTCMinutes())}:${pad(t.getUTCSeconds())}`;
      return stamp + line.slice(m[0].length);
    })
    .join('\n');
}