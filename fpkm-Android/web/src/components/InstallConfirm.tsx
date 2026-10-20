import { useEffect, useState } from 'react';
import {
  Modal,
  Button,
  Typography,
  Banner,
  Descriptions,
  Tag,
  Spin,
  Space,
} from '@douyinfe/semi-ui';
import type { InspectResult } from '../api/types';
import { fsInspect } from '../api/client';
import { formatSize, stripHtml } from '../utils/format';

interface Props {
  visible: boolean;
  /** 待安装包的路径 */
  packagePath: string | null;
  onCancel: () => void;
  /** 用户确认包信息后回调（此时还未安装，用于接着弹向导） */
  onConfirm: (path: string, title: string) => void;
}

const { Paragraph } = Typography;

/**
 * 本地包安装确认弹窗：展示包信息与安装/升级判定，确认后创建任务。
 */
export default function InstallConfirm({ visible, packagePath, onCancel, onConfirm }: Props) {
  const [info, setInfo] = useState<InspectResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!visible || !packagePath) {
      setInfo(null);
      setError('');
      return;
    }
    setLoading(true);
    setError('');
    fsInspect(packagePath)
      .then(setInfo)
      .catch((e) => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setLoading(false));
  }, [visible, packagePath]);

  // 注意：这里只负责"确认包信息"，不做实际安装。
  // 真正的安装由父组件在"安装向导"填写完成后再发起，
  // 以保证顺序是：确认 → 向导 → 安装日志。
  const confirm = () => {
    if (!packagePath) return;
    const title = `安装 ${info?.package?.displayName || info?.package?.appname || ''}`;
    onConfirm(packagePath, title);
  };

  const pkg = info?.package;

  // 判定标签
  const statusTag = () => {
    if (!info) return null;
    if (!info.installed) {
      return (
        <Tag color="green" size="small">
          新安装
        </Tag>
      );
    }
    if (info.isUpgrade) {
      return (
        <Tag color="orange" size="small">
          升级 {info.installedVersion} → {pkg?.version}
        </Tag>
      );
    }
    if (info.isSameVersion) {
      return (
        <Tag color="blue" size="small">
          版本相同（重装）
        </Tag>
      );
    }
    return (
      <Tag color="red" size="small">
        降级 {info.installedVersion} → {pkg?.version}
      </Tag>
    );
  };

  return (
    <Modal
      title="确认安装"
      visible={visible}
      onCancel={onCancel}
      footer={
        <Space>
          <Button onClick={onCancel}>取消</Button>
          <Button
            theme="solid"
            type="primary"
            disabled={loading || !!error || !info}
            onClick={confirm}
          >
            开始安装
          </Button>
        </Space>
      }
      width={640}
      maskClosable
    >
      {loading ? (
        <div style={{ textAlign: 'center', padding: 40 }}>
          <Spin size="large" />
        </div>
      ) : error ? (
        <Banner type="danger" description={error} closeIcon={null} />
      ) : pkg ? (
        <>
          <Space vertical align="start" style={{ width: '100%' }} spacing={12}>
            <Space>
              <span className="fpkm-app-title" style={{ fontSize: 16 }}>
                {pkg.displayName || pkg.appname}
              </span>
              {statusTag()}
            </Space>

            {pkg.desc ? (
              <Paragraph type="tertiary" style={{ marginBottom: 0 }}>
                {stripHtml(pkg.desc, 300)}
              </Paragraph>
            ) : null}

            <Descriptions
              row
              size="small"
              data={[
                { key: '应用名', value: pkg.appname },
                { key: '版本', value: pkg.version || '-' },
                { key: '平台', value: pkg.platform || '-' },
                { key: '维护者', value: pkg.maintainer || '-' },
                { key: '包大小', value: formatSize(pkg.size) },
                { key: '服务端口', value: pkg.servicePort || '-' },
              ]}
            />

            <div style={{ width: '100%' }}>
              <Paragraph type="tertiary" size="small" style={{ marginBottom: 4 }}>
                文件路径
              </Paragraph>
              <div className="fpkm-fs-path">{pkg.path}</div>
            </div>

            {info?.isDowngrade ? (
              <Banner
                type="warning"
                closeIcon={null}
                description={`当前已安装 ${info.installedVersion}，新包版本更低，将被判定为降级操作。`}
              />
            ) : null}
          </Space>
        </>
      ) : null}
    </Modal>
  );
}
