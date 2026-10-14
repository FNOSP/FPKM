import { useState } from 'react';
import { Button, Tag, Typography, Space, Tooltip } from '@douyinfe/semi-ui';
import { IconDownloadStroked, IconRefresh, IconPlay } from '@douyinfe/semi-icons';
import type { StoreApp } from '../api/types';
import { iconURL } from '../api/client';
import { formatSize, stripHtml } from '../utils/format';

interface Props {
  app: StoreApp;
  busy: boolean;
  onInstall: (app: StoreApp) => void;
  /** 点击卡片打开详情 */
  onOpenDetail?: (storeId: string) => void;
  /** 已安装应用的访问地址（用于"打开"） */
  accessURL?: string | null;
}

const { Text } = Typography;

/**
 * 应用图标。
 *
 * 依次尝试三个来源，任一失败就降级到下一个：
 *   1. /api/icons/{id}  —— 服务端返回包内图标，或代理并缓存远程图标
 *   2. app.icon         —— 应用源直链（第三方图床），前一步不可用时的兜底
 *   3. 名称首字母        —— 最终兜底，保证任何情况下都有可见的结果
 */
function AppIcon({ app, title }: { app: StoreApp; title: string }) {
  const [stage, setStage] = useState<0 | 1 | 2>(0);

  if (stage === 2) {
    return <span className="fpkm-store-icon-fallback">{(title || '?').slice(0, 1)}</span>;
  }

  const src = stage === 0 ? iconURL(app.id || app.name) : app.icon || '';
  if (!src) {
    setStage(2);
    return <span className="fpkm-store-icon-fallback">{(title || '?').slice(0, 1)}</span>;
  }

  return (
    <img
      src={src}
      alt={title}
      loading="lazy"
      onError={() => setStage((s) => (s === 0 && app.icon ? 1 : 2))}
    />
  );
}

/** 商店应用卡片 */
/** 清理展示名：远程源偶尔会返回未解析的占位符 */
function cleanName(name: string, id: string): string {
  if (!name || name.includes('${') || name.startsWith('$')) return id;
  return name;
}

export default function StoreCard({ app, busy, onInstall, onOpenDetail, accessURL }: Props) {
  const unsupported = !app.platformSupported;
  const tags = app.tags ?? [];
  const title = cleanName(app.name, app.id);

  return (
    <div className="fpkm-store-card" onClick={() => onOpenDetail?.(app.id)}>
      <div className="fpkm-store-card-head">
        <div className="fpkm-store-icon">
          <AppIcon app={app} title={title} />
        </div>

        <div className="fpkm-store-info">
          <div className="fpkm-store-name">{title}</div>
          <Space spacing={6} wrap style={{ marginTop: 4 }}>
            {tags.map((t) => (
              <Tag key={t} size="small" className="fpkm-tag">
                {t}
              </Tag>
            ))}
            {app.installed ? (
              <Tag size="small" color="green">
                已安装
              </Tag>
            ) : null}
            {app.updateAvailable ? (
              <Tag size="small" color="orange">
                可更新 {app.version}
              </Tag>
            ) : null}
            {unsupported ? (
              <Tag size="small" color="grey">
                不支持 {app.platform}
              </Tag>
            ) : app.platformWarning ? (
              <Tooltip content={app.platformWarning}>
                <Tag size="small" color="orange">
                  标注 {app.platform}
                </Tag>
              </Tooltip>
            ) : null}
          </Space>
        </div>
      </div>

      <div className="fpkm-store-desc">
        {stripHtml(app.desc, 120) || <Text type="tertiary">暂无描述</Text>}
      </div>

      <div className="fpkm-store-foot" onClick={(e) => e.stopPropagation()}>
        <Space spacing={12} className="fpkm-store-meta">
          {app.author ? (
            <Tooltip content={`作者：${app.author}`}>
              <Text type="tertiary" size="small">
                {app.author}
              </Text>
            </Tooltip>
          ) : null}
          {app.size > 0 ? (
            <Text type="tertiary" size="small">
              {formatSize(app.size)}
            </Text>
          ) : null}
          {app.version ? (
            <Text type="tertiary" size="small">
              v{app.version}
            </Text>
          ) : null}
        </Space>

        {app.installed ? (
          // 已安装：主按钮一律是"打开"（不再显示禁用的"已安装"）；
          // 若同时有更新，额外给一个"更新"次要按钮，两者都能操作。
          <Space spacing={6}>
            {app.updateAvailable ? (
              <Button
                size="small"
                theme="borderless"
                type="primary"
                className="fpkm-btn-outline"
                icon={<IconRefresh />}
                disabled={busy || unsupported}
                onClick={() => onInstall(app)}
              >
                更新
              </Button>
            ) : null}
            <Button
              size="small"
              theme="solid"
              type="primary"
              icon={<IconPlay />}
              disabled={!accessURL}
              onClick={() => {
                if (accessURL) window.open(accessURL, '_blank', 'noopener');
              }}
            >
              打开
            </Button>
          </Space>
        ) : (
          <Button
            size="small"
            theme="light"
            type="primary"
            icon={<IconDownloadStroked />}
            disabled={busy || unsupported}
            loading={busy}
            onClick={() => onInstall(app)}
          >
            安装
          </Button>
        )}
      </div>
    </div>
  );
}
