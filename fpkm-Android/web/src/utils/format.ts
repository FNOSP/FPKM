/** 通用格式化工具 */

/** 字节数转可读体积 */
export function formatSize(bytes: number | undefined | null): string {
  if (bytes === undefined || bytes === null || Number.isNaN(bytes)) return '-';
  if (bytes < 1024) return `${bytes} B`;
  const units = ['KiB', 'MiB', 'GiB', 'TiB'];
  let value = bytes;
  let i = -1;
  do {
    value /= 1024;
    i++;
  } while (value >= 1024 && i < units.length - 1);
  return `${value.toFixed(1)} ${units[i]}`;
}

/** 时间戳（毫秒）转本地时间字符串 */
export function formatTime(ms?: number): string {
  if (!ms) return '-';
  const d = new Date(ms);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(
    d.getMinutes(),
  )}:${pad(d.getSeconds())}`;
}

/** 耗时（毫秒）转可读文本 */
export function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms} ms`;
  const s = ms / 1000;
  if (s < 60) return `${s.toFixed(1)} s`;
  const m = Math.floor(s / 60);
  const rest = Math.round(s % 60);
  return `${m} 分 ${rest} 秒`;
}

/** 从 HTML 描述里提取纯文本摘要（manifest 的 desc 常为 HTML） */
export function stripHtml(html: string, maxLen = 160): string {
  if (!html) return '';
  const text = html
    .replace(/<[^>]*>/g, ' ')
    .replace(/&nbsp;/g, ' ')
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&amp;/g, '&')
    .replace(/&quot;/g, '"')
    .replace(/\s+/g, ' ')
    .trim();
  if (text.length <= maxLen) return text;
  return `${text.slice(0, maxLen)}…`;
}

/** 接入方式的中文标签 */
export function kindLabel(kind: string): string {
  switch (kind) {
    case 'gateway':
      return '统一网关';
    case 'cgi':
      return 'CGI 应用';
    case 'port':
      return '端口应用';
    default:
      return '未知类型';
  }
}

/**
 * 按"当前页面的主机名"拼出应用访问地址。
 *
 * 界面可能通过 127.0.0.1、局域网 IP 或域名访问，因此不能用 localhost：
 * 从另一台电脑打开 http://nas:7666 时，localhost 指的是那台电脑自己。
 * 这里复用浏览器地址栏的协议与主机名，只替换端口与路径。
 *
 * 若服务端通过 --public-host 显式指定了对外主机名/协议（反向代理场景），
 * 则优先使用它。
 */
export function buildAccessURL(
  access?: {
    accessPath?: string;
    accessPort?: number;
    scheme?: string;
  },
  publicHost?: string,
  publicScheme?: string,
): string | null {
  if (!access || !access.accessPort) return null;

  const { protocol, hostname } = window.location;
  const host = publicHost && publicHost.trim() ? publicHost.trim() : hostname;
  const scheme =
    publicScheme && publicScheme.trim()
      ? publicScheme.trim()
      : access.scheme || (protocol ? protocol.replace(':', '') : 'http');
  const path = access.accessPath || '/';

  // 显式指定主机名时（可能已含端口），不再拼接网关端口
  if (publicHost && publicHost.trim()) {
    return `${scheme}://${host}${path}`;
  }

  return `${scheme}://${host}:${access.accessPort}${path}`;
}
