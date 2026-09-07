package linux_test

import (
	"os"
	"path/filepath"
	"testing"

	"dash/agent/collect"
	"dash/agent/collect/linux"
)

func getTestDataPath(sub string) string {
	wd, _ := os.Getwd()
	// collect_test.go 位于 agent/collect/linux/，testdata 位于 agent/collect/testdata/
	return filepath.Join(wd, "..", "testdata", sub)
}

func TestCPUCollector(t *testing.T) {
	root := filepath.Join(getTestDataPath("normal"), "proc")
	c := linux.NewCPUCollectorWithRoot(root)
	defer c.Close()

	if c.Code() != "cpu" {
		t.Errorf("expected code cpu, got %s", c.Code())
	}
	if c.Tier() != collect.TierFast {
		t.Errorf("expected tier fast, got %v", c.Tier())
	}

	sample := collect.NewSample()

	// 第一次采样，由于没有前值，应记录基线且 CPUPct 为 nil
	if err := c.Collect(sample); err != nil {
		t.Fatalf("first collect failed: %v", err)
	}
	if sample.CPUPct != nil {
		t.Errorf("expected CPUPct to be nil on first sample, got %v", *sample.CPUPct)
	}

	// 第二次采样，传入新数据（模拟时间流逝与 cpu 计数增加）
	tmpDir := t.TempDir()
	statFile := filepath.Join(tmpDir, "stat")
	// 初始值
	_ = os.WriteFile(statFile, []byte("cpu  100 0 100 800 0 0 0 0 0 0\n"), 0644)
	c2 := linux.NewCPUCollectorWithRoot(tmpDir)
	defer c2.Close()

	sample2 := collect.NewSample()
	if err := c2.Collect(sample2); err != nil {
		t.Fatalf("c2 first collect failed: %v", err)
	}
	if sample2.CPUPct != nil {
		t.Errorf("expected nil on first collect")
	}

	// 变化：busy 增加 50 (user+50)，total 增加 100 (idle+50) -> 50%
	_ = os.WriteFile(statFile, []byte("cpu  150 0 100 850 0 0 0 0 0 0\n"), 0644)
	if err := c2.Collect(sample2); err != nil {
		t.Fatalf("c2 second collect failed: %v", err)
	}
	if sample2.CPUPct == nil {
		t.Fatalf("expected CPUPct not nil on second collect")
	}
	if *sample2.CPUPct != 50.0 {
		t.Errorf("expected 50.0%%, got %f", *sample2.CPUPct)
	}
}

func TestMemCollector_Normal(t *testing.T) {
	root := filepath.Join(getTestDataPath("normal"), "proc")
	c := linux.NewMemCollectorWithRoot(root)
	defer c.Close()

	sample := collect.NewSample()
	if err := c.Collect(sample); err != nil {
		t.Fatalf("collect failed: %v", err)
	}

	// MemTotal = 2048000 kB, MemAvailable = 1500000 kB
	// used = 548000 kB = 561152000 bytes
	expectedMemUsed := int64(2048000-1500000) * 1024
	if sample.MemUsed == nil || *sample.MemUsed != expectedMemUsed {
		t.Errorf("expected MemUsed %d, got %v", expectedMemUsed, sample.MemUsed)
	}

	// SwapTotal = 1024000 kB, SwapFree = 512000 kB
	// used = 512000 kB = 524288000 bytes
	expectedSwapUsed := int64(1024000-512000) * 1024
	if sample.SwapUsed == nil || *sample.SwapUsed != expectedSwapUsed {
		t.Errorf("expected SwapUsed %d, got %v", expectedSwapUsed, sample.SwapUsed)
	}
}

func TestMemCollector_AlpineStripped(t *testing.T) {
	// 验证 Alpine 精简版 meminfo：缺少 MemAvailable、Swap 等字段，测试回退公式并不 panic
	root := filepath.Join(getTestDataPath("alpine"), "proc")
	c := linux.NewMemCollectorWithRoot(root)
	defer c.Close()

	sample := collect.NewSample()
	if err := c.Collect(sample); err != nil {
		t.Fatalf("alpine collect failed: %v", err)
	}

	// MemTotal: 1024000, MemFree: 200000, Buffers: 50000, Cached: 300000
	// used = 1024000 - 200000 - 50000 - 300000 = 474000 kB = 485376000 bytes
	expectedMemUsed := int64(474000) * 1024
	if sample.MemUsed == nil || *sample.MemUsed != expectedMemUsed {
		t.Errorf("expected alpine MemUsed %d, got %v", expectedMemUsed, sample.MemUsed)
	}

	// Swap 未在 alpine meminfo 中配置，SwapUsed 应当为 nil
	if sample.SwapUsed != nil {
		t.Errorf("expected SwapUsed to be nil for alpine without swap, got %v", sample.SwapUsed)
	}
}

func TestLoadCollector(t *testing.T) {
	root := filepath.Join(getTestDataPath("normal"), "proc")
	c := linux.NewLoadCollectorWithRoot(root)
	defer c.Close()

	sample := collect.NewSample()
	if err := c.Collect(sample); err != nil {
		t.Fatalf("collect failed: %v", err)
	}

	if sample.Load == nil {
		t.Fatalf("expected Load not nil")
	}
	expected := [3]float64{0.12, 0.09, 0.05}
	if *sample.Load != expected {
		t.Errorf("expected load %v, got %v", expected, *sample.Load)
	}
}

func TestNetCollector(t *testing.T) {
	root := filepath.Join(getTestDataPath("normal"), "proc")
	c := linux.NewNetCollectorWithRoot(root)
	defer c.Close()

	sample := collect.NewSample()
	sample.EnsureSlow() // 测试单网卡维度收集
	if err := c.Collect(sample); err != nil {
		t.Fatalf("collect failed: %v", err)
	}

	if sample.Net == nil {
		t.Fatalf("expected Net not nil")
	}

	// lo 与 docker0 被默认排除，只采集 eth0:
	// eth0 rx = 512345678901, tx = 90123456789
	if sample.Net.TotalDown == nil || *sample.Net.TotalDown != 512345678901 {
		t.Errorf("expected TotalDown 512345678901, got %v", sample.Net.TotalDown)
	}
	if sample.Net.TotalUp == nil || *sample.Net.TotalUp != 90123456789 {
		t.Errorf("expected TotalUp 90123456789, got %v", sample.Net.TotalUp)
	}

	// 验证 NICs 维度被记录
	if sample.Slow == nil || len(sample.Slow.NICs) != 1 {
		t.Fatalf("expected 1 NICEntry, got %+v", sample.Slow)
	}
	nic := sample.Slow.NICs[0]
	if nic.Key != "eth0" || *nic.TotalDown != 512345678901 || *nic.TotalUp != 90123456789 {
		t.Errorf("unexpected NICEntry: %+v", nic)
	}
}

func TestUptimeCollector(t *testing.T) {
	root := filepath.Join(getTestDataPath("normal"), "proc")
	c := linux.NewUptimeCollectorWithRoot(root)
	defer c.Close()

	sample := collect.NewSample()
	if err := c.Collect(sample); err != nil {
		t.Fatalf("collect failed: %v", err)
	}

	// 864000.50 -> 864000
	if sample.UptimeS == nil || *sample.UptimeS != 864000 {
		t.Errorf("expected 864000, got %v", sample.UptimeS)
	}
}

func TestDiskCollector(t *testing.T) {
	root := filepath.Join(getTestDataPath("normal"), "proc")
	c := linux.NewDiskCollectorWithRoot(root)
	defer c.Close()

	// 模拟 Statfs 返回
	c.Statfs = func(path string) (total, used int64, dev uint64, err error) {
		switch path {
		case "/":
			return 40000000000, 10000000000, 1, nil
		case "/data":
			return 60000000000, 20000000000, 2, nil
		default:
			return 0, 0, 0, nil
		}
	}

	sample := collect.NewSample()
	if err := c.Collect(sample); err != nil {
		t.Fatalf("collect failed: %v", err)
	}

	if sample.Slow == nil || sample.Slow.DiskUsed == nil {
		t.Fatalf("expected DiskUsed not nil")
	}

	// 两个挂载点已用求和: 10G + 20G = 30G
	if *sample.Slow.DiskUsed != 30000000000 {
		t.Errorf("expected 30000000000, got %d", *sample.Slow.DiskUsed)
	}

	if len(sample.Slow.Disks) != 2 {
		t.Fatalf("expected 2 disks, got %d", len(sample.Slow.Disks))
	}
}

// TestDiskCollectorBindMount 验证 bind mount 场景下：
// 两个挂载点具有相同设备号，Disks 维度保留两个挂载点，但 disk_used / disk_total 仅计算一次。
func TestDiskCollectorBindMount(t *testing.T) {
	tempDir := t.TempDir()
	mountsContent := `/dev/sda1 / ext4 rw,relatime 0 0
/dev/sda1 /mnt/bind_root ext4 rw,relatime 0 0
`
	if err := os.WriteFile(filepath.Join(tempDir, "mounts"), []byte(mountsContent), 0644); err != nil {
		t.Fatalf("write mounts failed: %v", err)
	}

	diskCol := linux.NewDiskCollectorWithRoot(tempDir)
	defer diskCol.Close()

	diskCol.Statfs = func(path string) (total, used int64, dev uint64, err error) {
		switch path {
		case "/":
			return 40000000000, 10000000000, 100, nil
		case "/mnt/bind_root":
			return 40000000000, 10000000000, 100, nil // 相同设备号 100
		default:
			return 0, 0, 0, nil
		}
	}

	sample := collect.NewSample()
	if err := diskCol.Collect(sample); err != nil {
		t.Fatalf("disk collect failed: %v", err)
	}

	// 维度指标上报 2 个挂载点
	if sample.Slow == nil || len(sample.Slow.Disks) != 2 {
		t.Fatalf("expected 2 disks in Slow.Disks, got %+v", sample.Slow)
	}

	// 汇总指标 disk_used 按设备去重后只算一次 (10G，而不是 20G)
	if sample.Slow.DiskUsed == nil || *sample.Slow.DiskUsed != 10000000000 {
		t.Errorf("expected deduped DiskUsed 10000000000, got %v", sample.Slow.DiskUsed)
	}

	// 验证 facts 的 disk_total 也去重只算一次 (40G，而不是 80G)
	factsCol := linux.NewFactsCollectorWithRoots(tempDir, tempDir, tempDir)
	factsCol.Statfs = diskCol.Statfs
	factsSample := collect.NewSample()
	if err := factsCol.Collect(factsSample); err != nil {
		t.Fatalf("facts collect failed: %v", err)
	}
	if factsSample.Facts == nil || factsSample.Facts.DiskTotal != 40000000000 {
		t.Errorf("expected facts DiskTotal 40000000000, got %v", factsSample.Facts.DiskTotal)
	}
}

func TestProcCollector(t *testing.T) {
	root := filepath.Join(getTestDataPath("normal"), "proc")
	c := linux.NewProcCollectorWithRoot(root)

	sample := collect.NewSample()
	if err := c.Collect(sample); err != nil {
		t.Fatalf("collect failed: %v", err)
	}

	// testdata 包含 1, 2, 100 三个进程目录和一个 sys_dir 非数字目录
	if sample.Slow == nil || sample.Slow.ProcCount == nil {
		t.Fatalf("expected ProcCount not nil")
	}
	if *sample.Slow.ProcCount != 3 {
		t.Errorf("expected ProcCount 3, got %d", *sample.Slow.ProcCount)
	}
}

func TestConnsCollector(t *testing.T) {
	root := filepath.Join(getTestDataPath("normal"), "proc")
	c := linux.NewConnsCollectorWithRoot(root)
	defer c.Close()

	sample := collect.NewSample()
	if err := c.Collect(sample); err != nil {
		t.Fatalf("collect failed: %v", err)
	}

	if sample.Slow == nil || sample.Slow.TCPCount == nil || sample.Slow.UDPCount == nil {
		t.Fatalf("expected TCPCount and UDPCount not nil")
	}

	// 读取 sockstat (TCP: inuse 12, UDP: inuse 5) + sockstat6 (TCP6: inuse 5, UDP6: inuse 1)
	// tcp: 12 + 5 = 17
	if *sample.Slow.TCPCount != 17 {
		t.Errorf("expected TCPCount 17, got %d", *sample.Slow.TCPCount)
	}
	// udp: 5 + 1 = 6
	if *sample.Slow.UDPCount != 6 {
		t.Errorf("expected UDPCount 6, got %d", *sample.Slow.UDPCount)
	}
}

// TestConnsCollectorFallback 验证缺少 sockstat 时回退到 /proc/net/tcp 统计行数。
func TestConnsCollectorFallback(t *testing.T) {
	tempDir := t.TempDir()
	netDir := filepath.Join(tempDir, "net")
	if err := os.MkdirAll(netDir, 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	// 拷贝 normal 目录下的 tcp, tcp6, udp 到临时目录（不拷贝 sockstat）
	srcNet := filepath.Join(getTestDataPath("normal"), "proc", "net")
	for _, f := range []string{"tcp", "tcp6", "udp"} {
		data, err := os.ReadFile(filepath.Join(srcNet, f))
		if err != nil {
			t.Fatalf("read %s failed: %v", f, err)
		}
		if err := os.WriteFile(filepath.Join(netDir, f), data, 0644); err != nil {
			t.Fatalf("write %s failed: %v", f, err)
		}
	}

	c := linux.NewConnsCollectorWithRoot(tempDir)
	defer c.Close()

	sample := collect.NewSample()
	if err := c.Collect(sample); err != nil {
		t.Fatalf("collect fallback failed: %v", err)
	}

	if sample.Slow == nil || sample.Slow.TCPCount == nil || sample.Slow.UDPCount == nil {
		t.Fatalf("expected TCPCount and UDPCount not nil")
	}

	// 回退统计行数：tcp (2) + tcp6 (1) = 3, udp (1) = 1
	if *sample.Slow.TCPCount != 3 {
		t.Errorf("expected fallback TCPCount 3, got %d", *sample.Slow.TCPCount)
	}
	if *sample.Slow.UDPCount != 1 {
		t.Errorf("expected fallback UDPCount 1, got %d", *sample.Slow.UDPCount)
	}
}

func TestFactsCollector(t *testing.T) {
	normalPath := getTestDataPath("normal")
	procRoot := filepath.Join(normalPath, "proc")
	etcRoot := filepath.Join(normalPath, "etc")
	sysRoot := filepath.Join(normalPath, "sys")

	c := linux.NewFactsCollectorWithRoots(procRoot, etcRoot, sysRoot)
	c.Statfs = func(path string) (total, used int64, dev uint64, err error) {
		if path == "/" {
			return 42000000000, 10000000000, 1, nil
		}
		return 0, 0, 0, nil
	}

	sample := collect.NewSample()
	if err := c.Collect(sample); err != nil {
		t.Fatalf("collect failed: %v", err)
	}

	if sample.Facts == nil {
		t.Fatalf("expected Facts not nil")
	}

	facts := sample.Facts
	if facts.OSName != "debian" {
		t.Errorf("expected OSName debian, got %s", facts.OSName)
	}
	if facts.OSVersion != "12" {
		t.Errorf("expected OSVersion 12, got %s", facts.OSVersion)
	}
	if facts.CPUModel != "Intel(R) Xeon(R) Platinum" {
		t.Errorf("expected CPUModel 'Intel(R) Xeon(R) Platinum', got %s", facts.CPUModel)
	}
	if facts.CPUCores != 2 {
		t.Errorf("expected 2 cores, got %d", facts.CPUCores)
	}
	if facts.CPUThreads != 2 {
		t.Errorf("expected 2 threads, got %d", facts.CPUThreads)
	}
	if facts.MemTotal != 2048000*1024 {
		t.Errorf("expected MemTotal %d, got %d", 2048000*1024, facts.MemTotal)
	}
	if facts.SwapTotal != 1024000*1024 {
		t.Errorf("expected SwapTotal %d, got %d", 1024000*1024, facts.SwapTotal)
	}
	if facts.BootAtMs != 1757222400000 {
		t.Errorf("expected BootAtMs 1757222400000, got %d", facts.BootAtMs)
	}
	if facts.FactsHash == "" {
		t.Errorf("expected FactsHash not empty")
	}

	// 验收关键规则：boot_at_ms 不参与 facts_hash 计算
	factsCopy := *facts
	factsCopy.BootAtMs = 9999999999999
	newHash := linux.ComputeFactsHash(factsCopy)
	if newHash != facts.FactsHash {
		t.Errorf("boot_at_ms must NOT affect facts_hash! old=%s, new=%s", facts.FactsHash, newHash)
	}
}

// 验收项 4 演示：新增一个采集项只改两处（定义 + Register），主循环不需要任何 switch 或改动
type dummyCollector struct{}

func (d *dummyCollector) Code() string                    { return "custom_dummy" }
func (d *dummyCollector) Tier() collect.Tier              { return collect.TierFast }
func (d *dummyCollector) Collect(s *collect.Sample) error { return nil }

func TestRegistryExtension(t *testing.T) {
	beforeCount := len(collect.Collectors())

	// 注册新采集项
	collect.Register(&dummyCollector{})
	afterCount := len(collect.Collectors())

	if afterCount != beforeCount+1 {
		t.Errorf("expected collector count %d, got %d", beforeCount+1, afterCount)
	}

	found := false
	for _, c := range collect.Collectors() {
		if c.Code() == "custom_dummy" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("custom_dummy collector not found in registry")
	}
}
