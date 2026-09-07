import React from 'react';
import { renderToString } from 'react-dom/server';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { describe, it, beforeEach } from 'node:test';
import assert from 'node:assert';
import fs from 'node:fs';
import path from 'node:path';

import {
  apiFetch,
  ApiException,
  handleUnauthorized,
  setUnauthorizedNavigator,
  resetUnauthorizedStateForTesting,
  clearAuthCache,
  login,
  logout,
  getMe,
  authService,
  queryClient,
  CURRENT_USER_QUERY_KEY,
} from '../src/api';
import { ProtectedRoute, AppRoutes } from '../src/App';

describe('P1-24 前端真实登录态与 401 处理测试', () => {
  beforeEach(() => {
    queryClient.clear();
    setUnauthorizedNavigator(null);
    resetUnauthorizedStateForTesting();
  });

  describe('❶ 全仓库 dash_mock_auth 清理验证', () => {
    it('web/src/ 下必须 0 处出现 dash_mock_auth', () => {
      const srcDir = path.resolve(__dirname, '../src');
      function checkDir(dir: string) {
        const entries = fs.readdirSync(dir, { withFileTypes: true });
        for (const entry of entries) {
          const fullPath = path.join(dir, entry.name);
          if (entry.isDirectory()) {
            checkDir(fullPath);
          } else if (/\.(ts|tsx|js|jsx|json|css|html)$/.test(entry.name)) {
            const content = fs.readFileSync(fullPath, 'utf8');
            assert(
              !content.includes('dash_mock_auth'),
              `文件 ${fullPath} 依然包含 dash_mock_auth`
            );
          }
        }
      }
      checkDir(srcDir);
    });
  });

  describe('❷ 401 统一处理与防抖 (client.ts)', () => {
    it('非登录接口 401 触发跳转并带上 ?from=<当前路径>，同时清除用户缓存', () => {
      // 模拟当前已缓存用户信息
      queryClient.setQueryData(CURRENT_USER_QUERY_KEY, { id: 'u1', username: 'admin' });
      assert(queryClient.getQueryData(CURRENT_USER_QUERY_KEY) != null);

      let navigatedTarget = '';
      setUnauthorizedNavigator((url: string) => {
        navigatedTarget = url;
      });

      // 模拟在 /machines 路径下收到 401
      const originalWindow = globalThis.window;
      let eventFired = false;
      (globalThis as any).window = {
        location: {
          pathname: '/machines',
          search: '?view=cards',
          hash: '#top',
          href: '',
        },
        dispatchEvent: (e: any) => {
          if (e?.type === 'dash:unauthorized') {
            eventFired = true;
          }
        },
      };

      try {
        handleUnauthorized('/api/v1/nodes');

        // 验证用户缓存已被清除
        assert.strictEqual(queryClient.getQueryData(CURRENT_USER_QUERY_KEY), undefined);
        // 验证 dash:unauthorized 事件已触发
        assert.strictEqual(eventFired, true);
        // 验证已跳转至 /login 且带上当前路径
        assert.strictEqual(navigatedTarget, '/login?from=%2Fmachines%3Fview%3Dcards%23top');
      } finally {
        globalThis.window = originalWindow;
      }
    });

    it('/api/v1/login 自身的 401 绝不触发跳转（避免循环）', () => {
      let navigatedTarget = '';
      setUnauthorizedNavigator((url: string) => {
        navigatedTarget = url;
      });

      const originalWindow = globalThis.window;
      (globalThis as any).window = {
        location: {
          pathname: '/login',
          search: '',
          hash: '',
          href: '',
        },
        dispatchEvent: () => {},
      };

      try {
        handleUnauthorized('/api/v1/login');
        assert.strictEqual(navigatedTarget, '', '登录端点 401 不应触发跳转');
      } finally {
        globalThis.window = originalWindow;
      }
    });

    it('并发多个 401 请求时进行防抖，只跳转一次', () => {
      let navCount = 0;
      setUnauthorizedNavigator(() => {
        navCount++;
      });

      const originalWindow = globalThis.window;
      (globalThis as any).window = {
        location: {
          pathname: '/machines',
          search: '',
          hash: '',
          href: '',
        },
        dispatchEvent: () => {},
      };

      try {
        // 模拟并发 3 个请求同时 401
        handleUnauthorized('/api/v1/nodes');
        handleUnauthorized('/api/v1/groups');
        handleUnauthorized('/api/v1/tags');

        assert.strictEqual(navCount, 1, '并发 401 必须防抖，只跳转一次');
      } finally {
        globalThis.window = originalWindow;
      }
    });
  });

  describe('❸ 真实认证服务 (auth.ts)', () => {
    it('authService.isAuthenticated 依据 React Query 真实缓存判断', () => {
      queryClient.clear();
      assert.strictEqual(authService.isAuthenticated(), false);

      queryClient.setQueryData(CURRENT_USER_QUERY_KEY, {
        id: '01ADMIN',
        username: 'admin',
        is_admin: true,
      });
      assert.strictEqual(authService.isAuthenticated(), true);
    });

    it('logout 会清空 React Query 缓存', async () => {
      queryClient.setQueryData(CURRENT_USER_QUERY_KEY, { id: '01ADMIN', username: 'admin' });
      queryClient.setQueryData(['nodes-inventory'], [{ id: 'n1', name: 'vps' }]);

      const originalFetch = globalThis.fetch;
      globalThis.fetch = async () => new Response(JSON.stringify({ ok: true }), { status: 200 });

      try {
        await logout();
        assert.strictEqual(queryClient.getQueryData(CURRENT_USER_QUERY_KEY), undefined);
        assert.strictEqual(queryClient.getQueryData(['nodes-inventory']), undefined);
      } finally {
        globalThis.fetch = originalFetch;
      }
    });

    it('getMe 成功时返回当前用户，失败时不回退到 mockCurrentUser', async () => {
      const originalFetch = globalThis.fetch;

      // 1. 模拟 200 成功
      globalThis.fetch = async () =>
        new Response(
          JSON.stringify({
            user: { id: 'u_real', username: 'real_admin', is_admin: true },
          }),
          { status: 200, headers: { 'Content-Type': 'application/json' } }
        );

      const user = await getMe();
      assert.strictEqual(user.id, 'u_real');
      assert.strictEqual(user.username, 'real_admin');

      // 2. 模拟 401 失败：必须抛出异常，绝不返回 mock
      globalThis.fetch = async () =>
        new Response(
          JSON.stringify({
            error: { code: 'unauthorized', message: '未登录或会话已过期' },
          }),
          { status: 401, headers: { 'Content-Type': 'application/json' } }
        );

      await assert.rejects(async () => {
        await getMe();
      }, (err: any) => {
        assert(err instanceof ApiException);
        assert.strictEqual(err.status, 401);
        assert.strictEqual(err.message, '未登录或会话已过期');
        return true;
      });

      globalThis.fetch = originalFetch;
    });

    it('login 密码错误时抛出后端实际 message，限流时正确区分 429', async () => {
      const originalFetch = globalThis.fetch;

      // 1. 密码错误 401
      globalThis.fetch = async () =>
        new Response(
          JSON.stringify({
            error: { code: 'bad_credentials', message: '用户名或密码错误' },
          }),
          { status: 401, headers: { 'Content-Type': 'application/json' } }
        );

      await assert.rejects(async () => {
        await login({ username: 'admin', password: 'wrong' });
      }, (err: any) => {
        assert(err instanceof ApiException);
        assert.strictEqual(err.status, 401);
        assert.strictEqual(err.message, '用户名或密码错误');
        return true;
      });

      // 2. 限流 429
      globalThis.fetch = async () =>
        new Response(
          JSON.stringify({
            error: { code: 'rate_limited', message: '登录失败次数过多，已被锁定 15 分钟' },
          }),
          { status: 429, headers: { 'Content-Type': 'application/json' } }
        );

      await assert.rejects(async () => {
        await login({ username: 'admin', password: 'wrong' });
      }, (err: any) => {
        assert(err instanceof ApiException);
        assert.strictEqual(err.status, 429);
        assert.strictEqual(err.code, 'rate_limited');
        return true;
      });

      globalThis.fetch = originalFetch;
    });
  });

  describe('❹ ProtectedRoute 真实登录态渲染保护', () => {
    it('已登录状态下正常渲染受保护子内容', () => {
      const customClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 0 } },
      });
      customClient.setQueryData(CURRENT_USER_QUERY_KEY, {
        id: 'u1',
        username: 'admin',
        is_admin: true,
      });

      const html = renderToString(
        <QueryClientProvider client={customClient}>
          <MemoryRouter initialEntries={['/']}>
            <ProtectedRoute>
              <div>已成功进入受保护区</div>
            </ProtectedRoute>
          </MemoryRouter>
        </QueryClientProvider>
      );
      assert(html.includes('已成功进入受保护区'), '已登录状态应当正常渲染受保护子内容');
    });

    it('加载中状态下渲染加载提示', () => {
      const customClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 0 } },
      });
      // 未设置数据，且让 query 函数挂起返回永久 pending
      const html = renderToString(
        <QueryClientProvider client={customClient}>
          <MemoryRouter initialEntries={['/']}>
            <ProtectedRoute>
              <div>受保护内容</div>
            </ProtectedRoute>
          </MemoryRouter>
        </QueryClientProvider>
      );
      // 服务端初次渲染尚未获得数据，处于 isLoading 状态
      assert(html.includes('正在验证登录状态'), '应当显示加载态提示');
      assert(!html.includes('受保护内容'), '未完成验证前绝不渲染受保护内容');
    });
  });
});
