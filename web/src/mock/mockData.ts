import { MetricsQueryResponse, NodeDetail, NodeItem, SystemSettings, TimeSeriesSpan } from '../api/types';

export const mockCurrentUser = {
  id: '01JADMIN000000000000000000',
  username: 'admin',
  role: 'admin',
};

export const mockNodes: NodeItem[] = [
  {
    id: '01JBX79N1A9W5K3E8S6T2Y4P0A',
    name: 'hk-edge-01',
    conn_state: 'online',
    last_seen_at_ms: Date.now() - 3000,
    group: { id: 'grp-hk', name: '香港专线' },
    tags: [
      { id: 't1', name: 'proxy', color: '#4da6ff' },
      { id: 't2', name: 'hk-cmi', color: '#00d4aa' },
    ],
    latest: {
      ts_ms: Date.now() - 3000,
      cpu_pct: 12.4,
      mem_used: 1073741824 * 1.8,
      mem_total: 1073741824 * 4,
      disk_used: 1073741824 * 18,
      disk_total: 1073741824 * 60,
      net_up_bps: 4500000,
      net_down_bps: 12800000,
      traffic_month_up: 1073741824 * 180,
      traffic_month_down: 1073741824 * 420,
      uptime_s: 864000,
      load1: 0.85,
      tcp_count: 320,
    },
    billing: {
      expires_at_ms: Date.now() + 86400000 * 45,
      traffic_limit: 1073741824 * 1000,
    },
    clock_skew_ms: 12,
  },
  {
    id: '01JBX79N1A9W5K3E8S6T2Y4P0B',
    name: 'tokyo-core-02',
    conn_state: 'online',
    last_seen_at_ms: Date.now() - 4000,
    group: { id: 'grp-jp', name: '日本核心' },
    tags: [{ id: 't3', name: 'bgp', color: '#b392f0' }],
    latest: {
      ts_ms: Date.now() - 4000,
      cpu_pct: 28.7,
      mem_used: 1073741824 * 3.2,
      mem_total: 1073741824 * 8,
      disk_used: 1073741824 * 42,
      disk_total: 1073741824 * 120,
      net_up_bps: 15200000,
      net_down_bps: 28400000,
      traffic_month_up: 1073741824 * 320,
      traffic_month_down: 1073741824 * 850,
      uptime_s: 1420000,
      load1: 1.45,
      tcp_count: 580,
    },
    billing: {
      expires_at_ms: Date.now() + 86400000 * 5, // 即将到期 warn
      traffic_limit: 1073741824 * 2000,
    },
    clock_skew_ms: -5,
  },
  {
    id: '01JBX79N1A9W5K3E8S6T2Y4P0C',
    name: 'sg-node-03',
    conn_state: 'offline',
    last_seen_at_ms: Date.now() - 3600000 * 5,
    group: { id: 'grp-sg', name: '新加坡备用' },
    tags: [{ id: 't4', name: 'backup', color: '#ffb870' }],
    latest: {
      ts_ms: Date.now() - 3600000 * 5,
      cpu_pct: 0,
      mem_used: 0,
      net_up_bps: 0,
      net_down_bps: 0,
    },
    billing: {
      expires_at_ms: Date.now() - 86400000 * 2, // 已过期 err
    },
    clock_skew_ms: 0,
  },
  {
    id: '01JBX79N1A9W5K3E8S6T2Y4P0D',
    name: 'us-west-04',
    conn_state: 'never',
    last_seen_at_ms: 0,
    tags: [{ id: 't5', name: 'staging', color: '#8999af' }],
  },
];

export const mockNodeDetail: NodeDetail = {
  ...mockNodes[0],
  facts: {
    arch: 'amd64',
    os_name: 'Debian GNU/Linux',
    os_version: '12 (bookworm)',
    kernel: 'Linux 6.1.0-21-amd64',
    virt: 'KVM',
    cpu_model: 'AMD EPYC 7763 64-Core Processor',
    cpu_cores: 2,
    cpu_threads: 4,
    mem_total: 1073741824 * 4,
    swap_total: 1073741824 * 2,
    disk_total: 1073741824 * 60,
    ipv4: '103.145.22.18',
    ipv6: '2400:8901::f03c:93ff:fe94:218',
    boot_at_ms: Date.now() - 86400000 * 10,
  },
};

export const mockSettings: SystemSettings = {
  'site.domain': 'dash.example.com',
  'retention.raw_days': 7,
  'retention.1m_days': 30,
  'retention.1h_days': 180,
  'retention.1d_days': 730,
  'collect.interval_fast_s': 5,
  'collect.interval_slow_s': 60,
  'collect.enable_conns': true,
};

/**
 * 为 4 个跨度生成真实合理的列式时序 Mock 数据
 * 包含断网缺失点（null），用于验证 connectNulls: false
 */
export function generateMockMetrics(nodeId: string, span: TimeSeriesSpan): MetricsQueryResponse {
  const now = Date.now();
  let durationMs = 6 * 3600 * 1000;
  let source = 'sample_host';
  let stepMs = 5000;
  let pointsCount = 120;

  switch (span) {
    case '6h':
      durationMs = 6 * 3600 * 1000;
      source = 'sample_host';
      stepMs = 0; // raw 表 step_ms 契约为 0
      pointsCount = 180;
      break;
    case '3d':
      durationMs = 3 * 86400 * 1000;
      source = 'sample_host_1m';
      stepMs = 60000;
      pointsCount = 200;
      break;
    case '60d':
      durationMs = 60 * 86400 * 1000;
      source = 'sample_host_1h';
      stepMs = 3600000;
      pointsCount = 240;
      break;
    case '1y':
      durationMs = 365 * 86400 * 1000;
      source = 'sample_host_1d';
      stepMs = 86400000;
      pointsCount = 365;
      break;
  }

  const fromMs = now - durationMs;
  const toMs = now;
  const interval = durationMs / pointsCount;

  const ts_ms: number[] = [];
  const cpu_pct: (number | null)[] = [];
  const mem_used: (number | null)[] = [];
  const net_up_bps: (number | null)[] = [];
  const net_down_bps: (number | null)[] = [];
  const load1: (number | null)[] = [];

  const baseMem = 1073741824 * 1.5;

  for (let i = 0; i < pointsCount; i++) {
    const t = fromMs + i * interval;
    ts_ms.push(Math.round(t));

    // 模拟偶发的机器断网或上报缺失（第 35-37 点为 null）
    if (i >= 35 && i <= 37) {
      cpu_pct.push(null);
      mem_used.push(null);
      net_up_bps.push(null);
      net_down_bps.push(null);
      load1.push(null);
      continue;
    }

    // 拟合周期性波动 + 随机尖峰
    const cycle = Math.sin((i / pointsCount) * Math.PI * 4);
    const noise = Math.random() * 8;
    const spike = i === 70 || i === 140 ? 35 : 0;

    const cpu = Math.max(1, Math.min(100, Math.round((15 + cycle * 10 + noise + spike) * 10) / 10));
    const mem = Math.round(baseMem + cycle * 300000000 + Math.random() * 150000000);
    const upBps = Math.round(2000000 + cycle * 1200000 + Math.random() * 800000 + (spike ? 10000000 : 0));
    const downBps = Math.round(8000000 + cycle * 4500000 + Math.random() * 3000000 + (spike ? 25000000 : 0));
    const load = Math.round((0.5 + cycle * 0.4 + (spike ? 1.8 : 0) + Math.random() * 0.2) * 100) / 100;

    cpu_pct.push(cpu);
    mem_used.push(mem);
    net_up_bps.push(upBps);
    net_down_bps.push(downBps);
    load1.push(load);
  }

  return {
    node_id: nodeId,
    source,
    from_ms: fromMs,
    to_ms: toMs,
    step_ms: stepMs,
    ts_ms,
    series: {
      cpu_pct,
      mem_used,
      net_up_bps,
      net_down_bps,
      load1,
    },
  };
}
