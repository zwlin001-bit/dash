import React, { useState, useEffect, useMemo } from 'react';
import { useParams, Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { getNode } from '../../api/nodes';
import { getMetrics, subscribeMetricsStream } from '../../api/metrics';
import { MetricStreamEvent, NodeLatest, NodeStateStreamEvent, TimeSeriesSpan } from '../../api/types';
import { TimeSeriesChart } from '../../components/TimeSeriesChart';
import styles from './NodeDetail.module.css';

function formatBytes(bytes?: number): string {
  if (bytes === undefined || bytes === null || isNaN(bytes) || bytes <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  let val = bytes;
  while (val >= 1024 && i < units.length - 1) {
    val /= 1024;
    i++;
  }
  return `${val.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

function formatBps(bps?: number): string {
  if (!bps || bps <= 0) return '0 B/s';
  if (bps >= 1024 * 1024 * 1024) return `${(bps / (1024 * 1024 * 1024)).toFixed(1)} GB/s`;
  if (bps >= 1024 * 1024) return `${(bps / (1024 * 1024)).toFixed(1)} MB/s`;
  if (bps >= 1024) return `${(bps / 1024).toFixed(0)} KB/s`;
  return `${bps} B/s`;
}

function formatTimeAgo(ms?: number): string {
  if (!ms || ms <= 0) return '从未上线';
  const diff = Date.now() - ms;
  if (diff < 10000) return '刚刚';
  const sec = Math.floor(diff / 1000);
  if (sec < 60) return `${sec} 秒前`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min} 分钟前`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr} 小时前`;
  const day = Math.floor(hr / 24);
  return `${day} 天前`;
}

function formatDateTime(ms?: number): string {
  if (!ms || ms <= 0) return '--';
  const d = new Date(ms);
  const year = d.getFullYear();
  const month = String(d.getMonth() + 1).padStart(2, '0');
  const day = String(d.getDate()).padStart(2, '0');
  const hour = String(d.getHours()).padStart(2, '0');
  const min = String(d.getMinutes()).padStart(2, '0');
  const sec = String(d.getSeconds()).padStart(2, '0');
  return `${year}-${month}-${day} ${hour}:${min}:${sec}`;
}

function formatUptime(uptimeS?: number): string {
  if (!uptimeS || uptimeS <= 0) return '--';
  const d = Math.floor(uptimeS / 86400);
  const h = Math.floor((uptimeS % 86400) / 3600);
  const m = Math.floor((uptimeS % 3600) / 60);
  if (d > 0) return `${d} 天 ${h} 小时`;
  if (h > 0) return `${h} 小时 ${m} 分钟`;
  return `${m} 分钟`;
}

export const NodeDetail: React.FC = () => {
  const { id = '01JBX79N1A9W5K3E8S6T2Y4P0A' } = useParams<{ id: string }>();
  const [span, setSpan] = useState<TimeSeriesSpan>('6h');
  const [activeMetricTab, setActiveMetricTab] = useState<'all' | 'cpu' | 'mem' | 'net' | 'sys'>('all');

  // 节点静态数据与最新值查询
  const { data: nodeData } = useQuery({
    queryKey: ['node', id],
    queryFn: () => getNode(id),
    staleTime: 30000,
  });

  // 实时 SSE 推送数据缓存
  const [liveLatest, setLiveLatest] = useState<Partial<NodeLatest> | null>(null);
  const [liveState, setLiveState] = useState<{ conn_state: 'online' | 'offline' | 'never'; last_seen_at_ms: number } | null>(null);

  useEffect(() => {
    const unsubscribe = subscribeMetricsStream({
      onMetrics: (ev: MetricStreamEvent) => {
        if (ev.node_id === id) {
          setLiveLatest((prev) => ({
            ...(prev || {}),
            ts_ms: ev.ts_ms,
            cpu_pct: ev.cpu_pct,
            mem_used: ev.mem_used,
            net_up_bps: ev.net_up_bps,
            net_down_bps: ev.net_down_bps,
            disk_used: ev.disk_used,
            load1: ev.load1,
          }));
        }
      },
      onNodeState: (ev: NodeStateStreamEvent) => {
        if (ev.node_id === id) {
          setLiveState({
            conn_state: ev.conn_state,
            last_seen_at_ms: ev.last_seen_at_ms,
          });
        }
      },
    });

    return () => {
      unsubscribe();
    };
  }, [id]);

  const node = useMemo(() => {
    if (!nodeData) return null;
    return {
      ...nodeData,
      conn_state: liveState?.conn_state ?? nodeData.conn_state,
      last_seen_at_ms: liveState?.last_seen_at_ms ?? nodeData.last_seen_at_ms,
      latest: {
        ...(nodeData.latest || {
          ts_ms: Date.now(),
          cpu_pct: 0,
          mem_used: 0,
          net_up_bps: 0,
          net_down_bps: 0,
        }),
        ...(liveLatest || {}),
      },
    };
  }, [nodeData, liveState, liveLatest]);

  const getSpanDuration = (s: TimeSeriesSpan) => {
    switch (s) {
      case '6h': return 6 * 3600 * 1000;
      case '3d': return 3 * 86400 * 1000;
      case '60d': return 60 * 86400 * 1000;
      case '1y': return 365 * 86400 * 1000;
    }
  };

  // 跨度切换处理请求竞态与缓存复用（约束：不做重复请求，处理请求竞态）
  const now = Date.now();
  const { data: metricsData, isLoading: metricsLoading } = useQuery({
    queryKey: ['metrics', id, span],
    queryFn: ({ signal }) =>
      getMetrics(
        id,
        {
          from_ms: now - getSpanDuration(span),
          to_ms: now,
          span,
        },
        signal
      ),
    staleTime: 30000,
    gcTime: 300000,
  });

  // 根据选中的 Tab 决定要绘制的指标
  const getSelectedMetrics = () => {
    switch (activeMetricTab) {
      case 'cpu':
        return ['cpu_pct', 'load1', 'load5', 'load15'];
      case 'mem':
        return ['mem_used', 'swap_used'];
      case 'net':
        return ['net_up_bps', 'net_down_bps'];
      case 'sys':
        return ['disk_used', 'proc_count', 'tcp_count', 'udp_count'];
      default:
        return ['cpu_pct', 'net_up_bps', 'net_down_bps'];
    }
  };

  const isOnline = node?.conn_state === 'online';
  const isOffline = node?.conn_state === 'offline';

  // 计费与到期时间计算（docs/13-ui-spec.md §6）
  const daysLeft = node?.billing?.expires_at_ms
    ? Math.ceil((node.billing.expires_at_ms - Date.now()) / 86400000)
    : null;

  const monthTrafficUsed =
    (node?.latest?.traffic_month_up || 0) + (node?.latest?.traffic_month_down || 0);
  const trafficLimit = node?.billing?.traffic_limit;
  const trafficRatio = trafficLimit && trafficLimit > 0 ? monthTrafficUsed / trafficLimit : 0;
  const trafficPct = (trafficRatio * 100).toFixed(1);

  // 进度条颜色：≥80% 橙 --warn，≥100% 红 --err
  const trafficColorVar =
    trafficRatio >= 1.0 ? 'var(--err)' : trafficRatio >= 0.8 ? 'var(--warn)' : 'var(--ok)';

  return (
    <div>
      <div className="page-head">
        <Link to="/nodes" style={{ color: 'var(--text-dim)', fontSize: 13 }}>
          ← 节点总览
        </Link>
        <span style={{ color: 'var(--text-mute)' }}>/</span>
        <h2>{node?.name || id}</h2>
      </div>

      {/* 节点概览信息卡片 */}
      <div className={styles.headerCard}>
        <div className={styles.titleRow}>
          <div className={styles.nameGroup}>
            <span className={`health ${isOnline ? 'ok' : isOffline ? 'err' : ''}`}>
              <span className="hdot" />
            </span>
            <span className={styles.nodeName}>{node?.name || id}</span>
            <span className={styles.statusText} style={{ color: isOnline ? 'var(--ok)' : isOffline ? 'var(--err)' : 'var(--text-mute)' }}>
              {isOnline ? '在线' : isOffline ? '离线' : '未上线'}
            </span>
            {!isOnline && node?.last_seen_at_ms && (
              <span className={styles.timeAgo}>
                ({formatTimeAgo(node.last_seen_at_ms)})
              </span>
            )}
            {node?.group?.name && <span className="badge badge-info">{node.group.name}</span>}
            {node?.clock_skew_ms && Math.abs(node.clock_skew_ms) > 30000 && (
              <span
                className="badge badge-neutral"
                title={`时钟异常，偏差 ${(node.clock_skew_ms / 1000).toFixed(1)} 秒`}
              >
                时钟异常
              </span>
            )}
          </div>

          <div style={{ display: 'flex', gap: 6 }}>
            {node?.tags?.map((t) => (
              <span
                key={t.id}
                className="badge"
                style={{
                  borderColor: t.color ? `${t.color}55` : undefined,
                  color: t.color || 'var(--text-dim)',
                }}
              >
                {t.name}
              </span>
            ))}
          </div>
        </div>

        <div className={styles.metaGrid}>
          <div className={styles.metaItem}>
            <span className={styles.metaLabel}>公网 IPv4</span>
            <span className={styles.metaVal}>{node?.facts?.ipv4 || '--'}</span>
          </div>
          <div className={styles.metaItem}>
            <span className={styles.metaLabel}>操作系统 / 架构</span>
            <span className={styles.metaVal}>
              {node?.facts?.os_name || '--'} {node?.facts?.os_version || ''} ({node?.facts?.arch || '--'})
            </span>
          </div>
          <div className={styles.metaItem}>
            <span className={styles.metaLabel}>实时网络速率</span>
            <span className={styles.metaVal}>
              ↑ {formatBps(node?.latest?.net_up_bps)} / ↓ {formatBps(node?.latest?.net_down_bps)}
            </span>
          </div>
          <div className={styles.metaItem}>
            <span className={styles.metaLabel}>虚拟化类型</span>
            <span className={styles.metaVal}>{node?.facts?.virt || '--'}</span>
          </div>
          <div className={styles.metaItem}>
            <span className={styles.metaLabel}>运行时长</span>
            <span className={styles.metaVal}>
              {node?.latest?.uptime_s ? formatUptime(node.latest.uptime_s) : '--'}
            </span>
          </div>
        </div>
      </div>

      {/* 时序监控图表 (复用 TimeSeriesChart 组件，支持 6h / 3d / 60d / 1y 四个跨度) */}
      <div className={styles.chartSection}>
        <div className={styles.metricTabs}>
          <button
            type="button"
            className={`${styles.metricTab} ${activeMetricTab === 'all' ? styles.active : ''}`}
            onClick={() => setActiveMetricTab('all')}
          >
            综合指标 (CPU + 带宽叠加)
          </button>
          <button
            type="button"
            className={`${styles.metricTab} ${activeMetricTab === 'cpu' ? styles.active : ''}`}
            onClick={() => setActiveMetricTab('cpu')}
          >
            CPU 与系统负载
          </button>
          <button
            type="button"
            className={`${styles.metricTab} ${activeMetricTab === 'mem' ? styles.active : ''}`}
            onClick={() => setActiveMetricTab('mem')}
          >
            内存与 Swap
          </button>
          <button
            type="button"
            className={`${styles.metricTab} ${activeMetricTab === 'net' ? styles.active : ''}`}
            onClick={() => setActiveMetricTab('net')}
          >
            网络上传 / 下载速率
          </button>
          <button
            type="button"
            className={`${styles.metricTab} ${activeMetricTab === 'sys' ? styles.active : ''}`}
            onClick={() => setActiveMetricTab('sys')}
          >
            磁盘与连接数
          </button>
        </div>

        <TimeSeriesChart
          title="历史时序监控"
          data={metricsData}
          span={span}
          onSpanChange={(newSpan) => setSpan(newSpan)}
          metrics={getSelectedMetrics()}
          loading={metricsLoading}
          height={340}
        />
      </div>

      {/* 计费与流量规格明细 (node_billing) */}
      <div className={`card ${styles.billingSection}`}>
        <div className="card-header">
          <span>计费与流量规格 (node_billing)</span>
        </div>
        <div className="card-body">
          <div className={styles.factsGrid}>
            <div className={styles.factBox}>
              <span className="text-dim" style={{ fontSize: 11 }}>价格与周期</span>
              <span className="cell-mono">
                {node?.billing?.price !== undefined
                  ? `${node.billing.price.toFixed(2)} ${node.billing.currency || 'CNY'}`
                  : '--'}
                {node?.billing?.cycle_days ? ` / 每 ${node.billing.cycle_days} 天` : ' / 月付'}
              </span>
            </div>

            <div className={styles.factBox}>
              <span className="text-dim" style={{ fontSize: 11 }}>到期时间</span>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <span className="cell-mono">
                  {node?.billing?.expires_at_ms ? formatDateTime(node.billing.expires_at_ms).slice(0, 10) : '--'}
                </span>
                {daysLeft !== null && (
                  <span
                    className={`pill ${daysLeft <= 0 ? 'red' : daysLeft <= 7 ? 'warn' : 'grey'}`}
                    style={{
                      color:
                        daysLeft <= 0
                          ? 'var(--err)'
                          : daysLeft <= 7
                          ? 'var(--warn)'
                          : 'var(--text-dim)',
                    }}
                  >
                    {daysLeft <= 0 ? '已过期' : `剩余 ${daysLeft} 天`}
                  </span>
                )}
              </div>
            </div>

            <div className={styles.factBox}>
              <span className="text-dim" style={{ fontSize: 11 }}>当月已用流量</span>
              <span className="cell-mono" style={{ color: trafficRatio >= 0.8 ? trafficColorVar : undefined }}>
                {formatBytes(monthTrafficUsed)}
                {trafficLimit && trafficLimit > 0 ? ` / ${formatBytes(trafficLimit)} (${trafficPct}%)` : ''}
              </span>
              {trafficLimit && trafficLimit > 0 && (
                <div className={styles.progressTrack} title={`已用: ${trafficPct}%`}>
                  <div
                    className={styles.progressBar}
                    style={{
                      width: `${Math.min(100, Math.max(0, trafficRatio * 100))}%`,
                      backgroundColor: trafficColorVar,
                    }}
                  />
                </div>
              )}
            </div>

            <div className={styles.factBox}>
              <span className="text-dim" style={{ fontSize: 11 }}>流量计费口径</span>
              <span className="cell-mono">
                {node?.billing?.traffic_limit_kind === 'sum'
                  ? '双向流量求和'
                  : node?.billing?.traffic_limit_kind === 'max'
                  ? '取较大单向计费'
                  : '单向出站 (默认)'}
              </span>
            </div>
          </div>
        </div>
      </div>

      {/* 机器硬件与系统规格明细 (node_facts) */}
      <div className="card">
        <div className="card-header">
          <span>硬件与系统规格 (node_facts)</span>
        </div>
        <div className="card-body">
          <div className={styles.factsGrid}>
            <div className={styles.factBox}>
              <span className="text-dim" style={{ fontSize: 11 }}>CPU 型号</span>
              <span className="cell-mono">{node?.facts?.cpu_model || '--'}</span>
            </div>
            <div className={styles.factBox}>
              <span className="text-dim" style={{ fontSize: 11 }}>物理核心 / 逻辑线程</span>
              <span className="cell-mono">
                {node?.facts?.cpu_cores ? `${node.facts.cpu_cores} 核` : '--'} /{' '}
                {node?.facts?.cpu_threads ? `${node.facts.cpu_threads} 线程` : '--'}
              </span>
            </div>
            <div className={styles.factBox}>
              <span className="text-dim" style={{ fontSize: 11 }}>总物理内存 / Swap</span>
              <span className="cell-mono">
                {node?.facts?.mem_total ? formatBytes(node.facts.mem_total) : '--'}
                {node?.facts?.swap_total ? ` / Swap ${formatBytes(node.facts.swap_total)}` : ''}
              </span>
            </div>
            <div className={styles.factBox}>
              <span className="text-dim" style={{ fontSize: 11 }}>总磁盘容量</span>
              <span className="cell-mono">
                {node?.facts?.disk_total ? formatBytes(node.facts.disk_total) : '--'}
              </span>
            </div>
            <div className={styles.factBox}>
              <span className="text-dim" style={{ fontSize: 11 }}>内核版本</span>
              <span className="cell-mono">{node?.facts?.kernel || '--'}</span>
            </div>
            <div className={styles.factBox}>
              <span className="text-dim" style={{ fontSize: 11 }}>发行版详情</span>
              <span className="cell-mono">
                {node?.facts?.os_name || '--'} {node?.facts?.os_version || ''}
              </span>
            </div>
            <div className={styles.factBox}>
              <span className="text-dim" style={{ fontSize: 11 }}>公网 IPv6</span>
              <span className="cell-mono" style={{ fontSize: 11 }}>{node?.facts?.ipv6 || '--'}</span>
            </div>
            <div className={styles.factBox}>
              <span className="text-dim" style={{ fontSize: 11 }}>系统开机时间</span>
              <span className="cell-mono">{node?.facts?.boot_at_ms ? formatDateTime(node.facts.boot_at_ms) : '--'}</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
};
