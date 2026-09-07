import { useQuery } from '@tanstack/react-query';
import { apiFetch, clearAuthCache } from './client';
import { queryClient } from './queryClient';
import { LoginRequest, UserMe } from './types';

export const CURRENT_USER_QUERY_KEY = ['currentUser'] as const;

export async function login(data: LoginRequest): Promise<{ user: UserMe }> {
  const res = await apiFetch<{ user: UserMe }>('/api/v1/login', {
    method: 'POST',
    body: JSON.stringify(data),
  });
  if (res?.user) {
    queryClient.setQueryData(CURRENT_USER_QUERY_KEY, res.user);
  }
  return res;
}

export async function logout(): Promise<void> {
  try {
    await apiFetch('/api/v1/logout', { method: 'POST' });
  } catch {
    // 忽略登出网络错误
  } finally {
    clearAuthCache();
    queryClient.clear();
  }
}

export async function getMe(): Promise<UserMe> {
  const res = await apiFetch<{ user: UserMe }>('/api/v1/me');
  return res.user;
}

export async function changePassword(data: {
  old_password: string;
  new_password: string;
}): Promise<{ ok: boolean }> {
  return await apiFetch<{ ok: boolean }>('/api/v1/me/password', {
    method: 'POST',
    body: JSON.stringify(data),
  });
}

/**
 * useCurrentUser Hook:
 * - 真实服务端登录态查询 (GET /api/v1/me)
 * - staleTime 给 5 分钟，避免路由切换重复打接口
 * - retry 设置为 false，401 或未登录时不进行冗余重试
 */
export function useCurrentUser() {
  return useQuery({
    queryKey: CURRENT_USER_QUERY_KEY,
    queryFn: getMe,
    staleTime: 5 * 60 * 1000,
    retry: false,
  });
}

export const authService = {
  login,
  logout,
  getMe,
  changePassword,
  isAuthenticated: () => {
    return !!queryClient.getQueryData<UserMe>(CURRENT_USER_QUERY_KEY);
  },
};
