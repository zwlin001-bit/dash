import React, { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  fetchGuardOverview,
  updateGuardRule,
  dryRunGuard,
  evaluateGuard,
  forceStartInstance,
  fetchGuardCycles,
  OverviewResponse,
  AccountOverview,
  InstanceOverview,
  GuardRule,
  EvaluateResult,
  RuleUpdateRequest,
} from '../../api';
import { TimeSeriesChart } from '../../components/TimeSeriesChart/TimeSeriesChart';
import { fetchCloudAccountMetrics } from '../../api/cloud';
import { TimeSeriesSpan } from '../../api/types';
import styles from './CloudGuard.module.css';

const AccountCDTChart: React.FC<{ accountId: string; limitGB?: number }> = ({ accountId, limitGB }) => {
  const [span, setSpan] = useState<TimeSeriesSpan>('60d');
  const { data, isLoading } = useQuery({
    queryKey: ['cloud-account-metrics', accountId, span],
    queryFn: () => fetchCloudAccountMetrics(accountId, { span, metric: 'traffic_month_up' }),
    refetchInterval: 60000,
  });

  const threshold =
    limitGB && limitGB > 0
      ? {
          value: limitGB * 1024 * 1024 * 1024,
          label: `关机阈值: ${limitGB} GB`,
        }
      : undefined;

  return (
    <div className={styles.cdtChartWrapper}>
      <TimeSeriesChart
        data={data}
        span={span}
        onSpanChange={setSpan}
        title="CDT 流量月度曲线"
        sourceBadge="云监控 · 5 分钟粒度"
        metrics={['traffic_month_up']}
        height={220}
        loading={isLoading}
        threshold={threshold}
      />
    </div>
  );
};

const TIMEZONES = [
  { label: 'Asia/Shanghai (UTC+8)', value: 'Asia/Shanghai' },
  { label: 'UTC (UTC+0)', value: 'UTC' },
  { label: 'Asia/Tokyo (UTC+9)', value: 'Asia/Tokyo' },
  { label: 'America/New_York (UTC-5/-4)', value: 'America/New_York' },
  { label: 'America/Los_Angeles (UTC-8/-7)', value: 'America/Los_Angeles' },
  { label: 'Europe/London (UTC+0/+1)', value: 'Europe/London' },
];

export const CloudGuard: React.FC = () => {
  const queryClient = useQueryClient();

  // Dry Run Modal State
  const [dryRunResult, setDryRunResult] = useState<EvaluateResult | null>(null);
  const [isDryRunning, setIsDryRunning] = useState<boolean>(false);

  // Force Start Modal State
  const [forceStartTarget, setForceStartTarget] = useState<{
    instance: InstanceOverview;
    account: AccountOverview;
  } | null>(null);
  const [forceReason, setForceReason] = useState<string>('');
  const [forceConfirmed, setForceConfirmed] = useState<boolean>(false);
  const [forceError, setForceError] = useState<string>('');

  // Editing Rule Local State: Map<resource_id, Partial<RuleUpdateRequest>>
  const [editForms, setEditForms] = useState<Record<string, RuleUpdateRequest>>({});

  // CDT Chart collapse state
  const [collapsedCharts, setCollapsedCharts] = useState<Record<string, boolean>>({});
  const toggleChart = (accId: string) => {
    setCollapsedCharts((prev) => ({ ...prev, [accId]: !prev[accId] }));
  };

  // Queries
  const { data: overview, isLoading, refetch: refetchOverview } = useQuery<OverviewResponse>({
    queryKey: ['guard-overview'],
    queryFn: fetchGuardOverview,
    refetchInterval: 10000,
  });

  const { data: cyclesData, refetch: refetchCycles } = useQuery({
    queryKey: ['guard-cycles'],
    queryFn: () => fetchGuardCycles(1, 10),
    refetchInterval: 15000,
  });

  // Mutations
  const updateRuleMutation = useMutation({
    mutationFn: ({ resId, req }: { resId: string; req: RuleUpdateRequest }) =>
      updateGuardRule(resId, req),
    onSuccess: (_, variables) => {
      queryClient.invalidateQueries({ queryKey: ['guard-overview'] });
      setEditForms((prev) => {
        const next = { ...prev };
        delete next[variables.resId];
        return next;
      });
    },
  });

  const evaluateMutation = useMutation({
    mutationFn: evaluateGuard,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['guard-overview'] });
      queryClient.invalidateQueries({ queryKey: ['guard-cycles'] });
    },
  });

  const forceStartMutation = useMutation({
    mutationFn: ({ resId, reason }: { resId: string; reason: string }) =>
      forceStartInstance(resId, { confirm: true, reason }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['guard-overview'] });
      setForceStartTarget(null);
      setForceReason('');
      setForceConfirmed(false);
      setForceError('');
    },
    onError: (err: any) => {
      setForceError(err.message || '强制开机失败');
    },
  });

  const handleDryRun = async () => {
    setIsDryRunning(true);
    try {
      const res = await dryRunGuard();
      setDryRunResult(res);
    } catch (err: any) {
      alert('演练失败: ' + (err.message || '未知错误'));
    } finally {
      setIsDryRunning(false);
    }
  };

  const getFormValue = (resId: string, rule?: GuardRule): RuleUpdateRequest => {
    const custom = editForms[resId] || {};
    return {
      is_enabled: custom.is_enabled ?? rule?.is_enabled ?? false,
      actions_enabled: custom.actions_enabled ?? rule?.actions_enabled ?? false,
      traffic_limit_gb: custom.traffic_limit_gb ?? rule?.traffic_limit_gb ?? 50,
      traffic_action: custom.traffic_action ?? rule?.traffic_action ?? 'stop',
      schedule_enabled: custom.schedule_enabled ?? rule?.schedule_enabled ?? false,
      schedule_start: custom.schedule_start ?? rule?.schedule_start ?? '08:30',
      schedule_stop: custom.schedule_stop ?? rule?.schedule_stop ?? '20:00',
      schedule_tz: custom.schedule_tz ?? rule?.schedule_tz ?? 'Asia/Shanghai',
    };
  };

  const handleFormChange = (resId: string, patch: Partial<RuleUpdateRequest>) => {
    setEditForms((prev) => ({
      ...prev,
      [resId]: {
        ...(prev[resId] || {}),
        ...patch,
      },
    }));
  };

  const handleSaveRule = (resId: string, rule?: GuardRule) => {
    const req = getFormValue(resId, rule);
    updateRuleMutation.mutate({ resId, req });
  };

  const formatScheduleNext = (inst: InstanceOverview): string => {
    if (!inst.next_schedule_action || !inst.next_schedule_time_ms) {
      return '--';
    }
    const d = new Date(inst.next_schedule_time_ms);
    const timeStr = d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
    const actionLabel = inst.next_schedule_action === 'stop' ? '计划关机' : '计划开机';
    return `${actionLabel}: ${timeStr}`;
  };

  return (
    <div className={styles.container}>
      {/* 顶部标题与操作栏 */}
      <div className={styles.header}>
        <div className={styles.titleArea}>
          <div className={styles.title}>
            <span>🛡️</span>
            <span>ECS 保活与 CDT 流量守卫</span>
          </div>
          <div className={styles.subtitle}>
            跨地域出网流量自动监控，超额停机保护与按计划开关机
          </div>
        </div>

        <div className={styles.actionButtons}>
          <button
            type="button"
            className="btn"
            onClick={handleDryRun}
            disabled={isDryRunning}
            title="模拟执行一轮评估，输出动作清单，不改变云上任何状态"
          >
            {isDryRunning ? '演练中...' : '🔍 演练一次 (Dry Run)'}
          </button>
          <button
            type="button"
            className="btn"
            onClick={() => evaluateMutation.mutate()}
            disabled={evaluateMutation.isPending}
            title="立即触发后台守护周期评估"
          >
            {evaluateMutation.isPending ? '评估中...' : '⚡ 立即评估'}
          </button>
          <button
            type="button"
            className="btn ghost"
            onClick={() => {
              refetchOverview();
              refetchCycles();
            }}
            title="刷新页面数据"
          >
            🔄 刷新
          </button>
        </div>
      </div>

      {/* CDT 账号级共享说明 Banner */}
      <div className={styles.infoBanner}>
        <span className={styles.infoBannerIcon}>ℹ️</span>
        <div>
          <strong>CDT 共享额度机制：</strong>
          阿里云 CDT（云数据传输）每月提供 200GB 跨地域及出网免费额度。此额度属于<strong>账号级共享</strong>，同一账号下无论北京、杭州或香港的所有 ECS 实例共同消耗这一个额度计数。
          任一实例触发超限阈值将对开启守卫的对应实例执行保全停机。
        </div>
      </div>

      {isLoading && (
        <div style={{ textAlign: 'center', padding: '32px 0', color: 'var(--text-dim)' }}>
          正在加载守卫规则与云资产数据...
        </div>
      )}

      {/* 账号列表与实例网格 */}
      {overview?.accounts.map((account) => {
        const cdtGB = account.cdt_used_gb;
        const limitGB = account.traffic_limit_gb;
        const percent = account.usage_percent;

        let progressClass = styles.progressOk;
        if (percent >= 100) {
          progressClass = styles.progressErr;
        } else if (percent >= 80) {
          progressClass = styles.progressWarn;
        }

        return (
          <div key={account.account_id} className={styles.accountCard}>
            <div className={styles.accountHeader}>
              <div className={styles.accountName}>
                <span>☁️ {account.account_name}</span>
                <span className={styles.providerTag}>{account.provider_code}</span>
              </div>
              <div className="text-dim" style={{ fontSize: '12px' }}>
                默认地域: {account.default_region || 'cn-hangzhou'} | 实例数: {account.instances.length}
              </div>
            </div>

            {/* CDT 错误提示 */}
            {account.cdt_error && (
              <div className={styles.errorBanner}>
                ⚠️ CDT 流量查询失败: {account.cdt_error}（系统已进入安全隔离模式，暂停所有自动开机）
              </div>
            )}

            {/* 账号级 CDT 流量条 */}
            <div className={styles.trafficBarArea}>
              <div className={styles.trafficMeta}>
                <span>
                  本月 CDT 累计出网流量:
                  <strong className={styles.trafficValue} style={{ marginLeft: '6px' }}>
                    {cdtGB !== undefined && cdtGB !== null ? `${cdtGB.toFixed(2)} GB` : '--'}
                  </strong>
                  {' / '}
                  <span>{limitGB !== undefined && limitGB !== null ? `${limitGB.toFixed(0)} GB (阈值)` : '--'}</span>
                </span>
                <span style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-3)' }}>
                  <span style={{ fontWeight: 600 }}>
                    {percent !== undefined && percent !== null ? `${percent.toFixed(1)}%` : '--'}
                  </span>
                  <button
                    type="button"
                    className={styles.cdtToggleBtn}
                    onClick={() => toggleChart(account.account_id)}
                  >
                    {collapsedCharts[account.account_id] ? '展开趋势图 ▼' : '收起趋势图 ▲'}
                  </button>
                </span>
              </div>

              <div className={styles.progressBarContainer}>
                <div
                  className={`${styles.progressBar} ${progressClass}`}
                  style={{ width: `${Math.min(Math.max(percent || 0, 0), 100)}%` }}
                />
              </div>

              {/* 账号级 CDT 流量月度曲线 + 阈值横线 */}
              {!collapsedCharts[account.account_id] && (
                <AccountCDTChart accountId={account.account_id} limitGB={limitGB} />
              )}
            </div>

            {/* 该账号下的 ECS 实例卡片 */}
            <div className={styles.instancesGrid}>
              {account.instances.map((inst) => {
                const rule = inst.rule;
                const form = getFormValue(inst.resource_id, rule);
                const hasDirty = !!editForms[inst.resource_id];

                let statusBadgeClass = styles.statusStopped;
                if (inst.status === 'Running') {
                  statusBadgeClass = styles.statusRunning;
                } else if (inst.status === 'Starting' || inst.status === 'Stopping') {
                  statusBadgeClass = styles.statusTransitional;
                }

                return (
                  <div key={inst.resource_id} className={styles.instanceCard}>
                    {/* 实例头部 */}
                    <div className={styles.instanceHeader}>
                      <div className={styles.instanceTitleArea}>
                        <div className={styles.instanceName}>{inst.resource_name || inst.resource_ref}</div>
                        <div className={styles.instanceRef}>{inst.resource_ref}</div>
                      </div>
                      <span className={`${styles.statusBadge} ${statusBadgeClass}`}>
                        ● {inst.status || '--'}
                      </span>
                    </div>

                    {/* 网络与元属性 */}
                    <div className={styles.propsGrid}>
                      <div className={styles.propItem}>
                        <span className={styles.propLabel}>地域</span>
                        <span className={styles.propValue}>{inst.region || '--'}</span>
                      </div>
                      <div className={styles.propItem}>
                        <span className={styles.propLabel}>本月账单</span>
                        <span className={styles.propValue}>{inst.billing_info || '--'}</span>
                      </div>
                      <div className={styles.propItem}>
                        <span className={styles.propLabel}>公网 IP</span>
                        <span className={styles.propValue}>
                          {inst.public_ips?.length > 0 ? inst.public_ips.join(', ') : '--'}
                        </span>
                      </div>
                      <div className={styles.propItem}>
                        <span className={styles.propLabel}>内网 IP</span>
                        <span className={styles.propValue}>
                          {inst.private_ips?.length > 0 ? inst.private_ips.join(', ') : '--'}
                        </span>
                      </div>
                      <div className={styles.propItem}>
                        <span className={styles.propLabel}>下一次日程事件</span>
                        <span className={styles.propValue}>{formatScheduleNext(inst)}</span>
                      </div>
                      <div className={styles.propItem}>
                        <span className={styles.propLabel}>最近执行动作</span>
                        <span className={styles.propValue}>{rule?.last_action || '--'}</span>
                      </div>
                    </div>

                    {/* 守卫规则表单 */}
                    <div className={styles.ruleForm}>
                      {/* 启用守卫总开关 */}
                      <div className={styles.switchRow}>
                        <div className={styles.switchLabelArea}>
                          <strong>启用流量与保活守卫</strong>
                          <span className={styles.switchDesc}>按限额监控并参与守护调度</span>
                        </div>
                        <input
                          type="checkbox"
                          checked={form.is_enabled}
                          onChange={(e) =>
                            handleFormChange(inst.resource_id, { is_enabled: e.target.checked })
                          }
                        />
                      </div>

                      {/* 执行动作开关 (安全关键) */}
                      <div className={styles.switchRow}>
                        <div className={styles.switchLabelArea}>
                          <strong style={{ color: form.actions_enabled ? 'var(--warn)' : 'var(--text-main)' }}>
                            允许执行云上动作 (Actions Enabled)
                          </strong>
                          <span className={styles.switchDesc}>
                            {form.actions_enabled
                              ? '已授权自动开停机！到达条件将实际操作云服务器'
                              : '新建默认关闭；关闭时仅发送通知事件，不动真实机器'}
                          </span>
                        </div>
                        <input
                          type="checkbox"
                          checked={form.actions_enabled}
                          onChange={(e) =>
                            handleFormChange(inst.resource_id, { actions_enabled: e.target.checked })
                          }
                        />
                      </div>

                      {/* 流量阈值 */}
                      <div className={styles.fieldRow}>
                        <span>流量限额阈值 (GB):</span>
                        <input
                          type="number"
                          className={styles.input}
                          style={{ width: '90px' }}
                          min="1"
                          max="200"
                          value={form.traffic_limit_gb ?? 50}
                          onChange={(e) =>
                            handleFormChange(inst.resource_id, {
                              traffic_limit_gb: parseFloat(e.target.value) || 50,
                            })
                          }
                        />
                      </div>

                      {/* 每日开关机日程 */}
                      <div className={styles.scheduleBox}>
                        <div className={styles.switchRow}>
                          <div className={styles.switchLabelArea}>
                            <strong>每日开关机日程</strong>
                            <span className={styles.switchDesc}>按预定时间窗口自动开机与关机</span>
                          </div>
                          <input
                            type="checkbox"
                            checked={form.schedule_enabled}
                            onChange={(e) =>
                              handleFormChange(inst.resource_id, {
                                schedule_enabled: e.target.checked,
                              })
                            }
                          />
                        </div>

                        {form.schedule_enabled && (
                          <>
                            <div className={styles.scheduleInputs}>
                              <div style={{ display: 'flex', flexDirection: 'column', gap: '2px', flex: 1 }}>
                                <span className={styles.propLabel}>开机时间</span>
                                <input
                                  type="time"
                                  className={styles.input}
                                  value={form.schedule_start || '08:30'}
                                  onChange={(e) =>
                                    handleFormChange(inst.resource_id, {
                                      schedule_start: e.target.value,
                                    })
                                  }
                                />
                              </div>
                              <div style={{ display: 'flex', flexDirection: 'column', gap: '2px', flex: 1 }}>
                                <span className={styles.propLabel}>关机时间</span>
                                <input
                                  type="time"
                                  className={styles.input}
                                  value={form.schedule_stop || '20:00'}
                                  onChange={(e) =>
                                    handleFormChange(inst.resource_id, {
                                      schedule_stop: e.target.value,
                                    })
                                  }
                                />
                              </div>
                            </div>

                            <div style={{ display: 'flex', flexDirection: 'column', gap: '2px' }}>
                              <span className={styles.propLabel}>时区</span>
                              <select
                                className={styles.select}
                                value={form.schedule_tz || 'Asia/Shanghai'}
                                onChange={(e) =>
                                  handleFormChange(inst.resource_id, {
                                    schedule_tz: e.target.value,
                                  })
                                }
                              >
                                {TIMEZONES.map((tz) => (
                                  <option key={tz.value} value={tz.value}>
                                    {tz.label}
                                  </option>
                                ))}
                              </select>
                            </div>
                            <span className={styles.switchDesc} style={{ fontStyle: 'italic', marginTop: '2px' }}>
                              💡 提示：关闭日程开关不会立刻改变当前运行状态，只是不再受时间约束。
                            </span>
                          </>
                        )}
                      </div>
                    </div>

                    {/* 卡片底栏操作 */}
                    <div className={styles.cardFooter}>
                      {inst.status === 'Stopped' ? (
                        <button
                          type="button"
                          className="btn mini"
                          style={{ borderColor: 'var(--warn-border)', color: 'var(--warn)' }}
                          onClick={() => setForceStartTarget({ instance: inst, account })}
                        >
                          ⚡ 强制启动
                        </button>
                      ) : (
                        <span />
                      )}

                      <button
                        type="button"
                        className="btn mini primary"
                        disabled={!hasDirty || updateRuleMutation.isPending}
                        onClick={() => handleSaveRule(inst.resource_id, rule)}
                      >
                        {updateRuleMutation.isPending ? '保存中...' : '保存配置'}
                      </button>
                    </div>
                  </div>
                );
              })}
            </div>
          </div>
        );
      })}

      {/* 最近周期评估列表 */}
      <div className={styles.cyclesSection}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <div style={{ fontSize: '14px', fontWeight: 600 }}>⏱️ 最近评估周期</div>
          <span className="text-dim" style={{ fontSize: '12px' }}>
            每分钟自动运行一轮
          </span>
        </div>

        <div className={styles.tableWrapper}>
          <table className={styles.table}>
            <thead>
              <tr>
                <th>周期 ID</th>
                <th>开始时间</th>
                <th>耗时</th>
                <th>评估实例数</th>
                <th>执行动作</th>
                <th>失败数</th>
                <th>CDT 读数</th>
              </tr>
            </thead>
            <tbody>
              {cyclesData?.items?.length ? (
                cyclesData.items.map((c) => (
                  <tr key={c.id}>
                    <td className="cell-mono">{c.id.slice(0, 10)}...</td>
                    <td>{new Date(c.started_at_ms).toLocaleTimeString()}</td>
                    <td className="cell-mono">{c.duration_ms} ms</td>
                    <td className="cell-mono">{c.evaluated}</td>
                    <td className="cell-mono" style={{ color: c.acted > 0 ? 'var(--ok)' : undefined }}>
                      {c.acted}
                    </td>
                    <td className="cell-mono" style={{ color: c.failed > 0 ? 'var(--err)' : undefined }}>
                      {c.failed}
                    </td>
                    <td>
                      {c.cdt_error ? (
                        <span style={{ color: 'var(--err)' }}>失败: {c.cdt_error}</span>
                      ) : c.cdt_used_gb !== undefined && c.cdt_used_gb !== null ? (
                        `${c.cdt_used_gb.toFixed(2)} GB`
                      ) : (
                        '--'
                      )}
                    </td>
                  </tr>
                ))
              ) : (
                <tr>
                  <td colSpan={7} style={{ textAlign: 'center', color: 'var(--text-dim)', padding: '16px' }}>
                    暂无评估记录
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* 演练模式 Modal */}
      {dryRunResult && (
        <div className={styles.modalOverlay} onClick={() => setDryRunResult(null)}>
          <div className={styles.modalContent} onClick={(e) => e.stopPropagation()}>
            <div className={styles.modalHeader}>
              <div className={styles.modalTitle}>
                <span>🔍 守卫演练 (Dry Run) 结果</span>
                <span className="badge" style={{ background: 'var(--ok-bg)', color: 'var(--ok)' }}>
                  云上状态零变化
                </span>
              </div>
              <button type="button" className="btn mini ghost" onClick={() => setDryRunResult(null)}>
                ✕
              </button>
            </div>

            <div className={styles.modalBody}>
              <div className={styles.summaryCards}>
                <div className={styles.summaryCard}>
                  <div className={styles.summaryNum}>{dryRunResult.evaluated_count}</div>
                  <div className={styles.summaryLabel}>评估实例</div>
                </div>
                <div className={styles.summaryCard}>
                  <div className={styles.summaryNum} style={{ color: 'var(--err)' }}>
                    {dryRunResult.items.filter((i) => i.proposed_action === 'stop').length}
                  </div>
                  <div className={styles.summaryLabel}>拟关机</div>
                </div>
                <div className={styles.summaryCard}>
                  <div className={styles.summaryNum} style={{ color: 'var(--ok)' }}>
                    {dryRunResult.items.filter((i) => i.proposed_action === 'start').length}
                  </div>
                  <div className={styles.summaryLabel}>拟开机</div>
                </div>
                <div className={styles.summaryCard}>
                  <div className={styles.summaryNum}>
                    {dryRunResult.items.filter((i) => i.proposed_action === 'noop').length}
                  </div>
                  <div className={styles.summaryLabel}>保持不变</div>
                </div>
              </div>

              <div className={styles.tableWrapper}>
                <table className={styles.table}>
                  <thead>
                    <tr>
                      <th>实例</th>
                      <th>地域</th>
                      <th>当前状态</th>
                      <th>拟执行动作</th>
                      <th>判定原因</th>
                      <th>执行动作</th>
                    </tr>
                  </thead>
                  <tbody>
                    {dryRunResult.items.map((item) => {
                      let tagClass = styles.actionTagNoop;
                      let actionText = '保持';
                      if (item.proposed_action === 'stop') {
                        tagClass = styles.actionTagStop;
                        actionText = '关机';
                      } else if (item.proposed_action === 'start') {
                        tagClass = styles.actionTagStart;
                        actionText = '开机';
                      }

                      return (
                        <tr key={item.resource_id}>
                          <td>
                            <strong>{item.resource_name}</strong>
                            <div className="text-dim" style={{ fontSize: '11px' }}>
                              {item.resource_ref}
                            </div>
                          </td>
                          <td>{item.region}</td>
                          <td>{item.current_status}</td>
                          <td className={tagClass}>{actionText}</td>
                          <td style={{ fontSize: '12px' }}>{item.reason}</td>
                          <td className="text-dim">否 (演练模式)</td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            </div>

            <div className={styles.modalFooter}>
              <button type="button" className="btn" onClick={() => setDryRunResult(null)}>
                关闭
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 手动强制开机二次确认 Modal */}
      {forceStartTarget && (
        <div className={styles.modalOverlay} onClick={() => setForceStartTarget(null)}>
          <div className={styles.modalContent} onClick={(e) => e.stopPropagation()} style={{ maxWidth: '520px' }}>
            <div className={styles.modalHeader}>
              <div className={styles.modalTitle} style={{ color: 'var(--warn)' }}>
                <span>⚠️ 二次确认：手动强制启动 ECS</span>
              </div>
              <button type="button" className="btn mini ghost" onClick={() => setForceStartTarget(null)}>
                ✕
              </button>
            </div>

            <div className={styles.modalBody}>
              <div className={styles.confirmBox}>
                <div>
                  即将强制启动实例: <strong>{forceStartTarget.instance.resource_name}</strong> (
                  {forceStartTarget.instance.resource_ref})
                </div>
                <div>
                  当前账号 CDT 出网已用:
                  <strong> {forceStartTarget.account.cdt_used_gb?.toFixed(2) ?? '--'} GB</strong>
                  {' / '}
                  限额阈值:
                  <strong> {forceStartTarget.instance.rule?.traffic_limit_gb ?? '--'} GB</strong>
                </div>
                <div style={{ color: 'var(--err)', marginTop: '4px', fontWeight: 500 }}>
                  注意：该账号流量可能已达到或超过阈值。强制拉起后实例将继续消耗出网流量，可能产生按量计费账单！此操作将完整记录至系统审计日志
                  (audit_log)。
                </div>
              </div>

              <div style={{ display: 'flex', flexDirection: 'column', gap: '6px' }}>
                <label style={{ fontSize: '12px', fontWeight: 500 }}>
                  启动原因 / 运维备注 <span style={{ color: 'var(--err)' }}>*</span>
                </label>
                <input
                  type="text"
                  className={styles.input}
                  placeholder="例如: 紧急排查业务问题，已知晓超额风险"
                  value={forceReason}
                  onChange={(e) => setForceReason(e.target.value)}
                />
              </div>

              <div className={styles.confirmRow}>
                <input
                  type="checkbox"
                  id="confirmForce"
                  checked={forceConfirmed}
                  onChange={(e) => setForceConfirmed(e.target.checked)}
                />
                <label htmlFor="confirmForce" style={{ fontSize: '13px', cursor: 'pointer' }}>
                  我已知晓超额与计费风险，确认强制启动该实例
                </label>
              </div>

              {forceError && (
                <div style={{ color: 'var(--err)', fontSize: '12px' }}>
                  {forceError}
                </div>
              )}
            </div>

            <div className={styles.modalFooter}>
              <button type="button" className="btn ghost" onClick={() => setForceStartTarget(null)}>
                取消
              </button>
              <button
                type="button"
                className="btn primary"
                style={{ background: 'var(--err)', borderColor: 'var(--err)', color: 'var(--on-accent)' }}
                disabled={!forceConfirmed || !forceReason.trim() || forceStartMutation.isPending}
                onClick={() =>
                  forceStartMutation.mutate({
                    resId: forceStartTarget.instance.resource_id,
                    reason: forceReason.trim(),
                  })
                }
              >
                {forceStartMutation.isPending ? '提交中...' : '确认强制启动'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
