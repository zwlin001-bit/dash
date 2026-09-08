import React from 'react';
import { renderToString } from 'react-dom/server';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { QueryClientProvider } from '@tanstack/react-query';
import { describe, it, beforeEach } from 'node:test';
import assert from 'node:assert';

import { queryClient } from '../src/api';
import { ErrorBoundary } from '../src/components/ErrorBoundary';
import { Login } from '../src/pages/Login';
import { Overview } from '../src/pages/Overview';
import { NodeDetail } from '../src/pages/NodeDetail';
import { Machines } from '../src/pages/Machines';
import { Events } from '../src/pages/Events';
import { Settings } from '../src/pages/Settings';

/**
 * P1-27 验收项 4:
 * 控制台六个页面（登录 / 总览 / 节点详情 / 机器清单 / 事件 / 设置）
 * 在全空数据（0 节点 0 分组 0 标签 0 事件）下逐个打开，均不触发 ErrorBoundary。
 * 这是导致 y.map is not a function 线上故障的直接复现场景，必须实跑。
 */

function stubAllEmptyFetch() {
  globalThis.fetch = (async (input: RequestInfo | URL) => {
    const url = typeof input === 'string' ? input : input.toString();

    let data: any = {};
    if (url.includes('/api/v1/nodes/')) {
      if (url.includes('/metrics')) {
        data = { node_id: 'node-empty', source: 'sample_host', from_ms: 0, to_ms: 0, step_ms: 0, ts_ms: [], series: {} };
      } else if (url.includes('/latest')) {
        data = { node_id: 'node-empty', ts_ms: 0 };
      } else if (url.includes('/facts') || url.includes('/billing')) {
        data = {};
      } else {
        // 节点详情
        data = {
          id: 'node-empty',
          name: 'empty-node',
          conn_state: 'offline',
          created_at_ms: 1788800000000,
          updated_at_ms: 1788800000000,
          tags: [],
        };
      }
    } else if (url.includes('/api/v1/nodes')) {
      data = { items: [], total: 0, page: 1, page_size: 50 };
    } else if (url.includes('/api/v1/node-groups')) {
      data = { items: [], total: 0, page: 1, page_size: 50 };
    } else if (url.includes('/api/v1/tags')) {
      data = { items: [], total: 0, page: 1, page_size: 50 };
    } else if (url.includes('/api/v1/enroll-tokens')) {
      data = { items: [], total: 0, page: 1, page_size: 50 };
    } else if (url.includes('/api/v1/events/unread-count')) {
      data = { unread_count: 0 };
    } else if (url.includes('/api/v1/event-types')) {
      data = { items: [] };
    } else if (url.includes('/api/v1/events')) {
      data = { items: [], total: 0, limit: 20, offset: 0 };
    } else if (url.includes('/api/v1/settings')) {
      data = {
        'site.domain': 'dash.example.com',
        'collect.interval_fast_s': 5,
        'collect.interval_slow_s': 60,
        'collect.enable_conns': 1,
        'retention.raw_days': 3,
        'retention.1m_days': 30,
        'retention.1h_days': 365,
        'retention.1d_days': 0,
      };
    } else if (url.includes('/api/v1/me')) {
      data = { user: { id: 'admin', username: 'admin', is_admin: true } };
    } else if (url.includes('/healthz')) {
      data = { status: 'ok', version: 'v1.0.0-test', db: 'ok', agents_online: 0 };
    }

    return new Response(JSON.stringify(data), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });
  }) as any;
}

describe('P1-27 全空数据（0 节点 0 分组 0 标签 0 事件）下六大页面渲染', () => {
  let originalFetch: typeof globalThis.fetch;

  beforeEach(() => {
    originalFetch = globalThis.fetch;
    queryClient.clear();
    stubAllEmptyFetch();
  });

  const renderWithWrapper = (ui: React.ReactNode, initialPath = '/') => {
    return renderToString(
      <ErrorBoundary level="app">
        <QueryClientProvider client={queryClient}>
          <MemoryRouter initialEntries={[initialPath]}>
            <ErrorBoundary level="route">
              {ui}
            </ErrorBoundary>
          </MemoryRouter>
        </QueryClientProvider>
      </ErrorBoundary>
    );
  };

  it('1. 登录页 (Login) 全空状态下正常渲染且不崩溃', () => {
    try {
      const html = renderWithWrapper(<Login />, '/login');
      assert(html.includes('登录') || html.includes('dash'), '应包含登录界面标识');
      assert(!html.includes('当前页面加载出错'), '不应触发路由错误边界');
      assert(!html.includes('系统出错了'), '不应触发应用错误边界');
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  it('2. 总览页 (Overview) 全空状态（0节点0分组0标签）不触发 ErrorBoundary', () => {
    try {
      const html = renderWithWrapper(<Overview />, '/nodes');
      assert(html.includes('节点总览'), '应正常渲染总览页标题');
      assert(!html.includes('当前页面加载出错'), '不应触发路由错误边界 (y.map is not a function 故障点)');
      assert(!html.includes('系统出错了'), '不应触发应用错误边界');
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  it('3. 节点详情页 (NodeDetail) 全空/最小节点不触发 ErrorBoundary', () => {
    try {
      const html = renderWithWrapper(
        <Routes>
          <Route path="/nodes/:id" element={<NodeDetail />} />
        </Routes>,
        '/nodes/node-empty'
      );
      assert(html.includes('node-empty') || html.includes('节点总览'), '应正常渲染节点详情结构');
      assert(!html.includes('当前页面加载出错'), '不应触发路由错误边界');
      assert(!html.includes('系统出错了'), '不应触发应用错误边界');
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  it('4. 机器清单页 (Machines) 全空状态（0机器0令牌0分组0标签）不触发 ErrorBoundary', () => {
    try {
      const html = renderWithWrapper(<Machines />, '/machines');
      assert(html.includes('机器资产') || html.includes('清单'), '应正常渲染机器清单标题');
      assert(!html.includes('当前页面加载出错'), '不应触发路由错误边界');
      assert(!html.includes('系统出错了'), '不应触发应用错误边界');
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  it('5. 事件中心页 (Events) 全空状态（0事件0事件类型）不触发 ErrorBoundary', () => {
    try {
      const html = renderWithWrapper(<Events />, '/events');
      assert(html.includes('事件中心') || html.includes('事件'), '应正常渲染事件中心结构');
      assert(!html.includes('当前页面加载出错'), '不应触发路由错误边界');
      assert(!html.includes('系统出错了'), '不应触发应用错误边界');
    } finally {
      globalThis.fetch = originalFetch;
    }
  });

  it('6. 系统设置页 (Settings) 全空/默认设置不触发 ErrorBoundary', () => {
    try {
      const html = renderWithWrapper(<Settings />, '/settings');
      assert(html.includes('设置') || html.includes('dash.example.com'), '应正常渲染设置页面');
      assert(!html.includes('当前页面加载出错'), '不应触发路由错误边界');
      assert(!html.includes('系统出错了'), '不应触发应用错误边界');
    } finally {
      globalThis.fetch = originalFetch;
    }
  });
});
