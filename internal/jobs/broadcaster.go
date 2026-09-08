package jobs

import (
	"sync"
)

// StreamEvent SSE 广播事件。
type StreamEvent struct {
	Event string `json:"event"` // snapshot, job, step, log
	Data  any    `json:"data"`
}

// Broadcaster 管理每个 Job 的实时订阅与事件分发。
type Broadcaster struct {
	mu   sync.RWMutex
	subs map[string]map[chan StreamEvent]struct{}
}

// NewBroadcaster 创建广播管理器。
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		subs: make(map[string]map[chan StreamEvent]struct{}),
	}
}

// Subscribe 订阅指定 jobID 的实时事件。返回接收 channel 和取消订阅函数。
func (b *Broadcaster) Subscribe(jobID string) (<-chan StreamEvent, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	ch := make(chan StreamEvent, 64)
	if _, ok := b.subs[jobID]; !ok {
		b.subs[jobID] = make(map[chan StreamEvent]struct{})
	}
	b.subs[jobID][ch] = struct{}{}

	unsubscribe := func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if m, ok := b.subs[jobID]; ok {
			delete(m, ch)
			close(ch)
			if len(m) == 0 {
				delete(b.subs, jobID)
			}
		}
	}

	return ch, unsubscribe
}

// Publish 广播一个事件到对应 jobID 的所有订阅者。
func (b *Broadcaster) Publish(jobID string, ev StreamEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	subs, ok := b.subs[jobID]
	if !ok {
		return
	}

	for ch := range subs {
		select {
		case ch <- ev:
		default:
			// 慢消费保护，避免阻塞调度循环
		}
	}
}
