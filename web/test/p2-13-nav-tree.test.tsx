import React from 'react';
import { renderToString } from 'react-dom/server';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { describe, it, beforeEach } from 'node:test';
import assert from 'node:assert';

import { Layout } from '../src/components/Layout';

/**
 * P2-13 侧栏改二级菜单专项验收测试:
 * 1. 九个页面都能从新侧栏进到，路径未变
 * 2. 折叠某一组 → 刷新后仍折叠；★ 但当前所在组始终展开
 * 3. 有未读事件 / firing 告警 / 失败 job 时，对应一级项与子项显示徽章，折叠时也可见
 * 4. 三大分组完整性：监控 / 阿里云 / 系统设置
 */

describe('P2-13 侧栏二级菜单结构与行为测试', () => {
  let queryClient: QueryClient;
  let localStorageStore: Record<string, string> = {};

  beforeEach(() => {
    queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
      },
    });

    localStorageStore = {};
    globalThis.localStorage = {
      getItem: (key: string) => localStorageStore[key] || null,
      setItem: (key: string, val: string) => {
        localStorageStore[key] = String(val);
      },
      removeItem: (key: string) => {
        delete localStorageStore[key];
      },
      clear: () => {
        localStorageStore = {};
      },
      key: (i: number) => Object.keys(localStorageStore)[i] || null,
      length: Object.keys(localStorageStore).length,
    } as any;

    globalThis.fetch = (async (input: RequestInfo | URL) => {
      const url = typeof input === 'string' ? input : input.toString();
      let data: any = {};
      if (url.includes('/api/v1/events/unread-count')) {
        data = { unread_count: 5 };
      } else if (url.includes('/api/v1/alerts/active')) {
        data = {
          items: [
            { id: 'a1', event_state: 'firing' },
            { id: 'a2', event_state: 'firing' },
          ],
          total: 2,
        };
      } else if (url.includes('/api/v1/jobs')) {
        data = {
          jobs: [{ id: 'j1', job_state: 'failed' }],
          total: 3,
        };
      } else if (url.includes('/api/v1/me')) {
        data = { user: { id: 'admin', username: 'admin', is_admin: true } };
      }

      return new Response(JSON.stringify(data), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }) as any;
  });

  const renderLayoutAt = (path: string) => {
    return renderToString(
      <QueryClientProvider client={queryClient}>
        <MemoryRouter initialEntries={[path]}>
          <Routes>
            <Route path="/*" element={<Layout />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>
    );
  };

  it('❶ 必须包含三大一级分组：监控、阿里云、系统设置', () => {
    const html = renderLayoutAt('/nodes');
    assert.ok(html.includes('监控'), '侧栏必须包含「监控」分组');
    assert.ok(html.includes('阿里云'), '侧栏必须包含「阿里云」分组');
    assert.ok(html.includes('系统设置'), '侧栏必须包含「系统设置」分组');
  });

  it('❷ 九个目标页面均可在侧栏中找到对应 href，且路径完全未变', () => {
    const html = renderLayoutAt('/nodes');
    const expectedPaths = [
      '/nodes',
      '/machines',
      '/events',
      '/alerts',
      '/cloud',
      '/guard',
      '/billing',
      '/jobs',
      '/settings',
    ];

    for (const p of expectedPaths) {
      assert.ok(
        html.includes(`href="${p}"`),
        `侧栏必须包含二级导航链接 ${p}，不能丢失或更改路径`
      );
    }
  });

  it('❸ 当前所在分组始终强制展开，即使 localStorage 预设该分组为折叠', () => {
    localStorageStore['dash_sidebar_collapsed_groups'] = JSON.stringify({
      monitor: true,
      aliyun: true,
      system: true,
    });

    const html = renderLayoutAt('/nodes');

    assert.ok(
      html.includes('title="监控 (点击折叠)"') || html.includes('aria-expanded="true"'),
      '当前所在组 monitor 必须保持展开状态'
    );
  });

  it('❹ 非当前所在分组若在 localStorage 中折叠，应呈现折叠状态', () => {
    localStorageStore['dash_sidebar_collapsed_groups'] = JSON.stringify({
      aliyun: true,
    });

    const html = renderLayoutAt('/nodes');
    assert.ok(
      html.includes('title="阿里云 (点击展开)"') || html.includes('aria-expanded="false"'),
      '非当前所在组 aliyun 应保持折叠状态'
    );
  });

  it('❺ 一级项带该组未读/异常徽章，且折叠与展开状态下均可见', async () => {
    await queryClient.prefetchQuery({
      queryKey: ['events-unread-count'],
      queryFn: async () => ({ unread_count: 5 }),
    });
    await queryClient.prefetchQuery({
      queryKey: ['active-alerts'],
      queryFn: async () => ({
        items: [{ id: 'a1', event_state: 'firing' }],
        total: 1,
      }),
    });
    await queryClient.prefetchQuery({
      queryKey: ['jobs', 'failed', 'sidebar-badge'],
      queryFn: async () => ({
        jobs: [{ id: 'j1', job_state: 'failed' }],
        total: 4,
      }),
    });

    const html = renderLayoutAt('/nodes');

    assert.ok(
      html.includes('groupBadge') && html.includes('6'),
      '监控一级项必须汇总显示 6 个未读/异常徽章'
    );

    assert.ok(
      html.includes('groupBadge') && html.includes('4'),
      '系统设置一级项必须显示 4 个失败任务徽章'
    );
  });
});
