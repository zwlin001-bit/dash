import { apiFetch } from './client';
import { toItems } from './envelope';

export interface EventRecord {
  id: string;
  event_type: string;
  severity: 'info' | 'warning' | 'critical';
  source_module: string;
  target_kind?: string;
  target_id?: string;
  title: string;
  payload?: Record<string, any>;
  payload_json?: string;
  dedup_key?: string;
  is_read: boolean;
  occurred_at_ms: number;
  created_at_ms: number;
}

export interface EventType {
  event_type: string;
  display_name: string;
  default_severity: string;
  severity: string;
  default_disposition: string;
  disposition: string;
  description: string;
  is_builtin: boolean;
  updated_at_ms: number;
}

export interface EventsFilter {
  event_type?: string;
  severity?: string;
  target_id?: string;
  is_read?: boolean;
  from_ms?: number;
  to_ms?: number;
  limit?: number;
  offset?: number;
}

export interface EventsListResult {
  events: EventRecord[];
  total: number;
  limit: number;
  offset: number;
}

export async function fetchEvents(filter: EventsFilter = {}): Promise<EventsListResult> {
  const params = new URLSearchParams();
  if (filter.event_type) params.set('event_type', filter.event_type);
  if (filter.severity) params.set('severity', filter.severity);
  if (filter.target_id) params.set('target_id', filter.target_id);
  if (filter.is_read !== undefined) params.set('is_read', filter.is_read ? '1' : '0');
  if (filter.from_ms) params.set('from_ms', filter.from_ms.toString());
  if (filter.to_ms) params.set('to_ms', filter.to_ms.toString());
  if (filter.limit) params.set('limit', filter.limit.toString());
  if (filter.offset !== undefined) params.set('offset', filter.offset.toString());

  const qs = params.toString();
  const url = qs ? `/api/v1/events?${qs}` : '/api/v1/events';
  const raw = await apiFetch<any>(url);
  return {
    events: toItems<EventRecord>(raw),
    total: typeof raw?.total === 'number' ? raw.total : 0,
    limit: typeof raw?.limit === 'number' ? raw.limit : (filter.limit || 50),
    offset: typeof raw?.offset === 'number' ? raw.offset : (filter.offset || 0),
  };
}

export async function fetchUnreadCount(): Promise<{ unread_count: number }> {
  return apiFetch<{ unread_count: number }>('/api/v1/events/unread-count');
}

export async function markEventsRead(req: {
  ids?: string[];
  before_ms?: number;
  all?: boolean;
}): Promise<{ affected: number }> {
  return apiFetch<{ affected: number }>('/api/v1/events/read', {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

export async function fetchEventTypes(): Promise<EventType[]> {
  const raw = await apiFetch<unknown>('/api/v1/event-types');
  return toItems<EventType>(raw);
}

export async function updateEventType(
  eventType: string,
  update: { severity?: string; disposition?: string }
): Promise<EventType> {
  return apiFetch<EventType>(`/api/v1/event-types/${encodeURIComponent(eventType)}`, {
    method: 'PATCH',
    body: JSON.stringify(update),
  });
}
