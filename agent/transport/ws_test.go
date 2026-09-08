package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"dash/internal/protocol"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
	EnableCompression: true,
}

func TestResolveEndpoints(t *testing.T) {
	tests := []struct {
		input       string
		expectedWS  string
		expectedHTTP string
		expectErr   bool
	}{
		{
			input:        "https://demo.dash.dev",
			expectedWS:   "wss://demo.dash.dev/api/agent/v1/rpc",
			expectedHTTP: "https://demo.dash.dev/agent/v1/report",
		},
		{
			input:        "http://127.0.0.1:8080",
			expectedWS:   "ws://127.0.0.1:8080/api/agent/v1/rpc",
			expectedHTTP: "http://127.0.0.1:8080/agent/v1/report",
		},
		{
			input:        "wss://demo.dash.dev",
			expectedWS:   "wss://demo.dash.dev/api/agent/v1/rpc",
			expectedHTTP: "https://demo.dash.dev/agent/v1/report",
		},
		{
			input:        "ws://127.0.0.1:8080",
			expectedWS:   "ws://127.0.0.1:8080/api/agent/v1/rpc",
			expectedHTTP: "http://127.0.0.1:8080/agent/v1/report",
		},
		{
			input:        "demo.dash.dev",
			expectedWS:   "wss://demo.dash.dev/api/agent/v1/rpc",
			expectedHTTP: "https://demo.dash.dev/agent/v1/report",
		},
		{
			input:        "http://127.0.0.1:8080/custom",
			expectedWS:   "ws://127.0.0.1:8080/custom/api/agent/v1/rpc",
			expectedHTTP: "http://127.0.0.1:8080/custom/agent/v1/report",
		},
		{
			input:     "",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		wsURL, httpURL, err := resolveEndpoints(tt.input)
		if tt.expectErr {
			if err == nil {
				t.Fatalf("expected error for input %q, got nil", tt.input)
			}
			continue
		}
		if err != nil {
			t.Fatalf("unexpected error for input %q: %v", tt.input, err)
		}
		if wsURL != tt.expectedWS {
			t.Fatalf("wsURL mismatch for %q: got %q, want %q", tt.input, wsURL, tt.expectedWS)
		}
		if httpURL != tt.expectedHTTP {
			t.Fatalf("httpURL mismatch for %q: got %q, want %q", tt.input, httpURL, tt.expectedHTTP)
		}
	}
}

func TestWSTransport_Connect_Call_Send(t *testing.T) {
	expectedToken := "test-agent-token-12345"
	var receivedAuth string
	var wsConnMu sync.Mutex
	var srvWSConn *websocket.Conn

	metricsReceived := make(chan *protocol.Request, 10)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/agent/v1/rpc" {
			receivedAuth = r.Header.Get("Authorization")
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			wsConnMu.Lock()
			srvWSConn = conn
			wsConnMu.Unlock()

			go func() {
				for {
					_, payload, err := conn.ReadMessage()
					if err != nil {
						return
					}
					var req protocol.Request
					if err := json.Unmarshal(payload, &req); err != nil {
						continue
					}
					if req.Method == protocol.MethodAgentHello && req.ID != nil {
						// 响应 agent.hello
						resp := protocol.Response{
							JSONRPC: protocol.JSONRPCVersion,
							ID:      req.ID,
							Result:  json.RawMessage(`{"node_id":"01JBX001","interval_fast_s":5}`),
						}
						b, _ := json.Marshal(resp)
						_ = conn.WriteMessage(websocket.TextMessage, b)
					} else if req.Method == protocol.MethodAgentMetrics {
						metricsReceived <- &req
					}
				}
			}()
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	tr, err := New(Config{
		ServerURL:    server.URL,
		Token:        expectedToken,
		PingInterval: 100 * time.Millisecond,
		ReadTimeout:  1 * time.Second,
		DialTimeout:  1 * time.Second,
	})
	if err != nil {
		t.Fatalf("New transport failed: %v", err)
	}
	defer tr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// 等待连接建立
	deadline := time.Now().Add(2 * time.Second)
	for tr.State() != StateWSConnected && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if tr.State() != StateWSConnected {
		t.Fatalf("expected StateWSConnected, got %s", tr.State())
	}

	if receivedAuth != "Bearer "+expectedToken {
		t.Fatalf("expected Bearer auth %s, got %s", expectedToken, receivedAuth)
	}

	// 测试 Call agent.hello
	helloParams := protocol.HelloParams{
		ProtocolVersion: protocol.ProtocolVersion,
		AgentVersion:    "0.1.0",
		BootAtMs:        time.Now().UnixMilli(),
		Capabilities:    []string{"metrics", "facts"},
		FactsHash:       "hash123",
	}

	callCtx, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callCancel()

	rawResult, err := tr.Call(callCtx, protocol.MethodAgentHello, helloParams)
	if err != nil {
		t.Fatalf("Call agent.hello failed: %v", err)
	}

	var helloRes protocol.HelloResult
	if err := json.Unmarshal(rawResult, &helloRes); err != nil {
		t.Fatalf("unmarshal hello result failed: %v", err)
	}
	if helloRes.NodeID != "01JBX001" {
		t.Fatalf("expected NodeID 01JBX001, got %s", helloRes.NodeID)
	}

	// 测试 Send agent.metrics
	cpuPct := 12.5
	metricsParams := protocol.MetricsParams{
		TsMs:   time.Now().UnixMilli(),
		CPUPct: &cpuPct,
	}
	if err := tr.Send(protocol.MethodAgentMetrics, metricsParams); err != nil {
		t.Fatalf("Send agent.metrics failed: %v", err)
	}

	select {
	case req := <-metricsReceived:
		if req.Method != protocol.MethodAgentMetrics {
			t.Fatalf("expected method %s, got %s", protocol.MethodAgentMetrics, req.Method)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for agent.metrics notification")
	}

	wsConnMu.Lock()
	if srvWSConn != nil {
		_ = srvWSConn.Close()
	}
	wsConnMu.Unlock()
}

func TestWSTransport_Handshake401_StopsImmediately(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer server.Close()

	tr, err := New(Config{
		ServerURL: server.URL,
		Token:     "invalid-token",
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer tr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for tr.State() != StateStopped && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if tr.State() != StateStopped {
		t.Fatalf("expected StateStopped on handshake 401, got %s", tr.State())
	}
}

func TestWSTransport_ReadTimeout_TriggersReconnect(t *testing.T) {
	var connCount int
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		mu.Lock()
		connCount++
		mu.Unlock()
		defer conn.Close()

		// 保持连接静默，并且丢弃 ping 不回 pong，模拟服务端失联
		conn.SetPingHandler(func(string) error {
			return nil
		})
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	tr, err := New(Config{
		ServerURL:       server.URL,
		Token:           "test-token",
		PingInterval:    50 * time.Millisecond,
		ReadTimeout:     100 * time.Millisecond,
		BackoffSequence: []time.Duration{20 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer tr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// 等待初始连接
	deadline := time.Now().Add(2 * time.Second)
	for tr.State() != StateWSConnected && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if tr.State() != StateWSConnected {
		t.Fatalf("expected initial StateWSConnected, got %s", tr.State())
	}

	// 等待由于静默超时触发的主动重连
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		c := connCount
		mu.Unlock()
		if c >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	finalCount := connCount
	mu.Unlock()
	if finalCount < 2 {
		t.Fatalf("expected at least 2 connections due to read timeout reconnect, got %d", finalCount)
	}
}

func TestWSTransport_CompressionThreshold(t *testing.T) {
	receivedPayloads := make(chan []byte, 5)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			receivedPayloads <- payload
		}
	}))
	defer server.Close()

	tr, err := New(Config{
		ServerURL: server.URL,
		Token:     "test-token",
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer tr.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for tr.State() != StateWSConnected && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	// 1. 发送 < 1 KB 的小报文
	smallParams := map[string]string{"msg": "small"}
	if err := tr.Send("test.small", smallParams); err != nil {
		t.Fatalf("send small message failed: %v", err)
	}
	select {
	case payload := <-receivedPayloads:
		if len(payload) >= 1024 {
			t.Fatalf("expected < 1KB, got %d", len(payload))
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for small message")
	}

	// 2. 发送 >= 1 KB 的大报文（触发压缩）
	largePayload := make([]byte, 2048)
	for i := range largePayload {
		largePayload[i] = 'A'
	}
	largeParams := map[string]string{"data": string(largePayload)}
	if err := tr.Send("test.large", largeParams); err != nil {
		t.Fatalf("send large message failed: %v", err)
	}
	select {
	case payload := <-receivedPayloads:
		if len(payload) < 1024 {
			t.Fatalf("expected >= 1KB, got %d", len(payload))
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for large message")
	}
}

