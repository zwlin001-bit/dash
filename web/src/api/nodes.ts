import { apiFetch } from './client';
import { NodeDetail, NodeItem, SystemSettings } from './types';
import { mockNodeDetail, mockNodes, mockSettings } from '../mock/mockData';

export async function getNodes(): Promise<NodeItem[]> {
  try {
    const res = await apiFetch<{ items: NodeItem[] }>('/api/v1/nodes');
    return res.items;
  } catch {
    return mockNodes;
  }
}

export async function getNode(id: string): Promise<NodeDetail> {
  try {
    return await apiFetch<NodeDetail>(`/api/v1/nodes/${id}`);
  } catch {
    return { ...mockNodeDetail, id };
  }
}

export async function getSettings(): Promise<SystemSettings> {
  try {
    return await apiFetch<SystemSettings>('/api/v1/settings');
  } catch {
    return mockSettings;
  }
}
