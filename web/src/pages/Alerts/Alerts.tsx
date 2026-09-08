import React, { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  fetchAlertRules,
  createAlertRule,
  updateAlertRule,
  deleteAlertRule,
  fetchAlertEvents,
  fetchActiveAlerts,
  triggerAlertEval,
  AlertRule,
  fetchNotifyChannels,
} from '../../api';
import styles from './Alerts.module.css';

export const Alerts: React.FC = () => {
  const queryClient = useQueryClient();
  const [activeTab, setActiveTab] = useState<'rules' | 'events'>('rules');

  // Active alerts query
  const { data: activeData } = useQuery({
    queryKey: ['active-alerts'],
    queryFn: fetchActiveAlerts,
    refetchInterval: 10000,
  });
  const activeAlerts = activeData?.items || [];

  // Rules query
  const { data: rulesData, isLoading: rulesLoading } = useQuery({
    queryKey: ['alert-rules'],
    queryFn: fetchAlertRules,
  });
  const rules = rulesData?.items || [];

  // Events query
  const [eventStateFilter, setEventStateFilter] = useState<string>('');
  const { data: eventsData, isLoading: eventsLoading } = useQuery({
    queryKey: ['alert-events', eventStateFilter],
    queryFn: () =>
      fetchAlertEvents({
        state: eventStateFilter || undefined,
        limit: 50,
      }),
    refetchInterval: 10000,
  });
  const alertEvents = eventsData?.items || [];

  // Channels query for modal
  const { data: channelsData } = useQuery({
    queryKey: ['notify-channels'],
    queryFn: fetchNotifyChannels,
  });
  const channels = channelsData?.items || [];

  // Modal State
  const [isModalOpen, setIsModalOpen] = useState<boolean>(false);
  const [editingRule, setEditingRule] = useState<AlertRule | null>(null);

  // Form Fields
  const [formName, setFormName] = useState<string>('');
  const [formKind, setFormKind] = useState<'metric' | 'offline' | 'expiry' | 'traffic'>('metric');
  const [formScopeKind, setFormScopeKind] = useState<'all' | 'group' | 'tag' | 'node'>('all');
  const [formScopeRef, setFormScopeRef] = useState<string>('');
  const [formMetricCode, setFormMetricCode] = useState<string>('cpu_pct');
  const [formCompareOp, setFormCompareOp] = useState<'gt' | 'gte' | 'lt' | 'lte'>('gt');
  const [formThreshold, setFormThreshold] = useState<number>(90);
  const [formDurationS, setFormDurationS] = useState<number>(60);
  const [formSeverity, setFormSeverity] = useState<'info' | 'warning' | 'critical'>('warning');
  const [formSilenceS, setFormSilenceS] = useState<number>(3600);
  const [formIncludeAutoRenew, setFormIncludeAutoRenew] = useState<boolean>(false);
  const [formChannelIDs, setFormChannelIDs] = useState<string[]>([]);
  const [formError, setFormError] = useState<string>('');

  // Mutations
  const createMutation = useMutation({
    mutationFn: createAlertRule,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['alert-rules'] });
      closeModal();
    },
    onError: (err: Error) => {
      setFormError(err.message);
    },
  });

  const updateMutation = useMutation({
    mutationFn: ({ id, data }: { id: string; data: Partial<AlertRule> }) =>
      updateAlertRule(id, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['alert-rules'] });
      closeModal();
    },
    onError: (err: Error) => {
      setFormError(err.message);
    },
  });

  const deleteMutation = useMutation({
    mutationFn: deleteAlertRule,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['alert-rules'] });
    },
  });

  const evalMutation = useMutation({
    mutationFn: triggerAlertEval,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['active-alerts'] });
      queryClient.invalidateQueries({ queryKey: ['alert-events'] });
    },
  });

  const openCreateModal = () => {
    setEditingRule(null);
    setFormName('');
    setFormKind('metric');
    setFormScopeKind('all');
    setFormScopeRef('');
    setFormMetricCode('cpu_pct');
    setFormCompareOp('gt');
    setFormThreshold(90);
    setFormDurationS(60);
    setFormSeverity('warning');
    setFormSilenceS(3600);
    setFormIncludeAutoRenew(false);
    setFormChannelIDs([]);
    setFormError('');
    setIsModalOpen(true);
  };

  const openEditModal = (r: AlertRule) => {
    setEditingRule(r);
    setFormName(r.name);
    setFormKind(r.rule_kind as 'metric' | 'offline' | 'expiry' | 'traffic');
    setFormScopeKind(r.scope_kind);
    setFormScopeRef(r.scope_ref || '');
    setFormMetricCode(r.metric_code || 'cpu_pct');
    setFormCompareOp(r.compare_op);
    setFormThreshold(r.threshold);
    setFormDurationS(r.duration_s);
    setFormSeverity(r.severity);
    setFormSilenceS(r.silence_s);
    setFormIncludeAutoRenew(r.include_auto_renew || false);
    setFormChannelIDs(r.channel_ids || []);
    setFormError('');
    setIsModalOpen(true);
  };

  const closeModal = () => {
    setIsModalOpen(false);
    setEditingRule(null);
    setFormError('');
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!formName.trim()) {
      setFormError('请输入规则名称');
      return;
    }

    const payload: Partial<AlertRule> = {
      name: formName.trim(),
      is_enabled: editingRule ? editingRule.is_enabled : true,
      rule_kind: formKind,
      scope_kind: formScopeKind,
      scope_ref: formScopeRef.trim() || undefined,
      metric_code: formKind === 'metric' ? formMetricCode : undefined,
      compare_op: formCompareOp,
      threshold: Number(formThreshold),
      duration_s: Number(formDurationS),
      severity: formSeverity,
      silence_s: Number(formSilenceS),
      include_auto_renew: formKind === 'expiry' ? formIncludeAutoRenew : false,
      channel_ids: formChannelIDs,
    };

    if (editingRule) {
      updateMutation.mutate({ id: editingRule.id, data: payload });
    } else {
      createMutation.mutate(payload);
    }
  };

  const toggleRuleEnabled = (r: AlertRule) => {
    updateMutation.mutate({
      id: r.id,
      data: { is_enabled: !r.is_enabled },
    });
  };

  const handleDeleteRule = (id: string, name: string) => {
    if (window.confirm(`确定要删除告警规则 "${name}" 吗？关联的告警历史将一并清理。`)) {
      deleteMutation.mutate(id);
    }
  };

  const handleToggleChannel = (channelId: string) => {
    setFormChannelIDs((prev) =>
      prev.includes(channelId) ? prev.filter((id) => id !== channelId) : [...prev, channelId]
    );
  };

  const formatTime = (ms: number) => {
    if (!ms) return '--';
    const d = new Date(ms);
    return d.toLocaleString('zh-CN', { hour12: false });
  };

  const renderKindBadge = (kind: string) => {
    switch (kind) {
      case 'metric':
        return <span className={`${styles.badge} ${styles.badgeKind}`}>指标</span>;
      case 'offline':
        return <span className={`${styles.badge} ${styles.badgeKind}`}>离线</span>;
      case 'expiry':
        return <span className={`${styles.badge} ${styles.badgeKind}`}>到期</span>;
      case 'traffic':
        return <span className={`${styles.badge} ${styles.badgeKind}`}>流量</span>;
      default:
        return <span className={`${styles.badge} ${styles.badgeKind}`}>{kind}</span>;
    }
  };

  const renderSeverityBadge = (sev: string) => {
    switch (sev) {
      case 'critical':
        return <span className={`${styles.badge} ${styles.badgeCritical}`}>严重</span>;
      case 'warning':
        return <span className={`${styles.badge} ${styles.badgeWarning}`}>警告</span>;
      case 'info':
      default:
        return <span className={`${styles.badge} ${styles.badgeInfo}`}>提醒</span>;
    }
  };

  const renderCondition = (r: AlertRule) => {
    const opMap: Record<string, string> = { gt: '>', gte: '≥', lt: '<', lte: '≤' };
    const op = opMap[r.compare_op] || r.compare_op;

    switch (r.rule_kind) {
      case 'metric':
        return `${r.metric_code || 'cpu_pct'} ${op} ${r.threshold}${
          r.duration_s > 0 ? ` (持续 ${r.duration_s}s)` : ''
        }`;
      case 'offline':
        return `离线 ${op} ${r.threshold} 秒`;
      case 'expiry':
        return `到期剩余 ${op} ${r.threshold} 天`;
      case 'traffic':
        return `用量占比 ${op} ${(r.threshold * 100).toFixed(0)}%`;
      default:
        return `${op} ${r.threshold}`;
    }
  };

  const renderScope = (r: AlertRule) => {
    switch (r.scope_kind) {
      case 'node':
        return `节点: ${r.scope_ref || '--'}`;
      case 'tag':
        return `标签: ${r.scope_ref || '--'}`;
      case 'group':
        return `分组: ${r.scope_ref || '--'}`;
      case 'all':
      default:
        return '全部节点';
    }
  };

  return (
    <div className={styles.container}>
      {/* 顶部标题与操作 */}
      <div className={styles.header}>
        <div className={styles.titleArea}>
          <h1 className={styles.title}>告警中心</h1>
          {activeAlerts.length > 0 && (
            <span className={styles.firingPill} role="status">
              🔥 {activeAlerts.length} 项告警触发中
            </span>
          )}
        </div>
        <div className={styles.actionBtns}>
          <button
            type="button"
            className="btn mini ghost"
            onClick={() => evalMutation.mutate()}
            disabled={evalMutation.isPending}
            title="手动触发全量告警规则评估"
          >
            {evalMutation.isPending ? '评估中...' : '⚡ 立即评估'}
          </button>
          <button type="button" className="btn mini primary" onClick={openCreateModal}>
            + 新建规则
          </button>
        </div>
      </div>

      {/* 活跃告警横幅 */}
      {activeAlerts.length > 0 && (
        <div className={styles.activeCard}>
          <div className={styles.activeCardHeader}>
            <span>⚠️ 当前存在处于 firing 状态的活跃告警</span>
            <span>共 {activeAlerts.length} 个异常</span>
          </div>
          <div className={styles.activeList}>
            {activeAlerts.map((ev) => (
              <div key={ev.id} className={styles.activeItem}>
                <div className={styles.activeItemLeft}>
                  {renderSeverityBadge(ev.severity || 'warning')}
                  <div>
                    <div className={styles.activeItemTitle}>
                      {ev.rule_name || '告警'} · 节点: {ev.node_name || ev.node_id}
                    </div>
                    <div className={styles.activeItemDetail}>
                      {ev.detail || '指标越界触发'} · 触发时间: {formatTime(ev.fired_at_ms)}
                    </div>
                  </div>
                </div>
                <span className={`${styles.badge} ${styles.badgeFiring}`}>FIRING</span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Tab 切换 */}
      <div className={styles.tabs}>
        <button
          type="button"
          className={`${styles.tabBtn} ${activeTab === 'rules' ? styles.tabBtnActive : ''}`}
          onClick={() => setActiveTab('rules')}
        >
          告警规则 ({rules.length})
        </button>
        <button
          type="button"
          className={`${styles.tabBtn} ${activeTab === 'events' ? styles.tabBtnActive : ''}`}
          onClick={() => setActiveTab('events')}
        >
          告警历史
        </button>
      </div>

      {/* 规则列表 Tab */}
      {activeTab === 'rules' && (
        <div className={styles.card}>
          <div className={styles.tableWrapper}>
            <table className={styles.table}>
              <thead>
                <tr>
                  <th>规则名称</th>
                  <th>类型</th>
                  <th>触发条件</th>
                  <th>作用域</th>
                  <th>级别</th>
                  <th>静默期</th>
                  <th>通知渠道</th>
                  <th>状态</th>
                  <th style={{ textAlign: 'right' }}>操作</th>
                </tr>
              </thead>
              <tbody>
                {rulesLoading ? (
                  <tr>
                    <td colSpan={9} className={styles.emptyState}>
                      加载告警规则中...
                    </td>
                  </tr>
                ) : rules.length === 0 ? (
                  <tr>
                    <td colSpan={9} className={styles.emptyState}>
                      暂无告警规则，点击右上角 "+ 新建规则" 添加
                    </td>
                  </tr>
                ) : (
                  rules.map((r) => (
                    <tr key={r.id}>
                      <td style={{ fontWeight: 500 }}>{r.name}</td>
                      <td>{renderKindBadge(r.rule_kind)}</td>
                      <td>
                        <code>{renderCondition(r)}</code>
                      </td>
                      <td>{renderScope(r)}</td>
                      <td>{renderSeverityBadge(r.severity)}</td>
                      <td>
                        {r.silence_s >= 86400
                          ? `${(r.silence_s / 86400).toFixed(0)} 天`
                          : r.silence_s >= 3600
                          ? `${(r.silence_s / 3600).toFixed(0)} 小时`
                          : `${r.silence_s} 秒`}
                      </td>
                      <td>
                        {r.channel_ids && r.channel_ids.length > 0 ? (
                          <span title={r.channel_ids.join(', ')}>
                            {r.channel_ids.length} 个渠道
                          </span>
                        ) : (
                          <span style={{ color: 'var(--text-mute)' }}>未绑定</span>
                        )}
                      </td>
                      <td>
                        <button
                          type="button"
                          className={`btn mini ${r.is_enabled ? 'ghost' : 'secondary'}`}
                          onClick={() => toggleRuleEnabled(r)}
                        >
                          {r.is_enabled ? '已启用' : '已停用'}
                        </button>
                      </td>
                      <td style={{ textAlign: 'right' }}>
                        <div style={{ display: 'inline-flex', gap: 'var(--sp-2)' }}>
                          <button
                            type="button"
                            className="btn mini ghost"
                            onClick={() => openEditModal(r)}
                          >
                            编辑
                          </button>
                          <button
                            type="button"
                            className="btn mini ghost danger"
                            onClick={() => handleDeleteRule(r.id, r.name)}
                          >
                            删除
                          </button>
                        </div>
                      </td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* 告警历史 Tab */}
      {activeTab === 'events' && (
        <div className={styles.card}>
          <div className={styles.filterBar}>
            <select
              className={styles.filterSelect}
              value={eventStateFilter}
              onChange={(e) => setEventStateFilter(e.target.value)}
            >
              <option value="">全部状态</option>
              <option value="firing">firing (告警中)</option>
              <option value="resolved">resolved (已恢复)</option>
            </select>
          </div>

          <div className={styles.tableWrapper}>
            <table className={styles.table}>
              <thead>
                <tr>
                  <th>状态</th>
                  <th>级别</th>
                  <th>告警规则</th>
                  <th>目标节点</th>
                  <th>触发时间</th>
                  <th>恢复时间</th>
                  <th>详情描述</th>
                </tr>
              </thead>
              <tbody>
                {eventsLoading ? (
                  <tr>
                    <td colSpan={7} className={styles.emptyState}>
                      加载告警历史中...
                    </td>
                  </tr>
                ) : alertEvents.length === 0 ? (
                  <tr>
                    <td colSpan={7} className={styles.emptyState}>
                      暂无符合条件的告警历史记录
                    </td>
                  </tr>
                ) : (
                  alertEvents.map((ev) => (
                    <tr key={ev.id}>
                      <td>
                        {ev.event_state === 'firing' ? (
                          <span className={`${styles.badge} ${styles.badgeFiring}`}>
                            FIRING
                          </span>
                        ) : (
                          <span className={`${styles.badge} ${styles.badgeResolved}`}>
                            RESOLVED
                          </span>
                        )}
                      </td>
                      <td>{renderSeverityBadge(ev.severity || 'warning')}</td>
                      <td style={{ fontWeight: 500 }}>{ev.rule_name || ev.alert_rule_id}</td>
                      <td>{ev.node_name || ev.node_id}</td>
                      <td>{formatTime(ev.fired_at_ms)}</td>
                      <td>
                        {ev.resolved_at_ms ? (
                          formatTime(ev.resolved_at_ms)
                        ) : (
                          <span style={{ color: 'var(--danger)', fontWeight: 500 }}>
                            进行中
                          </span>
                        )}
                      </td>
                      <td style={{ color: 'var(--text-dim)', fontSize: '12px' }}>
                        {ev.detail || '--'}
                      </td>
                    </tr>
                  ))
                )}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* 创建 / 编辑规则 Modal */}
      {isModalOpen && (
        <div className={styles.modalOverlay}>
          <div className={styles.modalContent}>
            <div className={styles.modalHeader}>
              <h2 className={styles.modalTitle}>
                {editingRule ? '编辑告警规则' : '新建告警规则'}
              </h2>
              <button type="button" className={styles.closeBtn} onClick={closeModal}>
                ✕
              </button>
            </div>

            <form onSubmit={handleSubmit}>
              <div className={styles.modalBody}>
                {formError && (
                  <div
                    style={{
                      padding: 'var(--sp-2) var(--sp-3)',
                      backgroundColor: 'var(--danger-bg)',
                      border: '1px solid var(--danger-border)',
                      color: 'var(--danger)',
                      borderRadius: 'var(--radius-md)',
                      fontSize: '13px',
                    }}
                  >
                    {formError}
                  </div>
                )}

                <div className={styles.formGroup}>
                  <label className={styles.label}>规则名称</label>
                  <input
                    type="text"
                    className={styles.input}
                    value={formName}
                    onChange={(e) => setFormName(e.target.value)}
                    placeholder="例如：CPU 持续高负载预警"
                    required
                  />
                </div>

                <div className={styles.formRow}>
                  <div className={styles.formGroup}>
                    <label className={styles.label}>规则类型</label>
                    <select
                      className={styles.select}
                      value={formKind}
                      onChange={(e) =>
                        setFormKind(
                          e.target.value as 'metric' | 'offline' | 'expiry' | 'traffic'
                        )
                      }
                    >
                      <option value="metric">监控指标 (metric)</option>
                      <option value="offline">节点离线 (offline)</option>
                      <option value="expiry">VPS 到期提醒 (expiry)</option>
                      <option value="traffic">月度流量提醒 (traffic)</option>
                    </select>
                  </div>

                  <div className={styles.formGroup}>
                    <label className={styles.label}>告警级别</label>
                    <select
                      className={styles.select}
                      value={formSeverity}
                      onChange={(e) =>
                        setFormSeverity(e.target.value as 'info' | 'warning' | 'critical')
                      }
                    >
                      <option value="info">提醒 (info)</option>
                      <option value="warning">警告 (warning)</option>
                      <option value="critical">严重 (critical)</option>
                    </select>
                  </div>
                </div>

                {/* 指标专属字段 */}
                {formKind === 'metric' && (
                  <div className={styles.formGroup}>
                    <label className={styles.label}>监控指标 (Metric Code)</label>
                    <select
                      className={styles.select}
                      value={formMetricCode}
                      onChange={(e) => setFormMetricCode(e.target.value)}
                    >
                      <option value="cpu_pct">CPU 使用率 (%)</option>
                      <option value="mem_used">内存使用量 (Bytes)</option>
                      <option value="mem_pct">内存使用率 (%)</option>
                      <option value="load1">1分钟平均负载 (load1)</option>
                      <option value="load5">5分钟平均负载 (load5)</option>
                      <option value="load15">15分钟平均负载 (load15)</option>
                      <option value="disk_used">磁盘已用量 (Bytes)</option>
                      <option value="net_up_bps">上行带宽 (bps)</option>
                      <option value="net_down_bps">下行带宽 (bps)</option>
                      <option value="net.rtt_ms">Ping 握手延迟 (ms)</option>
                      <option value="net.loss_pct">Ping 丢包率 (%)</option>
                    </select>
                  </div>
                )}

                {/* 比较条件与阈值 */}
                <div className={styles.formRow}>
                  <div className={styles.formGroup}>
                    <label className={styles.label}>比较操作符</label>
                    <select
                      className={styles.select}
                      value={formCompareOp}
                      onChange={(e) =>
                        setFormCompareOp(e.target.value as 'gt' | 'gte' | 'lt' | 'lte')
                      }
                    >
                      <option value="gt">&gt; 大于</option>
                      <option value="gte">&ge; 大于等于</option>
                      <option value="lt">&lt; 小于</option>
                      <option value="lte">&le; 小于等于</option>
                    </select>
                  </div>

                  <div className={styles.formGroup}>
                    <label className={styles.label}>
                      {formKind === 'traffic'
                        ? '阈值比例 (如 0.8 表示 80%)'
                        : formKind === 'expiry'
                        ? '阈值 (剩余天数)'
                        : formKind === 'offline'
                        ? '阈值 (离线秒数)'
                        : '阈值'}
                    </label>
                    <input
                      type="number"
                      step="any"
                      className={styles.input}
                      value={formThreshold}
                      onChange={(e) => setFormThreshold(Number(e.target.value))}
                      required
                    />
                  </div>
                </div>

                {/* 去抖与静默期 */}
                <div className={styles.formRow}>
                  <div className={styles.formGroup}>
                    <label className={styles.label}>去抖持续时间 (秒)</label>
                    <input
                      type="number"
                      className={styles.input}
                      value={formDurationS}
                      onChange={(e) => setFormDurationS(Number(e.target.value))}
                      placeholder="0 表示立即触发"
                    />
                  </div>

                  <div className={styles.formGroup}>
                    <label className={styles.label}>静默周期 (秒)</label>
                    <input
                      type="number"
                      className={styles.input}
                      value={formSilenceS}
                      onChange={(e) => setFormSilenceS(Number(e.target.value))}
                      placeholder="触发后不重复通知周期"
                    />
                  </div>
                </div>

                {/* 作用域设置 */}
                <div className={styles.formRow}>
                  <div className={styles.formGroup}>
                    <label className={styles.label}>作用域类型</label>
                    <select
                      className={styles.select}
                      value={formScopeKind}
                      onChange={(e) =>
                        setFormScopeKind(e.target.value as 'all' | 'group' | 'tag' | 'node')
                      }
                    >
                      <option value="all">全部节点 (all)</option>
                      <option value="tag">指定标签 (tag)</option>
                      <option value="group">指定分组 (group)</option>
                      <option value="node">单个节点 (node)</option>
                    </select>
                  </div>

                  {formScopeKind !== 'all' && (
                    <div className={styles.formGroup}>
                      <label className={styles.label}>作用域标识 (ID 或标签名)</label>
                      <input
                        type="text"
                        className={styles.input}
                        value={formScopeRef}
                        onChange={(e) => setFormScopeRef(e.target.value)}
                        placeholder="例如：proxy 或 节点 ID"
                        required
                      />
                    </div>
                  )}
                </div>

                {/* 到期规则选项 */}
                {formKind === 'expiry' && (
                  <label className={styles.checkboxLabel}>
                    <input
                      type="checkbox"
                      checked={formIncludeAutoRenew}
                      onChange={(e) => setFormIncludeAutoRenew(e.target.checked)}
                    />
                    <span>包含标记为自动续费 (auto_renew) 的节点</span>
                  </label>
                )}

                {/* 绑定通知渠道 */}
                <div className={styles.formGroup}>
                  <label className={styles.label}>绑定通知渠道</label>
                  <div className={styles.channelBox}>
                    {channels.length === 0 ? (
                      <span style={{ color: 'var(--text-mute)', fontSize: '12px' }}>
                        暂未配置任何通知渠道，可前往系统设置添加
                      </span>
                    ) : (
                      channels.map((ch) => (
                        <label key={ch.id} className={styles.channelItem}>
                          <input
                            type="checkbox"
                            checked={formChannelIDs.includes(ch.id)}
                            onChange={() => handleToggleChannel(ch.id)}
                          />
                          <span>
                            {ch.name} ({ch.channel_kind})
                          </span>
                        </label>
                      ))
                    )}
                  </div>
                </div>
              </div>

              <div className={styles.modalFooter}>
                <button type="button" className="btn secondary" onClick={closeModal}>
                  取消
                </button>
                <button
                  type="submit"
                  className="btn primary"
                  disabled={createMutation.isPending || updateMutation.isPending}
                >
                  {createMutation.isPending || updateMutation.isPending
                    ? '保存中...'
                    : '保存规则'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  );
};
