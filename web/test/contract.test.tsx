import { describe, it, beforeEach } from 'node:test';
import assert from 'node:assert';

import {
  getNodes,
  getNodesPaged,
  getGroups,
  getNodeGroups,
  getTags,
  getEnrollTokens,
  fetchEvents,
  fetchEventTypes,
  getSettings,
  replaceNodeTags,
  checkHealth,
  queryClient,
  toItems,
} from '../src/api';

import nodesFixture from './fixtures/contracts/nodes.json';
import nodesEmptyFixture from './fixtures/contracts/nodes_empty.json';
import groupsFixture from './fixtures/contracts/groups.json';
import groupsEmptyFixture from './fixtures/contracts/groups_empty.json';
import tagsFixture from './fixtures/contracts/tags.json';
import tagsEmptyFixture from './fixtures/contracts/tags_empty.json';
import enrollTokensFixture from './fixtures/contracts/enroll_tokens.json';
import enrollTokensEmptyFixture from './fixtures/contracts/enroll_tokens_empty.json';
import eventsFixture from './fixtures/contracts/events.json';
import eventsEmptyFixture from './fixtures/contracts/events_empty.json';
import eventTypesFixture from './fixtures/contracts/event_types.json';
import settingsFixture from './fixtures/contracts/settings.json';
import replaceTagsFixture from './fixtures/contracts/replace_tags.json';
import healthzFixture from './fixtures/contracts/healthz.json';

function stubFetch(payload: any, status = 200) {
  globalThis.fetch = (async () =>
    new Response(JSON.stringify(payload), {
      status,
      headers: { 'Content-Type': 'application/json' },
    })) as any;
}

describe('P1-27 全端点契约测试 (Option A: 真实服务端 Fixture 消费断言)', () => {
  let originalFetch: typeof globalThis.fetch = globalThis.fetch;

  beforeEach(() => {
    originalFetch = globalThis.fetch;
    queryClient.clear();
  });

  describe('❶ 列表端点消费真实服务端信封并保证永远返回数组', () => {
    it('getNodes 正确消费 nodes.json 真实信封', async () => {
      stubFetch(nodesFixture);
      try {
        const nodes = await getNodes();
        assert(Array.isArray(nodes), 'getNodes 必须返回数组，绝不能返回信封对象');
        assert(nodes.length > 0, '应成功解析出节点列表项');
        assert(typeof nodes[0].id === 'string');
        assert(typeof nodes[0].name === 'string');
      } finally {
        globalThis.fetch = originalFetch;
      }
    });

    it('getNodes 消费 nodes_empty.json 空列表不抛异常且返回 []', async () => {
      stubFetch(nodesEmptyFixture);
      try {
        const nodes = await getNodes();
        assert(Array.isArray(nodes), '必须返回数组');
        assert.strictEqual(nodes.length, 0);
      } finally {
        globalThis.fetch = originalFetch;
      }
    });

    it('getNodesPaged 返回带分页信封包装的 list 数组', async () => {
      stubFetch(nodesFixture);
      try {
        const res = await getNodesPaged();
        assert(Array.isArray(res.list), 'res.list 必须是数组');
        assert(typeof res.total === 'number');
      } finally {
        globalThis.fetch = originalFetch;
      }
    });

    it('getGroups / getNodeGroups 正确消费 groups.json 真实信封', async () => {
      stubFetch(groupsFixture);
      try {
        const groups = await getGroups();
        assert(Array.isArray(groups), 'getGroups 必须返回数组，绝不能返回包含 items 的信封对象');
        assert(groups.length > 0);
        assert(typeof groups[0].id === 'string');
        assert(typeof groups[0].name === 'string');

        const groupsAlias = await getNodeGroups();
        assert.deepStrictEqual(groupsAlias, groups);
      } finally {
        globalThis.fetch = originalFetch;
      }
    });

    it('getGroups 消费 groups_empty.json 返回 []', async () => {
      stubFetch(groupsEmptyFixture);
      try {
        const groups = await getGroups();
        assert(Array.isArray(groups));
        assert.strictEqual(groups.length, 0);
      } finally {
        globalThis.fetch = originalFetch;
      }
    });

    it('getTags 正确消费 tags.json 真实信封', async () => {
      stubFetch(tagsFixture);
      try {
        const tags = await getTags();
        assert(Array.isArray(tags), 'getTags 必须返回数组');
        assert(tags.length > 0);
        assert(typeof tags[0].id === 'string');
        assert(typeof tags[0].name === 'string');
      } finally {
        globalThis.fetch = originalFetch;
      }
    });

    it('getTags 消费 tags_empty.json 返回 []', async () => {
      stubFetch(tagsEmptyFixture);
      try {
        const tags = await getTags();
        assert(Array.isArray(tags));
        assert.strictEqual(tags.length, 0);
      } finally {
        globalThis.fetch = originalFetch;
      }
    });

    it('getEnrollTokens 正确消费 enroll_tokens.json 真实信封', async () => {
      stubFetch(enrollTokensFixture);
      try {
        const tokens = await getEnrollTokens();
        assert(Array.isArray(tokens), 'getEnrollTokens 必须返回数组');
        assert(tokens.length > 0);
        assert(typeof tokens[0].id === 'string');
      } finally {
        globalThis.fetch = originalFetch;
      }
    });

    it('getEnrollTokens 消费 enroll_tokens_empty.json 返回 []', async () => {
      stubFetch(enrollTokensEmptyFixture);
      try {
        const tokens = await getEnrollTokens();
        assert(Array.isArray(tokens));
        assert.strictEqual(tokens.length, 0);
      } finally {
        globalThis.fetch = originalFetch;
      }
    });

    it('fetchEvents 正确消费 events.json 真实信封（events 数组 + limit/offset 分页）', async () => {
      stubFetch(eventsFixture);
      try {
        const res = await fetchEvents();
        assert(Array.isArray(res.events), 'res.events 必须是数组');
        assert(res.events.length > 0);
        assert(typeof res.total === 'number');
        assert(typeof res.limit === 'number');
        assert(typeof res.offset === 'number');
      } finally {
        globalThis.fetch = originalFetch;
      }
    });

    it('fetchEvents 消费 events_empty.json 返回空 events 列表', async () => {
      stubFetch(eventsEmptyFixture);
      try {
        const res = await fetchEvents();
        assert(Array.isArray(res.events));
        assert.strictEqual(res.events.length, 0);
      } finally {
        globalThis.fetch = originalFetch;
      }
    });

    it('fetchEventTypes 正确消费 event_types.json 真实信封', async () => {
      stubFetch(eventTypesFixture);
      try {
        const types = await fetchEventTypes();
        assert(Array.isArray(types), 'fetchEventTypes 必须返回数组');
        assert(types.length > 0);
        assert(typeof types[0].event_type === 'string');
      } finally {
        globalThis.fetch = originalFetch;
      }
    });
  });

  describe('❷ 非列表端点与变动契约验证', () => {
    it('getSettings 正确消费 settings.json', async () => {
      stubFetch(settingsFixture);
      try {
        const settings = await getSettings();
        assert(typeof settings === 'object' && settings !== null);
      } finally {
        globalThis.fetch = originalFetch;
      }
    });

    it('replaceNodeTags 服务端返回 {ok: true}', async () => {
      stubFetch(replaceTagsFixture);
      try {
        const res = await replaceNodeTags('test-node', ['t1']);
        assert.strictEqual(res.ok, true);
      } finally {
        globalThis.fetch = originalFetch;
      }
    });

    it('checkHealth 正确消费 healthz.json', async () => {
      stubFetch(healthzFixture);
      try {
        const res = await checkHealth();
        assert.strictEqual(res.status, 'ok');
      } finally {
        globalThis.fetch = originalFetch;
      }
    });
  });

  describe('❸ 错误不被静默吞没验证 (P1-27 §3)', () => {
    it('当 /api/v1/node-groups 500 时，getGroups 必须抛出异常而不是静默返回 []', async () => {
      stubFetch({ error: { code: 'server_error', message: 'DB down' } }, 500);
      try {
        let threw = false;
        try {
          await getGroups();
        } catch {
          threw = true;
        }
        assert.strictEqual(threw, true, 'getGroups 必须抛出错误');
      } finally {
        globalThis.fetch = originalFetch;
      }
    });

    it('当 /api/v1/tags 500 时，getTags 必须抛出异常而不是静默返回 []', async () => {
      stubFetch({ error: { code: 'server_error', message: 'DB down' } }, 500);
      try {
        let threw = false;
        try {
          await getTags();
        } catch {
          threw = true;
        }
        assert.strictEqual(threw, true, 'getTags 必须抛出错误');
      } finally {
        globalThis.fetch = originalFetch;
      }
    });
  });
});
