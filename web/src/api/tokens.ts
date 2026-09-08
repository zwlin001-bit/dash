import { apiFetch } from './client';
import { toItems } from './envelope';
import {
  EnrollToken,
  CreateEnrollTokenParams,
  CreateEnrollTokenResponse,
  PageResult,
} from './types';

/**
 * 获取注册令牌列表。
 * 通过 toItems 统一拆解服务端分页信封 {items:[...], total, page, page_size}。
 */
export async function getEnrollTokens(page: number = 1, pageSize: number = 50): Promise<EnrollToken[]> {
  const res = await apiFetch<unknown>(`/api/v1/enroll-tokens?page=${page}&page_size=${pageSize}`);
  return toItems<EnrollToken>(res);
}

export async function getEnrollTokensPaged(page: number = 1, pageSize: number = 50): Promise<PageResult<EnrollToken>> {
  const raw = await apiFetch<any>(`/api/v1/enroll-tokens?page=${page}&page_size=${pageSize}`);
  return {
    items: toItems<EnrollToken>(raw),
    total: typeof raw?.total === 'number' ? raw.total : 0,
    page: typeof raw?.page === 'number' ? raw.page : page,
    page_size: typeof raw?.page_size === 'number' ? raw.page_size : pageSize,
  };
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
