import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Button,
  Empty,
  Spin,
  Toast,
  Typography,
  Space,
  Input,
  Banner,
  Tooltip,
} from '@douyinfe/semi-ui';
import {
  IconRefresh,
  IconUpload,
  IconFolderOpen,
  IconSearch,
  IconHome,
  IconApps,
  IconExit,
  IconGridSquare,
  IconSetting,
} from '@douyinfe/semi-icons';

import type {
  AppView,
  AppAction,
  MetaInfo,
  StoreApp,
  AppSource,
  StoreAppsResponse,
  WizardResponse,
} from './api/types';
import {
  authStatus,
  currentTask,
  getMeta,
  listApps,
  appAction,
  installLocal,
  setSession,
  setUnauthorizedHandler,
  listStoreApps,
  installFromStore,
  downloadFromStore,
  getPackageWizard,
  getWizard as getAppWizard,
  listSources,
  saveSource,
  deleteSource,
} from './api/client';

import AppCard from './components/AppCard';
import Auth from './components/Auth';
import StoreCard from './components/StoreCard';
import FileBrowser from './components/FileBrowser';
import UploadInstaller from './components/UploadInstaller';
import InstallConfirm from './components/InstallConfirm';
import WizardModal from './components/WizardModal';
import AppDetailPage from './components/AppDetailPage';
import { buildAccessURL } from './utils/format';
import TaskModal from './components/TaskModal';
import Settings, { type SettingsTab } from './components/Settings';

const { Text } = Typography;

/** 侧边栏视图 */
type View = 'home' | 'all' | 'installed' | 'detail';

/** 表示"全部分类" */
const ALL_CATEGORY = '__all__';

export default function App() {
  const [ready, setReady] = useState(false);
  const [needsSetup, setNeedsSetup] = useState(false);
  const [needLogin, setNeedLogin] = useState(false);

  const [view, setView] = useState<View>('home');
  // 详情页目标：已安装应用名 或 商店条目 id
  const [detailTarget, setDetailTarget] = useState<{ appname?: string; storeId?: string } | null>(null);
  // 详情页来源视图，返回时回到原处（商店列表 / 已安装列表）
  const [detailFrom, setDetailFrom] = useState<View>('home');
  const [category, setCategory] = useState<string>(ALL_CATEGORY);
  const [keyword, setKeyword] = useState('');
  const [meta, setMeta] = useState<MetaInfo | null>(null);

  // 商店
  const [store, setStore] = useState<StoreAppsResponse | null>(null);
  const [storeLoading, setStoreLoading] = useState(false);
  const [storeError, setStoreError] = useState('');

  // 已安装
  const [apps, setApps] = useState<AppView[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  // 应用源
  const [sources, setSources] = useState<AppSource[]>([]);
  const [settingsVisible, setSettingsVisible] = useState(false);
  const [settingsTab, setSettingsTab] = useState<SettingsTab>('sources');

  // 本地/上传安装
  const [fsVisible, setFsVisible] = useState(false);
  const [uploadVisible, setUploadVisible] = useState(false);
  const [confirmPath, setConfirmPath] = useState<string | null>(null);

  // 任务
  const [taskId, setTaskId] = useState<string | null>(null);
  const [taskTitle, setTaskTitle] = useState('');
  const [taskVisible, setTaskVisible] = useState(false);

  // 正在安装的商店应用 ID
  const [installingId, setInstallingId] = useState<string | null>(null);

  // 下载任务的后续动作（下载完成后触发）
  const [pendingDownload, setPendingDownload] = useState<{ taskId: string; appName: string } | null>(null);

  // 向导：在装/卸之前弹出，用户提交后才创建任务并显示日志窗口
  const [wizard, setWizard] = useState<WizardResponse | null>(null);
  const [wizardLoading, setWizardLoading] = useState(false);
  const [wizardError, setWizardError] = useState('');
  const [wizardSubject, setWizardSubject] = useState('');
  const [wizardSubmitting, setWizardSubmitting] = useState(false);
  // 向导完成后的动作（安装或卸载）
  const [wizardAction, setWizardAction] = useState<
    | { type: 'install'; path: string; title: string }
    | { type: 'uninstall'; name: string }
    | null
  >(null);

  const refreshInstalled = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const res = await listApps();
      setApps(res.apps ?? []);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  const refreshStore = useCallback(async (force = false) => {
    setStoreLoading(true);
    setStoreError('');
    try {
      const res = await listStoreApps(force);
      setStore(res);
    } catch (e) {
      setStoreError(e instanceof Error ? e.message : String(e));
    } finally {
      setStoreLoading(false);
    }
  }, []);

  const refreshSources = useCallback(async () => {
    try {
      const res = await listSources();
      setSources(res.sources ?? []);
    } catch {
      /* 源列表失败不阻塞主流程 */
    }
  }, []);

  const bootstrap = useCallback(async () => {
    try {
      const st = await authStatus();
      setNeedsSetup(st.needsSetup);

      if (st.needsSetup || !st.authenticated) {
        setNeedLogin(true);
        return;
      }

      setNeedLogin(false);
      await Promise.all([refreshInstalled(), refreshStore(), refreshSources()]);
      getMeta().then(setMeta).catch(() => undefined);

      // 刷新页面时恢复进行中的任务
      currentTask()
        .then((res) => {
          if (res.task && res.task.state === 'running') {
            setTaskId(res.task.id);
            setTaskTitle(res.task.title);
            setTaskVisible(true);
          }
        })
        .catch(() => undefined);
    } catch {
      setNeedLogin(true);
    } finally {
      setReady(true);
    }
  }, [refreshInstalled, refreshStore, refreshSources]);

  useEffect(() => {
    setUnauthorizedHandler(() => {
      setSession('');
      setNeedLogin(true);
    });
    bootstrap();
    return () => setUnauthorizedHandler(null);
  }, [bootstrap]);

  /** 打开向导（若应用/包没有向导则直接执行动作） */
  const openWizard = async (
    kind: 'install' | 'uninstall',
    source: { app?: string; path?: string },
    action: NonNullable<typeof wizardAction>,
    subject: string,
  ) => {
    setWizardAction(action);
    setWizardSubject(subject);
    setWizard(null);
    setWizardError('');
    setWizardLoading(true);

    try {
      const res = source.path
        ? await getPackageWizard(source.path, kind)
        : await getAppWizard(source.app!, kind);

      if (!res.exists || !res.steps?.length) {
        // 没有向导：跳过弹窗，直接执行
        setWizardAction(null);
        await runWizardAction(action, {});
        return;
      }
      setWizard(res);
    } catch (e) {
      // 向导读取失败不阻断主流程，直接执行
      setWizardAction(null);
      Toast.warning('读取向导失败，将直接执行：' + (e instanceof Error ? e.message : String(e)));
      await runWizardAction(action, {});
    } finally {
      setWizardLoading(false);
    }
  };

  /** 真正执行安装/卸载，并打开日志窗口 */
  const runWizardAction = async (
    action: NonNullable<typeof wizardAction>,
    values: Record<string, string>,
  ) => {
    if (action.type === 'install') {
      const res = await installLocal(action.path, values);
      setTaskTitle(action.title);
      setTaskId(res.taskId);
      setTaskVisible(true);
      return;
    }
    const res = await appAction(action.name, 'uninstall', undefined, values);
    setTaskTitle(`卸载 ${action.name}`);
    setTaskId(res.taskId);
    setTaskVisible(true);
  };

  /**
   * 商店应用安装：下载（显示进度）→ 有向导则弹向导，无向导直接安装。
   *
   * 下载是长耗时操作（大包可能几十 MB），因此先弹任务窗口显示实时进度，
   * 下载结束后再自动进入下一步，用户不必等待无反馈。
   */
  const doStoreInstall = async (app: StoreApp | { id: string; name: string }) => {
    const id = 'id' in app ? app.id : '';
    const name = 'name' in app ? app.name : id;
    setInstallingId(id);
    try {
      const dl = await downloadFromStore(id);
      setInstallingId(null);

      // 记录"下载任务的后续动作"：下载成功后再弹向导或直接安装
      setPendingDownload({
        taskId: dl.taskId,
        appName: dl.name || name,
      });

      setTaskTitle(`下载 ${dl.name || name}`);
      setTaskId(dl.taskId);
      setTaskVisible(true);
    } catch (e) {
      setInstallingId(null);
      Toast.error(e instanceof Error ? e.message : String(e));
    }
  };

  /**
   * 下载任务结束后的收尾：有向导则弹向导，否则直接安装。
   * 由 TaskModal 的 onFinished 调用。
   */
  const handleTaskFinished = useCallback(
    async (snap: import('./api/types').TaskSnapshot) => {
      if (snap.kind !== 'download' || snap.state !== 'success') return;

      const res = (snap.result ?? {}) as {
        path?: string;
        name?: string;
        wizard?: WizardResponse | null;
      };
      if (!res.path) return;

      const title = `安装 ${res.name || pendingDownload?.appName || ''}`.trim();

      if (res.wizard?.exists && res.wizard.steps?.length) {
        // 先关掉下载日志窗口，避免与向导弹窗叠在一起
        setTaskVisible(false);
        setTaskId(null);
        setPendingDownload(null);

        setWizardAction({ type: 'install', path: res.path, title });
        setWizardSubject(title);
        setWizard(res.wizard);
        setWizardError('');
        return;
      }

      // 没有向导：先把下载窗口收起来，再直接安装（安装会开新的任务窗口）
      setTaskVisible(false);
      setTaskId(null);
      setPendingDownload(null);
      await runWizardAction({ type: 'install', path: res.path, title }, {});
    },
    [pendingDownload],
  );

  const runAction = async (name: string, action: AppAction, path?: string) => {
    // 卸载：先弹卸载向导
    if (action === 'uninstall') {
      await openWizard('uninstall', { app: name }, { type: 'uninstall', name }, `卸载 ${name}`);
      return;
    }

    try {
      const res = await appAction(name, action, path);
      const label = action === 'start' ? '启动' : action === 'stop' ? '停止' : '升级';
      setTaskTitle(`${label} ${name}`);
      setTaskId(res.taskId);
      setTaskVisible(true);
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : String(e));
    }
  };

  /** 向导提交 */
  const submitWizard = async (values: Record<string, string>) => {
    if (!wizardAction) return;
    setWizardSubmitting(true);
    try {
      const action = wizardAction;
      setWizard(null);
      setWizardAction(null);
      await runWizardAction(action, values);
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : String(e));
    } finally {
      setWizardSubmitting(false);
    }
  };

  /** 取消向导 */
  const cancelWizard = () => {
    setWizard(null);
    setWizardAction(null);
    setWizardError('');
  };

  /**
   * 切换视图时移除当前焦点。
   *
   * 浏览器的输入框在保持焦点时会绘制闪烁的文本插入符；
   * 从搜索框点进应用详情后，若焦点仍留在搜索框上，
   * 详情页里就会看到一个不相干的插入符在闪。
   * 这里在导航时主动 blur，避免该现象。
   */
  const blurActive = () => {
    const el = document.activeElement as HTMLElement | null;
    if (el && el !== document.body && typeof el.blur === 'function') {
      el.blur();
    }
  };

  /** 打开已安装应用详情（记住来源视图，便于返回） */
  const openInstalledDetail = (name: string) => {
    blurActive();
    setDetailFrom(view);
    setDetailTarget({ appname: name });
    setView('detail');
  };

  /** 打开商店应用详情 */
  const openStoreDetail = (storeId: string) => {
    blurActive();
    setDetailFrom(view);
    setDetailTarget({ storeId });
    setView('detail');
  };

  const onTaskDone = useCallback(() => {
    refreshInstalled();
    refreshStore();
  }, [refreshInstalled, refreshStore]);

  /** 分类：从远程应用的 tags 聚合 */
  const categories = useMemo(() => {
    const counts = new Map<string, number>();
    for (const a of store?.apps ?? []) {
      for (const t of a.tags ?? []) {
        counts.set(t, (counts.get(t) ?? 0) + 1);
      }
    }
    return Array.from(counts.entries()).sort((x, y) => y[1] - x[1]);
  }, [store]);

  const filteredStore = useMemo(() => {
    const kw = keyword.trim().toLowerCase();
    return (store?.apps ?? []).filter((a) => {
      if (category !== ALL_CATEGORY && !(a.tags ?? []).includes(category)) return false;
      if (!kw) return true;
      return (
        a.name.toLowerCase().includes(kw) ||
        (a.author ?? '').toLowerCase().includes(kw) ||
        (a.desc ?? '').toLowerCase().includes(kw)
      );
    });
  }, [store, category, keyword]);

  const filteredInstalled = useMemo(() => {
    const kw = keyword.trim().toLowerCase();
    if (!kw) return apps;
    return apps.filter(
      (a) =>
        a.name.toLowerCase().includes(kw) ||
        (a.displayName ?? '').toLowerCase().includes(kw) ||
        (a.desc ?? '').toLowerCase().includes(kw),
    );
  }, [apps, keyword]);

  const runningCount = apps.filter((a) => a.running).length;
  const updatableCount = (store?.apps ?? []).filter((a) => a.updateAvailable).length;

  if (!ready) {
    return (
      <div className="fpkm-center">
        <Spin size="large" />
      </div>
    );
  }

  if (needLogin) {
    return (
      <Auth
        needsSetup={needsSetup}
        onSuccess={() => {
          setNeedLogin(false);
          setReady(false);
          bootstrap();
        }}
      />
    );
  }

  const renderStore = () => {
    if (storeLoading && !store) {
      return (
        <div style={{ textAlign: 'center', padding: 80 }}>
          <Spin size="large" />
        </div>
      );
    }
    if (filteredStore.length === 0) {
      return (
        <Empty
          title="没有匹配的应用"
          description="试试切换分类、换个关键词，或在「应用源」里添加更多源"
        />
      );
    }
    return (
      <div className="fpkm-grid">
        {filteredStore.map((a) => (
          <StoreCard
            key={a.id || a.name}
            app={a}
            busy={installingId === a.id}
            onInstall={doStoreInstall}
            onOpenDetail={openStoreDetail}
            accessURL={a.installed && a.running ? buildAccessURL(a.access, meta?.publicHost, meta?.publicScheme) : null}
          />
        ))}
      </div>
    );
  };

  const renderInstalled = () => {
    if (loading && apps.length === 0) {
      return (
        <div style={{ textAlign: 'center', padding: 80 }}>
          <Spin size="large" />
        </div>
      );
    }
    if (filteredInstalled.length === 0) {
      return (
        <Empty
          title={apps.length === 0 ? '还没有安装任何应用' : '没有匹配的应用'}
          description={
            apps.length === 0
              ? '去「全部应用」里挑选，或从本地文件 / 上传安装包安装'
              : '试试调整搜索关键词'
          }
        />
      );
    }
    return (
      <div className="fpkm-app-grid">
        {filteredInstalled.map((a) => (
          <AppCard
            key={a.name}
            app={a}
            busy={loading}
            publicHost={meta?.publicHost}
            publicScheme={meta?.publicScheme}
            onAction={runAction}
            onOpenDetail={openInstalledDetail}
          />
        ))}
      </div>
    );
  };

  /** 分类下的应用数量 */
  const countOf = (name: string) =>
    name === ALL_CATEGORY
      ? store?.total ?? 0
      : categories.find(([n]) => n === name)?.[1] ?? 0;

  const chips = (
    <div className="fpkm-chips">
      <span
        className={`fpkm-chip ${category === ALL_CATEGORY ? 'fpkm-chip-active' : ''}`}
        onClick={() => {
          setCategory(ALL_CATEGORY);
          setKeyword('');
        }}
      >
        全部应用
      </span>
      {categories.slice(0, 10).map(([name]) => (
        <span
          key={name}
          className={`fpkm-chip ${category === name ? 'fpkm-chip-active' : ''}`}
          onClick={() => setCategory(name)}
        >
          {name}
        </span>
      ))}
    </div>
  );

  const sourceWarning = store?.warning ? (
    <Banner
      type="warning"
      closeIcon={null}
      description={`部分应用源不可用：${store.warning}`}
      style={{ marginBottom: 16 }}
    />
  ) : null;

  return (
    <div className="fpkm-shell">
      {/* ================= 侧边栏 ================= */}
      <aside className="fpkm-side">
        <div className="fpkm-side-brand">
          <img className="fpkm-side-logo" src="/fpkm-logo.png" alt="FPKM" />
          <div>
            <div className="fpkm-side-title">FPKM 应用商店</div>
            <div className="fpkm-side-sub">让 NAS 更强大</div>
          </div>
        </div>

        <nav className="fpkm-nav">
          <div
            className={`fpkm-nav-item ${view === 'home' ? 'fpkm-nav-item-active' : ''}`}
            onClick={() => {
              setView('home');
              setCategory(ALL_CATEGORY);
              setKeyword('');
            }}
          >
            <IconHome />
            首页
          </div>
          <div
            className={`fpkm-nav-item ${view === 'all' ? 'fpkm-nav-item-active' : ''}`}
            onClick={() => {
              setView('all');
              setKeyword('');
            }}
          >
            <IconGridSquare />
            全部应用
            {store ? <span className="fpkm-nav-badge">{store.total}</span> : null}
          </div>

          <div className="fpkm-nav-group-title">分类</div>
          <div
            className={`fpkm-nav-item ${
              view === 'all' && category === ALL_CATEGORY ? 'fpkm-nav-item-active' : ''
            }`}
            onClick={() => {
              setView('all');
              setCategory(ALL_CATEGORY);
            }}
          >
            <IconApps />
            全部
            <span className="fpkm-nav-badge">{countOf(ALL_CATEGORY)}</span>
          </div>
          {categories.slice(0, 12).map(([name]) => (
            <div
              key={name}
              className={`fpkm-nav-item ${
                view === 'all' && category === name ? 'fpkm-nav-item-active' : ''
              }`}
              onClick={() => {
                setView('all');
                setCategory(name);
              }}
            >
              <span style={{ width: 16, textAlign: 'center' }}>·</span>
              {name}
              <span className="fpkm-nav-badge">{countOf(name)}</span>
            </div>
          ))}

          <div className="fpkm-nav-group-title">管理</div>
          <div
            className={`fpkm-nav-item ${view === 'installed' ? 'fpkm-nav-item-active' : ''}`}
            onClick={() => {
              setView('installed');
              setKeyword('');
            }}
          >
            <IconApps />
            已安装
            {apps.length ? <span className="fpkm-nav-badge">{apps.length}</span> : null}
          </div>
          <div
            className={`fpkm-nav-item ${settingsVisible ? 'fpkm-nav-item-active' : ''}`}
            onClick={() => {
              setSettingsTab('sources');
              setSettingsVisible(true);
            }}
          >
            <IconSetting />
            应用源
          </div>
        </nav>
      </aside>

      {/* ================= 主区域 ================= */}
      <div className="fpkm-main">
        <header className="fpkm-top">
          <Input
            className="fpkm-top-search"
            prefix={<IconSearch />}
            placeholder="搜索应用名称、关键词、开发者..."
            value={keyword}
            onChange={setKeyword}
            showClear
            size="large"
          />

          {/* 右上角统一为无背景的图标按钮，悬停有浅色底以提示可点 */}
          <div className="fpkm-top-right">
            <Tooltip content="刷新应用源">
              <Button
                className="fpkm-icon-btn"
                icon={<IconRefresh />}
                theme="borderless"
                type="tertiary"
                loading={storeLoading}
                aria-label="刷新"
                onClick={() => {
                  refreshStore(true);
                  refreshInstalled();
                }}
              />
            </Tooltip>

            <Tooltip content="从服务器本地文件安装">
              <Button
                className="fpkm-icon-btn"
                icon={<IconFolderOpen />}
                theme="borderless"
                type="tertiary"
                aria-label="本地文件安装"
                onClick={() => setFsVisible(true)}
              />
            </Tooltip>

            <Tooltip content="上传安装包安装">
              <Button
                className="fpkm-icon-btn"
                icon={<IconUpload />}
                theme="borderless"
                type="tertiary"
                aria-label="上传安装"
                onClick={() => setUploadVisible(true)}
              />
            </Tooltip>

            <Tooltip content="设置">
              <Button
                className="fpkm-icon-btn"
                icon={<IconSetting />}
                theme="borderless"
                type="tertiary"
                aria-label="设置"
                onClick={() => {
                  setSettingsTab('sources');
                  setSettingsVisible(true);
                }}
              />
            </Tooltip>

            <Tooltip content="退出登录">
              <Button
                className="fpkm-icon-btn"
                icon={<IconExit />}
                theme="borderless"
                type="tertiary"
                aria-label="退出登录"
                onClick={() => {
                  setSession('');
                  setNeedLogin(true);
                }}
              />
            </Tooltip>
          </div>
        </header>

        <div className="fpkm-body">
          {view === 'home' ? (
            <>
              <div className="fpkm-banner">
                <div className="fpkm-banner-title">探索更多可能</div>
                <div className="fpkm-banner-sub">海量应用 · 一键安装 · 轻松扩展你的 NAS</div>
                <Space>
                  <Button
                    theme="solid"
                    type="primary"
                    size="large"
                    onClick={() => {
                      setView('all');
                      setCategory(ALL_CATEGORY);
                    }}
                  >
                    开始探索
                  </Button>
                  {updatableCount > 0 ? (
                    <Button size="large" theme="light" type="primary" onClick={() => setView('all')}>
                      {updatableCount} 个可更新
                    </Button>
                  ) : null}
                </Space>
                <div className="fpkm-banner-art">
                  {/* 内联 SVG：不依赖系统 emoji 字体，任何环境渲染一致 */}
                  <svg width="150" height="110" viewBox="0 0 150 110" fill="none" aria-hidden="true">
                    <rect x="18" y="30" width="114" height="70" rx="12" fill="#bcd4ff" />
                    <rect x="18" y="30" width="114" height="24" rx="12" fill="#9dbeff" />
                    <rect x="30" y="24" width="90" height="12" rx="6" fill="#7ea8ff" />
                    <circle cx="34" cy="58" r="5" fill="#fff" />
                    <circle cx="52" cy="58" r="5" fill="#fff" />
                    <rect x="30" y="74" width="60" height="6" rx="3" fill="#fff" opacity="0.85" />
                    <rect x="30" y="86" width="40" height="6" rx="3" fill="#fff" opacity="0.6" />
                    <circle cx="126" cy="34" r="16" fill="#cfe0ff" />
                    <path d="M118 34a8 8 0 0 1 16 0" stroke="#5b9dff" strokeWidth="3" fill="none" strokeLinecap="round"/>
                    <circle cx="40" cy="14" r="9" fill="#dbe8ff" />
                  </svg>
                </div>
              </div>

              {chips}
              {sourceWarning}

              {storeError ? (
                <Banner
                  type="warning"
                  closeIcon={null}
                  description={`应用源加载失败：${storeError}`}
                  style={{ marginBottom: 16 }}
                />
              ) : null}

              {renderStore()}
            </>
          ) : null}

          {view === 'all' ? (
            <>
              <div className="fpkm-section-title">
                {category === ALL_CATEGORY ? '全部应用' : category}
              </div>
              <div className="fpkm-section-sub">
                {store
                  ? `共 ${filteredStore.length} 个应用 · 来自 ${sources.length} 个应用源`
                  : '正在加载…'}
              </div>

              {chips}
              {sourceWarning}

              {storeError ? (
                <Banner
                  type="warning"
                  closeIcon={null}
                  description={`应用源加载失败：${storeError}`}
                  style={{ marginBottom: 16 }}
                />
              ) : null}

              {renderStore()}
            </>
          ) : null}

          {view === 'detail' && detailTarget ? (
            <AppDetailPage
              appname={detailTarget.appname}
              storeId={detailTarget.storeId}
              publicHost={meta?.publicHost}
              publicScheme={meta?.publicScheme}
              busy={loading}
              onBack={() => {
                setDetailTarget(null);
                setView(detailFrom);
              }}
              onAction={runAction}
              onInstall={(id) => doStoreInstall({ id, name: '' })}
            />
          ) : null}

          {view === 'installed' ? (
            <>
              <div className="fpkm-section-title">已安装应用</div>
              <div className="fpkm-section-sub">
                共 {apps.length} 个应用，{runningCount} 个运行中
              </div>
              {error ? (
                <Banner
                  type="danger"
                  description={error}
                  closeIcon={null}
                  style={{ marginBottom: 16 }}
                />
              ) : null}
              {renderInstalled()}
            </>
          ) : null}
        </div>
      </div>

      {/* ================= 弹层 ================= */}

      <Settings
        visible={settingsVisible}
        initialTab={settingsTab}
        meta={meta}
        onClose={() => setSettingsVisible(false)}
        onSourcesChanged={(list) => {
          setSources(list);
          refreshStore(true);
        }}
      />

      <FileBrowser
        visible={fsVisible}
        onCancel={() => setFsVisible(false)}
        onPick={(p) => {
          setFsVisible(false);
          setConfirmPath(p);
        }}
      />

      <UploadInstaller
        visible={uploadVisible}
        onCancel={() => setUploadVisible(false)}
        onTask={(id, title) => {
          setUploadVisible(false);
          setTaskId(id);
          setTaskTitle(title);
          setTaskVisible(true);
        }}
      />

      <InstallConfirm
        visible={confirmPath !== null}
        packagePath={confirmPath}
        onCancel={() => setConfirmPath(null)}
        onConfirm={(path, title) => {
          // 用户已确认包信息：接着弹安装向导，向导提交后才真正安装
          setConfirmPath(null);
          openWizard('install', { path }, { type: 'install', path, title }, title);
        }}
      />

      <WizardModal
        visible={wizard !== null || wizardLoading}
        wizard={wizard}
        loading={wizardLoading}
        error={wizardError}
        subject={wizardSubject}
        submitting={wizardSubmitting}
        onCancel={cancelWizard}
        onSubmit={submitWizard}
      />

      <TaskModal
        visible={taskVisible}
        taskId={taskId}
        title={taskTitle || '执行进度'}
        onClose={() => {
          setTaskVisible(false);
          setTaskId(null);
          setPendingDownload(null);
        }}
        onSuccess={onTaskDone}
        onFinished={handleTaskFinished}
      />
    </div>
  );
}
