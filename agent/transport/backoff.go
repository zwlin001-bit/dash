package transport

import (
	"math/rand"
	"sync"
	"time"
)

// DefaultBackoffSequence 是默认退避序列：1s → 2s → 4s → 8s → 16s → 32s → 60s（封顶）。
var DefaultBackoffSequence = []time.Duration{
	1 * time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
	16 * time.Second,
	32 * time.Second,
	60 * time.Second,
}

// Backoff 管理重连退避序列及 ±20% 随机抖动（因子 0.8 ~ 1.2）。
type Backoff struct {
	mu       sync.Mutex
	step     int
	sequence []time.Duration
	rng      *rand.Rand
}

// NewBackoff 创建一个默认或自定义序列的 Backoff 实例。
func NewBackoff(sequence ...time.Duration) *Backoff {
	seq := DefaultBackoffSequence
	if len(sequence) > 0 {
		seq = make([]time.Duration, len(sequence))
		copy(seq, sequence)
	}
	return &Backoff{
		sequence: seq,
		rng:      rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// NewCustomBackoff 支持指定种子/随机源以用于确定性测试。
func NewCustomBackoff(sequence []time.Duration, rng *rand.Rand) *Backoff {
	seq := DefaultBackoffSequence
	if len(sequence) > 0 {
		seq = make([]time.Duration, len(sequence))
		copy(seq, sequence)
	}
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	return &Backoff{
		sequence: seq,
		rng:      rng,
	}
}

// Next 返回下一次重连等待时长（基准时长乘 0.8 ~ 1.2 随机因子），并步进至下一阶（封顶在序列末尾）。
func (b *Backoff) Next() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.sequence) == 0 {
		return 0
	}

	idx := b.step
	if idx >= len(b.sequence) {
		idx = len(b.sequence) - 1
	}
	base := b.sequence[idx]

	if b.step < len(b.sequence)-1 {
		b.step++
	}

	// 0.8 ~ 1.2 随机抖动因子 (±20%)
	factor := 0.8 + b.rng.Float64()*0.4
	delay := time.Duration(float64(base) * factor)
	return delay
}

// Reset 重置退避阶数到 0。
func (b *Backoff) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.step = 0
}

// CurrentStep 返回当前所在的退避阶数。
func (b *Backoff) CurrentStep() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.step
}
