import { apiFetch } from './client';
import { toItems } from './envelope';

export type JobState = 'pending' | 'running' | 'succeeded' | 'failed' | 'cancelled';
export type StepState = 'pending' | 'running' | 'succeeded' | 'failed' | 'skipped';

export interface JobStep {
  id: string;
  job_id: string;
  step_index: number;
  name: string;
  step_state: StepState;
  log_text?: string;
  started_at_ms?: number;
  finished_at_ms?: number;
  created_at_ms: number;
  updated_at_ms: number;
}

export interface Job {
  id: string;
  job_kind: string;
  job_state: JobState;
  target_kind?: string;
  target_id?: string;
  params_json?: string;
  params?: Record<string, any>;
  result_json?: string;
  result?: Record<string, any>;
  error_text?: string;
  attempt: number;
  max_attempt: number;
  scheduled_at_ms: number;
  started_at_ms?: number;
  finished_at_ms?: number;
  created_at_ms: number;
  updated_at_ms: number;
  steps?: JobStep[];
}

export interface JobStepDef {
  name: string;
  idempotent: boolean;
}

export interface JobDefinition {
  kind: string;
  description: string;
  timeout: number;
  max_attempt: number;
  steps: JobStepDef[];
}

export interface SubmitJobRequest {
  kind: string;
  target_kind?: string;
  target_id?: string;
  params?: Record<string, any>;
  max_attempt?: number;
  reject_if_busy?: boolean;
}

export interface JobsListResponse {
  jobs: Job[];
  total: number;
  page?: number;
  page_size?: number;
}

export async function fetchJobs(params?: {
  kind?: string;
  state?: string;
  target_kind?: string;
  target_id?: string;
  limit?: number;
  offset?: number;
}): Promise<JobsListResponse> {
  const query = new URLSearchParams();
  if (params?.kind) query.set('kind', params.kind);
  if (params?.state && params.state !== 'all') query.set('state', params.state);
  if (params?.target_kind) query.set('target_kind', params.target_kind);
  if (params?.target_id) query.set('target_id', params.target_id);
  if (params?.limit) query.set('limit', params.limit.toString());
  if (params?.offset !== undefined) query.set('offset', params.offset.toString());

  const qs = query.toString();
  const raw = await apiFetch<any>(`/api/v1/jobs${qs ? `?${qs}` : ''}`);
  return {
    jobs: toItems<Job>(raw),
    total: typeof raw?.total === 'number' ? raw.total : 0,
    page: raw?.page,
    page_size: raw?.page_size,
  };
}

export async function fetchJobDetail(id: string): Promise<{ job: Job }> {
  return apiFetch<{ job: Job }>(`/api/v1/jobs/${id}`);
}

export async function fetchJobKinds(): Promise<{ kinds: JobDefinition[] }> {
  return apiFetch<{ kinds: JobDefinition[] }>('/api/v1/jobs/kinds');
}

export async function submitJob(req: SubmitJobRequest): Promise<{ job: Job }> {
  return apiFetch<{ job: Job }>('/api/v1/jobs', {
    method: 'POST',
    body: JSON.stringify(req),
  });
}

export async function cancelJob(id: string): Promise<{ job: Job }> {
  return apiFetch<{ job: Job }>(`/api/v1/jobs/${id}/cancel`, {
    method: 'POST',
  });
}

export async function retryJob(id: string): Promise<{ job: Job }> {
  return apiFetch<{ job: Job }>(`/api/v1/jobs/${id}/retry`, {
    method: 'POST',
  });
}
