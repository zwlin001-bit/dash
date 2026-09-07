import React from 'react';
import { renderToString } from 'react-dom/server';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { describe, it, beforeEach } from 'node:test';
import assert from 'node:assert';
import fs from 'node:fs';
import path from 'node:path';

import {
  getNodes,
  getNodesPaged,
  getNode,
  getMetrics,
  subscribeMetricsStream,
  SSEConnectionStatus,
  MetricStreamEvent,
  NodeStateStreamEvent,
  NodeItem,
} from '../src/api';
import { Overview } from '../src/pages/Overview';
import { DevNoAuthBanner } from '../src/App';

describe('P1-26 节点总览真实数据与开发模式测试', () => {
  describe('❶ 全代码库 mock / 假数据 清理验证', () => {
    it('web/src/ 下必须 0 处出现 mock 或 假数据（大小写均检查）', () => {
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
              !content.toLowerCase().includes('mock'),
              `文件 ${fullPath} 包含 mock 关键字残留`
            );
            assert(
              !content.includes('假数据'),
              `文件 ${fullPath} 包含 假数据 关键字残留`
            );
          }
        }
      }
      checkDir(srcDir);
    });

    it('web/src/mock 目录已被移除', () => {
      const mockDir = path.resolve(__dirname, '../src/mock');
      assert(!fs.existsSync(mockDir), 'web/src/mock 目录依然存在');
    });
  });

  describe('❷ API 失败时不吞异常不回退假数据', () => {
    const originalFetch = globalThis.fetch;

    beforeEach(() => {
      globalThis.fetch = originalFetch;
    });

    it('getNodes / getNodesPaged 网络失败时直接抛出异常', async () => {
      globalThis.fetch = async () => {
        throw new Error('network down');
      };

      await assert.rejects(async () => {
        await getNodesPaged();
      }, /network down/);

      await assert.rejects(async () => {
        await getNodes();
      }, /network down/);
    });

    it('getNode 接口 404 或网络错误时直接抛出异常', async () => {
      globalThis.fetch = async () => {
        return new Response(
          JSON.stringify({ error: { code: 'not_found', message: 'node not found' } }),
          { status: 404, headers: { 'Content-Type': 'application/json' } }
        );
      };

      await assert.rejects(async () => {
        await getNode('test-node-id');
      });
    });

    it('getMetrics 接口网络错误时直接抛出异常，不再返回模拟时序', async () => {
      globalThis.fetch = async () => {
        throw new Error('metrics backend offline');
      };

      await assert.rejects(async () => {
        await getMetrics('node-1', { from_ms: 1000, to_ms: 2000 });
      }, /metrics backend offline/);
    });
  });

  describe('❸ SSE 连接生命周期、重连与状态通知', () => {
    class MockEventSource {
      url: string;
      onopen: (() => void) | null = null;
      onerror: ((err: any) => void) | null = null;
      listeners: Record<string, ((ev: any) => void)[]> = {};
      closed = false;

      static instances: MockEventSource[] = [];

      constructor(url: string) {
        this.url = url;
        MockEventSource.instances.push(this);
      }

      addEventListener(event: string, cb: (ev: any) => void) {
        if (!this.listeners[event]) this.listeners[event] = [];
        this.listeners[event].push(cb);
      }

      removeEventListener(event: string, cb: (ev: any) => void) {
        if (!this.listeners[event]) return;
        this.listeners[event] = this.listeners[event].filter((l) => l !== cb);
      }

      close() {
        this.closed = true;
      }

      simulateOpen() {
        if (this.onopen) this.onopen();
      }

      simulateError(err: any) {
        if (this.onerror) this.onerror(err);
      }

      simulateMessage(event: string, data: any) {
        const cbs = this.listeners[event] || [];
        for (const cb of cbs) {
          cb({ data: JSON.stringify(data) });
        }
      }
    }

    const originalEventSource = (globalThis as any).EventSource;

    beforeEach(() => {
      MockEventSource.instances = [];
      (globalThis as any).EventSource = MockEventSource;
    });

    it('建立订阅时触发 connecting，连接成功触发 connected，接收 metrics 与 node_state', () => {
      const statuses: SSEConnectionStatus[] = [];
      const receivedMetrics: MetricStreamEvent[] = [];
      const receivedStates: NodeStateStreamEvent[] = [];

      const unsubscribe = subscribeMetricsStream({
        onStatusChange: (status) => statuses.push(status),
        onMetrics: (m) => receivedMetrics.push(m),
        onNodeState: (s) => receivedStates.push(s),
      });

      assert.strictEqual(statuses[0], 'connecting');
      assert.strictEqual(MockEventSource.instances.length, 1);
      const es = MockEventSource.instances[0];
      assert.strictEqual(es.url, '/api/v1/stream');

      // 模拟连接成功
      es.simulateOpen();
      assert.strictEqual(statuses[1], 'connected');

      // 模拟收到推送
      es.simulateMessage('metrics', { node_id: 'n1', cpu_pct: 35.5 });
      assert.strictEqual(receivedMetrics.length, 1);
      assert.strictEqual(receivedMetrics[0].node_id, 'n1');
      assert.strictEqual(receivedMetrics[0].cpu_pct, 35.5);

      es.simulateMessage('node_state', { node_id: 'n1', conn_state: 'offline', last_seen_at_ms: 12345 });
      assert.strictEqual(receivedStates.length, 1);
      assert.strictEqual(receivedStates[0].conn_state, 'offline');

      unsubscribe();
      assert.strictEqual(es.closed, true);
    });

    it('发生错误时置为 disconnected，并关闭故障连接', () => {
      const statuses: SSEConnectionStatus[] = [];

      const unsubscribe = subscribeMetricsStream({
        onStatusChange: (status) => statuses.push(status),
      });

      const es = MockEventSource.instances[0];
      es.simulateOpen();
      assert.strictEqual(statuses[statuses.length - 1], 'connected');

      es.simulateError(new Error('connection drop'));
      assert.strictEqual(statuses[statuses.length - 1], 'disconnected');
      assert.strictEqual(es.closed, true);

      unsubscribe();
    });
  });

  describe('❹ 节点总览 (Overview) 真实渲染与离线瞬时指标变 --', () => {
    it('在线节点展示有效 CPU 与上下行网速；离线节点瞬时指标显示 --', () => {
      const testNodes: NodeItem[] = [
        {
          id: 'n-online',
          name: 'online-vps',
          conn_state: 'online',
          last_seen_at_ms: Date.now() - 2000,
          latest: {
            cpu_pct: 18.5,
            mem_used: 1073741824,
            mem_total: 2147483648,
            net_up_bps: 2048000,
            net_down_bps: 4096000,
            disk_used: 10737418240,
            disk_total: 53687091200,
            traffic_month_up: 5000000000,
            traffic_month_down: 10000000000,
          },
        },
        {
          id: 'n-offline',
          name: 'offline-vps',
          conn_state: 'offline',
          last_seen_at_ms: Date.now() - 120000,
          latest: null, // 离线节点后端不返回 latest
        },
      ];

      const qc = new QueryClient({
        defaultOptions: { queries: { retry: false } },
      });
      qc.setQueryData(['nodes'], testNodes);
      qc.setQueryData(['node-groups'], []);
      qc.setQueryData(['tags'], []);

      const html = renderToString(
        <QueryClientProvider client={qc}>
          <MemoryRouter>
            <Overview />
          </MemoryRouter>
        </QueryClientProvider>
      );

      // 验证在线节点有数值
      assert(html.includes('online-vps'), '渲染结果应包含在线节点名称');
      assert(html.includes('18.5%'), '在线节点应显示 18.5% CPU');
      assert(html.includes('2 MB/s') || html.includes('2.0 MB/s') || html.includes('2048000'), '在线节点显示上传网速');

      // 验证离线节点显示
      assert(html.includes('offline-vps'), '渲染结果应包含离线节点名称');
      assert(html.includes('--'), '离线节点缺少指标应展示 -- 而非 0');

      // 验证总览页包含实时状态指示器
      assert(
        html.includes('实时') || html.includes('已断开'),
        '总览页应包含 SSE 实时/已断开状态指示'
      );
    });
  });

  describe('❺ 开发免登录模式 (DevNoAuthBanner)', () => {
    it('当 dev_no_auth 为 true 时在顶部常驻警告横幅', () => {
      const qc = new QueryClient();
      qc.setQueryData(['healthz'], {
        status: 'ok',
        version: 'v0.1.0',
        dev_no_auth: true,
      });

      const html = renderToString(
        <QueryClientProvider client={qc}>
          <DevNoAuthBanner />
        </QueryClientProvider>
      );

      assert(html.includes('dev-no-auth-banner'), '应包含 dev-no-auth-banner 容器');
      assert(html.includes('开发模式：鉴权已关闭'), '应包含醒目标题 开发模式：鉴权已关闭');
    });

    it('当 dev_no_auth 为 false 或未开启时不渲染横幅', () => {
      const qc = new QueryClient();
      qc.setQueryData(['healthz'], {
        status: 'ok',
        version: 'v0.1.0',
        dev_no_auth: false,
      });

      const html = renderToString(
        <QueryClientProvider client={qc}>
          <DevNoAuthBanner />
        </QueryClientProvider>
      );

      assert.strictEqual(html, '', '正常生产模式下不应渲染免密警告横幅');
    });
  });
});
