import { apiFetch } from './client';
import { NodeBilling, UpdateBillingParams } from './types';

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
