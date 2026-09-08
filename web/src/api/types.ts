/**
 * dash 前端类型定义
 * 依循 docs/08-field-map.md, docs/12-api-spec.md, P1-13-查询api.md
 */

// 统一错误结构（docs/12-api-spec.md §1）
export interface ApiError {
  code: string;
  message: string;
  detail?: any;
}

export interface ApiErrorResponse {
  error: ApiError;
}

// 登录与认证
export interface LoginRequest {
  username: string;
  password: string;
}

export interface UserMe {
  id: string;
  username: string;
  is_admin?: boolean;
  role?: string;
}

// 时序跨度
export type TimeSeriesSpan = '6h' | '3d' | '60d' | '1y';

// 时序查询响应（与 08-field-map.md §4 及任务 13 完全一致）
export interface MetricsQueryResponse {
  node_id: string;
  source: 'sample_host' | 'sample_host_1m' | 'sample_host_1h' | 'sample_host_1d' | string;
  from_ms: number;
  to_ms: number;
  step_ms: number;
  ts_ms: number[];
  series: {
    cpu_pct?: (number | null)[];
    mem_used?: (number | null)[];
    swap_used?: (number | null)[];
    load1?: (number | null)[];
    load5?: (number | null)[];
    load15?: (number | null)[];
    net_up_bps?: (number | null)[];
    net_down_bps?: (number | null)[];
    net_total_up?: (number | null)[];
    net_total_down?: (number | null)[];
    traffic_up?: (number | null)[];
    traffic_down?: (number | null)[];
    uptime_s?: (number | null)[];
    disk_used?: (number | null)[];
    proc_count?: (number | null)[];
    tcp_count?: (number | null)[];
    udp_count?: (number | null)[];
    // 允许测速和自定义指标
    [key: string]: (number | null)[] | undefined;
  };
}

// 节点最新采样（读内存缓存，08-field-map.md §1）
// 约束：采集失败时单项省略，全部字段均为可选
export interface NodeLatest {
  ts_ms?: number;
  cpu_pct?: number;
  mem_used?: number;
  mem_total?: number;
  disk_used?: number;
  disk_total?: number;
  net_up_bps?: number;
  net_down_bps?: number;
  traffic_month_up?: number;
  traffic_month_down?: number;
  uptime_s?: number;
  load1?: number;
  tcp_count?: number;
}

// 节点标签
export interface NodeTag {
  id: string;
  name: string;
  color?: string;
  created_at_ms?: number;
  node_count?: number;
}

// 节点分组
export interface NodeGroup {
  id: string;
  name: string;
  display_order?: number;
  created_at_ms?: number;
  node_count?: number;
}

// 节点计费信息
export interface NodeBilling {
  node_id?: string;
  currency?: string;
  price?: number;
  cycle_days?: number;
  is_auto_renew?: boolean;
  expires_at_ms?: number;
  traffic_limit?: number;
  traffic_limit_kind?: string;
  traffic_reset_day?: number;
  updated_at_ms?: number;
}

// 节点硬件事实（node_facts，08-field-map.md §3）
export interface NodeFacts {
  arch?: string;
  os_name?: string;
  os_version?: string;
  kernel?: string;
  virt?: string;
  cpu_model?: string;
  cpu_cores?: number;
  cpu_threads?: number;
  mem_total?: number;
  swap_total?: number;
  disk_total?: number;
  ipv4?: string;
  ipv6?: string;
  boot_at_ms?: number;
}

// 节点列表项（12-api-spec.md §4）
// 约束：group / billing / latest / clock_skew_ms / facts 无数据时可能被后端省略
export interface NodeItem {
  id: string;
  name: string;
  node_group_id?: string | null;
  display_order?: number;
  is_hidden?: boolean;
  agent_version?: string;
  conn_state: 'online' | 'offline' | 'never';
  last_seen_at_ms?: number | null;
  clock_skew_ms?: number | null;
  note?: string | null;
  created_at_ms?: number;
  updated_at_ms?: number;
  group?: NodeGroup | null;
  tags?: NodeTag[];
  latest?: NodeLatest | null;
  billing?: NodeBilling | null;
  facts?: NodeFacts | null;
}

// 节点详情
export interface NodeDetail extends NodeItem {
  facts?: NodeFacts | null;
}

// 分页通用结构
export interface PageResult<T> {
  items: T[];
  total: number;
  page: number;
  page_size: number;
}

// 节点 CRUD 请求参数
export interface CreateNodeParams {
  name: string;
  node_group_id?: string;
  display_order?: number;
  is_hidden?: boolean;
  note?: string;
}

export interface UpdateNodeParams {
  name?: string;
  node_group_id?: string | null;
  display_order?: number;
  is_hidden?: boolean;
  note?: string;
}

// 分组 CRUD 请求参数
export interface CreateGroupParams {
  name: string;
  display_order?: number;
}

export interface UpdateGroupParams {
  name?: string;
  display_order?: number;
}

// 标签 CRUD 请求参数
export interface CreateTagParams {
  name: string;
  color?: string;
}

export interface UpdateTagParams {
  name?: string;
  color?: string;
}

// 批量打标签参数
export interface BatchTagParams {
  node_ids: string[];
  tag_ids: string[];
  op: 'add' | 'remove';
}

// 注册令牌
export interface EnrollToken {
  id: string;
  preset_name?: string;
  preset_group_id?: string;
  expires_at_ms: number;
  used_at_ms?: number;
  used_node_id?: string;
  created_by?: string;
  created_at_ms: number;
  is_expired?: boolean;
  is_used?: boolean;
  install_cmd?: string;
  token?: string;
}

export interface CreateEnrollTokenParams {
  preset_name?: string;
  preset_group_id?: string;
  ttl_hours?: number;
}

export interface CreateEnrollTokenResponse {
  id: string;
  token: string;
  expires_at_ms: number;
  install_cmd: string;
}

// 计费更新参数
export interface UpdateBillingParams {
  currency?: string;
  price?: number;
  cycle_days?: number;
  is_auto_renew?: boolean;
  expires_at_ms?: number;
  traffic_limit?: number;
  traffic_limit_kind?: string;
  traffic_reset_day?: number;
}

// 系统设置
export interface SystemSettings {
  'site.domain': string;
  'site.console_domain'?: string;
  'retention.raw_days': number;
  'retention.1m_days': number;
  'retention.1h_days': number;
  'retention.1d_days': number;
  'collect.interval_fast_s': number;
  'collect.interval_slow_s': number;
  'collect.enable_conns': boolean;
}

// SSE 流事件
export interface MetricStreamEvent {
  node_id: string;
  ts_ms?: number;
  cpu_pct?: number;
  mem_used?: number;
  net_up_bps?: number;
  net_down_bps?: number;
  disk_used?: number;
  load1?: number;
}

export interface NodeStateStreamEvent {
  node_id: string;
  conn_state: 'online' | 'offline';
  last_seen_at_ms: number;
}

// 健康检查响应（docs/12-api-spec.md §9）
export interface HealthResponse {
  status: string;
  version?: string;
  db?: string;
  agents_online?: number;
  dev_no_auth?: boolean;
}

// 通知渠道（P2-01 / 09-events-notify.md §3）
export interface NotifyChannel {
  id: string;
  name: string;
  channel_kind: 'telegram' | 'webhook';
  credential_id?: string;
  config_json: string;
  is_enabled: boolean;
  created_at_ms: number;
  updated_at_ms: number;
  masked_secret?: string;
}

// 路由规则（P2-01 / 09-events-notify.md §4）
export interface NotifyRule {
  id: string;
  name: string;
  is_enabled: boolean;
  event_pattern: string;
  min_severity: 'info' | 'warning' | 'critical';
  notify_channel_id: string;
  template_name?: string;
  throttle_s: number;
  quiet_start_min?: number | null;
  quiet_end_min?: number | null;
  display_order: number;
  created_at_ms: number;
  updated_at_ms: number;
}

// 投递记录（P2-01 / 09-events-notify.md §4）
export interface NotifyDelivery {
  id: string;
  event_id: string;
  channel_id: string;
  rule_id?: string;
  state: 'pending' | 'sent' | 'failed' | 'throttled' | 'quiet_held';
  attempt: number;
  last_error?: string;
  rendered_text?: string;
  sent_at_ms?: number | null;
  created_at_ms: number;
  updated_at_ms: number;
}

