import React, { useState, useEffect } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { getSettings, updateSettings } from '../../api/nodes';
import { changePassword } from '../../api/auth';
import {
  fetchNotifyChannels,
  createNotifyChannel,
  updateNotifyChannel,
  deleteNotifyChannel,
  testNotifyChannel,
  fetchNotifyRules,
  createNotifyRule,
  updateNotifyRule,
  deleteNotifyRule,
  fetchNotifyDeliveries,
} from '../../api/notify';
import { SystemSettings, NotifyChannel, NotifyRule } from '../../api/types';
import styles from './Settings.module.css';

export const Settings: React.FC = () => {
  const queryClient = useQueryClient();
  const [activeTab, setActiveTab] = useState<'system' | 'channels' | 'rules' | 'deliveries'>('system');

  // --- 系统配置表单状态 ---
  const [domain, setDomain] = useState('dash.example.com');
  const [fastInterval, setFastInterval] = useState('5');
  const [slowInterval, setSlowInterval] = useState('60');
  const [enableConns, setEnableConns] = useState(true);

  const [rawRetention, setRawRetention] = useState('3');
  const [oneMinRetention, setOneMinRetention] = useState('30');
  const [oneHourRetention, setOneHourRetention] = useState('365');
  const [oneDayRetention, setOneDayRetention] = useState('0');

  // 修改密码表单状态
  const [oldPassword, setOldPassword] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');

  // 提示信息
  const [notice, setNotice] = useState('');
  const [errorMsg, setErrorMsg] = useState('');
  const [pwdNotice, setPwdNotice] = useState('');
  const [pwdError, setPwdError] = useState('');

  // --- 通知渠道弹窗与表单状态 ---
  const [channelModalOpen, setChannelModalOpen] = useState(false);
  const [editingChannel, setEditingChannel] = useState<NotifyChannel | null>(null);
  const [channelName, setChannelName] = useState('');
  const [channelKind, setChannelKind] = useState<'telegram' | 'webhook'>('telegram');
  const [channelSecret, setChannelSecret] = useState('');
  const [channelConfigJSON, setChannelConfigJSON] = useState('');
  const [channelEnabled, setChannelEnabled] = useState(true);

  // 测试发送状态
  const [testingChannelId, setTestingChannelId] = useState<string | null>(null);

  // --- 路由规则弹窗与表单状态 ---
  const [ruleModalOpen, setRuleModalOpen] = useState(false);
  const [editingRule, setEditingRule] = useState<NotifyRule | null>(null);
  const [ruleName, setRuleName] = useState('');
  const [rulePattern, setRulePattern] = useState('*');
  const [ruleMinSev, setRuleMinSev] = useState<'info' | 'warning' | 'critical'>('warning');
  const [ruleChannelId, setRuleChannelId] = useState('');
  const [ruleThrottle, setRuleThrottle] = useState('0');
  const [ruleEnabled, setRuleEnabled] = useState(true);
  const [ruleQuietStart, setRuleQuietStart] = useState('');
  const [ruleQuietEnd, setRuleQuietEnd] = useState('');

  // --- 投递日志状态 ---
  const [deliveryStateFilter, setDeliveryStateFilter] = useState('');
  const [viewingDeliveryText, setViewingDeliveryText] = useState<string | null>(null);

  // 1. 系统设置 Query
  const { data: settingsData, isLoading: settingsLoading } = useQuery({
    queryKey: ['system-settings'],
    queryFn: getSettings,
  });

  useEffect(() => {
    if (settingsData) {
      if (settingsData['site.domain']) setDomain(settingsData['site.domain']);
      if (settingsData['collect.interval_fast_s'] !== undefined) {
        setFastInterval(String(settingsData['collect.interval_fast_s']));
      }
      if (settingsData['collect.interval_slow_s'] !== undefined) {
        setSlowInterval(String(settingsData['collect.interval_slow_s']));
      }
      if (settingsData['collect.enable_conns'] !== undefined) {
        setEnableConns(!!settingsData['collect.enable_conns']);
      }
      if (settingsData['retention.raw_days'] !== undefined) {
        setRawRetention(String(settingsData['retention.raw_days']));
      }
      if (settingsData['retention.1m_days'] !== undefined) {
        setOneMinRetention(String(settingsData['retention.1m_days']));
      }
      if (settingsData['retention.1h_days'] !== undefined) {
        setOneHourRetention(String(settingsData['retention.1h_days']));
      }
      if (settingsData['retention.1d_days'] !== undefined) {
        setOneDayRetention(String(settingsData['retention.1d_days']));
      }
    }
  }, [settingsData]);

  // 2. 通知渠道 Query
  const { data: channelsData, isLoading: channelsLoading } = useQuery({
    queryKey: ['notify-channels'],
    queryFn: fetchNotifyChannels,
    enabled: activeTab === 'channels' || activeTab === 'rules',
  });

  // 3. 路由规则 Query
  const { data: rulesData, isLoading: rulesLoading } = useQuery({
    queryKey: ['notify-rules'],
    queryFn: fetchNotifyRules,
    enabled: activeTab === 'rules',
  });

  // 4. 投递记录 Query
  const { data: deliveriesData, isLoading: deliveriesLoading } = useQuery({
    queryKey: ['notify-deliveries', deliveryStateFilter],
    queryFn: () => fetchNotifyDeliveries({ state: deliveryStateFilter || undefined, limit: 50 }),
    enabled: activeTab === 'deliveries',
  });

  // 保存系统配置 Mutation
  const updateSettingsMutation = useMutation({
    mutationFn: async (updates: Partial<SystemSettings>) => {
      return await updateSettings(updates);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['system-settings'] });
      setNotice('✓ 系统设置已保存并同步（已下发至在线 Agent）');
      setErrorMsg('');
      setTimeout(() => setNotice(''), 4000);
    },
    onError: (err: any) => {
      setErrorMsg(err?.message || '保存设置失败');
      setNotice('');
    },
  });

  // 修改密码 Mutation
  const changePasswordMutation = useMutation({
    mutationFn: async () => {
      if (!oldPassword || !newPassword) {
        throw new Error('旧密码和新密码不能为空');
      }
      if (newPassword.length < 6) {
        throw new Error('新密码长度不能少于 6 位');
      }
      if (newPassword !== confirmPassword) {
        throw new Error('两次输入的新密码不一致');
      }
      return await changePassword({
        old_password: oldPassword,
        new_password: newPassword,
      });
    },
    onSuccess: () => {
      setPwdNotice('✓ 管理员密码修改成功');
      setPwdError('');
      setOldPassword('');
      setNewPassword('');
      setConfirmPassword('');
      setTimeout(() => setPwdNotice(''), 4000);
    },
    onError: (err: any) => {
      setPwdError(err?.message || '修改密码失败');
      setPwdNotice('');
    },
  });

  // --- 渠道 Mutation ---
  const saveChannelMutation = useMutation({
    mutationFn: async () => {
      if (editingChannel) {
        return await updateNotifyChannel(editingChannel.id, {
          name: channelName,
          is_enabled: channelEnabled,
          secret: channelSecret || undefined,
          config_json: channelConfigJSON,
        });
      } else {
        return await createNotifyChannel({
          name: channelName,
          channel_kind: channelKind,
          secret: channelSecret,
          config_json: channelConfigJSON,
        });
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['notify-channels'] });
      setChannelModalOpen(false);
      setNotice(editingChannel ? '✓ 渠道更新成功' : '✓ 渠道创建成功');
      setTimeout(() => setNotice(''), 3000);
    },
    onError: (err: any) => {
      setErrorMsg(err?.message || '操作渠道失败');
    },
  });

  const deleteChannelMutation = useMutation({
    mutationFn: async (id: string) => {
      return await deleteNotifyChannel(id);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['notify-channels'] });
      queryClient.invalidateQueries({ queryKey: ['notify-rules'] });
      setNotice('✓ 渠道已删除');
      setTimeout(() => setNotice(''), 3000);
    },
    onError: (err: any) => {
      setErrorMsg(err?.message || '删除渠道失败');
    },
  });

  const testChannelMutation = useMutation({
    mutationFn: async (id: string) => {
      setTestingChannelId(id);
      return await testNotifyChannel(id);
    },
    onSuccess: () => {
      setTestingChannelId(null);
      setNotice('✓ 测试消息已送达！');
      setTimeout(() => setNotice(''), 4000);
    },
    onError: (err: any) => {
      setTestingChannelId(null);
      setErrorMsg(err?.message || '测试发送失败');
    },
  });

  // --- 规则 Mutation ---
  const saveRuleMutation = useMutation({
    mutationFn: async () => {
      const startMin = ruleQuietStart !== '' ? parseInt(ruleQuietStart, 10) : null;
      const endMin = ruleQuietEnd !== '' ? parseInt(ruleQuietEnd, 10) : null;
      const throttle = parseInt(ruleThrottle, 10) || 0;

      if (editingRule) {
        return await updateNotifyRule(editingRule.id, {
          name: ruleName,
          is_enabled: ruleEnabled,
          event_pattern: rulePattern,
          min_severity: ruleMinSev,
          notify_channel_id: ruleChannelId,
          throttle_s: throttle,
          quiet_start_min: startMin,
          quiet_end_min: endMin,
        });
      } else {
        return await createNotifyRule({
          name: ruleName,
          is_enabled: ruleEnabled,
          event_pattern: rulePattern,
          min_severity: ruleMinSev,
          notify_channel_id: ruleChannelId,
          throttle_s: throttle,
          quiet_start_min: startMin,
          quiet_end_min: endMin,
        });
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['notify-rules'] });
      setRuleModalOpen(false);
      setNotice(editingRule ? '✓ 规则已更新' : '✓ 规则已创建');
      setTimeout(() => setNotice(''), 3000);
    },
    onError: (err: any) => {
      setErrorMsg(err?.message || '操作规则失败');
    },
  });

  const deleteRuleMutation = useMutation({
    mutationFn: async (id: string) => {
      return await deleteNotifyRule(id);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['notify-rules'] });
      setNotice('✓ 规则已删除');
      setTimeout(() => setNotice(''), 3000);
    },
    onError: (err: any) => {
      setErrorMsg(err?.message || '删除规则失败');
    },
  });

  const handleOpenChannelModal = (ch?: NotifyChannel) => {
    if (ch) {
      setEditingChannel(ch);
      setChannelName(ch.name);
      setChannelKind(ch.channel_kind);
      setChannelSecret('');
      setChannelConfigJSON(ch.config_json);
      setChannelEnabled(ch.is_enabled);
    } else {
      setEditingChannel(null);
      setChannelName('');
      setChannelKind('telegram');
      setChannelSecret('');
      setChannelConfigJSON('{\n  "chat_id": ""\n}');
      setChannelEnabled(true);
    }
    setChannelModalOpen(true);
  };

  const handleOpenRuleModal = (rule?: NotifyRule) => {
    const channels = channelsData || [];
    if (rule) {
      setEditingRule(rule);
      setRuleName(rule.name);
      setRulePattern(rule.event_pattern);
      setRuleMinSev(rule.min_severity);
      setRuleChannelId(rule.notify_channel_id);
      setRuleThrottle(String(rule.throttle_s));
      setRuleEnabled(rule.is_enabled);
      setRuleQuietStart(rule.quiet_start_min !== null && rule.quiet_start_min !== undefined ? String(rule.quiet_start_min) : '');
      setRuleQuietEnd(rule.quiet_end_min !== null && rule.quiet_end_min !== undefined ? String(rule.quiet_end_min) : '');
    } else {
      setEditingRule(null);
      setRuleName('');
      setRulePattern('*');
      setRuleMinSev('warning');
      setRuleChannelId(channels.length > 0 ? channels[0].id : '');
      setRuleThrottle('0');
      setRuleEnabled(true);
      setRuleQuietStart('');
      setRuleQuietEnd('');
    }
    setRuleModalOpen(true);
  };

  const handleSettingsSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const fast = parseInt(fastInterval, 10);
    const slow = parseInt(slowInterval, 10);
    const raw = parseInt(rawRetention, 10);
    const m1 = parseInt(oneMinRetention, 10);
    const h1 = parseInt(oneHourRetention, 10);
    const d1 = parseInt(oneDayRetention, 10);

    if (isNaN(fast) || fast < 1 || fast > 60) {
      setErrorMsg('Fast 采集间隔必须在 1 ~ 60 秒之间');
      return;
    }
    if (isNaN(slow) || slow < 10 || slow > 300) {
      setErrorMsg('Slow 采集间隔必须在 10 ~ 300 秒之间');
      return;
    }

    updateSettingsMutation.mutate({
      'collect.interval_fast_s': fast,
      'collect.interval_slow_s': slow,
      'collect.enable_conns': enableConns,
      'retention.raw_days': isNaN(raw) ? 3 : raw,
      'retention.1m_days': isNaN(m1) ? 30 : m1,
      'retention.1h_days': isNaN(h1) ? 365 : h1,
      'retention.1d_days': isNaN(d1) ? 0 : d1,
    });
  };

  return (
    <div className={styles.container}>
      <div className={styles.header}>
        <h1 className={styles.title}>系统与告警配置</h1>
        <p className={styles.subtitle}>监控采集参数、告警通知渠道、路由规则与投递审计记录</p>
      </div>

      <div className={styles.tabs}>
        <button
          className={`${styles.tab} ${activeTab === 'system' ? styles.active : ''}`}
          onClick={() => setActiveTab('system')}
        >
          系统配置
        </button>
        <button
          className={`${styles.tab} ${activeTab === 'channels' ? styles.active : ''}`}
          onClick={() => setActiveTab('channels')}
        >
          通知渠道
        </button>
        <button
          className={`${styles.tab} ${activeTab === 'rules' ? styles.active : ''}`}
          onClick={() => setActiveTab('rules')}
        >
          路由规则
        </button>
        <button
          className={`${styles.tab} ${activeTab === 'deliveries' ? styles.active : ''}`}
          onClick={() => setActiveTab('deliveries')}
        >
          投递记录
        </button>
      </div>

      {notice && <div className={styles.notice}>{notice}</div>}
      {errorMsg && <div className={styles.errorBanner}>{errorMsg}</div>}

      {/* TAB 1: 系统配置 */}
      {activeTab === 'system' && (
        <>
          <form onSubmit={handleSettingsSubmit} className={styles.section}>
            {/* 1. 站点域名与网络 */}
            <div className="card">
              <div className="card-body">
                <h2 className={styles.sectionTitle}>站点与访问配置</h2>
                <div className={styles.formGrid}>
                  <div className={styles.formGroup}>
                    <label className={styles.label}>访问域名 (settings.site.domain)</label>
                    <input
                      type="text"
                      className={`${styles.input} cell-mono`}
                      value={domain}
                      disabled
                    />
                    <span className={styles.hint}>
                      第一期只读展示，修改需通过 setup.sh 或配置文件调整
                    </span>
                  </div>

                  <div className={styles.formGroup}>
                    <label className={styles.label}>服务端监听端口</label>
                    <input
                      type="text"
                      className={`${styles.input} cell-mono`}
                      value=":8080"
                      disabled
                    />
                    <span className={styles.hint}>由配置文件或环境变量 DASH_SERVER_LISTEN 决定</span>
                  </div>
                </div>
              </div>
            </div>

            {/* 2. 采集频率 */}
            <div className="card">
              <div className="card-body">
                <h2 className={styles.sectionTitle}>Agent 采集参数 (运行期动态下发)</h2>
                <div className={styles.formGrid}>
                  <div className={styles.formGroup}>
                    <label className={styles.label}>Fast 采集间隔 (秒)</label>
                    <input
                      type="number"
                      className={styles.input}
                      value={fastInterval}
                      onChange={(e) => setFastInterval(e.target.value)}
                      min="1"
                      max="60"
                      required
                    />
                    <span className={styles.hint}>
                      默认 5 秒：采样 CPU / 内存 / Swap / 负载 / 流量速率
                    </span>
                  </div>

                  <div className={styles.formGroup}>
                    <label className={styles.label}>Slow 采集间隔 (秒)</label>
                    <input
                      type="number"
                      className={styles.input}
                      value={slowInterval}
                      onChange={(e) => setSlowInterval(e.target.value)}
                      min="10"
                      max="300"
                      required
                    />
                    <span className={styles.hint}>默认 60 秒：采样 磁盘空间 / 进程数 / 连接数</span>
                  </div>

                  <div className={styles.formGroup} style={{ gridColumn: '1 / -1' }}>
                    <label className={styles.checkboxRow}>
                      <input
                        type="checkbox"
                        checked={enableConns}
                        onChange={(e) => setEnableConns(e.target.checked)}
                      />
                      <span>开启 TCP / UDP 连接数采集 (collect.enable_conns)</span>
                    </label>
                    <span className={styles.hint}>
                      ★ 保存后将通过 WebSocket 立即向所有在线 Agent 下发 server.config 通知，无需 Agent 重连。
                    </span>
                  </div>
                </div>
              </div>
            </div>

            {/* 3. 数据保留策略 */}
            <div className="card">
              <div className="card-body">
                <h2 className={styles.sectionTitle}>数据保留策略 (Retention & Rollup)</h2>
                <div className={styles.formGrid}>
                  <div className={styles.formGroup}>
                    <label className={styles.label}>原始采样保留天数 (sample_host)</label>
                    <input
                      type="number"
                      className={styles.input}
                      value={rawRetention}
                      onChange={(e) => setRawRetention(e.target.value)}
                      min="1"
                      required
                    />
                    <span className={styles.hint}>默认 3 天，跨度 ≤ 6 小时查询走此表</span>
                  </div>

                  <div className={styles.formGroup}>
                    <label className={styles.label}>1分钟聚合保留天数 (sample_host_1m)</label>
                    <input
                      type="number"
                      className={styles.input}
                      value={oneMinRetention}
                      onChange={(e) => setOneMinRetention(e.target.value)}
                      min="1"
                      required
                    />
                    <span className={styles.hint}>默认 30 天，跨度 ≤ 3 天查询走此表</span>
                  </div>

                  <div className={styles.formGroup}>
                    <label className={styles.label}>1小时聚合保留天数 (sample_host_1h)</label>
                    <input
                      type="number"
                      className={styles.input}
                      value={oneHourRetention}
                      onChange={(e) => setOneHourRetention(e.target.value)}
                      min="1"
                      required
                    />
                    <span className={styles.hint}>默认 365 天，跨度 ≤ 60 天查询走此表</span>
                  </div>

                  <div className={styles.formGroup}>
                    <label className={styles.label}>1天聚合保留天数 (sample_host_1d)</label>
                    <input
                      type="number"
                      className={styles.input}
                      value={oneDayRetention}
                      onChange={(e) => setOneDayRetention(e.target.value)}
                      min="0"
                      required
                    />
                    <span className={styles.hint}>填 0 表示永久保留，跨度 &gt; 60 天查询走此表</span>
                  </div>
                </div>

                <div className={styles.actions}>
                  <button
                    type="submit"
                    className="btn primary"
                    disabled={settingsLoading || updateSettingsMutation.isPending}
                  >
                    {updateSettingsMutation.isPending ? '正在保存...' : '保存系统设置'}
                  </button>
                </div>
              </div>
            </div>
          </form>

          {/* 4. 修改管理员密码 */}
          <div className="card" style={{ marginTop: 'var(--sp-2)' }}>
            <div className="card-body">
              <h2 className={styles.sectionTitle}>修改管理员密码</h2>
              {pwdNotice && <div className={styles.notice} style={{ marginBottom: 'var(--sp-3)' }}>{pwdNotice}</div>}
              {pwdError && <div className={styles.errorBanner} style={{ marginBottom: 'var(--sp-3)' }}>{pwdError}</div>}

              <form onSubmit={(e) => { e.preventDefault(); changePasswordMutation.mutate(); }}>
                <div className={styles.formGrid}>
                  <div className={styles.formGroup}>
                    <label className={styles.label}>当前旧密码 *</label>
                    <input
                      type="password"
                      className={styles.input}
                      required
                      value={oldPassword}
                      onChange={(e) => setOldPassword(e.target.value)}
                      placeholder="输入当前使用的密码"
                    />
                  </div>

                  <div className={styles.formGroup}>
                    <label className={styles.label}>新密码 (至少 6 位) *</label>
                    <input
                      type="password"
                      className={styles.input}
                      required
                      minLength={6}
                      value={newPassword}
                      onChange={(e) => setNewPassword(e.target.value)}
                      placeholder="输入新密码"
                    />
                  </div>

                  <div className={styles.formGroup}>
                    <label className={styles.label}>确认新密码 *</label>
                    <input
                      type="password"
                      className={styles.input}
                      required
                      minLength={6}
                      value={confirmPassword}
                      onChange={(e) => setConfirmPassword(e.target.value)}
                      placeholder="再次输入新密码"
                    />
                  </div>
                </div>

                <div className={styles.actions}>
                  <button
                    type="submit"
                    className="btn primary"
                    disabled={changePasswordMutation.isPending}
                  >
                    {changePasswordMutation.isPending ? '正在修改...' : '确认修改密码'}
                  </button>
                </div>
              </form>
            </div>
          </div>
        </>
      )}

      {/* TAB 2: 通知渠道 */}
      {activeTab === 'channels' && (
        <div className="card">
          <div className="card-body">
            <div className={styles.sectionHeader}>
              <div>
                <h2 className={styles.sectionTitle}>通知渠道配置</h2>
                <span className={styles.hint}>支持配置多个 Telegram 机器人与 Webhook 接收端（机密凭据信封加密存储）</span>
              </div>
              <button
                type="button"
                className="btn primary"
                onClick={() => handleOpenChannelModal()}
              >
                + 添加渠道
              </button>
            </div>

            {channelsLoading ? (
              <div className={styles.emptyTip}>正在加载渠道列表...</div>
            ) : channelsData && channelsData.length > 0 ? (
              <div className={styles.tableWrapper}>
                <table className={styles.table}>
                  <thead>
                    <tr>
                      <th>渠道名称</th>
                      <th>类型</th>
                      <th>凭据状态</th>
                      <th>配置详情</th>
                      <th>状态</th>
                      <th style={{ textAlign: 'right' }}>操作</th>
                    </tr>
                  </thead>
                  <tbody>
                    {channelsData.map((ch) => (
                      <tr key={ch.id}>
                        <td><strong>{ch.name}</strong></td>
                        <td>
                          <span className={`${styles.badge} ${styles.badgeKind}`}>
                            {ch.channel_kind.toUpperCase()}
                          </span>
                        </td>
                        <td className="cell-mono" style={{ fontSize: '12px' }}>
                          {ch.masked_secret || '无密钥'}
                        </td>
                        <td className="cell-mono" style={{ fontSize: '11px', maxWidth: '240px', wordBreak: 'break-all' }}>
                          {ch.config_json}
                        </td>
                        <td>
                          {ch.is_enabled ? (
                            <span className={`${styles.badge} ${styles.badgeSent}`}>已启用</span>
                          ) : (
                            <span className={`${styles.badge} ${styles.badgeThrottled}`}>已停用</span>
                          )}
                        </td>
                        <td>
                          <div className={styles.cellActions} style={{ justifyContent: 'flex-end' }}>
                            <button
                              type="button"
                              className="btn secondary"
                              disabled={testChannelMutation.isPending && testingChannelId === ch.id}
                              onClick={() => testChannelMutation.mutate(ch.id)}
                            >
                              {testingChannelId === ch.id ? '发送中...' : '测试发送'}
                            </button>
                            <button
                              type="button"
                              className="btn"
                              onClick={() => handleOpenChannelModal(ch)}
                            >
                              编辑
                            </button>
                            <button
                              type="button"
                              className="btn danger"
                              onClick={() => {
                                if (confirm(`确定要删除渠道「${ch.name}」吗？`)) {
                                  deleteChannelMutation.mutate(ch.id);
                                }
                              }}
                            >
                              删除
                            </button>
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <div className={styles.emptyTip}>
                暂未配置通知渠道。点击上方「+ 添加渠道」配置 Telegram 或 Webhook。
              </div>
            )}
          </div>
        </div>
      )}

      {/* TAB 3: 路由规则 */}
      {activeTab === 'rules' && (
        <div className="card">
          <div className="card-body">
            <div className={styles.sectionHeader}>
              <div>
                <h2 className={styles.sectionTitle}>告警路由规则</h2>
                <span className={styles.hint}>决定哪些事件模式、什么级别推送到哪个通知渠道，支持静默期与节流窗口</span>
              </div>
              <button
                type="button"
                className="btn primary"
                onClick={() => handleOpenRuleModal()}
              >
                + 添加规则
              </button>
            </div>

            {rulesLoading ? (
              <div className={styles.emptyTip}>正在加载路由规则...</div>
            ) : rulesData && rulesData.length > 0 ? (
              <div className={styles.tableWrapper}>
                <table className={styles.table}>
                  <thead>
                    <tr>
                      <th>规则名称</th>
                      <th>匹配事件 (通配符)</th>
                      <th>最低级别</th>
                      <th>目标渠道</th>
                      <th>节流窗口</th>
                      <th>静默免打扰</th>
                      <th>状态</th>
                      <th style={{ textAlign: 'right' }}>操作</th>
                    </tr>
                  </thead>
                  <tbody>
                    {rulesData.map((r) => {
                      const ch = channelsData?.find((c) => c.id === r.notify_channel_id);
                      return (
                        <tr key={r.id}>
                          <td><strong>{r.name}</strong></td>
                          <td className="cell-mono">{r.event_pattern}</td>
                          <td>
                            <span className={`${styles.badge} ${
                              r.min_severity === 'critical' ? styles.badgeSeverityCritical :
                              r.min_severity === 'warning' ? styles.badgeSeverityWarning : styles.badgeSeverityInfo
                            }`}>
                              {r.min_severity.toUpperCase()}
                            </span>
                          </td>
                          <td>{ch ? ch.name : r.notify_channel_id}</td>
                          <td>{r.throttle_s > 0 ? `${r.throttle_s} 秒` : '无节流'}</td>
                          <td>
                            {r.quiet_start_min !== null && r.quiet_start_min !== undefined && r.quiet_end_min !== null && r.quiet_end_min !== undefined
                              ? `${Math.floor(r.quiet_start_min / 60).toString().padStart(2, '0')}:${(r.quiet_start_min % 60).toString().padStart(2, '0')} ~ ${Math.floor(r.quiet_end_min / 60).toString().padStart(2, '0')}:${(r.quiet_end_min % 60).toString().padStart(2, '0')}`
                              : '全天生效'}
                          </td>
                          <td>
                            {r.is_enabled ? (
                              <span className={`${styles.badge} ${styles.badgeSent}`}>生效中</span>
                            ) : (
                              <span className={`${styles.badge} ${styles.badgeThrottled}`}>已停用</span>
                            )}
                          </td>
                          <td>
                            <div className={styles.cellActions} style={{ justifyContent: 'flex-end' }}>
                              <button
                                type="button"
                                className="btn"
                                onClick={() => handleOpenRuleModal(r)}
                              >
                                编辑
                              </button>
                              <button
                                type="button"
                                className="btn danger"
                                onClick={() => {
                                  if (confirm(`确定要删除规则「${r.name}」吗？`)) {
                                    deleteRuleMutation.mutate(r.id);
                                  }
                                }}
                              >
                                删除
                              </button>
                            </div>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              </div>
            ) : (
              <div className={styles.emptyTip}>
                暂无路由规则。请先确保已添加至少一个渠道，然后点击「+ 添加规则」。
              </div>
            )}
          </div>
        </div>
      )}

      {/* TAB 4: 投递记录 */}
      {activeTab === 'deliveries' && (
        <div className="card">
          <div className="card-body">
            <div className={styles.sectionHeader}>
              <div>
                <h2 className={styles.sectionTitle}>通知投递审计记录</h2>
                <span className={styles.hint}>记录所有外发通知的状态、重试次数、渲染正文与失败原因（保留 30 天）</span>
              </div>
              <div style={{ display: 'flex', gap: 'var(--sp-2)', alignItems: 'center' }}>
                <span className={styles.label}>按状态筛选:</span>
                <select
                  className={styles.select}
                  value={deliveryStateFilter}
                  onChange={(e) => setDeliveryStateFilter(e.target.value)}
                >
                  <option value="">全部状态</option>
                  <option value="sent">成功 (sent)</option>
                  <option value="failed">失败 (failed)</option>
                  <option value="throttled">节流跳过 (throttled)</option>
                  <option value="quiet_held">免打扰暂扣 (quiet_held)</option>
                  <option value="pending">排队中 (pending)</option>
                </select>
              </div>
            </div>

            {deliveriesLoading ? (
              <div className={styles.emptyTip}>正在加载投递历史...</div>
            ) : deliveriesData?.deliveries && deliveriesData.deliveries.length > 0 ? (
              <div className={styles.tableWrapper}>
                <table className={styles.table}>
                  <thead>
                    <tr>
                      <th>投递状态</th>
                      <th>事件 ID</th>
                      <th>目标渠道 ID</th>
                      <th>重试次数</th>
                      <th>生成时间</th>
                      <th>失败错误 / 详情</th>
                      <th style={{ textAlign: 'right' }}>完整内容</th>
                    </tr>
                  </thead>
                  <tbody>
                    {deliveriesData.deliveries.map((d) => (
                      <tr key={d.id}>
                        <td>
                          <span className={`${styles.badge} ${
                            d.state === 'sent' ? styles.badgeSent :
                            d.state === 'failed' ? styles.badgeFailed :
                            d.state === 'throttled' ? styles.badgeThrottled :
                            d.state === 'quiet_held' ? styles.badgeQuietHeld : styles.badgePending
                          }`}>
                            {d.state}
                          </span>
                        </td>
                        <td className="cell-mono" style={{ fontSize: '11px' }}>{d.event_id}</td>
                        <td className="cell-mono" style={{ fontSize: '11px' }}>{d.channel_id}</td>
                        <td>{d.attempt} 次</td>
                        <td style={{ fontSize: '12px', color: 'var(--text-dim)' }}>
                          {new Date(d.created_at_ms).toLocaleString()}
                        </td>
                        <td style={{ fontSize: '12px', color: d.last_error ? 'var(--err)' : 'inherit', maxWidth: '200px', wordBreak: 'break-all' }}>
                          {d.last_error || '--'}
                        </td>
                        <td style={{ textAlign: 'right' }}>
                          {d.rendered_text ? (
                            <button
                              type="button"
                              className="btn"
                              style={{ fontSize: '11px', padding: '4px 8px' }}
                              onClick={() => setViewingDeliveryText(d.rendered_text || '')}
                            >
                              查看正文
                            </button>
                          ) : '--'}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <div className={styles.emptyTip}>暂无投递记录。</div>
            )}
          </div>
        </div>
      )}

      {/* 弹窗 1: 渠道新建/编辑 */}
      {channelModalOpen && (
        <div className={styles.modalOverlay} onClick={() => setChannelModalOpen(false)}>
          <div className={styles.modal} onClick={(e) => e.stopPropagation()}>
            <h3 className={styles.modalTitle}>
              {editingChannel ? `编辑渠道「${editingChannel.name}」` : '添加通知渠道'}
            </h3>
            <form onSubmit={(e) => { e.preventDefault(); saveChannelMutation.mutate(); }}>
              <div className={styles.formGroup} style={{ marginBottom: 'var(--sp-3)' }}>
                <label className={styles.label}>渠道名称 *</label>
                <input
                  type="text"
                  className={styles.input}
                  required
                  value={channelName}
                  onChange={(e) => setChannelName(e.target.value)}
                  placeholder="例如：运维告警 TG、Webhook 告警"
                />
              </div>

              {!editingChannel && (
                <div className={styles.formGroup} style={{ marginBottom: 'var(--sp-3)' }}>
                  <label className={styles.label}>渠道驱动类型 *</label>
                  <select
                    className={styles.select}
                    value={channelKind}
                    onChange={(e) => {
                      const k = e.target.value as 'telegram' | 'webhook';
                      setChannelKind(k);
                      if (k === 'telegram') {
                        setChannelConfigJSON('{\n  "chat_id": ""\n}');
                      } else {
                        setChannelConfigJSON('{\n  "url": "https://example.com/webhook",\n  "method": "POST"\n}');
                      }
                    }}
                  >
                    <option value="telegram">Telegram Bot</option>
                    <option value="webhook">HTTP Webhook</option>
                  </select>
                </div>
              )}

              <div className={styles.formGroup} style={{ marginBottom: 'var(--sp-3)' }}>
                <label className={styles.label}>
                  {channelKind === 'telegram' ? 'Bot Token (机密凭据)' : 'Webhook HMAC Secret (可选)'}
                </label>
                <input
                  type="password"
                  className={styles.input}
                  value={channelSecret}
                  onChange={(e) => setChannelSecret(e.target.value)}
                  placeholder={editingChannel ? '留空表示保持原有密钥不变' : (channelKind === 'telegram' ? '输入 Telegram 机器人 Token' : '输入可选签名密钥')}
                />
                <span className={styles.hint}>★ 机密信息通过 AES-256-GCM 信封加密保存，不会明文入库或在日志中泄露</span>
              </div>

              <div className={styles.formGroup} style={{ marginBottom: 'var(--sp-3)' }}>
                <label className={styles.label}>渠道配置 JSON *</label>
                <textarea
                  className={styles.textarea}
                  required
                  rows={4}
                  value={channelConfigJSON}
                  onChange={(e) => setChannelConfigJSON(e.target.value)}
                />
                <span className={styles.hint}>
                  {channelKind === 'telegram'
                    ? '包含 chat_id，可选 api_base (反代) / message_thread_id / parse_mode'
                    : '包含 url，可选 method, headers'}
                </span>
              </div>

              <div className={styles.formGroup} style={{ marginBottom: 'var(--sp-3)' }}>
                <label className={styles.checkboxRow}>
                  <input
                    type="checkbox"
                    checked={channelEnabled}
                    onChange={(e) => setChannelEnabled(e.target.checked)}
                  />
                  <span>启用此通知渠道</span>
                </label>
              </div>

              <div className={styles.actions}>
                <button type="button" className="btn" onClick={() => setChannelModalOpen(false)}>
                  取消
                </button>
                <button
                  type="submit"
                  className="btn primary"
                  disabled={saveChannelMutation.isPending}
                >
                  {saveChannelMutation.isPending ? '保存中...' : '确认保存'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* 弹窗 2: 规则新建/编辑 */}
      {ruleModalOpen && (
        <div className={styles.modalOverlay} onClick={() => setRuleModalOpen(false)}>
          <div className={styles.modal} onClick={(e) => e.stopPropagation()}>
            <h3 className={styles.modalTitle}>
              {editingRule ? `编辑规则「${editingRule.name}」` : '添加路由规则'}
            </h3>
            <form onSubmit={(e) => { e.preventDefault(); saveRuleMutation.mutate(); }}>
              <div className={styles.formGroup} style={{ marginBottom: 'var(--sp-3)' }}>
                <label className={styles.label}>规则名称 *</label>
                <input
                  type="text"
                  className={styles.input}
                  required
                  value={ruleName}
                  onChange={(e) => setRuleName(e.target.value)}
                  placeholder="例如：全部离线告警、关键告警推送"
                />
              </div>

              <div className={styles.formGroup} style={{ marginBottom: 'var(--sp-3)' }}>
                <label className={styles.label}>事件匹配表达式 (通配符) *</label>
                <input
                  type="text"
                  className={`${styles.input} cell-mono`}
                  required
                  value={rulePattern}
                  onChange={(e) => setRulePattern(e.target.value)}
                  placeholder="* 或 node.* 或 node.offline"
                />
                <span className={styles.hint}>支持精确匹配或末尾 * 前缀通配（如 node.*、cloud.guard.*、*）</span>
              </div>

              <div className={styles.formGrid} style={{ marginBottom: 'var(--sp-3)' }}>
                <div className={styles.formGroup}>
                  <label className={styles.label}>最低生效级别 *</label>
                  <select
                    className={styles.select}
                    value={ruleMinSev}
                    onChange={(e) => setRuleMinSev(e.target.value as any)}
                  >
                    <option value="info">Info (全推)</option>
                    <option value="warning">Warning (仅告警与严重)</option>
                    <option value="critical">Critical (仅严重)</option>
                  </select>
                </div>

                <div className={styles.formGroup}>
                  <label className={styles.label}>推送目标渠道 *</label>
                  <select
                    className={styles.select}
                    required
                    value={ruleChannelId}
                    onChange={(e) => setRuleChannelId(e.target.value)}
                  >
                    {channelsData?.map((ch) => (
                      <option key={ch.id} value={ch.id}>
                        {ch.name} ({ch.channel_kind})
                      </option>
                    ))}
                  </select>
                </div>
              </div>

              <div className={styles.formGrid} style={{ marginBottom: 'var(--sp-3)' }}>
                <div className={styles.formGroup}>
                  <label className={styles.label}>去重/节流窗口 (秒)</label>
                  <input
                    type="number"
                    className={styles.input}
                    min="0"
                    value={ruleThrottle}
                    onChange={(e) => setRuleThrottle(e.target.value)}
                    placeholder="0 表示不节流"
                  />
                  <span className={styles.hint}>同一 dedup_key 或目标在窗口期内仅外发一次</span>
                </div>

                <div className={styles.formGroup}>
                  <label className={styles.label}>免打扰静默期 (当天分钟 0-1439)</label>
                  <div style={{ display: 'flex', gap: 'var(--sp-1)', alignItems: 'center' }}>
                    <input
                      type="number"
                      className={styles.input}
                      placeholder="起始 (如 0)"
                      min="0"
                      max="1439"
                      value={ruleQuietStart}
                      onChange={(e) => setRuleQuietStart(e.target.value)}
                    />
                    <span>~</span>
                    <input
                      type="number"
                      className={styles.input}
                      placeholder="结束 (如 480)"
                      min="0"
                      max="1439"
                      value={ruleQuietEnd}
                      onChange={(e) => setRuleQuietEnd(e.target.value)}
                    />
                  </div>
                  <span className={styles.hint}>免打扰期内通知暂扣为 quiet_held，不立即打扰</span>
                </div>
              </div>

              <div className={styles.formGroup} style={{ marginBottom: 'var(--sp-3)' }}>
                <label className={styles.checkboxRow}>
                  <input
                    type="checkbox"
                    checked={ruleEnabled}
                    onChange={(e) => setRuleEnabled(e.target.checked)}
                  />
                  <span>启用此路由规则</span>
                </label>
              </div>

              <div className={styles.actions}>
                <button type="button" className="btn" onClick={() => setRuleModalOpen(false)}>
                  取消
                </button>
                <button
                  type="submit"
                  className="btn primary"
                  disabled={saveRuleMutation.isPending}
                >
                  {saveRuleMutation.isPending ? '保存中...' : '确认保存'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* 弹窗 3: 查看完整渲染正文 */}
      {viewingDeliveryText !== null && (
        <div className={styles.modalOverlay} onClick={() => setViewingDeliveryText(null)}>
          <div className={styles.modal} onClick={(e) => e.stopPropagation()}>
            <h3 className={styles.modalTitle}>渲染通知文本预览</h3>
            <pre style={{
              background: 'var(--bg-hover)',
              padding: 'var(--sp-3)',
              borderRadius: 'var(--radius-sm)',
              fontSize: '13px',
              fontFamily: 'var(--font-mono)',
              whiteSpace: 'pre-wrap',
              wordBreak: 'break-all',
              maxHeight: '360px',
              overflowY: 'auto'
            }}>
              {viewingDeliveryText}
            </pre>
            <div className={styles.actions}>
              <button type="button" className="btn primary" onClick={() => setViewingDeliveryText(null)}>
                关闭
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
