package collect

import (
	"sync"
)

var (
	mu         sync.RWMutex
	collectors []Collector
)

// Register 注册一个采集器。主循环只认注册表。
func Register(c Collector) {
	mu.Lock()
	defer mu.Unlock()
	collectors = append(collectors, c)
}

// Collectors 返回所有已注册采集器的切片副本。
func Collectors() []Collector {
	mu.RLock()
	defer mu.RUnlock()
	res := make([]Collector, len(collectors))
	copy(res, collectors)
	return res
}

// CollectorsByTier 返回指定 Tier 的采集器切片副本。
func CollectorsByTier(t Tier) []Collector {
	mu.RLock()
	defer mu.RUnlock()
	var res []Collector
	for _, c := range collectors {
		if c.Tier() == t {
			res = append(res, c)
		}
	}
	return res
}

// CollectTier 依次执行指定 Tier 的所有已注册采集器。
func CollectTier(t Tier, sample *Sample) error {
	mu.RLock()
	defer mu.RUnlock()
	for _, c := range collectors {
		if c.Tier() == t {
			_ = c.Collect(sample)
		}
	}
	return nil
}

// ResetRegistry 清空注册表（测试使用）。
func ResetRegistry() {
	mu.Lock()
	defer mu.Unlock()
	collectors = nil
}
