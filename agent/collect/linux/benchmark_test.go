package linux_test

import (
	"path/filepath"
	"testing"

	"dash/agent/collect"
	"dash/agent/collect/linux"
)

// BenchmarkFastCollection 衡量稳态下一轮 fast 档全部采集器的内存分配。
// 验收标准：allocs/op 必须是个位数（<= 9）。
func BenchmarkFastCollection(b *testing.B) {
	root := filepath.Join(getTestDataPath("normal"), "proc")

	cpuCol := linux.NewCPUCollectorWithRoot(root)
	defer cpuCol.Close()

	memCol := linux.NewMemCollectorWithRoot(root)
	defer memCol.Close()

	loadCol := linux.NewLoadCollectorWithRoot(root)
	defer loadCol.Close()

	netCol := linux.NewNetCollectorWithRoot(root)
	defer netCol.Close()

	uptimeCol := linux.NewUptimeCollectorWithRoot(root)
	defer uptimeCol.Close()

	sample := collect.NewSample()

	// 预热一轮，建立前值基线
	_ = cpuCol.Collect(sample)
	_ = memCol.Collect(sample)
	_ = loadCol.Collect(sample)
	_ = netCol.Collect(sample)
	_ = uptimeCol.Collect(sample)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		sample.Reset(collect.TierFast)
		_ = cpuCol.Collect(sample)
		_ = memCol.Collect(sample)
		_ = loadCol.Collect(sample)
		_ = netCol.Collect(sample)
		_ = uptimeCol.Collect(sample)
	}
}

// BenchmarkCollectTierFast 通过注册表调用全量 Fast 采集。
func BenchmarkCollectTierFast(b *testing.B) {
	sample := collect.NewSample()

	// 预热一轮建立基线
	_ = collect.CollectTier(collect.TierFast, sample)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		sample.Reset(collect.TierFast)
		_ = collect.CollectTier(collect.TierFast, sample)
	}
}
