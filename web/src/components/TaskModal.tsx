import { useEffect, useRef } from 'react';
import { Modal, Typography, Banner, Button, Spin, Progress } from '@douyinfe/semi-ui';
import type { TaskSnapshot } from '../api/types';
import { useTaskLog } from '../hooks/useTaskLog';
import { useViewport, fitPopupWidth, fitLogHeight } from '../hooks/useViewport';
import LogViewer from './LogViewer';

interface Props {
  visible: boolean;
  taskId: string | null;
  title?: string;
  /** 上传进度（0-100），仅在"上传+安装"流程中使用 */
  uploadPercent?: number | null;
  onClose: () => void;
  /** 任务成功结束后回调（用于刷新列表） */
  onSuccess?: () => void;
  /** 任务结束（无论成败）后回调，带上最终快照。
   *  用于"下载完成后接着弹向导/自动安装"这类串行流程。 */
  onFinished?: (snap: TaskSnapshot) => void;
}

const { Paragraph, Text } = Typography;

/**
 * 任务进度弹窗：展示上传进度与安装日志，结束后给出结论。
 */
export default function TaskModal({
  visible,
  taskId,
  title = '安装进度',
  uploadPercent,
  onClose,
  onSuccess,
  onFinished,
}: Props) {
  const { lines, snapshot, finished, failed } = useTaskLog(visible ? taskId : null);

  // 弹窗由 portal 挂到 body，固定 760px 在手机上会溢出屏幕：
  // 按视口收敛宽度，并按视口高度收紧日志面板，保证一屏内可见。
  const vp = useViewport();
  const modalWidth = fitPopupWidth(760, vp);
  const logHeight = fitLogHeight(340, vp);

  const uploading = uploadPercent !== null && uploadPercent !== undefined && uploadPercent < 100;
  const running = !finished && (Boolean(taskId) || uploading);

  // 任务成功结束后通知父组件刷新列表。
  // 必须放在 effect 中：在渲染期间调用父组件的 setState 会触发 React 警告甚至死循环。
  const notifiedRef = useRef<string | null>(null);
  useEffect(() => {
    if (!visible || !finished || !snapshot?.id) return;
    if (notifiedRef.current === snapshot.id) return;
    notifiedRef.current = snapshot.id;

    if (snapshot.state === 'success') onSuccess?.();
    onFinished?.(snapshot);
  }, [visible, finished, snapshot, onSuccess, onFinished]);

  /** 下载进度（仅 kind=download 时） */
  const progress = snapshot?.progress;
  const downloading = snapshot?.kind === 'download' && !finished;

  const statusBanner = () => {
    if (downloading) {
      // 部分应用源不返回 Content-Length，此时 total 未知（percent = -1），
      // 只展示已下载字节数并显示不确定进度条，避免出现"0%"的误导。
      const known = progress && progress.total > 0 && progress.percent >= 0;
      return (
        <div style={{ marginBottom: 12 }}>
          <Banner
            type="info"
            icon={<Spin size="small" />}
            description={
              known
                ? `正在下载安装包… ${progress.percent}%（${fmtBytes(progress.done)} / ${fmtBytes(progress.total)}）`
                : `正在下载安装包… 已下载 ${fmtBytes(progress?.done ?? 0)}`
            }
            closeIcon={null}
          />
          {known ? (
            <Progress percent={progress.percent} style={{ marginTop: 10 }} />
          ) : (
            <Progress percent={0} formatText={() => ''} style={{ marginTop: 10 }} />
          )}
        </div>
      );
    }
    if (uploading) {
      return (
        <Banner
          type="info"
          icon={<Spin size="small" />}
          description={`正在上传安装包… ${uploadPercent?.toFixed(0)}%`}
          closeIcon={null}
          style={{ marginBottom: 12 }}
        />
      );
    }
    if (!finished) {
      return (
        <Banner
          type="info"
          description="正在执行，请勿关闭页面…"
          closeIcon={null}
          style={{ marginBottom: 12 }}
        />
      );
    }
    if (failed || snapshot?.state === 'failed') {
      return (
        <Banner
          type="danger"
          description={snapshot?.error || '操作失败，详见下方日志'}
          closeIcon={null}
          style={{ marginBottom: 12 }}
        />
      );
    }
    return (
      <Banner type="success" description="操作已完成" closeIcon={null} style={{ marginBottom: 12 }} />
    );
  };

  return (
    <Modal
      title={title}
      visible={visible}
      onCancel={running ? undefined : onClose}
      footer={
        <Button
          theme="solid"
          type={failed ? 'danger' : 'primary'}
          disabled={running}
          onClick={onClose}
        >
          {running ? '执行中…' : '关闭'}
        </Button>
      }
      width={modalWidth}
      bodyStyle={vp.narrow ? { padding: '14px 14px 18px' } : undefined}
      maskClosable={!running}
      closable={!running}
    >
      {statusBanner()}
      <div style={{ marginBottom: 10 }}>
        <Text type="tertiary" size="small">
          实时日志
        </Text>
      </div>
      <LogViewer lines={lines} height={logHeight} />
      {snapshot?.ended && snapshot?.started ? (
        <Paragraph type="tertiary" size="small" style={{ marginTop: 10, marginBottom: 0 }}>
          耗时 {((snapshot.ended - snapshot.started) / 1000).toFixed(1)} 秒
          {snapshot.title ? ` · ${snapshot.title}` : ''}
        </Paragraph>
      ) : null}
    </Modal>
  );
}

export type { TaskSnapshot };

/** 字节数格式化（进度显示用） */
function fmtBytes(n: number): string {
  if (!n || n < 1024) return `${n || 0} B`;
  const units = ['KiB', 'MiB', 'GiB'];
  let v = n;
  let i = -1;
  do {
    v /= 1024;
    i++;
  } while (v >= 1024 && i < units.length - 1);
  return `${v.toFixed(1)} ${units[i]}`;
}
