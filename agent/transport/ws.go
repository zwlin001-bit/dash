package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dash/internal/protocol"

	"github.com/gorilla/websocket"
)

// Sentinel errors
var (
	ErrNotConnected   = errors.New("transport: not connected")
	ErrQueueFull      = errors.New("transport: queue full, message dropped")
	ErrStopped        = errors.New("transport: stopped, token invalid")
	ErrCallTimeout    = errors.New("transport: call timeout")
	ErrMethodNotFound = &protocol.RPCError{
		Code:    protocol.ErrCodeMethodNotFound,
		Message: "method not found",
	}
)

// Handler 处理服务端下行的请求与通知。
type Handler func(method string, params json.RawMessage) (result any, err error)

// ConnState 表示传输层的连接状态。
type ConnState int

const (
	StateDisconnected ConnState = iota
	StateConnecting
	StateWSConnected
	StateFallbackHTTP
	StateStopped // 收到 -32000，不再重连
)

func (s ConnState) String() string {
	switch s {
	case StateDisconnected:
		return "Disconnected"
	case StateConnecting:
		return "Connecting"
	case StateWSConnected:
		return "WSConnected"
	case StateFallbackHTTP:
		return "FallbackHTTP"
	case StateStopped:
		return "Stopped"
	default:
		return fmt.Sprintf("Unknown(%d)", s)
	}
}

// Transport 传输层对外契约。
type Transport interface {
	// Start 启动连接与重连循环，非阻塞
	Start(ctx context.Context) error
	// Send 发送一条 notification。连接不可用时直接丢弃并返回错误，★ 绝不阻塞调用方
	Send(method string, params any) error
	// Call 发送 request 并等待应答（仅 agent.hello 用）
	Call(ctx context.Context, method string, params any) (json.RawMessage, error)
	// OnServerMessage 注册下行消息处理器
	OnServerMessage(h Handler)
	// State 返回当前连接状态，供主循环判断
	State() ConnState
	Close() error
}

// Logger 结构化日志接口。
type Logger interface {
	Printf(format string, v ...any)
}

type defaultLogger struct{}

func (l defaultLogger) Printf(format string, v ...any) {
	log.Printf(format, v...)
}

// Config 传输层配置。
type Config struct {
	ServerURL             string        // 服务端地址（如 "wss://example.com" 或 "http://127.0.0.1:8080"）
	Token                 string        // 认证 Token (Bearer)
	NodeID                string        // 节点标识（用于 HTTP 回退上报的 X-Node 请求头）
	Transport             string        // 传输模式："auto"（默认）或 "http"（纯 HTTP 回退，不尝试 WS）
	PingInterval          time.Duration // 心跳发送间隔，默认 30s
	ReadTimeout           time.Duration // 心跳与读超时，默认 60s
	WriteTimeout          time.Duration // 写超时，默认 10s
	FallbackRetryInterval time.Duration // FallbackHTTP 状态下尝试恢复 WS 的间隔，默认 60s
	FastInterval          time.Duration // FallbackHTTP 状态下攒批上报间隔，默认 5s
	DialTimeout           time.Duration // 拨号握手超时，默认 10s
	BackoffSequence       []time.Duration
	HTTPClient            *http.Client
	WSDialer              *websocket.Dialer
	Logger                Logger
	OnConnected           func() // WS 连接就绪回调
}

type writeMsg struct {
	payload []byte
}

type wsTransport struct {
	cfg     Config
	wsURL   string
	httpURL string

	state   int32 // atomic ConnState
	stateMu sync.Mutex

	handler   Handler
	handlerMu sync.RWMutex

	startOnce sync.Once
	closeOnce sync.Once
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup

	connMu  sync.Mutex
	conn    *websocket.Conn
	writeCh chan *writeMsg

	pendingMu sync.Mutex
	pending   map[int64]chan *protocol.Response
	nextReqID int64

	fbMu     sync.Mutex
	fbBatch  []*protocol.Request
	fbClient *HTTPFallbackClient

	backoff   *Backoff
	failCount int
	logger    Logger
}

const (
	defaultWriteQueueCap  = 32
	defaultFallbackCap    = 32
	compressionThreshold  = 1024 // 1 KB
)

// New 创建 Transport 实例。
func New(cfg Config) (Transport, error) {
	wsURL, httpURL, err := resolveEndpoints(cfg.ServerURL)
	if err != nil {
		return nil, fmt.Errorf("resolve transport endpoints: %w", err)
	}

	if cfg.Transport == "" {
		cfg.Transport = "auto"
	}
	if cfg.PingInterval <= 0 {
		cfg.PingInterval = 30 * time.Second
	}
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = 60 * time.Second
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = 10 * time.Second
	}
	if cfg.FallbackRetryInterval <= 0 {
		cfg.FallbackRetryInterval = 60 * time.Second
	}
	if cfg.FastInterval <= 0 {
		cfg.FastInterval = 5 * time.Second
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 10 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = defaultLogger{}
	}
	if cfg.WSDialer == nil {
		cfg.WSDialer = &websocket.Dialer{
			HandshakeTimeout:  cfg.DialTimeout,
			EnableCompression: true,
		}
	} else {
		cfg.WSDialer.EnableCompression = true
	}

	fbClient := NewHTTPFallbackClient(httpURL, cfg.Token, cfg.HTTPClient, cfg.NodeID)

	var b *Backoff
	if len(cfg.BackoffSequence) > 0 {
		b = NewBackoff(cfg.BackoffSequence...)
	} else {
		b = NewBackoff()
	}

	t := &wsTransport{
		cfg:       cfg,
		wsURL:     wsURL,
		httpURL:   httpURL,
		writeCh:   make(chan *writeMsg, defaultWriteQueueCap),
		pending:   make(map[int64]chan *protocol.Response),
		fbClient:  fbClient,
		backoff:   b,
		logger:    cfg.Logger,
	}
	atomic.StoreInt32(&t.state, int32(StateDisconnected))

	return t, nil
}

// Start 启动连接与重连循环，非阻塞。
func (t *wsTransport) Start(ctx context.Context) error {
	t.startOnce.Do(func() {
		t.ctx, t.cancel = context.WithCancel(ctx)
		t.wg.Add(1)
		go t.runLoop()
	})
	return nil
}

// State 返回当前连接状态。
func (t *wsTransport) State() ConnState {
	return ConnState(atomic.LoadInt32(&t.state))
}

func (t *wsTransport) transitionTo(s ConnState) {
	t.stateMu.Lock()
	defer t.stateMu.Unlock()
	cur := ConnState(atomic.LoadInt32(&t.state))
	if cur == StateStopped {
		// 收到 -32000 为终态，不再迁移
		return
	}
	if cur != s {
		atomic.StoreInt32(&t.state, int32(s))
		t.logf("[transport] state changed: %s -> %s", cur, s)
	}
}

// OnServerMessage 注册下行消息处理器。
func (t *wsTransport) OnServerMessage(h Handler) {
	t.handlerMu.Lock()
	defer t.handlerMu.Unlock()
	t.handler = h
}

// Send 发送一条 notification。连接不可用时直接丢弃并返回错误，★ 绝不阻塞调用方。
func (t *wsTransport) Send(method string, params any) error {
	state := t.State()
	if state == StateStopped {
		return ErrStopped
	}
	if state != StateWSConnected && state != StateFallbackHTTP {
		return ErrNotConnected
	}

	rawParams, err := marshalParams(params)
	if err != nil {
		return fmt.Errorf("marshal params: %w", err)
	}

	req := &protocol.Request{
		JSONRPC: protocol.JSONRPCVersion,
		Method:  method,
		Params:  rawParams,
	}

	if state == StateWSConnected {
		payload, err := json.Marshal(req)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		select {
		case t.writeCh <- &writeMsg{payload: payload}:
			return nil
		default:
			// 连接不可用或发送队列满，直接丢弃
			return ErrQueueFull
		}
	}

	// StateFallbackHTTP
	return t.enqueueFallback(req)
}

func (t *wsTransport) enqueueFallback(req *protocol.Request) error {
	t.fbMu.Lock()
	defer t.fbMu.Unlock()

	if len(t.fbBatch) >= defaultFallbackCap {
		return ErrQueueFull
	}
	t.fbBatch = append(t.fbBatch, req)
	return nil
}

// Call 发送 request 并等待应答（仅 agent.hello 用）。
func (t *wsTransport) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	state := t.State()
	if state == StateStopped {
		return nil, ErrStopped
	}
	if state != StateWSConnected {
		return nil, ErrNotConnected
	}

	rawParams, err := marshalParams(params)
	if err != nil {
		return nil, fmt.Errorf("marshal params: %w", err)
	}

	reqID := atomic.AddInt64(&t.nextReqID, 1)
	req := &protocol.Request{
		JSONRPC: protocol.JSONRPCVersion,
		ID:      &reqID,
		Method:  method,
		Params:  rawParams,
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	respCh := make(chan *protocol.Response, 1)
	t.pendingMu.Lock()
	t.pending[reqID] = respCh
	t.pendingMu.Unlock()

	defer func() {
		t.pendingMu.Lock()
		delete(t.pending, reqID)
		t.pendingMu.Unlock()
	}()

	select {
	case t.writeCh <- &writeMsg{payload: payload}:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.ctx.Done():
		return nil, ErrNotConnected
	}

	select {
	case resp, ok := <-respCh:
		if !ok || resp == nil {
			return nil, ErrNotConnected
		}
		if resp.Error != nil {
			if resp.Error.Code == protocol.ErrCodeTokenInvalid {
				t.transitionTo(StateStopped)
			}
			return nil, resp.Error
		}
		return resp.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.ctx.Done():
		return nil, ErrNotConnected
	}
}

// Close 关闭传输层，释放资源。
func (t *wsTransport) Close() error {
	t.closeOnce.Do(func() {
		if t.cancel != nil {
			t.cancel()
		}

		t.connMu.Lock()
		if t.conn != nil {
			_ = t.conn.Close()
		}
		t.connMu.Unlock()

		t.wg.Wait()

		t.stateMu.Lock()
		if ConnState(atomic.LoadInt32(&t.state)) != StateStopped {
			atomic.StoreInt32(&t.state, int32(StateDisconnected))
		}
		t.stateMu.Unlock()

		t.drainWriteCh()
		t.notifyPendingClosed()
	})
	return nil
}

func (t *wsTransport) isHTTPOnly() bool {
	return strings.ToLower(strings.TrimSpace(t.cfg.Transport)) == "http"
}

func (t *wsTransport) runLoop() {
	defer t.wg.Done()

	if t.isHTTPOnly() {
		t.transitionTo(StateFallbackHTTP)
		t.runHTTPOnlyLoop()
		return
	}

	for {
		if t.State() == StateStopped {
			return
		}
		select {
		case <-t.ctx.Done():
			return
		default:
		}

		t.transitionTo(StateConnecting)
		conn, resp, err := t.dialWS()
		if err != nil {
			if resp != nil && resp.StatusCode == http.StatusUnauthorized {
				t.logf("[transport] token invalid or revoked (HTTP 401), stopping reconnection permanently")
				t.transitionTo(StateStopped)
				return
			}

			t.failCount++
			if t.failCount >= 3 {
				t.logf("[transport] ws dial failed %d times: %v; switching to FallbackHTTP", t.failCount, err)
				t.transitionTo(StateFallbackHTTP)
				if t.runFallbackHTTP() {
					return
				}
				continue
			}

			delay := t.backoff.Next()
			t.logf("[transport] ws dial failed (fail_count=%d): %v; backoff waiting %v before retry", t.failCount, err, delay)
			t.transitionTo(StateDisconnected)

			select {
			case <-time.After(delay):
			case <-t.ctx.Done():
				return
			}
			continue
		}

		// 拨号成功
		t.failCount = 0
		t.backoff.Reset()
		t.transitionTo(StateWSConnected)
		t.logf("[transport] websocket connected to %s", t.wsURL)

		t.runWSSession(conn)

		if t.State() == StateStopped {
			return
		}

		t.failCount++
		t.logf("[transport] websocket disconnected (fail_count=%d)", t.failCount)

		if t.failCount >= 3 {
			t.logf("[transport] ws continuous failure count reaches %d, switching to FallbackHTTP", t.failCount)
			t.transitionTo(StateFallbackHTTP)
			if t.runFallbackHTTP() {
				return
			}
			continue
		}

		delay := t.backoff.Next()
		t.logf("[transport] backoff waiting %v before reconnect", delay)
		t.transitionTo(StateDisconnected)

		select {
		case <-time.After(delay):
		case <-t.ctx.Done():
			return
		}
	}
}

func (t *wsTransport) runWSSession(conn *websocket.Conn) {
	t.connMu.Lock()
	t.conn = conn
	t.connMu.Unlock()

	done := make(chan struct{})
	var closeOnce sync.Once
	closeDone := func() {
		closeOnce.Do(func() {
			close(done)
		})
	}

	t.drainWriteCh()

	var sessionWG sync.WaitGroup
	sessionWG.Add(2)

	go func() {
		defer sessionWG.Done()
		defer closeDone()
		t.readPump(conn)
	}()

	go func() {
		defer sessionWG.Done()
		defer closeDone()
		t.writePump(conn, done)
	}()

	if t.cfg.OnConnected != nil {
		go t.cfg.OnConnected()
	}

	select {
	case <-done:
	case <-t.ctx.Done():
		closeDone()
	}

	_ = conn.Close()
	sessionWG.Wait()

	t.connMu.Lock()
	t.conn = nil
	t.connMu.Unlock()

	t.drainWriteCh()
	t.notifyPendingClosed()
}

func (t *wsTransport) readPump(conn *websocket.Conn) {
	defer func() {
		_ = conn.Close()
	}()

	_ = conn.SetReadDeadline(time.Now().Add(t.cfg.ReadTimeout))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(t.cfg.ReadTimeout))
		return nil
	})

	for {
		msgType, payload, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				t.logf("[transport] ws read error: %v", err)
			}
			return
		}

		_ = conn.SetReadDeadline(time.Now().Add(t.cfg.ReadTimeout))

		if msgType != websocket.TextMessage {
			continue
		}

		t.handleIncomingMessage(payload)
	}
}

func (t *wsTransport) writePump(conn *websocket.Conn, done <-chan struct{}) {
	ticker := time.NewTicker(t.cfg.PingInterval)
	defer func() {
		ticker.Stop()
		_ = conn.Close()
	}()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			_ = conn.SetWriteDeadline(time.Now().Add(t.cfg.WriteTimeout))
			if err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(t.cfg.WriteTimeout)); err != nil {
				t.logf("[transport] ws ping failed: %v", err)
				return
			}
		case msg, ok := <-t.writeCh:
			if !ok {
				return
			}
			_ = conn.SetWriteDeadline(time.Now().Add(t.cfg.WriteTimeout))
			// 小于 1 KB 的报文不压缩
			conn.EnableWriteCompression(len(msg.payload) >= compressionThreshold)
			if err := conn.WriteMessage(websocket.TextMessage, msg.payload); err != nil {
				t.logf("[transport] ws write failed: %v", err)
				return
			}
		}
	}
}

func (t *wsTransport) handleIncomingMessage(payload []byte) {
	var probe struct {
		JSONRPC string             `json:"jsonrpc"`
		ID      *int64             `json:"id,omitempty"`
		Method  string             `json:"method,omitempty"`
		Result  json.RawMessage    `json:"result,omitempty"`
		Error   *protocol.RPCError `json:"error,omitempty"`
		Params  json.RawMessage    `json:"params,omitempty"`
	}

	if err := json.Unmarshal(payload, &probe); err != nil {
		t.logf("[transport] parse json-rpc message error: %v", err)
		t.sendResponse(nil, nil, &protocol.RPCError{
			Code:    protocol.ErrCodeParse,
			Message: "parse error",
		})
		return
	}

	// 收到 -32000 (token 无效/已吊销) 停止重连
	if probe.Error != nil && probe.Error.Code == protocol.ErrCodeTokenInvalid {
		t.logf("[transport] received token invalid error (-32000), stopping permanently")
		t.transitionTo(StateStopped)
	}

	// Case A: 对应本地 Call 的 Response (无 method 且有 id)
	if probe.Method == "" && probe.ID != nil {
		t.pendingMu.Lock()
		ch, exists := t.pending[*probe.ID]
		t.pendingMu.Unlock()

		if exists {
			select {
			case ch <- &protocol.Response{
				JSONRPC: probe.JSONRPC,
				ID:      probe.ID,
				Result:  probe.Result,
				Error:   probe.Error,
			}:
			default:
			}
		}
		return
	}

	// Case B: 服务端下发的指令或通知
	if probe.Method != "" {
		t.dispatchServerMessage(probe.ID, probe.Method, probe.Params)
		return
	}
}

func (t *wsTransport) dispatchServerMessage(id *int64, method string, params json.RawMessage) {
	t.handlerMu.RLock()
	h := t.handler
	t.handlerMu.RUnlock()

	if h == nil {
		if id != nil {
			t.sendResponse(id, nil, ErrMethodNotFound)
		}
		return
	}

	result, err := h(method, params)
	if id == nil {
		// Notification 不回包
		if err != nil {
			t.logf("[transport] handler error for notification %s: %v", method, err)
		}
		return
	}

	// Request 必须回复响应
	if err != nil {
		var rpcErr *protocol.RPCError
		if errors.As(err, &rpcErr) {
			t.sendResponse(id, nil, rpcErr)
		} else {
			t.sendResponse(id, nil, &protocol.RPCError{
				Code:    protocol.ErrCodeMethodNotFound,
				Message: err.Error(),
			})
		}
		return
	}

	t.sendResponse(id, result, nil)
}

func (t *wsTransport) sendResponse(id *int64, result any, rpcErr *protocol.RPCError) {
	var rawResult json.RawMessage
	if result != nil {
		data, err := json.Marshal(result)
		if err != nil {
			t.logf("[transport] marshal response result: %v", err)
			return
		}
		rawResult = json.RawMessage(data)
	}

	resp := protocol.Response{
		JSONRPC: protocol.JSONRPCVersion,
		ID:      id,
		Result:  rawResult,
		Error:   rpcErr,
	}

	payload, err := json.Marshal(resp)
	if err != nil {
		t.logf("[transport] marshal response envelope: %v", err)
		return
	}

	select {
	case t.writeCh <- &writeMsg{payload: payload}:
	default:
		t.logf("[transport] write channel full, dropped response id %v", id)
	}
}

func (t *wsTransport) runFallbackHTTP() (stopped bool) {
	retryTicker := time.NewTicker(t.cfg.FallbackRetryInterval)
	defer retryTicker.Stop()

	flushTicker := time.NewTicker(t.cfg.FastInterval)
	defer flushTicker.Stop()

	for {
		if t.State() == StateStopped {
			return true
		}

		select {
		case <-t.ctx.Done():
			return false

		case <-flushTicker.C:
			if err := t.flushFallbackBatch(); err != nil {
				if t.State() == StateStopped {
					return true
				}
			}

		case <-retryTicker.C:
			// 每 60s 尝试恢复 WS
			t.transitionTo(StateConnecting)
			t.logf("[transport] attempting to recover websocket connection from fallback mode")

			conn, resp, err := t.dialWS()
			if err != nil {
				if resp != nil && resp.StatusCode == http.StatusUnauthorized {
					t.logf("[transport] token invalid on ws recovery (401), stopping")
					t.transitionTo(StateStopped)
					return true
				}
				t.logf("[transport] websocket recovery attempt failed: %v, remaining in FallbackHTTP", err)
				t.transitionTo(StateFallbackHTTP)
				continue
			}

			// 恢复成功
			t.logf("[transport] websocket recovered successfully, exiting FallbackHTTP")
			t.failCount = 0
			t.backoff.Reset()
			t.transitionTo(StateWSConnected)

			// 刷新剩余 HTTP 批次
			_ = t.flushFallbackBatch()

			t.runWSSession(conn)

			if t.State() == StateStopped {
				return true
			}

			t.failCount++
			if t.failCount < 3 {
				// 转回退避重连流程
				return false
			}
			// 再次连续失败 ≥3，留在 FallbackHTTP
			t.transitionTo(StateFallbackHTTP)
		}
	}
}

func (t *wsTransport) runHTTPOnlyLoop() {
	flushTicker := time.NewTicker(t.cfg.FastInterval)
	defer flushTicker.Stop()

	for {
		if t.State() == StateStopped {
			return
		}

		select {
		case <-t.ctx.Done():
			return

		case <-flushTicker.C:
			if err := t.flushFallbackBatch(); err != nil {
				if t.State() == StateStopped {
					return
				}
			}
		}
	}
}

func (t *wsTransport) flushFallbackBatch() error {
	t.fbMu.Lock()
	if len(t.fbBatch) == 0 {
		t.fbMu.Unlock()
		return nil
	}
	batch := t.fbBatch
	t.fbBatch = nil
	t.fbMu.Unlock()

	resp, err := t.fbClient.PostReport(t.ctx, batch)
	if err != nil {
		var rpcErr *protocol.RPCError
		if errors.As(err, &rpcErr) && rpcErr.Code == protocol.ErrCodeTokenInvalid {
			t.transitionTo(StateStopped)
			return err
		}
		t.logf("[transport] http fallback report error: %v", err)
		return err
	}

	// 顺序处理下行 commands
	t.handlerMu.RLock()
	h := t.handler
	t.handlerMu.RUnlock()

	if h != nil && len(resp.Commands) > 0 {
		for _, cmd := range resp.Commands {
			if _, err := h(cmd.Method, cmd.Params); err != nil {
				t.logf("[transport] fallback command handler error (%s): %v", cmd.Method, err)
			}
		}
	}

	return nil
}

func (t *wsTransport) dialWS() (*websocket.Conn, *http.Response, error) {
	header := http.Header{}
	header.Set("Authorization", "Bearer "+t.cfg.Token)
	if t.cfg.NodeID != "" {
		header.Set("X-Node", t.cfg.NodeID)
	}

	dialer := t.cfg.WSDialer
	if dialer == nil {
		dialer = &websocket.Dialer{
			HandshakeTimeout:  t.cfg.DialTimeout,
			EnableCompression: true,
		}
	}

	conn, resp, err := dialer.DialContext(t.ctx, t.wsURL, header)
	return conn, resp, err
}

func (t *wsTransport) drainWriteCh() {
	for {
		select {
		case <-t.writeCh:
		default:
			return
		}
	}
}

func (t *wsTransport) notifyPendingClosed() {
	t.pendingMu.Lock()
	defer t.pendingMu.Unlock()

	for id, ch := range t.pending {
		select {
		case ch <- nil:
		default:
		}
		delete(t.pending, id)
	}
}

func (t *wsTransport) logf(format string, v ...any) {
	if t.logger != nil {
		t.logger.Printf(format, v...)
	}
}

func resolveEndpoints(rawURL string) (wsURL string, httpURL string, err error) {
	if strings.TrimSpace(rawURL) == "" {
		return "", "", errors.New("server URL cannot be empty")
	}
	if !strings.Contains(rawURL, "://") {
		rawURL = "https://" + rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", "", fmt.Errorf("invalid server URL: %w", err)
	}

	var wsScheme, httpScheme string
	switch strings.ToLower(u.Scheme) {
	case "ws":
		wsScheme = "ws"
		httpScheme = "http"
	case "wss":
		wsScheme = "wss"
		httpScheme = "https"
	case "http":
		wsScheme = "ws"
		httpScheme = "http"
	case "https":
		wsScheme = "wss"
		httpScheme = "https"
	default:
		return "", "", fmt.Errorf("unsupported scheme: %s", u.Scheme)
	}

	basePath := strings.TrimRight(u.Path, "/")
	wsPath := basePath + "/api/agent/v1/rpc"
	httpPath := basePath + ReportEndpointPath

	wsU := *u
	wsU.Scheme = wsScheme
	wsU.Path = wsPath

	httpU := *u
	httpU.Scheme = httpScheme
	httpU.Path = httpPath

	return wsU.String(), httpU.String(), nil
}

func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	switch p := params.(type) {
	case json.RawMessage:
		return p, nil
	case []byte:
		return json.RawMessage(p), nil
	case string:
		return json.RawMessage(p), nil
	default:
		data, err := json.Marshal(p)
		if err != nil {
			return nil, err
		}
		return json.RawMessage(data), nil
	}
}
