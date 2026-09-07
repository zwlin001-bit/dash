import { apiFetch } from './client';
import {
  ListNodesFilterParams,
  NodeDetail,
  NodeGroup,
  NodeItem,
  NodeTag,
  SystemSettings,
} from './types';
import { mockNodeDetail, mockNodes, mockSettings } from '../mock/mockData';

const CACHE_KEY_NODES = 'dash_cached_nodes';

export async function getNodes(params?: ListNodesFilterParams): Promise<NodeItem[]> {
  const query = new URLSearchParams();
  if (params?.group_id) query.set('group_id', params.group_id);
  if (params?.tag_id) query.set('tag_id', params.tag_id);
  if (params?.conn_state && params.conn_state !== 'all') query.set('conn_state', params.conn_state);
  if (params?.q) query.set('q', params.q);
  if (params?.sort) query.set('sort', params.sort);
  if (params?.order) query.set('order', params.order);
  if (params?.page) query.set('page', params.page.toString());
  if (params?.page_size) query.set('page_size', params.page_size.toString());

  const qs = query.toString();
  const url = `/api/v1/nodes${qs ? `?${qs}` : ''}`;

  try {
    const res = await apiFetch<{ items: NodeItem[] }>(url);
    if (res?.items) {
      try {
        localStorage.setItem(CACHE_KEY_NODES, JSON.stringify(res.items));
      } catch {}
      return res.items;
    }
    return [];
  } catch (err) {
    // 数据库短暂不可用或离线时，优先读取本地持久化缓存
    try {
      const cached = localStorage.getItem(CACHE_KEY_NODES);
      if (cached) {
        const parsed = JSON.parse(cached) as NodeItem[];
        if (Array.isArray(parsed) && parsed.length > 0) {
          return parsed;
        }
      }
    } catch {}
    return mockNodes;
  }
}

export async function getNode(id: string): Promise<NodeDetail> {
  try {
    return await apiFetch<NodeDetail>(`/api/v1/nodes/${id}`);
  } catch {
    // 尝试从节点缓存中恢复基础信息
    try {
      const cached = localStorage.getItem(CACHE_KEY_NODES);
      if (cached) {
        const list = JSON.parse(cached) as NodeItem[];
        const found = list.find((n) => n.id === id);
        if (found) {
          return {
            ...mockNodeDetail,
            ...found,
          };
        }
      }
    } catch {}
    return { ...mockNodeDetail, id };
  }
}

export async function getNodeGroups(): Promise<NodeGroup[]> {
  try {
    const res = await apiFetch<NodeGroup[] | { items: NodeGroup[] }>('/api/v1/node-groups');
    if (Array.isArray(res)) return res;
    if (res && Array.isArray((res as any).items)) return (res as any).items;
    return [];
  } catch {
    return [
      { id: 'grp-hk', name: '香港专线' },
      { id: 'grp-jp', name: '日本核心' },
      { id: 'grp-sg', name: '新加坡备用' },
    ];
  }
}

export async function getTags(): Promise<NodeTag[]> {
  try {
    const res = await apiFetch<NodeTag[] | { items: NodeTag[] }>('/api/v1/tags');
    if (Array.isArray(res)) return res;
    if (res && Array.isArray((res as any).items)) return (res as any).items;
    return [];
  } catch {
    return [
      { id: 't1', name: 'proxy', color: '#4da6ff' },
      { id: 't2', name: 'hk-cmi', color: '#00d4aa' },
      { id: 't3', name: 'bgp', color: '#b392f0' },
      { id: 't4', name: 'backup', color: '#ffb870' },
      { id: 't5', name: 'staging', color: '#8999af' },
    ];
  }
}

export interface HealthCheckResponse {
  status: string;
  version?: string;
  db: 'ok' | 'error' | string;
  agents_online?: number;
}

export async function checkHealth(): Promise<HealthCheckResponse> {
  try {
    return await apiFetch<HealthCheckResponse>('/healthz');
  } catch (err: any) {
    return {
      status: 'error',
      db: 'error',
    };
  }
}

export async function getSettings(): Promise<SystemSettings> {
  try {
    return await apiFetch<SystemSettings>('/api/v1/settings');
  } catch {
    return mockSettings;
  }
}
