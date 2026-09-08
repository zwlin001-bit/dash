package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"dash/agent/transport"
	"dash/internal/protocol"
)

// mockTransport 模拟传输层供单元测试使用。
type mockTransport struct {
	mu             sync.Mutex
	state          transport.ConnState
	sent           []mockSentMsg
	callHandler    func(method string, params any) (json.RawMessage, error)
	serverMsgHandler transport.Handler
	closed         bool
}

type mockSentMsg struct {
	Method string
	Params any
}

func newMockTransport() *mockTransport {
	return &mockTransport{
		state: transport.StateWSConnected,
	}
}

func (m *mockTransport) Start(ctx context.Context) error {
	return nil
}

func (m *mockTransport) Send(method string, params any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, mockSentMsg{Method: method, Params: params})
	return nil
}

func (m *mockTransport) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	m.mu.Lock()
	handler := m.callHandler
	m.mu.Unlock()
	if handler != nil {
		return handler(method, params)
	}
	return json.RawMessage(`{}`), nil
}

func (m *mockTransport) OnServerMessage(h transport.Handler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.serverMsgHandler = h
}

func (m *mockTransport) State() transport.ConnState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *mockTransport) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *mockTransport) getSent() []mockSentMsg {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := make([]mockSentMsg, len(m.sent))
	copy(res, m.sent)
	return res
}

func (m *mockTransport) dispatchDownstream(method string, params json.RawMessage) (any, error) {
	m.mu.Lock()
	h := m.serverMsgHandler
	m.mu.Unlock()
	if h != nil {
		return h(method, params)
	}
	return nil, transport.ErrMethodNotFound
}

func floatPtr(v float64) *float64 { return &v }
func int64Ptr(v int64) *int64     { return &v }
func int32Ptr(v int32) *int32     { return &v }
func intPtr(v int) *int           { return &v }
func boolPtr(v bool) *bool        { return &v }

// TestMetricsEncoder 验证复用缓冲编码器生成的 JSON 与标准反序列化结果完全一致。
func TestMetricsEncoder(t *testing.T) {
	encoder := NewMetricsEncoder(2048)

	load := [3]float64{0.12, 0.09, 0.05}
	params := &protocol.MetricsParams{
		TsMs:     1757222405000,
		CPUPct:   floatPtr(3.5),
		MemUsed:  int64Ptr(412000000),
		SwapUsed: int64Ptr(0),
		Load:     &load,
		Net: &protocol.NetReport{
			UpBps:     int64Ptr(12000),
			DownBps:   int64Ptr(84000),
			TotalUp:   int64Ptr(90123456789),
			TotalDown: int64Ptr(0),
		},
		UptimeS: int64Ptr(864000),
		Slow: &protocol.SlowReport{
			DiskUsed:  int64Ptr(12000000000),
			ProcCount: int32Ptr(92),
			TCPCount:  int32Ptr(41),
			UDPCount:  int32Ptr(6),
			Disks: []protocol.DiskEntry{
				{Key: "/", Used: int64Ptr(12000000000), Total: int64Ptr(42000000000)},
			},
			NICs: []protocol.NICEntry{
				{Key: "eth0", TotalUp: int64Ptr(90123456789), TotalDown: int64Ptr(512345678901)},
			},
		},
	}

	encoded := encoder.Encode(params)
	if len(encoded) == 0 {
		t.Fatalf("expected non-empty encoded payload")
	}

	var decoded protocol.MetricsParams
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal encoded json failed: %v\nJSON: %s", err, string(encoded))
	}

	if decoded.TsMs != params.TsMs ||
		*decoded.CPUPct != *params.CPUPct ||
		*decoded.MemUsed != *params.MemUsed ||
		*decoded.SwapUsed != *params.SwapUsed ||
		*decoded.UptimeS != *params.UptimeS {
		t.Fatalf("decoded metrics fields mismatch: got %+v, want %+v", decoded, params)
	}

	if decoded.Slow == nil || *decoded.Slow.DiskUsed != *params.Slow.DiskUsed ||
		*decoded.Slow.ProcCount != *params.Slow.ProcCount ||
		*decoded.Slow.TCPCount != *params.Slow.TCPCount ||
		*decoded.Slow.UDPCount != *params.Slow.UDPCount {
		t.Fatalf("decoded slow metrics fields mismatch: got %+v, want %+v", decoded.Slow, params.Slow)
	}
}

// TestStateStore_AtomicAndRateLimit 验证状态文件原子替换与写入限频约束。
func TestStateStore_AtomicAndRateLimit(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "dash-agent-state-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	stateFile := filepath.Join(tempDir, "state.json")
	store := NewStateStore(stateFile)
	store.SetWriteInterval(50 * time.Millisecond)

	// 1. 初始化并加载（文件尚不存在）
	if err := store.Load(); err != nil {
		t.Fatalf("load initial state: %v", err)
	}
	if store.FactsHash() != "" {
		t.Fatalf("expected empty facts hash, got %s", store.FactsHash())
	}

	// 2. 更新 facts_hash 并刷新
	changed := store.UpdateFactsHash("hash-001")
	if !changed {
		t.Fatalf("expected true for first update")
	}

	if err := store.Flush(); err != nil {
		t.Fatalf("flush state: %v", err)
	}

	// 验证文件存在且内容正确
	data, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatalf("read flushed state file: %v", err)
	}
	var loaded StateData
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("unmarshal state file: %v", err)
	}
	if loaded.FactsHash != "hash-001" {
		t.Fatalf("expected hash-001, got %s", loaded.FactsHash)
	}

	// 3. 验证限频：紧接着修改并在限频时间内 FlushIfDue，不会执行写入
	store.UpdateFactsHash("hash-002")
	if err := store.FlushIfDue(); err != nil {
		t.Fatalf("flush if due: %v", err)
	}
	// 文件内容应仍为 hash-001
	data, _ = os.ReadFile(stateFile)
	_ = json.Unmarshal(data, &loaded)
	if loaded.FactsHash != "hash-001" {
		t.Fatalf("expected write to be rate-limited, but content changed to %s", loaded.FactsHash)
	}

	// 4. 超过限频时间后 FlushIfDue 成功写入
	time.Sleep(60 * time.Millisecond)
	if err := store.FlushIfDue(); err != nil {
		t.Fatalf("flush if due after interval: %v", err)
	}
	data, _ = os.ReadFile(stateFile)
	_ = json.Unmarshal(data, &loaded)
	if loaded.FactsHash != "hash-002" {
		t.Fatalf("expected hash-002 after interval expired, got %s", loaded.FactsHash)
	}
}

// TestRuntime_ServerConfigUpdateWithoutReconnect 验收项 4：
// 服务端下发新的 interval_fast_s，agent 立即生效，不需要重连。
func TestRuntime_ServerConfigUpdateWithoutReconnect(t *testing.T) {
	mockTr := newMockTransport()
	cfg := &Config{
		IntervalFastS:     5,
		IntervalSlowS:     60,
		FactsMaxIntervalS: 1800,
		CollectConns:      true,
	}

	rt, err := New(cfg, "test-version", WithTransport(mockTr))
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- rt.Run(ctx)
	}()

	// 确保主循环已启动
	time.Sleep(50 * time.Millisecond)

	// 下发 server.config 通知，将 fast 间隔修改为 1 秒
	newFast := 1
	configParams := protocol.ServerConfigParams{
		IntervalFastS: &newFast,
	}
	paramBytes, _ := json.Marshal(configParams)
	_, err = mockTr.dispatchDownstream(protocol.MethodServerConfig, paramBytes)
	if err != nil {
		t.Fatalf("dispatch server.config error: %v", err)
	}

	// 等待 2.2 秒，若 1 秒周期生效，应至少收到 2 次 metrics 上报
	initialCount := rt.MetricsReportCount()
	time.Sleep(2200 * time.Millisecond)
	afterCount := rt.MetricsReportCount()

	if afterCount-initialCount < 2 {
		t.Fatalf("expected at least 2 metrics reports in 2.2s after interval_fast changed to 1s, got %d", afterCount-initialCount)
	}

	// 验证期间 Transport 始终保持连接，无重连/Close
	if mockTr.closed {
		t.Fatalf("transport should not be closed or reconnected")
	}

	cancel()
	<-runErrCh
}

// TestRuntime_FactsReportCount 验收项 3：
// facts 不变时，启动那一次上报 + 兜底上报。
func TestRuntime_FactsReportCount(t *testing.T) {
	mockTr := newMockTransport()
	mockTr.callHandler = func(method string, params any) (json.RawMessage, error) {
		if method == protocol.MethodAgentHello {
			// 服务端要求上报 facts
			return json.RawMessage(`{"node_id":"01JBX001","interval_fast_s":10,"interval_slow_s":60,"facts_max_interval_s":1,"need_facts":true}`), nil
		}
		return json.RawMessage(`{}`), nil
	}

	tempDir, err := os.MkdirTemp("", "dash-agent-facts-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfg := &Config{
		StateFile:         filepath.Join(tempDir, "state.json"),
		IntervalFastS:     10,
		IntervalSlowS:     60,
		FactsMaxIntervalS: 1, // 单元测试中设为 1 秒模拟兜底
		CollectConns:      true,
	}

	rt, err := New(cfg, "test-version", WithTransport(mockTr))
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = rt.Run(ctx)
	}()

	// 触发一次连接就绪
	rt.TriggerOnWSConnected()

	// 启动后应上报 1 次 facts
	time.Sleep(100 * time.Millisecond)
	if rt.FactsReportCount() != 1 {
		t.Fatalf("expected 1 facts report on start, got %d", rt.FactsReportCount())
	}

	// 等待 2.2 秒（覆盖 2 次 1s 的 facts 兜底定时器周期）
	time.Sleep(2200 * time.Millisecond)

	// 总上报次数应为 1 (启动) + 2 (兜底) = 3 次
	cnt := rt.FactsReportCount()
	if cnt != 3 {
		t.Fatalf("expected exactly 3 facts reports (1 initial + 2 fallback), got %d", cnt)
	}

	cancel()
}

// TestRuntime_ConfigPrecedence 验证命令行参数 > 环境变量 > 配置文件 > 默认值优先级。
func TestRuntime_ConfigPrecedence(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "dash-agent-cfg-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	configFile := filepath.Join(tempDir, "config.json")
	fileContent := `{
		"endpoint": "https://file.dash.dev",
		"token": "file-token",
		"interval_fast_s": 15,
		"interval_slow_s": 90,
		"collect_conns": false
	}`
	if err := os.WriteFile(configFile, []byte(fileContent), 0600); err != nil {
		t.Fatalf("write test config: %v", err)
	}

	// 设置环境变量
	_ = os.Setenv("DASH_AGENT_CONFIG", configFile)
	defer os.Unsetenv("DASH_AGENT_CONFIG")
	_ = os.Setenv("DASH_AGENT_TOKEN", "env-token")
	defer os.Unsetenv("DASH_AGENT_TOKEN")
	_ = os.Setenv("DASH_AGENT_INTERVAL_FAST_S", "8")
	defer os.Unsetenv("DASH_AGENT_INTERVAL_FAST_S")

	// 命令行显式参数
	flagOverrides := &Config{
		Endpoint:      "https://cli.dash.dev",
		IntervalFastS: 3,
	}
	setFlags := map[string]bool{
		"endpoint":        true,
		"interval-fast-s": true,
	}

	cfg, err := LoadConfig(flagOverrides, setFlags)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	// 1. Endpoint: CLI 覆盖了 File
	if cfg.Endpoint != "https://cli.dash.dev" {
		t.Fatalf("expected CLI endpoint, got %s", cfg.Endpoint)
	}
	// 2. Token: ENV 覆盖了 File
	if cfg.Token != "env-token" {
		t.Fatalf("expected ENV token, got %s", cfg.Token)
	}
	// 3. IntervalFastS: CLI (3) 覆盖了 ENV (8) 和 File (15)
	if cfg.IntervalFastS != 3 {
		t.Fatalf("expected CLI interval_fast_s 3, got %d", cfg.IntervalFastS)
	}
	// 4. IntervalSlowS: 来自 File (90)
	if cfg.IntervalSlowS != 90 {
		t.Fatalf("expected File interval_slow_s 90, got %d", cfg.IntervalSlowS)
	}
	// 5. CollectConns: 来自 File (false)
	if cfg.CollectConns != false {
		t.Fatalf("expected File collect_conns false, got %v", cfg.CollectConns)
	}
	// 6. Transport 默认值: "auto"
	if cfg.Transport != "auto" {
		t.Fatalf("expected default transport auto, got %s", cfg.Transport)
	}
}

// TestRuntime_TransportAndNodeConfigPrecedence 严格校验 105.md 判据 5：
// 三层配置来源（配置文件 / 环境变量 / 命令行 flag）各验一次，优先级（命令行 > 环境变量 > 配置文件 > 默认值）不变。
func TestRuntime_TransportAndNodeConfigPrecedence(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "dash-agent-cfg-precedence-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// --- 阶段 1：零输入，验证默认值 ---
	cfgDef, err := LoadConfig(nil, nil)
	if err != nil {
		t.Fatalf("load default config: %v", err)
	}
	if cfgDef.Transport != "auto" {
		t.Fatalf("expected default Transport 'auto', got %q", cfgDef.Transport)
	}
	if cfgDef.NodeID != "" {
		t.Fatalf("expected default NodeID '', got %q", cfgDef.NodeID)
	}

	// --- 阶段 2：配置文件覆盖默认值 ---
	configFile := filepath.Join(tempDir, "config.json")
	fileJSON := `{
		"transport": "http",
		"node_id": "file-node-01"
	}`
	if err := os.WriteFile(configFile, []byte(fileJSON), 0600); err != nil {
		t.Fatalf("write file config: %v", err)
	}

	setFileFlags := map[string]bool{"config": true}
	flagConfigFile := &Config{ConfigFile: configFile}
	cfgFile, err := LoadConfig(flagConfigFile, setFileFlags)
	if err != nil {
		t.Fatalf("load file config: %v", err)
	}
	if cfgFile.Transport != "http" {
		t.Fatalf("expected file Transport 'http', got %q", cfgFile.Transport)
	}
	if cfgFile.NodeID != "file-node-01" {
		t.Fatalf("expected file NodeID 'file-node-01', got %q", cfgFile.NodeID)
	}

	// --- 阶段 3：环境变量覆盖配置文件 ---
	_ = os.Setenv("DASH_AGENT_CONFIG", configFile)
	defer os.Unsetenv("DASH_AGENT_CONFIG")
	_ = os.Setenv("DASH_AGENT_TRANSPORT", "auto")
	defer os.Unsetenv("DASH_AGENT_TRANSPORT")
	_ = os.Setenv("DASH_AGENT_NODE_ID", "env-node-02")
	defer os.Unsetenv("DASH_AGENT_NODE_ID")

	cfgEnv, err := LoadConfig(nil, nil)
	if err != nil {
		t.Fatalf("load env config: %v", err)
	}
	if cfgEnv.Transport != "auto" {
		t.Fatalf("expected env Transport 'auto' to override file, got %q", cfgEnv.Transport)
	}
	if cfgEnv.NodeID != "env-node-02" {
		t.Fatalf("expected env NodeID 'env-node-02' to override file, got %q", cfgEnv.NodeID)
	}

	// --- 阶段 4：命令行 flag 覆盖环境变量与配置文件 ---
	flagOverrides := &Config{
		Transport: "http",
		NodeID:    "cli-node-03",
	}
	setFlags := map[string]bool{
		"transport": true,
		"node-id":   true,
	}

	cfgCli, err := LoadConfig(flagOverrides, setFlags)
	if err != nil {
		t.Fatalf("load cli config: %v", err)
	}
	if cfgCli.Transport != "http" {
		t.Fatalf("expected cli Transport 'http' to override env, got %q", cfgCli.Transport)
	}
	if cfgCli.NodeID != "cli-node-03" {
		t.Fatalf("expected cli NodeID 'cli-node-03' to override env, got %q", cfgCli.NodeID)
	}
}

// TestMetricsEncoder_ZeroAllocations 验证稳态下复用编码器零内存分配。
func BenchmarkMetricsEncoder(b *testing.B) {
	encoder := NewMetricsEncoder(2048)
	load := [3]float64{0.12, 0.09, 0.05}
	params := &protocol.MetricsParams{
		TsMs:     1757222405000,
		CPUPct:   floatPtr(3.5),
		MemUsed:  int64Ptr(412000000),
		SwapUsed: int64Ptr(0),
		Load:     &load,
		Net: &protocol.NetReport{
			UpBps:     int64Ptr(12000),
			DownBps:   int64Ptr(84000),
			TotalUp:   int64Ptr(90123456789),
			TotalDown: int64Ptr(0),
		},
		UptimeS: int64Ptr(864000),
	}

	// 预热
	_ = encoder.Encode(params)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = encoder.Encode(params)
	}
}
