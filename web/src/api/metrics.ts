import { apiFetch } from './client';
import { MetricsQueryResponse, MetricStreamEvent, NodeLatest, NodeStateStreamEvent, TimeSeriesSpan } from './types';

export interface GetMetricsParams {
  from_ms: number;
  to_ms: number;
  fields?: string;
  max_points?: number;
  span?: TimeSeriesSpan;
}

export async function getMetrics(
  nodeId: string,
  params: GetMetricsParams,
  signal?: AbortSignal
): Promise<MetricsQueryResponse> {
  const query = new URLSearchParams();
  query.set('from_ms', params.from_ms.toString());
  query.set('to_ms', params.to_ms.toString());
  if (params.fields) query.set('fields', params.fields);
  if (params.max_points) query.set('max_points', params.max_points.toString());

  return await apiFetch<MetricsQueryResponse>(`/api/v1/nodes/${nodeId}/metrics?${query.toString()}`, { signal });
}

export async function getLatestMetrics(nodeId: string): Promise<NodeLatest> {
  return await apiFetch<NodeLatest>(`/api/v1/nodes/${nodeId}/latest`);
}

export type SSEConnectionStatus = 'connecting' | 'connected' | 'disconnected';

export interface SubscribeStreamHandlers {
  onMetrics?: (ev: MetricStreamEvent) => void;
  onNodeState?: (ev: NodeStateStreamEvent) => void;
  onError?: (err: any) => void;
  onStatusChange?: (status: SSEConnectionStatus) => void;
}

/**
 * 订阅实时指标与状态变更 SSE 流（12-api-spec.md §6）
 * 支持指数退避自动重连与状态通知
 */
export function subscribeMetricsStream(handlers: SubscribeStreamHandlers): () => void {
  let es: EventSource | null = null;
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  let isClosed = false;
  let backoffMs = 1000;
  const maxBackoffMs = 15000;

  function connect() {
    if (isClosed) return;

    handlers.onStatusChange?.('connecting');

    try {
      es = new EventSource('/api/v1/stream');

      es.onopen = () => {
        if (isClosed) {
          es?.close();
          return;
        }
        backoffMs = 1000;
        handlers.onStatusChange?.('connected');
      };

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
        // 保活心跳
      });

      es.onerror = (err) => {
        if (handlers.onError) handlers.onError(err);
        handlers.onStatusChange?.('disconnected');

        if (es) {
          es.close();
          es = null;
        }

        if (!isClosed && !reconnectTimer) {
          const delay = backoffMs;
          backoffMs = Math.min(backoffMs * 2, maxBackoffMs);
          reconnectTimer = setTimeout(() => {
            reconnectTimer = null;
            connect();
          }, delay);
        }
      };
    } catch (e) {
      if (handlers.onError) handlers.onError(e);
      handlers.onStatusChange?.('disconnected');

      if (!isClosed && !reconnectTimer) {
        const delay = backoffMs;
        backoffMs = Math.min(backoffMs * 2, maxBackoffMs);
        reconnectTimer = setTimeout(() => {
          reconnectTimer = null;
          connect();
        }, delay);
      }
    }
  }

  connect();

  // 返回取消订阅函数
  return () => {
    isClosed = true;
    if (reconnectTimer) {
      clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }
    if (es) {
      es.close();
      es = null;
    }
  };
}
