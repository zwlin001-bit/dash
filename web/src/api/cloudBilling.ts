import { apiFetch } from './client';

export interface CurrencySummary {
  currency: string;
  total_amount: number;
  pretax_amount?: number;
  discount_amount?: number;
  mom_ratio?: number; // month-over-month ratio, e.g. 0.05 is +5%
  budget_amount?: number;
  budget_progress?: number;
}

export interface MonthPoint {
  period: string; // "2026-09"
  amount: number;
}

export interface CurrencyTrend {
  currency: string;
  months: MonthPoint[];
}

export interface BillingOverview {
  period: string;
  totals: CurrencySummary[];
  trends: CurrencyTrend[];
  synced_at_ms?: number;
  sync_state: string; // ok, syncing, failed, pending
}

export interface BillItem {
  id: string;
  cloud_account_id: string;
  period: string;
  res_kind: string;
  res_ref?: string;
  cloud_resource_id?: string;
  item_name?: string;
  product_code?: string;
  currency: string;
  amount: number;
  usage_text?: string;
  created_at_ms: number;
  updated_at_ms: number;
  account_name?: string;
  tags?: string[];
}

export interface AggregatedGroup {
  key: string;
  display_name: string;
  total_amount: number;
  currency: string;
  item_count: number;
  is_unlinked: boolean;
  items?: BillItem[];
}

export interface ItemsResponse {
  period: string;
  groups: AggregatedGroup[];
  items: BillItem[];
}

export interface BillBudget {
  id: string;
  scope_kind: string; // all, account, tag
  scope_ref?: string;
  period_kind?: string; // month
  currency: string;
  amount: number;
  warn_ratio: number;
  is_enabled: boolean;
  current_spent?: number;
  progress?: number;
  created_at_ms?: number;
  updated_at_ms?: number;
}

export interface CreateBudgetParams {
  scope_kind: string;
  scope_ref?: string;
  period_kind?: string;
  currency: string;
  amount: number;
  warn_ratio: number;
  is_enabled?: boolean;
}

export async function getBillingOverview(period?: string): Promise<BillingOverview> {
  const url = period ? `/api/v1/billing/overview?period=${encodeURIComponent(period)}` : '/api/v1/billing/overview';
  return await apiFetch<BillingOverview>(url);
}

export async function getBillingItems(params?: {
  period?: string;
  group_by?: 'kind' | 'resource' | 'tag';
  account_id?: string;
}): Promise<ItemsResponse> {
  const q = new URLSearchParams();
  if (params?.period) q.set('period', params.period);
  if (params?.group_by) q.set('group_by', params.group_by);
  if (params?.account_id) q.set('account_id', params.account_id);
  const qs = q.toString();
  return await apiFetch<ItemsResponse>(`/api/v1/billing/items${qs ? '?' + qs : ''}`);
}

export async function listBudgets(): Promise<{ items: BillBudget[] }> {
  return await apiFetch<{ items: BillBudget[] }>('/api/v1/billing/budgets');
}

export async function createBudget(params: CreateBudgetParams): Promise<BillBudget> {
  return await apiFetch<BillBudget>('/api/v1/billing/budgets', {
    method: 'POST',
    body: JSON.stringify(params),
  });
}

export async function updateBudget(id: string, params: Partial<BillBudget>): Promise<BillBudget> {
  return await apiFetch<BillBudget>(`/api/v1/billing/budgets/${id}`, {
    method: 'PUT',
    body: JSON.stringify(params),
  });
}

export async function deleteBudget(id: string): Promise<{ ok: boolean }> {
  return await apiFetch<{ ok: boolean }>(`/api/v1/billing/budgets/${id}`, {
    method: 'DELETE',
  });
}

export async function triggerBillSync(accountId?: string): Promise<{ message?: string; status?: string; job_id?: string }> {
  const url = accountId ? `/api/v1/billing/sync?account_id=${encodeURIComponent(accountId)}` : '/api/v1/billing/sync';
  return await apiFetch<{ message?: string; status?: string; job_id?: string }>(url, {
    method: 'POST',
  });
}

export async function triggerBudgetEval(): Promise<{ ok: boolean; message: string }> {
  return await apiFetch<{ ok: boolean; message: string }>('/api/v1/billing/eval', {
    method: 'POST',
  });
}
