import { useEffect, useState } from 'react';
import { Progress, Typography } from '@douyinfe/semi-ui';
import type { AppView } from '../api/types';
import type { SystemStats } from '../api/client';
import { IconActivity, IconServer, IconApps } from './icons';

const { Text } = Typography;

interface Props {
  stats: SystemStats | null;
  apps: AppView[];
}

/** 字节数格式化（1.5 GB / 420 GB） */
function fmtBytes(n: number, digits = 1): string {
  if (!n || n <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  const d = v >= 100 || i <= 1 ? 0 : digits;
  return `${v.toFixed(d)} ${units[i]}`;
}

/** 运行时长（秒 → 3天4小时） */
function fmtUptime(sec: number): string {
  if (!sec || sec <= 0) return '--';
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  if (d > 0) return `${d} 天 ${h} 小时`;
  if (h > 0) return `${h} 小时 ${m} 分`;
  return `${m} 分钟`;
}

/** 桌面右侧小组件：时钟 / 系统状态 / 存储 / 应用统计 */
export default function Widgets({ stats, apps }: Props) {
  const [now, setNow] = useState(new Date());

  useEffect(() => {
    const t = window.setInterval(() => setNow(new Date()), 1000);
    return () => window.clearInterval(t);
  }, []);

  const running = apps.filter((a) => a.running).length;
  const primaryDisk = stats?.disks?.[0];
  const memPercent = stats?.mem?.percent ?? 0;
  const diskPercent = primaryDisk?.percent ?? 0;

  const hh = String(now.getHours()).padStart(2, '0');
  const mm = String(now.getMinutes()).padStart(2, '0');
  const WEEK = ['星期日', '星期一', '星期二', '星期三', '星期四', '星期五', '星期六'];

  return (
    <div className="fpkm-widgets" onClick={(e) => e.stopPropagation()}>
      {/* 时钟 */}
      <div className="fpkm-widget fpkm-widget-clock">
        <div className="fpkm-clock-time">
          {hh}:{mm}
        </div>
        <div className="fpkm-clock-date">
          {now.getFullYear()}/{String(now.getMonth() + 1).padStart(2, '0')}/
          {String(now.getDate()).padStart(2, '0')} {WEEK[now.getDay()]}
        </div>
      </div>

      {/* 系统状态：内存 + 负载 */}
      <div className="fpkm-widget">
        <div className="fpkm-widget-title">
          <IconActivity />
          <span>系统状态</span>
        </div>

        <div className="fpkm-widget-row">
          <div className="fpkm-widget-gauge">
            <Progress
              percent={Math.round(memPercent)}
              type="circle"
              width={62}
              strokeWidth={7}
              format={(p) => `${p}%`}
            />
          </div>
          <div className="fpkm-widget-col">
            <Text size="small" type="tertiary">
              内存
            </Text>
            <Text strong>
              {stats ? `${fmtBytes(stats.mem.used)} / ${fmtBytes(stats.mem.total)}` : '--'}
            </Text>
          </div>
        </div>

        <div className="fpkm-widget-kv">
          <span>CPU 负载</span>
          <b>{stats ? stats.cpu.load1.toFixed(2) : '--'}</b>
        </div>
        <div className="fpkm-widget-kv">
          <span>核心</span>
          <b>{stats?.cpu.cores ?? '--'}</b>
        </div>
        <div className="fpkm-widget-kv">
          <span>运行时长</span>
          <b>{stats ? fmtUptime(stats.uptime) : '--'}</b>
        </div>
      </div>

      {/* 存储空间 */}
      <div className="fpkm-widget">
        <div className="fpkm-widget-title">
          <IconServer />
          <span>存储空间</span>
          <em className="fpkm-widget-pct">{primaryDisk ? `${Math.round(diskPercent)}%` : ''}</em>
        </div>
        <Progress percent={Math.round(diskPercent)} showInfo={false} stroke="var(--semi-color-primary)" />
        <div className="fpkm-widget-sub">
          {primaryDisk ? `${fmtBytes(primaryDisk.used)} / ${fmtBytes(primaryDisk.total)}` : '--'}
        </div>

        {(stats?.disks ?? []).slice(1, 3).map((d) => (
          <div key={d.mount} className="fpkm-widget-disk">
            <span className="fpkm-widget-disk-mount">{d.mount}</span>
            <Progress
              percent={Math.round(d.percent)}
              showInfo={false}
              style={{ flex: 1 }}
              stroke="var(--semi-color-primary-light-active)"
            />
            <span className="fpkm-widget-disk-pct">{Math.round(d.percent)}%</span>
          </div>
        ))}
      </div>

      {/* 应用统计 */}
      <div className="fpkm-widget">
        <div className="fpkm-widget-title">
          <IconApps />
          <span>应用</span>
        </div>
        <div className="fpkm-widget-stats">
          <div>
            <b>{apps.length}</b>
            <span>已安装</span>
          </div>
          <div>
            <b>{running}</b>
            <span>运行中</span>
          </div>
          <div>
            <b>{stats?.store?.total ?? '--'}</b>
            <span>商店</span>
          </div>
        </div>
      </div>
    </div>
  );
}
