import { useEffect, useState } from 'react';
import {
  Button,
  Spin,
  Typography,
  Space,
  Tag,
  Toast,
  Banner,
  Modal,
  Dropdown,
  SplitButtonGroup,
  Tooltip,
} from '@douyinfe/semi-ui';
import {
  IconArrowLeft,
  IconPlay,
  IconDownloadStroked,
  IconRefresh,
  IconExternalOpen,
  IconLink,
  IconChevronDown,
  IconSetting,
} from '@douyinfe/semi-icons';
import type { AppDetail, StoreApp } from '../api/types';
import { getAppDetail, getStoreDetail, iconURL } from '../api/client';
import { buildAccessURL, formatSize, stripHtml } from '../utils/format';

interface Props {
  /** 已安装应用名 或 商店条目 id（二选一） */
  appname?: string;
  storeId?: string;
  publicHost?: string;
  publicScheme?: string;
  busy?: boolean;
  onBack: () => void;
  onAction: (name: string, action: 'start' | 'stop' | 'uninstall' | 'upgrade', path?: string) => void;
  onInstall: (id: string) => void;
  /**
   * 按应用名打开。桌面模式下由桌面提供，
   * 使详情页的「打开」与桌面图标行为一致（子窗口 / 新标签页）。
   */
  onOpenApp?: (name: string) => void;
  /** 打开「应用设置」弹窗（选择端口 / 网关 / CGI 接入方式） */
  onOpenSettings?: (appname: string) => void;
}

const { Text, Title, Paragraph } = Typography;

/** 详情页图标：三级降级，与商店卡片一致 */
function DetailIcon({ detail }: { detail: AppDetail }) {
  // 0=本地/接口图标 → 1=远程源图标 → 2=首字母兜底
  const [stage, setStage] = useState<0 | 1 | 2>(0);

  if (stage === 2) {
    return (
      <span className="fpkm-detail-icon-fallback">{(detail.displayName || '?').slice(0, 1)}</span>
    );
  }
  const src = stage === 0 ? iconURL(detail.name) : detail.icon || '';
  if (!src) {
    return (
      <span className="fpkm-detail-icon-fallback">{(detail.displayName || '?').slice(0, 1)}</span>
    );
  }
  return (
    <img src={src} alt={detail.displayName} onError={() => setStage((s) => (s === 0 && detail.icon ? 1 : 2))} />
  );
}

/** 应用详情页（参考飞牛应用中心详情） */
export default function AppDetailPage({
  appname,
  storeId,
  publicHost,
  publicScheme,
  busy,
  onBack,
  onAction,
  onInstall,
  onOpenApp,
  onOpenSettings,
}: Props) {
  const [detail, setDetail] = useState<AppDetail | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    let alive = true;
    setLoading(true);
    setError('');
    setDetail(null);

    const p = appname ? getAppDetail(appname) : storeId ? getStoreDetail(storeId) : null;
    if (!p) {
      setError('缺少应用标识');
      setLoading(false);
      return;
    }
    p.then((d) => {
      if (alive) setDetail(d);
    })
      .catch((e) => {
        if (alive) setError(e instanceof Error ? e.message : String(e));
      })
      .finally(() => {
        if (alive) setLoading(false);
      });

    return () => {
      alive = false;
    };
  }, [appname, storeId]);

  if (loading) {
    return (
      <div className="fpkm-center" style={{ minHeight: 320 }}>
        <Spin size="large" />
      </div>
    );
  }

  if (error || !detail) {
    return (
      <div>
        <Button icon={<IconArrowLeft />} theme="borderless" onClick={onBack}>
          返回
        </Button>
        <Banner type="danger" description={error || '未找到应用'} closeIcon={null} style={{ marginTop: 12 }} />
      </div>
    );
  }

  const accessURL = buildAccessURL(detail.access, publicHost, publicScheme);
  const tags = detail.tags ?? [];

  /** 元信息行（开发者/发布者/下载数/大小/安装位置/当前版本） */
  const metaItems = [
    { label: '开发者', value: detail.author || '-' },
    { label: '发布者', value: detail.author || '-' },
    ...(detail.downloads > 0 ? [{ label: '下载数', value: String(detail.downloads) }] : []),
    ...(detail.size > 0 ? [{ label: '大小', value: formatSize(detail.size) }] : []),
    ...(detail.installed ? [{ label: '安装位置', value: detail.installPath }] : []),
    { label: detail.installed ? '当前版本' : '版本', value: detail.version || '-' },
    ...(detail.updatedAt ? [{ label: '更新时间', value: detail.updatedAt }] : []),
  ];

  /** 确认卸载（下拉菜单项触发） */
  const confirmUninstall = () => {
    Modal.confirm({
      title: `确认卸载 ${detail?.displayName || detail?.name || ''}？`,
      content: '将停止应用并删除其目录与全部数据，操作不可撤销。',
      okText: '卸载',
      cancelText: '取消',
      okButtonProps: { type: 'danger', theme: 'solid' },
      onOk: () => {
        if (detail) onAction(detail.name, 'uninstall');
      },
    });
  };

  /**
   * 主操作区。
   *
   * 已安装的应用统一渲染为**分裂按钮**（Semi SplitButtonGroup）：
   *   [ 打开 ▾ ]
   * 主按钮是"打开"（应用运行中）或"启动"（未运行），
   * 下拉菜单里放次要操作：停止、卸载。
   *
   * 这样"停止"与"卸载"不再占主界面位置，避免误点；
   * 也与飞牛应用中心的交互一致。
   */
  const primaryAction = () => {
    if (!detail) return null;

    // 未安装：安装按钮
    //
    // 禁用条件必须与商店列表卡片一致：由服务端按
    // "本地包实测优先、其次 platform 标签" 给出的 platformSupported 决定。
    // 早期版本这里写死 `detail.platform === 'arm'` 是给 x86 NAS 用的老规则，
    // 在 arm64 设备上会把**所有 arm 应用**的安装按钮置灰（明明能装），
    // 造成"列表能装、详情页灰着点不动"的不一致。
    if (!detail.installed) {
      const unsupported = detail.platformSupported === false;
      return (
        <Space spacing={8} wrap>
          <Tooltip content={detail.unsupportedReason || ''} position="top">
            <Button
              size="small"
              theme="light"
              type="primary"
              icon={<IconDownloadStroked />}
              disabled={busy || unsupported}
              onClick={() => detail.storeId && onInstall(detail.storeId)}
            >
              安装
            </Button>
          </Tooltip>
          {unsupported ? (
            <Tag size="small" color="grey">
              不支持 {detail.platform}
            </Tag>
          ) : detail.platformWarning ? (
            <Tooltip content={detail.platformWarning}>
              <Tag size="small" color="orange">
                标注 {detail.platform}
              </Tag>
            </Tooltip>
          ) : null}
        </Space>
      );
    }

    return (
      <>
      <SplitButtonGroup className="fpkm-split-btn">
        {/* 主按钮：运行中→打开；未运行→启动 */}
        {detail.running ? (
          <Button
            size="small"
            theme="light"
            type="primary"
            icon={<IconExternalOpen />}
            disabled={!accessURL}
            onClick={() => {
              // 桌面模式：交给桌面按 launchMode 决定子窗口/新标签页
              if (onOpenApp) {
                onOpenApp(detail.name);
                return;
              }
              if (accessURL) window.open(accessURL, '_blank', 'noopener');
            }}
          >
            打开
          </Button>
        ) : (
          <Button
            size="small"
            theme="light"
            type="primary"
            icon={<IconPlay />}
            disabled={busy}
            onClick={() => onAction(detail.name, 'start')}
          >
            启动
          </Button>
        )}

        {/* 下拉：停止 / 卸载 */}
        <Dropdown
          trigger="click"
          position="bottomRight"
          clickToHide
          render={
            // 菜单宽度与分裂按钮整体一致（见 .fpkm-split-menu）
            <Dropdown.Menu className="fpkm-split-menu">
              {/* 仅在运行中才显示"停止" */}
              {detail.running ? (
                <Dropdown.Item onClick={() => onAction(detail.name, 'stop')}>
                  停止
                </Dropdown.Item>
              ) : null}
              <Dropdown.Item type="danger" onClick={confirmUninstall}>
                卸载
              </Dropdown.Item>
            </Dropdown.Menu>
          }
        >
          <Button
            size="small"
            theme="light"
            type="primary"
            icon={<IconChevronDown />}
            aria-label="更多操作"
          />
        </Dropdown>
      </SplitButtonGroup>

      {/* 应用设置：放在「打开」分裂按钮右侧（参考飞牛应用设置入口） */}
      {onOpenSettings ? (
        <Button
          size="small"
          theme="light"
          type="primary"
          icon={<IconSetting />}
          onClick={() => onOpenSettings(detail.name)}
        >
          应用设置
        </Button>
      ) : null}
      </>
    );
  };

  return (
    <div className="fpkm-detail">
      <Button icon={<IconArrowLeft />} theme="borderless" onClick={onBack} className="fpkm-detail-back">
        返回
      </Button>

      {/* 头部：图标 + 标题 + 操作 */}
      <div className="fpkm-detail-head">
        <div className="fpkm-detail-icon">
          <DetailIcon detail={detail} />
        </div>

        <div className="fpkm-detail-headmain">
          <Title heading={4} style={{ margin: 0 }}>
            {detail.displayName || detail.name}
          </Title>
          <div className="fpkm-detail-sub">
            {tags.length > 0 ? (
              tags.slice(0, 2).join(' · ')
            ) : detail.sourceName ? (
              detail.sourceName
            ) : (
              <Text type="tertiary">{detail.name}</Text>
            )}
          </div>

          {/* 操作区：分裂按钮（打开/启动 + 下拉菜单） */}
          <div className="fpkm-detail-actions" onClick={(e) => e.stopPropagation()}>
            <Space spacing={8} wrap className="fpkm-detail-actions-main">
              {primaryAction()}
            </Space>
          </div>
        </div>
      </div>

      {/* 元信息行 */}
      <div className="fpkm-detail-meta">
        {metaItems.map((m) => (
          <div key={m.label} className="fpkm-detail-meta-item">
            <div className="fpkm-detail-meta-label">{m.label}</div>
            <div className="fpkm-detail-meta-value" title={m.value}>
              {m.value}
            </div>
          </div>
        ))}
      </div>

      {/* 运行状态 */}
      {detail.installed ? (
        <Space spacing={6} style={{ marginBottom: 8 }} wrap>
          {detail.running ? (
            <Tag color="green" size="small">
              运行中{detail.pid ? ` #${detail.pid}` : ''}
            </Tag>
          ) : (
            <Tag color="grey" size="small">
              已停止
            </Tag>
          )}
          {detail.port ? <Tag size="small" type="ghost">端口 {detail.port}</Tag> : null}
          {detail.updateAvailable ? (
            <Tag color="orange" size="small">
              可更新到 {detail.version}
            </Tag>
          ) : null}
        </Space>
      ) : null}

      {/* 应用介绍 */}
      <div className="fpkm-detail-section">
        <Title heading={6} style={{ marginBottom: 12 }}>
          应用介绍
        </Title>
        {detail.desc ? (
          // 描述来自 FPK manifest / 应用源，普遍是 HTML 富文本
          <div
            className="fpkm-detail-desc"
            dangerouslySetInnerHTML={{ __html: sanitizeHTML(detail.desc) }}
          />
        ) : (
          <Paragraph type="tertiary">暂无介绍</Paragraph>
        )}

        {detail.repo ? (
          <div style={{ marginTop: 16 }}>
            <Button
              icon={<IconLink />}
              theme="borderless"
              onClick={() => window.open(detail.repo, '_blank', 'noopener')}
            >
              项目主页
            </Button>
          </div>
        ) : null}
      </div>
    </div>
  );
}

/**
 * 清洗描述 HTML：去掉脚本类标签与事件属性。
 * 内容来自第三方 FPK 包或应用源，属于不可信输入。
 */
function sanitizeHTML(html: string): string {
  if (!html) return '';
  let out = html;
  out = out.replace(/<\s*(script|style|iframe|object|embed|link|meta)\b[\s\S]*?<\s*\/\s*\1\s*>/gi, '');
  out = out.replace(/<\s*(script|iframe|object|embed|link|meta)\b[^>]*\/?>/gi, '');
  out = out.replace(/\son[a-z]+\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)/gi, '');
  out = out.replace(
    /(href|src)\s*=\s*("\s*(javascript|data|vbscript):[^"]*"|'\s*(javascript|data|vbscript):[^']*')/gi,
    '$1="#"',
  );
  return out;
}

export type { StoreApp };
