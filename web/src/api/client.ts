import { ApiError, ApiErrorResponse } from './types';
import { queryClient } from './queryClient';

export class ApiException extends Error {
  code: string;
  detail?: any;
  status: number;

  constructor(apiErr: ApiError, status: number) {
    super(apiErr.message || 'API request failed');
    this.name = 'ApiException';
    this.code = apiErr.code || 'unknown_error';
    this.detail = apiErr.detail;
    this.status = status;
  }
}

let isRedirecting = false;
let customNavigator: ((url: string) => void) | null = null;

export function setUnauthorizedNavigator(navigator: ((url: string) => void) | null) {
  customNavigator = navigator;
}

export function resetUnauthorizedStateForTesting() {
  isRedirecting = false;
}

export function clearAuthCache() {
  queryClient.removeQueries({ queryKey: ['currentUser'] });
}

export function handleUnauthorized(endpoint: string) {
  // 排除登录端点自身的 401（避免输错密码时跳转循环）
  if (endpoint.includes('/api/v1/login')) {
    return;
  }

  // 清除本地用户缓存
  clearAuthCache();

  if (typeof window !== 'undefined') {
    window.dispatchEvent(new CustomEvent('dash:unauthorized'));
  }

  if (typeof window === 'undefined') {
    return;
  }

  // 已经在登录页，不需要再次跳转
  if (window.location.pathname === '/login') {
    return;
  }

  // 跳转防抖：并发多个请求同时 401 时只跳一次
  if (isRedirecting) {
    return;
  }
  isRedirecting = true;

  const currentPath = window.location.pathname + window.location.search + window.location.hash;
  const target = `/login?from=${encodeURIComponent(currentPath)}`;

  if (customNavigator) {
    customNavigator(target);
  } else {
    window.location.href = target;
  }

  setTimeout(() => {
    isRedirecting = false;
  }, 2000);
}

export async function apiFetch<T>(endpoint: string, options: RequestInit = {}): Promise<T> {
  const url = endpoint.startsWith('http') ? endpoint : endpoint;
  const headers = new Headers(options.headers || {});
  
  if (!headers.has('Accept')) {
    headers.set('Accept', 'application/json');
  }
  if (options.body && typeof options.body === 'string' && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json');
  }

  const config: RequestInit = {
    ...options,
    headers,
    credentials: options.credentials || 'same-origin',
  };

  let response: Response;
  try {
    response = await fetch(url, config);
  } catch (err: any) {
    if (err?.name === 'AbortError') {
      throw err;
    }
    throw new ApiException(
      {
        code: 'network_error',
        message: err?.message || '网络请求失败，请检查网络连接',
      },
      0
    );
  }

  if (response.status === 401) {
    handleUnauthorized(endpoint);
  }

  if (!response.ok) {
    let errData: ApiError = {
      code: `http_${response.status}`,
      message: response.statusText || '请求异常',
    };
    try {
      const parsed: ApiErrorResponse = await response.json();
      if (parsed?.error) {
        errData = parsed.error;
      }
    } catch {
      // 非 JSON 错误回退使用默认状态文本
    }
    throw new ApiException(errData, response.status);
  }

  if (response.status === 204) {
    return {} as T;
  }

  return response.json();
}
