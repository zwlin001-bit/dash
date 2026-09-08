import React, { useState, useEffect } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  fetchJobs,
  fetchJobDetail,
  fetchJobKinds,
  submitJob,
  cancelJob,
  retryJob,
  Job,
  JobStep,
} from '../../api';
import styles from './Jobs.module.css';

export const Jobs: React.FC = () => {
  const queryClient = useQueryClient();

  // Filters
  const [filterState, setFilterState] = useState<string>('all');
  const [filterKind, setFilterKind] = useState<string>('');
  const [page, setPage] = useState<number>(0);
  const pageSize = 20;

  // Selected Job for Detail Drawer
  const [selectedJobId, setSelectedJobId] = useState<string | null>(null);
  const [liveJob, setLiveJob] = useState<Job | null>(null);
  const [isSseConnected, setIsSseConnected] = useState<boolean>(false);

  // Submit Modal state
  const [isSubmitModalOpen, setIsSubmitModalOpen] = useState<boolean>(false);
  const [submitKind, setSubmitKind] = useState<string>('test.three_steps');
  const [submitTargetKind, setSubmitTargetKind] = useState<string>('');
  const [submitTargetId, setSubmitTargetId] = useState<string>('');
  const [submitMaxAttempt, setSubmitMaxAttempt] = useState<number>(3);
  const [submitRejectIfBusy, setSubmitRejectIfBusy] = useState<boolean>(false);
  const [submitParamsText, setSubmitParamsText] = useState<string>(
    JSON.stringify({ step1_delay_ms: 500, step2_delay_ms: 500, step3_delay_ms: 500 }, null, 2)
  );
  const [submitError, setSubmitError] = useState<string>('');

  // Fetch Jobs List
  const { data: jobsData, isLoading, refetch } = useQuery({
    queryKey: ['jobs', filterState, filterKind, page],
    queryFn: () =>
      fetchJobs({
        state: filterState,
        kind: filterKind || undefined,
        limit: pageSize,
        offset: page * pageSize,
      }),
    refetchInterval: 4000,
  });

  // Fetch Kinds for selector
  const { data: kindsData } = useQuery({
    queryKey: ['job-kinds'],
    queryFn: fetchJobKinds,
  });

  // Fetch static job detail if not live
  const { data: detailData } = useQuery({
    queryKey: ['job-detail', selectedJobId],
    queryFn: () => fetchJobDetail(selectedJobId!),
    enabled: !!selectedJobId,
  });

  useEffect(() => {
    if (detailData?.job) {
      setLiveJob(detailData.job);
    }
  }, [detailData]);

  // Real-time SSE Stream subscription for selected job
  useEffect(() => {
    if (!selectedJobId) {
      setIsSseConnected(false);
      setLiveJob(null);
      return;
    }

    const es = new EventSource(`/api/v1/jobs/${selectedJobId}/stream`);
    setIsSseConnected(true);

    es.addEventListener('snapshot', (e: MessageEvent) => {
      try {
        const snap = JSON.parse(e.data);
        setLiveJob(snap);
      } catch {}
    });

    es.addEventListener('job', (e: MessageEvent) => {
      try {
        const jobUpdate = JSON.parse(e.data);
        setLiveJob((prev) => (prev ? { ...prev, ...jobUpdate } : prev));
        queryClient.invalidateQueries({ queryKey: ['jobs'] });
      } catch {}
    });

    es.addEventListener('step', (e: MessageEvent) => {
      try {
        const stepUpdate: JobStep = JSON.parse(e.data);
        setLiveJob((prev) => {
          if (!prev) return prev;
          const steps = [...(prev.steps || [])];
          const idx = steps.findIndex((s) => s.step_index === stepUpdate.step_index);
          if (idx >= 0) {
            steps[idx] = { ...steps[idx], ...stepUpdate };
          } else {
            steps.push(stepUpdate);
          }
          return { ...prev, steps };
        });
      } catch {}
    });

    es.addEventListener('log', (e: MessageEvent) => {
      try {
        const { step_index, log } = JSON.parse(e.data);
        setLiveJob((prev) => {
          if (!prev) return prev;
          const steps = [...(prev.steps || [])];
          const idx = steps.findIndex((s) => s.step_index === step_index);
          if (idx >= 0) {
            const curLog = steps[idx].log_text || '';
            steps[idx] = {
              ...steps[idx],
              log_text: curLog ? curLog + '\n' + log : log,
            };
          }
          return { ...prev, steps };
        });
      } catch {}
    });

    es.onerror = () => {
      setIsSseConnected(false);
    };

    return () => {
      es.close();
      setIsSseConnected(false);
    };
  }, [selectedJobId, queryClient]);

  // Mutations
  const submitMutation = useMutation({
    mutationFn: submitJob,
    onSuccess: (res) => {
      setIsSubmitModalOpen(false);
      setSubmitError('');
      queryClient.invalidateQueries({ queryKey: ['jobs'] });
      setSelectedJobId(res.job.id);
    },
    onError: (err: any) => {
      setSubmitError(err?.message || '提交任务失败');
    },
  });

  const cancelMutation = useMutation({
    mutationFn: cancelJob,
    onSuccess: (res) => {
      queryClient.invalidateQueries({ queryKey: ['jobs'] });
      if (res.job) setLiveJob(res.job);
    },
  });

  const retryMutation = useMutation({
    mutationFn: retryJob,
    onSuccess: (res) => {
      queryClient.invalidateQueries({ queryKey: ['jobs'] });
      if (res.job) setLiveJob(res.job);
    },
  });

  const handleOpenSubmit = () => {
    setSubmitError('');
    setIsSubmitModalOpen(true);
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    let parsedParams: Record<string, any> = {};
    if (submitParamsText.trim()) {
      try {
        parsedParams = JSON.parse(submitParamsText);
      } catch (err: any) {
        setSubmitError('参数 JSON 格式有误: ' + err.message);
        return;
      }
    }

    submitMutation.mutate({
      kind: submitKind,
      target_kind: submitTargetKind || undefined,
      target_id: submitTargetId || undefined,
      params: parsedParams,
      max_attempt: submitMaxAttempt,
      reject_if_busy: submitRejectIfBusy,
    });
  };

  const formatTime = (ts?: number) => {
    if (!ts) return '-';
    return new Date(ts).toLocaleString('zh-CN', { hour12: false });
  };

  const formatDuration = (start?: number, finish?: number) => {
    if (!start) return '-';
    const end = finish || Date.now();
    const ms = end - start;
    if (ms < 1000) return `${ms}ms`;
    if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`;
    return `${Math.floor(ms / 60000)}m ${Math.floor((ms % 60000) / 1000)}s`;
  };

  const renderStateBadge = (state: string) => {
    switch (state) {
      case 'running':
        return (
          <span className={`${styles.stateBadge} ${styles.stateRunning}`}>
            <span className={styles.stateDot} />
            进行中
          </span>
        );
      case 'succeeded':
        return (
          <span className={`${styles.stateBadge} ${styles.stateSucceeded}`}>
            <span className={styles.stateDot} />
            成功
          </span>
        );
      case 'failed':
        return (
          <span className={`${styles.stateBadge} ${styles.stateFailed}`}>
            <span className={styles.stateDot} />
            失败
          </span>
        );
      case 'cancelled':
        return (
          <span className={`${styles.stateBadge} ${styles.stateCancelled}`}>
            <span className={styles.stateDot} />
            已取消
          </span>
        );
      default:
        return (
          <span className={`${styles.stateBadge} ${styles.statePending}`}>
            <span className={styles.stateDot} />
            等待中
          </span>
        );
    }
  };

  const currentDetailJob = liveJob || detailData?.job;

  return (
    <div className={styles.container}>
      {/* 顶部标题与操作栏 */}
      <div className={styles.header}>
        <div className={styles.titleArea}>
          <div>
            <h1 className={styles.title}>任务中心</h1>
            <div className={styles.subtitle}>后台异步任务持久化执行、调度状态与实时进度</div>
          </div>
        </div>

        <div className={styles.headerActions}>
          <button type="button" className="btn primary small" onClick={handleOpenSubmit}>
            <span>+</span>
            <span>新建任务</span>
          </button>
          <button type="button" className="btn ghost small" onClick={() => refetch()}>
            刷新
          </button>
        </div>
      </div>

      {/* 筛选过滤工具条 */}
      <div className={styles.filterCard}>
        <div className={styles.filterGroup}>
          <span className={styles.label}>状态:</span>
          <select
            className={styles.select}
            value={filterState}
            onChange={(e) => {
              setFilterState(e.target.value);
              setPage(0);
            }}
          >
            <option value="all">全部状态</option>
            <option value="running">进行中 (running)</option>
            <option value="pending">排队中 (pending)</option>
            <option value="succeeded">已完成 (succeeded)</option>
            <option value="failed">失败 (failed)</option>
            <option value="cancelled">已取消 (cancelled)</option>
          </select>
        </div>

        <div className={styles.filterGroup}>
          <span className={styles.label}>类型:</span>
          <input
            type="text"
            className={styles.input}
            placeholder="搜索任务类型..."
            value={filterKind}
            onChange={(e) => {
              setFilterKind(e.target.value);
              setPage(0);
            }}
          />
        </div>
      </div>

      {/* 任务列表表格 */}
      <div className={styles.tableCard}>
        <table className={styles.table}>
          <thead>
            <tr>
              <th className={styles.th}>任务 ID</th>
              <th className={styles.th}>任务类型</th>
              <th className={styles.th}>目标对象</th>
              <th className={styles.th}>状态</th>
              <th className={styles.th}>尝试</th>
              <th className={styles.th}>耗时</th>
              <th className={styles.th}>提交时间</th>
              <th className={styles.th} style={{ textAlign: 'right' }}>
                操作
              </th>
            </tr>
          </thead>
          <tbody>
            {isLoading ? (
              <tr>
                <td colSpan={8} className={styles.emptyState}>
                  正在加载任务清单...
                </td>
              </tr>
            ) : !jobsData?.jobs || jobsData.jobs.length === 0 ? (
              <tr>
                <td colSpan={8} className={styles.emptyState}>
                  暂无任务记录
                </td>
              </tr>
            ) : (
              jobsData.jobs.map((j) => (
                <tr key={j.id} className={styles.tr}>
                  <td className={styles.td}>
                    <span
                      className={styles.idCell}
                      onClick={() => setSelectedJobId(j.id)}
                      title={j.id}
                    >
                      {j.id.slice(0, 10)}...
                    </span>
                  </td>
                  <td className={styles.td}>
                    <span className={styles.kindBadge}>{j.job_kind}</span>
                  </td>
                  <td className={styles.td}>
                    {j.target_kind ? (
                      <span className={styles.targetBadge}>
                        <span>{j.target_kind}</span>
                        {j.target_id && <span>: {j.target_id}</span>}
                      </span>
                    ) : (
                      <span style={{ color: 'var(--text-dim)' }}>-</span>
                    )}
                  </td>
                  <td className={styles.td}>{renderStateBadge(j.job_state)}</td>
                  <td className={styles.td}>
                    {j.attempt} / {j.max_attempt}
                  </td>
                  <td className={styles.td}>
                    {formatDuration(j.started_at_ms, j.finished_at_ms)}
                  </td>
                  <td className={styles.td}>{formatTime(j.created_at_ms)}</td>
                  <td className={styles.td} style={{ textAlign: 'right' }}>
                    <div className={styles.actions} style={{ justifyContent: 'flex-end' }}>
                      <button
                        type="button"
                        className="btn mini ghost"
                        onClick={() => setSelectedJobId(j.id)}
                      >
                        详情
                      </button>
                      {(j.job_state === 'failed' || j.job_state === 'cancelled') && (
                        <button
                          type="button"
                          className="btn mini ghost"
                          onClick={() => retryMutation.mutate(j.id)}
                          title="从失败断点继续重试"
                        >
                          重试
                        </button>
                      )}
                      {(j.job_state === 'running' || j.job_state === 'pending') && (
                        <button
                          type="button"
                          className="btn mini danger"
                          onClick={() => cancelMutation.mutate(j.id)}
                          title="中断执行"
                        >
                          取消
                        </button>
                      )}
                    </div>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      {/* 任务详情抽屉 (支持 SSE 实时更新) */}
      {selectedJobId && currentDetailJob && (
        <div className={styles.modalOverlay} onClick={() => setSelectedJobId(null)}>
          <div className={styles.drawer} onClick={(e) => e.stopPropagation()}>
            <div className={styles.drawerHeader}>
              <div>
                <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                  <h2 style={{ fontSize: 16, margin: 0, color: 'var(--text-main)' }}>
                    任务详情
                  </h2>
                  {renderStateBadge(currentDetailJob.job_state)}
                </div>
                <div style={{ fontFamily: 'var(--font-mono)', fontSize: 12, color: 'var(--text-dim)', marginTop: 4 }}>
                  {currentDetailJob.id}
                </div>
              </div>

              <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                {isSseConnected && (
                  <div className={styles.liveIndicator}>
                    <span className={styles.liveDot} />
                    <span>实时流推送中</span>
                  </div>
                )}
                <button
                  type="button"
                  className="btn mini ghost"
                  onClick={() => setSelectedJobId(null)}
                >
                  ✕
                </button>
              </div>
            </div>

            <div className={styles.drawerBody}>
              {/* 元数据指标网格 */}
              <div className={styles.metaGrid}>
                <div className={styles.metaItem}>
                  <span className={styles.metaLabel}>任务类型</span>
                  <span className={styles.metaValue}>{currentDetailJob.job_kind}</span>
                </div>
                <div className={styles.metaItem}>
                  <span className={styles.metaLabel}>目标资源</span>
                  <span className={styles.metaValue}>
                    {currentDetailJob.target_kind
                      ? `${currentDetailJob.target_kind}: ${currentDetailJob.target_id || '-'}`
                      : '无'}
                  </span>
                </div>
                <div className={styles.metaItem}>
                  <span className={styles.metaLabel}>尝试次数</span>
                  <span className={styles.metaValue}>
                    第 {currentDetailJob.attempt} 次 (上限 {currentDetailJob.max_attempt} 次)
                  </span>
                </div>
                <div className={styles.metaItem}>
                  <span className={styles.metaLabel}>累计耗时</span>
                  <span className={styles.metaValue}>
                    {formatDuration(currentDetailJob.started_at_ms, currentDetailJob.finished_at_ms)}
                  </span>
                </div>
                <div className={styles.metaItem}>
                  <span className={styles.metaLabel}>创建时间</span>
                  <span className={styles.metaValue}>{formatTime(currentDetailJob.created_at_ms)}</span>
                </div>
                <div className={styles.metaItem}>
                  <span className={styles.metaLabel}>完成时间</span>
                  <span className={styles.metaValue}>{formatTime(currentDetailJob.finished_at_ms)}</span>
                </div>
              </div>

              {/* 错误提示横幅 */}
              {currentDetailJob.error_text && (
                <div className={styles.errorBanner}>
                  <strong>错误原因:</strong> {currentDetailJob.error_text}
                </div>
              )}

              {/* 步骤列表与实时日志 */}
              <div className={styles.stepsSection}>
                <h3 className={styles.sectionTitle}>执行步骤 ({currentDetailJob.steps?.length || 0})</h3>
                {currentDetailJob.steps && currentDetailJob.steps.length > 0 ? (
                  currentDetailJob.steps.map((st) => (
                    <div key={st.id || st.step_index} className={styles.stepCard}>
                      <div className={styles.stepHeader}>
                        <div className={styles.stepTitle}>
                          <span>步骤 {st.step_index + 1}:</span>
                          <span>{st.name}</span>
                          {renderStateBadge(st.step_state)}
                        </div>
                        <div className={styles.stepMeta}>
                          {formatDuration(st.started_at_ms, st.finished_at_ms)}
                        </div>
                      </div>
                      {st.log_text && (
                        <div className={styles.logConsole}>
                          {st.log_text}
                        </div>
                      )}
                    </div>
                  ))
                ) : (
                  <div style={{ color: 'var(--text-dim)', fontSize: 13 }}>暂无步骤数据</div>
                )}
              </div>

              {/* 任务参数 */}
              {currentDetailJob.params && Object.keys(currentDetailJob.params).length > 0 && (
                <div>
                  <h3 className={styles.sectionTitle}>任务输入参数</h3>
                  <pre className={styles.jsonBox}>
                    {JSON.stringify(currentDetailJob.params, null, 2)}
                  </pre>
                </div>
              )}

              {/* 任务结果 */}
              {currentDetailJob.result && Object.keys(currentDetailJob.result).length > 0 && (
                <div>
                  <h3 className={styles.sectionTitle}>执行产出结果</h3>
                  <pre className={styles.jsonBox}>
                    {JSON.stringify(currentDetailJob.result, null, 2)}
                  </pre>
                </div>
              )}
            </div>

            <div className={styles.drawerFooter}>
              <div>
                {(currentDetailJob.job_state === 'running' || currentDetailJob.job_state === 'pending') && (
                  <button
                    type="button"
                    className="btn small danger"
                    onClick={() => cancelMutation.mutate(currentDetailJob.id)}
                  >
                    中断取消任务
                  </button>
                )}
                {(currentDetailJob.job_state === 'failed' || currentDetailJob.job_state === 'cancelled') && (
                  <button
                    type="button"
                    className="btn small primary"
                    onClick={() => retryMutation.mutate(currentDetailJob.id)}
                  >
                    从断点重试任务
                  </button>
                )}
              </div>
              <button
                type="button"
                className="btn small ghost"
                onClick={() => setSelectedJobId(null)}
              >
                关闭
              </button>
            </div>
          </div>
        </div>
      )}

      {/* 新建任务表单 Modal */}
      {isSubmitModalOpen && (
        <div className={styles.formModal} onClick={() => setIsSubmitModalOpen(false)}>
          <div className={styles.formCard} onClick={(e) => e.stopPropagation()}>
            <div className={styles.formHeader}>
              <h2 style={{ fontSize: 16, margin: 0 }}>新建异步任务</h2>
              <button
                type="button"
                className="btn mini ghost"
                onClick={() => setIsSubmitModalOpen(false)}
              >
                ✕
              </button>
            </div>

            <form onSubmit={handleSubmit}>
              <div className={styles.formBody}>
                {submitError && <div className={styles.errorBanner}>{submitError}</div>}

                <div className={styles.formRow}>
                  <label>任务类型 (Kind)</label>
                  <select
                    className={styles.select}
                    value={submitKind}
                    onChange={(e) => setSubmitKind(e.target.value)}
                  >
                    {kindsData?.kinds?.map((k) => (
                      <option key={k.kind} value={k.kind}>
                        {k.kind} - {k.description}
                      </option>
                    )) || <option value="test.three_steps">test.three_steps (内置测试)</option>}
                  </select>
                </div>

                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10 }}>
                  <div className={styles.formRow}>
                    <label>目标类型 (Target Kind, 可选)</label>
                    <input
                      type="text"
                      className={styles.input}
                      placeholder="如 ecs, node, cloud"
                      value={submitTargetKind}
                      onChange={(e) => setSubmitTargetKind(e.target.value)}
                    />
                  </div>
                  <div className={styles.formRow}>
                    <label>目标标识 (Target ID, 可选)</label>
                    <input
                      type="text"
                      className={styles.input}
                      placeholder="如 i-bp123456"
                      value={submitTargetId}
                      onChange={(e) => setSubmitTargetId(e.target.value)}
                    />
                  </div>
                </div>

                <div className={styles.formRow}>
                  <label>最大尝试次数 (Max Attempt)</label>
                  <input
                    type="number"
                    min={1}
                    max={10}
                    className={styles.input}
                    value={submitMaxAttempt}
                    onChange={(e) => setSubmitMaxAttempt(parseInt(e.target.value, 10) || 1)}
                  />
                </div>

                <label className={styles.checkboxRow}>
                  <input
                    type="checkbox"
                    checked={submitRejectIfBusy}
                    onChange={(e) => setSubmitRejectIfBusy(e.target.checked)}
                  />
                  <span>目标忙碌时直接拒绝 (Reject If Busy) 而非排队等待</span>
                </label>

                <div className={styles.formRow}>
                  <label>任务输入参数 (JSON)</label>
                  <textarea
                    className={styles.textarea}
                    value={submitParamsText}
                    onChange={(e) => setSubmitParamsText(e.target.value)}
                    placeholder="输入 JSON 参数对象..."
                  />
                </div>
              </div>

              <div className={styles.formFooter}>
                <button
                  type="button"
                  className="btn small ghost"
                  onClick={() => setIsSubmitModalOpen(false)}
                >
                  取消
                </button>
                <button
                  type="submit"
                  className="btn small primary"
                  disabled={submitMutation.isPending}
                >
                  {submitMutation.isPending ? '提交中...' : '立即提交'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  );
};
