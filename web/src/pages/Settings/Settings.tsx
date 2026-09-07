import React, { useState } from 'react';
import styles from './Settings.module.css';

export const Settings: React.FC = () => {
  const [domain] = useState('dash.internal.net');
  const [fastInterval, setFastInterval] = useState('5');
  const [slowInterval, setSlowInterval] = useState('60');
  const [rawRetention, setRawRetention] = useState('2');
  const [oneMinRetention, setOneMinRetention] = useState('30');
  const [oneHourRetention, setOneHourRetention] = useState('365');
  const [saved, setSaved] = useState(false);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    setSaved(true);
    setTimeout(() => setSaved(false), 2500);
  };

  return (
    <div className={styles.container}>
      <div className={styles.header}>
        <h1 className={styles.title}>系统设置</h1>
        <p className={styles.subtitle}>监控采集参数、数据保留期与站点配置</p>
      </div>

      <form onSubmit={handleSubmit} className={styles.section}>
        <div className="card">
          <h2 className={styles.sectionTitle}>站点与自举配置</h2>
          <div className={styles.formGrid}>
            <div className={styles.formGroup}>
              <label className={styles.label}>访问域名 (settings.site.domain)</label>
              <input
                type="text"
                className={`${styles.input} cell-mono`}
                value={domain}
                disabled
              />
              <span className={styles.hint}>第一期只读展示，修改需通过 setup.sh 执行</span>
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

        <div className="card">
          <h2 className={styles.sectionTitle}>Agent 采集参数</h2>
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
              />
              <span className={styles.hint}>默认 5 秒：CPU / 内存 / Swap / 负载 / 流量速率</span>
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
              />
              <span className={styles.hint}>默认 60 秒：磁盘空间 / 进程数 / 连接数</span>
            </div>
          </div>
        </div>

        <div className="card">
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
              />
              <span className={styles.hint}>默认 2 天，跨度 ≤ 6 小时查询走此表</span>
            </div>

            <div className={styles.formGroup}>
              <label className={styles.label}>1分钟聚合保留天数 (sample_host_1m)</label>
              <input
                type="number"
                className={styles.input}
                value={oneMinRetention}
                onChange={(e) => setOneMinRetention(e.target.value)}
                min="7"
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
                min="30"
              />
              <span className={styles.hint}>默认 365 天，跨度 ≤ 60 天查询走此表</span>
            </div>
          </div>

          <div className={styles.actions}>
            {saved && (
              <span style={{ color: 'var(--color-success)', fontSize: 13, alignSelf: 'center' }}>
                ✓ 配置已保存 (Mock)
              </span>
            )}
            <button type="submit" className="btn btn-primary">
              保存配置
            </button>
          </div>
        </div>
      </form>
    </div>
  );
};
