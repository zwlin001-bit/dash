import React, { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  fetchGuardOverview,
  updateGuardRule,
  updateAccountPolicy,
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
  AccountPolicyUpdateRequest,
} from '../../api';
import { TimeSeriesChart } from '../../components/TimeSeriesChart/TimeSeriesChart';
import { Sparkline } from '../../components/Sparkline/Sparkline';
import { fetchCloudAccountMetrics, fetchCloudResourceMetrics } from '../../api/cloud';
import { TimeSeriesSpan } from '../../api/types';
import styles from './CloudGuard.module.css';

function formatBytes(bytes: number | null | undefined): string {
  if (bytes === null || bytes === undefined || isNaN(bytes)) return '--';
  if (bytes >= 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GB`;
  if (bytes >= 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  return `${Math.round(bytes)} B`;
}

function formatRate(bps: number | null | undefined): string {
  if (bps === null || bps === undefined || isNaN(bps)) return '--';
  if (bps >= 1024 * 1024) return `${(bps / (1024 * 1024)).toFixed(2)} MB/s`;
  if (bps >= 1024) return `${(bps / 1024).toFixed(1)} KB/s`;
  return `${Math.round(bps)} B/s`;
}

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

interface InstanceCardProps {
  inst: InstanceOverview;
  account: AccountOverview;
  form: RuleUpdateRequest;
  onFormChange: (patch: Partial<RuleUpdateRequest>) => void;
  onSaveRule: () => void;
  hasDirty: boolean;
  isSaving: boolean;
  onForceStart: () => void;
  formatScheduleNext: (inst: InstanceOverview) => string;
}

const InstanceCard: React.FC<InstanceCardProps> = ({
  inst,
  account,
  form,
  onFormChange,
  onSaveRule,
  hasDirty,
  isSaving,
  onForceStart,
  formatScheduleNext,
}) => {
  const [showRuleEdit, setShowRuleEdit] = useState(false);
  const { data: cloudMetrics } = useQuery({
    queryKey: ['cloud-resource-metrics', inst.resource_id],
    queryFn: () => fetchCloudResourceMetrics(inst.resource_id, { span: '6h' }),
    refetchInterval: 30000,
  });

  const rule = inst.rule;
  const accountActionsDisabled = account.policy ? !account.policy.actions_enabled : false;
  let statusBadgeClass = styles.statusStopped;
  if (inst.status === 'Running') {
    statusBadgeClass = styles.statusRunning;
  } else if (inst.status === 'Starting' || inst.status === 'Stopping') {
    statusBadgeClass = styles.statusTransitional;
  }

  // 提取最新指标值 (5 列指标格)
  const getLatest = (series?: (number | null)[]) => {
    if (!series || series.length === 0) return null;
    for (let i = series.length - 1; i >= 0; i--) {
      if (series[i] !== null && series[i] !== undefined && !isNaN(series[i]!)) {
        return series[i];
      }
    }
    return null;
  };

  const latestCpu = getLatest(cloudMetrics?.series?.cpu_pct);
  const latestMem = getLatest(cloudMetrics?.series?.mem_used);
  const latestUp = getLatest(cloudMetrics?.series?.net_up_bps);
  const latestDown = getLatest(cloudMetrics?.series?.net_down_bps);

  // 流量进度条
  const limitGB = rule?.traffic_limit_gb ?? account.traffic_limit_gb ?? 50;
  const cdtUsedGB = account.cdt_used_gb ?? 0;
  const usagePercent = limitGB > 0 ? (cdtUsedGB / limitGB) * 100 : 0;

  let progressClass = styles.progressOk;
  if (usagePercent >= 100) {
    progressClass = styles.progressErr;
  } else if (usagePercent >= 80) {
    progressClass = styles.progressWarn;
  }

  // Sparkline 数据
  const sparkPoints = cloudMetrics?.series?.net_up_bps || [];
  const sparkTs = cloudMetrics?.ts_ms || [];

  return (
    <div className={styles.instanceCard}>
      {/* 1. card-head */}
      <div className={styles.cardHead}>
        <div className={styles.cardHeadLeft}>
          <div className={styles.instanceName}>{inst.resource_name || inst.resource_ref}</div>
          <div className={styles.instanceSub}>
            {inst.region} · {inst.public_ips?.[0] || inst.private_ips?.[0] || '无公网IP'}
          </div>
        </div>
        <div className={styles.cardHeadRight}>
          <span className={`${styles.statusBadge} ${statusBadgeClass}`}>
            ● {inst.status || '--'}
          </span>
          {inst.status === 'Stopped' && (
            <button
              type="button"
              className="btn mini warning"
              onClick={onForceStart}
              title="解除保全锁定，强制开机"
            >
              强制开机
            </button>
          )}
          <button
            type="button"
            className={styles.ruleToggleBtn}
            onClick={() => setShowRuleEdit((v) => !v)}
            title="展开/收起守卫规则配置"
          >
            {showRuleEdit ? '收起配置 ▲' : '⚙️ 规则配置 ▼'}
          </button>
        </div>
      </div>

      {/* 2. metric-grid (5 列小指标格) */}
      <div className={styles.metricGrid}>
        <div className={styles.metricCell}>
          <span className={styles.metricLabel}>CPU</span>
          <span className={styles.metricValue}>
            {latestCpu !== null ? `${latestCpu.toFixed(1)}%` : '--'}
          </span>
        </div>
        <div className={styles.metricCell}>
          <span className={styles.metricLabel}>内存</span>
          <span className={styles.metricValue}>
            {latestMem !== null ? formatBytes(latestMem) : '--'}
          </span>
        </div>
        <div className={styles.metricCell}>
          <span className={styles.metricLabel}>磁盘</span>
          <span className={styles.metricValue}>--</span>
        </div>
        <div className={styles.metricCell}>
          <span className={styles.metricLabel}>上行</span>
          <span className={styles.metricValue}>
            {latestUp !== null ? formatRate(latestUp) : '--'}
          </span>
        </div>
        <div className={styles.metricCell}>
          <span className={styles.metricLabel}>下行</span>
          <span className={styles.metricValue}>
            {latestDown !== null ? formatRate(latestDown) : '--'}
          </span>
        </div>
      </div>

      {/* 3. traffic-row + progress */}
      <div className={styles.trafficRow}>
        <span style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-2)' }}>
          出网流量 / 阈值
          {inst.limit_origin === 'account' ? (
            <span className={styles.originBadgeInherit} title="继承自账号级阈值">继承账号</span>
          ) : (
            <span className={styles.originBadgeOverride} title="设备独立覆盖阈值">设备覆盖</span>
          )}
        </span>
        <span className={styles.trafficRowValue}>
          {cdtUsedGB.toFixed(2)} GB / {limitGB.toFixed(0)} GB ({usagePercent.toFixed(1)}%)
        </span>
      </div>
      <div className={styles.progress}>
        <div
          className={`${styles.progressBar} ${progressClass}`}
          style={{ width: `${Math.min(Math.max(usagePercent, 0), 100)}%` }}
        />
      </div>

      {/* 4. sparkline (内嵌 SVG 曲线，5 项细节齐全) */}
      <Sparkline
        points={sparkPoints}
        timestamps={sparkTs}
        unit="bytes/s"
        metricLabel="上行出网速率"
        emptyText="暂无云监控采样数据（停机或尚未产生采样）"
      />

      {/* 5. next-line */}
      <div className={styles.nextLine}>
        <span style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-2)' }}>
          ⏱️ 下一次日程:
          {inst.schedule_origin === 'account' ? (
            <span className={styles.originBadgeInherit} title="继承自账号级日程">继承账号</span>
          ) : (
            <span className={styles.originBadgeOverride} title="设备独立日程">设备覆盖</span>
          )}
        </span>
        <span style={{ fontFamily: 'var(--font-mono)' }}>{formatScheduleNext(inst)}</span>
      </div>

      {/* 6. result-line */}
      <div className={styles.resultLine}>
        <span>📋 上轮评估:</span>
        <span>{rule?.last_action ? `${rule.last_action} (最近执行)` : '正常监控中，未触发动作'}</span>
      </div>

      {/* 可展开守卫配置区域 */}
      {showRuleEdit && (
        <div className={styles.ruleForm} style={{ marginTop: 'var(--sp-2)' }}>
          {/* 继承账号策略开关 (跟随账号 / 自定义) */}
          <div className={styles.switchRow}>
            <div className={styles.switchLabelArea}>
              <strong>策略继承模式</strong>
              <span className={styles.switchDesc}>
                {form.inherit_account
                  ? '跟随账号策略（未覆盖字段继承账号设置）'
                  : '设备自定义模式（独立维护配置）'}
              </span>
            </div>
            <label style={{ display: 'inline-flex', alignItems: 'center', gap: 'var(--sp-2)', cursor: 'pointer' }}>
              <input
                type="checkbox"
                checked={form.inherit_account ?? true}
                onChange={(e) => onFormChange({ inherit_account: e.target.checked })}
              />
              <span style={{ fontSize: '12px', color: 'var(--text-main)' }}>
                {form.inherit_account ? '跟随账号' : '自定义'}
              </span>
            </label>
          </div>

          {/* 启用守卫总开关 */}
          <div className={styles.switchRow}>
            <div className={styles.switchLabelArea}>
              <strong>启用流量与保活守卫</strong>
              <span className={styles.switchDesc}>按限额监控并参与守护调度</span>
            </div>
            <input
              type="checkbox"
              checked={form.is_enabled}
              onChange={(e) => onFormChange({ is_enabled: e.target.checked })}
            />
          </div>

          {/* 执行动作开关 (安全关键，受账号级 actions_enabled 双重锁保护) */}
          <div className={`${styles.switchRow} ${accountActionsDisabled ? styles.fieldDisabled : ''}`}>
            <div className={styles.switchLabelArea}>
              <strong style={{ color: form.actions_enabled && !accountActionsDisabled ? 'var(--warn)' : 'var(--text-main)' }}>
                允许执行云上动作 (Actions Enabled)
              </strong>
              <span className={styles.switchDesc}>
                {accountActionsDisabled
                  ? '账号级总闸已关闭，处于仅监控模式，所有设备动作禁止执行'
                  : form.actions_enabled
                  ? '已授权自动开停机！到达条件将实际操作云服务器'
                  : '新建默认关闭；关闭时仅发送通知事件，不动真实机器'}
              </span>
              {accountActionsDisabled && (
                <span className={styles.disabledReason}>
                  ⚠️ 账号级动作总闸已锁定：必须先在账号设置中开启允许动作
                </span>
              )}
            </div>
            <input
              type="checkbox"
              checked={form.actions_enabled}
              disabled={accountActionsDisabled}
              onChange={(e) => onFormChange({ actions_enabled: e.target.checked })}
            />
          </div>

          {/* 流量阈值 */}
          <div className={styles.fieldRow}>
            <span>
              流量限额阈值 (GB):
              {form.inherit_account && form.traffic_limit_gb === undefined && (
                <span className={styles.originBadgeInherit} style={{ marginLeft: '6px' }}>继承账号</span>
              )}
            </span>
            <input
              type="number"
              className={styles.input}
              style={{ width: '90px' }}
              min="1"
              max="200"
              value={form.traffic_limit_gb ?? 50}
              onChange={(e) =>
                onFormChange({
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
                  onFormChange({
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
                        onFormChange({
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
                        onFormChange({
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
                      onFormChange({
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

          <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 'var(--sp-2)' }}>
            <button
              type="button"
              className="btn mini primary"
              disabled={!hasDirty || isSaving}
              onClick={onSaveRule}
            >
              {isSaving ? '保存中...' : '保存配置'}
            </button>
          </div>
        </div>
      )}
    </div>
  );
};

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

  // Editing Account Policy Local State: Map<account_id, Partial<AccountPolicyUpdateRequest>>
  const [accountPolicyForms, setAccountPolicyForms] = useState<Record<string, AccountPolicyUpdateRequest>>({});
  const [expandedAccountPolicies, setExpandedAccountPolicies] = useState<Record<string, boolean>>({});

  // CDT Chart collapse state
  const [collapsedCharts, setCollapsedCharts] = useState<Record<string, boolean>>({});
  const toggleChart = (accId: string) => {
    setCollapsedCharts((prev) => ({ ...prev, [accId]: !prev[accId] }));
  };

  const toggleAccountPolicy = (accId: string) => {
    setExpandedAccountPolicies((prev) => ({ ...prev, [accId]: !prev[accId] }));
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

  const updateAccountPolicyMutation = useMutation({
    mutationFn: ({ accountId, req }: { accountId: string; req: AccountPolicyUpdateRequest }) =>
      updateAccountPolicy(accountId, req),
    onSuccess: (_, variables) => {
      queryClient.invalidateQueries({ queryKey: ['guard-overview'] });
      setAccountPolicyForms((prev) => {
        const next = { ...prev };
        delete next[variables.accountId];
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
      inherit_account: custom.inherit_account ?? rule?.inherit_account ?? true,
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

  const getAccountPolicyFormValue = (acc: AccountOverview): AccountPolicyUpdateRequest => {
    const custom = accountPolicyForms[acc.account_id] || {};
    const policy = acc.policy;
    return {
      is_enabled: custom.is_enabled ?? policy?.is_enabled ?? true,
      actions_enabled: custom.actions_enabled ?? policy?.actions_enabled ?? false,
      traffic_limit_gb: custom.traffic_limit_gb ?? policy?.traffic_limit_gb ?? acc.traffic_limit_gb ?? 50,
      traffic_action: custom.traffic_action ?? policy?.traffic_action ?? 'stop',
      schedule_enabled: custom.schedule_enabled ?? policy?.schedule_enabled ?? false,
      schedule_start: custom.schedule_start ?? policy?.schedule_start ?? '08:30',
      schedule_stop: custom.schedule_stop ?? policy?.schedule_stop ?? '20:00',
      schedule_tz: custom.schedule_tz ?? policy?.schedule_tz ?? 'Asia/Shanghai',
    };
  };

  const handleAccountPolicyFormChange = (accountId: string, patch: Partial<AccountPolicyUpdateRequest>) => {
    setAccountPolicyForms((prev) => ({
      ...prev,
      [accountId]: {
        ...(prev[accountId] || {}),
        ...patch,
      },
    }));
  };

  const handleSaveAccountPolicy = (acc: AccountOverview) => {
    const req = getAccountPolicyFormValue(acc);
    updateAccountPolicyMutation.mutate({ accountId: acc.account_id, req });
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

  // 顶部指标带统计
  const totalCdtGB = overview?.accounts.reduce((sum, a) => sum + (a.cdt_used_gb || 0), 0) ?? 0;
  const totalLimitGB = overview?.accounts.reduce((sum, a) => sum + (a.traffic_limit_gb || 0), 0) ?? 0;
  const remainingGB = Math.max(0, totalLimitGB - totalCdtGB);
  let runningCount = 0;
  let totalInstances = 0;
  overview?.accounts.forEach((a) => {
    totalInstances += a.instances.length;
    runningCount += a.instances.filter((inst) => inst.status === 'Running').length;
  });

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

      {/* ① 顶部 4 列指标带 (aliyun-guard 风格，等宽大号数值) */}
      <div className={styles.statBand}>
        <div className={styles.statCard}>
          <div className={styles.statCardHeader}>
            <span className={styles.statIcon}>🌐</span>
            <span className={styles.statLabel}>账号本月 CDT 用量</span>
          </div>
          <div className={styles.statValue}>{totalCdtGB.toFixed(2)} GB</div>
        </div>
        <div className={styles.statCard}>
          <div className={styles.statCardHeader}>
            <span className={styles.statIcon}>🎯</span>
            <span className={styles.statLabel}>阈值余量</span>
          </div>
          <div className={styles.statValue}>{totalLimitGB > 0 ? `${remainingGB.toFixed(2)} GB` : '--'}</div>
        </div>
        <div className={styles.statCard}>
          <div className={styles.statCardHeader}>
            <span className={styles.statIcon}>⚡</span>
            <span className={styles.statLabel}>在线实例数</span>
          </div>
          <div className={styles.statValue}>{runningCount} / {totalInstances} 台</div>
        </div>
        <div className={styles.statCard}>
          <div className={styles.statCardHeader}>
            <span className={styles.statIcon}>💰</span>
            <span className={styles.statLabel}>本月账单状态</span>
          </div>
          <div className={styles.statValue}>正常</div>
        </div>
      </div>

      {/* ④ 骨架屏: 加载中显示占位卡片 */}
      {isLoading && (
        <div className={styles.instances}>
          {[1, 2, 3, 4].map((i) => (
            <div key={i} className={styles.skCard}>
              <div className={styles.skHead}>
                <div className={styles.skTitle} />
                <div className={styles.skBadge} />
              </div>
              <div className={styles.skGrid}>
                {[1, 2, 3, 4, 5].map((j) => (
                  <div key={j} className={styles.skMetric} />
                ))}
              </div>
              <div className={styles.skProgress} />
              <div className={styles.skChart} />
            </div>
          ))}
        </div>
      )}

      {/* 账号列表与实例网格 */}
      {!isLoading && overview?.accounts.map((account) => {
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

            {/* 仅监控模式横幅 (P2-11 §3: 账号级 actions_enabled=false 时醒目展示) */}
            {account.policy && !account.policy.actions_enabled && (
              <div className={styles.monitorOnlyBanner}>
                <span>🛡️</span>
                <div>
                  <strong>当前账号处于「仅监控」模式：</strong>
                  账号级动作总闸已关闭。到达阈值或日程时系统照常评估并发出告警通知，但不执行任何实际的云上启停操作。设备级动作配置已被全局锁定。
                </div>
              </div>
            )}

            {/* CDT 错误提示 */}
            {account.cdt_error && (
              <div className={styles.errorBanner}>
                ⚠️ CDT 流量查询失败: {account.cdt_error}（系统已进入安全隔离模式，暂停所有自动开机）
              </div>
            )}

            {/* 账号级保活策略控制区 (P2-11 §3) */}
            {(() => {
              const policyForm = getAccountPolicyFormValue(account);
              const isPolicyExpanded = !!expandedAccountPolicies[account.account_id];
              const isPolicyDirty = !!accountPolicyForms[account.account_id];

              return (
                <div className={styles.accountPolicyControls}>
                  <div className={styles.accountPolicyHeader}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-2)' }}>
                      <strong>⚙️ 账号级保活与调度策略</strong>
                      <span className="text-dim" style={{ fontSize: '12px' }}>
                        {account.policy?.is_enabled === false
                          ? '（守卫已暂停）'
                          : account.policy?.actions_enabled
                          ? '（允许自动动作）'
                          : '（仅监控模式）'}
                      </span>
                    </div>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-2)' }}>
                      {isPolicyExpanded && (
                        <button
                          type="button"
                          className="btn mini primary"
                          disabled={!isPolicyDirty || updateAccountPolicyMutation.isPending}
                          onClick={() => handleSaveAccountPolicy(account)}
                        >
                          {updateAccountPolicyMutation.isPending ? '保存中...' : '保存策略'}
                        </button>
                      )}
                      <button
                        type="button"
                        className={styles.cdtToggleBtn}
                        onClick={() => toggleAccountPolicy(account.account_id)}
                      >
                        {isPolicyExpanded ? '收起策略配置 ▲' : '配置账号策略 ▼'}
                      </button>
                    </div>
                  </div>

                  {isPolicyExpanded && (
                    <div className={styles.accountPolicyGrid}>
                      <div className={styles.accountPolicyItem}>
                        <div className={styles.switchRow}>
                          <div className={styles.switchLabelArea}>
                            <strong>启用账号守卫</strong>
                            <span className={styles.switchDesc}>关闭将跳过整个账号评估</span>
                          </div>
                          <input
                            type="checkbox"
                            checked={policyForm.is_enabled ?? true}
                            onChange={(e) =>
                              handleAccountPolicyFormChange(account.account_id, {
                                is_enabled: e.target.checked,
                              })
                            }
                          />
                        </div>
                      </div>

                      <div className={styles.accountPolicyItem}>
                        <div className={styles.switchRow}>
                          <div className={styles.switchLabelArea}>
                            <strong style={{ color: policyForm.actions_enabled ? 'var(--warn)' : 'var(--text-main)' }}>
                              账号动作总闸 (Actions Enabled)
                            </strong>
                            <span className={styles.switchDesc}>
                              {policyForm.actions_enabled
                                ? '允许账号下所有授权设备执行真实启停'
                                : '急停开关：关闭后全账号仅监控，不执行启停'}
                            </span>
                          </div>
                          <input
                            type="checkbox"
                            checked={policyForm.actions_enabled ?? false}
                            onChange={(e) =>
                              handleAccountPolicyFormChange(account.account_id, {
                                actions_enabled: e.target.checked,
                              })
                            }
                          />
                        </div>
                      </div>

                      <div className={styles.accountPolicyItem}>
                        <div className={styles.fieldRow}>
                          <span>全账号 CDT 月度限额 (GB):</span>
                          <input
                            type="number"
                            className={styles.input}
                            style={{ width: '90px' }}
                            min="1"
                            max="500"
                            value={policyForm.traffic_limit_gb ?? 50}
                            onChange={(e) =>
                              handleAccountPolicyFormChange(account.account_id, {
                                traffic_limit_gb: parseFloat(e.target.value) || 50,
                              })
                            }
                          />
                        </div>
                      </div>

                      <div className={styles.accountPolicyItem} style={{ gridColumn: '1 / -1' }}>
                        <div className={styles.scheduleBox}>
                          <div className={styles.switchRow}>
                            <div className={styles.switchLabelArea}>
                              <strong>账号默认每日开关机日程</strong>
                              <span className={styles.switchDesc}>未单独设置日程的实例将继承此时间窗口</span>
                            </div>
                            <input
                              type="checkbox"
                              checked={policyForm.schedule_enabled ?? false}
                              onChange={(e) =>
                                handleAccountPolicyFormChange(account.account_id, {
                                  schedule_enabled: e.target.checked,
                                })
                              }
                            />
                          </div>

                          {policyForm.schedule_enabled && (
                            <>
                              <div className={styles.scheduleInputs}>
                                <div style={{ display: 'flex', flexDirection: 'column', gap: '2px', flex: 1 }}>
                                  <span className={styles.propLabel}>开机时间</span>
                                  <input
                                    type="time"
                                    className={styles.input}
                                    value={policyForm.schedule_start || '08:30'}
                                    onChange={(e) =>
                                      handleAccountPolicyFormChange(account.account_id, {
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
                                    value={policyForm.schedule_stop || '20:00'}
                                    onChange={(e) =>
                                      handleAccountPolicyFormChange(account.account_id, {
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
                                  value={policyForm.schedule_tz || 'Asia/Shanghai'}
                                  onChange={(e) =>
                                    handleAccountPolicyFormChange(account.account_id, {
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
                            </>
                          )}
                        </div>
                      </div>
                    </div>
                  )}
                </div>
              );
            })()}

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

            {/* ② 实例卡片网格 (aliyun-guard 风格两列网格，窄屏单列) */}
            <div className={styles.instances}>
              {account.instances.map((inst) => {
                const rule = inst.rule;
                const form = getFormValue(inst.resource_id, rule);
                const hasDirty = !!editForms[inst.resource_id];

                return (
                  <InstanceCard
                    key={inst.resource_id}
                    inst={inst}
                    account={account}
                    form={form}
                    onFormChange={(patch) => handleFormChange(inst.resource_id, patch)}
                    onSaveRule={() => handleSaveRule(inst.resource_id, rule)}
                    hasDirty={hasDirty}
                    isSaving={updateRuleMutation.isPending}
                    onForceStart={() => setForceStartTarget({ instance: inst, account })}
                    formatScheduleNext={formatScheduleNext}
                  />
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
                <th>CDT 流量</th>
                <th>评估实例</th>
                <th>执行动作</th>
                <th>失败数</th>
              </tr>
            </thead>
            <tbody>
              {cyclesData?.cycles?.length ? (
                cyclesData.cycles.map((c) => (
                  <tr key={c.id}>
                    <td style={{ fontFamily: 'var(--font-mono)' }}>{c.id.slice(0, 8)}...</td>
                    <td>{new Date(c.started_at_ms).toLocaleTimeString()}</td>
                    <td>{c.duration_ms} ms</td>
                    <td>{c.cdt_used_gb !== undefined ? `${c.cdt_used_gb.toFixed(2)} GB` : '--'}</td>
                    <td>{c.evaluated}</td>
                    <td style={{ color: c.acted > 0 ? 'var(--warn)' : 'var(--text-main)', fontWeight: c.acted > 0 ? 600 : 400 }}>
                      {c.acted}
                    </td>
                    <td style={{ color: c.failed > 0 ? 'var(--err)' : 'var(--text-main)' }}>
                      {c.failed}
                    </td>
                  </tr>
                ))
              ) : (
                <tr>
                  <td colSpan={7} style={{ textAlign: 'center', color: 'var(--text-dim)', padding: '24px 0' }}>
                    暂无评估记录
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* Dry Run 结果模态弹窗 */}
      {dryRunResult && (
        <div className={styles.modalBackdrop} onClick={() => setDryRunResult(null)}>
          <div className={styles.modal} onClick={(e) => e.stopPropagation()}>
            <div className={styles.modalHeader}>
              <div className={styles.modalTitle}>🔍 守卫评估演练结果 (Dry Run)</div>
              <button
                type="button"
                className="btn mini ghost"
                onClick={() => setDryRunResult(null)}
              >
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
                    {dryRunResult.actions.filter((i) => i.proposed_action === 'stop').length}
                  </div>
                  <div className={styles.summaryLabel}>拟关机</div>
                </div>
                <div className={styles.summaryCard}>
                  <div className={styles.summaryNum} style={{ color: 'var(--ok)' }}>
                    {dryRunResult.actions.filter((i) => i.proposed_action === 'start').length}
                  </div>
                  <div className={styles.summaryLabel}>拟开机</div>
                </div>
                <div className={styles.summaryCard}>
                  <div className={styles.summaryNum}>
                    {dryRunResult.actions.filter((i) => i.proposed_action === 'noop').length}
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
                    {dryRunResult.actions.map((item) => {
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

            <div className={styles.tableWrapper} style={{ marginTop: 'var(--sp-3)' }}>
              <table className={styles.table}>
                <thead>
                  <tr>
                    <th>实例</th>
                    <th>地域</th>
                    <th>当前状态</th>
                    <th>拟执行动作</th>
                    <th>判定原因</th>
                  </tr>
                </thead>
                <tbody>
                  {dryRunResult.actions.map((item) => (
                    <tr key={item.resource_id}>
                      <td>
                        <strong>{item.resource_name || item.resource_ref}</strong>
                        <div className="text-dim" style={{ fontSize: '11px' }}>
                          {item.account_name}
                        </div>
                      </td>
                      <td>{item.region}</td>
                      <td>{item.current_status}</td>
                      <td>
                        <span
                          className={
                            item.proposed_action === 'stop'
                              ? styles.actionTagStop
                              : item.proposed_action === 'start'
                              ? styles.actionTagStart
                              : styles.actionTagNoop
                          }
                        >
                          {item.proposed_action === 'stop'
                            ? '🛑 关机'
                            : item.proposed_action === 'start'
                            ? '🚀 开机'
                            : '保持'}
                        </span>
                      </td>
                      <td style={{ fontSize: '12px' }}>{item.reason}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 'var(--sp-4)' }}>
              <button
                type="button"
                className="btn primary"
                onClick={() => setDryRunResult(null)}
              >
                关闭
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 强制启动模态弹窗 */}
      {forceStartTarget && (
        <div className={styles.modalBackdrop} onClick={() => setForceStartTarget(null)}>
          <div className={styles.modal} onClick={(e) => e.stopPropagation()}>
            <div className={styles.modalHeader}>
              <div className={styles.modalTitle}>⚡ 强制开机确认 (解除保护)</div>
              <button
                type="button"
                className="btn mini ghost"
                onClick={() => setForceStartTarget(null)}
              >
                ✕
              </button>
            </div>

            <div className={styles.errorBanner} style={{ marginTop: 'var(--sp-2)' }}>
              ⚠️ 注意：该操作将解除保全关机状态并对云服务器下发真实开机指令。
              如果当前流量仍处于超标状态，本周期将暂时忽略超标保护，直到下一次决策。
            </div>

            <div style={{ margin: 'var(--sp-3) 0', fontSize: '13px' }}>
              <div><strong>实例:</strong> {forceStartTarget.instance.resource_name || forceStartTarget.instance.resource_ref}</div>
              <div><strong>所属账号:</strong> {forceStartTarget.account.account_name}</div>
            </div>

            <div className={styles.formField}>
              <label>请输入强制开机的原因 / 申请单号:</label>
              <input
                type="text"
                className={styles.input}
                placeholder="如：紧急处理线上故障、已额外充值..."
                value={forceReason}
                onChange={(e) => setForceReason(e.target.value)}
              />
            </div>

            <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-2)', marginTop: 'var(--sp-3)' }}>
              <input
                type="checkbox"
                id="confirmForceStart"
                checked={forceConfirmed}
                onChange={(e) => setForceConfirmed(e.target.checked)}
              />
              <label htmlFor="confirmForceStart" style={{ fontSize: '13px', cursor: 'pointer' }}>
                我已明确知晓超额流量可能会产生额外按量账单，并确认强制启动此机器。
              </label>
            </div>

            {forceError && (
              <div className={styles.errorBanner} style={{ marginTop: 'var(--sp-2)' }}>
                {forceError}
              </div>
            )}

            <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 'var(--sp-2)', marginTop: 'var(--sp-4)' }}>
              <button
                type="button"
                className="btn ghost"
                onClick={() => setForceStartTarget(null)}
              >
                取消
              </button>
              <button
                type="button"
                className="btn warning"
                disabled={!forceConfirmed || !forceReason.trim() || forceStartMutation.isPending}
                onClick={() =>
                  forceStartMutation.mutate({
                    resId: forceStartTarget.instance.resource_id,
                    reason: forceReason.trim(),
                  })
                }
              >
                {forceStartMutation.isPending ? '开机中...' : '确认强制开机'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};

export default CloudGuard;
