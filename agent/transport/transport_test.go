package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dash/internal/protocol"

	"github.com/gorilla/websocket"
)

type logCollector struct {
	mu   sync.Mutex
	logs []string
}

func (l *logCollector) Printf(format string, v ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logs = append(l.logs, fmt.Sprintf(format, v...))
}

func (l *logCollector) GetAll() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	copied := make([]string, len(l.logs))
	copy(copied, l.logs)
	return copied
}

// 验收标准 1：断网再恢复，自动重连成功，且 agent 内存无增长
func TestAcceptance_1_ReconnectAndNoMemoryGrowth(t *testing.T) {
	var wsActive atomic.Bool
	wsActive.Store(true)

	receivedMetrics := make(chan *protocol.Request, 20)

	var srvConnMu sync.Mutex
	var curSrvConn *websocket.Conn

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/agent/v1/rpc" {
			if !wsActive.Load() {
				http.Error(w, "network down", http.StatusServiceUnavailable)
				return
			}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			srvConnMu.Lock()
			curSrvConn = conn
			srvConnMu.Unlock()
			defer conn.Close()

			for {
				_, payload, err := conn.ReadMessage()
				if err != nil {
					return
				}
				if !wsActive.Load() {
					return
				}
				var req protocol.Request
				if err := json.Unmarshal(payload, &req); err == nil {
					if req.Method == protocol.MethodAgentMetrics {
						receivedMetrics <- &req
					}
				}
			}
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	tr, err := New(Config{
		ServerURL:             server.URL,
		Token:                 "test-token",
		PingInterval:          50 * time.Millisecond,
		ReadTimeout:           200 * time.Millisecond,
		WriteTimeout:          100 * time.Millisecond,
		FallbackRetryInterval: 500 * time.Millisecond,
		BackoffSequence:       []time.Duration{20 * time.Millisecond, 40 * time.Millisecond},
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

	// 等待初始 WS 连接成功
	deadline := time.Now().Add(2 * time.Second)
	for tr.State() != StateWSConnected && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if tr.State() != StateWSConnected {
		t.Fatalf("expected StateWSConnected, got %s", tr.State())
	}

	// 发送一条指标，确认通道畅通
	if err := tr.Send(protocol.MethodAgentMetrics, protocol.MetricsParams{TsMs: time.Now().UnixMilli()}); err != nil {
		t.Fatalf("initial send failed: %v", err)
	}
	select {
	case <-receivedMetrics:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for initial metric")
	}

	// 模拟断网：关闭服务端连接并不再接受新连接
	wsActive.Store(false)
	srvConnMu.Lock()
	if curSrvConn != nil {
		_ = curSrvConn.Close()
	}
	srvConnMu.Unlock()

	// 等待传输层探测到断开并退出 WSConnected
	deadline = time.Now().Add(2 * time.Second)
	for tr.State() == StateWSConnected && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if tr.State() == StateWSConnected {
		t.Fatalf("expected state to exit WSConnected after disconnect, got %s", tr.State())
	}

	runtime.GC()
	var mBefore runtime.MemStats
	runtime.ReadMemStats(&mBefore)

	// 在断网期间调用 Send 大量指标，验证绝不阻塞且立刻丢弃
	for i := 0; i < 500; i++ {
		err := tr.Send(protocol.MethodAgentMetrics, protocol.MetricsParams{
			TsMs: time.Now().UnixMilli(),
		})
		if err == nil {
			t.Fatalf("expected error during network disconnect, got nil")
		}
	}

	runtime.GC()
	var mDuring runtime.MemStats
	runtime.ReadMemStats(&mDuring)

	// 堆对象增长应极低，绝无无限缓冲泄漏
	if int64(mDuring.HeapInuse)-int64(mBefore.HeapInuse) > 1024*1024 {
		t.Fatalf("unexpected memory growth during disconnect: before=%d, during=%d", mBefore.HeapInuse, mDuring.HeapInuse)
	}

	// 模拟网络恢复
	wsActive.Store(true)

	// 等待自动重连成功
	deadline = time.Now().Add(3 * time.Second)
	for tr.State() != StateWSConnected && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if tr.State() != StateWSConnected {
		t.Fatalf("expected reconnected StateWSConnected, got %s", tr.State())
	}

	// 恢复后发送一条指标
	if err := tr.Send(protocol.MethodAgentMetrics, protocol.MetricsParams{TsMs: time.Now().UnixMilli()}); err != nil {
		t.Fatalf("send after recovery failed: %v", err)
	}
	select {
	case <-receivedMetrics:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for metric after recovery")
	}
}

// 验收标准 2：服务端返回 -32000 后停止重连（看日志确认不再发起连接）
func TestAcceptance_2_StopOnTokenInvalid32000(t *testing.T) {
	var connectAttempts atomic.Int32
	logger := &logCollector{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/agent/v1/rpc" {
			connectAttempts.Add(1)
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
				var req protocol.Request
				if err := json.Unmarshal(payload, &req); err == nil {
					if req.ID != nil {
						// 返回 -32000 token 已吊销
						resp := protocol.Response{
							JSONRPC: protocol.JSONRPCVersion,
							ID:      req.ID,
							Error: &protocol.RPCError{
								Code:    protocol.ErrCodeTokenInvalid,
								Message: "token revoked or invalid",
							},
						}
						b, _ := json.Marshal(resp)
						_ = conn.WriteMessage(websocket.TextMessage, b)
					}
				}
			}
		}
	}))
	defer server.Close()

	tr, err := New(Config{
		ServerURL:       server.URL,
		Token:           "revoked-token",
		BackoffSequence: []time.Duration{20 * time.Millisecond},
		Logger:          logger,
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

	// 等待连上
	deadline := time.Now().Add(2 * time.Second)
	for tr.State() != StateWSConnected && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	// 发起 agent.hello 触发 -32000
	callCtx, callCancel := context.WithTimeout(context.Background(), time.Second)
	defer callCancel()

	_, err = tr.Call(callCtx, protocol.MethodAgentHello, protocol.HelloParams{})
	if err == nil {
		t.Fatalf("expected error on -32000, got nil")
	}

	// 验证状态变为 StateStopped
	deadline = time.Now().Add(2 * time.Second)
	for tr.State() != StateStopped && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if tr.State() != StateStopped {
		t.Fatalf("expected StateStopped, got %s", tr.State())
	}

	// 记录当前的拨号次数，等待一段时间确认不再发起任何重连
	attemptsBefore := connectAttempts.Load()
	time.Sleep(200 * time.Millisecond)
	attemptsAfter := connectAttempts.Load()

	if attemptsAfter > attemptsBefore {
		t.Fatalf("expected no further reconnection attempts after -32000: before=%d, after=%d", attemptsBefore, attemptsAfter)
	}

	// 验证日志中记录了 -32000 并停止
	logs := logger.GetAll()
	foundStopLog := false
	for _, l := range logs {
		if strings.Contains(l, "-32000") || strings.Contains(l, "stopping permanently") {
			foundStopLog = true
			break
		}
	}
	if !foundStopLog {
		t.Fatalf("expected stop log, got: %v", logs)
	}

	// 验证 Send 返回 ErrStopped
	if err := tr.Send(protocol.MethodAgentMetrics, nil); !errors.Is(err, ErrStopped) {
		t.Fatalf("expected ErrStopped on Send, got %v", err)
	}
}

// 验收标准 3：服务端下发一个未定义的 method，agent 返回 -32601 且继续正常上报
func TestAcceptance_3_UnknownMethodReturns32601AndContinues(t *testing.T) {
	var connMu sync.Mutex
	var srvConn *websocket.Conn
	respReceived := make(chan *protocol.Response, 5)
	metricsReceived := make(chan *protocol.Request, 5)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/agent/v1/rpc" {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			connMu.Lock()
			srvConn = conn
			connMu.Unlock()

			go func() {
				for {
					_, payload, err := conn.ReadMessage()
					if err != nil {
						return
					}
					var probe struct {
						JSONRPC string             `json:"jsonrpc"`
						ID      *int64             `json:"id"`
						Method  string             `json:"method"`
						Error   *protocol.RPCError `json:"error"`
					}
					if err := json.Unmarshal(payload, &probe); err != nil {
						continue
					}
					if probe.Method == "" && probe.ID != nil {
						respReceived <- &protocol.Response{
							JSONRPC: probe.JSONRPC,
							ID:      probe.ID,
							Error:   probe.Error,
						}
					} else if probe.Method == protocol.MethodAgentMetrics {
						metricsReceived <- &protocol.Request{
							Method: probe.Method,
						}
					}
				}
			}()
			return
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

	// 注册 Handler：只支持 server.config，其余返回 ErrMethodNotFound
	tr.OnServerMessage(func(method string, params json.RawMessage) (any, error) {
		if method == protocol.MethodServerConfig {
			return map[string]any{"ok": true}, nil
		}
		return nil, ErrMethodNotFound
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for tr.State() != StateWSConnected && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	// 服务端下发未定义的 method: server.unknown_action (带 id: 888)
	connMu.Lock()
	c := srvConn
	connMu.Unlock()
	if c == nil {
		t.Fatalf("server connection is nil")
	}

	unknownReq := `{"jsonrpc":"2.0","id":888,"method":"server.unknown_action","params":{"foo":"bar"}}`
	if err := c.WriteMessage(websocket.TextMessage, []byte(unknownReq)); err != nil {
		t.Fatalf("server write unknown request failed: %v", err)
	}

	// 验证 agent 回复 -32601 Method not found
	select {
	case resp := <-respReceived:
		if resp.ID == nil || *resp.ID != 888 {
			t.Fatalf("expected resp ID 888, got %v", resp.ID)
		}
		if resp.Error == nil {
			t.Fatalf("expected RPC error, got nil")
		}
		if resp.Error.Code != protocol.ErrCodeMethodNotFound {
			t.Fatalf("expected code %d (-32601), got %d", protocol.ErrCodeMethodNotFound, resp.Error.Code)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for -32601 response")
	}

	// 验证 agent 并没有崩溃，仍然能够继续正常上报指标
	if err := tr.Send(protocol.MethodAgentMetrics, protocol.MetricsParams{TsMs: time.Now().UnixMilli()}); err != nil {
		t.Fatalf("send metrics failed after unknown method: %v", err)
	}

	select {
	case req := <-metricsReceived:
		if req.Method != protocol.MethodAgentMetrics {
			t.Fatalf("expected agent.metrics, got %s", req.Method)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for metrics after unknown method")
	}
}

// 验收标准 4：打断 WS 三次后自动转 HTTP 回退，恢复后能切回 WS
func TestAcceptance_4_ThreeFailuresSwitchToFallbackAndRecover(t *testing.T) {
	var wsReject atomic.Bool
	wsReject.Store(true) // 初始打断 WS

	var httpPostCount atomic.Int32
	commandHandled := make(chan string, 5)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/agent/v1/rpc" {
			if wsReject.Load() {
				http.Error(w, "ws unavailable", http.StatusBadGateway)
				return
			}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}

		if r.URL.Path == "/agent/v1/report" {
			httpPostCount.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"server_time_ms": 1757222400000,
				"commands": [
					{"jsonrpc":"2.0","method":"server.config","params":{"interval_fast_s":10}}
				]
			}`))
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	tr, err := New(Config{
		ServerURL:             server.URL,
		Token:                 "test-token",
		DialTimeout:           50 * time.Millisecond,
		PingInterval:          100 * time.Millisecond,
		ReadTimeout:           200 * time.Millisecond,
		FallbackRetryInterval: 300 * time.Millisecond, // 快速恢复重试
		FastInterval:          50 * time.Millisecond,  // 快速批处理
		BackoffSequence:       []time.Duration{20 * time.Millisecond, 20 * time.Millisecond, 20 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer tr.Close()

	tr.OnServerMessage(func(method string, params json.RawMessage) (any, error) {
		commandHandled <- method
		return nil, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// 连续失败 3 次后自动转为 StateFallbackHTTP
	deadline := time.Now().Add(3 * time.Second)
	for tr.State() != StateFallbackHTTP && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if tr.State() != StateFallbackHTTP {
		t.Fatalf("expected StateFallbackHTTP after 3 failures, got %s", tr.State())
	}

	// 在 FallbackHTTP 模式下上报数据
	if err := tr.Send(protocol.MethodAgentMetrics, protocol.MetricsParams{TsMs: time.Now().UnixMilli()}); err != nil {
		t.Fatalf("Send failed in FallbackHTTP: %v", err)
	}

	// 验证 HTTP 回退接口收到了 POST 请求
	deadline = time.Now().Add(2 * time.Second)
	for httpPostCount.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if httpPostCount.Load() == 0 {
		t.Fatalf("expected HTTP POST report to be called in fallback mode")
	}

	// 验证 HTTP 响应里回带的 commands 交付给 Handler
	select {
	case cmd := <-commandHandled:
		if cmd != protocol.MethodServerConfig {
			t.Fatalf("expected command %s, got %s", protocol.MethodServerConfig, cmd)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for fallback command handler")
	}

	// 现在恢复 WS
	wsReject.Store(false)

	// 等待尝试恢复定时器触发并成功切回 WSConnected
	deadline = time.Now().Add(3 * time.Second)
	for tr.State() != StateWSConnected && time.Now().Before(deadline) {
		time.Sleep(30 * time.Millisecond)
	}
	if tr.State() != StateWSConnected {
		t.Fatalf("expected recovery to StateWSConnected, got %s", tr.State())
	}
}

// 验收标准 5：退避间隔的抖动可以在日志里观察到（不是固定的 1/2/4/8）
func TestAcceptance_5_BackoffJitterObservableInLogs(t *testing.T) {
	logger := &logCollector{}

	// 连接一个不存在的端口，连续触发退避重试
	tr, err := New(Config{
		ServerURL:       "http://127.0.0.1:1", // 端口 1 必定失败
		Token:           "test-token",
		DialTimeout:     20 * time.Millisecond,
		BackoffSequence: []time.Duration{100 * time.Millisecond, 200 * time.Millisecond},
		Logger:          logger,
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

	// 收集至少两次退避日志
	deadline := time.Now().Add(2 * time.Second)
	re := regexp.MustCompile(`backoff waiting ([0-9\.]+[mµn]?s) before retry`)

	var matchedDurations []string
	for time.Now().Before(deadline) {
		logs := logger.GetAll()
		matchedDurations = nil
		for _, l := range logs {
			matches := re.FindStringSubmatch(l)
			if len(matches) > 1 {
				matchedDurations = append(matchedDurations, matches[1])
			}
		}
		if len(matchedDurations) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if len(matchedDurations) < 2 {
		t.Fatalf("expected at least 2 backoff log lines, got: %v", logger.GetAll())
	}

	// 验证不是纯整数或固定值（比如 100ms 抖动后会是 80ms~120ms 之间的浮点/随机纳秒数）
	d1, err := time.ParseDuration(matchedDurations[0])
	if err != nil {
		t.Fatalf("parse duration %s failed: %v", matchedDurations[0], err)
	}
	d2, err := time.ParseDuration(matchedDurations[1])
	if err != nil {
		t.Fatalf("parse duration %s failed: %v", matchedDurations[1], err)
	}

	// 100ms 的基准在 [80ms, 120ms]
	if d1 < 80*time.Millisecond || d1 > 120*time.Millisecond {
		t.Fatalf("d1 %v outside of expected jitter range [80ms, 120ms]", d1)
	}
	// 200ms 的基准在 [160ms, 240ms]
	if d2 < 160*time.Millisecond || d2 > 240*time.Millisecond {
		t.Fatalf("d2 %v outside of expected jitter range [160ms, 240ms]", d2)
	}

	// 确保它不是恰好等于基准值 100ms 或 200ms（或者至少带有小数抖动）
	if d1 == 100*time.Millisecond && d2 == 200*time.Millisecond {
		t.Fatalf("expected jitter, but got exact base durations: %v, %v", d1, d2)
	}
}

// TestAcceptance_6_TransportHTTPMode 验收 105.md：
// 1. transport: "http" 下抓包：零次 WS 握手尝试，日志里没有 ws 相关行；
// 2. 直接进入 StateFallbackHTTP，不定时重试 WS；
// 3. 请求头里 X-Node 与 Authorization 都在，且值正确；
// 4. 端点路径是 /agent/v1/report。
func TestAcceptance_6_TransportHTTPMode(t *testing.T) {
	var wsHandshakeAttempts atomic.Int32
	var httpPostCount atomic.Int32
	var receivedAuth atomic.Pointer[string]
	var receivedNode atomic.Pointer[string]
	var receivedPath atomic.Pointer[string]
	commandHandled := make(chan string, 10)

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 监听 WebSocket 握手尝试
		if strings.HasPrefix(r.URL.Path, "/api/agent/v1/rpc") || strings.Contains(r.Header.Get("Upgrade"), "websocket") {
			wsHandshakeAttempts.Add(1)
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			_ = conn.Close()
			return
		}

		// 监听 HTTP 回退端点
		if r.URL.Path == "/agent/v1/report" {
			auth := r.Header.Get("Authorization")
			node := r.Header.Get("X-Node")
			path := r.URL.Path
			receivedAuth.Store(&auth)
			receivedNode.Store(&node)
			receivedPath.Store(&path)
			httpPostCount.Add(1)

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"server_time_ms": 1757222400999,
				"commands": [
					{"jsonrpc":"2.0","method":"server.config","params":{"interval_fast_s":5}}
				]
			}`))
			return
		}

		http.NotFound(w, r)
	}))
	defer server.Close()

	logger := &logCollector{}
	expectedToken := "test-secret-token-456"
	expectedNode := "tokyo-prod-01"

	tr, err := New(Config{
		ServerURL:             server.URL,
		Token:                 expectedToken,
		NodeID:                expectedNode,
		Transport:             "http",
		FastInterval:          30 * time.Millisecond,
		FallbackRetryInterval: 100 * time.Millisecond,
		Logger:                logger,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer tr.Close()

	tr.OnServerMessage(func(method string, params json.RawMessage) (any, error) {
		commandHandled <- method
		return nil, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// 1. 验证启动后直接进入 StateFallbackHTTP（无需等待 3 次 WS 失败）
	deadline := time.Now().Add(500 * time.Millisecond)
	for tr.State() != StateFallbackHTTP && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if tr.State() != StateFallbackHTTP {
		t.Fatalf("expected immediate StateFallbackHTTP in transport:http, got %s", tr.State())
	}

	// 2. 发送一条指标数据
	cpuPct := 25.0
	metricsParams := protocol.MetricsParams{
		TsMs:   time.Now().UnixMilli(),
		CPUPct: &cpuPct,
	}
	if err := tr.Send(protocol.MethodAgentMetrics, metricsParams); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	// 3. 等待 HTTP 上报完成
	deadline = time.Now().Add(2 * time.Second)
	for httpPostCount.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if httpPostCount.Load() == 0 {
		t.Fatalf("expected HTTP POST report to be delivered")
	}

	// 4. 判据 1：零次 WS 握手尝试
	time.Sleep(150 * time.Millisecond) // 等待超过 FallbackRetryInterval
	if wsCount := wsHandshakeAttempts.Load(); wsCount != 0 {
		t.Fatalf("expected 0 WS handshake attempts, got %d", wsCount)
	}

	// 5. 判据 1：日志里没有 ws 相关行
	logs := logger.GetAll()
	for _, line := range logs {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "ws") || strings.Contains(lower, "websocket") {
			t.Fatalf("found unexpected ws-related log in transport:http mode: %q", line)
		}
	}

	// 6. 判据 3：请求头里 X-Node 与 Authorization 都在，值正确
	authPtr := receivedAuth.Load()
	if authPtr == nil || *authPtr != "Bearer "+expectedToken {
		t.Fatalf("expected Authorization Bearer %s, got %v", expectedToken, authPtr)
	}
	nodePtr := receivedNode.Load()
	if nodePtr == nil || *nodePtr != expectedNode {
		t.Fatalf("expected X-Node %s, got %v", expectedNode, nodePtr)
	}

	// 7. 判据 4：端点路径是 /agent/v1/report
	pathPtr := receivedPath.Load()
	if pathPtr == nil || *pathPtr != "/agent/v1/report" {
		t.Fatalf("expected path /agent/v1/report, got %v", pathPtr)
	}

	// 8. 验证下行指令由 Handler 正确接收
	select {
	case cmd := <-commandHandled:
		if cmd != protocol.MethodServerConfig {
			t.Fatalf("expected command %s, got %s", protocol.MethodServerConfig, cmd)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for fallback command")
	}
}

// TestAcceptance_7_TransportAutoModeRegression 验收 105.md：
// transport: "auto" 下行为与改动前完全一致（回归），WS 正常可用时优先连接 WS。
func TestAcceptance_7_TransportAutoModeRegression(t *testing.T) {
	var wsConnected atomic.Bool
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/agent/v1/rpc") {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			wsConnected.Store(true)
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	tr, err := New(Config{
		ServerURL:    server.URL,
		Token:        "auto-token",
		NodeID:       "auto-node",
		Transport:    "auto", // 或省略
		PingInterval: 100 * time.Millisecond,
		ReadTimeout:  200 * time.Millisecond,
		DialTimeout:  100 * time.Millisecond,
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
	for !wsConnected.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !wsConnected.Load() {
		t.Fatalf("expected WebSocket connection to succeed in transport:auto mode")
	}
	if tr.State() != StateWSConnected {
		t.Fatalf("expected StateWSConnected, got %s", tr.State())
	}
}
