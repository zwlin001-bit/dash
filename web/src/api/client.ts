import { ApiError, ApiErrorResponse } from './types';

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
    window.dispatchEvent(new CustomEvent('dash:unauthorized'));
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
