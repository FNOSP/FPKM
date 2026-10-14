import { useState } from 'react';
import { Button, Card, Tag, Typography, Space, Popconfirm, Tooltip } from '@douyinfe/semi-ui';
import {
  IconPlay,
  IconStop,
  IconDelete,
  IconApps,
  IconRefresh,
  IconExternalOpen,
} from '@douyinfe/semi-icons';
import type { AppView, AppAction } from '../api/types';
import { buildAccessURL, kindLabel, stripHtml } from '../utils/format';
import { iconURL } from '../api/client';

interface Props {
  app: AppView;
  busy: boolean;
  /** 服务端指定的对外主机名（反向代理场景），为空则用浏览器当前主机名 */
  publicHost?: string;
  publicScheme?: string;
  onAction: (name: string, action: AppAction, path?: string) => void;
  /** 点击卡片打开应用详情 */
  onOpenDetail?: (name: string) => void;
}

const { Text } = Typography;

/** 已安装应用卡片 */
export default function AppCard({ app, busy, publicHost, publicScheme, onAction, onOpenDetail }: Props) {
  // 按当前页面的主机名拼地址，而不是写死 localhost
  const accessURL = buildAccessURL(app.access, publicHost, publicScheme);

  return (
    <div className="fpkm-app-card-wrap" onClick={() => onOpenDetail?.(app.name)}>
    <Card
      className="fpkm-app-card"
      shadows="hover"
      bodyStyle={{ display: 'flex', flexDirection: 'column', height: '100%' }}
    >
      <div className="fpkm-app-head">
        <div className="fpkm-app-icon">
          <AppIcon app={app} />
        </div>
        <div style={{ flex: 1, minWidth: 0 }}>
          <div className="fpkm-app-title">{app.displayName || app.name}</div>
          <div className="fpkm-app-sub">
            {app.name} · v{app.version}
          </div>
          {/* 标签区固定两行高度：无端口时第二行为空，保证各卡片对齐 */}
          <div className="fpkm-app-tags">
            <Space spacing={4} wrap>
              {app.running ? (
                <Tag color="green" size="small">
                  运行中{app.pid ? ` #${app.pid}` : ''}
                </Tag>
              ) : (
                <Tag color="grey" size="small">
                  已停止
                </Tag>
              )}
              <Tag size="small" color="blue">
                {kindLabel(app.kind)}
              </Tag>
              {app.isDocker ? (
                <Tag size="small" color="cyan">
                  Docker
                </Tag>
              ) : null}
              {app.updateAvailable ? (
                <Tooltip content={`发现新版本 ${app.latestVersion}，可从本地包升级`}>
                  <Tag size="small" color="orange">
                    可升级 {app.latestVersion}
                  </Tag>
                </Tooltip>
              ) : null}
              {/* 端口标签：无端口时占位留空，保持两行结构稳定 */}
              {app.port ? (
                <Tooltip content="服务端口">
                  <Tag size="small" color="blue" className="fpkm-tag-port">
                    端口 {app.port}
                  </Tag>
                </Tooltip>
              ) : null}
            </Space>
          </div>
        </div>
      </div>

      <div className="fpkm-app-desc">
        {stripHtml(app.desc) || <Text type="tertiary">暂无描述</Text>}
      </div>

      <div className="fpkm-card-footer" onClick={(e) => e.stopPropagation()}>
        <Space spacing={6} wrap>
          {app.running ? (
            <Button
              size="small"
              theme="borderless"
              type="tertiary"
              className="fpkm-btn-neutral"
              icon={<IconStop />}
              disabled={busy}
              onClick={() => onAction(app.name, 'stop')}
            >
              停止
            </Button>
          ) : (
            <Button
              size="small"
              theme="solid"
              type="primary"
              icon={<IconPlay />}
              disabled={busy}
              onClick={() => onAction(app.name, 'start')}
            >
              启动
            </Button>
          )}

          {accessURL ? (
            <Tooltip content={accessURL}>
              <Button
                size="small"
                theme="solid"
                type="primary"
                icon={<IconExternalOpen />}
                onClick={() => window.open(accessURL, '_blank', 'noopener')}
              >
                打开
              </Button>
            </Tooltip>
          ) : null}

          {app.updateAvailable && app.latestPath ? (
            <Button
              size="small"
              theme="borderless"
              type="primary"
              className="fpkm-btn-outline"
              icon={<IconRefresh />}
              disabled={busy}
              onClick={() => onAction(app.name, 'upgrade', app.latestPath)}
            >
              升级
            </Button>
          ) : null}

          <Popconfirm
            title="确认卸载？"
            content="将停止应用并删除其目录与全部数据，操作不可撤销。"
            okType="danger"
            okText="卸载"
            cancelText="取消"
            onConfirm={() => onAction(app.name, 'uninstall')}
          >
            <Button
              size="small"
              type="danger"
              className="fpkm-danger-btn"
              icon={<IconDelete />}
              disabled={busy}
            >
              卸载
            </Button>
          </Popconfirm>

        </Space>
      </div>
    </Card>
    </div>
  );
}

/** 应用图标：优先包内/缓存图标，失败回退远程源图标，再失败显示首字母 */
function AppIcon({ app }: { app: AppView }) {
  const [stage, setStage] = useState<0 | 1 | 2>(0);

  if (stage === 2) {
    return <span className="fpkm-app-icon-fallback">{(app.displayName || app.name || '?').slice(0, 1)}</span>;
  }

  const src = stage === 0 ? iconURL(app.name) : app.icon || '';
  if (!src) {
    return <span className="fpkm-app-icon-fallback">{(app.displayName || app.name || '?').slice(0, 1)}</span>;
  }

  return (
    <img
      src={src}
      alt={app.displayName || app.name}
      loading="lazy"
      onError={() => setStage((s) => (s === 0 && app.icon ? 1 : 2))}
    />
  );
}
