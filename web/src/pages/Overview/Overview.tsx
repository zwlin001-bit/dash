import React, { useState, useEffect, useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { checkHealth, getNodeGroups, getNodes, getTags } from '../../api/nodes';
import { subscribeMetricsStream, SSEConnectionStatus } from '../../api/metrics';
import { MetricStreamEvent, NodeLatest, NodeStateStreamEvent } from '../../api/types';
import { fmt, formatBytes, formatBps, formatTimeAgo } from '../../utils';
import styles from './Overview.module.css';

export const Overview: React.FC = () => {
  const [search, setSearch] = useState('');
  const [selectedGroup, setSelectedGroup] = useState<string>('');
  const [selectedTag, setSelectedTag] = useState<string>('');
  const [selectedState, setSelectedState] = useState<'all' | 'online' | 'offline' | 'never'>('all');

  // SSE 连接状态（'connecting' | 'connected' | 'disconnected'）
  const [sseStatus, setSseStatus] = useState<SSEConnectionStatus>('connecting');

  // 实时 SSE 推送的指标与状态缓存（不轮询数据库，docs/12-api-spec.md §6）
  const [realtimeMetrics, setRealtimeMetrics] = useState<Record<string, Partial<NodeLatest>>>({});
  const [realtimeStates, setRealtimeStates] = useState<
    Record<string, { conn_state: 'online' | 'offline' | 'never'; last_seen_at_ms: number }>
  >({});

  // 检查数据库健康状态（验证约束 5：数据库不可用时的顶部横幅与内存降级）
  const { data: healthData } = useQuery({
    queryKey: ['healthz'],
    queryFn: checkHealth,
    refetchInterval: 15000,
  });

  const isDbUnavailable = healthData?.db === 'error';

  // 节点列表查询：SSE 连通时依靠推送，断开时降级为 15 秒轮询保底
  const { data: rawNodes = [], isLoading } = useQuery({
    queryKey: ['nodes'],
    queryFn: () => getNodes(),
    staleTime: 30000,
    refetchInterval: sseStatus === 'connected' ? false : 15000,
  });

  // 加载分组与标签过滤候选项
  const { data: groupList = [] } = useQuery({
    queryKey: ['node-groups'],
    queryFn: getNodeGroups,
    staleTime: 60000,
  });

  const { data: tagList = [] } = useQuery({
    queryKey: ['tags'],
    queryFn: getTags,
    staleTime: 60000,
  });

  // 建立 SSE 订阅（实时刷新走 SSE，断开自动重连，不可用时降级轮询）
  useEffect(() => {
    const unsubscribe = subscribeMetricsStream({
      onStatusChange: (status) => {
        setSseStatus(status);
      },
      onMetrics: (ev: MetricStreamEvent) => {
        setRealtimeMetrics((prev) => ({
          ...prev,
          [ev.node_id]: {
            ...(prev[ev.node_id] || {}),
            ts_ms: ev.ts_ms,
            cpu_pct: ev.cpu_pct,
            mem_used: ev.mem_used,
            net_up_bps: ev.net_up_bps,
            net_down_bps: ev.net_down_bps,
            disk_used: ev.disk_used,
          },
        }));
      },
      onNodeState: (ev: NodeStateStreamEvent) => {
        setRealtimeStates((prev) => ({
          ...prev,
          [ev.node_id]: {
            conn_state: ev.conn_state,
            last_seen_at_ms: ev.last_seen_at_ms,
          },
        }));
        if (ev.conn_state !== 'online') {
          setRealtimeMetrics((prev) => {
            const next = { ...prev };
            delete next[ev.node_id];
            return next;
          });
        }
      },
    });

    return () => {
      unsubscribe();
    };
  }, []);

  // 将静态列表与 SSE 最新状态/指标动态合并（不构造伪造的 0 值）
  const liveNodes = useMemo(() => {
    return rawNodes.map((node) => {
      const stateOverride = realtimeStates[node.id];
      const metricOverride = realtimeMetrics[node.id];

      const mergedConnState = stateOverride?.conn_state ?? node.conn_state;
      const mergedLastSeen = stateOverride?.last_seen_at_ms ?? node.last_seen_at_ms;

      let mergedLatest: NodeLatest | undefined = undefined;
      if (mergedConnState === 'online' && (node.latest || metricOverride)) {
        mergedLatest = {
          ...(node.latest || {}),
          ...(metricOverride || {}),
        };
      }

      return {
        ...node,
        conn_state: mergedConnState,
        last_seen_at_ms: mergedLastSeen,
        latest: mergedLatest,
      };
    });
  }, [rawNodes, realtimeStates, realtimeMetrics]);

  // 多条件过滤：分组、标签、在线状态、关键词搜索
  const filteredNodes = useMemo(() => {
    return liveNodes.filter((n) => {
      // 在线状态过滤
      if (selectedState !== 'all' && n.conn_state !== selectedState) {
        return false;
      }
      // 分组过滤
      if (selectedGroup && n.group?.id !== selectedGroup && n.group?.name !== selectedGroup) {
        return false;
      }
      // 标签过滤
      if (selectedTag && !n.tags?.some((t) => t.id === selectedTag || t.name === selectedTag)) {
        return false;
      }
      // 模糊搜索
      if (search.trim()) {
        const q = search.trim().toLowerCase();
        const matchName = n.name.toLowerCase().includes(q);
        const matchGroup = n.group?.name.toLowerCase().includes(q) ?? false;
        const matchTag = n.tags?.some((t) => t.name.toLowerCase().includes(q)) ?? false;
        if (!matchName && !matchGroup && !matchTag) {
          return false;
        }
      }
      return true;
    });
  }, [liveNodes, selectedState, selectedGroup, selectedTag, search]);

  const onlineCount = liveNodes.filter((n) => n.conn_state === 'online').length;
  const offlineCount = liveNodes.filter((n) => n.conn_state === 'offline').length;
  const neverCount = liveNodes.filter((n) => n.conn_state === 'never').length;

  const onlineNodesWithCpu = liveNodes.filter(
    (n) => n.conn_state === 'online' && n.latest?.cpu_pct !== undefined && n.latest?.cpu_pct !== null
  );
  const avgCpu =
    onlineNodesWithCpu.length > 0
      ? onlineNodesWithCpu.reduce((acc, n) => acc + (n.latest?.cpu_pct || 0), 0) /
        onlineNodesWithCpu.length
      : null;

  const onlineNodes = liveNodes.filter((n) => n.conn_state === 'online');
  const hasUpBps = onlineNodes.some(
    (n) => n.latest?.net_up_bps !== undefined && n.latest?.net_up_bps !== null
  );
  const totalUpBps = hasUpBps
    ? onlineNodes.reduce((acc, n) => acc + (n.latest?.net_up_bps || 0), 0)
    : null;

  const hasDownBps = onlineNodes.some(
    (n) => n.latest?.net_down_bps !== undefined && n.latest?.net_down_bps !== null
  );
  const totalDownBps = hasDownBps
    ? onlineNodes.reduce((acc, n) => acc + (n.latest?.net_down_bps || 0), 0)
    : null;

  const hasFilterActive =
    search.trim() !== '' || selectedGroup !== '' || selectedTag !== '' || selectedState !== 'all';

  const resetFilters = () => {
    setSearch('');
    setSelectedGroup('');
    setSelectedTag('');
    setSelectedState('all');
  };

  return (
    <div>
      {/* 数据库不可用顶部横幅（docs/13-ui-spec.md §6 与验收约束 5） */}
      {isDbUnavailable && (
        <div className="db-banner err" style={{ marginBottom: 16 }}>
          <span>⚠️ 数据库服务不可用，当前页面正使用内存缓存数据与 SSE 实时监控流</span>
        </div>
      )}

      <div className="page-head">
        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
          <h2>节点总览</h2>
          <span
            className={`health ${sseStatus === 'connected' ? 'ok' : 'err'}`}
            title={sseStatus === 'connected' ? '实时监控已连接 (SSE)' : '实时监控已断开，已降级为 15 秒轮询'}
          >
            <span className="hdot" />
            <span>{sseStatus === 'connected' ? '实时' : '已断开'}</span>
          </span>
        </div>
        <span className="count">共 {liveNodes.length} 台节点</span>
      </div>

      {/* KPI 指标卡 */}
      <div className={styles.kpiGrid}>
        <div className={styles.kpiCard}>
          <span className={styles.kpiTitle}>在线节点</span>
          <span className={styles.kpiValue} style={{ color: 'var(--ok)' }}>
            {onlineCount} <span style={{ fontSize: 13, color: 'var(--text-dim)' }}>/ {liveNodes.length}</span>
          </span>
          <span className={styles.kpiSub}>
            离线: {offlineCount} 台{neverCount > 0 ? ` · 未上线: ${neverCount} 台` : ''}
          </span>
        </div>

        <div className={styles.kpiCard}>
          <span className={styles.kpiTitle}>平均 CPU</span>
          <span className={styles.kpiValue}>{fmt(avgCpu, '%')}</span>
          <span className={styles.kpiSub}>仅统计在线节点</span>
        </div>

        <div className={styles.kpiCard}>
          <span className={styles.kpiTitle}>总上传带宽</span>
          <span className={styles.kpiValue}>{formatBps(totalUpBps)}</span>
          <span className={styles.kpiSub}>实时出站速率</span>
        </div>

        <div className={styles.kpiCard}>
          <span className={styles.kpiTitle}>总下载带宽</span>
          <span className={styles.kpiValue}>{formatBps(totalDownBps)}</span>
          <span className={styles.kpiSub}>实时入站速率</span>
        </div>
      </div>

      {/* 过滤控制栏（分组、标签、在线状态过滤） */}
      <div className={styles.filterBar}>
        <input
          type="search"
          placeholder="搜索节点名称、分组、标签..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          style={{ width: 220 }}
        />

        {/* 分组过滤 */}
        <select
          className={styles.filterSelect}
          value={selectedGroup}
          onChange={(e) => setSelectedGroup(e.target.value)}
        >
          <option value="">全部分组</option>
          {groupList.map((g) => (
            <option key={g.id} value={g.id}>
              {g.name}
            </option>
          ))}
        </select>

        {/* 标签过滤 */}
        <select
          className={styles.filterSelect}
          value={selectedTag}
          onChange={(e) => setSelectedTag(e.target.value)}
        >
          <option value="">全部标签</option>
          {tagList.map((t) => (
            <option key={t.id} value={t.id}>
              {t.name}
            </option>
          ))}
        </select>

        {/* 在线状态快速切换 */}
        <div className={styles.stateTabs}>
          <button
            type="button"
            className={`${styles.stateTab} ${selectedState === 'all' ? styles.active : ''}`}
            onClick={() => setSelectedState('all')}
          >
            全部
          </button>
          <button
            type="button"
            className={`${styles.stateTab} ${selectedState === 'online' ? styles.active : ''}`}
            onClick={() => setSelectedState('online')}
          >
            在线
          </button>
          <button
            type="button"
            className={`${styles.stateTab} ${selectedState === 'offline' ? styles.active : ''}`}
            onClick={() => setSelectedState('offline')}
          >
            离线
          </button>
          <button
            type="button"
            className={`${styles.stateTab} ${selectedState === 'never' ? styles.active : ''}`}
            onClick={() => setSelectedState('never')}
          >
            未上线
          </button>
        </div>

        {hasFilterActive && (
          <button type="button" className="btn mini ghost" onClick={resetFilters}>
            清空过滤
          </button>
        )}

        <span className="count" style={{ marginLeft: 'auto' }}>
          匹配 {filteredNodes.length} 台
        </span>
      </div>

      {/* 节点表格（全部规范遵循 docs/13-ui-spec.md §6） */}
      <div className="tbl-wrap">
        <table className="tbl">
          <thead>
            <tr>
              <th style={{ width: 80 }}>状态</th>
              <th>节点名称</th>
              <th>分组</th>
              <th>标签</th>
              <th className="col-num">CPU</th>
              <th className="col-num">内存占用</th>
              <th className="col-num">磁盘占用</th>
              <th className="col-num">上下行网速</th>
              <th className="col-num">当月流量</th>
              <th>到期时间</th>
              <th style={{ textAlign: 'right' }}>操作</th>
            </tr>
          </thead>
          <tbody>
            {isLoading ? (
              <tr>
                <td colSpan={11} style={{ textAlign: 'center', padding: '32px 0', color: 'var(--text-mute)' }}>
                  加载中...
                </td>
              </tr>
            ) : filteredNodes.length === 0 ? (
              <tr>
                <td colSpan={11} style={{ textAlign: 'center', padding: '32px 0', color: 'var(--text-mute)' }}>
                  未匹配到节点
                </td>
              </tr>
            ) : (
              filteredNodes.map((node) => {
                const isOnline = node.conn_state === 'online';
                const isOffline = node.conn_state === 'offline';

                // 到期状态计算
                const daysLeft = node.billing?.expires_at_ms
                  ? Math.ceil((node.billing.expires_at_ms - Date.now()) / 86400000)
                  : null;

                // 内存占用
                const memUsed = node.latest?.mem_used;
                const memTotal = node.latest?.mem_total ?? node.facts?.mem_total;
                let memText = '--';
                if (memUsed !== undefined && memUsed !== null && memTotal && memTotal > 0) {
                  const memPct = ((memUsed / memTotal) * 100).toFixed(0);
                  memText = `${memPct}% (${formatBytes(memUsed)}/${formatBytes(memTotal)})`;
                } else if (memUsed !== undefined && memUsed !== null) {
                  memText = formatBytes(memUsed);
                }

                // 磁盘占用
                const diskUsed = node.latest?.disk_used;
                const diskTotal = node.latest?.disk_total ?? node.facts?.disk_total;
                let diskText = '--';
                if (diskUsed !== undefined && diskUsed !== null && diskTotal && diskTotal > 0) {
                  const diskPct = ((diskUsed / diskTotal) * 100).toFixed(0);
                  diskText = `${diskPct}% (${formatBytes(diskUsed)}/${formatBytes(diskTotal)})`;
                } else if (diskUsed !== undefined && diskUsed !== null) {
                  diskText = formatBytes(diskUsed);
                }

                // 当月流量计算与阈值着色（流量 ≥80% 橙 --warn，≥100% 红 --err）
                const trafficUp = node.latest?.traffic_month_up;
                const trafficDown = node.latest?.traffic_month_down;
                const hasTraffic =
                  (trafficUp !== undefined && trafficUp !== null) ||
                  (trafficDown !== undefined && trafficDown !== null);
                const monthTrafficUsed = (trafficUp || 0) + (trafficDown || 0);
                const trafficLimit = node.billing?.traffic_limit;
                let trafficRatio = 0;
                let trafficWarnClass = '';
                if (hasTraffic && trafficLimit && trafficLimit > 0) {
                  trafficRatio = monthTrafficUsed / trafficLimit;
                  if (trafficRatio >= 1.0) {
                    trafficWarnClass = styles.trafficErr;
                  } else if (trafficRatio >= 0.8) {
                    trafficWarnClass = styles.trafficWarn;
                  }
                }

                return (
                  <tr
                    key={node.id}
                    className={isOffline ? styles.offlineRow : ''}
                  >
                    {/* 在线状态点 + 离线显示多久之前 */}
                    <td>
                      <div>
                        <span
                          className={`health ${isOnline ? 'ok' : isOffline ? 'err' : ''}`}
                          title={isOnline ? '在线' : isOffline ? '离线' : '未上线'}
                        >
                          <span className="hdot" />
                          <span>{isOnline ? '在线' : isOffline ? '离线' : '未上线'}</span>
                        </span>
                        {!isOnline && (
                          <div className={styles.timeAgo}>
                            {isOffline ? formatTimeAgo(node.last_seen_at_ms) : '从未上线'}
                          </div>
                        )}
                      </div>
                    </td>

                    {/* 节点名称 + 时钟偏差标记 */}
                    <td>
                      <Link to={`/nodes/${node.id}`} className={styles.nodeLink}>
                        {node.name}
                      </Link>
                      {node.clock_skew_ms != null && Math.abs(node.clock_skew_ms) > 30000 && (
                        <span
                          className={`badge badge-neutral ${styles.clockBadge}`}
                          title={`时钟异常，偏差 ${(node.clock_skew_ms / 1000).toFixed(1)} 秒`}
                        >
                          时钟异常
                        </span>
                      )}
                    </td>

                    {/* 分组 */}
                    <td>{node.group?.name || '--'}</td>

                    {/* 标签 */}
                    <td>
                      <div className={styles.tagList}>
                        {node.tags && node.tags.length > 0
                          ? node.tags.map((t) => (
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
                            ))
                          : '--'}
                      </div>
                    </td>

                    {/* CPU */}
                    <td className="col-num cell-mono">
                      {fmt(node.latest?.cpu_pct, '%')}
                    </td>

                    {/* 内存 */}
                    <td className="col-num cell-mono">{memText}</td>

                    {/* 磁盘 */}
                    <td className="col-num cell-mono">{diskText}</td>

                    {/* 上下行网速 */}
                    <td className="col-num cell-mono">
                      <span className="text-pos">↑ {formatBps(node.latest?.net_up_bps)}</span>
                      {' / '}
                      <span style={{ color: 'var(--color-blue)' }}>
                        ↓ {formatBps(node.latest?.net_down_bps)}
                      </span>
                    </td>

                    {/* 当月流量 */}
                    <td className={`col-num cell-mono ${trafficWarnClass}`}>
                      {!hasTraffic ? (
                        '--'
                      ) : trafficLimit && trafficLimit > 0 ? (
                        <span>
                          {formatBytes(monthTrafficUsed)} / {formatBytes(trafficLimit)}
                          <span style={{ fontSize: 11, marginLeft: 4 }}>
                            ({(trafficRatio * 100).toFixed(0)}%)
                          </span>
                        </span>
                      ) : (
                        <span>{formatBytes(monthTrafficUsed)}</span>
                      )}
                    </td>

                    {/* 到期时间（≤7 天橙 --warn，已过期红 --err） */}
                    <td>
                      {daysLeft !== null ? (
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
                          {daysLeft <= 0 ? '已过期' : `${daysLeft} 天后`}
                        </span>
                      ) : (
                        '--'
                      )}
                    </td>

                    {/* 操作 */}
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
