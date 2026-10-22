import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Button,
  Empty,
  RadioGroup,
  Select,
  Spin,
  Switch,
  Tag,
  Toast,
  Tooltip,
  Typography,
} from '@douyinfe/semi-ui';
import {
  IconArrowLeft,
  IconCopy,
  IconDownload,
  IconRefresh,
  IconSearch,
} from '@douyinfe/semi-icons';
import type { AppLogFileView, AppLogsView, AppView } from '../api/types';
import { getAppLogs, getFpkmLogs } from '../api/client';
import { formatSize, formatTime } from '../utils/format';
import { formatTZOffset, localizeLogTimes, viewerOffsetSeconds } from '../utils/logtime';

const { Text } = Typography;

/** 日志来源：应用日志（某个应用）或 FPKM 程序自身日志 */
export type LogsMode = 'app' | 'fpkm';

interface Props {
  /**
   * 日志来源。
   *   - 'app'  ：某个**已安装应用**的日志目录（<应用目录>/logs）
   *   - 'fpkm' ：FPKM **程序自身**的日志目录（<数据目录>/logs）
   * 两者目录与用途都不同，因此拆成两个独立页面（各自一个侧栏入口）。
   */
  mode?: LogsMode;
  /** 已安装应用列表（来自 /api/apps），用于「应用日志」页选择应用 */
  apps: AppView[];
  /** 进入页面时预选的应用名（例如从应用详情页「查看日志」进入） */
  initialApp?: string;
  /** 返回上一视图（通常是首页） */
  onBack?: () => void;
}

/** tail 选项：每个文件读取的尾部字节数 */
const TAIL_OPTIONS = [
  { value: 64 << 10, label: '64 KB' },
  { value: 256 << 10, label: '256 KB' },
  { value: 1 << 20, label: '1 MB' },
  { value: 4 << 20, label: '4 MB（上限）' },
];

/** 自动刷新间隔（毫秒） */
const AUTO_REFRESH_MS = 4000;

/**
 * 日志文件数不超过该值时用「标签」切换，超过则改用「下拉框」：
 * 文件多时标签会挤在一行，下拉框更易检索。
 */
const FILE_TABS_MAX = 6;

/** 每个来源对应的页面文案与提示 */
const MODE_TEXT: Record<LogsMode, { title: string; sub: string; emptyHint: string }> = {
  fpkm: {
    title: 'FPKM 日志',
    sub: 'FPKM 程序自身（网关、宿主进程、安装流程）的日志，用于排查程序层面的问题',
    emptyHint: 'FPKM 暂无可查看的日志',
  },
  app: {
    title: '应用日志',
    sub: '列出所选应用日志目录下的全部日志文件，便于排查启动失败与服务异常',
    emptyHint: '该应用暂无日志文件',
  },
};

/**
 * 日志查看页面（Web / Android / Windows 三端共用）。
 *
 * 同一套组件按 `mode` 提供两个页面：
 *   - 「FPKM 日志」：程序自身的日志目录，无应用选择器；
 *   - 「应用日志」：某个应用日志目录下的全部日志文件，需先选应用。
 *
 * 展示正文前先给出「日志文件」切换控件（文件少用标签、文件多用下拉框），
 * 一次只展示所选文件的正文，不再把所有文件纵向堆叠。手机窄屏下改为纵向
 * 堆叠、可滚动，不溢出。
 *
 * 日志正文行首的时间按**访问者本地时区**显示：日志由宿主进程按其本地时区写入，
 * 宿主时区与浏览者时区可能不同（见 utils/logtime.ts）。
 */
export default function AppLogsPage({ mode = 'app', apps, initialApp, onBack }: Props) {
  const isFpkm = mode === 'fpkm';
  const text = MODE_TEXT[mode];

  const [app, setApp] = useState<string>(initialApp || apps[0]?.name || '');
  const [tail, setTail] = useState<number>(256 << 10);
  const [autoRefresh, setAutoRefresh] = useState(false);
  const [keyword, setKeyword] = useState('');
  const [data, setData] = useState<AppLogsView | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  /** 当前展示的日志文件名（空表示尚未选择，取第一个） */
  const [activeFile, setActiveFile] = useState('');
  const reqSeq = useRef(0);

  // 应用列表异步到达时兜底选第一个；external 预选（从详情页「查看日志」进入）
  // 只在它的值**发生变化**时生效，避免覆盖用户在页面内的手动选择。
  const appliedInit = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (isFpkm) return;
    if (initialApp && initialApp !== appliedInit.current) {
      appliedInit.current = initialApp;
      setApp(initialApp);
      return;
    }
    if (!app && apps.length > 0) {
      setApp(apps[0].name);
    }
  }, [isFpkm, initialApp, apps, app]);

  const load = useCallback(async () => {
    // 应用日志页在没选到应用时不发请求；FPKM 日志页始终有数据源
    if (!isFpkm && !app) return;
    const seq = ++reqSeq.current;
    setLoading(true);
    setError('');
    try {
      const res = isFpkm ? await getFpkmLogs(tail) : await getAppLogs(app, tail);
      // 丢弃过期响应：用户可能已切换到别的应用
      if (seq === reqSeq.current) setData(res);
    } catch (e) {
      if (seq === reqSeq.current) {
        setError(e instanceof Error ? e.message : String(e));
        setData(null);
      }
    } finally {
      if (seq === reqSeq.current) setLoading(false);
    }
  }, [isFpkm, app, tail]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    if (!autoRefresh) return;
    const timer = window.setInterval(() => {
      void load();
    }, AUTO_REFRESH_MS);
    return () => window.clearInterval(timer);
  }, [autoRefresh, load]);

  const appOptions = useMemo(
    () =>
      apps.map((a) => ({
        value: a.name,
        label: a.displayName ? `${a.displayName}（${a.name}）` : a.name,
      })),
    [apps],
  );

  const files = useMemo(() => data?.files ?? [], [data]);
  const totalSize = files.reduce((n, f) => n + f.size, 0);

  // 切页/切应用/刷新后保持所选文件有效：文件没了或首次进入时回退到第一个
  useEffect(() => {
    setActiveFile((cur) => (files.some((f) => f.name === cur) ? cur : files[0]?.name ?? ''));
  }, [files]);

  const active = useMemo(
    () => files.find((f) => f.name === activeFile) ?? files[0],
    [files, activeFile],
  );

  /**
   * 宿主时区 → 访问者本地时区的时差（秒）。
   * 接口未带时区偏移时（旧版服务端）视为与本机一致，不做换算。
   */
  const viewerOffset = useMemo(() => viewerOffsetSeconds(), []);
  const serverOffset = data?.tzOffsetSeconds ?? viewerOffset;
  const deltaSeconds = viewerOffset - serverOffset;

  /** 下载文件名前缀：FPKM 日志用 fpkm，应用日志用应用名 */
  const namePrefix = isFpkm ? 'fpkm' : app;

  /** 把正文行首时间换算成访问者本地时区；接口无时区信息时原样返回 */
  const toLocal = useCallback(
    (content: string) => localizeLogTimes(content, deltaSeconds),
    [deltaSeconds],
  );

  const copy = async (f: AppLogFileView, content: string) => {
    try {
      await navigator.clipboard.writeText(content);
      Toast.success(`已复制 ${f.name}`);
    } catch {
      Toast.error('复制失败：浏览器未授予剪贴板权限');
    }
  };

  const download = (f: AppLogFileView, content: string) => {
    try {
      const blob = new Blob([content], { type: 'text/plain;charset=utf-8' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `${namePrefix}-${f.name}`;
      document.body.appendChild(a);
      a.click();
      a.remove();
      URL.revokeObjectURL(url);
    } catch {
      Toast.error('下载失败');
    }
  };

  /** 按关键词过滤内容行（保留原文，仅用于定位问题） */
  const filterContent = (text: string): string => {
    const kw = keyword.trim();
    if (!kw) return text;
    return text
      .split('\n')
      .filter((line) => line.toLowerCase().includes(kw.toLowerCase()))
      .join('\n');
  };

  /** 渲染单个日志文件：头部（名称/体积/最近写入/操作）+ 正文（时间已本地化） */
  const renderFile = (f: AppLogFileView) => {
    const local = toLocal(f.content);
    const shown = filterContent(local);
    return (
      <section className="fpkm-logs-file" key={f.name}>
        <header className="fpkm-logs-file-head">
          <div className="fpkm-logs-file-main">
            <div className="fpkm-logs-file-name" title={f.path}>
              <span className="fpkm-logs-file-title">{f.name}</span>
              <Tag size="small" type="ghost">
                {formatSize(f.size)}
              </Tag>
              {f.truncated ? (
                <Tag size="small" color="orange">
                  仅显示尾部
                </Tag>
              ) : null}
            </div>
            {f.modTime ? (
              <span className="fpkm-logs-file-time">
                最近写入 {formatTimeReadable(f.modTime)}
              </span>
            ) : null}
          </div>
          <div className="fpkm-logs-file-actions">
            <Tooltip content="复制当前显示内容">
              <Button
                icon={<IconCopy />}
                theme="borderless"
                type="tertiary"
                size="small"
                onClick={() => void copy(f, local)}
              >
                复制
              </Button>
            </Tooltip>
            <Tooltip content="下载当前显示内容">
              <Button
                icon={<IconDownload />}
                theme="borderless"
                type="tertiary"
                size="small"
                onClick={() => download(f, local)}
              >
                下载
              </Button>
            </Tooltip>
          </div>
        </header>
        <pre className="fpkm-logs-content">
          {shown.trim().length > 0 ? shown : keyword.trim() ? '（无匹配行）' : '（暂无内容）'}
        </pre>
      </section>
    );
  };

  const noApps = !isFpkm && apps.length === 0;

  return (
    <div className="fpkm-logs">
      <div className="fpkm-logs-head">
        {onBack ? (
          <Button
            icon={<IconArrowLeft />}
            theme="borderless"
            onClick={onBack}
            className="fpkm-logs-back"
          >
            返回
          </Button>
        ) : null}
        <div className="fpkm-logs-title">
          <span className="fpkm-logs-title-main">{text.title}</span>
          <span className="fpkm-logs-title-sub">{text.sub}</span>
        </div>
      </div>

      {/* 工具栏：应用（仅应用日志）/ 读取上限 / 关键词 / 自动刷新 / 手动刷新 */}
      <div className="fpkm-logs-toolbar">
        {!isFpkm ? (
          <Select
            className="fpkm-logs-app"
            value={app || undefined}
            onChange={(v) => setApp(String(v))}
            optionList={appOptions}
            filter
            placeholder="选择应用"
            emptyContent="没有已安装的应用"
            size="default"
          />
        ) : null}
        <Select
          className="fpkm-logs-tail"
          value={tail}
          onChange={(v) => setTail(Number(v))}
          optionList={TAIL_OPTIONS}
          size="default"
        />
        <div className="fpkm-logs-search">
          <IconSearch className="fpkm-logs-search-icon" />
          <input
            className="fpkm-logs-search-input"
            value={keyword}
            placeholder="过滤日志内容（关键词）"
            onChange={(e) => setKeyword(e.target.value)}
          />
        </div>
        <label className="fpkm-logs-auto">
          <Switch size="small" checked={autoRefresh} onChange={setAutoRefresh} />
          <span>自动刷新</span>
        </label>
        <Tooltip content="立即刷新">
          <Button
            className="fpkm-icon-btn"
            icon={<IconRefresh />}
            theme="borderless"
            type="tertiary"
            loading={loading}
            aria-label="刷新日志"
            onClick={() => void load()}
          />
        </Tooltip>
      </div>

      {noApps ? (
        <div className="fpkm-logs-empty">
          <Empty description="没有已安装的应用，暂无可查看的日志" />
        </div>
      ) : null}

      {error ? (
        <div className="fpkm-logs-banner">
          <Text type="danger">读取日志失败：{error}</Text>
        </div>
      ) : null}

      {loading && !data ? (
        <div className="fpkm-center" style={{ minHeight: 200 }}>
          <Spin size="large" />
        </div>
      ) : null}

      {data && !error ? (
        <>
          <div className="fpkm-logs-meta">
            <Text type="tertiary" size="small">
              {files.length > 0
                ? `共 ${files.length} 个日志文件 · 合计 ${formatSize(totalSize)} · 每个文件最多读取尾部 ${formatSize(data.tailBytes)}`
                : data.hint || text.emptyHint}
            </Text>
            {files.length > 0 ? (
              <Text type="tertiary" size="small" className="fpkm-logs-tz">
                时间：本地时间（{formatTZOffset(viewerOffset)}）
                {deltaSeconds !== 0 ? `，日志记录于 ${formatTZOffset(serverOffset)}` : ''}
              </Text>
            ) : null}
            {data.dir ? (
              <Text type="tertiary" size="small" className="fpkm-logs-dir" title={data.dir}>
                目录：{data.dir}
              </Text>
            ) : null}
          </div>

          {files.length === 0 ? (
            <div className="fpkm-logs-empty">
              <Empty description={data.hint || text.emptyHint} />
            </div>
          ) : (
            <>
              {/* 展示正文前先切换要看的日志文件：文件少用标签，文件多用下拉框 */}
              {files.length > 1 ? (
                <div className="fpkm-logs-fileswitch">
                  <span className="fpkm-logs-fileswitch-label">日志文件</span>
                  {files.length <= FILE_TABS_MAX ? (
                    <RadioGroup
                      className="fpkm-logs-filetabs"
                      type="button"
                      buttonSize="middle"
                      value={active?.name}
                      onChange={(e) => setActiveFile(String(e.target.value))}
                      options={files.map((f) => ({ value: f.name, label: f.name }))}
                    />
                  ) : (
                    <Select
                      className="fpkm-logs-fileselect"
                      value={active?.name}
                      onChange={(v) => setActiveFile(String(v))}
                      filter
                      optionList={files.map((f) => ({
                        value: f.name,
                        label: `${f.name}（${formatSize(f.size)}）`,
                      }))}
                      size="default"
                    />
                  )}
                </div>
              ) : null}
              {active ? renderFile(active) : null}
            </>
          )}
        </>
      ) : null}
    </div>
  );
}

/** 把 RFC3339 时间字符串转成易读形式；解析失败时原样返回 */
function formatTimeReadable(iso: string): string {
  const ms = Date.parse(iso);
  if (Number.isNaN(ms)) return iso;
  return formatTime(ms);
}