import React, { useState, useMemo } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  getNodes,
  getGroups,
  getTags,
  createNode,
  updateNode,
  deleteNode,
  createGroup,
  updateGroup,
  deleteGroup,
  createTag,
  updateTag,
  deleteTag,
  replaceNodeTags,
  batchNodeTags,
  getNodeBilling,
  updateNodeBilling,
  deleteNodeBilling,
  getEnrollTokens,
  createEnrollToken,
  deleteEnrollToken,
  NodeItem,
  NodeGroup,
  NodeTag,
  NodeBilling,
  EnrollToken,
  CreateNodeParams,
  UpdateNodeParams,
  CreateEnrollTokenResponse,
} from '../../api';
import styles from './Machines.module.css';

const PRESET_COLORS = [
  { name: '蓝色', val: '#4da6ff' },
  { name: '青绿', val: '#00d4aa' },
  { name: '黄色', val: '#ffb870' },
  { name: '紫色', val: '#b392f0' },
  { name: '青色', val: '#39d2c0' },
  { name: '红色', val: '#f85149' },
  { name: '灰色', val: '#8999af' },
];

export const Machines: React.FC = () => {
  const queryClient = useQueryClient();
  const [activeTab, setActiveTab] = useState<'nodes' | 'groups' | 'tags' | 'install'>('nodes');

  // 消息提示
  const [notice, setNotice] = useState<string>('');
  const [errorMsg, setErrorMsg] = useState<string>('');

  const showNotice = (msg: string) => {
    setNotice(msg);
    setTimeout(() => setNotice(''), 4000);
  };

  const showError = (msg: string) => {
    setErrorMsg(msg);
    setTimeout(() => setErrorMsg(''), 5000);
  };

  // ==========================
  // 数据获取
  // ==========================
  const { data: nodes = [], isLoading: nodesLoading } = useQuery({
    queryKey: ['nodes-inventory'],
    queryFn: () => getNodes({ page_size: 200 }),
  });

  const { data: groups = [], isLoading: groupsLoading } = useQuery({
    queryKey: ['groups'],
    queryFn: getGroups,
  });

  const { data: tags = [], isLoading: tagsLoading } = useQuery({
    queryKey: ['tags'],
    queryFn: getTags,
  });

  const { data: enrollTokensData, isLoading: tokensLoading } = useQuery({
    queryKey: ['enroll-tokens'],
    queryFn: () => getEnrollTokens(1, 100),
  });
  const enrollTokens = enrollTokensData?.items || [];

  // ==========================
  // 节点筛选与批量操作
  // ==========================
  const [filterSearch, setFilterSearch] = useState<string>('');
  const [filterGroup, setFilterGroup] = useState<string>('');
  const [filterTag, setFilterTag] = useState<string>('');
  const [filterState, setFilterState] = useState<string>('all');
  const [selectedNodeIds, setSelectedNodeIds] = useState<Record<string, boolean>>({});

  const filteredNodes = useMemo(() => {
    return nodes.filter((n) => {
      if (filterState !== 'all' && n.conn_state !== filterState) return false;
      if (filterGroup && n.node_group_id !== filterGroup) return false;
      if (filterTag && !n.tags?.some((t) => t.id === filterTag)) return false;
      if (filterSearch) {
        const q = filterSearch.toLowerCase();
        const matchName = n.name.toLowerCase().includes(q);
        const matchId = n.id.toLowerCase().includes(q);
        const matchNote = (n.note || '').toLowerCase().includes(q);
        const matchIp = (n.facts?.ipv4 || '').includes(q);
        if (!matchName && !matchId && !matchNote && !matchIp) return false;
      }
      return true;
    });
  }, [nodes, filterSearch, filterGroup, filterTag, filterState]);

  const selectedCount = Object.values(selectedNodeIds).filter(Boolean).length;
  const isAllFilteredSelected =
    filteredNodes.length > 0 && filteredNodes.every((n) => selectedNodeIds[n.id]);

  const handleToggleSelectAll = () => {
    if (isAllFilteredSelected) {
      setSelectedNodeIds({});
    } else {
      const next: Record<string, boolean> = {};
      filteredNodes.forEach((n) => {
        next[n.id] = true;
      });
      setSelectedNodeIds(next);
    }
  };

  const handleToggleSelectRow = (id: string) => {
    setSelectedNodeIds((prev) => ({
      ...prev,
      [id]: !prev[id],
    }));
  };

  // ==========================
  // 节点弹窗状态 (新建 / 编辑 / 删除 / 标签 / 计费)
  // ==========================
  const [nodeModal, setNodeModal] = useState<{
    open: boolean;
    mode: 'create' | 'edit';
    node?: NodeItem;
  }>({ open: false, mode: 'create' });

  const [nodeForm, setNodeForm] = useState<{
    name: string;
    node_group_id: string;
    display_order: number;
    is_hidden: boolean;
    note: string;
  }>({
    name: '',
    node_group_id: '',
    display_order: 0,
    is_hidden: false,
    note: '',
  });

  const openCreateNodeModal = () => {
    setNodeForm({
      name: '',
      node_group_id: '',
      display_order: 0,
      is_hidden: false,
      note: '',
    });
    setNodeModal({ open: true, mode: 'create' });
  };

  const openEditNodeModal = (node: NodeItem) => {
    setNodeForm({
      name: node.name,
      node_group_id: node.node_group_id || '',
      display_order: node.display_order || 0,
      is_hidden: !!node.is_hidden,
      note: node.note || '',
    });
    setNodeModal({ open: true, mode: 'edit', node });
  };

  // 删除节点二次确认弹窗
  const [deleteNodeModal, setDeleteNodeModal] = useState<{
    open: boolean;
    node?: NodeItem;
  }>({ open: false });

  // 单节点打标签弹窗
  const [nodeTagsModal, setNodeTagsModal] = useState<{
    open: boolean;
    node?: NodeItem;
    selectedTagIds: Record<string, boolean>;
  }>({ open: false, selectedTagIds: {} });

  // 批量打标签弹窗
  const [batchTagsModal, setBatchTagsModal] = useState<{
    open: boolean;
    op: 'add' | 'remove';
    selectedTagIds: Record<string, boolean>;
  }>({ open: false, op: 'add', selectedTagIds: {} });

  // 计费弹窗
  const [billingModal, setBillingModal] = useState<{
    open: boolean;
    node?: NodeItem;
    billing: NodeBilling;
  }>({
    open: false,
    billing: {},
  });

  // ==========================
  // 分组弹窗状态
  // ==========================
  const [groupModal, setGroupModal] = useState<{
    open: boolean;
    mode: 'create' | 'edit';
    group?: NodeGroup;
  }>({ open: false, mode: 'create' });

  const [groupForm, setGroupForm] = useState<{
    name: string;
    display_order: number;
  }>({ name: '', display_order: 0 });

  const [deleteGroupModal, setDeleteGroupModal] = useState<{
    open: boolean;
    group?: NodeGroup;
  }>({ open: false });

  // ==========================
  // 标签弹窗状态
  // ==========================
  const [tagModal, setTagModal] = useState<{
    open: boolean;
    mode: 'create' | 'edit';
    tag?: NodeTag;
  }>({ open: false, mode: 'create' });

  const [tagForm, setTagForm] = useState<{
    name: string;
    color: string;
  }>({ name: '', color: '#4da6ff' });

  const [deleteTagModal, setDeleteTagModal] = useState<{
    open: boolean;
    tag?: NodeTag;
  }>({ open: false });

  // ==========================
  // 装机令牌创建与回显
  // ==========================
  const [tokenForm, setTokenForm] = useState<{
    preset_name: string;
    preset_group_id: string;
    ttl_hours: number;
  }>({ preset_name: '', preset_group_id: '', ttl_hours: 24 });

  const [createdTokenResult, setCreatedTokenResult] = useState<CreateEnrollTokenResponse | null>(
    null
  );
  const [copiedCmd, setCopiedCmd] = useState<boolean>(false);

  // ==========================
  // Mutations
  // ==========================

  // 1. 节点保存
  const saveNodeMutation = useMutation({
    mutationFn: async () => {
      if (nodeModal.mode === 'create') {
        const payload: CreateNodeParams = {
          name: nodeForm.name.trim(),
          node_group_id: nodeForm.node_group_id || undefined,
          display_order: nodeForm.display_order,
          is_hidden: nodeForm.is_hidden,
          note: nodeForm.note.trim() || undefined,
        };
        return await createNode(payload);
      } else if (nodeModal.node) {
        const payload: UpdateNodeParams = {
          name: nodeForm.name.trim(),
          node_group_id: nodeForm.node_group_id || null,
          display_order: nodeForm.display_order,
          is_hidden: nodeForm.is_hidden,
          note: nodeForm.note.trim() || undefined,
        };
        return await updateNode(nodeModal.node.id, payload);
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['nodes-inventory'] });
      queryClient.invalidateQueries({ queryKey: ['nodes'] });
      setNodeModal({ open: false, mode: 'create' });
      showNotice(nodeModal.mode === 'create' ? '节点创建成功' : '节点信息已更新');
    },
    onError: (err: any) => {
      showError(err?.message || '操作节点失败');
    },
  });

  // 2. 节点删除（二次确认）
  const deleteNodeMutation = useMutation({
    mutationFn: async (id: string) => {
      return await deleteNode(id);
    },
    onSuccess: (res) => {
      queryClient.invalidateQueries({ queryKey: ['nodes-inventory'] });
      queryClient.invalidateQueries({ queryKey: ['nodes'] });
      setSelectedNodeIds((prev) => {
        const next = { ...prev };
        if (deleteNodeModal.node) delete next[deleteNodeModal.node.id];
        return next;
      });
      setDeleteNodeModal({ open: false });
      showNotice(res.message || '节点已删除，关联数据已清理');
    },
    onError: (err: any) => {
      showError(err?.message || '删除节点失败');
    },
  });

  // 3. 节点单个打标签
  const saveNodeTagsMutation = useMutation({
    mutationFn: async () => {
      if (!nodeTagsModal.node) return;
      const tagIds = Object.keys(nodeTagsModal.selectedTagIds).filter(
        (tid) => nodeTagsModal.selectedTagIds[tid]
      );
      return await replaceNodeTags(nodeTagsModal.node.id, tagIds);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['nodes-inventory'] });
      queryClient.invalidateQueries({ queryKey: ['nodes'] });
      setNodeTagsModal({ open: false, selectedTagIds: {} });
      showNotice('节点标签已更新');
    },
    onError: (err: any) => {
      showError(err?.message || '更新节点标签失败');
    },
  });

  // 4. 批量给节点打/移除标签
  const batchTagsMutation = useMutation({
    mutationFn: async () => {
      const nodeIds = Object.keys(selectedNodeIds).filter((nid) => selectedNodeIds[nid]);
      const tagIds = Object.keys(batchTagsModal.selectedTagIds).filter(
        (tid) => batchTagsModal.selectedTagIds[tid]
      );
      if (nodeIds.length === 0 || tagIds.length === 0) return;
      return await batchNodeTags({
        node_ids: nodeIds,
        tag_ids: tagIds,
        op: batchTagsModal.op,
      });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['nodes-inventory'] });
      queryClient.invalidateQueries({ queryKey: ['nodes'] });
      setBatchTagsModal({ open: false, op: 'add', selectedTagIds: {} });
      showNotice(
        `批量${batchTagsModal.op === 'add' ? '打' : '移除'}标签成功 (已应用至 ${selectedCount} 台节点)`
      );
    },
    onError: (err: any) => {
      showError(err?.message || '批量修改标签失败');
    },
  });

  // 5. 计费修改
  const saveBillingMutation = useMutation({
    mutationFn: async () => {
      if (!billingModal.node) return;
      return await updateNodeBilling(billingModal.node.id, {
        currency: billingModal.billing.currency || 'USD',
        price: billingModal.billing.price,
        cycle_days: billingModal.billing.cycle_days,
        is_auto_renew: billingModal.billing.is_auto_renew,
        expires_at_ms: billingModal.billing.expires_at_ms,
        traffic_limit: billingModal.billing.traffic_limit,
        traffic_limit_kind: billingModal.billing.traffic_limit_kind || 'both',
        traffic_reset_day: billingModal.billing.traffic_reset_day,
      });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['nodes-inventory'] });
      queryClient.invalidateQueries({ queryKey: ['nodes'] });
      setBillingModal({ open: false, billing: {} });
      showNotice('节点计费与配额信息已保存');
    },
    onError: (err: any) => {
      showError(err?.message || '保存计费信息失败');
    },
  });

  const clearBillingMutation = useMutation({
    mutationFn: async () => {
      if (!billingModal.node) return;
      return await deleteNodeBilling(billingModal.node.id);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['nodes-inventory'] });
      queryClient.invalidateQueries({ queryKey: ['nodes'] });
      setBillingModal({ open: false, billing: {} });
      showNotice('已清除该节点计费记录');
    },
    onError: (err: any) => {
      showError(err?.message || '清除计费信息失败');
    },
  });

  // 6. 分组 CRUD
  const saveGroupMutation = useMutation({
    mutationFn: async () => {
      if (groupModal.mode === 'create') {
        return await createGroup({
          name: groupForm.name.trim(),
          display_order: groupForm.display_order,
        });
      } else if (groupModal.group) {
        return await updateGroup(groupModal.group.id, {
          name: groupForm.name.trim(),
          display_order: groupForm.display_order,
        });
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['groups'] });
      queryClient.invalidateQueries({ queryKey: ['nodes-inventory'] });
      setGroupModal({ open: false, mode: 'create' });
      showNotice(groupModal.mode === 'create' ? '分组创建成功' : '分组已更新');
    },
    onError: (err: any) => {
      showError(err?.message || '操作分组失败');
    },
  });

  const deleteGroupMutation = useMutation({
    mutationFn: async (id: string) => {
      return await deleteGroup(id);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['groups'] });
      queryClient.invalidateQueries({ queryKey: ['nodes-inventory'] });
      setDeleteGroupModal({ open: false });
      showNotice('分组已删除，关联节点已置为未分组');
    },
    onError: (err: any) => {
      showError(err?.message || '删除分组失败');
    },
  });

  // 7. 标签 CRUD
  const saveTagMutation = useMutation({
    mutationFn: async () => {
      if (tagModal.mode === 'create') {
        return await createTag({
          name: tagForm.name.trim(),
          color: tagForm.color,
        });
      } else if (tagModal.tag) {
        return await updateTag(tagModal.tag.id, {
          name: tagForm.name.trim(),
          color: tagForm.color,
        });
      }
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['tags'] });
      queryClient.invalidateQueries({ queryKey: ['nodes-inventory'] });
      setTagModal({ open: false, mode: 'create' });
      showNotice(tagModal.mode === 'create' ? '标签创建成功' : '标签已更新');
    },
    onError: (err: any) => {
      showError(err?.message || '操作标签失败');
    },
  });

  const deleteTagMutation = useMutation({
    mutationFn: async (id: string) => {
      return await deleteTag(id);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['tags'] });
      queryClient.invalidateQueries({ queryKey: ['nodes-inventory'] });
      setDeleteTagModal({ open: false });
      showNotice('标签已删除');
    },
    onError: (err: any) => {
      showError(err?.message || '删除标签失败');
    },
  });

  // 8. 装机令牌生成与删除
  const createTokenMutation = useMutation({
    mutationFn: async () => {
      return await createEnrollToken({
        preset_name: tokenForm.preset_name.trim() || undefined,
        preset_group_id: tokenForm.preset_group_id || undefined,
        ttl_hours: tokenForm.ttl_hours || 24,
      });
    },
    onSuccess: (res) => {
      setCreatedTokenResult(res);
      queryClient.invalidateQueries({ queryKey: ['enroll-tokens'] });
      showNotice('注册令牌已生成，一键安装命令已就绪');
    },
    onError: (err: any) => {
      showError(err?.message || '生成令牌失败');
    },
  });

  const deleteTokenMutation = useMutation({
    mutationFn: async (id: string) => {
      return await deleteEnrollToken(id);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['enroll-tokens'] });
      showNotice('令牌已成功作废');
    },
    onError: (err: any) => {
      showError(err?.message || '作废令牌失败');
    },
  });

  // 复制命令辅助
  const handleCopyCmd = (cmd: string) => {
    navigator.clipboard.writeText(cmd).then(() => {
      setCopiedCmd(true);
      setTimeout(() => setCopiedCmd(false), 2500);
    });
  };

  // 打开计费编辑
  const handleOpenBilling = async (node: NodeItem) => {
    try {
      const b = await getNodeBilling(node.id);
      setBillingModal({ open: true, node, billing: b || {} });
    } catch {
      setBillingModal({
        open: true,
        node,
        billing: {
          currency: 'USD',
          cycle_days: 30,
          traffic_limit_kind: 'both',
        },
      });
    }
  };

  // 打开节点标签编辑
  const handleOpenNodeTags = (node: NodeItem) => {
    const selectedMap: Record<string, boolean> = {};
    (node.tags || []).forEach((t) => {
      selectedMap[t.id] = true;
    });
    setNodeTagsModal({ open: true, node, selectedTagIds: selectedMap });
  };

  return (
    <div className={styles.container}>
      {/* 头部标题与操作 */}
      <header className={styles.header}>
        <div className={styles.titleArea}>
          <h1 className={styles.title}>机器资产与清单管理</h1>
          <p className={styles.subtitle}>
            节点增删改、分组归类、批量打标签、到期与计费设置、一键装机命令
          </p>
        </div>

        {/* 顶部快捷操作 */}
        <div style={{ display: 'flex', gap: 'var(--sp-2)' }}>
          <button
            type="button"
            className="btn primary"
            onClick={() => {
              setActiveTab('install');
            }}
          >
            + 一键装机接入
          </button>
          <button type="button" className="btn" onClick={openCreateNodeModal}>
            + 手动新建节点
          </button>
        </div>
      </header>

      {/* 提示横幅 */}
      {notice && <div className={styles.notice}>{notice}</div>}
      {errorMsg && <div className={styles.errorBanner}>{errorMsg}</div>}

      {/* 导航选项卡 */}
      <div className={styles.tabs}>
        <button
          type="button"
          className={`${styles.tabBtn} ${activeTab === 'nodes' ? styles.tabBtnActive : ''}`}
          onClick={() => setActiveTab('nodes')}
        >
          节点管理 ({nodes.length})
        </button>
        <button
          type="button"
          className={`${styles.tabBtn} ${activeTab === 'groups' ? styles.tabBtnActive : ''}`}
          onClick={() => setActiveTab('groups')}
        >
          分组管理 ({groups.length})
        </button>
        <button
          type="button"
          className={`${styles.tabBtn} ${activeTab === 'tags' ? styles.tabBtnActive : ''}`}
          onClick={() => setActiveTab('tags')}
        >
          标签管理 ({tags.length})
        </button>
        <button
          type="button"
          className={`${styles.tabBtn} ${activeTab === 'install' ? styles.tabBtnActive : ''}`}
          onClick={() => setActiveTab('install')}
        >
          装机与令牌 ({enrollTokens.length})
        </button>
      </div>

      {/* ==================================================================== */}
      {/* 1. 节点管理 TAB */}
      {/* ==================================================================== */}
      {activeTab === 'nodes' && (
        <>
          {/* 过滤栏 */}
          <div className={styles.filterCard}>
            <div className={styles.filterGroup}>
              <span className={styles.label}>搜索:</span>
              <input
                type="text"
                className={styles.input}
                placeholder="名称 / ID / 备注 / IP..."
                value={filterSearch}
                onChange={(e) => setFilterSearch(e.target.value)}
                style={{ width: 180 }}
              />
            </div>

            <div className={styles.filterGroup}>
              <span className={styles.label}>分组:</span>
              <select
                className={styles.select}
                value={filterGroup}
                onChange={(e) => setFilterGroup(e.target.value)}
              >
                <option value="">全部分组</option>
                {groups.map((g) => (
                  <option key={g.id} value={g.id}>
                    {g.name}
                  </option>
                ))}
              </select>
            </div>

            <div className={styles.filterGroup}>
              <span className={styles.label}>标签:</span>
              <select
                className={styles.select}
                value={filterTag}
                onChange={(e) => setFilterTag(e.target.value)}
              >
                <option value="">全部标签</option>
                {tags.map((t) => (
                  <option key={t.id} value={t.id}>
                    {t.name}
                  </option>
                ))}
              </select>
            </div>

            <div className={styles.filterGroup}>
              <span className={styles.label}>状态:</span>
              <select
                className={styles.select}
                value={filterState}
                onChange={(e) => setFilterState(e.target.value)}
              >
                <option value="all">全部状态</option>
                <option value="online">在线 (online)</option>
                <option value="offline">离线 (offline)</option>
                <option value="never">未上线 (never)</option>
              </select>
            </div>

            <div style={{ marginLeft: 'auto', display: 'flex', gap: 'var(--sp-2)' }}>
              <button
                type="button"
                className="btn mini"
                onClick={() => {
                  queryClient.invalidateQueries({ queryKey: ['nodes-inventory'] });
                }}
              >
                刷新
              </button>
            </div>
          </div>

          {/* 批量操作工具栏 */}
          {selectedCount > 0 && (
            <div className={styles.batchBar}>
              <div className={styles.batchInfo}>已选中 {selectedCount} 台节点</div>
              <div className={styles.batchActions}>
                <button
                  type="button"
                  className="btn mini primary"
                  onClick={() => {
                    setBatchTagsModal({ open: true, op: 'add', selectedTagIds: {} });
                  }}
                >
                  + 批量打标签
                </button>
                <button
                  type="button"
                  className="btn mini danger"
                  onClick={() => {
                    setBatchTagsModal({ open: true, op: 'remove', selectedTagIds: {} });
                  }}
                >
                  - 批量移除标签
                </button>
                <button
                  type="button"
                  className="btn mini ghost"
                  onClick={() => setSelectedNodeIds({})}
                >
                  取消全选
                </button>
              </div>
            </div>
          )}

          {/* 节点表格 */}
          <div className={styles.tableCard}>
            <div className={styles.tableWrap}>
              <table className={styles.table}>
                <thead>
                  <tr>
                    <th style={{ width: 36, textAlign: 'center' }}>
                      <input
                        type="checkbox"
                        checked={isAllFilteredSelected}
                        onChange={handleToggleSelectAll}
                        title="全选 / 取消全选当前过滤节点"
                      />
                    </th>
                    <th>节点名称 / ID</th>
                    <th>状态</th>
                    <th>分组</th>
                    <th>标签</th>
                    <th>计费 / 到期</th>
                    <th>备注</th>
                    <th style={{ textAlign: 'right' }}>操作</th>
                  </tr>
                </thead>
                <tbody>
                  {nodesLoading ? (
                    <tr>
                      <td colSpan={8} className={styles.emptyState}>
                        加载节点数据中...
                      </td>
                    </tr>
                  ) : filteredNodes.length === 0 ? (
                    <tr>
                      <td colSpan={8} className={styles.emptyState}>
                        未找到匹配的机器记录
                      </td>
                    </tr>
                  ) : (
                    filteredNodes.map((n) => {
                      const isSelected = !!selectedNodeIds[n.id];
                      const isOffline = n.conn_state === 'offline';

                      // 计费到期天数
                      let expiryText = '--';
                      let expiryWarn = false;
                      if (n.billing?.expires_at_ms) {
                        const daysLeft = Math.ceil(
                          (n.billing.expires_at_ms - Date.now()) / 86400000
                        );
                        if (daysLeft < 0) {
                          expiryText = `已过期 ${Math.abs(daysLeft)} 天`;
                          expiryWarn = true;
                        } else if (daysLeft <= 7) {
                          expiryText = `${daysLeft} 天后到期`;
                          expiryWarn = true;
                        } else {
                          expiryText = `${daysLeft} 天后到期`;
                        }
                      }

                      return (
                        <tr
                          key={n.id}
                          className={isOffline ? styles.rowOffline : undefined}
                          style={isSelected ? { backgroundColor: 'var(--bg-hover)' } : undefined}
                        >
                          <td style={{ textAlign: 'center' }}>
                            <input
                              type="checkbox"
                              checked={isSelected}
                              onChange={() => handleToggleSelectRow(n.id)}
                            />
                          </td>
                          <td>
                            <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                              <span style={{ fontWeight: 600, color: 'var(--text-main)' }}>
                                {n.name}
                              </span>
                              {n.is_hidden && (
                                <span className="badge badge-neutral" style={{ fontSize: 10 }}>
                                  隐藏
                                </span>
                              )}
                            </div>
                            <div
                              className="cell-mono"
                              style={{ fontSize: 11, color: 'var(--text-mute)' }}
                            >
                              {n.id}
                            </div>
                          </td>
                          <td>
                            {n.conn_state === 'online' ? (
                              <span className="health ok">
                                <span className="hdot" /> online
                              </span>
                            ) : isOffline ? (
                              <span className="health err">
                                <span className="hdot" /> offline
                              </span>
                            ) : (
                              <span className="health" style={{ color: 'var(--text-mute)' }}>
                                <span className="hdot" style={{ background: 'var(--text-mute)' }} /> never
                              </span>
                            )}
                          </td>
                          <td>
                            {n.group?.name ? (
                              <span className="badge badge-neutral">{n.group.name}</span>
                            ) : (
                              <span style={{ color: 'var(--text-mute)' }}>未分组</span>
                            )}
                          </td>
                          <td>
                            <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap' }}>
                              {n.tags && n.tags.length > 0 ? (
                                n.tags.map((t) => (
                                  <span
                                    key={t.id}
                                    className="badge"
                                    style={{
                                      borderColor: t.color || 'var(--border-main)',
                                      color: t.color || 'var(--text-main)',
                                    }}
                                  >
                                    {t.name}
                                  </span>
                                ))
                              ) : (
                                <span style={{ color: 'var(--text-mute)', fontSize: 12 }}>无</span>
                              )}
                            </div>
                          </td>
                          <td>
                            <div className="cell-mono" style={{ fontSize: 12 }}>
                              {n.billing?.price !== undefined && n.billing?.price !== null
                                ? `${n.billing.currency || '$'}${n.billing.price}`
                                : '--'}
                            </div>
                            <div
                              className="cell-mono"
                              style={{
                                fontSize: 11,
                                color: expiryWarn ? 'var(--warn)' : 'var(--text-dim)',
                              }}
                            >
                              {expiryText}
                            </div>
                          </td>
                          <td style={{ color: 'var(--text-dim)', fontSize: 12, maxWidth: 180 }}>
                            {n.note || '--'}
                          </td>
                          <td style={{ textAlign: 'right', whiteSpace: 'nowrap' }}>
                            <div
                              style={{
                                display: 'inline-flex',
                                gap: 'var(--sp-1)',
                                alignItems: 'center',
                              }}
                            >
                              <button
                                type="button"
                                className="btn mini"
                                onClick={() => openEditNodeModal(n)}
                              >
                                编辑
                              </button>
                              <button
                                type="button"
                                className="btn mini"
                                onClick={() => handleOpenNodeTags(n)}
                              >
                                标签
                              </button>
                              <button
                                type="button"
                                className="btn mini"
                                onClick={() => handleOpenBilling(n)}
                              >
                                计费
                              </button>
                              <button
                                type="button"
                                className="btn mini danger"
                                onClick={() => setDeleteNodeModal({ open: true, node: n })}
                              >
                                删除
                              </button>
                            </div>
                          </td>
                        </tr>
                      );
                    })
                  )}
                </tbody>
              </table>
            </div>
          </div>
        </>
      )}

      {/* ==================================================================== */}
      {/* 2. 分组管理 TAB */}
      {/* ==================================================================== */}
      {activeTab === 'groups' && (
        <div className={styles.tableCard}>
          <div
            style={{
              padding: 'var(--sp-3) var(--sp-4)',
              borderBottom: '1px solid var(--border-main)',
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'center',
            }}
          >
            <span style={{ color: 'var(--text-dim)', fontSize: 13 }}>
              分组用于对物理/云主机进行地域、环境或业务维度的划分（支持排序）。
            </span>
            <button
              type="button"
              className="btn primary mini"
              onClick={() => {
                setGroupForm({ name: '', display_order: 0 });
                setGroupModal({ open: true, mode: 'create' });
              }}
            >
              + 新建分组
            </button>
          </div>

          <table className={styles.table}>
            <thead>
              <tr>
                <th>分组名称</th>
                <th>分组 ID</th>
                <th>排序权重</th>
                <th>节点数量</th>
                <th style={{ textAlign: 'right' }}>操作</th>
              </tr>
            </thead>
            <tbody>
              {groupsLoading ? (
                <tr>
                  <td colSpan={5} className={styles.emptyState}>
                    加载分组数据中...
                  </td>
                </tr>
              ) : groups.length === 0 ? (
                <tr>
                  <td colSpan={5} className={styles.emptyState}>
                    暂无分组记录，点击右上角新建
                  </td>
                </tr>
              ) : (
                groups.map((g) => {
                  const nodeCount = nodes.filter((n) => n.node_group_id === g.id).length;
                  return (
                    <tr key={g.id}>
                      <td style={{ fontWeight: 600 }}>{g.name}</td>
                      <td className="cell-mono" style={{ color: 'var(--text-mute)', fontSize: 11 }}>
                        {g.id}
                      </td>
                      <td className="cell-mono">{g.display_order ?? 0}</td>
                      <td className="cell-mono">{nodeCount} 台</td>
                      <td style={{ textAlign: 'right' }}>
                        <div
                          style={{
                            display: 'inline-flex',
                            gap: 'var(--sp-1)',
                            alignItems: 'center',
                          }}
                        >
                          <button
                            type="button"
                            className="btn mini"
                            onClick={() => {
                              setGroupForm({
                                name: g.name,
                                display_order: g.display_order || 0,
                              });
                              setGroupModal({ open: true, mode: 'edit', group: g });
                            }}
                          >
                            编辑
                          </button>
                          <button
                            type="button"
                            className="btn mini danger"
                            onClick={() => setDeleteGroupModal({ open: true, group: g })}
                          >
                            删除
                          </button>
                        </div>
                      </td>
                    </tr>
                  );
                })
              )}
            </tbody>
          </table>
        </div>
      )}

      {/* ==================================================================== */}
      {/* 3. 标签管理 TAB */}
      {/* ==================================================================== */}
      {activeTab === 'tags' && (
        <div className={styles.tableCard}>
          <div
            style={{
              padding: 'var(--sp-3) var(--sp-4)',
              borderBottom: '1px solid var(--border-main)',
              display: 'flex',
              justifyContent: 'space-between',
              alignItems: 'center',
            }}
          >
            <span style={{ color: 'var(--text-dim)', fontSize: 13 }}>
              支持多标签与自定义颜色标识；一个节点可打多个标签。
            </span>
            <button
              type="button"
              className="btn primary mini"
              onClick={() => {
                setTagForm({ name: '', color: '#4da6ff' });
                setTagModal({ open: true, mode: 'create' });
              }}
            >
              + 新建标签
            </button>
          </div>

          <table className={styles.table}>
            <thead>
              <tr>
                <th>标签名称</th>
                <th>颜色</th>
                <th>标签 ID</th>
                <th>绑定节点数</th>
                <th style={{ textAlign: 'right' }}>操作</th>
              </tr>
            </thead>
            <tbody>
              {tagsLoading ? (
                <tr>
                  <td colSpan={5} className={styles.emptyState}>
                    加载标签数据中...
                  </td>
                </tr>
              ) : tags.length === 0 ? (
                <tr>
                  <td colSpan={5} className={styles.emptyState}>
                    暂无标签记录，点击右上角新建
                  </td>
                </tr>
              ) : (
                tags.map((t) => {
                  const nodeCount = nodes.filter((n) => n.tags?.some((nt) => nt.id === t.id))
                    .length;
                  return (
                    <tr key={t.id}>
                      <td>
                        <span
                          className="badge"
                          style={{
                            borderColor: t.color || 'var(--border-main)',
                            color: t.color || 'var(--text-main)',
                          }}
                        >
                          {t.name}
                        </span>
                      </td>
                      <td>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                          <span
                            className={styles.colorDot}
                            style={{ backgroundColor: t.color || 'var(--accent)' }}
                          />
                          <span className="cell-mono" style={{ fontSize: 11 }}>
                            {t.color || '默认'}
                          </span>
                        </div>
                      </td>
                      <td className="cell-mono" style={{ color: 'var(--text-mute)', fontSize: 11 }}>
                        {t.id}
                      </td>
                      <td className="cell-mono">{nodeCount} 台</td>
                      <td style={{ textAlign: 'right' }}>
                        <div
                          style={{
                            display: 'inline-flex',
                            gap: 'var(--sp-1)',
                            alignItems: 'center',
                          }}
                        >
                          <button
                            type="button"
                            className="btn mini"
                            onClick={() => {
                              setTagForm({
                                name: t.name,
                                color: t.color || '#4da6ff',
                              });
                              setTagModal({ open: true, mode: 'edit', tag: t });
                            }}
                          >
                            编辑
                          </button>
                          <button
                            type="button"
                            className="btn mini danger"
                            onClick={() => setDeleteTagModal({ open: true, tag: t })}
                          >
                            删除
                          </button>
                        </div>
                      </td>
                    </tr>
                  );
                })
              )}
            </tbody>
          </table>
        </div>
      )}

      {/* ==================================================================== */}
      {/* 4. 装机与令牌 TAB */}
      {/* ==================================================================== */}
      {activeTab === 'install' && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--sp-4)' }}>
          {/* 生成令牌卡片 */}
          <div className="card">
            <div className="card-header">
              <span>一键装机 · 生成注册令牌 (Enrollment Token)</span>
            </div>
            <div className="card-body">
              <form
                onSubmit={(e) => {
                  e.preventDefault();
                  createTokenMutation.mutate();
                }}
                style={{ display: 'flex', flexDirection: 'column', gap: 'var(--sp-3)' }}
              >
                <div className={styles.formGrid}>
                  <div className={styles.formGroup}>
                    <label className={styles.fieldLabel}>预设节点名称 (可选)</label>
                    <input
                      type="text"
                      className={styles.input}
                      placeholder="例如 hk-edge-01"
                      value={tokenForm.preset_name}
                      onChange={(e) =>
                        setTokenForm((prev) => ({ ...prev, preset_name: e.target.value }))
                      }
                    />
                    <span className={styles.hint}>安装上报时若带此预设，将自动以此名称建档</span>
                  </div>

                  <div className={styles.formGroup}>
                    <label className={styles.fieldLabel}>预设分组 (可选)</label>
                    <select
                      className={styles.select}
                      value={tokenForm.preset_group_id}
                      onChange={(e) =>
                        setTokenForm((prev) => ({ ...prev, preset_group_id: e.target.value }))
                      }
                    >
                      <option value="">不预设分组</option>
                      {groups.map((g) => (
                        <option key={g.id} value={g.id}>
                          {g.name}
                        </option>
                      ))}
                    </select>
                  </div>

                  <div className={styles.formGroup}>
                    <label className={styles.fieldLabel}>令牌有效时长 (小时)</label>
                    <input
                      type="number"
                      className={styles.input}
                      min="1"
                      max="720"
                      value={tokenForm.ttl_hours}
                      onChange={(e) =>
                        setTokenForm((prev) => ({
                          ...prev,
                          ttl_hours: parseInt(e.target.value, 10) || 24,
                        }))
                      }
                    />
                    <span className={styles.hint}>默认 24 小时；过期后无法使用</span>
                  </div>

                  <div
                    style={{
                      display: 'flex',
                      alignItems: 'flex-end',
                      justifyContent: 'flex-end',
                    }}
                  >
                    <button
                      type="submit"
                      className="btn primary"
                      disabled={createTokenMutation.isPending}
                    >
                      {createTokenMutation.isPending ? '生成中...' : '立即生成安装命令'}
                    </button>
                  </div>
                </div>
              </form>

              {/* 生成结果命令回显 */}
              {createdTokenResult && (
                <div
                  style={{
                    marginTop: 'var(--sp-4)',
                    paddingTop: 'var(--sp-4)',
                    borderTop: '1px solid var(--border-dim)',
                    display: 'flex',
                    flexDirection: 'column',
                    gap: 'var(--sp-2)',
                  }}
                >
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <span style={{ fontWeight: 600, color: 'var(--ok)' }}>
                      ✓ 令牌生成成功！请在目标主机执行以下一键安装命令：
                    </span>
                    <span className="cell-mono" style={{ fontSize: 11, color: 'var(--text-dim)' }}>
                      有效期至 {new Date(createdTokenResult.expires_at_ms).toLocaleString()}
                    </span>
                  </div>

                  <div className={styles.installCodeBox}>
                    <div className={styles.installCmd}>{createdTokenResult.install_cmd}</div>
                    <button
                      type="button"
                      className={styles.copyBtn}
                      onClick={() => handleCopyCmd(createdTokenResult.install_cmd)}
                    >
                      {copiedCmd ? '✓ 已复制' : '📋 复制命令'}
                    </button>
                  </div>
                  <span className={styles.hint}>
                    ★ 脚本将自动识别 Linux 架构 (amd64 / arm64 / armv7)，下载二进制、注册 systemd
                    服务并完成上线。
                  </span>
                </div>
              )}
            </div>
          </div>

          {/* 历史与当前令牌列表 */}
          <div className={styles.tableCard}>
            <div
              style={{
                padding: 'var(--sp-3) var(--sp-4)',
                borderBottom: '1px solid var(--border-main)',
                fontWeight: 600,
                color: 'var(--text-main)',
              }}
            >
              注册令牌列表与状态
            </div>

            <table className={styles.table}>
              <thead>
                <tr>
                  <th>令牌 ID</th>
                  <th>预设名称</th>
                  <th>预设分组</th>
                  <th>创建者</th>
                  <th>状态</th>
                  <th>到期时间</th>
                  <th>创建时间</th>
                  <th style={{ textAlign: 'right' }}>操作</th>
                </tr>
              </thead>
              <tbody>
                {tokensLoading ? (
                  <tr>
                    <td colSpan={8} className={styles.emptyState}>
                      加载令牌数据中...
                    </td>
                  </tr>
                ) : enrollTokens.length === 0 ? (
                  <tr>
                    <td colSpan={8} className={styles.emptyState}>
                      暂无注册令牌记录
                    </td>
                  </tr>
                ) : (
                  enrollTokens.map((tk: EnrollToken) => {
                    const isExpired = Date.now() > tk.expires_at_ms;
                    const isUsed = !!tk.used_at_ms;

                    let statusBadge = (
                      <span className="pill green">有效</span>
                    );
                    if (isUsed) {
                      statusBadge = (
                        <span className="pill blue" title={`由节点 ${tk.used_node_id} 使用`}>
                          已使用
                        </span>
                      );
                    } else if (isExpired) {
                      statusBadge = <span className="pill grey">已过期</span>;
                    }

                    const groupObj = groups.find((g) => g.id === tk.preset_group_id);

                    return (
                      <tr key={tk.id}>
                        <td className="cell-mono" style={{ fontWeight: 500 }}>
                          {tk.id}
                        </td>
                        <td>{tk.preset_name || '--'}</td>
                        <td>
                          {groupObj ? (
                            <span className="badge badge-neutral">{groupObj.name}</span>
                          ) : (
                            '--'
                          )}
                        </td>
                        <td style={{ color: 'var(--text-dim)' }}>{tk.created_by || 'admin'}</td>
                        <td>{statusBadge}</td>
                        <td className="cell-mono" style={{ fontSize: 11 }}>
                          {new Date(tk.expires_at_ms).toLocaleString()}
                        </td>
                        <td className="cell-mono" style={{ fontSize: 11, color: 'var(--text-dim)' }}>
                          {new Date(tk.created_at_ms).toLocaleString()}
                        </td>
                        <td style={{ textAlign: 'right' }}>
                          <button
                            type="button"
                            className="btn mini danger"
                            disabled={isUsed || isExpired || deleteTokenMutation.isPending}
                            onClick={() => deleteTokenMutation.mutate(tk.id)}
                            title="作废该令牌"
                          >
                            作废
                          </button>
                        </td>
                      </tr>
                    );
                  })
                )}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* ==================================================================== */}
      {/* 弹窗：新建 / 编辑节点 */}
      {/* ==================================================================== */}
      {nodeModal.open && (
        <div className={styles.modalOverlay} onClick={() => setNodeModal({ open: false, mode: 'create' })}>
          <div className={styles.modalContent} onClick={(e) => e.stopPropagation()}>
            <div className={styles.modalHeader}>
              <h2 className={styles.modalTitle}>
                {nodeModal.mode === 'create' ? '手动新建节点' : `编辑节点: ${nodeModal.node?.name}`}
              </h2>
              <button
                type="button"
                className={styles.modalCloseBtn}
                onClick={() => setNodeModal({ open: false, mode: 'create' })}
              >
                ✕
              </button>
            </div>

            <form
              onSubmit={(e) => {
                e.preventDefault();
                saveNodeMutation.mutate();
              }}
            >
              <div className={styles.modalBody}>
                <div className={styles.formGroup}>
                  <label className={styles.fieldLabel}>节点名称 *</label>
                  <input
                    type="text"
                    required
                    className={styles.input}
                    placeholder="例如 tokyo-01"
                    value={nodeForm.name}
                    onChange={(e) => setNodeForm((prev) => ({ ...prev, name: e.target.value }))}
                  />
                </div>

                <div className={styles.formGroup}>
                  <label className={styles.fieldLabel}>归属分组</label>
                  <select
                    className={styles.select}
                    value={nodeForm.node_group_id}
                    onChange={(e) =>
                      setNodeForm((prev) => ({ ...prev, node_group_id: e.target.value }))
                    }
                  >
                    <option value="">未分组</option>
                    {groups.map((g) => (
                      <option key={g.id} value={g.id}>
                        {g.name}
                      </option>
                    ))}
                  </select>
                </div>

                <div className={styles.formGroup}>
                  <label className={styles.fieldLabel}>排序权重 (数值越小越靠前)</label>
                  <input
                    type="number"
                    className={styles.input}
                    value={nodeForm.display_order}
                    onChange={(e) =>
                      setNodeForm((prev) => ({
                        ...prev,
                        display_order: parseInt(e.target.value, 10) || 0,
                      }))
                    }
                  />
                </div>

                <div className={styles.formGroup}>
                  <label className={styles.checkboxRow}>
                    <input
                      type="checkbox"
                      checked={nodeForm.is_hidden}
                      onChange={(e) =>
                        setNodeForm((prev) => ({ ...prev, is_hidden: e.target.checked }))
                      }
                    />
                    <span>在总览监控大盘中隐藏该节点</span>
                  </label>
                </div>

                <div className={styles.formGroup}>
                  <label className={styles.fieldLabel}>备注说明</label>
                  <textarea
                    className={styles.input}
                    rows={3}
                    placeholder="服务用途、到期说明等"
                    value={nodeForm.note}
                    onChange={(e) => setNodeForm((prev) => ({ ...prev, note: e.target.value }))}
                  />
                </div>
              </div>

              <div className={styles.modalFooter}>
                <button
                  type="button"
                  className="btn ghost"
                  onClick={() => setNodeModal({ open: false, mode: 'create' })}
                >
                  取消
                </button>
                <button type="submit" className="btn primary" disabled={saveNodeMutation.isPending}>
                  {saveNodeMutation.isPending ? '保存中...' : '保存'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* ==================================================================== */}
      {/* 弹窗：删除节点二次确认 (约束：明确提示时序数据自然过期) */}
      {/* ==================================================================== */}
      {deleteNodeModal.open && deleteNodeModal.node && (
        <div className={styles.modalOverlay} onClick={() => setDeleteNodeModal({ open: false })}>
          <div className={styles.modalContent} onClick={(e) => e.stopPropagation()}>
            <div className={styles.modalHeader}>
              <h2 className={styles.modalTitle} style={{ color: 'var(--err)' }}>
                二次确认：删除节点
              </h2>
              <button
                type="button"
                className={styles.modalCloseBtn}
                onClick={() => setDeleteNodeModal({ open: false })}
              >
                ✕
              </button>
            </div>

            <div className={styles.modalBody}>
              <p style={{ fontSize: 14 }}>
                您确定要删除节点 <strong>{deleteNodeModal.node.name}</strong> (
                <span className="cell-mono">{deleteNodeModal.node.id}</span>) 吗？
              </p>

              <div className={styles.warningBox}>
                <strong>注意：</strong>
                <br />
                1. 节点档案、硬件事实、计费信息及标签关联将立即删除，Agent Token 将被吊销。
                <br />
                2. <strong>时序监控数据会随保留期自然过期，不会立即同步删除</strong>
                （避免长时间数据库锁表）。
              </div>
            </div>

            <div className={styles.modalFooter}>
              <button
                type="button"
                className="btn ghost"
                onClick={() => setDeleteNodeModal({ open: false })}
              >
                取消
              </button>
              <button
                type="button"
                className="btn danger"
                disabled={deleteNodeMutation.isPending}
                onClick={() => deleteNodeMutation.mutate(deleteNodeModal.node!.id)}
              >
                {deleteNodeMutation.isPending ? '删除中...' : '确认删除'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* ==================================================================== */}
      {/* 弹窗：单节点标签修改 */}
      {/* ==================================================================== */}
      {nodeTagsModal.open && nodeTagsModal.node && (
        <div className={styles.modalOverlay} onClick={() => setNodeTagsModal({ open: false, selectedTagIds: {} })}>
          <div className={styles.modalContent} onClick={(e) => e.stopPropagation()}>
            <div className={styles.modalHeader}>
              <h2 className={styles.modalTitle}>
                管理标签: {nodeTagsModal.node.name}
              </h2>
              <button
                type="button"
                className={styles.modalCloseBtn}
                onClick={() => setNodeTagsModal({ open: false, selectedTagIds: {} })}
              >
                ✕
              </button>
            </div>

            <div className={styles.modalBody}>
              <div className={styles.fieldLabel}>勾选要为该节点赋予的标签：</div>
              <div className={styles.tagSelector}>
                {tags.map((t) => {
                  const isChecked = !!nodeTagsModal.selectedTagIds[t.id];
                  return (
                    <div
                      key={t.id}
                      className={`${styles.tagChoice} ${
                        isChecked ? styles.tagChoiceSelected : ''
                      }`}
                      onClick={() =>
                        setNodeTagsModal((prev) => ({
                          ...prev,
                          selectedTagIds: {
                            ...prev.selectedTagIds,
                            [t.id]: !isChecked,
                          },
                        }))
                      }
                    >
                      <span
                        className={styles.colorDot}
                        style={{ backgroundColor: t.color || 'var(--accent)' }}
                      />
                      <span>{t.name}</span>
                      {isChecked && <span>✓</span>}
                    </div>
                  );
                })}
              </div>
            </div>

            <div className={styles.modalFooter}>
              <button
                type="button"
                className="btn ghost"
                onClick={() => setNodeTagsModal({ open: false, selectedTagIds: {} })}
              >
                取消
              </button>
              <button
                type="button"
                className="btn primary"
                disabled={saveNodeTagsMutation.isPending}
                onClick={() => saveNodeTagsMutation.mutate()}
              >
                {saveNodeTagsMutation.isPending ? '保存中...' : '保存标签'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* ==================================================================== */}
      {/* 弹窗：批量打 / 移除标签 (约束：支持一次给多个节点批量打标签) */}
      {/* ==================================================================== */}
      {batchTagsModal.open && (
        <div className={styles.modalOverlay} onClick={() => setBatchTagsModal({ open: false, op: 'add', selectedTagIds: {} })}>
          <div className={styles.modalContent} onClick={(e) => e.stopPropagation()}>
            <div className={styles.modalHeader}>
              <h2 className={styles.modalTitle}>
                批量{batchTagsModal.op === 'add' ? '打标签' : '移除标签'} (已选 {selectedCount} 台)
              </h2>
              <button
                type="button"
                className={styles.modalCloseBtn}
                onClick={() => setBatchTagsModal({ open: false, op: 'add', selectedTagIds: {} })}
              >
                ✕
              </button>
            </div>

            <div className={styles.modalBody}>
              <div className={styles.fieldLabel}>
                请选择要批量{batchTagsModal.op === 'add' ? '添加给' : '从'}这 {selectedCount} 台节点的标签：
              </div>
              <div className={styles.tagSelector}>
                {tags.map((t) => {
                  const isChecked = !!batchTagsModal.selectedTagIds[t.id];
                  return (
                    <div
                      key={t.id}
                      className={`${styles.tagChoice} ${
                        isChecked ? styles.tagChoiceSelected : ''
                      }`}
                      onClick={() =>
                        setBatchTagsModal((prev) => ({
                          ...prev,
                          selectedTagIds: {
                            ...prev.selectedTagIds,
                            [t.id]: !isChecked,
                          },
                        }))
                      }
                    >
                      <span
                        className={styles.colorDot}
                        style={{ backgroundColor: t.color || 'var(--accent)' }}
                      />
                      <span>{t.name}</span>
                      {isChecked && <span>✓</span>}
                    </div>
                  );
                })}
              </div>
            </div>

            <div className={styles.modalFooter}>
              <button
                type="button"
                className="btn ghost"
                onClick={() => setBatchTagsModal({ open: false, op: 'add', selectedTagIds: {} })}
              >
                取消
              </button>
              <button
                type="button"
                className={`btn ${batchTagsModal.op === 'add' ? 'primary' : 'danger'}`}
                disabled={batchTagsMutation.isPending}
                onClick={() => batchTagsMutation.mutate()}
              >
                {batchTagsMutation.isPending ? '执行中...' : '确认批量执行'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* ==================================================================== */}
      {/* 弹窗：计费与周期设置 */}
      {/* ==================================================================== */}
      {billingModal.open && billingModal.node && (
        <div className={styles.modalOverlay} onClick={() => setBillingModal({ open: false, billing: {} })}>
          <div className={styles.modalContent} onClick={(e) => e.stopPropagation()}>
            <div className={styles.modalHeader}>
              <h2 className={styles.modalTitle}>
                计费与流量配额: {billingModal.node.name}
              </h2>
              <button
                type="button"
                className={styles.modalCloseBtn}
                onClick={() => setBillingModal({ open: false, billing: {} })}
              >
                ✕
              </button>
            </div>

            <form
              onSubmit={(e) => {
                e.preventDefault();
                saveBillingMutation.mutate();
              }}
            >
              <div className={styles.modalBody}>
                <div className={styles.formGrid}>
                  <div className={styles.formGroup}>
                    <label className={styles.fieldLabel}>价格</label>
                    <input
                      type="number"
                      step="0.01"
                      className={styles.input}
                      placeholder="例如 5.00"
                      value={billingModal.billing.price ?? ''}
                      onChange={(e) =>
                        setBillingModal((prev) => ({
                          ...prev,
                          billing: {
                            ...prev.billing,
                            price: e.target.value === '' ? undefined : parseFloat(e.target.value),
                          },
                        }))
                      }
                    />
                  </div>

                  <div className={styles.formGroup}>
                    <label className={styles.fieldLabel}>货币单位</label>
                    <input
                      type="text"
                      className={styles.input}
                      placeholder="USD / CNY / EUR"
                      value={billingModal.billing.currency || ''}
                      onChange={(e) =>
                        setBillingModal((prev) => ({
                          ...prev,
                          billing: { ...prev.billing, currency: e.target.value },
                        }))
                      }
                    />
                  </div>

                  <div className={styles.formGroup}>
                    <label className={styles.fieldLabel}>计费周期 (天数)</label>
                    <input
                      type="number"
                      className={styles.input}
                      placeholder="30 (月付) / 365 (年付)"
                      value={billingModal.billing.cycle_days ?? ''}
                      onChange={(e) =>
                        setBillingModal((prev) => ({
                          ...prev,
                          billing: {
                            ...prev.billing,
                            cycle_days:
                              e.target.value === '' ? undefined : parseInt(e.target.value, 10),
                          },
                        }))
                      }
                    />
                  </div>

                  <div className={styles.formGroup}>
                    <label className={styles.fieldLabel}>到期时间</label>
                    <input
                      type="date"
                      className={styles.input}
                      value={
                        billingModal.billing.expires_at_ms
                          ? new Date(billingModal.billing.expires_at_ms).toISOString().split('T')[0]
                          : ''
                      }
                      onChange={(e) => {
                        const ms = e.target.value ? new Date(e.target.value).getTime() : undefined;
                        setBillingModal((prev) => ({
                          ...prev,
                          billing: { ...prev.billing, expires_at_ms: ms },
                        }));
                      }}
                    />
                  </div>

                  <div className={styles.formGroup}>
                    <label className={styles.fieldLabel}>月度流量配额 (GB)</label>
                    <input
                      type="number"
                      className={styles.input}
                      placeholder="例如 1000"
                      value={
                        billingModal.billing.traffic_limit
                          ? Math.round(billingModal.billing.traffic_limit / (1024 * 1024 * 1024))
                          : ''
                      }
                      onChange={(e) => {
                        const gb = e.target.value ? parseInt(e.target.value, 10) : undefined;
                        const bytes = gb ? gb * 1024 * 1024 * 1024 : undefined;
                        setBillingModal((prev) => ({
                          ...prev,
                          billing: { ...prev.billing, traffic_limit: bytes },
                        }));
                      }}
                    />
                  </div>

                  <div className={styles.formGroup}>
                    <label className={styles.fieldLabel}>流量计算方向</label>
                    <select
                      className={styles.select}
                      value={billingModal.billing.traffic_limit_kind || 'both'}
                      onChange={(e) =>
                        setBillingModal((prev) => ({
                          ...prev,
                          billing: { ...prev.billing, traffic_limit_kind: e.target.value },
                        }))
                      }
                    >
                      <option value="both">双向流量 (出+入)</option>
                      <option value="up">仅出网流量 (Up)</option>
                      <option value="down">仅入网流量 (Down)</option>
                    </select>
                  </div>

                  <div className={styles.formGroup}>
                    <label className={styles.fieldLabel}>每月重置日 (1-31)</label>
                    <input
                      type="number"
                      min="1"
                      max="31"
                      className={styles.input}
                      placeholder="默认 1"
                      value={billingModal.billing.traffic_reset_day ?? ''}
                      onChange={(e) =>
                        setBillingModal((prev) => ({
                          ...prev,
                          billing: {
                            ...prev.billing,
                            traffic_reset_day:
                              e.target.value === '' ? undefined : parseInt(e.target.value, 10),
                          },
                        }))
                      }
                    />
                  </div>

                  <div className={styles.formGroup} style={{ justifyContent: 'center' }}>
                    <label className={styles.checkboxRow}>
                      <input
                        type="checkbox"
                        checked={!!billingModal.billing.is_auto_renew}
                        onChange={(e) =>
                          setBillingModal((prev) => ({
                            ...prev,
                            billing: { ...prev.billing, is_auto_renew: e.target.checked },
                          }))
                        }
                      />
                      <span>自动续费 (到期提醒忽略)</span>
                    </label>
                  </div>
                </div>
              </div>

              <div className={styles.modalFooter}>
                <button
                  type="button"
                  className="btn danger ghost"
                  style={{ marginRight: 'auto' }}
                  onClick={() => clearBillingMutation.mutate()}
                  disabled={clearBillingMutation.isPending}
                >
                  清除计费
                </button>
                <button
                  type="button"
                  className="btn ghost"
                  onClick={() => setBillingModal({ open: false, billing: {} })}
                >
                  取消
                </button>
                <button
                  type="submit"
                  className="btn primary"
                  disabled={saveBillingMutation.isPending}
                >
                  {saveBillingMutation.isPending ? '保存中...' : '保存计费信息'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* ==================================================================== */}
      {/* 弹窗：新建 / 编辑分组 */}
      {/* ==================================================================== */}
      {groupModal.open && (
        <div className={styles.modalOverlay} onClick={() => setGroupModal({ open: false, mode: 'create' })}>
          <div className={styles.modalContent} onClick={(e) => e.stopPropagation()}>
            <div className={styles.modalHeader}>
              <h2 className={styles.modalTitle}>
                {groupModal.mode === 'create' ? '新建分组' : '编辑分组'}
              </h2>
              <button
                type="button"
                className={styles.modalCloseBtn}
                onClick={() => setGroupModal({ open: false, mode: 'create' })}
              >
                ✕
              </button>
            </div>

            <form
              onSubmit={(e) => {
                e.preventDefault();
                saveGroupMutation.mutate();
              }}
            >
              <div className={styles.modalBody}>
                <div className={styles.formGroup}>
                  <label className={styles.fieldLabel}>分组名称 *</label>
                  <input
                    type="text"
                    required
                    className={styles.input}
                    placeholder="例如 香港 / 日本 / 生产环境"
                    value={groupForm.name}
                    onChange={(e) => setGroupForm((prev) => ({ ...prev, name: e.target.value }))}
                  />
                </div>

                <div className={styles.formGroup}>
                  <label className={styles.fieldLabel}>排序权重 (数值越小越靠前)</label>
                  <input
                    type="number"
                    className={styles.input}
                    value={groupForm.display_order}
                    onChange={(e) =>
                      setGroupForm((prev) => ({
                        ...prev,
                        display_order: parseInt(e.target.value, 10) || 0,
                      }))
                    }
                  />
                </div>
              </div>

              <div className={styles.modalFooter}>
                <button
                  type="button"
                  className="btn ghost"
                  onClick={() => setGroupModal({ open: false, mode: 'create' })}
                >
                  取消
                </button>
                <button
                  type="submit"
                  className="btn primary"
                  disabled={saveGroupMutation.isPending}
                >
                  {saveGroupMutation.isPending ? '保存中...' : '保存'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* ==================================================================== */}
      {/* 弹窗：删除分组确认 */}
      {/* ==================================================================== */}
      {deleteGroupModal.open && deleteGroupModal.group && (
        <div className={styles.modalOverlay} onClick={() => setDeleteGroupModal({ open: false })}>
          <div className={styles.modalContent} onClick={(e) => e.stopPropagation()}>
            <div className={styles.modalHeader}>
              <h2 className={styles.modalTitle} style={{ color: 'var(--err)' }}>
                确认删除分组
              </h2>
              <button
                type="button"
                className={styles.modalCloseBtn}
                onClick={() => setDeleteGroupModal({ open: false })}
              >
                ✕
              </button>
            </div>

            <div className={styles.modalBody}>
              <p>
                您确定要删除分组 <strong>{deleteGroupModal.group.name}</strong> 吗？
              </p>
              <div className={styles.warningBox}>
                <strong>注意：</strong> 分组删除后，其下的机器将被置为「未分组」，<strong>不会删除任何机器</strong>。
              </div>
            </div>

            <div className={styles.modalFooter}>
              <button
                type="button"
                className="btn ghost"
                onClick={() => setDeleteGroupModal({ open: false })}
              >
                取消
              </button>
              <button
                type="button"
                className="btn danger"
                disabled={deleteGroupMutation.isPending}
                onClick={() => deleteGroupMutation.mutate(deleteGroupModal.group!.id)}
              >
                {deleteGroupMutation.isPending ? '删除中...' : '确认删除'}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* ==================================================================== */}
      {/* 弹窗：新建 / 编辑标签 */}
      {/* ==================================================================== */}
      {tagModal.open && (
        <div className={styles.modalOverlay} onClick={() => setTagModal({ open: false, mode: 'create' })}>
          <div className={styles.modalContent} onClick={(e) => e.stopPropagation()}>
            <div className={styles.modalHeader}>
              <h2 className={styles.modalTitle}>
                {tagModal.mode === 'create' ? '新建标签' : '编辑标签'}
              </h2>
              <button
                type="button"
                className={styles.modalCloseBtn}
                onClick={() => setTagModal({ open: false, mode: 'create' })}
              >
                ✕
              </button>
            </div>

            <form
              onSubmit={(e) => {
                e.preventDefault();
                saveTagMutation.mutate();
              }}
            >
              <div className={styles.modalBody}>
                <div className={styles.formGroup}>
                  <label className={styles.fieldLabel}>标签名称 *</label>
                  <input
                    type="text"
                    required
                    className={styles.input}
                    placeholder="例如 proxy / ingress / database"
                    value={tagForm.name}
                    onChange={(e) => setTagForm((prev) => ({ ...prev, name: e.target.value }))}
                  />
                </div>

                <div className={styles.formGroup}>
                  <label className={styles.fieldLabel}>预设颜色</label>
                  <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginTop: 4 }}>
                    {PRESET_COLORS.map((c) => (
                      <button
                        key={c.val}
                        type="button"
                        style={{
                          width: 26,
                          height: 26,
                          borderRadius: '50%',
                          backgroundColor: c.val,
                          border:
                            tagForm.color === c.val
                              ? '2px solid var(--text-main)'
                              : '1px solid var(--border-main)',
                          cursor: 'pointer',
                        }}
                        onClick={() => setTagForm((prev) => ({ ...prev, color: c.val }))}
                        title={c.name}
                      />
                    ))}
                  </div>
                </div>

                <div className={styles.formGroup}>
                  <label className={styles.fieldLabel}>自定义颜色代码 (HEX)</label>
                  <input
                    type="text"
                    className={`${styles.input} cell-mono`}
                    placeholder="#4da6ff"
                    value={tagForm.color}
                    onChange={(e) => setTagForm((prev) => ({ ...prev, color: e.target.value }))}
                  />
                </div>
              </div>

              <div className={styles.modalFooter}>
                <button
                  type="button"
                  className="btn ghost"
                  onClick={() => setTagModal({ open: false, mode: 'create' })}
                >
                  取消
                </button>
                <button type="submit" className="btn primary" disabled={saveTagMutation.isPending}>
                  {saveTagMutation.isPending ? '保存中...' : '保存'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* ==================================================================== */}
      {/* 弹窗：删除标签确认 */}
      {/* ==================================================================== */}
      {deleteTagModal.open && deleteTagModal.tag && (
        <div className={styles.modalOverlay} onClick={() => setDeleteTagModal({ open: false })}>
          <div className={styles.modalContent} onClick={(e) => e.stopPropagation()}>
            <div className={styles.modalHeader}>
              <h2 className={styles.modalTitle} style={{ color: 'var(--err)' }}>
                确认删除标签
              </h2>
              <button
                type="button"
                className={styles.modalCloseBtn}
                onClick={() => setDeleteTagModal({ open: false })}
              >
                ✕
              </button>
            </div>

            <div className={styles.modalBody}>
              <p>
                您确定要删除标签 <strong>{deleteTagModal.tag.name}</strong> 吗？
              </p>
              <div className={styles.warningBox}>
                <strong>注意：</strong> 标签删除后，所有打有该标签的节点将自动解除关联，<strong>不会删除节点</strong>。
              </div>
            </div>

            <div className={styles.modalFooter}>
              <button
                type="button"
                className="btn ghost"
                onClick={() => setDeleteTagModal({ open: false })}
              >
                取消
              </button>
              <button
                type="button"
                className="btn danger"
                disabled={deleteTagMutation.isPending}
                onClick={() => deleteTagMutation.mutate(deleteTagModal.tag!.id)}
              >
                {deleteTagMutation.isPending ? '删除中...' : '确认删除'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
};
