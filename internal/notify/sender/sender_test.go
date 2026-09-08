package sender

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTelegramSenderSuccess(t *testing.T) {
	var receivedBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/bot123456:FAKE_TOKEN/sendMessage") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &receivedBody)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok": true, "result": {"message_id": 100}}`))
	}))
	defer ts.Close()

	sender := NewTelegramSender(ts.Client())
	sender.customBase = ts.URL

	cfg := &ChannelConfig{
		Kind:       "telegram",
		ConfigJSON: `{"chat_id": "-100112233", "parse_mode": "HTML"}`,
	}

	err := sender.Send(context.Background(), cfg, "123456:FAKE_TOKEN", "hello telegram")
	if err != nil {
		t.Fatalf("send failed: %v", err)
	}

	if receivedBody["chat_id"] != "-100112233" {
		t.Errorf("expected chat_id -100112233, got %v", receivedBody["chat_id"])
	}
	if receivedBody["text"] != "hello telegram" {
		t.Errorf("expected text 'hello telegram', got %v", receivedBody["text"])
	}
}

func TestTelegramSenderBadTokenNonRetriableAndSanitized(t *testing.T) {
	token := "secret_token_abcdef123456"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"ok": false, "error_code": 401, "description": "Unauthorized: invalid token"}`))
	}))
	defer ts.Close()

	sender := NewTelegramSender(ts.Client())
	sender.customBase = ts.URL

	cfg := &ChannelConfig{
		Kind:       "telegram",
		ConfigJSON: `{"chat_id": "-100112233"}`,
	}

	err := sender.Send(context.Background(), cfg, token, "test message")
	if err == nil {
		t.Fatal("expected error on 401 unauthorized, got nil")
	}

	// 1. Must be marked non-retriable
	if !IsNonRetriable(err) {
		t.Errorf("expected error to be non-retriable, got: %v", err)
	}

	// 2. Must NOT contain token plaintext (Acceptance criteria 2 and 3)
	errMsg := err.Error()
	if strings.Contains(errMsg, token) {
		t.Fatalf("CRITICAL: error message leaked bot token plaintext: %s", errMsg)
	}
}

func TestTelegramSenderTruncation(t *testing.T) {
	var receivedText string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &req)
		receivedText, _ = req["text"].(string)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	sender := NewTelegramSender(ts.Client())
	sender.customBase = ts.URL

	cfg := &ChannelConfig{
		Kind:       "telegram",
		ConfigJSON: `{"chat_id": "123"}`,
	}

	// Message of 5000 characters
	longMsg := strings.Repeat("A", 5000)
	err := sender.Send(context.Background(), cfg, "fake_token", longMsg)
	if err != nil {
		t.Fatalf("send failed: %v", err)
	}

	if len([]rune(receivedText)) > 4096 {
		t.Errorf("expected text length <= 4096, got %d", len([]rune(receivedText)))
	}
	if !strings.Contains(receivedText, "已截断") {
		t.Errorf("expected truncation notice in text, got: %s", receivedText)
	}
}

func TestTelegramSenderSerialPerChat(t *testing.T) {
	var inFlight int32
	var maxConcurrent int32
	var mu sync.Mutex

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&inFlight, 1)
		mu.Lock()
		if cur > maxConcurrent {
			maxConcurrent = cur
		}
		mu.Unlock()

		time.Sleep(30 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	sender := NewTelegramSender(ts.Client())
	sender.customBase = ts.URL

	cfg := &ChannelConfig{
		Kind:       "telegram",
		ConfigJSON: `{"chat_id": "same_chat_123"}`,
	}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = sender.Send(context.Background(), cfg, "fake_token", "concurrent msg")
		}()
	}
	wg.Wait()

	if maxConcurrent > 1 {
		t.Errorf("expected max concurrent requests to same chat to be 1, got %d", maxConcurrent)
	}
}

func TestWebhookSenderWithHMAC(t *testing.T) {
	// Acceptance criterion 8: Webhook 渠道对着 httptest 服务端验证 HMAC 签名正确
	secret := "my_webhook_secret_key_888"
	var verifiedHMAC bool

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sigHeader := r.Header.Get("X-Dash-Signature")
		if !strings.HasPrefix(sigHeader, "sha256=") {
			http.Error(w, "missing or bad signature header", http.StatusBadRequest)
			return
		}

		sigHex := strings.TrimPrefix(sigHeader, "sha256=")
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		expectedHex := hex.EncodeToString(mac.Sum(nil))

		if hmac.Equal([]byte(sigHex), []byte(expectedHex)) {
			verifiedHMAC = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"received"}`))
		} else {
			http.Error(w, "invalid signature", http.StatusForbidden)
		}
	}))
	defer ts.Close()

	sender := NewWebhookSender(ts.Client())
	cfg := &ChannelConfig{
		Kind:       "webhook",
		ConfigJSON: `{"url":"` + ts.URL + `", "method":"POST"}`,
	}

	err := sender.Send(context.Background(), cfg, secret, "webhook notification content")
	if err != nil {
		t.Fatalf("send webhook failed: %v", err)
	}

	if !verifiedHMAC {
		t.Fatal("HMAC signature was not verified on server")
	}
}

func TestRegistryHasSenders(t *testing.T) {
	kinds := RegisteredKinds()
	hasTG := false
	hasWH := false
	for _, k := range kinds {
		if k == "telegram" {
			hasTG = true
		}
		if k == "webhook" {
			hasWH = true
		}
	}
	if !hasTG || !hasWH {
		t.Fatalf("expected both telegram and webhook in registry, got: %v", kinds)
	}
}
