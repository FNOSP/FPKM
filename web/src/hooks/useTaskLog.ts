import { useEffect, useRef, useState } from 'react';
import type { LogLine, TaskSnapshot } from '../api/types';
import { streamTask } from '../api/client';

export interface TaskLogState {
  lines: LogLine[];
  snapshot: TaskSnapshot | null;
  finished: boolean;
  failed: boolean;
}

/**
 * 订阅任务日志。
 * 在 taskId 变化时自动重新连接，并保留历史行。
 */
export function useTaskLog(taskId: string | null): TaskLogState {
  const [lines, setLines] = useState<LogLine[]>([]);
  const [snapshot, setSnapshot] = useState<TaskSnapshot | null>(null);
  const [finished, setFinished] = useState(false);
  const [failed, setFailed] = useState(false);
  const closeRef = useRef<(() => void) | null>(null);

  useEffect(() => {
    // 重置状态
    setLines([]);
    setSnapshot(null);
    setFinished(false);
    setFailed(false);

    if (!taskId) return;

    closeRef.current = streamTask(taskId, {
      onLog: (line) => {
        setLines((prev) => (prev.some((l) => l.seq === line.seq) ? prev : [...prev, line]));
      },
      onDone: (snap) => {
        setSnapshot(snap);
        setFinished(true);
        setFailed(snap.state === 'failed');
      },
      onError: () => {
        setFinished(true);
      },
    });

    return () => {
      closeRef.current?.();
      closeRef.current = null;
    };
  }, [taskId]);

  return { lines, snapshot, finished, failed };
}
