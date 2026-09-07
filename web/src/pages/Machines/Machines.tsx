import React, { useState } from 'react';
import styles from './Machines.module.css';

interface MachineItem {
  id: string;
  name: string;
  provider: string;
  region: string;
  ip: string;
  status: 'running' | 'stopped' | 'error';
  cost: string;
  lastSeen: string;
}

const MOCK_MACHINES: MachineItem[] = [
  {
    id: 'm-ali-01',
    name: 'aliyun-hk-01',
    provider: 'Alibaba Cloud',
    region: 'cn-hongkong',
    ip: '47.242.88.12',
    status: 'running',
    cost: '$4.50/mo',
    lastSeen: '10s 前',
  },
  {
    id: 'm-gcp-01',
    name: 'gcp-us-central',
    provider: 'Google Cloud',
    region: 'us-central1-a',
    ip: '34.120.45.19',
    status: 'running',
    cost: '$0.00/mo (Free tier)',
    lastSeen: '15s 前',
  },
  {
    id: 'm-ora-01',
    name: 'oracle-tokyo',
    provider: 'Oracle Cloud',
    region: 'ap-tokyo-1',
    ip: '140.238.12.90',
    status: 'running',
    cost: '$0.00/mo (Always Free)',
    lastSeen: '25s 前',
  },
  {
    id: 'm-dgo-01',
    name: 'do-sgp-edge',
    provider: 'DigitalOcean',
    region: 'sgp1',
    ip: '128.199.200.44',
    status: 'stopped',
    cost: '$6.00/mo',
    lastSeen: '2d 前',
  },
];

export const Machines: React.FC = () => {
  const [searchTerm, setSearchTerm] = useState('');

  const filteredMachines = MOCK_MACHINES.filter(
    (m) =>
      m.name.toLowerCase().includes(searchTerm.toLowerCase()) ||
      m.provider.toLowerCase().includes(searchTerm.toLowerCase()) ||
      m.ip.includes(searchTerm)
  );

  return (
    <div className={styles.container}>
      <div className={styles.header}>
        <div>
          <h1 className={styles.title}>机器清单</h1>
          <p className={styles.subtitle}>跨云厂商资产管理与实例生命周期控制</p>
        </div>
        <div className={styles.toolbar}>
          <input
            type="text"
            className={styles.searchInput}
            placeholder="搜索机器名称 / IP / 厂商..."
            value={searchTerm}
            onChange={(e) => setSearchTerm(e.target.value)}
          />
          <button className="btn btn-primary" onClick={() => alert('添加机器接口将在任务 17 对接')}>
            + 接入新机器
          </button>
        </div>
      </div>

      <div className="card">
        <div style={{ overflowX: 'auto' }}>
          <table className="tbl">
            <thead>
              <tr>
                <th>实例名称</th>
                <th>云厂商</th>
                <th>地域</th>
                <th>公网 IP</th>
                <th>状态</th>
                <th>成本估算</th>
                <th>最近心跳</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {filteredMachines.length === 0 ? (
                <tr>
                  <td colSpan={8} className={styles.emptyState}>
                    未找到匹配的机器记录
                  </td>
                </tr>
              ) : (
                filteredMachines.map((m) => (
                  <tr key={m.id}>
                    <td>
                      <div style={{ fontWeight: 500, color: 'var(--text-primary)' }}>{m.name}</div>
                      <div className="cell-mono" style={{ fontSize: 11, color: 'var(--text-muted)' }}>
                        {m.id}
                      </div>
                    </td>
                    <td>
                      <span className="pill pill-default">{m.provider}</span>
                    </td>
                    <td className="cell-mono">{m.region}</td>
                    <td className="cell-mono">{m.ip}</td>
                    <td>
                      <span
                        className={`badge ${
                          m.status === 'running'
                            ? 'badge-success'
                            : m.status === 'stopped'
                            ? 'badge-warning'
                            : 'badge-danger'
                        }`}
                      >
                        <span
                          className={`health health-${
                            m.status === 'running'
                              ? 'good'
                              : m.status === 'stopped'
                              ? 'warn'
                              : 'error'
                          }`}
                          style={{ marginRight: 6 }}
                        />
                        {m.status === 'running' ? '运行中' : m.status === 'stopped' ? '已停止' : '异常'}
                      </span>
                    </td>
                    <td className="cell-mono">{m.cost}</td>
                    <td style={{ color: 'var(--text-secondary)' }}>{m.lastSeen}</td>
                    <td>
                      <button
                        className="btn btn-secondary"
                        style={{ padding: '4px 10px', fontSize: 12 }}
                        onClick={() => alert(`管理实例 ${m.name}`)}
                      >
                        管理
                      </button>
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
};
