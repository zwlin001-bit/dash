import React, { useState, useEffect } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { getSettings, updateSettings } from '../../api/nodes';
import { changePassword } from '../../api/auth';
import { SystemSettings } from '../../api/types';
import styles from './Settings.module.css';

export const Settings: React.FC = () => {
  const queryClient = useQueryClient();

  // 设置项本地表单状态
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

  // 消息提示
  const [notice, setNotice] = useState('');
  const [errorMsg, setErrorMsg] = useState('');
  const [pwdNotice, setPwdNotice] = useState('');
  const [pwdError, setPwdError] = useState('');

  const { data: settingsData, isLoading } = useQuery({
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

  const handlePasswordSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    changePasswordMutation.mutate();
  };

  return (
    <div className={styles.container}>
      <div className={styles.header}>
        <h1 className={styles.title}>系统设置</h1>
        <p className={styles.subtitle}>监控采集参数、数据保留期与管理员账户配置</p>
      </div>

      {notice && <div className={styles.notice}>{notice}</div>}
      {errorMsg && <div className={styles.errorBanner}>{errorMsg}</div>}

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
                disabled={isLoading || updateSettingsMutation.isPending}
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

          <form onSubmit={handlePasswordSubmit}>
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
    </div>
  );
};
