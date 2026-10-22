import { useEffect, useState } from 'react';
import {
  SideSheet,
  Tabs,
  TabPane,
  Button,
  Input,
  Space,
  Typography,
  Banner,
  Empty,
  Switch,
  Toast,
  Descriptions,
  Tag,
} from '@douyinfe/semi-ui';
import { IconDelete, IconPlus, IconLock } from '@douyinfe/semi-icons';
import type { AppSource, MetaInfo } from '../api/types';
import { listSources, saveSource, deleteSource, changePassword, setSession } from '../api/client';
import { useViewport, fitPopupWidth } from '../hooks/useViewport';

const { Paragraph, Text } = Typography;

export type SettingsTab = 'sources' | 'security' | 'about';

interface Props {
  visible: boolean;
  initialTab?: SettingsTab;
  meta: MetaInfo | null;
  onClose: () => void;
  /** 应用源变更后回调（用于刷新商店索引） */
  onSourcesChanged: (list: AppSource[]) => void;
}

/** 设置面板：应用源 / 安全 / 关于 */
export default function Settings({ visible, initialTab = 'sources', meta, onClose, onSourcesChanged }: Props) {
  const [tab, setTab] = useState<SettingsTab>(initialTab);
  const [sources, setSources] = useState<AppSource[]>([]);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState('');

  // 侧滑面板按视口收敛宽度：宽屏 640px，手机窄屏占满整屏，避免 640px
  // 固定宽度在小屏上把内容挤出屏幕（面板由 portal 挂到 body，CSS 断点管不到）。
  const vp = useViewport();
  const sheetWidth = vp.narrow ? vp.width : fitPopupWidth(640, vp);

  useEffect(() => {
    if (visible) setTab(initialTab);
  }, [visible, initialTab]);

  const refresh = async () => {
    setLoading(true);
    setLoadError('');
    try {
      const res = await listSources();
      setSources(res.sources ?? []);
    } catch (e) {
      // request() 带 15s 超时兜底，这里保证不会无限停留在"正在加载"。
      setSources([]);
      setLoadError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (visible) refresh();
  }, [visible]);

  return (
    <SideSheet
      title="设置"
      visible={visible}
      onCancel={onClose}
      width={sheetWidth}
      bodyStyle={vp.narrow ? { padding: '14px 14px 24px' } : undefined}
    >
      <Tabs type="line" activeKey={tab} onChange={(k) => setTab(k as SettingsTab)}>
        <TabPane tab="应用源" itemKey="sources">
          <SourcesSection
            sources={sources}
            loading={loading}
            loadError={loadError}
            onChanged={(list) => {
              setSources(list);
              setLoadError('');
              onSourcesChanged(list);
            }}
          />
        </TabPane>

        <TabPane tab="安全" itemKey="security">
          <SecuritySection onPasswordChanged={(s) => setSession(s)} />
        </TabPane>

        <TabPane tab="关于" itemKey="about">
          <AboutSection meta={meta} narrow={vp.narrow} />
        </TabPane>
      </Tabs>
    </SideSheet>
  );
}

/** ---------- 应用源 ---------- */

function SourcesSection({
  sources,
  loading,
  loadError,
  onChanged,
}: {
  sources: AppSource[];
  loading: boolean;
  loadError?: string;
  onChanged: (list: AppSource[]) => void;
}) {
  const [name, setName] = useState('');
  const [url, setUrl] = useState('');
  const [adding, setAdding] = useState(false);
  const [error, setError] = useState('');

  const add = async () => {
    if (!url.trim()) {
      setError('请填写源地址');
      return;
    }
    setAdding(true);
    setError('');
    try {
      const res = await saveSource({ name: name.trim(), url: url.trim(), enabled: true });
      onChanged(res.sources ?? []);
      setName('');
      setUrl('');
      Toast.success('已添加应用源');
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setAdding(false);
    }
  };

  const toggle = async (src: AppSource, enabled: boolean) => {
    try {
      const res = await saveSource({ id: src.id, name: src.name, url: src.url, enabled });
      onChanged(res.sources ?? []);
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : String(e));
    }
  };

  const remove = async (src: AppSource) => {
    try {
      const res = await deleteSource(src.id);
      onChanged(res.sources ?? []);
      Toast.success('已删除应用源');
    } catch (e) {
      Toast.error(e instanceof Error ? e.message : String(e));
    }
  };

  return (
    <Space vertical align="start" spacing={14} style={{ width: '100%', paddingTop: 8 }}>
      <Banner
        type={loadError ? 'danger' : 'info'}
        closeIcon={null}
        description={loadError || '应用源提供应用索引与安装包下载地址。可添加多个源；同一应用以先出现的源为准。'}
      />

      {sources.length === 0 ? (
        <Empty
          title={loading ? '正在加载…' : loadError ? '应用源加载失败' : '还没有配置应用源'}
          description={loadError ? '请确认服务正常后点击下方按钮重试，或重新打开设置面板。' : '在下方添加一个源地址'}
        />
      ) : (
        <div style={{ width: '100%' }}>
          {sources.map((s) => (
            <div key={s.id} className="fpkm-source-row">
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ fontWeight: 600, fontSize: 13.5 }}>
                  {s.name || s.id}
                  {s.builtin ? (
                    <Tag size="small" color="blue" style={{ marginLeft: 8, transform: 'translateY(-1px)' }}>
                      内置
                    </Tag>
                  ) : null}
                </div>
                <div className="fpkm-fs-path" style={{ marginTop: 4 }}>
                  {s.url}
                </div>
              </div>
              <Switch checked={s.enabled} onChange={(v) => toggle(s, v)} />
              {/* 内置源只能停用、不能删除，因此不渲染删除按钮 */}
              {s.builtin ? null : (
                <Button
                  icon={<IconDelete />}
                  type="danger"
                  theme="borderless"
                  aria-label="删除应用源"
                  onClick={() => remove(s)}
                />
              )}
            </div>
          ))}
        </div>
      )}

      <div style={{ width: '100%', borderTop: '1px solid var(--semi-color-border)', paddingTop: 16 }}>
        <Paragraph style={{ marginBottom: 10, fontWeight: 600 }}>添加应用源</Paragraph>
        <Space vertical align="start" spacing={10} style={{ width: '100%' }}>
          <Input
            placeholder="名称（可选，默认使用域名）"
            value={name}
            onChange={setName}
            style={{ width: '100%' }}
          />
          <Input
            placeholder="源地址，如 http://nas.example.com:5660"
            value={url}
            onChange={setUrl}
            style={{ width: '100%' }}
          />
          {error ? <Banner type="danger" description={error} closeIcon={null} /> : null}
          <Button theme="solid" type="primary" icon={<IconPlus />} loading={adding} onClick={add}>
            添加
          </Button>
        </Space>
      </div>
    </Space>
  );
}

/** ---------- 安全（修改密码） ---------- */

function SecuritySection({ onPasswordChanged }: { onPasswordChanged: (session: string) => void }) {
  const [oldPwd, setOldPwd] = useState('');
  const [newPwd, setNewPwd] = useState('');
  const [confirm, setConfirm] = useState('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [done, setDone] = useState(false);

  const submit = async () => {
    setError('');
    setDone(false);

    if (newPwd.length < 6) {
      setError('新密码至少 6 位');
      return;
    }
    if (newPwd !== confirm) {
      setError('两次输入的新密码不一致');
      return;
    }

    setSaving(true);
    try {
      const res = await changePassword(oldPwd, newPwd, confirm);
      // 改密会使旧会话失效，服务端已下发新会话
      onPasswordChanged(res.session);
      setOldPwd('');
      setNewPwd('');
      setConfirm('');
      setDone(true);
      Toast.success('密码已修改');
    } catch (e) {
      setError(e instanceof Error ? e.message : '修改失败');
    } finally {
      setSaving(false);
    }
  };

  return (
    <Space vertical align="start" spacing={12} style={{ width: '100%', paddingTop: 8 }}>
      <Banner
        type="info"
        closeIcon={null}
        description="修改管理密码需要先验证当前密码。修改成功后其他已登录的会话会失效。"
      />

      <Input
        mode="password"
        prefix={<IconLock />}
        placeholder="当前密码"
        value={oldPwd}
        onChange={setOldPwd}
        size="large"
      />
      <Input
        mode="password"
        prefix={<IconLock />}
        placeholder="新密码（至少 6 位）"
        value={newPwd}
        onChange={setNewPwd}
        size="large"
      />
      <Input
        mode="password"
        prefix={<IconLock />}
        placeholder="再次输入新密码"
        value={confirm}
        onChange={setConfirm}
        onEnterPress={submit}
        size="large"
      />

      {error ? <Banner type="danger" description={error} closeIcon={null} /> : null}
      {done ? <Banner type="success" description="密码已更新" closeIcon={null} /> : null}

      <Button theme="solid" type="primary" loading={saving} onClick={submit}>
        修改密码
      </Button>

      <Text type="tertiary" size="small">
        忘记密码时，可在服务器执行：<code>fpkm web --reset-password</code>
      </Text>
    </Space>
  );
}

/** ---------- 关于 ---------- */

function AboutSection({ meta, narrow = false }: { meta: MetaInfo | null; narrow?: boolean }) {
  return (
    <Space vertical align="start" spacing={12} style={{ width: '100%', paddingTop: 8 }}>
      <Descriptions
        // 宽屏一行铺开；窄屏改为每行一条，否则数据目录等长文本会被挤成竖排
        row={!narrow}
        column={narrow ? 1 : 3}
        size="small"
        data={[
          { key: '版本', value: meta?.version ?? '-' },
          { key: '主机名', value: meta?.hostname ?? '-' },
          { key: '管理端口', value: meta?.webPort ? String(meta.webPort) : '-' },
          { key: '网关端口', value: meta?.gatewayPort ? String(meta.gatewayPort) : '-' },
          { key: '数据目录', value: meta?.baseDir ?? '-' },
          { key: '应用目录', value: meta?.appsDir ?? '-' },
          { key: '安装后自动启动', value: meta?.autoStart ? '已开启' : '已关闭' },
          { key: '文件浏览', value: meta?.allowFS ? '已开启' : '已关闭' },
        ]}
      />
      <Banner
        type="warning"
        closeIcon={null}
        description="本界面可以安装并运行任意程序，请妥善保管管理密码，不要将端口暴露到公网。"
      />
    </Space>
  );
}
