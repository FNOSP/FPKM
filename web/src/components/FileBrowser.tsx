import { useCallback, useEffect, useState } from 'react';
import {
  SideSheet,
  Button,
  Table,
  Typography,
  Toast,
  Tag,
  Empty,
  Spin,
  Breadcrumb,
  Input,
  Space,
  Banner,
} from '@douyinfe/semi-ui';
import {
  IconFolder,
  IconFile,
  IconRefresh,
  IconChevronRight,
  IconHome,
  IconSearch,
} from '@douyinfe/semi-icons';
import type { FSEntry, FSListResult } from '../api/types';
import { fsList } from '../api/client';
import { formatSize, formatTime } from '../utils/format';

interface Props {
  visible: boolean;
  onCancel: () => void;
  /** 选中某个 .fpk 后回调（进入安装确认流程） */
  onPick: (path: string) => void;
}

const { Text } = Typography;

/** 服务器文件浏览器：逐级浏览目录，挑选 .fpk 安装包 */
export default function FileBrowser({ visible, onCancel, onPick }: Props) {
  const [path, setPath] = useState('');
  const [data, setData] = useState<FSListResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [manual, setManual] = useState('');

  const load = useCallback(async (target?: string) => {
    setLoading(true);
    setError('');
    try {
      const res = await fsList(target);
      setData(res);
      setPath(res.path);
      setManual(res.path);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    if (visible) load();
  }, [visible, load]);

  const enter = (entry: FSEntry) => {
    if (entry.isDir) {
      load(entry.path);
    } else if (entry.isFpk) {
      onPick(entry.path);
    }
  };

  const segments = path.split('/').filter(Boolean);

  const columns = [
    {
      title: '名称',
      dataIndex: 'name',
      render: (_: unknown, row: FSEntry) => (
        <div className="fpkm-fs-name">
          {row.isDir ? (
            <IconFolder size="large" className="fpkm-fs-icon-dir" />
          ) : (
            <IconFile size="large" className="fpkm-fs-icon-file" />
          )}
          <span>{row.name}</span>
          {row.isFpk ? (
            <Tag size="small" color="green">
              FPK
            </Tag>
          ) : null}
        </div>
      ),
    },
    {
      title: '大小',
      dataIndex: 'size',
      width: 110,
      render: (_: unknown, row: FSEntry) => (row.isDir ? '-' : formatSize(row.size)),
    },
    {
      title: '修改时间',
      dataIndex: 'mtime',
      width: 170,
      render: (_: unknown, row: FSEntry) => (row.mtime ? formatTime(row.mtime) : '-'),
    },
    {
      title: '操作',
      width: 100,
      render: (_: unknown, row: FSEntry) => (
        <Button
          size="small"
          theme={row.isFpk ? 'solid' : 'light'}
          type={row.isFpk ? 'primary' : 'tertiary'}
          onClick={(e) => {
            e.stopPropagation();
            enter(row);
          }}
        >
          {row.isDir ? '打开' : '选择'}
        </Button>
      ),
    },
  ];

  return (
    <SideSheet
      title="从服务器文件安装"
      visible={visible}
      onCancel={onCancel}
      width={860}
      bodyStyle={{ padding: '16px 20px' }}
    >
      <Space vertical align="start" style={{ width: '100%' }} spacing={12}>
        <Banner
          type="info"
          closeIcon={null}
          description="浏览服务器上的目录，选择要安装的 .fpk 文件。仅显示目录与 .fpk 文件。"
        />

        {/* 路径导航 */}
        <div style={{ width: '100%' }}>
          <Space style={{ marginBottom: 8 }}>
            <Button
              size="small"
              icon={<IconHome />}
              onClick={() => load()}
              theme="borderless"
            >
              主目录
            </Button>
            <Button
              size="small"
              icon={<IconRefresh />}
              onClick={() => load(path)}
              loading={loading}
              theme="borderless"
            >
              刷新
            </Button>
          </Space>
          <Breadcrumb
            separator={<IconChevronRight size="small" />}
            style={{ marginBottom: 8 }}
          >
            {segments.map((seg, i) => {
              const target = '/' + segments.slice(0, i + 1).join('/');
              return (
                <Breadcrumb.Item
                  key={target}
                  onClick={() => load(target)}
                  style={{ cursor: 'pointer' }}
                >
                  {seg}
                </Breadcrumb.Item>
              );
            })}
          </Breadcrumb>
          <div className="fpkm-fs-path">{path || '加载中…'}</div>
        </div>

        {/* 手动输入路径 */}
        <Space style={{ width: '100%' }}>
          <Input
            value={manual}
            onChange={setManual}
            prefix={<IconSearch />}
            placeholder="也可以直接输入 .fpk 的绝对路径"
            onEnterPress={() => {
              if (manual.trim().toLowerCase().endsWith('.fpk')) onPick(manual.trim());
              else load(manual.trim());
            }}
            style={{ width: 520 }}
          />
          <Button
            onClick={() => {
              if (manual.trim().toLowerCase().endsWith('.fpk')) onPick(manual.trim());
              else load(manual.trim());
            }}
          >
            前往
          </Button>
        </Space>

        {error ? <Banner type="danger" description={error} closeIcon={null} /> : null}

        {data?.parent ? (
          <Button
            size="small"
            theme="borderless"
            onClick={() => load(data.parent)}
            disabled={loading}
          >
            ← 返回上级目录
          </Button>
        ) : null}

        <div style={{ width: '100%', minHeight: 320 }}>
          {loading && !data ? (
            <div style={{ textAlign: 'center', padding: 60 }}>
              <Spin size="large" />
            </div>
          ) : !data || (data.entries?.length ?? 0) === 0 ? (
            <Empty
              title="此目录下没有可安装的内容"
              description="这里既没有子目录，也没有 .fpk 文件"
            />
          ) : (
            <Table
              columns={columns}
              dataSource={data.entries ?? []}
              rowKey="path"
              pagination={false}
              size="small"
              onRow={(row) => ({
                onClick: () => enter(row as FSEntry),
                className: 'fpkm-fs-row',
              })}
              loading={loading}
              scroll={{ y: 400 }}
            />
          )}
        </div>

        {data && data.fpkCount > 0 ? (
          <Text type="success" size="small">
            当前目录发现 {data.fpkCount} 个 .fpk 安装包，点击即可选择
          </Text>
        ) : null}
      </Space>
    </SideSheet>
  );
}
