import { useCallback, useRef, useState } from 'react';
import {
  SideSheet,
  Button,
  Typography,
  Banner,
  Space,
  Progress,
  Toast,
} from '@douyinfe/semi-ui';
import { IconUpload, IconFile, IconClose } from '@douyinfe/semi-icons';
import { uploadAndInstall, type UploadHandle } from '../api/client';
import { formatSize } from '../utils/format';

interface Props {
  visible: boolean;
  onCancel: () => void;
  /** 上传成功、拿到 taskId 后回调（由父组件打开进度弹窗） */
  onTask: (taskId: string, title: string) => void;
}

const { Text, Paragraph } = Typography;

/**
 * 上传安装面板。
 * 选择文件 → 上传（带真实进度）→ 交给父组件跟踪安装任务。
 *
 * 说明：安装包的 manifest 由服务端在 /api/install/upload 中解析并随响应返回，
 * 客户端不做本地解析，避免重复实现 tar/gzip 逻辑。
 */
export default function UploadInstaller({ visible, onCancel, onTask }: Props) {
  const [file, setFile] = useState<File | null>(null);
  const [error, setError] = useState('');
  const [uploading, setUploading] = useState(false);
  const [percent, setPercent] = useState(0);
  const [dragOver, setDragOver] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const handleRef = useRef<UploadHandle | null>(null);

  const reset = () => {
    setFile(null);
    setError('');
    setPercent(0);
    setUploading(false);
  };

  const close = () => {
    if (uploading) handleRef.current?.abort();
    reset();
    onCancel();
  };

  const pick = useCallback((f: File) => {
    setError('');
    setPercent(0);
    if (!f.name.toLowerCase().endsWith('.fpk')) {
      setError('仅支持 .fpk 文件');
      setFile(null);
      return;
    }
    setFile(f);
  }, []);

  const onDrop = (e: React.DragEvent) => {
    e.preventDefault();
    setDragOver(false);
    const f = e.dataTransfer.files?.[0];
    if (f) pick(f);
  };

  const startUpload = async () => {
    if (!file) return;
    setUploading(true);
    setPercent(0);

    const handle = uploadAndInstall(file, undefined, (p) => setPercent(p));
    handleRef.current = handle;

    try {
      const res = await handle.done;
      Toast.success('上传完成，开始安装');
      const title = `安装 ${file.name}`;
      reset();
      onTask(res.taskId, title);
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      if (!msg.includes('取消')) Toast.error(msg);
      setError(msg);
      setUploading(false);
    } finally {
      handleRef.current = null;
    }
  };

  return (
    <SideSheet
      title="上传安装包安装"
      visible={visible}
      onCancel={close}
      width={680}
      bodyStyle={{ padding: '16px 20px' }}
    >
      <Space vertical align="start" spacing={14} style={{ width: '100%' }}>
        <Banner
          type="info"
          closeIcon={null}
          description="从本机选择 .fpk 安装包上传到服务器并安装。上传完成后会自动归档，便于日后升级。"
        />

        {!file ? (
          <div
            className={`fpkm-dropzone ${dragOver ? 'fpkm-dropzone-active' : ''}`}
            style={{ width: '100%' }}
            onClick={() => inputRef.current?.click()}
            onDragOver={(e) => {
              e.preventDefault();
              setDragOver(true);
            }}
            onDragLeave={() => setDragOver(false)}
            onDrop={onDrop}
          >
            <div className="fpkm-dropzone-icon">
              <IconUpload size="extra-large" />
            </div>
            <Paragraph style={{ marginBottom: 4 }}>点击选择或拖拽 .fpk 文件到此处</Paragraph>
            <Text type="tertiary" size="small">
              单个文件最大 2 GiB
            </Text>
            <input
              ref={inputRef}
              type="file"
              accept=".fpk"
              hidden
              onChange={(e) => {
                const f = e.target.files?.[0];
                if (f) pick(f);
                e.target.value = '';
              }}
            />
          </div>
        ) : (
          <div
            style={{
              width: '100%',
              border: '1px solid var(--semi-color-border)',
              borderRadius: 8,
              padding: 16,
            }}
          >
            <Space align="start" style={{ width: '100%' }}>
              <IconFile size="extra-large" style={{ color: 'var(--semi-color-warning)' }} />
              <div style={{ flex: 1, minWidth: 0 }}>
                <div className="fpkm-app-title" style={{ fontSize: 14 }}>
                  {file.name}
                </div>
                <Text type="tertiary" size="small">
                  {formatSize(file.size)}
                </Text>
              </div>
              {!uploading ? (
                <Button
                  icon={<IconClose />}
                  theme="borderless"
                  type="tertiary"
                  onClick={reset}
                  aria-label="移除文件"
                />
              ) : null}
            </Space>

            {uploading ? (
              <div style={{ marginTop: 16 }}>
                <Progress percent={Number(percent.toFixed(0))} />
                <Text type="tertiary" size="small">
                  正在上传… {percent.toFixed(0)}%
                </Text>
              </div>
            ) : null}
          </div>
        )}

        {error ? <Banner type="danger" description={error} closeIcon={null} /> : null}

        <Space>
          <Button
            theme="solid"
            type="primary"
            icon={<IconUpload />}
            disabled={!file || uploading}
            loading={uploading}
            onClick={startUpload}
          >
            {uploading ? '上传中…' : '上传并安装'}
          </Button>
          <Button onClick={close} disabled={uploading}>
            取消
          </Button>
        </Space>
      </Space>
    </SideSheet>
  );
}
