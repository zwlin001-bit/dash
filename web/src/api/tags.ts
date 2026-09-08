import { apiFetch } from './client';
import { toItems } from './envelope';
import {
  NodeTag,
  CreateTagParams,
  UpdateTagParams,
  BatchTagParams,
} from './types';

/**
 * 获取所有标签列表。
 * 通过 toItems 统一拆解服务端分页信封 {items:[...], total, page, page_size}。
 * 错误照常抛出，不进行静默 catch 吞异常（P1-27 §3）。
 */
export async function getTags(): Promise<NodeTag[]> {
  const res = await apiFetch<unknown>('/api/v1/tags');
  return toItems<NodeTag>(res);
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

export async function replaceNodeTags(id: string, tagIds: string[]): Promise<{ ok: boolean }> {
  return await apiFetch<{ ok: boolean }>(`/api/v1/nodes/${id}/tags`, {
    method: 'POST',
    body: JSON.stringify({ tag_ids: tagIds }),
  });
}

export async function attachNodeTag(id: string, tagId: string): Promise<{ ok: boolean }> {
  return await apiFetch<{ ok: boolean }>(`/api/v1/nodes/${id}/tags/${tagId}`, {
    method: 'POST',
  });
}

export async function detachNodeTag(id: string, tagId: string): Promise<{ ok: boolean }> {
  return await apiFetch<{ ok: boolean }>(`/api/v1/nodes/${id}/tags/${tagId}`, {
    method: 'DELETE',
  });
}

export async function batchNodeTags(params: BatchTagParams): Promise<{ ok: boolean; affected?: number }> {
  return await apiFetch<{ ok: boolean; affected?: number }>('/api/v1/nodes/tags:batch', {
    method: 'POST',
    body: JSON.stringify(params),
  });
}
