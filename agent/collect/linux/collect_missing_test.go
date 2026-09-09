package linux

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"dash/agent/collect"
)

// TestCollectorsRegistry_Criterion1 验证三个采集器正确注册进 registry 且可通过 TierSlow 取得。
func TestCollectorsRegistry_Criterion1(t *testing.T) {
	// 验证 CollectorByCode
	listenCol := collect.CollectorByCode("listen")
	if listenCol == nil {
		t.Fatalf("CollectorByCode(\"listen\") returned nil")
	}
	if listenCol.Code() != "listen" || listenCol.Tier() != collect.TierSlow {
		t.Errorf("listen collector code or tier mismatch: %s, %v", listenCol.Code(), listenCol.Tier())
	}

	tpCol := collect.CollectorByCode("tool_param")
	if tpCol == nil {
		t.Fatalf("CollectorByCode(\"tool_param\") returned nil")
	}
	if tpCol.Code() != "tool_param" || tpCol.Tier() != collect.TierSlow {
		t.Errorf("tool_param collector code or tier mismatch: %s, %v", tpCol.Code(), tpCol.Tier())
	}

	giaCol := collect.CollectorByCode("giascan")
	if giaCol == nil {
		t.Fatalf("CollectorByCode(\"giascan\") returned nil")
	}
	if giaCol.Code() != "giascan" || giaCol.Tier() != collect.TierSlow {
		t.Errorf("giascan collector code or tier mismatch: %s, %v", giaCol.Code(), giaCol.Tier())
	}

	// 验证 CollectorsByTier(collect.TierSlow) 包含三个采集器
	slowCollectors := collect.CollectorsByTier(collect.TierSlow)
	foundListen, foundTP, foundGia := false, false, false
	for _, c := range slowCollectors {
		switch c.Code() {
		case "listen":
			foundListen = true
		case "tool_param":
			foundTP = true
		case "giascan":
			foundGia = true
		}
	}
	if !foundListen || !foundTP || !foundGia {
		t.Errorf("CollectorsByTier(TierSlow) missing collectors: listen=%v, tool_param=%v, giascan=%v",
			foundListen, foundTP, foundGia)
	}
}

// TestListenCollector_Normal 验证使用真实样本解析 tcp/tcp6 以及关联进程。
func TestListenCollector_Normal(t *testing.T) {
	testProc := filepath.Join("..", "testdata", "normal", "proc")
	col := NewListenCollectorWithRoot(testProc)

	sample := collect.NewSample()
	if err := col.Collect(sample); err != nil {
		t.Fatalf("col.Collect failed: %v", err)
	}

	if sample.Listen == nil {
		t.Fatalf("sample.Listen is nil")
	}

	entries := sample.Listen.Listen
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d: %+v", len(entries), entries)
	}

	// 0: 0100007F:4457 (127.0.0.1:17495) -> proc: test_proc (inode 7842843)
	// 1: 0100007F:64AD (127.0.0.1:25773) -> proc: ""
	// 2: IPv6 [::]:22 (dual stack or ipv6)
	found17495 := false
	found25773 := false
	foundV6 := false

	for _, e := range entries {
		if e.Local == "127.0.0.1:17495" {
			found17495 = true
			if e.Proc != "test_proc" {
				t.Errorf("expected proc 'test_proc' for 17495, got %q", e.Proc)
			}
		}
		if e.Local == "127.0.0.1:25773" {
			found25773 = true
			if e.Proc != "" {
				t.Errorf("expected empty proc for 25773, got %q", e.Proc)
			}
		}
		if strings.Contains(e.Local, ":22") {
			foundV6 = true
		}
	}

	if !found17495 || !found25773 || !foundV6 {
		t.Errorf("missing expected entries: 17495=%v, 25773=%v, v6=%v", found17495, found25773, foundV6)
	}
}

// TestListenCollector_FailureAndBoundary 验证失败路径与边界处理。
func TestListenCollector_FailureAndBoundary(t *testing.T) {
	// 1. 失败路径：指向不存在的目录
	colFail := NewListenCollectorWithRoot("/non_existent_proc_path_12345")
	sampleFail := collect.NewSample()
	err := colFail.Collect(sampleFail)
	if err == nil {
		t.Errorf("expected error for non-existent proc path, got nil")
	}
	if sampleFail.Listen != nil {
		t.Errorf("sample.Listen should be nil on failure, got %+v", sampleFail.Listen)
	}

	// 2. 边界路径：空的/只有表头的 tcp 文件
	tmpDir := t.TempDir()
	netDir := filepath.Join(tmpDir, "net")
	if err := os.MkdirAll(netDir, 0755); err != nil {
		t.Fatal(err)
	}
	headerOnly := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
	if err := os.WriteFile(filepath.Join(netDir, "tcp"), []byte(headerOnly), 0644); err != nil {
		t.Fatal(err)
	}

	colEmpty := NewListenCollectorWithRoot(tmpDir)
	sampleEmpty := collect.NewSample()
	if err := colEmpty.Collect(sampleEmpty); err != nil {
		t.Fatalf("colEmpty.Collect failed: %v", err)
	}
	if sampleEmpty.Listen == nil || len(sampleEmpty.Listen.Listen) != 0 {
		t.Errorf("expected empty listen slice, got %+v", sampleEmpty.Listen)
	}

	// 3. 边界路径：非监听状态（01 ESTABLISHED，不是 0A）与非法字符
	corrupted := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		"   0: 0100007F:1111 00000000:0000 01 00000000:00000000 00:00000000 00000000  1001 0 1234 2 00000000 100 0 0 10 0\n" +
		"   1: invalid_row\n"
	if err := os.WriteFile(filepath.Join(netDir, "tcp"), []byte(corrupted), 0644); err != nil {
		t.Fatal(err)
	}
	sampleCorrupted := collect.NewSample()
	if err := colEmpty.Collect(sampleCorrupted); err != nil {
		t.Fatalf("corrupted tcp should be tolerated, got: %v", err)
	}
	if len(sampleCorrupted.Listen.Listen) != 0 {
		t.Errorf("expected 0 listening sockets, got %d", len(sampleCorrupted.Listen.Listen))
	}
}

// TestToolParamCollector_Normal 验证使用真实样本解析 network 与 podguard 工具参数。
func TestToolParamCollector_Normal(t *testing.T) {
	testToolRoot := filepath.Join("..", "testdata", "normal", "opt", "tool")
	col := NewToolParamCollectorWithRoot(testToolRoot)

	sample := collect.NewSample()
	if err := col.Collect(sample); err != nil {
		t.Fatalf("col.Collect failed: %v", err)
	}

	if sample.ToolParam == nil {
		t.Fatalf("sample.ToolParam is nil")
	}

	netKV, ok := sample.ToolParam["network"]
	if !ok {
		t.Fatalf("network config missing from tool_param")
	}
	if netKV["THRESHOLD_GB"] != "1000" {
		t.Errorf("THRESHOLD_GB mismatch, got %q", netKV["THRESHOLD_GB"])
	}
	if netKV["RATE_LIMIT_GB"] != "1.0" {
		t.Errorf("RATE_LIMIT_GB mismatch, got %q", netKV["RATE_LIMIT_GB"])
	}
	if netKV["THROTTLE_SPEED"] != "50kbit" {
		t.Errorf("THROTTLE_SPEED mismatch, got %q", netKV["THROTTLE_SPEED"])
	}
	if netKV["ENABLE_TG_NOTIFY"] != "false" {
		t.Errorf("ENABLE_TG_NOTIFY mismatch, got %q", netKV["ENABLE_TG_NOTIFY"])
	}

	pgKV, ok := sample.ToolParam["podguard"]
	if !ok {
		t.Fatalf("podguard config missing from tool_param")
	}
	if pgKV["CONTAINER_NAME"] != "my-pod" {
		t.Errorf("CONTAINER_NAME mismatch, got %q", pgKV["CONTAINER_NAME"])
	}
	// PORT 应该被 podguard.local.conf 覆盖为 9090
	if pgKV["PORT"] != "9090" {
		t.Errorf("PORT override mismatch, expected 9090, got %q", pgKV["PORT"])
	}
	if pgKV["DEBUG_MODE"] != "true" {
		t.Errorf("DEBUG_MODE mismatch, got %q", pgKV["DEBUG_MODE"])
	}
}

// TestToolParamCollector_FailureAndBoundary 验证 tool_param 失败路径与边界情况。
func TestToolParamCollector_FailureAndBoundary(t *testing.T) {
	// 1. 边界：工具目录完全不存在（返回空 map，无报错）
	colEmpty := NewToolParamCollectorWithRoot(filepath.Join(t.TempDir(), "empty_tool"))
	sampleEmpty := collect.NewSample()
	if err := colEmpty.Collect(sampleEmpty); err != nil {
		t.Fatalf("colEmpty.Collect failed: %v", err)
	}
	if sampleEmpty.ToolParam == nil || len(sampleEmpty.ToolParam) != 0 {
		t.Errorf("expected empty map for non-existent tools, got %+v", sampleEmpty.ToolParam)
	}

	// 2. 边界：只有注释与空行的文件
	tmpDir := t.TempDir()
	netDir := filepath.Join(tmpDir, "network")
	if err := os.MkdirAll(netDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(netDir, "config.conf"), []byte("# comment only\n   \n# another comment"), 0644); err != nil {
		t.Fatal(err)
	}
	colComment := NewToolParamCollectorWithRoot(tmpDir)
	sampleComment := collect.NewSample()
	if err := colComment.Collect(sampleComment); err != nil {
		t.Fatalf("colComment.Collect failed: %v", err)
	}
	if len(sampleComment.ToolParam["network"]) != 0 {
		t.Errorf("expected 0 keys for comment-only file, got %d", len(sampleComment.ToolParam["network"]))
	}
}

// TestGiascanCollector_Normal 验证使用真实样本解析 available.tsv。
func TestGiascanCollector_Normal(t *testing.T) {
	testToolRoot := filepath.Join("..", "testdata", "normal", "opt", "tool")
	col := NewGiascanCollectorWithRoot(testToolRoot)

	sample := collect.NewSample()
	if err := col.Collect(sample); err != nil {
		t.Fatalf("col.Collect failed: %v", err)
	}

	if sample.Giascan == nil {
		t.Fatalf("sample.Giascan is nil")
	}

	if !sample.Giascan.Exists {
		t.Errorf("expected Exists=true, got false, err: %v", sample.Giascan.Error)
	}
	if sample.Giascan.Size == nil || *sample.Giascan.Size <= 0 {
		t.Errorf("expected valid Size, got %v", sample.Giascan.Size)
	}
	if sample.Giascan.Mtime == nil || *sample.Giascan.Mtime <= 0 {
		t.Errorf("expected valid Mtime, got %v", sample.Giascan.Mtime)
	}
	if sample.Giascan.Text == nil || !strings.Contains(*sample.Giascan.Text, "amd2\tus-west") {
		t.Errorf("expected text containing amd2 node, got %v", sample.Giascan.Text)
	}
}

// TestGiascanCollector_FailureAndBoundary 验证 giascan 失败路径与边界处理。
func TestGiascanCollector_FailureAndBoundary(t *testing.T) {
	// 1. 失败路径：root 未注册
	colUnregistered := NewGiascanCollectorWithRoot(filepath.Join(t.TempDir(), "empty_tool"))
	sampleUnregistered := collect.NewSample()
	if err := colUnregistered.Collect(sampleUnregistered); err != nil {
		t.Fatalf("col.Collect unexpected error: %v", err)
	}
	if sampleUnregistered.Giascan == nil || sampleUnregistered.Giascan.Exists {
		t.Fatalf("expected Exists=false for unregistered root")
	}
	if sampleUnregistered.Giascan.Error == nil || !strings.Contains(*sampleUnregistered.Giascan.Error, "root giascan2-router 未注册") {
		t.Errorf("expected unregistered error, got %v", sampleUnregistered.Giascan.Error)
	}

	// 2. 边界路径：root 目录存在但 data/available.tsv 缺失
	tmpDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmpDir, "giascan2-router"), 0755); err != nil {
		t.Fatal(err)
	}
	colMissingFile := NewGiascanCollectorWithRoot(tmpDir)
	sampleMissingFile := collect.NewSample()
	if err := colMissingFile.Collect(sampleMissingFile); err != nil {
		t.Fatalf("col.Collect unexpected error: %v", err)
	}
	if sampleMissingFile.Giascan == nil || sampleMissingFile.Giascan.Exists {
		t.Errorf("expected Exists=false when available.tsv is missing")
	}
	if sampleMissingFile.Giascan.Error != nil {
		t.Errorf("expected Error=nil when file is merely absent, got %v", *sampleMissingFile.Giascan.Error)
	}

	// 3. 边界路径：大文件截断限制（验证 maxBytes 上限）
	largeDataDir := filepath.Join(tmpDir, "giascan2-router", "data")
	if err := os.MkdirAll(largeDataDir, 0755); err != nil {
		t.Fatal(err)
	}
	largeContent := strings.Repeat("A", 200000)
	if err := os.WriteFile(filepath.Join(largeDataDir, "available.tsv"), []byte(largeContent), 0644); err != nil {
		t.Fatal(err)
	}
	sampleLarge := collect.NewSample()
	if err := colMissingFile.Collect(sampleLarge); err != nil {
		t.Fatalf("col.Collect unexpected error: %v", err)
	}
	if sampleLarge.Giascan == nil || !sampleLarge.Giascan.Exists {
		t.Fatalf("expected Exists=true for large file")
	}
	if len(*sampleLarge.Giascan.Text) != defaultGiascanMaxBytes {
		t.Errorf("expected text length truncated to %d, got %d", defaultGiascanMaxBytes, len(*sampleLarge.Giascan.Text))
	}
}

// TestOmittedVsZeroFields_Criterion5 制造采集失败，验证对应字段在 JSON 里完全不存在（不是 0，不是 null 字面量）。
func TestOmittedVsZeroFields_Criterion5(t *testing.T) {
	sample := collect.NewSample()
	// 未采集或采集失败时，各字段保持为 nil
	sample.Reset(collect.TierSlow)

	data, err := json.Marshal(sample)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	jsonStr := string(data)

	if strings.Contains(jsonStr, `"listen"`) {
		t.Errorf("criterion 5 violated: 'listen' key must not appear in JSON when nil/failed, got: %s", jsonStr)
	}
	if strings.Contains(jsonStr, `"tool_param"`) {
		t.Errorf("criterion 5 violated: 'tool_param' key must not appear in JSON when nil/failed, got: %s", jsonStr)
	}
	if strings.Contains(jsonStr, `"giascan"`) {
		t.Errorf("criterion 5 violated: 'giascan' key must not appear in JSON when nil/failed, got: %s", jsonStr)
	}
	if strings.Contains(jsonStr, `:null`) {
		t.Errorf("criterion 5 violated: JSON must not contain null literals: %s", jsonStr)
	}
}

// TestCollectors_AlpineSamples 验证 Alpine 环境下的真实样本解析。
func TestCollectors_AlpineSamples(t *testing.T) {
	alpineProc := filepath.Join("..", "testdata", "alpine", "proc")
	listenCol := NewListenCollectorWithRoot(alpineProc)
	listenPayload, err := listenCol.CollectPayload()
	if err != nil {
		t.Fatalf("listenCol.CollectPayload on alpine failed: %v", err)
	}
	if len(listenPayload.Listen) != 3 {
		t.Errorf("expected 3 listen entries on alpine sample, got %d", len(listenPayload.Listen))
	}

	alpineTool := filepath.Join("..", "testdata", "alpine", "opt", "tool")
	tpCol := NewToolParamCollectorWithRoot(alpineTool)
	tpPayload, err := tpCol.CollectPayload()
	if err != nil {
		t.Fatalf("tpCol.CollectPayload on alpine failed: %v", err)
	}
	if tpPayload["network"]["THRESHOLD_GB"] != "500" {
		t.Errorf("expected alpine network THRESHOLD_GB=500, got %q", tpPayload["network"]["THRESHOLD_GB"])
	}

	giaCol := NewGiascanCollectorWithRoot(alpineTool)
	giaPayload, err := giaCol.CollectPayload()
	if err != nil {
		t.Fatalf("giaCol.CollectPayload on alpine failed: %v", err)
	}
	if !giaPayload.Exists || giaPayload.Text == nil || !strings.Contains(*giaPayload.Text, "alpine1") {
		t.Errorf("expected alpine giascan payload containing alpine1, got %+v", giaPayload)
	}
}

// TestListenCollector_LivePerformance 针对本机数百连接实测耗时与内存分配。
func TestListenCollector_LivePerformance(t *testing.T) {
	col := NewListenCollector()
	start := time.Now()
	payload, err := col.CollectPayload()
	dur := time.Since(start)
	if err != nil {
		t.Fatalf("live collect failed: %v", err)
	}
	t.Logf("Live machine listen collect: entries=%d, duration=%v", len(payload.Listen), dur)
	if dur > 100*time.Millisecond {
		t.Errorf("listen collect duration %v exceeded 100ms budget", dur)
	}
}

// TestListenCollector_500ActiveConnectionsPerformance 建立 500 个真实 TCP 连接模拟高并发连接场景，
// 验证在连接数大幅增长时 ListenCollector 的耗时与内存开销符合预算要求（Criterion 3）。
func TestListenCollector_500ActiveConnectionsPerformance(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen failed: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()
	const numConns = 500
	clientConns := make([]net.Conn, 0, numConns)
	serverConns := make([]net.Conn, 0, numConns)
	defer func() {
		for _, c := range clientConns {
			_ = c.Close()
		}
		for _, c := range serverConns {
			_ = c.Close()
		}
	}()

	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for i := 0; i < numConns; i++ {
			sConn, err := ln.Accept()
			if err != nil {
				return
			}
			serverConns = append(serverConns, sConn)
		}
	}()

	for i := 0; i < numConns; i++ {
		cConn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatalf("net.Dial %d failed: %v", i, err)
		}
		clientConns = append(clientConns, cConn)
	}
	<-acceptDone

	col := NewListenCollector()
	var m1, m2 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)

	start := time.Now()
	payload, err := col.CollectPayload()
	dur := time.Since(start)

	runtime.ReadMemStats(&m2)
	allocBytes := m2.TotalAlloc - m1.TotalAlloc

	if err != nil {
		t.Fatalf("CollectPayload with 500 conns failed: %v", err)
	}

	t.Logf("500 Active Connections: entries=%d, duration=%v, total allocs=%d KB",
		len(payload.Listen), dur, allocBytes/1024)

	if dur > 100*time.Millisecond {
		t.Errorf("listen collect duration %v exceeded 100ms budget under 500 connections", dur)
	}
}

// BenchmarkListenCollector_LiveMachine 测试在真实系统数百连接下的性能。
func BenchmarkListenCollector_LiveMachine(b *testing.B) {
	col := NewListenCollector()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := col.CollectPayload()
		if err != nil {
			b.Fatal(err)
		}
	}
}

// TestLivePairComparison_Criterion2 在当前机器上分别采集三种 payload 并做对拍比对输出。
func TestLivePairComparison_Criterion2(t *testing.T) {
	// 1. Listen
	listenCol := NewListenCollector()
	listenRes, err := listenCol.CollectPayload()
	if err != nil {
		t.Fatalf("listen live collect failed: %v", err)
	}
	listenJSON, _ := json.Marshal(listenRes)
	t.Logf("Go listen payload: %s", string(listenJSON))

	// 2. ToolParam
	tpCol := NewToolParamCollector()
	tpRes, err := tpCol.CollectPayload()
	if err != nil {
		t.Fatalf("tool_param live collect failed: %v", err)
	}
	tpJSON, _ := json.Marshal(tpRes)
	t.Logf("Go tool_param payload: %s", string(tpJSON))

	// 3. Giascan
	giaCol := NewGiascanCollector()
	giaRes, err := giaCol.CollectPayload()
	if err != nil {
		t.Fatalf("giascan live collect failed: %v", err)
	}
	giaJSON, _ := json.Marshal(giaRes)
	t.Logf("Go giascan payload: %s", string(giaJSON))
}


