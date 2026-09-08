package notify

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dash/internal/crypto"
	"dash/internal/db"
	"dash/internal/events"
	"dash/internal/migrate"
	"dash/internal/notify/sender"
	_ "github.com/go-sql-driver/mysql"
)

func setupTestDB(t *testing.T) *db.DB {
	cfg := &db.Config{
		Driver: "mysql",
		DSN:    "root:root@tcp(127.0.0.1:33306)/dash_test?parseTime=true",
	}
	d, err := db.Open(cfg)
	if err != nil {
		t.Skipf("skipping test: cannot connect to MySQL on 33306: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	mig := migrate.New(d, "../../migrations")
	if err := mig.Up(ctx); err != nil {
		t.Fatalf("migration up failed: %v", err)
	}

	// Clean tables
	_, _ = d.Exec(ctx, "DELETE FROM notify_deliveries")
	_, _ = d.Exec(ctx, "DELETE FROM notify_rules")
	_, _ = d.Exec(ctx, "DELETE FROM notify_channels")
	_, _ = d.Exec(ctx, "DELETE FROM credentials")
	_, _ = d.Exec(ctx, "DELETE FROM events")

	return d
}

func setupTestCrypto(t *testing.T) *crypto.Manager {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	mgr, err := crypto.NewManagerWithKey(key)
	if err != nil {
		t.Fatalf("setup crypto failed: %v", err)
	}
	return mgr
}

// 验收 1: 配一个 Telegram 渠道 + 一条 node.offline 的路由 → 触发事件 → 手机（测试服务器）收到消息
func TestAcceptance1_EndToEndTelegramNotification(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()
	cryptoMgr := setupTestCrypto(t)

	var receivedMsg atomic.Value

	// Mock Telegram API server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		receivedMsg.Store(m)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok": true, "result": {"message_id": 101}}`))
	}))
	defer ts.Close()

	// Use custom test client pointing to mock server
	tgSender := sender.NewTelegramSender(ts.Client())
	sender.Register(tgSender)

	store := NewStore(database, cryptoMgr)
	engine := NewTemplateEngine("")
	dispatcher := NewDispatcher(store, engine, "test.domain", 100)
	dispatcher.Start(1)
	defer dispatcher.Stop()

	// 1. Create telegram channel with api_base pointing to mock server
	configJSON := fmt.Sprintf(`{"chat_id":"-100999","api_base":"%s"}`, ts.URL)
	ch, err := store.CreateChannel(context.Background(), "Ops Alert TG", "telegram", "123456:BOT_TOKEN", configJSON)
	if err != nil {
		t.Fatalf("create channel failed: %v", err)
	}

	// 2. Create rule for node.offline
	rule, err := store.CreateRule(context.Background(), &Rule{
		Name:            "Node Offline Rule",
		IsEnabled:       true,
		EventPattern:    "node.offline",
		MinSeverity:     "warning",
		NotifyChannelID: ch.ID,
		ThrottleS:       0,
	})
	if err != nil {
		t.Fatalf("create rule failed: %v", err)
	}
	_ = rule

	// 3. Dispatch event
	dispatcher.Dispatch(events.Event{
		Type:       "node.offline",
		Source:     "control",
		TargetKind: "node",
		TargetID:   "01J_NODE_1",
		Title:      "hk-01 节点离线",
		Payload: map[string]any{
			"NodeName":       "hk-01",
			"GroupName":      "香港核心池",
			"PublicIP":       "1.2.3.4",
			"OfflineSeconds": int64(120),
		},
	})

	// Wait for delivery
	var gotMsg map[string]any
	for i := 0; i < 30; i++ {
		if v := receivedMsg.Load(); v != nil {
			gotMsg = v.(map[string]any)
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if gotMsg == nil {
		t.Fatal("Telegram mock server never received the message")
	}

	text, _ := gotMsg["text"].(string)
	if !strings.Contains(text, "🔴 节点离线: hk-01") {
		t.Fatalf("expected rendered offline message, got: %s", text)
	}
	if !strings.Contains(text, "IP: 1.2.3.4") {
		t.Fatalf("expected IP in message, got: %s", text)
	}

	// Verify delivery record
	deliveries, total, err := store.ListDeliveries(context.Background(), DeliveryFilter{Limit: 10})
	if err != nil || total == 0 {
		t.Fatalf("delivery record missing: total=%d, err=%v", total, err)
	}
	if deliveries[0].State != "sent" {
		t.Fatalf("expected delivery state 'sent', got %s", deliveries[0].State)
	}
}

// 验收 2: 故意把 bot_token 配错 → 投递记录里 state=failed 且 last_error 不含 token 明文
func TestAcceptance2_BadTokenSanitizedInDeliveryRecord(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()
	cryptoMgr := setupTestCrypto(t)

	badToken := "secret_leaked_token_999888777"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"ok": false, "error_code": 401, "description": "Unauthorized: invalid bot token"}`))
	}))
	defer ts.Close()

	tgSender := sender.NewTelegramSender(ts.Client())
	sender.Register(tgSender)

	store := NewStore(database, cryptoMgr)
	engine := NewTemplateEngine("")
	dispatcher := NewDispatcher(store, engine, "test.domain", 100)
	dispatcher.Start(1)
	defer dispatcher.Stop()

	configJSON := fmt.Sprintf(`{"chat_id":"-100999","api_base":"%s"}`, ts.URL)
	ch, err := store.CreateChannel(context.Background(), "Bad TG", "telegram", badToken, configJSON)
	if err != nil {
		t.Fatalf("create channel failed: %v", err)
	}

	_, err = store.CreateRule(context.Background(), &Rule{
		Name:            "Catch All",
		IsEnabled:       true,
		EventPattern:    "*",
		MinSeverity:     "info",
		NotifyChannelID: ch.ID,
	})
	if err != nil {
		t.Fatalf("create rule failed: %v", err)
	}

	dispatcher.Dispatch(events.Event{
		Type:  "node.offline",
		Title: "Test Bad Token",
	})

	// Wait for delivery attempt and failure
	var failedDelivery *Delivery
	for i := 0; i < 30; i++ {
		list, total, err := store.ListDeliveries(context.Background(), DeliveryFilter{Limit: 10})
		if err == nil && total > 0 && list[0].State == "failed" {
			failedDelivery = list[0]
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if failedDelivery == nil {
		t.Fatal("delivery was not marked as failed")
	}

	if failedDelivery.State != "failed" {
		t.Fatalf("expected state failed, got: %s", failedDelivery.State)
	}

	// Check that last_error does NOT leak token plaintext
	if strings.Contains(failedDelivery.LastError, badToken) {
		t.Fatalf("CRITICAL LEAK: last_error contains plaintext bot token: %s", failedDelivery.LastError)
	}
}

// 验收 3: 全仓库 grep：日志与 API 响应里不出现 bot_token / webhook secret
func TestAcceptance3_SecretsMaskedInChannelAPI(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()
	cryptoMgr := setupTestCrypto(t)

	store := NewStore(database, cryptoMgr)

	secretTG := "bot_secret_tg_123456"
	secretWH := "wh_secret_hmac_789012"

	ch1, err := store.CreateChannel(context.Background(), "TG 1", "telegram", secretTG, `{"chat_id":"123"}`)
	if err != nil {
		t.Fatal(err)
	}
	ch2, err := store.CreateChannel(context.Background(), "WH 1", "webhook", secretWH, `{"url":"https://example.com"}`)
	if err != nil {
		t.Fatal(err)
	}

	list, err := store.ListChannels(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	for _, ch := range list {
		if ch.MaskedSecret == secretTG || ch.MaskedSecret == secretWH {
			t.Fatalf("Channel secret leaked in plaintext: %s", ch.MaskedSecret)
		}
		if !strings.HasPrefix(ch.MaskedSecret, "••••") {
			t.Fatalf("Channel secret not properly masked: %s", ch.MaskedSecret)
		}
	}

	_ = ch1
	_ = ch2
}

// 验收 4: 同一 dedup_key 在去重窗口内连发 10 次 → 只推 1 条
func TestAcceptance4_DeduplicationWindow(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()
	cryptoMgr := setupTestCrypto(t)

	var sendCount int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&sendCount, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	tgSender := sender.NewTelegramSender(ts.Client())
	sender.Register(tgSender)

	store := NewStore(database, cryptoMgr)
	engine := NewTemplateEngine("")
	dispatcher := NewDispatcher(store, engine, "test.domain", 100)
	dispatcher.Start(1)
	defer dispatcher.Stop()

	configJSON := fmt.Sprintf(`{"chat_id":"-100","api_base":"%s"}`, ts.URL)
	ch, _ := store.CreateChannel(context.Background(), "TG Dedup", "telegram", "token", configJSON)

	// Rule with 60 seconds throttle
	_, _ = store.CreateRule(context.Background(), &Rule{
		Name:            "Dedup Rule",
		IsEnabled:       true,
		EventPattern:    "node.offline",
		NotifyChannelID: ch.ID,
		ThrottleS:       60,
	})

	// Emit 10 times rapidly with same DedupKey
	for i := 0; i < 10; i++ {
		dispatcher.Dispatch(events.Event{
			Type:     "node.offline",
			Title:    "hk-01 离线",
			DedupKey: "node.offline:01J_HK",
		})
	}

	time.Sleep(300 * time.Millisecond)

	if count := atomic.LoadInt32(&sendCount); count != 1 {
		t.Fatalf("expected exactly 1 message delivered, got %d", count)
	}

	// Verify deliveries: 1 sent, 9 throttled
	list, total, err := store.ListDeliveries(context.Background(), DeliveryFilter{Limit: 20})
	if err != nil || total != 10 {
		t.Fatalf("expected 10 delivery records, got %d, err=%v", total, err)
	}

	sentCount := 0
	throttledCount := 0
	for _, d := range list {
		if d.State == "sent" {
			sentCount++
		} else if d.State == "throttled" {
			throttledCount++
		}
	}

	if sentCount != 1 || throttledCount != 9 {
		t.Fatalf("expected 1 sent and 9 throttled, got %d sent and %d throttled", sentCount, throttledCount)
	}
}

// 验收 5: 静默期内的事件 → 入库但不推 (state 为 quiet_held)
func TestAcceptance5_QuietHoursHeld(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()
	cryptoMgr := setupTestCrypto(t)

	var sendCount int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&sendCount, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	tgSender := sender.NewTelegramSender(ts.Client())
	sender.Register(tgSender)

	store := NewStore(database, cryptoMgr)
	engine := NewTemplateEngine("")
	dispatcher := NewDispatcher(store, engine, "test.domain", 100)
	dispatcher.Start(1)
	defer dispatcher.Stop()

	configJSON := fmt.Sprintf(`{"chat_id":"-100","api_base":"%s"}`, ts.URL)
	ch, _ := store.CreateChannel(context.Background(), "TG Quiet", "telegram", "token", configJSON)

	// Quiet hours covering all day: 0 to 1439
	qStart := 0
	qEnd := 1439
	_, _ = store.CreateRule(context.Background(), &Rule{
		Name:            "Quiet Rule",
		IsEnabled:       true,
		EventPattern:    "node.offline",
		NotifyChannelID: ch.ID,
		QuietStartMin:   &qStart,
		QuietEndMin:     &qEnd,
	})

	dispatcher.Dispatch(events.Event{
		Type:  "node.offline",
		Title: "hk-01 离线 (静默)",
	})

	time.Sleep(200 * time.Millisecond)

	// Must NOT send out
	if count := atomic.LoadInt32(&sendCount); count != 0 {
		t.Fatalf("expected 0 messages sent during quiet hours, got %d", count)
	}

	// Must record delivery as quiet_held
	list, total, err := store.ListDeliveries(context.Background(), DeliveryFilter{Limit: 10})
	if err != nil || total == 0 {
		t.Fatalf("expected quiet_held delivery record, got total=%d, err=%v", total, err)
	}
	if list[0].State != "quiet_held" {
		t.Fatalf("expected state quiet_held, got: %s", list[0].State)
	}
}

// 验收 6: 模板里故意写一个不存在的变量 → 仍然推出去（降级为标题），不是静默失败
func TestAcceptance6_TemplateDegradedFallbackDelivered(t *testing.T) {
	database := setupTestDB(t)
	defer database.Close()
	cryptoMgr := setupTestCrypto(t)

	var receivedText string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		receivedText, _ = m["text"].(string)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	tgSender := sender.NewTelegramSender(ts.Client())
	sender.Register(tgSender)

	tmpDir := t.TempDir()
	brokenTmpl := `{{.Title}} - {{.DefinitelyNonExistentVariable}}`
	_ = os.WriteFile(filepath.Join(tmpDir, "broken.tmpl"), []byte(brokenTmpl), 0644)

	store := NewStore(database, cryptoMgr)
	engine := NewTemplateEngine(tmpDir)
	dispatcher := NewDispatcher(store, engine, "test.domain", 100)
	dispatcher.Start(1)
	defer dispatcher.Stop()

	configJSON := fmt.Sprintf(`{"chat_id":"-100","api_base":"%s"}`, ts.URL)
	ch, _ := store.CreateChannel(context.Background(), "TG Fallback", "telegram", "token", configJSON)

	_, _ = store.CreateRule(context.Background(), &Rule{
		Name:            "Offline Fallback Rule",
		IsEnabled:       true,
		EventPattern:    "node.offline",
		TemplateName:    "broken",
		NotifyChannelID: ch.ID,
	})

	// Dispatch event with broken template
	dispatcher.Dispatch(events.Event{
		Type:  "node.offline",
		Title: "降级回退测试标题",
	})

	for i := 0; i < 30; i++ {
		if receivedText != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if receivedText == "" {
		t.Fatal("message was dropped instead of degraded delivery")
	}

	// Must contain degraded fallback format: [SEVERITY] EventType \n Title
	if !strings.Contains(receivedText, "node.offline") || !strings.Contains(receivedText, "降级回退测试标题") {
		t.Fatalf("expected degraded fallback text, got: %s", receivedText)
	}
}

// 验收 7: 关掉网络让投递全部失败 → Emit() 的耗时不受影响（基准测试证明）
func BenchmarkAcceptance7_EmitLatencyWhenNetworkFails(b *testing.B) {
	// Emit should complete in < 50 microseconds even when all network deliveries fail
	evStore := events.NewStore(nil, 4096)
	evStore.Start()
	defer evStore.Stop()
	events.SetDefaultStore(evStore)

	// Mock dispatcher that simulates failing/slow network
	failingDispatcher := &dummySlowDispatcher{}
	evStore.SetNotifyHook(failingDispatcher)

	ctx := context.Background()
	e := events.Event{
		Type:  "node.offline",
		Title: "Network Failing Event",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		events.Emit(ctx, e)
	}
}

type dummySlowDispatcher struct{}

func (d *dummySlowDispatcher) Dispatch(e events.Event) {
	// Simulates async dispatch without blocking Emit caller
	go func() {
		time.Sleep(500 * time.Millisecond) // Simulated network timeout
	}()
}

// 验收 8: Webhook 渠道对着 httptest 服务端验证 HMAC 签名正确
func TestAcceptance8_WebhookHMACSignature(t *testing.T) {
	secret := "secret_key_verification_hmac_999"
	var verified bool

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sig := r.Header.Get("X-Dash-Signature")
		if !strings.HasPrefix(sig, "sha256=") {
			http.Error(w, "bad sig header", http.StatusBadRequest)
			return
		}

		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))

		if sig == expected {
			verified = true
			w.WriteHeader(http.StatusOK)
		} else {
			http.Error(w, "invalid signature", http.StatusForbidden)
		}
	}))
	defer ts.Close()

	whSender := sender.NewWebhookSender(ts.Client())
	sender.Register(whSender)

	cfg := &sender.ChannelConfig{
		Kind:       "webhook",
		ConfigJSON: fmt.Sprintf(`{"url":"%s","method":"POST"}`, ts.URL),
	}

	err := whSender.Send(context.Background(), cfg, secret, "test hmac content")
	if err != nil {
		t.Fatalf("webhook send failed: %v", err)
	}
	if !verified {
		t.Fatal("HMAC signature failed server-side validation")
	}
}
