import React from 'react';
import { renderToString } from 'react-dom/server';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { describe, it, after } from 'node:test';
import assert from 'node:assert';
import './auth.test';
import './overview_real.test';
import './api_envelope.test';
import './contract.test';
import './empty_data_all_pages.test';
import './p2-13-nav-tree.test';

import { ErrorBoundary } from '../src/components/ErrorBoundary';
import { Overview } from '../src/pages/Overview';
import { Machines } from '../src/pages/Machines';
import { NodeDetail } from '../src/pages/NodeDetail';
import {
  fmt,
  formatBytes,
  formatBps,
  formatTimeAgo,
  formatDateTime,
  formatUptime,
} from '../src/utils';
import { NodeItem } from '../src/api/types';

describe('P1-23 前端空值健壮性与错误边界测试', () => {
  describe('❶ 数值格式化 helper (fmt) 健壮性', () => {
    it('undefined / null / NaN 一律返回 --', () => {
      assert.strictEqual(fmt(undefined), '--');
      assert.strictEqual(fmt(null), '--');
      assert.strictEqual(fmt(NaN), '--');
      assert.strictEqual(fmt(''), '--');

      assert.strictEqual(fmt(undefined, '%'), '--');
      assert.strictEqual(fmt(null, '%'), '--');
      assert.strictEqual(fmt(NaN, '%'), '--');

      assert.strictEqual(fmt(undefined, 'bytes'), '--');
      assert.strictEqual(fmt(null, 'bytes'), '--');

      assert.strictEqual(fmt(undefined, 'bps'), '--');
      assert.strictEqual(fmt(null, 'bps'), '--');
    });

    it('有效数值正常格式化，0 值不被掩盖为缺失', () => {
      // 核心要求：0% 和「没数据」在监控界面上是两回事，0 应当正常显示为 0.0%
      assert.strictEqual(fmt(0, '%'), '0.0%');
      assert.strictEqual(fmt(3.5, '%'), '3.5%');
      assert.strictEqual(fmt(100, '%'), '100.0%');
      assert.strictEqual(fmt(42), '42');
      assert.strictEqual(fmt(4, ' 核'), '4 核');
    });

    it('formatBytes 正确处理 null/undefined 与有效字节', () => {
      assert.strictEqual(formatBytes(undefined), '--');
      assert.strictEqual(formatBytes(null), '--');
      assert.strictEqual(formatBytes(0), '0 B');
      assert.strictEqual(formatBytes(1024), '1.0 KB');
      assert.strictEqual(formatBytes(1048576), '1.0 MB');
    });

    it('formatBps 正确处理 null/undefined 与网络速率', () => {
      assert.strictEqual(formatBps(undefined), '--');
      assert.strictEqual(formatBps(null), '--');
      assert.strictEqual(formatBps(0), '0 B/s');
      assert.strictEqual(formatBps(1024), '1 KB/s');
      assert.strictEqual(formatBps(1048576), '1.0 MB/s');
    });

    it('formatTimeAgo / formatDateTime / formatUptime 健壮性', () => {
      assert.strictEqual(formatTimeAgo(undefined), '从未上线');
      assert.strictEqual(formatTimeAgo(null), '从未上线');
      assert.strictEqual(formatDateTime(undefined), '--');
      assert.strictEqual(formatDateTime(null), '--');
      assert.strictEqual(formatUptime(undefined), '--');
      assert.strictEqual(formatUptime(null), '--');
      assert.strictEqual(formatUptime(0), '--');
      assert.strictEqual(formatUptime(3660), '1 小时 1 分钟');
    });
  });

  describe('❷ 最小字段节点渲染与快照测试 (Machines, Overview, NodeDetail)', () => {
    // 约束：{"items":[{"id":"x","name":"n","conn_state":"offline","tags":[]}]} 最小节点数据
    const minimalNode: NodeItem = {
      id: 'x',
      name: 'n',
      conn_state: 'offline',
      tags: [],
    };

    it('渲染 /machines 页面不抛异常，正常显示最小节点且未分组、未配置计费显示 --', () => {
      const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 0 } },
      });
      queryClient.setQueryData(['nodes-inventory'], [minimalNode]);
      queryClient.setQueryData(['groups'], []);
      queryClient.setQueryData(['tags'], []);
      queryClient.setQueryData(['enroll-tokens'], {
        items: [],
        total: 0,
        page: 1,
        page_size: 100,
      });

      let html = '';
      assert.doesNotThrow(() => {
        html = renderToString(
          <QueryClientProvider client={queryClient}>
            <MemoryRouter initialEntries={['/machines']}>
              <Machines />
            </MemoryRouter>
          </QueryClientProvider>
        );
      });

      assert(html.includes('n'), '页面应渲染出机器名称 n');
      assert(html.includes('offline'), '页面应渲染出 offline 状态');
      assert(html.includes('未分组'), '无 group 字段时应正常回退至“未分组”而非抛异常');
      assert(html.includes('--'), '无 billing 字段时计费与到期应回退至 --');
    });

    it('渲染 / 节点总览 (Overview) 页面不抛异常，缺失指标显示 --', () => {
      const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 0 } },
      });
      queryClient.setQueryData(['nodes'], [minimalNode]);
      queryClient.setQueryData(['node-groups'], []);
      queryClient.setQueryData(['tags'], []);
      queryClient.setQueryData(['healthz'], { status: 'ok', db: 'ok' });

      let html = '';
      assert.doesNotThrow(() => {
        html = renderToString(
          <QueryClientProvider client={queryClient}>
            <MemoryRouter initialEntries={['/nodes']}>
              <Overview />
            </MemoryRouter>
          </QueryClientProvider>
        );
      });

      assert(html.includes('n'), '总览页应包含节点名称');
      assert(html.includes('offline'), '总览页应包含 offline');
      assert(html.includes('--'), '缺失指标项应渲染 -- 而非 0% 或 NaN');
    });

    it('渲染 /nodes/:id (NodeDetail) 页面面对空 facts / billing / latest 不抛异常', () => {
      const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false, gcTime: 0 } },
      });
      queryClient.setQueryData(['node', 'x'], minimalNode);

      let html = '';
      assert.doesNotThrow(() => {
        html = renderToString(
          <QueryClientProvider client={queryClient}>
            <MemoryRouter initialEntries={['/nodes/x']}>
              <NodeDetail />
            </MemoryRouter>
          </QueryClientProvider>
        );
      });

      assert(html.includes('n'), '详情页应包含节点名');
      assert(html.includes('--'), '无 facts/billing 详情项应显示 --');
    });
  });

  describe('❸ ErrorBoundary 错误边界功能', () => {
    it('getDerivedStateFromError 正确捕获并设置错误状态', () => {
      const err = new Error('test crash');
      const state = ErrorBoundary.getDerivedStateFromError(err);
      assert.strictEqual(state.hasError, true);
      assert.strictEqual(state.error, err);
    });

    it('路由级 ErrorBoundary 发生错误时渲染错误卡片与摘要，不白屏', () => {
      const boundary = new ErrorBoundary({ level: 'route', children: <div>原内容</div> });
      boundary.state = {
        hasError: true,
        error: new Error('boom in route'),
        errorInfo: null,
      };

      const originalConsoleError = console.error;
      let logged = false;
      console.error = () => {
        logged = true;
      };

      try {
        boundary.componentDidCatch(new Error('boom in route'), { componentStack: 'stack trace' });
        assert(logged, 'componentDidCatch 应当调用 console.error 输出日志');

        const rendered = boundary.render();
        const html = renderToString(rendered as React.ReactElement);
        assert(html.includes('当前页面加载出错'), '应包含“当前页面加载出错”标题');
        assert(html.includes('boom in route'), '应包含错误消息');
        assert(html.includes('重试'), '应包含重试按钮');
        assert(html.includes('返回节点总览'), '应包含返回总览链接');
      } finally {
        console.error = originalConsoleError;
      }
    });

    it('应用级 ErrorBoundary 发生错误时渲染系统出错了与刷新按钮', () => {
      const boundary = new ErrorBoundary({ level: 'app', children: <div>原内容</div> });
      boundary.state = {
        hasError: true,
        error: new Error('fatal root crash'),
        errorInfo: null,
      };

      const rendered = boundary.render();
      const html = renderToString(rendered as React.ReactElement);
      assert(html.includes('系统出错了'), '应包含“系统出错了”标题');
      assert(html.includes('fatal root crash'), '应包含致命错误消息');
      assert(html.includes('刷新页面'), '应包含刷新页面按钮');
      assert(html.includes('重试'), '应包含重试按钮');
    });

    it('当 resetKey 发生变化时自动清除错误状态', () => {
      const boundary = new ErrorBoundary({
        level: 'route',
        resetKey: '/machines',
        children: <div>正常工作</div>,
      });
      boundary.state = {
        hasError: true,
        error: new Error('boom'),
        errorInfo: null,
      };

      // 模拟路由发生改变：resetKey 从 /machines 变为 /nodes
      boundary.componentDidUpdate({
        level: 'route',
        resetKey: '/machines',
        children: <div>正常工作</div>,
      });
      // resetKey 没变时保持错误
      assert.strictEqual(boundary.state.hasError, true);

      // resetKey 改变时重置错误
      (boundary as any).props = {
        level: 'route',
        resetKey: '/nodes',
        children: <div>正常工作</div>,
      };
      boundary.componentDidUpdate({
        level: 'route',
        resetKey: '/machines',
        children: <div>正常工作</div>,
      });
      assert.strictEqual(boundary.state.hasError, false);
      assert.strictEqual(boundary.state.error, null);
    });

    it('无异常时正常渲染子组件内容', () => {
      const boundary = new ErrorBoundary({ level: 'route', children: <div>正常工作</div> });
      const html = renderToString(boundary.render() as React.ReactElement);
      assert(html.includes('正常工作'));
    });
  });

  after(() => {
    // 确保测试跑完后立即退出，不等待 react-query 默认的 5 分钟 gcTime 定时器
    setTimeout(() => {
      if (typeof process !== 'undefined') {
        process.exit(0);
      }
    }, 50);
  });
});
