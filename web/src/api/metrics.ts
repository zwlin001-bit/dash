import { apiFetch } from './client';
import { MetricsQueryResponse, MetricStreamEvent, NodeLatest, NodeStateStreamEvent, TimeSeriesSpan } from './types';
import { generateMockMetrics, mockNodeDetail } from '../mock/mockData';

export interface GetMetricsParams {
  from_ms: number;
  to_ms: number;
  fields?: string;
  max_points?: number;
  span?: TimeSeriesSpan;
}

export async function getMetrics(nodeId: string, params: GetMetricsParams): Promise<MetricsQueryResponse> {
  const query = new URLSearchParams();
  query.set('from_ms', params.from_ms.toString());
  query.set('to_ms', params.to_ms.toString());
  if (params.fields) query.set('fields', params.fields);
  if (params.max_points) query.set('max_points', params.max_points.toString());

  try {
    return await apiFetch<MetricsQueryResponse>(`/api/v1/nodes/${nodeId}/metrics?${query.toString()}`);
  } catch {
    // 脚手架阶段回退到符合契约的 Mock 时序数据
    const span = params.span || '6h';
    return generateMockMetrics(nodeId, span);
  }
}

export async function getLatestMetrics(nodeId: string): Promise<NodeLatest> {
  try {
    return await apiFetch<NodeLatest>(`/api/v1/nodes/${nodeId}/latest`);
  } catch {
    return mockNodeDetail.latest!;
  }
}

/**
 * 订阅实时指标与状态变更 SSE 流（12-api-spec.md §6）
 */
export function subscribeMetricsStream(handlers: {
  onMetrics?: (ev: MetricStreamEvent) => void;
  onNodeState?: (ev: NodeStateStreamEvent) => void;
  onError?: (err: any) => void;
}): () => void {
  let es: EventSource | null = null;
  try {
    es = new EventSource('/api/v1/stream');

    es.addEventListener('metrics', (e) => {
      if (handlers.onMetrics && e.data) {
        try {
          handlers.onMetrics(JSON.parse(e.data));
        } catch {}
      }
    });

    es.addEventListener('node_state', (e) => {
      if (handlers.onNodeState && e.data) {
        try {
          handlers.onNodeState(JSON.parse(e.data));
        } catch {}
      }
    });

    es.addEventListener('ping', () => {
      // 保活事件，无需额外处理
    });

    es.onerror = (err) => {
      if (handlers.onError) handlers.onError(err);
    };
  } catch (e) {
    if (handlers.onError) handlers.onError(e);
  }

  // 返回取消订阅函数
  return () => {
    if (es) {
      es.close();
      es = null;
    }
  };
}
