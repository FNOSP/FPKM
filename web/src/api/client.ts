import type {
  AppsResponse,
  AppAction,
  FSListResult,
  InspectResult,
  MetaInfo,
} from './types';

/**
 * 会话令牌不再存放在 localStorage。
 *
 * 令牌通过 **HttpOnly Cookie** 下发，浏览器会自动携带，
 * 因此前端无需（也不应）读取它：
 *   - HttpOnly 意味着 JS 无法读取 → XSS 无法窃取会话；
 *   - Cookie 不区分端口 → 统一网关 / CGI 应用（另一个端口）也能收到；
 *   - fetch(同源) / XHR / EventSource / <img> 都会自动带上 Cookie。
 *
 * 保留 setSession 作为空实现，是为了兼容调用方（登录成功后的回调），
 * 避免为了删除一个赋值而改动多处调用点。
 */

/** 兼容旧调用：令牌已由服务端通过 HttpOnly Cookie 下发，此处无需保存 */
export function setSession(_token: string): void {
  /* 有意留空：令牌由 Cookie 承载 */
}

/** 401 时的回调，由 App 注入以弹出登录框 */
let onUnauthorized: (() => void) | null = null;
export function setUnauthorizedHandler(fn: (() => void) | null): void {
  onUnauthorized = fn;
}

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
    this.name = 'ApiError';
  }
}

/** 默认请求超时：避免 Android WebView/网络栈异常时 fetch 永不结束，
 *  前端一直停留在"加载中"状态。调用方可通过 init.signal 主动取消。 */
const REQUEST_TIMEOUT_MS = 15000;

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  // 会话由 HttpOnly Cookie 自动携带（同源 fetch 默认 credentials: same-origin）
  if (init.body && !(init.body instanceof FormData)) {
    headers.set('Content-Type', 'application/json');
  }

  // 超时兜底：AbortController 15s 后中止请求，
  // 调用方传入的 signal 也可以随时取消本请求。
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), REQUEST_TIMEOUT_MS);
  if (init.signal) {
    if (init.signal.aborted) {
      controller.abort();
    } else {
      init.signal.addEventListener('abort', () => controller.abort(), { once: true });
    }
  }

  let resp: Response;
  try {
    resp = await fetch(path, { ...init, headers, signal: controller.signal });
  } catch (e) {
    throw new ApiError(0, '无法连接服务：' + (e instanceof Error ? e.message : String(e)));
  } finally {
    clearTimeout(timer);
  }

  // 401 处理：登录/设置密码接口的 401 是"密码不对"这类业务错误，
  // 应由调用方原样展示，不能触发全局登出（否则会把提示覆盖成"登录已失效"）。
  const isAuthEndpoint = path.startsWith('/api/auth/login') || path.startsWith('/api/auth/setup');

  if (resp.status === 401 && !isAuthEndpoint) {
    onUnauthorized?.();
    throw new ApiError(401, '登录已失效，请重新登录');
  }

  const text = await resp.text();
  let data: unknown = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = text;
    }
  }

  if (!resp.ok) {
    const msg =
      data && typeof data === 'object' && 'error' in data
        ? String((data as { error: unknown }).error)
        : `请求失败 (HTTP ${resp.status})`;
    throw new ApiError(resp.status, msg);
  }

  return data as T;
}

/** 认证状态 */
export interface AuthStatus {
  needsSetup: boolean;
  hasPassword: boolean;
  authenticated: boolean;
  allowFS: boolean;
  version: string;
}

export function authStatus(): Promise<AuthStatus> {
  return fetch('/api/auth/status').then(async (r) => {
    if (!r.ok) throw new ApiError(r.status, '无法获取认证状态');
    return r.json();
  });
}

/** 首次设置管理密码 */
export function setupPassword(password: string, confirm: string): Promise<{ session: string }> {
  return request('/api/auth/setup', {
    method: 'POST',
    body: JSON.stringify({ password, confirm }),
  });
}

/** 登录 */
export function login(password: string): Promise<{ session: string }> {
  return request('/api/auth/login', {
    method: 'POST',
    body: JSON.stringify({ password }),
  });
}

/** 注销 */
export function logout(): Promise<{ ok: boolean }> {
  return request('/api/auth/logout', { method: 'POST' });
}

/** 修改密码 */
export function changePassword(
  oldPassword: string,
  newPassword: string,
  confirm: string,
): Promise<{ session: string }> {
  return request('/api/auth/password', {
    method: 'POST',
    body: JSON.stringify({ oldPassword, newPassword, confirm }),
  });
}

/** 运行环境信息 */
export function getMeta(): Promise<MetaInfo> {
  return request('/api/meta');
}

/** 已安装应用列表 */
export function listApps(): Promise<AppsResponse> {
  return request('/api/apps');
}

/** 应用操作（启动/停止/卸载/升级）
 *  values 为向导收集到的参数，会作为环境变量传给生命周期脚本。 */
export function appAction(
  name: string,
  action: AppAction,
  path?: string,
  values?: Record<string, string>,
): Promise<{ taskId: string }> {
  const qs = path ? `?path=${encodeURIComponent(path)}` : '';
  return request(`/api/apps/${encodeURIComponent(name)}/${action}${qs}`, {
    method: 'POST',
    body: JSON.stringify({ values: values ?? {} }),
  });
}

/** 查看某个应用的全部日志文件（「查看所有日志」）。
 *  tail 为每个文件返回的尾部字节数上限，缺省由服务端决定。 */
export function getAppLogs(name: string, tail?: number): Promise<import('./types').AppLogsView> {
  const qs = tail && tail > 0 ? `?tail=${Math.floor(tail)}` : '';
  return request(`/api/apps/${encodeURIComponent(name)}/log${qs}`);
}

/** 查看 FPKM **程序自身**的日志文件（「FPKM 日志」页）。
 *  与「应用日志」不同，它读取的是程序日志目录（<数据目录>/logs），
 *  用于排查网关、宿主进程、安装流程等 FPKM 自身的问题。 */
export function getFpkmLogs(tail?: number): Promise<import('./types').AppLogsView> {
  const qs = tail && tail > 0 ? `?tail=${Math.floor(tail)}` : '';
  return request(`/api/logs${qs}`);
}

/** 列出目录 */
export function fsList(path?: string): Promise<FSListResult> {
  const qs = path ? `?path=${encodeURIComponent(path)}` : '';
  return request(`/api/fs/list${qs}`);
}

/** 解析本地包信息 */
export function fsInspect(path: string): Promise<InspectResult> {
  return request(`/api/fs/inspect?path=${encodeURIComponent(path)}`);
}

/** 从本地路径安装 */
export function installLocal(
  path: string,
  set?: Record<string, string>,
): Promise<{ taskId: string }> {
  return request('/api/install/local', {
    method: 'POST',
    body: JSON.stringify({ path, set: set ?? {} }),
  });
}

export interface UploadHandle {
  /** 取消上传 */
  abort: () => void;
  /** 上传完成后的响应 */
  done: Promise<{ taskId: string }>;
}

/**
 * 上传 .fpk 并安装。
 * 使用 XMLHttpRequest 以获得真实的上传进度。
 */
export function uploadAndInstall(
  file: File,
  set: Record<string, string> | undefined,
  onProgress: (percent: number, loaded: number, total: number) => void,
): UploadHandle {
  const xhr = new XMLHttpRequest();
  const form = new FormData();
  form.append('file', file);
  if (set) {
    for (const [k, v] of Object.entries(set)) form.append(`set[${k}]`, v);
  }

  const done = new Promise<{ taskId: string }>((resolve, reject) => {
    xhr.open('POST', '/api/install/upload');
    // 同源 XHR 会自动携带会话 Cookie

    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress((e.loaded / e.total) * 100, e.loaded, e.total);
    };

    xhr.onload = () => {
      let data: unknown = null;
      try {
        data = JSON.parse(xhr.responseText);
      } catch {
        /* 非 JSON 响应 */
      }
      if (xhr.status === 401) {
        onUnauthorized?.();
        reject(new ApiError(401, '登录已失效，请重新登录'));
        return;
      }
      if (xhr.status < 200 || xhr.status >= 300) {
        const msg =
          data && typeof data === 'object' && 'error' in data
            ? String((data as { error: unknown }).error)
            : `上传失败 (HTTP ${xhr.status})`;
        reject(new ApiError(xhr.status, msg));
        return;
      }
      resolve(data as { taskId: string });
    };

    xhr.onerror = () => reject(new ApiError(0, '上传过程中网络中断'));
    xhr.onabort = () => reject(new ApiError(0, '上传已取消'));

    xhr.send(form);
  });

  return { abort: () => xhr.abort(), done };
}

/** 查询当前正在执行的任务（页面刷新后据此恢复进度弹窗） */
export function currentTask(): Promise<{ task: import('./types').TaskSnapshot | null }> {
  return request('/api/tasks/current');
}

/** 订阅任务日志（SSE） */
export interface TaskStreamHandlers {
  onLog: (line: import('./types').LogLine) => void;
  onDone: (snapshot: import('./types').TaskSnapshot) => void;
  onError?: (err: Error) => void;
}

export function streamTask(taskId: string, handlers: TaskStreamHandlers): () => void {
  // 同源 EventSource 会自动携带会话 Cookie
  const es = new EventSource(`/api/tasks/${encodeURIComponent(taskId)}/stream`);
  let closed = false;

  const close = () => {
    if (!closed) {
      closed = true;
      es.close();
    }
  };

  es.addEventListener('log', (ev) => {
    try {
      handlers.onLog(JSON.parse((ev as MessageEvent).data));
    } catch {
      /* 忽略坏帧 */
    }
  });

  es.addEventListener('done', (ev) => {
    try {
      handlers.onDone(JSON.parse((ev as MessageEvent).data));
    } catch {
      handlers.onDone({} as import('./types').TaskSnapshot);
    }
    close();
  });

  es.onerror = () => {
    // EventSource 会自动重连；仅在已结束时停止
    if (es.readyState === EventSource.CLOSED) {
      close();
      handlers.onError?.(new Error('日志连接已断开'));
    }
  };

  return close;
}

// ---------- 商店（远程应用源） ----------

/** 远程应用列表 */
export function listStoreApps(refresh = false): Promise<import('./types').StoreAppsResponse> {
  return request(`/api/store/apps${refresh ? '?refresh=1' : ''}`);
}

/** 从远程源安装 */
export function installFromStore(id: string): Promise<{ taskId: string; name: string }> {
  return request('/api/store/install', {
    method: 'POST',
    body: JSON.stringify({ id }),
  });
}

/** 列出应用源 */
export function listSources(): Promise<{ sources: import('./types').AppSource[] }> {
  return request('/api/store/sources');
}

/** 新增/更新应用源 */
export function saveSource(src: {
  id?: string;
  name?: string;
  url: string;
  enabled?: boolean;
}): Promise<{ sources: import('./types').AppSource[] }> {
  return request('/api/store/sources', { method: 'POST', body: JSON.stringify(src) });
}

/** 删除应用源 */
export function deleteSource(id: string): Promise<{ sources: import('./types').AppSource[] }> {
  return request(`/api/store/sources/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

/** 应用图标地址（会话由 Cookie 自动携带，<img> 同源请求即可） */
export function iconURL(key: string): string {
  return `/api/icons/${encodeURIComponent(key)}`;
}

// ---------- 向导 ----------

/** 查询已安装应用的向导 */
export function getWizard(
  appname: string,
  kind: 'install' | 'uninstall' | 'config' | 'upgrade',
): Promise<import('./types').WizardResponse> {
  return request(`/api/wizard?app=${encodeURIComponent(appname)}&kind=${kind}`);
}

/** 查询本地 .fpk 包内的向导（安装前预览） */
export function getPackageWizard(
  path: string,
  kind: 'install' | 'uninstall' | 'config' | 'upgrade',
): Promise<import('./types').WizardResponse> {
  return request(`/api/wizard?path=${encodeURIComponent(path)}&kind=${kind}`);
}

/** 下载远程应用安装包（不安装）。返回 taskId，进度通过任务日志流获取。 */
export function downloadFromStore(id: string): Promise<{ taskId: string; name: string }> {
  return request('/api/store/download', {
    method: 'POST',
    body: JSON.stringify({ id }),
  });
}

/** 应用详情（已安装应用） */
export function getAppDetail(appname: string): Promise<import('./types').AppDetail> {
  return request(`/api/detail?app=${encodeURIComponent(appname)}`);
}

/** 应用详情（商店条目） */
export function getStoreDetail(id: string): Promise<import('./types').AppDetail> {
  return request(`/api/detail?id=${encodeURIComponent(id)}`);
}

/**
 * 等待任务结束并返回最终快照。
 * 用于"下载完成后自动继续下一步"的串行流程（如下载结束再弹向导）。
 */
export function waitTask(
  taskId: string,
  onProgress?: (snap: import('./types').TaskSnapshot) => void,
): Promise<import('./types').TaskSnapshot> {
  return new Promise((resolve, reject) => {
    const close = streamTask(taskId, {
      onLog: () => undefined,
      onDone: (snap) => {
        close();
        resolve(snap);
      },
      onError: (e) => {
        close();
        reject(e);
      },
    });
    // 通过 state 事件之外的轮询获取进度代价高，
    // 这里直接复用 SSE 的连接：进度随快照一起返回。
    void onProgress;
  });
}

// ---------- 系统状态（桌面小组件） ----------

export interface SystemStats {
  hostname: string;
  platform: string;
  uptime: number;
  serverTime: string;
  cpu: { load1: number; load5: number; load15: number; cores: number; usage: number };
  mem: { total: number; used: number; available: number; percent: number };
  disks: { mount: string; total: number; used: number; available: number; percent: number }[];
  store: { installed: number; running: number; total: number };
}

/** 系统状态（内存/存储/负载） */
export function getSystemStats(): Promise<SystemStats> {
  return request('/api/system/stats');
}

/** 应用接入方式设置（应用设置弹窗） */
export interface AccessSetting {
  appName: string;
  /** '' = 跟随 ui/config；port / gateway / cgi = 用户手动指定 */
  mode: '' | 'port' | 'gateway' | 'cgi';
  port?: number;
  path?: string;
}

/** 读取某个应用的接入方式设置 */
export function getAccessSetting(app: string): Promise<AccessSetting> {
  return request(`/api/access?app=${encodeURIComponent(app)}`);
}

/** 保存某项的接入方式设置 */
export function setAccessSetting(input: {
  app: string;
  mode: '' | 'port' | 'gateway' | 'cgi';
  port?: number | string;
  path?: string;
}): Promise<AccessSetting> {
  return request('/api/access/set', {
    method: 'POST',
    body: JSON.stringify(input),
  });
}
