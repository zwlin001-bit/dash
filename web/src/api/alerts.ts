import { apiFetch } from './client';

export interface AlertRule {
  id: string;
  name: string;
  is_enabled: boolean;
  rule_kind: 'metric' | 'offline' | 'expiry' | 'traffic' | 'budget';
  scope_kind: 'all' | 'group' | 'tag' | 'node';
  scope_ref?: string;
  metric_code?: string;
  compare_op: 'gt' | 'gte' | 'lt' | 'lte';
  threshold: number;
  duration_s: number;
  severity: 'info' | 'warning' | 'critical';
  silence_s: number;
  include_auto_renew?: boolean;
  extra_json?: string;
  channel_ids?: string[];
  created_at_ms: number;
  updated_at_ms: number;
}

export interface AlertEvent {
  id: string;
  alert_rule_id: string;
  node_id: string;
  event_state: 'firing' | 'resolved';
  fired_at_ms: number;
  resolved_at_ms?: number;
  peak_value?: number;
  detail?: string;
  notified_at_ms?: number;
  created_at_ms: number;
  updated_at_ms: number;

  rule_name?: string;
  node_name?: string;
  rule_kind?: string;
  severity?: string;
}

export interface AlertEventsFilter {
  rule_id?: string;
  node_id?: string;
  state?: string;
  from_ms?: number;
  to_ms?: number;
  limit?: number;
  offset?: number;
}

export async function fetchAlertRules(): Promise<{ items: AlertRule[] }> {
  return apiFetch<{ items: AlertRule[] }>('/api/v1/alerts/rules');
}

export async function createAlertRule(data: Partial<AlertRule>): Promise<AlertRule> {
  return apiFetch<AlertRule>('/api/v1/alerts/rules', {
    method: 'POST',
    body: JSON.stringify(data),
  });
}

export async function updateAlertRule(id: string, data: Partial<AlertRule>): Promise<AlertRule> {
  return apiFetch<AlertRule>(`/api/v1/alerts/rules/${encodeURIComponent(id)}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  });
}

export async function deleteAlertRule(id: string): Promise<{ ok: boolean }> {
  return apiFetch<{ ok: boolean }>(`/api/v1/alerts/rules/${encodeURIComponent(id)}`, {
    method: 'DELETE',
  });
}

export async function fetchAlertEvents(
  filter?: AlertEventsFilter
): Promise<{ items: AlertEvent[]; total: number }> {
  const params = new URLSearchParams();
  if (filter?.rule_id) params.set('rule_id', filter.rule_id);
  if (filter?.node_id) params.set('node_id', filter.node_id);
  if (filter?.state) params.set('state', filter.state);
  if (filter?.from_ms) params.set('from_ms', String(filter.from_ms));
  if (filter?.to_ms) params.set('to_ms', String(filter.to_ms));
  if (filter?.limit) params.set('limit', String(filter.limit));
  if (filter?.offset) params.set('offset', String(filter.offset));

  const qs = params.toString();
  return apiFetch<{ items: AlertEvent[]; total: number }>(`/api/v1/alerts/events${qs ? `?${qs}` : ''}`);
}

export async function fetchActiveAlerts(): Promise<{ items: AlertEvent[]; total: number }> {
  return apiFetch<{ items: AlertEvent[]; total: number }>('/api/v1/alerts/active');
}

export async function triggerAlertEval(): Promise<{ ok: boolean; message: string }> {
  return apiFetch<{ ok: boolean; message: string }>('/api/v1/alerts/eval', {
    method: 'POST',
  });
}
