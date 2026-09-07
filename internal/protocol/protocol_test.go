package protocol_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"dash/internal/protocol"
)

// ptr 辅助函数，生成指针
func ptr[T any](v T) *T {
	return &v
}

// 1. 04-protocol.md 里每个示例 JSON 都能反序列化成功且字段值正确
func TestProtocolDocExamples(t *testing.T) {
	t.Run("agent.hello request", func(t *testing.T) {
		raw := `{"jsonrpc":"2.0","id":1,"method":"agent.hello","params":{
  "protocol_version": 1,
  "agent_version": "0.1.0",
  "boot_at_ms": 1757222400000,
  "capabilities": ["metrics","facts","exec_actions","terminal"],
  "facts_hash": "3f2a"
}}`
		var req protocol.Request
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			t.Fatalf("unmarshal Request failed: %v", err)
		}
		if req.JSONRPC != "2.0" {
			t.Errorf("expected jsonrpc 2.0, got %s", req.JSONRPC)
		}
		if req.Method != protocol.MethodAgentHello {
			t.Errorf("expected method %s, got %s", protocol.MethodAgentHello, req.Method)
		}
		if req.ID == nil || *req.ID != 1 {
			t.Fatalf("expected id 1, got %v", req.ID)
		}

		var params protocol.HelloParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			t.Fatalf("unmarshal HelloParams failed: %v", err)
		}
		if params.ProtocolVersion != 1 {
			t.Errorf("expected protocol_version 1, got %d", params.ProtocolVersion)
		}
		if params.AgentVersion != "0.1.0" {
			t.Errorf("expected agent_version 0.1.0, got %s", params.AgentVersion)
		}
		if params.BootAtMs != 1757222400000 {
			t.Errorf("expected boot_at_ms 1757222400000, got %d", params.BootAtMs)
		}
		expectedCaps := []string{"metrics", "facts", "exec_actions", "terminal"}
		if !reflect.DeepEqual(params.Capabilities, expectedCaps) {
			t.Errorf("expected capabilities %v, got %v", expectedCaps, params.Capabilities)
		}
		if params.FactsHash != "3f2a" {
			t.Errorf("expected facts_hash 3f2a, got %s", params.FactsHash)
		}
	})

	t.Run("agent.hello response", func(t *testing.T) {
		raw := `{"jsonrpc":"2.0","id":1,"result":{
  "node_id": "01JBX",
  "server_time_ms": 1757222400123,
  "interval_fast_s": 5,
  "interval_slow_s": 60,
  "facts_max_interval_s": 1800,
  "collect_conns": true,
  "exec_mode": "actions",
  "enable_terminal": false,
  "need_facts": true,
  "action_catalog_rev": 7
}}`
		var resp protocol.Response
		if err := json.Unmarshal([]byte(raw), &resp); err != nil {
			t.Fatalf("unmarshal Response failed: %v", err)
		}
		if resp.JSONRPC != "2.0" {
			t.Errorf("expected jsonrpc 2.0, got %s", resp.JSONRPC)
		}
		if resp.ID == nil || *resp.ID != 1 {
			t.Fatalf("expected id 1, got %v", resp.ID)
		}
		if resp.Error != nil {
			t.Fatalf("expected error to be nil, got %v", resp.Error)
		}

		var res protocol.HelloResult
		if err := json.Unmarshal(resp.Result, &res); err != nil {
			t.Fatalf("unmarshal HelloResult failed: %v", err)
		}
		if res.NodeID != "01JBX" {
			t.Errorf("expected node_id 01JBX, got %s", res.NodeID)
		}
		if res.ServerTimeMs != 1757222400123 {
			t.Errorf("expected server_time_ms 1757222400123, got %d", res.ServerTimeMs)
		}
		if res.IntervalFastS != 5 {
			t.Errorf("expected interval_fast_s 5, got %d", res.IntervalFastS)
		}
		if res.IntervalSlowS != 60 {
			t.Errorf("expected interval_slow_s 60, got %d", res.IntervalSlowS)
		}
		if res.FactsMaxIntervalS != 1800 {
			t.Errorf("expected facts_max_interval_s 1800, got %d", res.FactsMaxIntervalS)
		}
		if !res.CollectConns {
			t.Errorf("expected collect_conns true, got %v", res.CollectConns)
		}
		if !res.NeedFacts {
			t.Errorf("expected need_facts true, got %v", res.NeedFacts)
		}
	})

	t.Run("agent.metrics notification (fast+slow)", func(t *testing.T) {
		raw := `{"jsonrpc":"2.0","method":"agent.metrics","params":{
  "ts_ms": 1757222405000,
  "cpu_pct": 3.5,
  "mem_used": 412000000,
  "swap_used": 0,
  "load": [0.12, 0.09, 0.05],
  "net": {"up_bps": 12000, "down_bps": 84000, "total_up": 90123456789, "total_down": 0},
  "uptime_s": 864000,
  "slow": {
    "disk_used": 12000000000,
    "proc_count": 92,
    "tcp_count": 41,
    "udp_count": 6,
    "disks": [{"k":"/","used":12000000000,"total":42000000000}],
    "nics": [{"k":"eth0","total_up":90123456789,"total_down":512345678901}]
  }
}}`
		var req protocol.Request
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			t.Fatalf("unmarshal Request failed: %v", err)
		}
		if req.JSONRPC != "2.0" {
			t.Errorf("expected jsonrpc 2.0, got %s", req.JSONRPC)
		}
		if req.Method != protocol.MethodAgentMetrics {
			t.Errorf("expected method %s, got %s", protocol.MethodAgentMetrics, req.Method)
		}
		if req.ID != nil {
			t.Errorf("expected notification (id=nil), got %v", req.ID)
		}

		var params protocol.MetricsParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			t.Fatalf("unmarshal MetricsParams failed: %v", err)
		}
		if params.TsMs != 1757222405000 {
			t.Errorf("expected ts_ms 1757222405000, got %d", params.TsMs)
		}
		if params.CPUPct == nil || *params.CPUPct != 3.5 {
			t.Errorf("expected cpu_pct 3.5, got %v", params.CPUPct)
		}
		if params.MemUsed == nil || *params.MemUsed != 412000000 {
			t.Errorf("expected mem_used 412000000, got %v", params.MemUsed)
		}
		if params.SwapUsed == nil || *params.SwapUsed != 0 {
			t.Errorf("expected swap_used 0, got %v", params.SwapUsed)
		}
		if params.Load == nil || *params.Load != [3]float64{0.12, 0.09, 0.05} {
			t.Errorf("expected load [0.12, 0.09, 0.05], got %v", params.Load)
		}
		if params.Net == nil {
			t.Fatalf("expected net not nil")
		}
		if params.Net.UpBps == nil || *params.Net.UpBps != 12000 {
			t.Errorf("expected net.up_bps 12000, got %v", params.Net.UpBps)
		}
		if params.Net.DownBps == nil || *params.Net.DownBps != 84000 {
			t.Errorf("expected net.down_bps 84000, got %v", params.Net.DownBps)
		}
		if params.Net.TotalUp == nil || *params.Net.TotalUp != 90123456789 {
			t.Errorf("expected net.total_up 90123456789, got %v", params.Net.TotalUp)
		}
		if params.Net.TotalDown == nil || *params.Net.TotalDown != 0 {
			t.Errorf("expected net.total_down 0, got %v", params.Net.TotalDown)
		}
		if params.UptimeS == nil || *params.UptimeS != 864000 {
			t.Errorf("expected uptime_s 864000, got %v", params.UptimeS)
		}

		if params.Slow == nil {
			t.Fatalf("expected slow not nil")
		}
		if params.Slow.DiskUsed == nil || *params.Slow.DiskUsed != 12000000000 {
			t.Errorf("expected slow.disk_used 12000000000, got %v", params.Slow.DiskUsed)
		}
		if params.Slow.ProcCount == nil || *params.Slow.ProcCount != 92 {
			t.Errorf("expected slow.proc_count 92, got %v", params.Slow.ProcCount)
		}
		if params.Slow.TCPCount == nil || *params.Slow.TCPCount != 41 {
			t.Errorf("expected slow.tcp_count 41, got %v", params.Slow.TCPCount)
		}
		if params.Slow.UDPCount == nil || *params.Slow.UDPCount != 6 {
			t.Errorf("expected slow.udp_count 6, got %v", params.Slow.UDPCount)
		}
		if len(params.Slow.Disks) != 1 {
			t.Fatalf("expected 1 disk entry, got %d", len(params.Slow.Disks))
		}
		if params.Slow.Disks[0].Key != "/" || *params.Slow.Disks[0].Used != 12000000000 || *params.Slow.Disks[0].Total != 42000000000 {
			t.Errorf("unexpected disk entry: %+v", params.Slow.Disks[0])
		}
		if len(params.Slow.NICs) != 1 {
			t.Fatalf("expected 1 nic entry, got %d", len(params.Slow.NICs))
		}
		if params.Slow.NICs[0].Key != "eth0" || *params.Slow.NICs[0].TotalUp != 90123456789 || *params.Slow.NICs[0].TotalDown != 512345678901 {
			t.Errorf("unexpected nic entry: %+v", params.Slow.NICs[0])
		}
	})

	t.Run("agent.facts notification", func(t *testing.T) {
		raw := `{"jsonrpc":"2.0","method":"agent.facts","params":{
  "arch": "amd64",
  "os_name": "debian",
  "os_version": "12",
  "kernel": "6.1.0-21-amd64",
  "virt": "kvm",
  "cpu_model": "Intel Xeon",
  "cpu_cores": 2,
  "cpu_threads": 4,
  "mem_total": 4120000000,
  "swap_total": 1073741824,
  "disk_total": 42000000000,
  "ipv4": "198.51.100.1",
  "ipv6": "2001:db8::1",
  "boot_at_ms": 1757222400000,
  "facts_hash": "3f2a5b6c"
}}`
		var req protocol.Request
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			t.Fatalf("unmarshal Request failed: %v", err)
		}
		if req.JSONRPC != "2.0" {
			t.Errorf("expected jsonrpc 2.0, got %s", req.JSONRPC)
		}
		if req.Method != protocol.MethodAgentFacts {
			t.Errorf("expected method %s, got %s", protocol.MethodAgentFacts, req.Method)
		}
		if req.ID != nil {
			t.Errorf("expected notification (id=nil), got %v", req.ID)
		}

		var facts protocol.FactsParams
		if err := json.Unmarshal(req.Params, &facts); err != nil {
			t.Fatalf("unmarshal FactsParams failed: %v", err)
		}
		if facts.Arch != "amd64" {
			t.Errorf("expected arch amd64, got %s", facts.Arch)
		}
		if facts.OSName != "debian" {
			t.Errorf("expected os_name debian, got %s", facts.OSName)
		}
		if facts.OSVersion != "12" {
			t.Errorf("expected os_version 12, got %s", facts.OSVersion)
		}
		if facts.Kernel != "6.1.0-21-amd64" {
			t.Errorf("expected kernel 6.1.0-21-amd64, got %s", facts.Kernel)
		}
		if facts.Virt != "kvm" {
			t.Errorf("expected virt kvm, got %s", facts.Virt)
		}
		if facts.CPUModel != "Intel Xeon" {
			t.Errorf("expected cpu_model Intel Xeon, got %s", facts.CPUModel)
		}
		if facts.CPUCores != 2 {
			t.Errorf("expected cpu_cores 2, got %d", facts.CPUCores)
		}
		if facts.CPUThreads != 4 {
			t.Errorf("expected cpu_threads 4, got %d", facts.CPUThreads)
		}
		if facts.MemTotal != 4120000000 {
			t.Errorf("expected mem_total 4120000000, got %d", facts.MemTotal)
		}
		if facts.SwapTotal != 1073741824 {
			t.Errorf("expected swap_total 1073741824, got %d", facts.SwapTotal)
		}
		if facts.DiskTotal != 42000000000 {
			t.Errorf("expected disk_total 42000000000, got %d", facts.DiskTotal)
		}
		if facts.IPv4 != "198.51.100.1" {
			t.Errorf("expected ipv4 198.51.100.1, got %s", facts.IPv4)
		}
		if facts.IPv6 != "2001:db8::1" {
			t.Errorf("expected ipv6 2001:db8::1, got %s", facts.IPv6)
		}
		if facts.BootAtMs != 1757222400000 {
			t.Errorf("expected boot_at_ms 1757222400000, got %d", facts.BootAtMs)
		}
		if facts.FactsHash != "3f2a5b6c" {
			t.Errorf("expected facts_hash 3f2a5b6c, got %s", facts.FactsHash)
		}
	})

	t.Run("server.config notification", func(t *testing.T) {
		raw := `{"jsonrpc":"2.0","method":"server.config","params":{
  "interval_fast_s": 10,
  "interval_slow_s": 120,
  "facts_max_interval_s": 3600,
  "collect_conns": false,
  "need_facts": false
}}`
		var req protocol.Request
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			t.Fatalf("unmarshal Request failed: %v", err)
		}
		if req.JSONRPC != "2.0" {
			t.Errorf("expected jsonrpc 2.0, got %s", req.JSONRPC)
		}
		if req.Method != protocol.MethodServerConfig {
			t.Errorf("expected method %s, got %s", protocol.MethodServerConfig, req.Method)
		}
		if req.ID != nil {
			t.Errorf("expected notification (id=nil), got %v", req.ID)
		}

		var cfg protocol.ServerConfigParams
		if err := json.Unmarshal(req.Params, &cfg); err != nil {
			t.Fatalf("unmarshal ServerConfigParams failed: %v", err)
		}
		if cfg.IntervalFastS == nil || *cfg.IntervalFastS != 10 {
			t.Errorf("expected interval_fast_s 10, got %v", cfg.IntervalFastS)
		}
		if cfg.IntervalSlowS == nil || *cfg.IntervalSlowS != 120 {
			t.Errorf("expected interval_slow_s 120, got %v", cfg.IntervalSlowS)
		}
		if cfg.FactsMaxIntervalS == nil || *cfg.FactsMaxIntervalS != 3600 {
			t.Errorf("expected facts_max_interval_s 3600, got %v", cfg.FactsMaxIntervalS)
		}
		if cfg.CollectConns == nil || *cfg.CollectConns != false {
			t.Errorf("expected collect_conns false, got %v", cfg.CollectConns)
		}
		if cfg.NeedFacts == nil || *cfg.NeedFacts != false {
			t.Errorf("expected need_facts false, got %v", cfg.NeedFacts)
		}
	})

	t.Run("RPC error response", func(t *testing.T) {
		raw := `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"token invalid or revoked"}}`
		var resp protocol.Response
		if err := json.Unmarshal([]byte(raw), &resp); err != nil {
			t.Fatalf("unmarshal Response failed: %v", err)
		}
		if resp.JSONRPC != "2.0" {
			t.Errorf("expected jsonrpc 2.0, got %s", resp.JSONRPC)
		}
		if resp.ID == nil || *resp.ID != 1 {
			t.Fatalf("expected id 1, got %v", resp.ID)
		}
		if resp.Result != nil {
			t.Errorf("expected result to be nil, got %s", string(resp.Result))
		}
		if resp.Error == nil {
			t.Fatalf("expected error not nil")
		}
		if resp.Error.Code != protocol.ErrCodeTokenInvalid {
			t.Errorf("expected code %d, got %d", protocol.ErrCodeTokenInvalid, resp.Error.Code)
		}
		if resp.Error.Message != "token invalid or revoked" {
			t.Errorf("expected message 'token invalid or revoked', got %s", resp.Error.Message)
		}
		if !strings.Contains(resp.Error.Error(), "-32000") {
			t.Errorf("expected Error() string to contain code, got %s", resp.Error.Error())
		}
	})
}

// 2. 序列化往返测试：结构体 → JSON → 结构体，结果相等
func TestRoundTripSerialization(t *testing.T) {
	t.Run("HelloParams", func(t *testing.T) {
		orig := protocol.HelloParams{
			ProtocolVersion: protocol.ProtocolVersion,
			AgentVersion:    "0.1.0",
			BootAtMs:        1757222400000,
			Capabilities:    []string{"metrics", "facts"},
			FactsHash:       "a1b2c3d4e5",
		}
		data, err := json.Marshal(orig)
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}
		var decoded protocol.HelloParams
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if !reflect.DeepEqual(orig, decoded) {
			t.Errorf("roundtrip mismatch: orig=%+v, decoded=%+v", orig, decoded)
		}
	})

	t.Run("HelloResult", func(t *testing.T) {
		orig := protocol.HelloResult{
			NodeID:            "01JBX1234567890ABCDEF",
			ServerTimeMs:      1757222400123,
			IntervalFastS:     5,
			IntervalSlowS:     60,
			FactsMaxIntervalS: 1800,
			CollectConns:      true,
			NeedFacts:         true,
		}
		data, err := json.Marshal(orig)
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}
		var decoded protocol.HelloResult
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if !reflect.DeepEqual(orig, decoded) {
			t.Errorf("roundtrip mismatch: orig=%+v, decoded=%+v", orig, decoded)
		}
	})

	t.Run("MetricsParams full (fast + slow)", func(t *testing.T) {
		orig := protocol.MetricsParams{
			TsMs:     1757222405000,
			CPUPct:   ptr(12.5),
			MemUsed:  ptr(int64(104857600)),
			SwapUsed: ptr(int64(0)),
			Load:     &[3]float64{0.5, 0.3, 0.1},
			Net: &protocol.NetReport{
				UpBps:     ptr(int64(1024)),
				DownBps:   ptr(int64(2048)),
				TotalUp:   ptr(int64(50000)),
				TotalDown: ptr(int64(100000)),
			},
			UptimeS: ptr(int64(3600)),
			Slow: &protocol.SlowReport{
				DiskUsed:  ptr(int64(5368709120)),
				ProcCount: ptr(int32(110)),
				TCPCount:  ptr(int32(25)),
				UDPCount:  ptr(int32(4)),
				Disks: []protocol.DiskEntry{
					{Key: "/", Used: ptr(int64(5368709120)), Total: ptr(int64(21474836480))},
					{Key: "/data", Used: ptr(int64(1073741824)), Total: ptr(int64(53687091200))},
				},
				NICs: []protocol.NICEntry{
					{Key: "eth0", TotalUp: ptr(int64(50000)), TotalDown: ptr(int64(100000))},
				},
			},
		}
		data, err := json.Marshal(orig)
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}
		var decoded protocol.MetricsParams
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if !reflect.DeepEqual(orig, decoded) {
			t.Errorf("roundtrip mismatch: orig=%+v, decoded=%+v", orig, decoded)
		}
	})

	t.Run("MetricsParams fast-only (slow=nil)", func(t *testing.T) {
		orig := protocol.MetricsParams{
			TsMs:    1757222405000,
			CPUPct:  ptr(5.0),
			MemUsed: ptr(int64(52428800)),
			UptimeS: ptr(int64(120)),
		}
		data, err := json.Marshal(orig)
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}
		// slow 必须在 JSON 中被省略
		if strings.Contains(string(data), "slow") {
			t.Errorf("expected slow to be omitted when nil, got: %s", string(data))
		}
		var decoded protocol.MetricsParams
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if !reflect.DeepEqual(orig, decoded) {
			t.Errorf("roundtrip mismatch: orig=%+v, decoded=%+v", orig, decoded)
		}
		if decoded.Slow != nil {
			t.Errorf("expected decoded.Slow to be nil")
		}
	})

	t.Run("FactsParams", func(t *testing.T) {
		orig := protocol.FactsParams{
			Arch:       "arm64",
			OSName:     "alpine",
			OSVersion:  "3.19",
			Kernel:     "6.6.0-alpine",
			Virt:       "container",
			CPUModel:   "Neoverse-N1",
			CPUCores:   4,
			CPUThreads: 4,
			MemTotal:   8589934592,
			SwapTotal:  0,
			DiskTotal:  85899345920,
			IPv4:       "203.0.113.10",
			IPv6:       "2001:db8::10",
			BootAtMs:   1757220000000,
			FactsHash:  "9f8e7d6c5b",
		}
		data, err := json.Marshal(orig)
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}
		var decoded protocol.FactsParams
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if !reflect.DeepEqual(orig, decoded) {
			t.Errorf("roundtrip mismatch: orig=%+v, decoded=%+v", orig, decoded)
		}
	})

	t.Run("ServerConfigParams (partial and full)", func(t *testing.T) {
		partial := protocol.ServerConfigParams{
			IntervalFastS: ptr(10),
			NeedFacts:     ptr(true),
		}
		data, err := json.Marshal(partial)
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}
		if strings.Contains(string(data), "interval_slow_s") {
			t.Errorf("unspecified fields must be omitted, got: %s", string(data))
		}
		var decoded protocol.ServerConfigParams
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if !reflect.DeepEqual(partial, decoded) {
			t.Errorf("roundtrip mismatch: orig=%+v, decoded=%+v", partial, decoded)
		}
	})

	t.Run("Request and Response Envelopes", func(t *testing.T) {
		rawParams := json.RawMessage(`{"key":"val"}`)
		req := protocol.Request{
			JSONRPC: protocol.JSONRPCVersion,
			Method:  protocol.MethodAgentHello,
			Params:  rawParams,
			ID:      ptr(int64(42)),
		}
		reqData, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("marshal request failed: %v", err)
		}
		var decodedReq protocol.Request
		if err := json.Unmarshal(reqData, &decodedReq); err != nil {
			t.Fatalf("unmarshal request failed: %v", err)
		}
		if decodedReq.JSONRPC != req.JSONRPC || decodedReq.Method != req.Method || *decodedReq.ID != *req.ID || string(decodedReq.Params) != string(req.Params) {
			t.Errorf("request mismatch: orig=%+v, decoded=%+v", req, decodedReq)
		}

		resp := protocol.Response{
			JSONRPC: protocol.JSONRPCVersion,
			ID:      ptr(int64(42)),
			Error: &protocol.RPCError{
				Code:    protocol.ErrCodeTokenInvalid,
				Message: "token revoked",
			},
		}
		respData, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("marshal response failed: %v", err)
		}
		var decodedResp protocol.Response
		if err := json.Unmarshal(respData, &decodedResp); err != nil {
			t.Fatalf("unmarshal response failed: %v", err)
		}
		if decodedResp.JSONRPC != resp.JSONRPC || *decodedResp.ID != *resp.ID || decodedResp.Error.Code != resp.Error.Code || decodedResp.Error.Message != resp.Error.Message {
			t.Errorf("response mismatch: orig=%+v, decoded=%+v", resp, decodedResp)
		}
	})
}

// 3. 省略字段的用例：不带 cpu_pct 的报文反序列化后能区分「未上报」与「值为 0」
func TestOmittedVsZeroFields(t *testing.T) {
	t.Run("cpu_pct omitted vs 0.0", func(t *testing.T) {
		// 未上报：字段缺失
		jsonOmitted := `{"ts_ms": 1757222405000}`
		var paramsOmitted protocol.MetricsParams
		if err := json.Unmarshal([]byte(jsonOmitted), &paramsOmitted); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if paramsOmitted.CPUPct != nil {
			t.Errorf("expected CPUPct to be nil (omitted), got %v", *paramsOmitted.CPUPct)
		}

		// 明确上报：值为 0.0
		jsonZero := `{"ts_ms": 1757222405000, "cpu_pct": 0.0}`
		var paramsZero protocol.MetricsParams
		if err := json.Unmarshal([]byte(jsonZero), &paramsZero); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if paramsZero.CPUPct == nil {
			t.Fatalf("expected CPUPct to NOT be nil (explicit 0.0)")
		}
		if *paramsZero.CPUPct != 0.0 {
			t.Errorf("expected CPUPct == 0.0, got %f", *paramsZero.CPUPct)
		}

		// 序列化时：值为 0.0 不被省略
		dataZero, err := json.Marshal(paramsZero)
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}
		if !strings.Contains(string(dataZero), `"cpu_pct":0`) {
			t.Errorf("expected cpu_pct:0 in JSON, got: %s", string(dataZero))
		}

		// 序列化时：nil 字段被省略
		dataOmitted, err := json.Marshal(paramsOmitted)
		if err != nil {
			t.Fatalf("marshal failed: %v", err)
		}
		if strings.Contains(string(dataOmitted), "cpu_pct") {
			t.Errorf("expected cpu_pct to be omitted in JSON, got: %s", string(dataOmitted))
		}
	})

	t.Run("swap_used and net.total_down omitted vs 0", func(t *testing.T) {
		jsonOmitted := `{"ts_ms": 1757222405000, "net": {}}`
		var paramsOmitted protocol.MetricsParams
		if err := json.Unmarshal([]byte(jsonOmitted), &paramsOmitted); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if paramsOmitted.SwapUsed != nil {
			t.Errorf("expected SwapUsed to be nil, got %v", *paramsOmitted.SwapUsed)
		}
		if paramsOmitted.Net.TotalDown != nil {
			t.Errorf("expected Net.TotalDown to be nil, got %v", *paramsOmitted.Net.TotalDown)
		}

		jsonZero := `{"ts_ms": 1757222405000, "swap_used": 0, "net": {"total_down": 0}}`
		var paramsZero protocol.MetricsParams
		if err := json.Unmarshal([]byte(jsonZero), &paramsZero); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if paramsZero.SwapUsed == nil || *paramsZero.SwapUsed != 0 {
			t.Errorf("expected SwapUsed == 0, got %v", paramsZero.SwapUsed)
		}
		if paramsZero.Net.TotalDown == nil || *paramsZero.Net.TotalDown != 0 {
			t.Errorf("expected Net.TotalDown == 0, got %v", paramsZero.Net.TotalDown)
		}
	})

	t.Run("slow fields omitted vs 0", func(t *testing.T) {
		jsonZero := `{"ts_ms": 1757222405000, "slow": {"tcp_count": 0, "disk_used": 0}}`
		var paramsZero protocol.MetricsParams
		if err := json.Unmarshal([]byte(jsonZero), &paramsZero); err != nil {
			t.Fatalf("unmarshal failed: %v", err)
		}
		if paramsZero.Slow == nil {
			t.Fatalf("expected Slow not nil")
		}
		if paramsZero.Slow.TCPCount == nil || *paramsZero.Slow.TCPCount != 0 {
			t.Errorf("expected TCPCount == 0, got %v", paramsZero.Slow.TCPCount)
		}
		if paramsZero.Slow.DiskUsed == nil || *paramsZero.Slow.DiskUsed != 0 {
			t.Errorf("expected DiskUsed == 0, got %v", paramsZero.Slow.DiskUsed)
		}
		if paramsZero.Slow.UDPCount != nil {
			t.Errorf("expected UDPCount == nil, got %v", paramsZero.Slow.UDPCount)
		}
	})
}

// 验证常量和错误码定义正确性
func TestConstantsAndErrorCodes(t *testing.T) {
	if protocol.ProtocolVersion != 1 {
		t.Errorf("expected ProtocolVersion == 1, got %d", protocol.ProtocolVersion)
	}
	if protocol.MethodAgentHello != "agent.hello" {
		t.Errorf("unexpected MethodAgentHello: %s", protocol.MethodAgentHello)
	}
	if protocol.MethodAgentMetrics != "agent.metrics" {
		t.Errorf("unexpected MethodAgentMetrics: %s", protocol.MethodAgentMetrics)
	}
	if protocol.MethodAgentFacts != "agent.facts" {
		t.Errorf("unexpected MethodAgentFacts: %s", protocol.MethodAgentFacts)
	}
	if protocol.MethodServerConfig != "server.config" {
		t.Errorf("unexpected MethodServerConfig: %s", protocol.MethodServerConfig)
	}

	// 占位常量
	if protocol.MethodAgentResult != "agent.result" ||
		protocol.MethodAgentPingRes != "agent.ping_result" ||
		protocol.MethodAgentSpeedRes != "agent.speedtest_result" ||
		protocol.MethodServerExec != "server.exec" ||
		protocol.MethodServerUpgrade != "server.upgrade" ||
		protocol.MethodServerPingTask != "server.ping_task" ||
		protocol.MethodServerSpeedtest != "server.speedtest" {
		t.Errorf("placeholder method constants incorrect")
	}

	// 错误码
	if protocol.ErrCodeTokenInvalid != -32000 ||
		protocol.ErrCodeVersionMismatch != -32001 ||
		protocol.ErrCodeCapDisabled != -32002 ||
		protocol.ErrCodeExecTimeout != -32003 ||
		protocol.ErrCodeRateLimited != -32004 ||
		protocol.ErrCodeParse != -32700 ||
		protocol.ErrCodeInvalidRequest != -32600 ||
		protocol.ErrCodeMethodNotFound != -32601 ||
		protocol.ErrCodeInvalidParams != -32602 {
		t.Errorf("error code constants incorrect")
	}
}
