import { apiFetch } from './client';
import { toItems } from './envelope';
import { NotifyChannel, NotifyRule, NotifyDelivery } from './types';

export interface DeliveriesFilter {
  event_id?: string;
  channel_id?: string;
  state?: string;
  limit?: number;
  offset?: number;
}

export interface DeliveriesListResponse {
  deliveries: NotifyDelivery[];
  total: number;
  limit: number;
  offset: number;
}

// Channels

export async function fetchNotifyChannels(): Promise<NotifyChannel[]> {
  const res = await apiFetch<unknown>('/api/v1/notify/channels');
  return toItems<NotifyChannel>(res);
}

export async function createNotifyChannel(data: {
  name: string;
  channel_kind: string;
  secret?: string;
  config_json: string;
}): Promise<NotifyChannel> {
  return apiFetch<NotifyChannel>('/api/v1/notify/channels', {
    method: 'POST',
    body: JSON.stringify(data),
  });
}

export async function updateNotifyChannel(
  id: string,
  data: {
    name?: string;
    is_enabled?: boolean;
    secret?: string;
    config_json?: string;
  }
): Promise<NotifyChannel> {
  return apiFetch<NotifyChannel>(`/api/v1/notify/channels/${encodeURIComponent(id)}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  });
}

export async function deleteNotifyChannel(id: string): Promise<{ ok: boolean }> {
  return apiFetch<{ ok: boolean }>(`/api/v1/notify/channels/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  });
}

export async function testNotifyChannel(id: string, text?: string): Promise<{ ok: boolean; message: string }> {
  return apiFetch<{ ok: boolean; message: string }>(`/api/v1/notify/channels/${encodeURIComponent(id)}/test`, {
    method: 'POST',
    body: JSON.stringify({ text: text || '' }),
  });
}

// Rules

export async function fetchNotifyRules(): Promise<NotifyRule[]> {
  const res = await apiFetch<unknown>('/api/v1/notify/rules');
  return toItems<NotifyRule>(res);
}

export async function createNotifyRule(data: Partial<NotifyRule>): Promise<NotifyRule> {
  return apiFetch<NotifyRule>('/api/v1/notify/rules', {
    method: 'POST',
    body: JSON.stringify(data),
  });
}

export async function updateNotifyRule(id: string, data: Partial<NotifyRule>): Promise<NotifyRule> {
  return apiFetch<NotifyRule>(`/api/v1/notify/rules/${encodeURIComponent(id)}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  });
}

export async function deleteNotifyRule(id: string): Promise<{ ok: boolean }> {
  return apiFetch<{ ok: boolean }>(`/api/v1/notify/rules/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  });
}

// Deliveries

export async function fetchNotifyDeliveries(filter: DeliveriesFilter = {}): Promise<DeliveriesListResponse> {
  const params = new URLSearchParams();
  if (filter.event_id) params.set('event_id', filter.event_id);
  if (filter.channel_id) params.set('channel_id', filter.channel_id);
  if (filter.state) params.set('state', filter.state);
  if (filter.limit) params.set('limit', filter.limit.toString());
  if (filter.offset !== undefined) params.set('offset', filter.offset.toString());

  const qs = params.toString();
  const url = qs ? `/api/v1/notify/deliveries?${qs}` : '/api/v1/notify/deliveries';
  const raw = await apiFetch<any>(url);
  return {
    deliveries: toItems<NotifyDelivery>(raw),
    total: typeof raw?.total === 'number' ? raw.total : 0,
    limit: typeof raw?.limit === 'number' ? raw.limit : (filter.limit || 50),
    offset: typeof raw?.offset === 'number' ? raw.offset : (filter.offset || 0),
  };
}
