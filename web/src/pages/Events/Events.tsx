import React, { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  fetchEvents,
  fetchUnreadCount,
  fetchEventTypes,
  markEventsRead,
  updateEventType,
  EventRecord,
  EventType,
} from '../../api';
import styles from './Events.module.css';

export const Events: React.FC = () => {
  const queryClient = useQueryClient();
  const [activeTab, setActiveTab] = useState<'timeline' | 'policies'>('timeline');

  // Filters for timeline
  const [filterType, setFilterType] = useState<string>('');
  const [filterSeverity, setFilterSeverity] = useState<string>('');
  const [filterTarget, setFilterTarget] = useState<string>('');
  const [filterRead, setFilterRead] = useState<string>('all');
  const [page, setPage] = useState<number>(0);
  const pageSize = 20;

  // Local state for expanded event payloads
  const [expandedIds, setExpandedIds] = useState<Record<string, boolean>>({});

  // Local edit states for policy rows: { [eventType]: { severity, disposition } }
  const [policyEdits, setPolicyEdits] = useState<
    Record<string, { severity: string; disposition: string }>
  >({});
  const [statusMsg, setStatusMsg] = useState<string>('');

  // Queries
  const { data: unreadData } = useQuery({
    queryKey: ['events-unread-count'],
    queryFn: fetchUnreadCount,
    refetchInterval: 5000,
  });

  const { data: typesData } = useQuery({
    queryKey: ['event-types'],
    queryFn: fetchEventTypes,
  });

  const isReadBool = filterRead === 'all' ? undefined : filterRead === 'read';

  const { data: eventsData, isLoading: eventsLoading, refetch: refetchEvents } = useQuery({
    queryKey: ['events', filterType, filterSeverity, filterTarget, isReadBool, page],
    queryFn: () =>
      fetchEvents({
        event_type: filterType || undefined,
        severity: filterSeverity || undefined,
        target_id: filterTarget || undefined,
        is_read: isReadBool,
        limit: pageSize,
        offset: page * pageSize,
      }),
  });

  // Mutations
  const markReadMutation = useMutation({
    mutationFn: markEventsRead,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['events'] });
      queryClient.invalidateQueries({ queryKey: ['events-unread-count'] });
    },
  });

  const updateTypeMutation = useMutation({
    mutationFn: ({
      eventType,
      severity,
      disposition,
    }: {
      eventType: string;
      severity?: string;
      disposition?: string;
    }) => updateEventType(eventType, { severity, disposition }),
    onSuccess: (updated) => {
      setStatusMsg(`已更新类型策略：${updated.display_name} (${updated.event_type})`);
      setTimeout(() => setStatusMsg(''), 3000);
      queryClient.invalidateQueries({ queryKey: ['event-types'] });
    },
  });

  const handleMarkAllRead = () => {
    markReadMutation.mutate({ all: true });
  };

  const handleMarkSingleRead = (id: string) => {
    markReadMutation.mutate({ ids: [id] });
  };

  const toggleExpand = (id: string) => {
    setExpandedIds((prev) => ({ ...prev, [id]: !prev[id] }));
  };

  const handlePolicyChange = (
    eventType: string,
    field: 'severity' | 'disposition',
    val: string,
    currentType: EventType
  ) => {
    setPolicyEdits((prev) => {
      const existing = prev[eventType] || {
        severity: currentType.severity,
        disposition: currentType.disposition,
      };
      return {
        ...prev,
        [eventType]: {
          ...existing,
          [field]: val,
        },
      };
    });
  };

  const handleSavePolicy = (t: EventType) => {
    const edits = policyEdits[t.event_type] || {
      severity: t.severity,
      disposition: t.disposition,
    };
    updateTypeMutation.mutate({
      eventType: t.event_type,
      severity: edits.severity,
      disposition: edits.disposition,
    });
  };

  const unreadCount = unreadData?.unread_count || 0;
  const totalItems = eventsData?.total || 0;
  const totalPages = Math.ceil(totalItems / pageSize) || 1;

  const formatTime = (tsMs: number) => {
    return new Date(tsMs).toLocaleString('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
    });
  };

  const renderSeverityBadge = (severity: string) => {
    let cls = styles.sevInfo;
    if (severity === 'warning') cls = styles.sevWarning;
    if (severity === 'critical') cls = styles.sevCritical;

    return <span className={`${styles.severityPill} ${cls}`}>{severity}</span>;
  };

  return (
    <div className={styles.container}>
      <header className={styles.header}>
        <div className={styles.titleArea}>
          <h1 className={styles.title}>事件总线</h1>
          {unreadCount > 0 && (
            <span className={styles.unreadPill}>{unreadCount} 条未读</span>
          )}
        </div>

        <div className={styles.tabs}>
          <button
            type="button"
            className={`${styles.tabBtn} ${activeTab === 'timeline' ? styles.tabBtnActive : ''}`}
            onClick={() => setActiveTab('timeline')}
          >
            事件时间线
          </button>
          <button
            type="button"
            className={`${styles.tabBtn} ${activeTab === 'policies' ? styles.tabBtnActive : ''}`}
            onClick={() => setActiveTab('policies')}
          >
            类型与处置策略
          </button>
        </div>
      </header>

      {statusMsg && <div className={styles.notice}>{statusMsg}</div>}

      {activeTab === 'timeline' ? (
        <>
          <div className={styles.filterCard}>
            <div className={styles.filterGroup}>
              <span className={styles.label}>类型:</span>
              <select
                className={styles.select}
                value={filterType}
                onChange={(e) => {
                  setFilterType(e.target.value);
                  setPage(0);
                }}
              >
                <option value="">全部类型</option>
                {typesData?.map((t) => (
                  <option key={t.event_type} value={t.event_type}>
                    {t.display_name} ({t.event_type})
                  </option>
                ))}
              </select>
            </div>

            <div className={styles.filterGroup}>
              <span className={styles.label}>级别:</span>
              <select
                className={styles.select}
                value={filterSeverity}
                onChange={(e) => {
                  setFilterSeverity(e.target.value);
                  setPage(0);
                }}
              >
                <option value="">全部级别</option>
                <option value="info">info (信息)</option>
                <option value="warning">warning (警告)</option>
                <option value="critical">critical (严重)</option>
              </select>
            </div>

            <div className={styles.filterGroup}>
              <span className={styles.label}>目标:</span>
              <input
                type="text"
                className={styles.input}
                placeholder="节点 / 目标 ID"
                value={filterTarget}
                onChange={(e) => {
                  setFilterTarget(e.target.value);
                  setPage(0);
                }}
              />
            </div>

            <div className={styles.filterGroup}>
              <span className={styles.label}>状态:</span>
              <select
                className={styles.select}
                value={filterRead}
                onChange={(e) => {
                  setFilterRead(e.target.value);
                  setPage(0);
                }}
              >
                <option value="all">全部</option>
                <option value="unread">仅未读</option>
                <option value="read">已读</option>
              </select>
            </div>

            <div className={styles.actions}>
              <button
                type="button"
                className="btn mini"
                onClick={() => refetchEvents()}
                title="刷新列表"
              >
                刷新
              </button>
              {unreadCount > 0 && (
                <button
                  type="button"
                  className="btn mini primary"
                  onClick={handleMarkAllRead}
                  disabled={markReadMutation.isPending}
                >
                  全部标为已读
                </button>
              )}
            </div>
          </div>

          {eventsLoading ? (
            <div className={styles.emptyState}>加载中...</div>
          ) : !eventsData?.events || eventsData.events.length === 0 ? (
            <div className={styles.emptyState}>暂无符合过滤条件的事件</div>
          ) : (
            <div className={styles.timelineList}>
              {eventsData.events.map((event: EventRecord) => {
                const isExpanded = !!expandedIds[event.id];
                return (
                  <div
                    key={event.id}
                    className={`${styles.eventCard} ${
                      event.is_read ? styles.eventCardRead : styles.eventCardUnread
                    }`}
                  >
                    <div className={styles.eventHeader}>
                      {renderSeverityBadge(event.severity)}
                      <span className={styles.eventTypeBadge}>{event.event_type}</span>
                      <span className={styles.eventTitle}>{event.title}</span>

                      <div style={{ marginLeft: 'auto', display: 'flex', gap: '8px' }}>
                        {!event.is_read && (
                          <button
                            type="button"
                            className="btn mini ghost"
                            onClick={() => handleMarkSingleRead(event.id)}
                          >
                            标为已读
                          </button>
                        )}
                        <button
                          type="button"
                          className="btn mini"
                          onClick={() => toggleExpand(event.id)}
                        >
                          {isExpanded ? '收起详情' : '详情'}
                        </button>
                      </div>
                    </div>

                    <div className={styles.metaRow}>
                      <span className={styles.time}>{formatTime(event.occurred_at_ms)}</span>
                      <span>来源: {event.source_module}</span>
                      {event.target_kind && (
                        <span>
                          目标: {event.target_kind}
                          {event.target_id ? ` (${event.target_id})` : ''}
                        </span>
                      )}
                      {event.dedup_key && <span>去重标识: {event.dedup_key}</span>}
                    </div>

                    {isExpanded && (
                      <div className={styles.payloadBox}>
                        <div>
                          <strong>ID:</strong> {event.id}
                        </div>
                        {event.payload && (
                          <div style={{ marginTop: '4px' }}>
                            <strong>Payload:</strong>
                            <pre style={{ margin: '4px 0 0 0' }}>
                              {JSON.stringify(event.payload, null, 2)}
                            </pre>
                          </div>
                        )}
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          )}

          {totalPages > 1 && (
            <div className={styles.pagination}>
              <span>
                第 {page + 1} 页 / 共 {totalPages} 页 (总计 {totalItems} 条)
              </span>
              <div style={{ display: 'flex', gap: '8px' }}>
                <button
                  type="button"
                  className="btn mini"
                  disabled={page === 0}
                  onClick={() => setPage((p) => Math.max(0, p - 1))}
                >
                  上一页
                </button>
                <button
                  type="button"
                  className="btn mini"
                  disabled={page + 1 >= totalPages}
                  onClick={() => setPage((p) => p + 1)}
                >
                  下一页
                </button>
              </div>
            </div>
          )}
        </>
      ) : (
        <div className={styles.tableCard}>
          <div style={{ padding: '12px 16px', borderBottom: '1px solid var(--border-main)', fontSize: '13px', color: 'var(--text-dim)' }}>
            ★ 事件类型由系统代码定义，界面不允许增删；用户可按需覆盖级别及处置方式（支持 drop 丢弃、store 入库、store+ui 提醒、store+notify 外发）。
          </div>
          <table className={styles.table}>
            <thead>
              <tr>
                <th>类型编码</th>
                <th>显示名称</th>
                <th>默认级别</th>
                <th>生效级别</th>
                <th>默认处置</th>
                <th>生效处置</th>
                <th>说明</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {typesData?.map((t: EventType) => {
                const currentEdit = policyEdits[t.event_type] || {
                  severity: t.severity,
                  disposition: t.disposition,
                };
                const isModified =
                  currentEdit.severity !== t.severity ||
                  currentEdit.disposition !== t.disposition;

                return (
                  <tr key={t.event_type}>
                    <td className={styles.typeCode}>{t.event_type}</td>
                    <td style={{ fontWeight: 500 }}>{t.display_name}</td>
                    <td>{renderSeverityBadge(t.default_severity)}</td>
                    <td>
                      <select
                        className={styles.select}
                        value={currentEdit.severity}
                        onChange={(e) =>
                          handlePolicyChange(t.event_type, 'severity', e.target.value, t)
                        }
                      >
                        <option value="info">info</option>
                        <option value="warning">warning</option>
                        <option value="critical">critical</option>
                      </select>
                    </td>
                    <td>
                      <span className="badge badge-neutral">{t.default_disposition}</span>
                    </td>
                    <td>
                      <select
                        className={styles.select}
                        value={currentEdit.disposition}
                        onChange={(e) =>
                          handlePolicyChange(t.event_type, 'disposition', e.target.value, t)
                        }
                      >
                        <option value="drop">drop (不入库)</option>
                        <option value="store">store (仅入库)</option>
                        <option value="store+ui">store+ui (入库+未读)</option>
                        <option value="store+notify">store+notify (入库+通知)</option>
                      </select>
                    </td>
                    <td style={{ color: 'var(--text-dim)', fontSize: '12px' }}>
                      {t.description || '-'}
                    </td>
                    <td>
                      <button
                        type="button"
                        className="btn mini primary"
                        disabled={!isModified || updateTypeMutation.isPending}
                        onClick={() => handleSavePolicy(t)}
                      >
                        保存
                      </button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
};
