package sender

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"dash/internal/logx"
)

// WebhookConfig defines properties parsed from config_json.
type WebhookConfig struct {
	URL     string            `json:"url"`
	Method  string            `json:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// WebhookSender implements the Sender interface for generic HTTP Webhooks.
type WebhookSender struct {
	client *http.Client
}

func init() {
	Register(NewWebhookSender(nil))
}

// NewWebhookSender initializes a WebhookSender.
func NewWebhookSender(client *http.Client) *WebhookSender {
	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Second,
		}
	}
	return &WebhookSender{
		client: client,
	}
}

func (s *WebhookSender) Kind() string {
	return "webhook"
}

func (s *WebhookSender) Send(ctx context.Context, cfg *ChannelConfig, secret string, text string) error {
	var whCfg WebhookConfig
	if cfg != nil && cfg.ConfigJSON != "" {
		_ = json.Unmarshal([]byte(cfg.ConfigJSON), &whCfg)
	}

	whCfg.URL = strings.TrimSpace(whCfg.URL)
	if whCfg.URL == "" {
		return MarkNonRetriable(fmt.Errorf("webhook: url is required"))
	}

	method := strings.ToUpper(strings.TrimSpace(whCfg.Method))
	if method == "" {
		method = http.MethodPost
	}

	payloadData := map[string]any{
		"text":         text,
		"timestamp_ms": time.Now().UnixMilli(),
	}
	payloadBytes, err := json.Marshal(payloadData)
	if err != nil {
		return fmt.Errorf("webhook: marshal payload failed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, method, whCfg.URL, bytes.NewReader(payloadBytes))
	if err != nil {
		return sanitizeWebhookError(fmt.Errorf("webhook: create request failed: %w", err), secret)
	}

	req.Header.Set("Content-Type", "application/json")
	for k, v := range whCfg.Headers {
		req.Header.Set(k, v)
	}

	// Compute HMAC-SHA256 signature if secret is provided (Acceptance criterion 8)
	secret = strings.TrimSpace(secret)
	if secret != "" {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(payloadBytes)
		sigHex := hex.EncodeToString(mac.Sum(nil))
		req.Header.Set("X-Dash-Signature", "sha256="+sigHex)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return sanitizeWebhookError(fmt.Errorf("webhook: request failed: %w", err), secret)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	bodySnippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	errMsg := fmt.Sprintf("webhook server returned status %d: %s", resp.StatusCode, string(bodySnippet))
	formattedErr := sanitizeWebhookError(fmt.Errorf("%s", errMsg), secret)

	if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
		return MarkNonRetriable(formattedErr)
	}

	return formattedErr
}

func sanitizeWebhookError(err error, secret string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if secret != "" {
		msg = strings.ReplaceAll(msg, secret, "••••")
	}
	msg = logx.Redact(msg)
	return fmt.Errorf("%s", msg)
}
