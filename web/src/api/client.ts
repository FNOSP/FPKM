import type {
  AppsResponse,
  AppAction,
  FSListResult,
  InspectResult,
  MetaInfo,
} from './types';

const SESSION_KEY = 'fpkm.session';

/** 读取本地保存的会话令牌 */
export function getSession(): string {
  try {
    return localStorage.getItem(SESSION_KEY) ?? '';
  } catch {
    return '';
  }
}

/** 保存会话令牌 */
export function setSession(token: string): void {
  try {
    if (token) localStorage.setItem(SESSION_KEY, token);
    else localStorage.removeItem(SESSION_KEY);
  } catch {
    /* localStorage 不可用时忽略 */
  }
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

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  const token = getSession();
  if (token) headers.set('Authorization', `Bearer ${token}`);
  if (init.body && !(init.body instanceof FormData)) {
    headers.set('Content-Type', 'application/json');
  }

  let resp: Response;
  try {
    resp = await fetch(path, { ...init, headers });
  } catch (e) {
    throw new ApiError(0, '无法连接服务：' + (e instanceof Error ? e.message : String(e)));
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
  const headers = new Headers();
  const token = getSession();
  if (token) headers.set('Authorization', `Bearer ${token}`);
  return fetch('/api/auth/status', { headers }).then(async (r) => {
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
    const token = getSession();
    if (token) xhr.setRequestHeader('Authorization', `Bearer ${token}`);

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
  const token = getSession();
  const qs = token ? `?session=${encodeURIComponent(token)}` : '';
  const es = new EventSource(`/api/tasks/${encodeURIComponent(taskId)}/stream${qs}`);
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

/** 应用图标地址（会话通过查询参数传递，便于 <img> 直接加载） */
export function iconURL(key: string): string {
  const token = getSession();
  const qs = token ? `?session=${encodeURIComponent(token)}` : '';
  return `/api/icons/${encodeURIComponent(key)}${qs}`;
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
