import { apiFetch } from './client';
import { SystemSettings } from './types';

export async function getSettings(): Promise<SystemSettings> {
  return await apiFetch<SystemSettings>('/api/v1/settings');
}

export async function updateSettings(params: Partial<SystemSettings>): Promise<SystemSettings> {
  return await apiFetch<SystemSettings>('/api/v1/settings', {
    method: 'PATCH',
    body: JSON.stringify(params),
  });
}
