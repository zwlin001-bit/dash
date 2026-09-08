import { apiFetch } from './client';
import { toItems } from './envelope';
import {
  NodeItem,
  NodeDetail,
  NodeFacts,
  CreateNodeParams,
  UpdateNodeParams,
  HealthResponse,
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

export interface NodesPagedResult {
  list: NodeItem[];
  total: number;
  page: number;
  page_size: number;
}

export async function getNodes(params?: ListNodesParams | unknown): Promise<NodeItem[]> {
  const filter = params && typeof params === 'object' && !('queryKey' in params)
    ? (params as ListNodesParams)
    : undefined;
  const res = await apiFetch<unknown>(`/api/v1/nodes${buildNodeQueryString(filter)}`);
  return toItems<NodeItem>(res);
}

export async function getNodesPaged(params?: ListNodesParams): Promise<NodesPagedResult> {
  const query = buildNodeQueryString(params);
  const raw = await apiFetch<any>(`/api/v1/nodes${query}`);
  return {
    list: toItems<NodeItem>(raw),
    total: typeof raw?.total === 'number' ? raw.total : 0,
    page: typeof raw?.page === 'number' ? raw.page : 1,
    page_size: typeof raw?.page_size === 'number' ? raw.page_size : 50,
  };
}

function buildNodeQueryString(params?: ListNodesParams): string {
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
  return qs ? `?${qs}` : '';
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

export async function getNodeFacts(id: string): Promise<NodeFacts> {
  return await apiFetch<NodeFacts>(`/api/v1/nodes/${id}/facts`);
}

export async function putNodeFacts(id: string, facts: Partial<NodeFacts>): Promise<NodeFacts> {
  return await apiFetch<NodeFacts>(`/api/v1/nodes/${id}/facts`, {
    method: 'PUT',
    body: JSON.stringify(facts),
  });
}

export async function checkHealth(): Promise<HealthResponse> {
  return await apiFetch<HealthResponse>('/healthz');
}
