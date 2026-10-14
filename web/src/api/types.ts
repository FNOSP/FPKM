/** 与后端 API 对应的类型定义 */

/** 应用访问参数：前端按当前页面的主机名拼出真实地址，
 *  避免在界面里写死 localhost（从别的机器访问时 localhost 指向的是用户自己的电脑）。 */
export interface AccessInfo {
  /** gateway | cgi | port | unknown */
  kind: 'gateway' | 'cgi' | 'port' | 'unknown';
  /** 打开应用的路径，取自 ui/config 的 url 字段。
   *  可能与网关路由前缀不同：fnchat 前缀是 /app/fnchat，打开地址是 /app/fnchat/ */
  accessPath?: string;
  /** 网关路由前缀（不带尾斜杠），用于展示与诊断 */
  prefix?: string;
  /** 浏览器应访问的端口 */
  accessPort?: number;
  scheme?: string;
}

export interface AppView {
  name: string;
  version: string;
  displayName: string;
  desc: string;
  platform: string;
  dir: string;
  hasUI: boolean;
  isDocker: boolean;

  running: boolean;
  pid?: number;
  port?: string;
  entryType?: string;

  /** 接入方式：gateway | cgi | port | unknown */
  kind: 'gateway' | 'cgi' | 'port' | 'unknown';
  access: AccessInfo;

  /** 远程源图标地址（本地图标不可用时的兜底） */
  icon?: string;
  /** 商店条目编号 */
  storeId?: string;

  updateAvailable: boolean;
  latestVersion?: string;
  latestPath?: string;
}

export interface AppsResponse {
  apps: AppView[];
  total: number;
  packagesDir: string;
}

export interface PackageInfo {
  path: string;
  fileName: string;
  size: number;
  appname: string;
  version: string;
  displayName: string;
  desc: string;
  maintainer: string;
  platform: string;
  servicePort?: string;
  serviceSocket?: string;
  installType?: string;
  hasIcon: boolean;
  checksum?: string;
}

export interface FSEntry {
  name: string;
  path: string;
  isDir: boolean;
  size: number;
  isFpk: boolean;
  mtime: number;
}

export interface FSListResult {
  path: string;
  parent: string;
  entries: FSEntry[];
  fpkCount: number;
}

export interface InspectResult {
  package: PackageInfo;
  installed: boolean;
  installedVersion: string;
  isUpgrade?: boolean;
  isDowngrade?: boolean;
  isSameVersion?: boolean;
}

export interface MetaInfo {
  version: string;
  baseDir: string;
  appsDir: string;
  hostname: string;
  gatewayPort: number;
  webPort: number;
  allowFS: boolean;
  autoStart: boolean;
  /** 服务端显式指定的对外主机名（反向代理场景），为空则用浏览器当前主机名 */
  publicHost?: string;
  publicScheme?: string;
}

/** 应用详情（已安装应用与商店条目共用） */
export interface AppDetail {
  name: string;
  displayName: string;
  version: string;
  desc: string;
  author: string;
  platform: string;
  tags: string[] | null;

  downloads: number;
  ratingAvg: number;
  ratingCount: number;
  size: number;
  updatedAt: string;
  repo: string;
  sourceName: string;
  icon: string;

  installed: boolean;
  installedVersion: string;
  updateAvailable: boolean;
  running: boolean;
  pid?: number;
  port?: string;
  installPath: string;
  kind: string;
  access: AccessInfo;

  storeId?: string;
}

export interface LogLine {
  seq: number;
  level: 'debug' | 'info' | 'warn' | 'error' | string;
  text: string;
  ts: number;
}

export interface TaskProgress {
  done: number;
  total: number;
  /** 0-100；total 未知时为 -1 */
  percent: number;
}

export interface TaskSnapshot {
  id: string;
  kind: string;
  title: string;
  state: 'running' | 'success' | 'failed';
  error?: string;
  started: number;
  ended?: number;
  lines: number;
  progress?: TaskProgress;
  /** 下载类任务的结果（含本地路径与包内向导） */
  result?: Record<string, unknown>;
}

export type AppAction = 'start' | 'stop' | 'uninstall' | 'upgrade';

/** 商店（远程应用源）中的应用 */
export interface StoreApp {
  id: string;
  name: string;
  version: string;
  desc: string;
  author: string;
  tags: string[] | null;
  type: string;
  platform: string;
  updatedAt: string;
  size: number;
  downloads: number;
  ratingAvg: number;
  ratingCount: number;
  repo: string;
  sourceName: string;
  /** 远程图标直链（来自应用源），作为本地提取失败时的兜底 */
  icon: string;

  installed: boolean;
  installedVersion: string;
  updateAvailable: boolean;
  platformSupported: boolean;
  /** 确定不可运行的原因（非空时禁用安装） */
  unsupportedReason?: string;
  /** 软提示：平台标签可能不匹配，但不阻止安装 */
  platformWarning?: string;

  /** 已安装时可用：运行状态与访问参数（供"打开"按钮直接使用） */
  running?: boolean;
  access?: AccessInfo;
  appName?: string;
}

export interface StoreAppsResponse {
  apps: StoreApp[];
  total: number;
  warning?: string;
  arch: string;
}

/** 应用源 */
export interface AppSource {
  id: string;
  name: string;
  url: string;
  enabled: boolean;
}

/** 向导校验规则 */
export interface WizardRule {
  required?: boolean;
  min?: number | null;
  max?: number | null;
  len?: number | null;
  pattern?: string;
  message?: string;
}

export interface WizardOption {
  label: string;
  value: string;
}

/** 向导字段类型 */
export type WizardItemType =
  | 'tips'
  | 'text'
  | 'password'
  | 'radio'
  | 'checkbox'
  | 'select'
  | 'switch';

export interface WizardItem {
  type: WizardItemType;
  field: string;
  label: string;
  helpText: string;
  placeholder: string;
  initValue: string;
  options: WizardOption[] | null;
  rules: WizardRule[] | null;
}

export interface WizardStep {
  stepTitle: string;
  items: WizardItem[];
}

export interface WizardResponse {
  exists: boolean;
  kind: 'install' | 'uninstall' | 'config' | 'upgrade' | string;
  title: string;
  steps: WizardStep[] | null;
  defaults: Record<string, string> | null;
}
