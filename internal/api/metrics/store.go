package metrics

import (
	"encoding/json"
	"sync"
)

// LatestStore 提供纯内存最新值缓存与 SSE 广播通道（不查库，与落库解耦）。
type LatestStore struct {
	mu          sync.RWMutex
	latest      map[string]NodeLatest
	subscribers map[uint64]chan StreamEvent
	nextSubID   uint64
}

// NewLatestStore 创建 LatestStore 实例。
func NewLatestStore() *LatestStore {
	return &LatestStore{
		latest:      make(map[string]NodeLatest),
		subscribers: make(map[uint64]chan StreamEvent),
	}
}

// SetLatest 更新节点在内存中的最新采样值。
func (s *LatestStore) SetLatest(nodeID string, latest NodeLatest) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latest[nodeID] = latest
}

// GetLatest 读取节点在内存中的最新采样值。
func (s *LatestStore) GetLatest(nodeID string) (NodeLatest, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	val, ok := s.latest[nodeID]
	return val, ok
}

// DeleteLatest 从内存中移除节点最新采样值。
func (s *LatestStore) DeleteLatest(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.latest, nodeID)
}

// BroadcastMetrics 更新最新值缓存并向所有活跃 SSE 订阅者广播 metrics 事件。
func (s *LatestStore) BroadcastMetrics(nodeID string, latest NodeLatest) {
	s.SetLatest(nodeID, latest)

	evPayload := MetricStreamEvent{
		NodeID:     nodeID,
		TsMs:       latest.TsMs,
		CpuPct:     latest.CpuPct,
		MemUsed:    latest.MemUsed,
		NetUpBps:   latest.NetUpBps,
		NetDownBps: latest.NetDownBps,
	}
	data, err := json.Marshal(evPayload)
	if err != nil {
		return
	}

	s.broadcast(StreamEvent{
		Event: "metrics",
		Data:  string(data),
	})
}

// BroadcastNodeState 向所有活跃 SSE 订阅者广播 node_state 事件。
func (s *LatestStore) BroadcastNodeState(nodeID string, connState string, lastSeenMs int64) {
	evPayload := NodeStateStreamEvent{
		NodeID:       nodeID,
		ConnState:    connState,
		LastSeenAtMs: lastSeenMs,
	}
	data, err := json.Marshal(evPayload)
	if err != nil {
		return
	}

	s.broadcast(StreamEvent{
		Event: "node_state",
		Data:  string(data),
	})
}

func (s *LatestStore) broadcast(ev StreamEvent) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, ch := range s.subscribers {
		select {
		case ch <- ev:
		default:
			// 慢客户端丢弃单个报文，避免阻塞广播
		}
	}
}

// Subscribe 注册一个 SSE 订阅者，返回事件通道与释放函数。
// 客户端断开必须调用 unsubscribe，以安全关闭通道与回收资源。
func (s *LatestStore) Subscribe() (<-chan StreamEvent, func()) {
	s.mu.Lock()
	subID := s.nextSubID
	s.nextSubID++
	ch := make(chan StreamEvent, 64)
	s.subscribers[subID] = ch
	s.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.subscribers, subID)
			close(ch)
			s.mu.Unlock()
		})
	}

	return ch, unsubscribe
}

// ActiveSubscribers 返回当前正在订阅的连接数。
func (s *LatestStore) ActiveSubscribers() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.subscribers)
}
