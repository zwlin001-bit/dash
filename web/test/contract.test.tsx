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
  fetchNotifyChannels,
  fetchNotifyRules,
  fetchNotifyDeliveries,
  fetchCredentials,
  fetchCloudAccounts,
  fetchCloudResources,
  fetchJobs,
  fetchJobKinds,
  fetchGuardOverview,
  fetchGuardCycles,
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

import channelsFixture from './fixtures/contracts/channels.json';
import channelsEmptyFixture from './fixtures/contracts/channels_empty.json';
import rulesFixture from './fixtures/contracts/rules.json';
import rulesEmptyFixture from './fixtures/contracts/rules_empty.json';
import deliveriesFixture from './fixtures/contracts/deliveries.json';
import deliveriesEmptyFixture from './fixtures/contracts/deliveries_empty.json';
import credentialsFixture from './fixtures/contracts/credentials.json';
import credentialsEmptyFixture from './fixtures/contracts/credentials_empty.json';
import cloudAccountsFixture from './fixtures/contracts/cloud_accounts.json';
import cloudAccountsEmptyFixture from './fixtures/contracts/cloud_accounts_empty.json';
import cloudResourcesFixture from './fixtures/contracts/cloud_resources.json';
import cloudResourcesEmptyFixture from './fixtures/contracts/cloud_resources_empty.json';
import jobsFixture from './fixtures/contracts/jobs.json';
import jobsEmptyFixture from './fixtures/contracts/jobs_empty.json';
import jobKindsFixture from './fixtures/contracts/job_kinds.json';
import guardOverviewFixture from './fixtures/contracts/guard_overview.json';
import guardCyclesFixture from './fixtures/contracts/guard_cycles.json';
import guardCyclesEmptyFixture from './fixtures/contracts/guard_cycles_empty.json';

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

    it('fetchNotifyChannels 正确消费 channels.json 与 channels_empty.json', async () => {
      stubFetch(channelsFixture);
      try {
        const chs = await fetchNotifyChannels();
        assert(Array.isArray(chs), '必须返回数组');
        assert(chs.length > 0);
        assert(typeof chs[0].id === 'string');
      } finally {
        globalThis.fetch = originalFetch;
      }

      stubFetch(channelsEmptyFixture);
      try {
        const chs = await fetchNotifyChannels();
        assert(Array.isArray(chs));
        assert.strictEqual(chs.length, 0);
      } finally {
        globalThis.fetch = originalFetch;
      }
    });

    it('fetchNotifyRules 正确消费 rules.json 与 rules_empty.json', async () => {
      stubFetch(rulesFixture);
      try {
        const rules = await fetchNotifyRules();
        assert(Array.isArray(rules), '必须返回数组');
        assert(rules.length > 0);
        assert(typeof rules[0].id === 'string');
      } finally {
        globalThis.fetch = originalFetch;
      }

      stubFetch(rulesEmptyFixture);
      try {
        const rules = await fetchNotifyRules();
        assert(Array.isArray(rules));
        assert.strictEqual(rules.length, 0);
      } finally {
        globalThis.fetch = originalFetch;
      }
    });

    it('fetchNotifyDeliveries 正确消费 deliveries.json 与 deliveries_empty.json', async () => {
      stubFetch(deliveriesFixture);
      try {
        const res = await fetchNotifyDeliveries();
        assert(Array.isArray(res.deliveries), 'res.deliveries 必须是数组');
        assert(res.deliveries.length > 0);
        assert(typeof res.total === 'number');
      } finally {
        globalThis.fetch = originalFetch;
      }

      stubFetch(deliveriesEmptyFixture);
      try {
        const res = await fetchNotifyDeliveries();
        assert(Array.isArray(res.deliveries));
        assert.strictEqual(res.deliveries.length, 0);
      } finally {
        globalThis.fetch = originalFetch;
      }
    });

    it('fetchCredentials 正确消费 credentials.json 与 credentials_empty.json', async () => {
      stubFetch(credentialsFixture);
      try {
        const creds = await fetchCredentials();
        assert(Array.isArray(creds), '必须返回数组');
        assert(creds.length > 0);
        assert(typeof creds[0].id === 'string');
      } finally {
        globalThis.fetch = originalFetch;
      }

      stubFetch(credentialsEmptyFixture);
      try {
        const creds = await fetchCredentials();
        assert(Array.isArray(creds));
        assert.strictEqual(creds.length, 0);
      } finally {
        globalThis.fetch = originalFetch;
      }
    });

    it('fetchCloudAccounts 正确消费 cloud_accounts.json 与 cloud_accounts_empty.json', async () => {
      stubFetch(cloudAccountsFixture);
      try {
        const accs = await fetchCloudAccounts();
        assert(Array.isArray(accs), '必须返回数组');
        assert(accs.length > 0);
        assert(typeof accs[0].id === 'string');
      } finally {
        globalThis.fetch = originalFetch;
      }

      stubFetch(cloudAccountsEmptyFixture);
      try {
        const accs = await fetchCloudAccounts();
        assert(Array.isArray(accs));
        assert.strictEqual(accs.length, 0);
      } finally {
        globalThis.fetch = originalFetch;
      }
    });

    it('fetchCloudResources 正确消费 cloud_resources.json 与 cloud_resources_empty.json', async () => {
      stubFetch(cloudResourcesFixture);
      try {
        const res = await fetchCloudResources();
        assert(Array.isArray(res), '必须返回数组');
        assert(res.length > 0);
        assert(typeof res[0].id === 'string');
      } finally {
        globalThis.fetch = originalFetch;
      }

      stubFetch(cloudResourcesEmptyFixture);
      try {
        const res = await fetchCloudResources();
        assert(Array.isArray(res));
        assert.strictEqual(res.length, 0);
      } finally {
        globalThis.fetch = originalFetch;
      }
    });

    it('fetchJobs 正确消费 jobs.json 与 jobs_empty.json', async () => {
      stubFetch(jobsFixture);
      try {
        const res = await fetchJobs();
        assert(Array.isArray(res.jobs), 'res.jobs 必须是数组');
        assert(res.jobs.length > 0);
        assert(typeof res.total === 'number');
      } finally {
        globalThis.fetch = originalFetch;
      }

      stubFetch(jobsEmptyFixture);
      try {
        const res = await fetchJobs();
        assert(Array.isArray(res.jobs));
        assert.strictEqual(res.jobs.length, 0);
      } finally {
        globalThis.fetch = originalFetch;
      }
    });

    it('fetchGuardCycles 正确消费 guard_cycles.json 与 guard_cycles_empty.json', async () => {
      stubFetch(guardCyclesFixture);
      try {
        const res = await fetchGuardCycles();
        assert(Array.isArray(res.cycles), 'res.cycles 必须是数组');
        assert(res.cycles.length > 0);
        assert(typeof res.total === 'number');
      } finally {
        globalThis.fetch = originalFetch;
      }

      stubFetch(guardCyclesEmptyFixture);
      try {
        const res = await fetchGuardCycles();
        assert(Array.isArray(res.cycles));
        assert.strictEqual(res.cycles.length, 0);
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

    it('fetchJobKinds 正确消费 job_kinds.json', async () => {
      stubFetch(jobKindsFixture);
      try {
        const res = await fetchJobKinds();
        assert(Array.isArray(res.kinds), 'res.kinds 必须是数组');
        assert(res.kinds.length > 0);
        assert(typeof res.kinds[0].kind === 'string');
      } finally {
        globalThis.fetch = originalFetch;
      }
    });

    it('fetchGuardOverview 正确消费 guard_overview.json', async () => {
      stubFetch(guardOverviewFixture);
      try {
        const res = await fetchGuardOverview();
        assert(Array.isArray(res.accounts), 'res.accounts 必须是数组');
        assert(Array.isArray(res.recent_cycles), 'res.recent_cycles 必须是数组');
        assert(typeof res.total_instances === 'number');
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
