import { apiFetch } from './client';
import { toItems } from './envelope';

export interface GuardRule {
  id: string;
  cloud_resource_id: string;
  is_enabled: boolean;
  actions_enabled: boolean;
  traffic_limit_gb?: number;
  traffic_action: string;
  schedule_enabled: boolean;
  schedule_start?: string;
  schedule_stop?: string;
  schedule_tz: string;
  last_eval_at_ms?: number;
  last_action?: string;
  last_action_at_ms?: number;
  created_at_ms: number;
  updated_at_ms: number;
}

export interface GuardCycle {
  id: string;
  cloud_account_id: string;
  started_at_ms: number;
  duration_ms: number;
  cdt_used_gb?: number;
  cdt_error?: string;
  evaluated: number;
  acted: number;
  failed: number;
  created_at_ms: number;
}

export interface EvaluationItem {
  account_id: string;
  account_name: string;
  resource_id: string;
  resource_name: string;
  resource_ref: string;
  region: string;
  current_status: string;
  proposed_action: 'stop' | 'start' | 'noop';
  reason: string;
  cdt_used_gb: number;
  traffic_limit_gb?: number;
  usage_percent: number;
  actions_enabled: boolean;
  in_schedule_stop: boolean;
  in_schedule_run: boolean;
  next_schedule_action?: string;
  next_schedule_time_ms?: number;
  would_execute: boolean;
  job_submitted?: boolean;
  job_id?: string;
  error?: string;
}

export interface EvaluateResult {
  cycle_id: string;
  started_at_ms: number;
  duration_ms: number;
  is_dry_run: boolean;
  evaluated_count: number;
  acted_count: number;
  failed_count: number;
  actions: EvaluationItem[];
}

export interface InstanceOverview {
  resource_id: string;
  resource_name: string;
  resource_ref: string;
  region: string;
  status: string;
  public_ips: string[];
  private_ips: string[];
  billing_info?: string;
  rule?: GuardRule;
  next_schedule_action?: string;
  next_schedule_time_ms?: number;
}

export interface AccountOverview {
  account_id: string;
  account_name: string;
  provider_code: string;
  default_region: string;
  account_site: string;
  cdt_used_gb?: number;
  traffic_limit_gb?: number;
  usage_percent: number;
  cdt_error?: string;
  instances: InstanceOverview[];
}

export interface OverviewResponse {
  accounts: AccountOverview[];
  recent_cycles: GuardCycle[];
  total_instances: number;
  guarded_count: number;
  actions_enabled_count: number;
  current_time_ms: number;
}

export interface RuleUpdateRequest {
  is_enabled?: boolean;
  actions_enabled?: boolean;
  traffic_limit_gb?: number;
  traffic_action?: string;
  schedule_enabled?: boolean;
  schedule_start?: string;
  schedule_stop?: string;
  schedule_tz?: string;
}

export interface ForceStartRequest {
  confirm: boolean;
  reason: string;
}

export async function fetchGuardOverview(): Promise<OverviewResponse> {
  return apiFetch<OverviewResponse>('/api/v1/guard/overview');
}

export async function updateGuardRule(resourceId: string, req: RuleUpdateRequest): Promise<GuardRule> {
  return apiFetch<GuardRule>(`/api/v1/guard/rules/${resourceId}`, {
    method: 'PUT',
    body: JSON.stringify(req),
  });
}

export async function dryRunGuard(): Promise<EvaluateResult> {
  const raw = await apiFetch<any>('/api/v1/guard/dry-run', {
    method: 'POST',
  });
  return {
    cycle_id: raw?.cycle_id ?? '',
    started_at_ms: raw?.started_at_ms ?? 0,
    duration_ms: raw?.duration_ms ?? 0,
    is_dry_run: raw?.is_dry_run ?? true,
    evaluated_count: typeof raw?.evaluated_count === 'number' ? raw.evaluated_count : 0,
    acted_count: typeof raw?.acted_count === 'number' ? raw.acted_count : 0,
    failed_count: typeof raw?.failed_count === 'number' ? raw.failed_count : 0,
    actions: toItems<EvaluationItem>(raw),
  };
}

export async function evaluateGuard(): Promise<EvaluateResult> {
  const raw = await apiFetch<any>('/api/v1/guard/evaluate', {
    method: 'POST',
  });
  return {
    cycle_id: raw?.cycle_id ?? '',
    started_at_ms: raw?.started_at_ms ?? 0,
    duration_ms: raw?.duration_ms ?? 0,
    is_dry_run: raw?.is_dry_run ?? false,
    evaluated_count: typeof raw?.evaluated_count === 'number' ? raw.evaluated_count : 0,
    acted_count: typeof raw?.acted_count === 'number' ? raw.acted_count : 0,
    failed_count: typeof raw?.failed_count === 'number' ? raw.failed_count : 0,
    actions: toItems<EvaluationItem>(raw),
  };
}

export async function forceStartInstance(resourceId: string, req: ForceStartRequest): Promise<{ message: string; job_id: string }> {
  return apiFetch<{ message: string; job_id: string }>(`/api/v1/guard/instances/${resourceId}/force-start`, {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

export interface GuardCyclesResponse {
  cycles: GuardCycle[];
  total: number;
  page: number;
  page_size: number;
}

export async function fetchGuardCycles(page = 1, limit = 20): Promise<GuardCyclesResponse> {
  const raw = await apiFetch<any>(`/api/v1/guard/cycles?page=${page}&limit=${limit}`);
  return {
    cycles: toItems<GuardCycle>(raw),
    total: typeof raw?.total === 'number' ? raw.total : 0,
    page: typeof raw?.page === 'number' ? raw.page : page,
    page_size: typeof raw?.page_size === 'number' ? raw.page_size : limit,
  };
}
