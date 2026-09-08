import { apiFetch } from './client';
import { TimeSeriesSpan } from './types';

export interface CredentialSummary {
  id: string;
  name: string;
  cred_kind: string;
  fingerprint: string;
  created_at_ms: number;
  updated_at_ms: number;
}

export interface CloudAccount {
  id: string;
  provider_code: string;
  name: string;
  credential_id: string;
  default_region: string;
  account_site: string; // "china" | "international"
  config_json?: string;
  is_enabled: boolean;
  last_sync_at_ms?: number;
  created_at_ms: number;
  updated_at_ms: number;
}

export interface CloudResource {
  id: string;
  cloud_account_id: string;
  provider_code: string;
  res_kind: string;
  res_ref: string;
  name: string;
  region: string;
  status: string;
  public_ips: string;
  private_ips: string;
  specs_json?: string;
  billing_json?: string;
  attrs_json?: string;
  node_id?: string;
  synced_at_ms: number;
  is_deleted: boolean;
  created_at_ms: number;
  updated_at_ms: number;
}

export interface SyncJobStatus {
  job_id: string;
  account_id: string;
  state: string;
  message?: string;
  synced_count: number;
  traffic_bytes: number;
}

export interface DiscoveredResource {
  provider_code: string;
  kind: string;
  ref: string;
  name: string;
  region: string;
  status: string;
  public_ips: string[];
  private_ips: string[];
  specs: { vcpu: number; mem_mb: number };
  billing?: { price?: number; expires_at_ms?: number };
  attrs?: Record<string, any>;
  bill_error?: string;
}

// Credentials API
export const fetchCredentials = async (): Promise<CredentialSummary[]> => {
  const res = await apiFetch<{ items: CredentialSummary[] }>('/api/v1/credentials');
  return res?.items || [];
};

export const createCredential = async (data: {
  name: string;
  cred_kind: string;
  access_key_id: string;
  access_key_secret: string;
}): Promise<CredentialSummary> => {
  return apiFetch<CredentialSummary>('/api/v1/credentials', {
    method: 'POST',
    body: JSON.stringify(data),
  });
};

export const deleteCredential = async (id: string): Promise<void> => {
  await apiFetch(`/api/v1/credentials/${id}`, { method: 'DELETE' });
};

// Cloud Accounts API
export const fetchCloudAccounts = async (): Promise<CloudAccount[]> => {
  const res = await apiFetch<{ items: CloudAccount[] }>('/api/v1/cloud-accounts');
  return res?.items || [];
};

export const createCloudAccount = async (data: Partial<CloudAccount>): Promise<CloudAccount> => {
  return apiFetch<CloudAccount>('/api/v1/cloud-accounts', {
    method: 'POST',
    body: JSON.stringify(data),
  });
};

export const deleteCloudAccount = async (id: string): Promise<void> => {
  await apiFetch(`/api/v1/cloud-accounts/${id}`, { method: 'DELETE' });
};

export const triggerCloudSync = async (accountId: string): Promise<{ job_id: string }> => {
  return apiFetch<{ job_id: string }>(`/api/v1/cloud-accounts/${accountId}/sync`, {
    method: 'POST',
  });
};

export const getSyncStatus = async (jobId: string): Promise<SyncJobStatus> => {
  return apiFetch<SyncJobStatus>(`/api/v1/cloud-accounts/sync/${jobId}`);
};

export const discoverCloudResources = async (data: {
  credential_id: string;
  regions?: string[];
  account_site?: string;
}): Promise<DiscoveredResource[]> => {
  const res = await apiFetch<{ items: DiscoveredResource[] }>('/api/v1/cloud-accounts/discover', {
    method: 'POST',
    body: JSON.stringify(data),
  });
  return res?.items || [];
};

// Cloud Resources API
export const fetchCloudResources = async (params?: {
  account_id?: string;
  provider_code?: string;
  res_kind?: string;
  region?: string;
  status?: string;
}): Promise<CloudResource[]> => {
  const search = new URLSearchParams();
  if (params?.account_id) search.set('account_id', params.account_id);
  if (params?.provider_code) search.set('provider_code', params.provider_code);
  if (params?.res_kind) search.set('res_kind', params.res_kind);
  if (params?.region) search.set('region', params.region);
  if (params?.status) search.set('status', params.status);

  const query = search.toString();
  const url = `/api/v1/cloud-resources${query ? `?${query}` : ''}`;
  const res = await apiFetch<{ items: CloudResource[] }>(url);
  return res?.items || [];
};

export const actionCloudResource = async (
  resourceId: string,
  action: 'start' | 'stop'
): Promise<{ job_handle: string; status: string; message?: string }> => {
  return apiFetch<{ job_handle: string; status: string; message?: string }>(
    `/api/v1/cloud-resources/${resourceId}/action`,
    {
      method: 'POST',
      body: JSON.stringify({ action }),
    }
  );
};

// Cloud Metrics API (P2-10)
export interface CloudMetricSeriesResponse {
  cloud_account_id: string;
  res_ref: string;
  source: string; // "cloud_samples"
  from_ms: number;
  to_ms: number;
  step_ms: number;
  ts_ms: number[];
  series: Record<string, (number | null)[]>;
}

export const fetchCloudResourceMetrics = async (
  resourceId: string,
  params?: { span?: TimeSeriesSpan; from_ms?: number; to_ms?: number; metric_code?: string }
): Promise<CloudMetricSeriesResponse> => {
  const search = new URLSearchParams();
  const now = Date.now();
  if (params?.span) {
    let dur = 6 * 3600 * 1000;
    if (params.span === '3d') dur = 3 * 86400 * 1000;
    else if (params.span === '60d') dur = 60 * 86400 * 1000;
    else if (params.span === '1y') dur = 365 * 86400 * 1000;
    search.set('from_ms', String(now - dur));
    search.set('to_ms', String(now));
  } else {
    if (params?.from_ms) search.set('from_ms', String(params.from_ms));
    if (params?.to_ms) search.set('to_ms', String(params.to_ms));
  }
  if (params?.metric_code) search.set('metric_code', params.metric_code);
  const q = search.toString();
  return apiFetch<CloudMetricSeriesResponse>(`/api/v1/cloud-resources/${resourceId}/metrics${q ? `?${q}` : ''}`);
};

export const fetchCloudAccountMetrics = async (
  accountId: string,
  params?: { span?: TimeSeriesSpan; from_ms?: number; to_ms?: number; res_ref?: string; metric_code?: string; metric?: string }
): Promise<CloudMetricSeriesResponse> => {
  const search = new URLSearchParams();
  const now = Date.now();
  if (params?.span) {
    let dur = 6 * 3600 * 1000;
    if (params.span === '3d') dur = 3 * 86400 * 1000;
    else if (params.span === '60d') dur = 60 * 86400 * 1000;
    else if (params.span === '1y') dur = 365 * 86400 * 1000;
    search.set('from_ms', String(now - dur));
    search.set('to_ms', String(now));
  } else {
    if (params?.from_ms) search.set('from_ms', String(params.from_ms));
    if (params?.to_ms) search.set('to_ms', String(params.to_ms));
  }
  if (params?.res_ref) search.set('res_ref', params.res_ref);
  const code = params?.metric_code || params?.metric;
  if (code) search.set('metric_code', code);
  const q = search.toString();
  return apiFetch<CloudMetricSeriesResponse>(`/api/v1/cloud-metrics/accounts/${accountId}${q ? `?${q}` : ''}`);
};

export const triggerCloudMetricSync = async (): Promise<{ ok: boolean; report?: any }> => {
  return apiFetch<{ ok: boolean; report?: any }>('/api/v1/cloud-metrics/sync', {
    method: 'POST',
  });
};

