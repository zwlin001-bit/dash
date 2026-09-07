import { apiFetch } from './client';
import {
  BatchTagParams,
  CreateEnrollTokenParams,
  CreateEnrollTokenResponse,
  CreateGroupParams,
  CreateNodeParams,
  CreateTagParams,
  EnrollToken,
  HealthResponse,
  NodeBilling,
  NodeDetail,
  NodeGroup,
  NodeItem,
  NodeTag,
  PageResult,
  SystemSettings,
  UpdateBillingParams,
  UpdateGroupParams,
  UpdateNodeParams,
  UpdateTagParams,
} from './types';

export interface ListNodesParams {
  group_id?: string;
  tag_id?: string;
  conn_state?: string;
  q?: string;
  sort?: string;
  order?: string;
  page?: number;
  page_size?: number;
}

// 节点查询
export async function getNodes(params?: ListNodesParams | unknown): Promise<NodeItem[]> {
  const filter = params && typeof params === 'object' && !('queryKey' in params)
    ? (params as ListNodesParams)
    : undefined;
  const res = await getNodesPaged(filter);
  return res.items;
}

export async function getNodesPaged(params?: ListNodesParams): Promise<PageResult<NodeItem>> {
  const query = new URLSearchParams();
  if (params?.group_id) query.set('group_id', params.group_id);
  if (params?.tag_id) query.set('tag_id', params.tag_id);
  if (params?.conn_state) query.set('conn_state', params.conn_state);
  if (params?.q) query.set('q', params.q);
  if (params?.sort) query.set('sort', params.sort);
  if (params?.order) query.set('order', params.order);
  if (params?.page) query.set('page', String(params.page));
  if (params?.page_size) query.set('page_size', String(params.page_size));

  const qs = query.toString();
  return await apiFetch<PageResult<NodeItem>>(`/api/v1/nodes${qs ? `?${qs}` : ''}`);
}

export async function getNode(id: string): Promise<NodeDetail> {
  return await apiFetch<NodeDetail>(`/api/v1/nodes/${id}`);
}

export async function createNode(params: CreateNodeParams): Promise<NodeDetail> {
  return await apiFetch<NodeDetail>('/api/v1/nodes', {
    method: 'POST',
    body: JSON.stringify(params),
  });
}

export async function updateNode(id: string, params: UpdateNodeParams): Promise<NodeDetail> {
  return await apiFetch<NodeDetail>(`/api/v1/nodes/${id}`, {
    method: 'PATCH',
    body: JSON.stringify(params),
  });
}

export async function deleteNode(id: string): Promise<{ ok: boolean; message: string }> {
  return await apiFetch<{ ok: boolean; message: string }>(`/api/v1/nodes/${id}`, {
    method: 'DELETE',
  });
}

export async function revokeNodeToken(id: string): Promise<{ ok: boolean }> {
  return await apiFetch<{ ok: boolean }>(`/api/v1/nodes/${id}/token:revoke`, {
    method: 'POST',
  });
}

// 健康检查（docs/12-api-spec.md §9）
export async function checkHealth(): Promise<HealthResponse> {
  try {
    return await apiFetch<HealthResponse>('/healthz');
  } catch {
    return { status: 'error', db: 'error' };
  }
}

// 分组管理
export async function getGroups(): Promise<NodeGroup[]> {
  try {
    return await apiFetch<NodeGroup[]>('/api/v1/node-groups');
  } catch {
    return [];
  }
}

export { getGroups as getNodeGroups };

export async function createGroup(params: CreateGroupParams): Promise<NodeGroup> {
  return await apiFetch<NodeGroup>('/api/v1/node-groups', {
    method: 'POST',
    body: JSON.stringify(params),
  });
}

export async function updateGroup(id: string, params: UpdateGroupParams): Promise<NodeGroup> {
  return await apiFetch<NodeGroup>(`/api/v1/node-groups/${id}`, {
    method: 'PATCH',
    body: JSON.stringify(params),
  });
}

export async function deleteGroup(id: string): Promise<{ ok: boolean }> {
  return await apiFetch<{ ok: boolean }>(`/api/v1/node-groups/${id}`, {
    method: 'DELETE',
  });
}

// 标签管理
export async function getTags(): Promise<NodeTag[]> {
  try {
    return await apiFetch<NodeTag[]>('/api/v1/tags');
  } catch {
    return [];
  }
}

export async function createTag(params: CreateTagParams): Promise<NodeTag> {
  return await apiFetch<NodeTag>('/api/v1/tags', {
    method: 'POST',
    body: JSON.stringify(params),
  });
}

export async function updateTag(id: string, params: UpdateTagParams): Promise<NodeTag> {
  return await apiFetch<NodeTag>(`/api/v1/tags/${id}`, {
    method: 'PATCH',
    body: JSON.stringify(params),
  });
}

export async function deleteTag(id: string): Promise<{ ok: boolean }> {
  return await apiFetch<{ ok: boolean }>(`/api/v1/tags/${id}`, {
    method: 'DELETE',
  });
}

export async function replaceNodeTags(id: string, tagIds: string[]): Promise<NodeTag[]> {
  return await apiFetch<NodeTag[]>(`/api/v1/nodes/${id}/tags`, {
    method: 'POST',
    body: JSON.stringify({ tag_ids: tagIds }),
  });
}

export async function batchNodeTags(params: BatchTagParams): Promise<{ ok: boolean; affected?: number }> {
  return await apiFetch<{ ok: boolean; affected?: number }>('/api/v1/nodes/tags:batch', {
    method: 'POST',
    body: JSON.stringify(params),
  });
}

// 计费信息管理
export async function getNodeBilling(id: string): Promise<NodeBilling> {
  return await apiFetch<NodeBilling>(`/api/v1/nodes/${id}/billing`);
}

export async function updateNodeBilling(id: string, params: UpdateBillingParams): Promise<NodeBilling> {
  return await apiFetch<NodeBilling>(`/api/v1/nodes/${id}/billing`, {
    method: 'PUT',
    body: JSON.stringify(params),
  });
}

export async function deleteNodeBilling(id: string): Promise<{ ok: boolean }> {
  return await apiFetch<{ ok: boolean }>(`/api/v1/nodes/${id}/billing`, {
    method: 'DELETE',
  });
}

// 注册令牌管理 (装机)
export async function getEnrollTokens(page: number = 1, pageSize: number = 50): Promise<PageResult<EnrollToken>> {
  return await apiFetch<PageResult<EnrollToken>>(`/api/v1/enroll-tokens?page=${page}&page_size=${pageSize}`);
}

export async function createEnrollToken(params: CreateEnrollTokenParams): Promise<CreateEnrollTokenResponse> {
  return await apiFetch<CreateEnrollTokenResponse>('/api/v1/enroll-tokens', {
    method: 'POST',
    body: JSON.stringify(params),
  });
}

export async function deleteEnrollToken(id: string): Promise<{ ok: boolean }> {
  return await apiFetch<{ ok: boolean }>(`/api/v1/enroll-tokens/${id}`, {
    method: 'DELETE',
  });
}

// 系统设置
export async function getSettings(): Promise<SystemSettings> {
  return await apiFetch<SystemSettings>('/api/v1/settings');
}

export async function updateSettings(updates: Partial<SystemSettings>): Promise<SystemSettings> {
  return await apiFetch<SystemSettings>('/api/v1/settings', {
    method: 'PATCH',
    body: JSON.stringify(updates),
  });
}
