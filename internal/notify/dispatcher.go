package notify

import (
	"context"
	"fmt"
	"sync"
	"time"

	"dash/internal/events"
	"dash/internal/logx"
	"dash/internal/notify/sender"
	"dash/internal/ulid"
)

// DispatchTask represents a pending delivery task to be executed by background workers.
type DispatchTask struct {
	DeliveryID   string
	ChannelID    string
	RenderedText string
	Attempt      int
}

// Dispatcher coordinates event routing, template rendering, and async notification delivery.
type Dispatcher struct {
	store        *Store
	router       *Router
	engine       *TemplateEngine
	siteDomain   string
	queue        chan DispatchTask
	stop         chan struct{}
	wg           sync.WaitGroup
	mu           sync.RWMutex
	retryBackoff []time.Duration
}

// NewDispatcher creates a Dispatcher with bounded channel queue and worker pool.
func NewDispatcher(s *Store, engine *TemplateEngine, siteDomain string, queueSize int) *Dispatcher {
	if queueSize <= 0 {
		queueSize = 1024
	}
	d := &Dispatcher{
		store:        s,
		router:       NewRouter(),
		engine:       engine,
		siteDomain:   siteDomain,
		queue:        make(chan DispatchTask, queueSize),
		stop:         make(chan struct{}),
		retryBackoff: []time.Duration{10 * time.Second, 60 * time.Second, 300 * time.Second},
	}
	return d
}

// SetRetryBackoff overrides default retry backoff durations (useful for fast unit testing).
func (d *Dispatcher) SetRetryBackoff(backoff []time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.retryBackoff = backoff
}

func (d *Dispatcher) getRetryBackoff() []time.Duration {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.retryBackoff
}

// Start spawns background delivery worker routines.
func (d *Dispatcher) Start(workerCount int) {
	if workerCount <= 0 {
		workerCount = 2
	}
	for i := 0; i < workerCount; i++ {
		d.wg.Add(1)
		go d.worker()
	}
}

// Stop signals all workers to exit and drains any in-flight deliveries.
func (d *Dispatcher) Stop() {
	d.mu.Lock()
	select {
	case <-d.stop:
		d.mu.Unlock()
		return
	default:
		close(d.stop)
	}
	d.mu.Unlock()

	d.wg.Wait()
}

// Dispatch processes an event published via events.Emit() without blocking the caller.
// Matches rules, handles quiet hours / throttling, records deliveries, and enqueues sends.
func (d *Dispatcher) Dispatch(e events.Event) {
	// ★ Non-blocking guarantee per P2-01: 投递逻辑绝不许阻塞 Emit() 耗时
	go d.processEvent(e)
}

func (d *Dispatcher) processEvent(e events.Event) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rules, err := d.store.ListRules(ctx)
	if err != nil {
		logx.Warn(fmt.Sprintf("notify: failed to list rules for event %s: %v", e.Type, err))
		return
	}
	if len(rules) == 0 {
		return
	}

	now := time.Now().UTC()
	severity, _ := events.GetPolicy(e.Type)
	matches := d.router.EvaluateRules(rules, e.Type, severity, e.DedupKey, e.TargetID, now)
	if len(matches) == 0 {
		return
	}

	eventID := e.DedupKey
	if eventID == "" {
		eventID = ulid.New()
	}

	// Prepare data map for template rendering
	renderData := make(map[string]any)
	// Seed known optional fields with defaults so missing optional payload keys don't trigger error
	renderData["NodeName"] = ""
	renderData["GroupName"] = ""
	renderData["PublicIP"] = ""
	renderData["LastSeenAtMs"] = int64(0)
	renderData["OfflineSeconds"] = int64(0)
	renderData["ErrorMsg"] = ""
	renderData["FailedSeconds"] = int64(0)
	renderData["DroppedBatches"] = int64(0)

	for k, v := range e.Payload {
		renderData[k] = v
	}
	renderData["EventType"] = e.Type
	renderData["Severity"] = severity
	renderData["Source"] = e.Source
	renderData["TargetKind"] = e.TargetKind
	renderData["TargetId"] = e.TargetID
	renderData["Title"] = e.Title
	if e.OccurredAt > 0 {
		renderData["OccurredAtMs"] = e.OccurredAt
	} else {
		renderData["OccurredAtMs"] = now.UnixMilli()
	}
	renderData["SiteDomain"] = d.siteDomain

	for _, m := range matches {
		deliveryID := ulid.New()
		ruleID := ""
		if m.Rule != nil {
			ruleID = m.Rule.ID
		}

		if m.IsQuietHeld {
			// Record quiet_held delivery
			_ = d.store.CreateDelivery(ctx, &Delivery{
				ID:          deliveryID,
				EventID:     eventID,
				ChannelID:   m.ChannelID,
				RuleID:      ruleID,
				State:       "quiet_held",
				Attempt:     0,
				CreatedAtMs: now.UnixMilli(),
			})
			continue
		}

		if m.IsThrottled {
			// Record throttled delivery
			_ = d.store.CreateDelivery(ctx, &Delivery{
				ID:          deliveryID,
				EventID:     eventID,
				ChannelID:   m.ChannelID,
				RuleID:      ruleID,
				State:       "throttled",
				Attempt:     0,
				CreatedAtMs: now.UnixMilli(),
			})
			continue
		}

		// Render message text with template engine
		renderedText, renderErr := d.engine.Render(e.Type, m.TemplateName, renderData)
		if renderErr != nil {
			logx.Warn(fmt.Sprintf("notify: template render failed for %s, degraded to fallback: %v", e.Type, renderErr))
		}

		// Insert delivery in pending state
		err := d.store.CreateDelivery(ctx, &Delivery{
			ID:           deliveryID,
			EventID:      eventID,
			ChannelID:    m.ChannelID,
			RuleID:       ruleID,
			State:        "pending",
			Attempt:      0,
			RenderedText: renderedText,
			CreatedAtMs:  now.UnixMilli(),
		})
		if err != nil {
			logx.Error(fmt.Sprintf("notify: failed to record pending delivery: %v", err))
			continue
		}

		// Enqueue for background delivery
		task := DispatchTask{
			DeliveryID:   deliveryID,
			ChannelID:    m.ChannelID,
			RenderedText: renderedText,
			Attempt:      0,
		}

		select {
		case d.queue <- task:
		default:
			logx.Warn(fmt.Sprintf("notify: delivery queue full, dropping delivery %s for channel %s", deliveryID, m.ChannelID))
			_ = d.store.UpdateDeliveryState(ctx, deliveryID, "failed", 0, "delivery queue buffer full", nil)
		}
	}
}

func (d *Dispatcher) worker() {
	defer d.wg.Done()

	for {
		select {
		case <-d.stop:
			return
		case task := <-d.queue:
			d.deliverWithRetry(task)
		}
	}
}

func (d *Dispatcher) deliverWithRetry(task DispatchTask) {
	backoffs := d.getRetryBackoff()
	maxAttempts := len(backoffs) + 1

	for task.Attempt < maxAttempts {
		task.Attempt++

		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := d.attemptSend(ctx, task.ChannelID, task.RenderedText)
		cancel()

		nowMs := time.Now().UnixMilli()
		if err == nil {
			// Delivery success
			_ = d.store.UpdateDeliveryState(context.Background(), task.DeliveryID, "sent", task.Attempt, "", &nowMs)
			return
		}

		cleanErrMsg := logx.Redact(err.Error())

		// Check if non-retriable or reached max attempts
		if sender.IsNonRetriable(err) || task.Attempt >= maxAttempts {
			_ = d.store.UpdateDeliveryState(context.Background(), task.DeliveryID, "failed", task.Attempt, cleanErrMsg, nil)
			logx.Error(fmt.Sprintf("notify: delivery %s to channel %s failed: %s", task.DeliveryID, task.ChannelID, cleanErrMsg))

			// Emit notify failed event
			events.Emit(context.Background(), events.Event{
				Type:   "system.notify_failed",
				Source: "notify",
				Title:  fmt.Sprintf("通知投递失败 (channel=%s)", task.ChannelID),
				Payload: map[string]any{
					"DeliveryID": task.DeliveryID,
					"ChannelID":  task.ChannelID,
					"Error":      cleanErrMsg,
				},
			})
			return
		}

		// Update intermediate attempt failure
		_ = d.store.UpdateDeliveryState(context.Background(), task.DeliveryID, "pending", task.Attempt, cleanErrMsg, nil)

		backoff := backoffs[task.Attempt-1]
		select {
		case <-d.stop:
			return
		case <-time.After(backoff):
		}
	}
}

func (d *Dispatcher) attemptSend(ctx context.Context, channelID, text string) error {
	ch, err := d.store.GetChannel(ctx, channelID)
	if err != nil {
		return sender.MarkNonRetriable(fmt.Errorf("channel not found: %w", err))
	}
	if !ch.IsEnabled {
		return sender.MarkNonRetriable(fmt.Errorf("channel is disabled"))
	}

	s, ok := sender.Get(ch.Kind)
	if !ok {
		return sender.MarkNonRetriable(fmt.Errorf("unsupported channel driver: %s", ch.Kind))
	}

	secret := ""
	if ch.CredentialID != "" {
		sec, err := d.store.GetDecryptedSecret(ctx, ch.CredentialID)
		if err != nil {
			return sender.MarkNonRetriable(fmt.Errorf("decrypt secret failed: %w", err))
		}
		secret = sec
	}

	cfg := &sender.ChannelConfig{
		ID:         ch.ID,
		Name:       ch.Name,
		Kind:       ch.Kind,
		ConfigJSON: ch.ConfigJSON,
	}

	return s.Send(ctx, cfg, secret, text)
}

// TestSendChannel delivers an immediate test notification to a specific channel.
func (d *Dispatcher) TestSendChannel(ctx context.Context, channelID, customText string) error {
	if customText == "" {
		customText = fmt.Sprintf("🔔 来自 dash 的通知测试消息\n发送时间: %s\n此消息用于验证渠道配置与连通性。", time.Now().UTC().Format("2006-01-02 15:04:05 UTC"))
	}
	return d.attemptSend(ctx, channelID, customText)
}
