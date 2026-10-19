import { useCallback, useEffect, useState } from 'react';
import { Modal, Radio, RadioGroup, Input, Toast, Spin, Banner } from '@douyinfe/semi-ui';
import type { AppView } from '../api/types';
import { getAccessSetting, setAccessSetting } from '../api/client';
import { iconURL } from '../api/client';
import { buildAccessURL, kindLabel } from '../utils/format';

type Mode = '' | 'port' | 'gateway' | 'cgi';

interface Props {
  /** 要设置的应用；null 表示弹窗关闭 */
  app: AppView | null;
  onClose: () => void;
  /** 保存成功后通知父级刷新列表（访问地址可能变化） */
  onSaved: () => void;
  /** 服务端指定的对外主机名/协议（反向代理场景），用于地址预览 */
  publicHost?: string;
  publicScheme?: string;
}

/**
 * 应用设置弹窗（参考飞牛「应用设置」）。
 *
 * 核心功能：让用户手动指定应用的接入方式，覆盖 ui/config 的默认判断。
 *
 * 为什么需要它：部分应用（如 Lucky 3.0.2）是飞牛定制构建，
 * 它硬编码 fnOS 原生 socket 路径、并按 fnOS 网关签名校验请求，
 * 经 fpkm 网关转发会被判为非法（"fnOS gateway identity invalid"）。
 * 这类应用只要切成「端口应用」直连其端口即可正常使用。
 */
export default function AppSettingsModal({
  app,
  onClose,
  onSaved,
  publicHost,
  publicScheme,
}: Props) {
  const [mode, setMode] = useState<Mode>('');
  const [port, setPort] = useState('');
  const [path, setPath] = useState('/');
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');

  // 打开时读取当前设置
  useEffect(() => {
    if (!app) return;
    let cancelled = false;
    setLoading(true);
    setError('');
    getAccessSetting(app.name)
      .then((s) => {
        if (cancelled) return;
        setMode(s.mode || '');
        setPort(s.port ? String(s.port) : app.port ? String(app.port) : '');
        setPath(s.path || '/');
      })
      .catch((e) => {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [app]);

  const save = useCallback(async () => {
    if (!app) return;
    setError('');
    if (mode === 'port') {
      const n = Number(port);
      if (!Number.isInteger(n) || n <= 0 || n > 65535) {
        setError('端口应用需要填写 1-65535 的端口');
        return;
      }
    }
    setSaving(true);
    try {
      await setAccessSetting({ app: app.name, mode, port, path });
      Toast.success('已保存');
      onSaved();
      onClose();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }, [app, mode, port, path, onClose, onSaved]);

  if (!app) return null;

  // 访问地址预览：与前端 buildAccessURL 的规则保持一致
  /**
   * 打开地址预览。
   *
   * 「跟随应用配置」时不能用页面端口/路径去拼 —— 那只是猜测，
   * 可能与应用真实地址不符（例如网关应用应是 :7666/app/xxx，
   * 端口应用应是 :16601/）。这种模式下直接显示服务端返回的真实地址。
   */
  const preview = () => {
    if (mode === '') {
      if (!app) return '—';
      // AppView 自带 access，直接复用统一拼接规则，避免用页面端口瞎猜
      return buildAccessURL(app.access, publicHost, publicScheme) || '（由应用配置决定）';
    }
    const host = publicHost?.trim() || window.location.hostname;
    const scheme =
      publicScheme?.trim() || window.location.protocol.replace(':', '') || 'http';
    const p = path.trim() || '/';
    const joined = p.startsWith('/') ? p : '/' + p;
    if (mode === 'port') {
      const n = Number(port);
      if (!Number.isInteger(n) || n <= 0) return '（请填写端口）';
      return `${scheme}://${host}:${n}${joined}`;
    }
    // 网关类：走当前页面端口
    const current = window.location.port ? `:${window.location.port}` : '';
    return `${scheme}://${host}${current}${joined}`;
  };

  return (
    <Modal
      title="应用设置"
      visible={!!app}
      onCancel={onClose}
      onOk={save}
      okText="保存"
      cancelText="取消"
      confirmLoading={saving}
      width={640}
      bodyStyle={{ padding: '8px 24px 8px' }}
    >
      {/* 应用头部：图标 + 名称 + 当前类型 */}
      <div className="fpkm-set-head">
        <img className="fpkm-set-icon" src={iconURL(app.name)} alt={app.name} />
        <div className="fpkm-set-titlewrap">
          <div className="fpkm-set-name">{app.displayName || app.name}</div>
          <div className="fpkm-set-sub">
            {app.name} · v{app.version}
          </div>
        </div>
        <div className="fpkm-set-kind">{kindLabel(app.kind)}</div>
      </div>

      {loading ? (
        <div style={{ padding: 40, textAlign: 'center' }}>
          <Spin />
        </div>
      ) : (
        <div className="fpkm-set-body">
          <div className="fpkm-set-label">访问方式</div>
          <RadioGroup
            value={mode}
            onChange={(e) => setMode(e.target.value as Mode)}
            direction="vertical"
          >
            <Radio value="">
              跟随应用配置
              <span className="fpkm-set-hint">（由应用自带的 ui/config 决定，推荐）</span>
            </Radio>
            <Radio value="port">
              端口应用
              <span className="fpkm-set-hint">（浏览器直接访问应用端口，不经网关）</span>
            </Radio>
            <Radio value="gateway">
              统一网关
              <span className="fpkm-set-hint">（经 /app/{app.name} 转发）</span>
            </Radio>
            <Radio value="cgi">
              CGI 应用
              <span className="fpkm-set-hint">（经网关转发到 CGI 程序）</span>
            </Radio>
          </RadioGroup>

          {mode === 'port' ? (
            <>
              <div className="fpkm-set-label">访问端口</div>
              <Input
                placeholder="如 16601"
                value={port}
                onChange={setPort}
                style={{ width: 200 }}
              />
              <div className="fpkm-set-tip">
                填写应用自己的服务端口。例如填 16601，打开地址就是
                <code>{preview()}</code>
              </div>
            </>
          ) : null}

          {mode !== '' ? (
            <>
              <div className="fpkm-set-label">访问路径</div>
              <Input
                placeholder="/"
                value={path}
                onChange={setPath}
                style={{ width: 240 }}
              />
              <div className="fpkm-set-tip">
                {mode === 'port'
                  ? '端口应用通常填 / 即可（如需子路径可填 /admin）。'
                  : '网关应用的子路径，例如 /gateway。'}
              </div>
            </>
          ) : null}

          <div className="fpkm-set-label">打开地址</div>
          <div className="fpkm-set-preview">{preview()}</div>

          {error ? (
            <Banner type="danger" description={error} closeIcon={null} style={{ marginTop: 12 }} />
          ) : null}
        </div>
      )}
    </Modal>
  );
}
