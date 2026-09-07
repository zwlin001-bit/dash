import React, { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { getNodes } from '../../api/nodes';
import styles from './Overview.module.css';

export const Overview: React.FC = () => {
  const [search, setSearch] = useState('');
  const { data: nodes = [], isLoading } = useQuery({
    queryKey: ['nodes'],
    queryFn: getNodes,
  });

  const filteredNodes = nodes.filter((n) =>
    n.name.toLowerCase().includes(search.toLowerCase()) ||
    n.group?.name.toLowerCase().includes(search.toLowerCase()) ||
    n.tags?.some((t) => t.name.toLowerCase().includes(search.toLowerCase()))
  );

  const onlineCount = nodes.filter((n) => n.conn_state === 'online').length;
  const offlineCount = nodes.filter((n) => n.conn_state === 'offline').length;

  const formatBps = (bps?: number) => {
    if (!bps) return '0 B/s';
    if (bps >= 1024 * 1024) return `${(bps / (1024 * 1024)).toFixed(1)} MB/s`;
    if (bps >= 1024) return `${(bps / 1024).toFixed(0)} KB/s`;
    return `${bps} B/s`;
  };

  const formatMem = (used?: number, total?: number) => {
    if (!used || !total) return '--';
    const pct = ((used / total) * 100).toFixed(0);
    const usedGB = (used / 1073741824).toFixed(1);
    const totalGB = (total / 1073741824).toFixed(0);
    return `${pct}% (${usedGB}/${totalGB}G)`;
  };

  return (
    <div>
      <div className="page-head">
        <h2>节点总览</h2>
        <span className="count">共 {nodes.length} 台节点</span>

        <div className="toolbar">
          <input
            type="search"
            placeholder="搜索节点、分组、标签..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            style={{ width: 220 }}
          />
        </div>
      </div>

      {/* KPI 指标卡 */}
      <div className={styles.kpiGrid}>
        <div className={styles.kpiCard}>
          <span className={styles.kpiTitle}>在线节点</span>
          <span className={styles.kpiValue} style={{ color: 'var(--ok)' }}>
            {onlineCount} <span style={{ fontSize: 13, color: 'var(--text-dim)' }}>/ {nodes.length}</span>
          </span>
          <span className={styles.kpiSub}>离线: {offlineCount} 台</span>
        </div>

        <div className={styles.kpiCard}>
          <span className={styles.kpiTitle}>平均 CPU</span>
          <span className={styles.kpiValue}>
            {nodes.length > 0
              ? (nodes.reduce((acc, n) => acc + (n.latest?.cpu_pct || 0), 0) / (onlineCount || 1)).toFixed(1)
              : 0}
            %
          </span>
          <span className={styles.kpiSub}>计算周期 5s</span>
        </div>

        <div className={styles.kpiCard}>
          <span className={styles.kpiTitle}>总上传带宽</span>
          <span className={styles.kpiValue}>
            {formatBps(nodes.reduce((acc, n) => acc + (n.latest?.net_up_bps || 0), 0))}
          </span>
          <span className={styles.kpiSub}>出站速率实时统计</span>
        </div>

        <div className={styles.kpiCard}>
          <span className={styles.kpiTitle}>总下载带宽</span>
          <span className={styles.kpiValue}>
            {formatBps(nodes.reduce((acc, n) => acc + (n.latest?.net_down_bps || 0), 0))}
          </span>
          <span className={styles.kpiSub}>入站速率实时统计</span>
        </div>
      </div>

      {/* 节点表格 */}
      <div className="tbl-wrap">
        <table className="tbl">
          <thead>
            <tr>
              <th style={{ width: 40 }}>状态</th>
              <th>节点名称</th>
              <th>分组</th>
              <th>标签</th>
              <th className="col-num">CPU</th>
              <th className="col-num">内存</th>
              <th className="col-num">上传 / 下载</th>
              <th>到期时间</th>
              <th style={{ textAlign: 'right' }}>操作</th>
            </tr>
          </thead>
          <tbody>
            {isLoading ? (
              <tr>
                <td colSpan={9} style={{ textAlign: 'center', padding: '32px 0', color: 'var(--text-mute)' }}>
                  加载中...
                </td>
              </tr>
            ) : filteredNodes.length === 0 ? (
              <tr>
                <td colSpan={9} style={{ textAlign: 'center', padding: '32px 0', color: 'var(--text-mute)' }}>
                  未匹配到节点
                </td>
              </tr>
            ) : (
              filteredNodes.map((node) => {
                const isOnline = node.conn_state === 'online';
                const isOffline = node.conn_state === 'offline';
                const daysLeft = node.billing?.expires_at_ms
                  ? Math.ceil((node.billing.expires_at_ms - Date.now()) / 86400000)
                  : null;

                return (
                  <tr key={node.id} style={{ opacity: isOffline ? 0.75 : 1 }}>
                    <td>
                      <span
                        className={`health ${isOnline ? 'ok' : isOffline ? 'err' : ''}`}
                        title={node.conn_state}
                      >
                        <span className="hdot" />
                      </span>
                    </td>
                    <td>
                      <Link to={`/nodes/${node.id}`} className={styles.nodeLink}>
                        {node.name}
                      </Link>
                      {node.clock_skew_ms && Math.abs(node.clock_skew_ms) > 30000 && (
                        <span className="badge badge-neutral" style={{ marginLeft: 6 }}>
                          时钟偏差
                        </span>
                      )}
                    </td>
                    <td>{node.group?.name || '--'}</td>
                    <td>
                      <div className={styles.tagList}>
                        {node.tags?.map((t) => (
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
                        )) || '--'}
                      </div>
                    </td>
                    <td className="col-num cell-mono">
                      {node.latest?.cpu_pct !== undefined ? `${node.latest.cpu_pct.toFixed(1)}%` : '--'}
                    </td>
                    <td className="col-num cell-mono">
                      {formatMem(node.latest?.mem_used, node.latest?.mem_total)}
                    </td>
                    <td className="col-num cell-mono">
                      <span className="text-pos">↑ {formatBps(node.latest?.net_up_bps)}</span>
                      {' / '}
                      <span style={{ color: 'var(--color-blue)' }}>↓ {formatBps(node.latest?.net_down_bps)}</span>
                    </td>
                    <td>
                      {daysLeft !== null ? (
                        <span
                          className={`pill ${
                            daysLeft <= 0 ? 'red' : daysLeft <= 7 ? 'warn' : 'grey'
                          }`}
                          style={{
                            color:
                              daysLeft <= 0
                                ? 'var(--err)'
                                : daysLeft <= 7
                                ? 'var(--warn)'
                                : 'var(--text-dim)',
                          }}
                        >
                          {daysLeft <= 0 ? '已到期' : `${daysLeft} 天后`}
                        </span>
                      ) : (
                        '--'
                      )}
                    </td>
                    <td style={{ textAlign: 'right' }}>
                      <Link to={`/nodes/${node.id}`} className="btn mini">
                        查看详情
                      </Link>
                    </td>
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
};
