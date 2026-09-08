import React from 'react';
import { renderToString } from 'react-dom/server';
import { MemoryRouter } from 'react-router-dom';
import { QueryClientProvider } from '@tanstack/react-query';
import { describe, it, beforeEach } from 'node:test';
import assert from 'node:assert';

import { getGroups, getNodeGroups, getTags, getNodes, queryClient } from '../src/api';
import { Overview } from '../src/pages/Overview';

/**
 * P1-27 F1 回归：列表端点统一返回分页信封 {items,total,page,page_size}
 * （docs/12-api-spec.md §3）。前端曾把 /api/v1/node-groups 与 /api/v1/tags
 * 的响应当成裸数组，导致 groupList.map 抛 "y.map is not a function" 整页崩溃。
 * 该缺陷自 P1-17 起就存在，P1-22 之后被 401 掩盖（catch 返回 []），
 * P1-24 修好登录态后才暴露出来。
 */

const PAGED = (items: any[]) => ({ items, total: items.length, page: 1, page_size: 50 });

function stubFetch(payload: any, status = 200) {
  globalThis.fetch = (async () =>
    new Response(JSON.stringify(payload), {
      status,
      headers: { 'Content-Type': 'application/json' },
    })) as any;
}

describe('P1-27 列表端点响应信封契约', () => {
  let originalFetch: typeof globalThis.fetch = globalThis.fetch;

  beforeEach(() => {
    originalFetch = globalThis.fetch;
    queryClient.clear();
  });

  describe('❶ 分组与标签必须拆信封，永远返回数组', () => {
    it('getGroups 收到分页信封时返回 items 数组', async () => {
      stubFetch(PAGED([{ id: 'g1', name: '香港' }]));
      try {
        const groups = await getGroups();
        assert(Array.isArray(groups), 'getGroups 必须返回数组');
        assert.strictEqual(groups.length, 1);
        assert.strictEqual(groups[0].name, '香港');
      } finally {
        globalThis.fetch = originalFetch;
      }
    });

    it('getNodeGroups 是 getGroups 的别名，行为一致', async () => {
      stubFetch(PAGED([{ id: 'g1', name: '东京' }]));
      try {
        const groups = await getNodeGroups();
        assert(Array.isArray(groups));
        assert.strictEqual(groups[0].name, '东京');
      } finally {
        globalThis.fetch = originalFetch;
      }
    });

    it('getTags 收到分页信封时返回 items 数组', async () => {
      stubFetch(PAGED([{ id: 't1', name: 'prod' }]));
      try {
        const tags = await getTags();
        assert(Array.isArray(tags));
        assert.strictEqual(tags[0].name, 'prod');
      } finally {
        globalThis.fetch = originalFetch;
      }
    });

    it('空列表（items 为 [] 或 null）时返回空数组而不是崩溃', async () => {
      try {
        stubFetch(PAGED([]));
        assert.deepStrictEqual(await getGroups(), []);

        stubFetch({ items: null, total: 0, page: 1, page_size: 50 });
        assert.deepStrictEqual(await getTags(), []);
      } finally {
        globalThis.fetch = originalFetch;
      }
    });

    it('兼容裸数组响应（旧契约不回归）', async () => {
      stubFetch([{ id: 'g1', name: '裸数组' }]);
      try {
        const groups = await getGroups();
        assert(Array.isArray(groups));
        assert.strictEqual(groups[0].name, '裸数组');
      } finally {
        globalThis.fetch = originalFetch;
      }
    });
  });

  describe('❷ 节点列表同样不许把 null 透传给页面', () => {
    it('items 为 null 时 getNodes 返回 []', async () => {
      stubFetch({ items: null, total: 0, page: 1, page_size: 50 });
      try {
        const nodes = await getNodes();
        assert(Array.isArray(nodes), 'getNodes 必须返回数组');
        assert.strictEqual(nodes.length, 0);
      } finally {
        globalThis.fetch = originalFetch;
      }
    });
  });

  describe('❸ 总览页在全空数据下必须能渲染，不触发 ErrorBoundary', () => {
    it('三个列表端点都返回空信封时，Overview 不抛异常', () => {
      stubFetch(PAGED([]));
      try {
        const html = renderToString(
          <QueryClientProvider client={queryClient}>
            <MemoryRouter initialEntries={['/nodes']}>
              <Overview />
            </MemoryRouter>
          </QueryClientProvider>
        );
        assert(html.includes('节点总览'), '总览页应正常渲染标题');
        assert(!html.includes('当前页面加载出错'), '不应落到 ErrorBoundary');
      } finally {
        globalThis.fetch = originalFetch;
      }
    });
  });
});
