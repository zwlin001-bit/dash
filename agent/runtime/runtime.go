package runtime

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"dash/agent/collect"
	"dash/agent/collect/linux"
	"dash/agent/transport"
	"dash/internal/protocol"

	"github.com/gorilla/websocket"
)

// Runtime 是 Agent 的运行时主循环与生命周期管理器。
// 见 docs/agy/tasks/P1-08-agent主循环.md 与 docs/03-agent.md。
type Runtime struct {
	cfg       *Config
	version   string
	bootAtMs  int64
	transport transport.Transport
	state     *StateStore
	encoder   *MetricsEncoder

	sample      *collect.Sample
	factsSample *collect.Sample

	intervalFast     atomic.Int64
	intervalSlow     atomic.Int64
	factsMaxInterval atomic.Int64
	collectConns     atomic.Bool

	fastIntervalCh  chan time.Duration
	factsIntervalCh chan time.Duration

	factsMu          sync.Mutex
	currentFactsHash string
	lastSlow         time.Time

	metricsReportCount atomic.Int64
	factsReportCount   atomic.Int64

	connsCollector *linux.ConnsCollector
	diskCollector  *linux.DiskCollector
	netCollector   *linux.NetCollector
	memCollector   *linux.MemCollector
	factsCollector *linux.FactsCollector

	customTransport bool
}

// Option 定义 Runtime 初始化可选参数。
type Option func(*Runtime)

// WithTransport 允许注入自定义 Transport（主要供测试使用）。
func WithTransport(tr transport.Transport) Option {
	return func(r *Runtime) {
		r.transport = tr
		r.customTransport = true
	}
}

// WithStateStore 允许注入自定义 StateStore。
func WithStateStore(s *StateStore) Option {
	return func(r *Runtime) {
		r.state = s
	}
}

// New 创建 Agent 运行时实例。
func New(cfg *Config, version string, opts ...Option) (*Runtime, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	if version == "" {
		version = "0.1.0"
	}

	r := &Runtime{
		cfg:             cfg,
		version:         version,
		fastIntervalCh:  make(chan time.Duration, 4),
		factsIntervalCh: make(chan time.Duration, 4),
		encoder:         NewMetricsEncoder(4096),
		sample:          collect.NewSample(),
		factsSample:     collect.NewSample(),
	}

	r.intervalFast.Store(int64(cfg.IntervalFastS))
	r.intervalSlow.Store(int64(cfg.IntervalSlowS))
	r.factsMaxInterval.Store(int64(cfg.FactsMaxIntervalS))
	r.collectConns.Store(cfg.CollectConns)

	// 1. 初始化并配置采集器
	r.initCollectors()

	// 2. 初始化状态文件
	r.state = NewStateStore(cfg.StateFile)

	// 3. 应用外部选项
	for _, opt := range opts {
		opt(r)
	}

	// 4. 加载状态
	if err := r.state.Load(); err != nil {
		log.Printf("dash-agent: load state warning: %v", err)
	}

	// 5. 采集初始 facts 与开机时间
	r.initFacts()

	// 6. 初始化传输层（如果未通过选项注入）
	if !r.customTransport {
		tr, err := r.buildTransport()
		if err != nil {
			return nil, fmt.Errorf("build transport: %w", err)
		}
		r.transport = tr
	}

	// 7. 注册下行指令处理器
	r.transport.OnServerMessage(r.handleServerMessage)

	return r, nil
}

func (r *Runtime) initCollectors() {
	if c := collect.CollectorByCode("conns"); c != nil {
		if lc, ok := c.(*linux.ConnsCollector); ok {
			lc.Enabled = r.cfg.CollectConns
			r.connsCollector = lc
		}
	}
	if c := collect.CollectorByCode("disk"); c != nil {
		if lc, ok := c.(*linux.DiskCollector); ok {
			lc.IncludeMounts = r.cfg.IncludeMounts
			lc.ExcludeMounts = r.cfg.ExcludeMounts
			r.diskCollector = lc
		}
	}
	if c := collect.CollectorByCode("net"); c != nil {
		if lc, ok := c.(*linux.NetCollector); ok {
			lc.IncludeNICs = r.cfg.IncludeNICs
			lc.ExcludeNICs = r.cfg.ExcludeNICs
			r.netCollector = lc
		}
	}
	if c := collect.CollectorByCode("mem"); c != nil {
		if lc, ok := c.(*linux.MemCollector); ok {
			lc.IncludeCache = r.cfg.MemIncludeCache
			r.memCollector = lc
		}
	}
	if c := collect.CollectorByCode("facts"); c != nil {
		if lc, ok := c.(*linux.FactsCollector); ok {
			r.factsCollector = lc
		}
	}
}

func (r *Runtime) initFacts() {
	r.factsMu.Lock()
	defer r.factsMu.Unlock()

	r.factsSample.Reset(collect.TierFacts)
	_ = collect.CollectTier(collect.TierFacts, r.factsSample)

	if r.factsSample.Facts != nil {
		r.currentFactsHash = r.factsSample.Facts.FactsHash
		if r.factsSample.Facts.BootAtMs > 0 {
			r.bootAtMs = r.factsSample.Facts.BootAtMs
		}
	}
	if r.bootAtMs == 0 {
		r.bootAtMs = time.Now().UnixMilli()
	}
}

func (r *Runtime) buildTransport() (transport.Transport, error) {
	wsDialer := &websocket.Dialer{
		HandshakeTimeout:  10 * time.Second,
		EnableCompression: true,
	}
	if r.cfg.InsecureSkipVerify {
		wsDialer.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}
	if r.cfg.InsecureSkipVerify {
		httpClient.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}

	trCfg := transport.Config{
		ServerURL:    r.cfg.Endpoint,
		Token:        r.cfg.Token,
		PingInterval: 30 * time.Second,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 10 * time.Second,
		FastInterval: time.Duration(r.intervalFast.Load()) * time.Second,
		WSDialer:     wsDialer,
		HTTPClient:   httpClient,
		OnConnected:  r.onWSConnected,
	}

	return transport.New(trCfg)
}

// onWSConnected 在 WebSocket 连接就绪后由 Transport 异步调用。
// 发起 agent.hello request 握手并同步服务端配置策略。
func (r *Runtime) onWSConnected() {
	r.factsMu.Lock()
	factsHash := r.currentFactsHash
	r.factsMu.Unlock()

	helloParams := protocol.HelloParams{
		ProtocolVersion: 1,
		AgentVersion:    r.version,
		BootAtMs:        r.bootAtMs,
		Capabilities:    []string{"metrics", "facts"},
		FactsHash:       factsHash,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rawResult, err := r.transport.Call(ctx, protocol.MethodAgentHello, helloParams)
	if err != nil {
		log.Printf("dash-agent: agent.hello error: %v", err)
		return
	}

	var helloRes protocol.HelloResult
	if err := json.Unmarshal(rawResult, &helloRes); err != nil {
		log.Printf("dash-agent: parse agent.hello result error: %v", err)
		return
	}

	// 采纳服务端策略（服务端参数优先）
	if helloRes.IntervalFastS > 0 {
		r.setIntervalFast(helloRes.IntervalFastS)
	}
	if helloRes.IntervalSlowS > 0 {
		r.setIntervalSlow(helloRes.IntervalSlowS)
	}
	if helloRes.FactsMaxIntervalS > 0 {
		r.setFactsMaxInterval(helloRes.FactsMaxIntervalS)
	}
	r.setCollectConns(helloRes.CollectConns)

	// 如果服务端需要 facts，或者本地存储的 facts_hash 与当前不一致，立即上报 facts
	savedHash := r.state.FactsHash()
	if helloRes.NeedFacts || factsHash != savedHash {
		r.sendFactsReport()
		if r.state.UpdateFactsHash(factsHash) {
			_ = r.state.FlushIfDue()
		}
	}
}

// handleServerMessage 处理服务端下发的指令与通知。
func (r *Runtime) handleServerMessage(method string, params json.RawMessage) (any, error) {
	switch method {
	case protocol.MethodServerConfig:
		var p protocol.ServerConfigParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &protocol.RPCError{
				Code:    protocol.ErrCodeInvalidParams,
				Message: "invalid server.config params",
			}
		}
		r.applyServerConfig(&p)
		return nil, nil
	default:
		return nil, transport.ErrMethodNotFound
	}
}

func (r *Runtime) applyServerConfig(p *protocol.ServerConfigParams) {
	if p.IntervalFastS != nil && *p.IntervalFastS > 0 {
		r.setIntervalFast(*p.IntervalFastS)
	}
	if p.IntervalSlowS != nil && *p.IntervalSlowS > 0 {
		r.setIntervalSlow(*p.IntervalSlowS)
	}
	if p.FactsMaxIntervalS != nil && *p.FactsMaxIntervalS > 0 {
		r.setFactsMaxInterval(*p.FactsMaxIntervalS)
	}
	if p.CollectConns != nil {
		r.setCollectConns(*p.CollectConns)
	}
	if p.NeedFacts != nil && *p.NeedFacts {
		r.sendFactsReport()
	}
}

func (r *Runtime) setIntervalFast(sec int) {
	if sec <= 0 {
		return
	}
	old := r.intervalFast.Swap(int64(sec))
	if old != int64(sec) {
		log.Printf("dash-agent: interval_fast updated: %ds -> %ds", old, sec)
		select {
		case r.fastIntervalCh <- time.Duration(sec) * time.Second:
		default:
		}
	}
}

func (r *Runtime) setIntervalSlow(sec int) {
	if sec <= 0 {
		return
	}
	old := r.intervalSlow.Swap(int64(sec))
	if old != int64(sec) {
		log.Printf("dash-agent: interval_slow updated: %ds -> %ds", old, sec)
	}
}

func (r *Runtime) setFactsMaxInterval(sec int) {
	if sec <= 0 {
		return
	}
	old := r.factsMaxInterval.Swap(int64(sec))
	if old != int64(sec) {
		log.Printf("dash-agent: facts_max_interval updated: %ds -> %ds", old, sec)
		select {
		case r.factsIntervalCh <- time.Duration(sec) * time.Second:
		default:
		}
	}
}

func (r *Runtime) setCollectConns(enabled bool) {
	r.collectConns.Store(enabled)
	if r.connsCollector != nil {
		r.connsCollector.Enabled = enabled
	}
}

// sendFactsReport 采样并上报 agent.facts 通知。
func (r *Runtime) sendFactsReport() {
	r.factsMu.Lock()
	defer r.factsMu.Unlock()

	r.factsSample.Reset(collect.TierFacts)
	_ = collect.CollectTier(collect.TierFacts, r.factsSample)

	if r.factsSample.Facts != nil {
		r.currentFactsHash = r.factsSample.Facts.FactsHash
		_ = r.transport.Send(protocol.MethodAgentFacts, r.factsSample.Facts)
		r.factsReportCount.Add(1)
	}
}

// Run 启动 Agent 运行时，驱动三档定时器并阻塞直到 context 取消。
func (r *Runtime) Run(ctx context.Context) error {
	log.Printf("dash-agent runtime starting (fast=%ds, slow=%ds, facts_max=%ds)",
		r.intervalFast.Load(), r.intervalSlow.Load(), r.factsMaxInterval.Load())

	// 启动传输层
	if err := r.transport.Start(ctx); err != nil {
		return fmt.Errorf("start transport: %w", err)
	}

	fastInterval := time.Duration(r.intervalFast.Load()) * time.Second
	fastTicker := time.NewTicker(fastInterval)
	defer fastTicker.Stop()

	factsInterval := time.Duration(r.factsMaxInterval.Load()) * time.Second
	factsTicker := time.NewTicker(factsInterval)
	defer factsTicker.Stop()

	stateTicker := time.NewTicker(time.Minute)
	defer stateTicker.Stop()

	r.lastSlow = time.Time{}

	for {
		select {
		case <-ctx.Done():
			log.Printf("dash-agent shutting down gracefully...")
			_ = r.state.Flush()
			_ = r.transport.Close()
			return nil

		case newFast := <-r.fastIntervalCh:
			fastTicker.Reset(newFast)

		case newFacts := <-r.factsIntervalCh:
			factsTicker.Reset(newFacts)

		case <-fastTicker.C:
			r.tickFast()

		case <-factsTicker.C:
			// facts 兜底定时器（默认 30 分钟）
			r.sendFactsReport()

		case <-stateTicker.C:
			_ = r.state.FlushIfDue()
		}
	}
}

// tickFast 执行 fast 档采样并发送 agent.metrics 报文。
// 若 slow 周期已到，将 slow 档数据搭在本次 fast 报文中一起发送。
func (r *Runtime) tickFast() {
	now := time.Now()
	slowInterval := time.Duration(r.intervalSlow.Load()) * time.Second
	isSlowDue := r.lastSlow.IsZero() || now.Sub(r.lastSlow) >= slowInterval

	// 1. 采集 fast 档
	r.sample.Reset(collect.TierFast)
	r.sample.TsMs = now.UnixMilli()
	_ = collect.CollectTier(collect.TierFast, r.sample)

	// 2. 如果 slow 档到期，采集 slow 档
	if isSlowDue {
		r.sample.Reset(collect.TierSlow)
		_ = collect.CollectTier(collect.TierSlow, r.sample)
		r.lastSlow = now

		// 检查 facts 是否有变动
		r.checkFactsChanged()
	} else {
		// 确保不携带 slow 数据
		r.sample.Reset(collect.TierSlow)
	}

	// 3. 复用编码缓冲序列化为 JSON
	params := r.sample.ToMetricsParams()
	payload := r.encoder.Encode(params)

	// 4. 发送通知
	_ = r.transport.Send(protocol.MethodAgentMetrics, json.RawMessage(payload))
	r.metricsReportCount.Add(1)
}

// checkFactsChanged 检查 facts 是否变化，仅在变更时主动上报。
func (r *Runtime) checkFactsChanged() {
	r.factsMu.Lock()
	defer r.factsMu.Unlock()

	r.factsSample.Reset(collect.TierFacts)
	_ = collect.CollectTier(collect.TierFacts, r.factsSample)

	if r.factsSample.Facts != nil {
		newHash := r.factsSample.Facts.FactsHash
		if newHash != "" && newHash != r.currentFactsHash {
			log.Printf("dash-agent: facts_hash changed: %s -> %s, reporting", r.currentFactsHash, newHash)
			r.currentFactsHash = newHash
			_ = r.transport.Send(protocol.MethodAgentFacts, r.factsSample.Facts)
			r.factsReportCount.Add(1)
			if r.state.UpdateFactsHash(newHash) {
				_ = r.state.FlushIfDue()
			}
		}
	}
}

// MetricsReportCount 返回已发送的 metrics 报文计数（供测试与验收统计）。
func (r *Runtime) MetricsReportCount() int64 {
	return r.metricsReportCount.Load()
}

// FactsReportCount 返回已发送的 facts 报文计数（供测试与验收统计）。
func (r *Runtime) FactsReportCount() int64 {
	return r.factsReportCount.Load()
}

// TriggerOnWSConnected 手动触发 onWSConnected（供测试使用）。
func (r *Runtime) TriggerOnWSConnected() {
	r.onWSConnected()
}
