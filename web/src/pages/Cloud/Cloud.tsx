import React, { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  fetchCredentials,
  createCredential,
  deleteCredential,
  fetchCloudAccounts,
  createCloudAccount,
  deleteCloudAccount,
  triggerCloudSync,
  fetchCloudResources,
  actionCloudResource,
  discoverCloudResources,
  CloudAccount,
  CloudResource,
  DiscoveredResource,
} from '../../api/cloud';
import styles from './Cloud.module.css';

export const Cloud: React.FC = () => {
  const queryClient = useQueryClient();
  const [activeTab, setActiveTab] = useState<'resources' | 'accounts'>('resources');

  // Filters
  const [filterAccount, setFilterAccount] = useState<string>('');
  const [filterRegion, setFilterRegion] = useState<string>('');
  const [filterStatus, setFilterStatus] = useState<string>('');

  // Modals
  const [showAddCredModal, setShowAddCredModal] = useState<boolean>(false);
  const [showAddAccModal, setShowAddAccModal] = useState<boolean>(false);
  const [showDiscoverModal, setShowDiscoverModal] = useState<boolean>(false);

  // Forms
  const [newCredName, setNewCredName] = useState<string>('');
  const [newAK, setNewAK] = useState<string>('');
  const [newSK, setNewSK] = useState<string>('');

  const [newAccName, setNewAccName] = useState<string>('');
  const [newAccCredID, setNewAccCredID] = useState<string>('');
  const [newAccRegion, setNewAccRegion] = useState<string>('cn-hongkong');
  const [newAccSite, setNewAccSite] = useState<string>('china');

  // Discover state
  const [selectedCredForDiscover, setSelectedCredForDiscover] = useState<string>('');
  const [discoveredList, setDiscoveredList] = useState<DiscoveredResource[]>([]);
  const [isDiscovering, setIsDiscovering] = useState<boolean>(false);
  const [syncingAccountId, setSyncingAccountId] = useState<string | null>(null);

  // Queries
  const { data: credentials = [] } = useQuery({
    queryKey: ['credentials'],
    queryFn: fetchCredentials,
  });

  const { data: accounts = [], refetch: refetchAccounts } = useQuery({
    queryKey: ['cloud-accounts'],
    queryFn: fetchCloudAccounts,
  });

  const { data: resources = [], isLoading: isLoadingResources, refetch: refetchResources } = useQuery({
    queryKey: ['cloud-resources', filterAccount, filterRegion, filterStatus],
    queryFn: () =>
      fetchCloudResources({
        account_id: filterAccount || undefined,
        region: filterRegion || undefined,
        status: filterStatus || undefined,
      }),
  });

  // Mutations
  const createCredMutation = useMutation({
    mutationFn: createCredential,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['credentials'] });
      setShowAddCredModal(false);
      setNewCredName('');
      setNewAK('');
      setNewSK('');
    },
    onError: (err: any) => {
      alert('录入凭据失败: ' + (err?.response?.data?.error?.message || err.message));
    },
  });

  const deleteCredMutation = useMutation({
    mutationFn: deleteCredential,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['credentials'] });
    },
    onError: (err: any) => {
      alert('删除凭据失败: ' + (err?.response?.data?.error?.message || err.message));
    },
  });

  const createAccMutation = useMutation({
    mutationFn: createCloudAccount,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['cloud-accounts'] });
      setShowAddAccModal(false);
      setNewAccName('');
    },
    onError: (err: any) => {
      alert('添加云账号失败: ' + (err?.response?.data?.error?.message || err.message));
    },
  });

  const deleteAccMutation = useMutation({
    mutationFn: deleteCloudAccount,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['cloud-accounts'] });
      queryClient.invalidateQueries({ queryKey: ['cloud-resources'] });
    },
    onError: (err: any) => {
      alert('删除账号失败: ' + (err?.response?.data?.error?.message || err.message));
    },
  });

  const handleSync = async (account: CloudAccount) => {
    try {
      setSyncingAccountId(account.id);
      await triggerCloudSync(account.id);
      setTimeout(() => {
        refetchAccounts();
        refetchResources();
        setSyncingAccountId(null);
      }, 2000);
    } catch (err: any) {
      setSyncingAccountId(null);
      alert('触发同步失败: ' + (err?.response?.data?.error?.message || err.message));
    }
  };

  const handleAction = async (resource: CloudResource, action: 'start' | 'stop') => {
    const actionText = action === 'start' ? '启动' : '停止';
    if (!window.confirm(`确定要对实例 ${resource.name || resource.res_ref} 执行【${actionText}】操作吗？`)) {
      return;
    }
    try {
      await actionCloudResource(resource.id, action);
      alert(`已成功发送【${actionText}】指令，请等待云平台完成状态转换。`);
      setTimeout(refetchResources, 2000);
    } catch (err: any) {
      alert(`操作失败: ` + (err?.response?.data?.error?.message || err.message));
    }
  };

  const handleRunDiscover = async () => {
    if (!selectedCredForDiscover) {
      alert('请选择凭据');
      return;
    }
    setIsDiscovering(true);
    try {
      const items = await discoverCloudResources({
        credential_id: selectedCredForDiscover,
        regions: ['cn-hangzhou', 'cn-shanghai', 'cn-beijing', 'cn-shenzhen', 'cn-hongkong', 'ap-southeast-1'],
      });
      setDiscoveredList(items);
    } catch (err: any) {
      alert('自动发现失败: ' + (err?.response?.data?.error?.message || err.message));
    } finally {
      setIsDiscovering(false);
    }
  };

  const formatBytes = (bytes: number) => {
    if (!bytes || bytes <= 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
  };

  return (
    <div className={styles.pageContainer}>
      <div className={styles.header}>
        <div>
          <h1 className={styles.title}>云资产管理</h1>
          <div className={styles.subtitle}>阿里云 ECS 实例管理、信封加密凭据与 CDT 流量监控</div>
        </div>
      </div>

      <div className={styles.tabs}>
        <button
          className={`${styles.tabBtn} ${activeTab === 'resources' ? styles.tabBtnActive : ''}`}
          onClick={() => setActiveTab('resources')}
        >
          云资源列表 ({resources.length})
        </button>
        <button
          className={`${styles.tabBtn} ${activeTab === 'accounts' ? styles.tabBtnActive : ''}`}
          onClick={() => setActiveTab('accounts')}
        >
          云账号与凭据 ({accounts.length})
        </button>
      </div>

      {activeTab === 'resources' && (
        <div className={styles.sectionCard}>
          <div className={styles.filterBar}>
            <select
              className={styles.select}
              value={filterAccount}
              onChange={(e) => setFilterAccount(e.target.value)}
            >
              <option value="">全部账号</option>
              {accounts.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
            </select>

            <select
              className={styles.select}
              value={filterRegion}
              onChange={(e) => setFilterRegion(e.target.value)}
            >
              <option value="">全部区域</option>
              <option value="cn-hongkong">cn-hongkong</option>
              <option value="cn-hangzhou">cn-hangzhou</option>
              <option value="cn-shanghai">cn-shanghai</option>
              <option value="cn-beijing">cn-beijing</option>
              <option value="ap-southeast-1">ap-southeast-1</option>
            </select>

            <select
              className={styles.select}
              value={filterStatus}
              onChange={(e) => setFilterStatus(e.target.value)}
            >
              <option value="">全部状态</option>
              <option value="running">运行中 (running)</option>
              <option value="stopped">已停止 (stopped)</option>
            </select>

            <button className="btn mini secondary" onClick={() => refetchResources()}>
              🔄 刷新
            </button>
          </div>

          {isLoadingResources ? (
            <div style={{ padding: '30px', textAlign: 'center', color: 'var(--text-dim)' }}>
              加载资源中...
            </div>
          ) : resources.length === 0 ? (
            <div style={{ padding: '40px', textAlign: 'center', color: 'var(--text-dim)' }}>
              暂未同步到任何云资源。请切换到「云账号与凭据」标签页添加云账号并点击「立即同步」。
            </div>
          ) : (
            <table className={styles.table}>
              <thead>
                <tr>
                  <th>实例名称 / ID</th>
                  <th>厂商 / 区域</th>
                  <th>状态</th>
                  <th>IP 地址</th>
                  <th>规格</th>
                  <th>费用 / 异常</th>
                  <th>操作</th>
                </tr>
              </thead>
              <tbody>
                {resources.map((r) => {
                  let specs: any = {};
                  try {
                    if (r.specs_json) specs = JSON.parse(r.specs_json);
                  } catch (e) {}

                  return (
                    <tr key={r.id}>
                      <td>
                        <div style={{ fontWeight: 600 }}>{r.name || r.res_ref}</div>
                        <div style={{ fontSize: '11px', color: 'var(--text-dim)' }}>{r.res_ref}</div>
                      </td>
                      <td>
                        <div>阿里云</div>
                        <div style={{ fontSize: '11px', color: 'var(--text-dim)' }}>{r.region}</div>
                      </td>
                      <td>
                        <span
                          className={`${styles.badge} ${
                            r.status === 'running'
                              ? styles.badgeRunning
                              : r.status === 'stopped'
                              ? styles.badgeStopped
                              : styles.badgeStarting
                          }`}
                        >
                          {r.status}
                        </span>
                      </td>
                      <td>
                        <div>公: {r.public_ips || '-'}</div>
                        <div style={{ fontSize: '11px', color: 'var(--text-dim)' }}>内: {r.private_ips || '-'}</div>
                      </td>
                      <td>
                        {specs.vcpu ? `${specs.vcpu}核 ${specs.mem_mb ? specs.mem_mb / 1024 + 'GB' : ''}` : '-'}
                      </td>
                      <td>
                        {r.attrs_json && JSON.parse(r.attrs_json || '{}').bill_error ? (
                          <span style={{ color: 'var(--warn)', fontSize: '11px' }} title={JSON.parse(r.attrs_json).bill_error}>
                            ⚠️ 账单暂不可用
                          </span>
                        ) : (
                          <span>正常</span>
                        )}
                      </td>
                      <td>
                        {r.status === 'running' ? (
                          <button
                            className="btn mini danger"
                            onClick={() => handleAction(r, 'stop')}
                          >
                            停止
                          </button>
                        ) : (
                          <button
                            className="btn mini primary"
                            onClick={() => handleAction(r, 'start')}
                          >
                            启动
                          </button>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          )}
        </div>
      )}

      {activeTab === 'accounts' && (
        <>
          {/* 云账号列表 */}
          <div className={styles.sectionCard}>
            <div className={styles.sectionHeader}>
              <h2 className={styles.sectionTitle}>云账号 ({accounts.length})</h2>
              <div style={{ display: 'flex', gap: '8px' }}>
                <button className="btn mini secondary" onClick={() => setShowDiscoverModal(true)}>
                  🔍 自动发现实例
                </button>
                <button className="btn mini primary" onClick={() => setShowAddAccModal(true)}>
                  + 添加云账号
                </button>
              </div>
            </div>

            {accounts.length === 0 ? (
              <div style={{ padding: '30px', textAlign: 'center', color: 'var(--text-dim)' }}>
                暂无云账号，请点击右上角「+ 添加云账号」开始接入。
              </div>
            ) : (
              <table className={styles.table}>
                <thead>
                  <tr>
                    <th>账号名称</th>
                    <th>厂商</th>
                    <th>站点</th>
                    <th>默认区域</th>
                    <th>最近同步</th>
                    <th>CDT 公网流量</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {accounts.map((acc) => {
                    let cdtBytes = 0;
                    try {
                      if (acc.config_json) {
                        const parsed = JSON.parse(acc.config_json);
                        cdtBytes = parsed.cdt_traffic_bytes || 0;
                      }
                    } catch (e) {}

                    return (
                      <tr key={acc.id}>
                        <td style={{ fontWeight: 600 }}>{acc.name}</td>
                        <td>阿里云</td>
                        <td>{acc.account_site === 'international' ? '国际站' : '中国站'}</td>
                        <td>{acc.default_region || '全部'}</td>
                        <td>
                          {acc.last_sync_at_ms
                            ? new Date(acc.last_sync_at_ms).toLocaleString()
                            : '未同步'}
                        </td>
                        <td style={{ fontWeight: 600, color: 'var(--accent)' }}>
                          {formatBytes(cdtBytes)}
                        </td>
                        <td>
                          <div style={{ display: 'flex', gap: '6px' }}>
                            <button
                              className="btn mini secondary"
                              disabled={syncingAccountId === acc.id}
                              onClick={() => handleSync(acc)}
                            >
                              {syncingAccountId === acc.id ? '同步中...' : '🔄 立即同步'}
                            </button>
                            <button
                              className="btn mini ghost"
                              style={{ color: 'var(--err)' }}
                              onClick={() => {
                                if (window.confirm(`确定删除账号 ${acc.name} 吗？`)) {
                                  deleteAccMutation.mutate(acc.id);
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
            )}
          </div>

          {/* 凭据管理 */}
          <div className={styles.sectionCard}>
            <div className={styles.sectionHeader}>
              <div>
                <h2 className={styles.sectionTitle}>凭据管理 (信封加密)</h2>
                <div style={{ fontSize: '12px', color: 'var(--text-dim)', marginTop: '2px' }}>
                  ★ 采用 AES-256-GCM 信封加密，主密钥永不进库，明文仅在 RPC 通信内存中瞬时留存
                </div>
              </div>
              <button className="btn mini primary" onClick={() => setShowAddCredModal(true)}>
                + 录入新凭据
              </button>
            </div>

            {credentials.length === 0 ? (
              <div style={{ padding: '20px', textAlign: 'center', color: 'var(--text-dim)' }}>
                暂无凭据，请录入阿里云 AK/SK。
              </div>
            ) : (
              <table className={styles.table}>
                <thead>
                  <tr>
                    <th>凭据名称</th>
                    <th>类型</th>
                    <th>安全指纹 (掩码)</th>
                    <th>录入时间</th>
                    <th>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {credentials.map((c) => (
                    <tr key={c.id}>
                      <td style={{ fontWeight: 600 }}>{c.name}</td>
                      <td>阿里云 AK</td>
                      <td>
                        <code style={{ background: 'var(--bg-card-sub)', padding: '2px 6px', borderRadius: '4px' }}>
                          {c.fingerprint}
                        </code>
                      </td>
                      <td>{new Date(c.created_at_ms).toLocaleString()}</td>
                      <td>
                        <button
                          className="btn mini ghost"
                          style={{ color: 'var(--err)' }}
                          onClick={() => {
                            if (window.confirm(`确定删除凭据 ${c.name} 吗？`)) {
                              deleteCredMutation.mutate(c.id);
                            }
                          }}
                        >
                          删除
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>

          {/* RAM 最小化权限指南 */}
          <div className={styles.ramCard}>
            <h3 className={styles.ramTitle}>🛡️ RAM 权限最小化配置指南（请勿授予 AdministratorAccess）</h3>
            <ul className={styles.ramList}>
              <li>
                <strong>ecs:DescribeInstances / ecs:StartInstance / ecs:StopInstance</strong>：用于查询实例状态及启停控制（建议使用 Resource 条件限制到指定实例）。
              </li>
              <li>
                <strong>AliyunCDTReadOnlyAccess</strong>：用于查询账号级 CDT 公网出网流量。
              </li>
              <li>
                <strong>AliyunBSSReadOnlyAccess</strong>（可选）：用于查询当月实例账单。未配置该权限时系统会自动隔离错误，不影响 ECS 状态查询与流量决策。
              </li>
            </ul>
          </div>
        </>
      )}

      {/* Modal: 录入凭据 */}
      {showAddCredModal && (
        <div className={styles.modalBackdrop}>
          <div className={styles.modal}>
            <h3 style={{ marginTop: 0 }}>录入阿里云 AK/SK 凭据</h3>
            <div className={styles.formGroup}>
              <label className={styles.formLabel}>凭据名称</label>
              <input
                className={styles.input}
                style={{ width: '100%' }}
                placeholder="如: aliyun-prod-ak"
                value={newCredName}
                onChange={(e) => setNewCredName(e.target.value)}
              />
            </div>
            <div className={styles.formGroup}>
              <label className={styles.formLabel}>AccessKey ID</label>
              <input
                className={styles.input}
                style={{ width: '100%' }}
                placeholder="LTAI..."
                value={newAK}
                onChange={(e) => setNewAK(e.target.value)}
              />
            </div>
            <div className={styles.formGroup}>
              <label className={styles.formLabel}>AccessKey Secret</label>
              <input
                type="password"
                className={styles.input}
                style={{ width: '100%' }}
                placeholder="••••••••••••••••"
                value={newSK}
                onChange={(e) => setNewSK(e.target.value)}
              />
            </div>
            <div style={{ fontSize: '12px', color: 'var(--text-dim)' }}>
              🔒 提交后密钥将立即使用 AES-256-GCM 信封加密，明文不会在任何 API 中返回。
            </div>
            <div className={styles.formActions}>
              <button className="btn mini ghost" onClick={() => setShowAddCredModal(false)}>
                取消
              </button>
              <button
                className="btn mini primary"
                disabled={!newCredName || !newAK || !newSK}
                onClick={() => {
                  createCredMutation.mutate({
                    name: newCredName,
                    cred_kind: 'aliyun_ak',
                    access_key_id: newAK,
                    access_key_secret: newSK,
                  });
                }}
              >
                安全保存
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Modal: 添加云账号 */}
      {showAddAccModal && (
        <div className={styles.modalBackdrop}>
          <div className={styles.modal}>
            <h3 style={{ marginTop: 0 }}>添加云账号</h3>
            <div className={styles.formGroup}>
              <label className={styles.formLabel}>账号名称</label>
              <input
                className={styles.input}
                style={{ width: '100%' }}
                placeholder="如: 生产香港账号"
                value={newAccName}
                onChange={(e) => setNewAccName(e.target.value)}
              />
            </div>
            <div className={styles.formGroup}>
              <label className={styles.formLabel}>选择凭据</label>
              <select
                className={styles.select}
                style={{ width: '100%' }}
                value={newAccCredID}
                onChange={(e) => setNewAccCredID(e.target.value)}
              >
                <option value="">请选择凭据...</option>
                {credentials.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name} ({c.fingerprint})
                  </option>
                ))}
              </select>
            </div>
            <div className={styles.formGroup}>
              <label className={styles.formLabel}>账号站点 (决定 BSS 账单 Endpoint)</label>
              <select
                className={styles.select}
                style={{ width: '100%' }}
                value={newAccSite}
                onChange={(e) => setNewAccSite(e.target.value)}
              >
                <option value="china">中国站 (business.aliyuncs.com)</option>
                <option value="international">国际站 (business.ap-southeast-1.aliyuncs.com)</option>
              </select>
            </div>
            <div className={styles.formGroup}>
              <label className={styles.formLabel}>默认扫描区域</label>
              <input
                className={styles.input}
                style={{ width: '100%' }}
                placeholder="如 cn-hongkong"
                value={newAccRegion}
                onChange={(e) => setNewAccRegion(e.target.value)}
              />
            </div>
            <div className={styles.formActions}>
              <button className="btn mini ghost" onClick={() => setShowAddAccModal(false)}>
                取消
              </button>
              <button
                className="btn mini primary"
                disabled={!newAccName || !newAccCredID}
                onClick={() => {
                  createAccMutation.mutate({
                    provider_code: 'aliyun',
                    name: newAccName,
                    credential_id: newAccCredID,
                    default_region: newAccRegion,
                    account_site: newAccSite,
                  });
                }}
              >
                创建账号
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Modal: 自动发现实例 */}
      {showDiscoverModal && (
        <div className={styles.modalBackdrop}>
          <div className={styles.modal} style={{ width: '680px' }}>
            <h3 style={{ marginTop: 0 }}>跨区域自动发现 ECS 实例</h3>
            <div style={{ display: 'flex', gap: '10px', marginBottom: '16px' }}>
              <select
                className={styles.select}
                style={{ flex: 1 }}
                value={selectedCredForDiscover}
                onChange={(e) => setSelectedCredForDiscover(e.target.value)}
              >
                <option value="">选择凭据以扫描...</option>
                {credentials.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name} ({c.fingerprint})
                  </option>
                ))}
              </select>
              <button
                className="btn mini primary"
                disabled={!selectedCredForDiscover || isDiscovering}
                onClick={handleRunDiscover}
              >
                {isDiscovering ? '扫描中...' : '开始扫描'}
              </button>
            </div>

            {discoveredList.length > 0 ? (
              <div style={{ maxHeight: '300px', overflowY: 'auto' }}>
                <table className={styles.table}>
                  <thead>
                    <tr>
                      <th>实例名称</th>
                      <th>区域</th>
                      <th>状态</th>
                      <th>公网 IP</th>
                    </tr>
                  </thead>
                  <tbody>
                    {discoveredList.map((d) => (
                      <tr key={d.ref}>
                        <td>
                          <div style={{ fontWeight: 600 }}>{d.name || d.ref}</div>
                          <div style={{ fontSize: '11px', color: 'var(--text-dim)' }}>{d.ref}</div>
                        </td>
                        <td>{d.region}</td>
                        <td>{d.status}</td>
                        <td>{d.public_ips?.join(', ') || '-'}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <div style={{ padding: '20px', textAlign: 'center', color: 'var(--text-dim)' }}>
                {isDiscovering ? '正在扫描阿里云主流区域（杭州、香港、新加坡等）...' : '请选择凭据并点击「开始扫描」'}
              </div>
            )}

            <div className={styles.formActions}>
              <button className="btn mini ghost" onClick={() => setShowDiscoverModal(false)}>
                关闭
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
export default Cloud;
