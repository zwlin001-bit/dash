import { apiFetch } from './client';
import { toItems } from './envelope';
import { NodeGroup, CreateGroupParams, UpdateGroupParams } from './types';

/**
 * 获取所有分组列表。
 * 通过 toItems 统一拆解服务端分页信封 {items:[...], total, page, page_size}。
 * 错误照常抛出，不进行静默 catch 吞异常（P1-27 §3）。
 */
export async function getGroups(): Promise<NodeGroup[]> {
  const res = await apiFetch<unknown>('/api/v1/node-groups');
  return toItems<NodeGroup>(res);
}

// 兼容既有别名
export const getNodeGroups = getGroups;

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
