import React, { useState } from 'react';
import { useParams, Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { getNode } from '../../api/nodes';
import { getMetrics } from '../../api/metrics';
import { TimeSeriesSpan } from '../../api/types';
import { TimeSeriesChart } from '../../components/TimeSeriesChart';
import styles from './NodeDetail.module.css';

export const NodeDetail: React.FC = () => {
  const { id = '01JBX79N1A9W5K3E8S6T2Y4P0A' } = useParams<{ id: string }>();
  const [span, setSpan] = useState<TimeSeriesSpan>('6h');
  const [activeMetricTab, setActiveMetricTab] = useState<'all' | 'cpu' | 'mem' | 'net'>('all');

  const { data: node } = useQuery({
    queryKey: ['node', id],
    queryFn: () => getNode(id),
  });

  const now = Date.now();
  const getSpanDuration = (s: TimeSeriesSpan) => {
    switch (s) {
      case '6h': return 6 * 3600 * 1000;
      case '3d': return 3 * 86400 * 1000;
      case '60d': return 60 * 86400 * 1000;
      case '1y': return 365 * 86400 * 1000;
    }
  };

  const { data: metricsData, isLoading: metricsLoading } = useQuery({
    queryKey: ['metrics', id, span],
    queryFn: () =>
      getMetrics(id, {
        from_ms: now - getSpanDuration(span),
        to_ms: now,
        span,
      }),
  });

  // 根据选中的 Tab 决定要绘制的指标
  const getSelectedMetrics = () => {
    switch (activeMetricTab) {
      case 'cpu':
        return ['cpu_pct', 'load1'];
      case 'mem':
        return ['mem_used'];
      case 'net':
        return ['net_up_bps', 'net_down_bps'];
      default:
        return ['cpu_pct', 'net_up_bps', 'net_down_bps'];
    }
  };

  const isOnline = node?.conn_state === 'online';

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
            <span className={`health ${isOnline ? 'ok' : 'err'}`}>
              <span className="hdot" />
            </span>
            <span className={styles.nodeName}>{node?.name}</span>
            <span className="badge badge-info">{node?.group?.name || '默认分组'}</span>
          </div>

          <div style={{ display: 'flex', gap: 6 }}>
            {node?.tags?.map((t) => (
              <span key={t.id} className="badge" style={{ color: t.color }}>
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
            <span className={styles.metaLabel}>系统架构</span>
            <span className={styles.metaVal}>{node?.facts?.os_name} ({node?.facts?.arch})</span>
          </div>
          <div className={styles.metaItem}>
            <span className={styles.metaLabel}>虚拟化类型</span>
            <span className={styles.metaVal}>{node?.facts?.virt || 'KVM'}</span>
          </div>
          <div className={styles.metaItem}>
            <span className={styles.metaLabel}>运行时长</span>
            <span className={styles.metaVal}>
              {node?.latest?.uptime_s ? `${Math.floor(node.latest.uptime_s / 86400)} 天` : '--'}
            </span>
          </div>
        </div>
      </div>

      {/* 时序监控图表 (验证 P1-15 核心交付物) */}
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
            CPU & Load
          </button>
          <button
            type="button"
            className={`${styles.metricTab} ${activeMetricTab === 'mem' ? styles.active : ''}`}
            onClick={() => setActiveMetricTab('mem')}
          >
            内存趋势
          </button>
          <button
            type="button"
            className={`${styles.metricTab} ${activeMetricTab === 'net' ? styles.active : ''}`}
            onClick={() => setActiveMetricTab('net')}
          >
            网络上传 / 下载
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

      {/* 机器硬件规格明细 */}
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
              <span className="text-dim" style={{ fontSize: 11 }}>物理核心 / 线程</span>
              <span className="cell-mono">{node?.facts?.cpu_cores} 核 / {node?.facts?.cpu_threads} 线程</span>
            </div>
            <div className={styles.factBox}>
              <span className="text-dim" style={{ fontSize: 11 }}>总物理内存</span>
              <span className="cell-mono">
                {node?.facts?.mem_total ? `${(node.facts.mem_total / 1073741824).toFixed(1)} GB` : '--'}
              </span>
            </div>
            <div className={styles.factBox}>
              <span className="text-dim" style={{ fontSize: 11 }}>总磁盘容量</span>
              <span className="cell-mono">
                {node?.facts?.disk_total ? `${(node.facts.disk_total / 1073741824).toFixed(0)} GB` : '--'}
              </span>
            </div>
            <div className={styles.factBox}>
              <span className="text-dim" style={{ fontSize: 11 }}>内核版本</span>
              <span className="cell-mono">{node?.facts?.kernel || '--'}</span>
            </div>
            <div className={styles.factBox}>
              <span className="text-dim" style={{ fontSize: 11 }}>IPv6 地址</span>
              <span className="cell-mono" style={{ fontSize: 11 }}>{node?.facts?.ipv6 || '--'}</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
};
