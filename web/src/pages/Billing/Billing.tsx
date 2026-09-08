import React, { useState } from 'react';
import { useQuery, useMutation } from '@tanstack/react-query';
import styles from './Billing.module.css';
import {
  getBillingOverview,
  getBillingItems,
  listBudgets,
  createBudget,
  deleteBudget,
  triggerBillSync,
  triggerBudgetEval,
} from '../../api';

export const Billing: React.FC = () => {
  const [selectedPeriod] = useState<string>(''); // empty means current
  const [groupBy, setGroupBy] = useState<'kind' | 'resource' | 'tag'>('kind');
  const [isBudgetModalOpen, setIsBudgetModalOpen] = useState(false);
  const [isNewBudgetOpen, setIsNewBudgetOpen] = useState(false);

  // Form states for new budget
  const [newBudgetScope, setNewBudgetScope] = useState<string>('all');
  const [newBudgetRef, setNewBudgetRef] = useState<string>('');
  const [newBudgetCurrency, setNewBudgetCurrency] = useState<string>('CNY');
  const [newBudgetAmount, setNewBudgetAmount] = useState<string>('1000');
  const [newBudgetWarn, setNewBudgetWarn] = useState<string>('0.8');

  // Queries
  const { data: overview, isLoading: isOverviewLoading, refetch: refetchOverview } = useQuery({
    queryKey: ['billingOverview', selectedPeriod],
    queryFn: () => getBillingOverview(selectedPeriod),
  });

  const { data: itemsData, isLoading: isItemsLoading, refetch: refetchItems } = useQuery({
    queryKey: ['billingItems', selectedPeriod, groupBy],
    queryFn: () => getBillingItems({ period: selectedPeriod, group_by: groupBy }),
  });

  const { data: budgetsData, refetch: refetchBudgets } = useQuery({
    queryKey: ['billingBudgets'],
    queryFn: listBudgets,
  });

  // Mutations
  const syncMutation = useMutation({
    mutationFn: () => triggerBillSync(),
    onSuccess: () => {
      setTimeout(() => {
        refetchOverview();
        refetchItems();
        refetchBudgets();
      }, 1000);
    },
  });

  const evalMutation = useMutation({
    mutationFn: () => triggerBudgetEval(),
    onSuccess: () => {
      refetchBudgets();
    },
  });

  const createBudgetMutation = useMutation({
    mutationFn: () =>
      createBudget({
        scope_kind: newBudgetScope,
        scope_ref: newBudgetRef || undefined,
        currency: newBudgetCurrency,
        amount: parseFloat(newBudgetAmount) || 0,
        warn_ratio: parseFloat(newBudgetWarn) || 0.8,
        is_enabled: true,
      }),
    onSuccess: () => {
      setIsNewBudgetOpen(false);
      refetchBudgets();
      refetchOverview();
    },
  });

  const deleteBudgetMutation = useMutation({
    mutationFn: (id: string) => deleteBudget(id),
    onSuccess: () => {
      refetchBudgets();
      refetchOverview();
    },
  });

  // Helpers
  const formatAmount = (val?: number) => {
    if (val === undefined || val === null || isNaN(val)) return '--';
    return val.toFixed(2);
  };

  const formatPercent = (val?: number) => {
    if (val === undefined || val === null || isNaN(val)) return '--';
    const sign = val > 0 ? '+' : '';
    return `${sign}${(val * 100).toFixed(1)}%`;
  };

  const getProgressClass = (ratio?: number) => {
    if (ratio === undefined || ratio === null) return styles.progressOk;
    if (ratio >= 1.0) return styles.progressExceeded;
    if (ratio >= 0.8) return styles.progressWarn;
    return styles.progressOk;
  };

  return (
    <div className={styles.container}>
      {/* 顶部标题与操作 */}
      <div className={styles.header}>
        <div className={styles.titleArea}>
          <div className={styles.title}>
            <span>💰 云账单聚合与预算</span>
            {overview?.sync_state && (
              <span className={`status-badge ${overview.sync_state === 'ok' ? 'running' : 'stopped'}`}>
                {overview.sync_state === 'ok' ? '已同步' : overview.sync_state}
              </span>
            )}
          </div>
          <div className={styles.subtitle}>
            跨云厂商真实费用对账、按维度汇总与多币种预算监控
            {overview?.synced_at_ms ? ` · 最近更新: ${new Date(overview.synced_at_ms).toLocaleString()}` : ''}
          </div>
        </div>

        <div className={styles.headerActions}>
          <button
            type="button"
            className="btn ghost mini"
            onClick={() => setIsBudgetModalOpen(true)}
          >
            ⚙️ 预算管理 ({budgetsData?.items?.length || 0})
          </button>
          <button
            type="button"
            className="btn ghost mini"
            onClick={() => evalMutation.mutate()}
            disabled={evalMutation.isPending}
          >
            {evalMutation.isPending ? '评估中...' : '🎯 评估预算'}
          </button>
          <button
            type="button"
            className="btn primary mini"
            onClick={() => syncMutation.mutate()}
            disabled={syncMutation.isPending}
          >
            {syncMutation.isPending ? '同步中...' : '🔄 立即同步'}
          </button>
        </div>
      </div>

      {/* 顶部汇总卡片：按币种分组显示，禁止折算 */}
      <div className={styles.overviewCards}>
        {isOverviewLoading ? (
          <div className={styles.card}>
            <div className={styles.cardTitle}>正在加载账单总览...</div>
          </div>
        ) : overview?.totals && overview.totals.length > 0 ? (
          overview.totals.map((tot) => {
            const momRatio = tot.mom_ratio;
            let momClass = styles.momFlat;
            if (momRatio !== undefined && momRatio !== null) {
              if (momRatio > 0.001) momClass = styles.momUp;
              else if (momRatio < -0.001) momClass = styles.momDown;
            }

            return (
              <div key={tot.currency} className={styles.card}>
                <div className={styles.cardHeader}>
                  <span className={styles.cardTitle}>当月账单支出</span>
                  <span className={styles.currencyBadge}>{tot.currency}</span>
                </div>

                <div className={styles.cardValueRow}>
                  <span className={styles.cardValue}>
                    {tot.currency === 'USD' ? '$' : '¥'} {formatAmount(tot.total_amount)}
                  </span>
                  {momRatio !== undefined && momRatio !== null ? (
                    <span className={`${styles.momText} ${momClass}`}>
                      环比 {formatPercent(momRatio)}
                    </span>
                  ) : (
                    <span className={`${styles.momText} ${styles.momFlat}`}>环比 --</span>
                  )}
                </div>

                {tot.budget_amount && tot.budget_amount > 0 ? (
                  <div className={styles.budgetRow}>
                    <div className={styles.budgetLabelRow}>
                      <span>预算 {formatAmount(tot.budget_amount)} {tot.currency}</span>
                      <span>{tot.budget_progress !== undefined ? `${(tot.budget_progress * 100).toFixed(1)}%` : '--'}</span>
                    </div>
                    <div className={styles.progressBarBg}>
                      <div
                        className={`${styles.progressBarFill} ${getProgressClass(tot.budget_progress)}`}
                        style={{ width: `${Math.min(100, Math.max(0, (tot.budget_progress || 0) * 100))}%` }}
                      />
                    </div>
                  </div>
                ) : (
                  <div className={styles.budgetRow}>
                    <div className={styles.budgetLabelRow}>
                      <span>未设置总预算</span>
                      <span>--</span>
                    </div>
                  </div>
                )}
              </div>
            );
          })
        ) : (
          <div className={styles.card}>
            <div className={styles.cardTitle}>暂无账单数据</div>
            <div className={styles.cardValueRow}>
              <span className={styles.cardValue}>--</span>
            </div>
            <div className={styles.subtitle}>请点击右上角「立即同步」拉取云厂商账单</div>
          </div>
        )}
      </div>

      {/* 趋势图部分：最近 12 个月柱状图，按币种分开画 */}
      {overview?.trends && overview.trends.length > 0 && (
        <div className={styles.chartSection}>
          <div className={styles.chartHeader}>
            <div className={styles.sectionTitle}>
              <span>📈 费用趋势 (最近 12 个月)</span>
            </div>
          </div>

          <div className={styles.chartGrid}>
            {overview.trends.map((tr) => {
              const maxAmt = Math.max(1, ...tr.months.map((m) => m.amount));
              return (
                <div key={tr.currency} className={styles.chartCard}>
                  <div className={styles.chartTitle}>{tr.currency} 支出趋势</div>
                  <div className={styles.barChart}>
                    {tr.months.map((m) => {
                      const heightPct = (m.amount / maxAmt) * 100;
                      return (
                        <div key={m.period} className={styles.barCol} title={`${m.period}: ${m.amount.toFixed(2)} ${tr.currency}`}>
                          <div className={styles.barAmt}>
                            {m.amount > 0 ? (m.amount >= 1000 ? `${(m.amount / 1000).toFixed(1)}k` : m.amount.toFixed(0)) : ''}
                          </div>
                          <div
                            className={styles.barFill}
                            style={{ height: `${Math.max(3, heightPct)}%` }}
                          />
                          <div className={styles.barLabel}>{m.period.slice(5)}月</div>
                        </div>
                      );
                    })}
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      )}

      {/* 明细表部分：维度切换 (类型 / 资源 / 标签)，未关联资源单列一组 */}
      <div className={styles.itemsSection}>
        <div className={styles.itemsHeader}>
          <div className={styles.sectionTitle}>
            <span>📋 费用明细与维度汇总</span>
          </div>

          <div className={styles.filterGroup}>
            <div className={styles.segmentedControl}>
              <button
                type="button"
                className={`${styles.segmentBtn} ${groupBy === 'kind' ? styles.segmentBtnActive : ''}`}
                onClick={() => setGroupBy('kind')}
              >
                按资源类型
              </button>
              <button
                type="button"
                className={`${styles.segmentBtn} ${groupBy === 'resource' ? styles.segmentBtnActive : ''}`}
                onClick={() => setGroupBy('resource')}
              >
                按单资源
              </button>
              <button
                type="button"
                className={`${styles.segmentBtn} ${groupBy === 'tag' ? styles.segmentBtnActive : ''}`}
                onClick={() => setGroupBy('tag')}
              >
                按标签
              </button>
            </div>
          </div>
        </div>

        {isItemsLoading ? (
          <div className={styles.emptyState}>正在加载费用明细...</div>
        ) : !itemsData?.groups || itemsData.groups.length === 0 ? (
          <div className={styles.emptyState}>当前账期暂无明细数据</div>
        ) : (
          <div className={styles.tableWrapper}>
            <table className={styles.table}>
              <thead>
                <tr>
                  <th>分组 / 条目</th>
                  <th>类型</th>
                  <th>资源标识</th>
                  <th>标签</th>
                  <th>使用量</th>
                  <th style={{ textAlign: 'right' }}>金额</th>
                </tr>
              </thead>
              <tbody>
                {itemsData.groups.map((grp) => (
                  <React.Fragment key={grp.key}>
                    <tr className={styles.groupRow}>
                      <td colSpan={5}>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--sp-2)' }}>
                          <span>{grp.display_name}</span>
                          {grp.is_unlinked && (
                            <span className={styles.unlinkedBadge}>⚠️ 未关联本地资源</span>
                          )}
                          <span style={{ fontSize: '11px', color: 'var(--text-mute)' }}>
                            ({grp.item_count} 条)
                          </span>
                        </div>
                      </td>
                      <td style={{ textAlign: 'right', fontFamily: 'var(--font-mono)' }}>
                        {formatAmount(grp.total_amount)} {grp.currency}
                      </td>
                    </tr>
                    {grp.items &&
                      grp.items.map((it) => (
                        <tr key={it.id}>
                          <td style={{ paddingLeft: 'var(--sp-5)' }}>
                            {it.item_name || it.product_code || '--'}
                          </td>
                          <td>
                            <span className="status-badge stopped">{it.res_kind}</span>
                          </td>
                          <td style={{ fontFamily: 'var(--font-mono)' }}>
                            {it.res_ref || '--'}
                          </td>
                          <td>
                            {it.tags && it.tags.length > 0 ? (
                              it.tags.map((t) => (
                                <span key={t} className={styles.tagBadge}>
                                  {t}
                                </span>
                              ))
                            ) : (
                              <span style={{ color: 'var(--text-mute)' }}>--</span>
                            )}
                          </td>
                          <td>{it.usage_text || '--'}</td>
                          <td style={{ textAlign: 'right', fontFamily: 'var(--font-mono)' }}>
                            {formatAmount(it.amount)} {it.currency}
                          </td>
                        </tr>
                      ))}
                  </React.Fragment>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {/* 预算管理模态框 */}
      {isBudgetModalOpen && (
        <div className={styles.modalOverlay} onClick={() => setIsBudgetModalOpen(false)}>
          <div className={styles.modalContent} onClick={(e) => e.stopPropagation()}>
            <div className={styles.modalHeader}>
              <div className={styles.modalTitle}>预算配置与监控</div>
              <button
                type="button"
                className={styles.closeBtn}
                onClick={() => setIsBudgetModalOpen(false)}
              >
                ✕
              </button>
            </div>

            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <span style={{ fontSize: '13px', color: 'var(--text-dim)' }}>
                当支出达到预警比例时触发告警并通知
              </span>
              <button
                type="button"
                className="btn mini primary"
                onClick={() => setIsNewBudgetOpen(true)}
              >
                + 添加预算
              </button>
            </div>

            <div className={styles.budgetList}>
              {!budgetsData?.items || budgetsData.items.length === 0 ? (
                <div className={styles.emptyState}>暂未配置任何预算</div>
              ) : (
                budgetsData.items.map((b) => (
                  <div key={b.id} className={styles.budgetItem}>
                    <div className={styles.budgetDesc}>
                      <span style={{ fontWeight: 600 }}>
                        {b.scope_kind === 'all'
                          ? '全局月度预算'
                          : b.scope_kind === 'tag'
                          ? `标签预算: ${b.scope_ref}`
                          : `账号预算: ${b.scope_ref}`}
                      </span>
                      <span className={styles.budgetSub}>
                        限额: {formatAmount(b.amount)} {b.currency} · 预警线: {(b.warn_ratio * 100).toFixed(0)}%
                        {b.current_spent !== undefined && ` · 当月已用: ${formatAmount(b.current_spent)} ${b.currency}`}
                      </span>
                    </div>
                    <button
                      type="button"
                      className="btn mini ghost"
                      onClick={() => deleteBudgetMutation.mutate(b.id)}
                    >
                      删除
                    </button>
                  </div>
                ))
              )}
            </div>

            <div className={styles.modalFooter}>
              <button
                type="button"
                className="btn ghost mini"
                onClick={() => setIsBudgetModalOpen(false)}
              >
                关闭
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 新增预算模态框 */}
      {isNewBudgetOpen && (
        <div className={styles.modalOverlay} onClick={() => setIsNewBudgetOpen(false)}>
          <div className={styles.modalContent} onClick={(e) => e.stopPropagation()}>
            <div className={styles.modalHeader}>
              <div className={styles.modalTitle}>新建预算规则</div>
              <button
                type="button"
                className={styles.closeBtn}
                onClick={() => setIsNewBudgetOpen(false)}
              >
                ✕
              </button>
            </div>

            <div className={styles.formGroup}>
              <label className={styles.formLabel}>作用范围</label>
              <select
                className={styles.formSelect}
                value={newBudgetScope}
                onChange={(e) => setNewBudgetScope(e.target.value)}
              >
                <option value="all">全局 (全部账号)</option>
                <option value="tag">按标签</option>
                <option value="account">按账号</option>
              </select>
            </div>

            {newBudgetScope !== 'all' && (
              <div className={styles.formGroup}>
                <label className={styles.formLabel}>
                  {newBudgetScope === 'tag' ? '标签名 (如 proxy)' : '账号 ID'}
                </label>
                <input
                  type="text"
                  className={styles.formInput}
                  value={newBudgetRef}
                  onChange={(e) => setNewBudgetRef(e.target.value)}
                  placeholder={newBudgetScope === 'tag' ? '例如: proxy' : '例如: 01M1...'}
                />
              </div>
            )}

            <div className={styles.formGroup}>
              <label className={styles.formLabel}>币种</label>
              <select
                className={styles.formSelect}
                value={newBudgetCurrency}
                onChange={(e) => setNewBudgetCurrency(e.target.value)}
              >
                <option value="CNY">CNY (人民币)</option>
                <option value="USD">USD (美元)</option>
              </select>
            </div>

            <div className={styles.formGroup}>
              <label className={styles.formLabel}>预算金额</label>
              <input
                type="number"
                className={styles.formInput}
                value={newBudgetAmount}
                onChange={(e) => setNewBudgetAmount(e.target.value)}
                min="1"
              />
            </div>

            <div className={styles.formGroup}>
              <label className={styles.formLabel}>预警阈值比例 (如 0.8 代表 80%)</label>
              <input
                type="number"
                step="0.05"
                min="0.1"
                max="1.0"
                className={styles.formInput}
                value={newBudgetWarn}
                onChange={(e) => setNewBudgetWarn(e.target.value)}
              />
            </div>

            <div className={styles.modalFooter}>
              <button
                type="button"
                className="btn ghost mini"
                onClick={() => setIsNewBudgetOpen(false)}
              >
                取消
              </button>
              <button
                type="button"
                className="btn primary mini"
                onClick={() => createBudgetMutation.mutate()}
                disabled={createBudgetMutation.isPending}
              >
                {createBudgetMutation.isPending ? '创建中...' : '确认创建'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
