import { apiFetch } from './client';
import { LoginRequest, UserMe } from './types';
import { mockCurrentUser } from '../mock/mockData';

export async function login(data: LoginRequest): Promise<{ status: string }> {
  try {
    return await apiFetch<{ status: string }>('/api/v1/login', {
      method: 'POST',
      body: JSON.stringify(data),
    });
  } catch (err) {
    // 脚手架阶段若后端接口未就绪，使用 Mock 验证登录
    if (data.username === 'admin' && data.password) {
      localStorage.setItem('dash_mock_auth', '1');
      return { status: 'ok' };
    }
    throw err;
  }
}

export async function logout(): Promise<void> {
  localStorage.removeItem('dash_mock_auth');
  try {
    await apiFetch('/api/v1/logout', { method: 'POST' });
  } catch {
    // 忽略登出网络错误
  }
}

export async function getMe(): Promise<UserMe> {
  try {
    return await apiFetch<UserMe>('/api/v1/me');
  } catch {
    if (localStorage.getItem('dash_mock_auth') === '1') {
      return mockCurrentUser;
    }
    return mockCurrentUser; // 默认提供 mock 用户信息供脚手架路由使用
  }
}

export async function changePassword(data: { old_password: string; new_password: string }): Promise<{ ok: boolean }> {
  return await apiFetch<{ ok: boolean }>('/api/v1/me/password', {
    method: 'POST',
    body: JSON.stringify(data),
  });
}

export const authService = {
  login,
  logout,
  getMe,
  changePassword,
  isAuthenticated: () => {
    return localStorage.getItem('dash_mock_auth') === '1';
  },
};


