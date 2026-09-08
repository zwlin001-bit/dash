package sender

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"dash/internal/logx"
)

const (
	defaultTelegramAPIBase = "https://api.telegram.org"
	maxTelegramMsgLength   = 4096
	truncateLimit          = 4000
)

// TelegramConfig describes configuration parsed from config_json.
type TelegramConfig struct {
	ChatID          string `json:"chat_id"`
	MessageThreadID *int64 `json:"message_thread_id,omitempty"`
	ParseMode       string `json:"parse_mode,omitempty"`
	APIBase         string `json:"api_base,omitempty"`
}

// TelegramSender implements the Sender interface for Telegram Bot API.
type TelegramSender struct {
	client     *http.Client
	chatLocks  sync.Map // key: chat_id string -> *sync.Mutex
	customBase string
}

func init() {
	Register(NewTelegramSender(nil))
}

// NewTelegramSender initializes a TelegramSender with an optional custom HTTP client.
func NewTelegramSender(client *http.Client) *TelegramSender {
	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Second,
		}
	}
	return &TelegramSender{
		client: client,
	}
}

func (s *TelegramSender) Kind() string {
	return "telegram"
}

func (s *TelegramSender) getChatLock(chatID string) *sync.Mutex {
	v, _ := s.chatLocks.LoadOrStore(chatID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (s *TelegramSender) Send(ctx context.Context, cfg *ChannelConfig, botToken string, text string) error {
	botToken = strings.TrimSpace(botToken)
	if botToken == "" {
		return MarkNonRetriable(fmt.Errorf("telegram: bot_token is empty"))
	}

	var tgCfg TelegramConfig
	if cfg != nil && cfg.ConfigJSON != "" {
		_ = json.Unmarshal([]byte(cfg.ConfigJSON), &tgCfg)
	}

	if tgCfg.ChatID == "" {
		return MarkNonRetriable(fmt.Errorf("telegram: chat_id is required in channel config"))
	}

	// 1. Serialize requests to the same chat_id to avoid 429 rate limiting
	mu := s.getChatLock(tgCfg.ChatID)
	mu.Lock()
	defer mu.Unlock()

	// 2. Truncate text if it exceeds Telegram's 4096 character limit
	if len([]rune(text)) > maxTelegramMsgLength {
		runes := []rune(text)
		text = string(runes[:truncateLimit]) + "\n... (消息过长已截断)"
	}

	apiBase := defaultTelegramAPIBase
	if tgCfg.APIBase != "" {
		apiBase = strings.TrimRight(tgCfg.APIBase, "/")
	}
	if s.customBase != "" {
		apiBase = strings.TrimRight(s.customBase, "/")
	}

	url := fmt.Sprintf("%s/bot%s/sendMessage", apiBase, botToken)

	parseMode := tgCfg.ParseMode
	if parseMode == "" {
		parseMode = "HTML"
	}

	reqBody := map[string]any{
		"chat_id":    tgCfg.ChatID,
		"text":       text,
		"parse_mode": parseMode,
	}
	if tgCfg.MessageThreadID != nil {
		reqBody["message_thread_id"] = *tgCfg.MessageThreadID
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("telegram: marshal request body failed: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return sanitizeTelegramError(fmt.Errorf("telegram: create http request failed: %w", err), botToken)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return sanitizeTelegramError(fmt.Errorf("telegram: send request failed: %w", err), botToken)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusOK {
		return nil
	}

	// Parse Telegram error response
	var tgResp struct {
		OK          bool   `json:"ok"`
		ErrorCode   int    `json:"error_code"`
		Description string `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	_ = json.Unmarshal(respBody, &tgResp)

	errMsg := tgResp.Description
	if errMsg == "" {
		errMsg = string(respBody)
	}

	formattedErr := sanitizeTelegramError(fmt.Errorf("telegram API error (status %d, code %d): %s", resp.StatusCode, tgResp.ErrorCode, errMsg), botToken)

	// Rate limit handling (429)
	if resp.StatusCode == http.StatusTooManyRequests {
		if tgResp.Parameters.RetryAfter > 0 {
			formattedErr = fmt.Errorf("%w (retry_after=%ds)", formattedErr, tgResp.Parameters.RetryAfter)
		}
		return formattedErr // Retriable
	}

	// Non-retriable statuses: client-side configuration / token / target errors
	if resp.StatusCode == http.StatusBadRequest ||
		resp.StatusCode == http.StatusUnauthorized ||
		resp.StatusCode == http.StatusForbidden ||
		resp.StatusCode == http.StatusNotFound {
		return MarkNonRetriable(formattedErr)
	}

	// 5xx or other server-side errors are retriable
	return formattedErr
}

// sanitizeTelegramError ensures the botToken and any other secrets never appear in error messages.
func sanitizeTelegramError(err error, token string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if token != "" {
		msg = strings.ReplaceAll(msg, token, "••••")
	}
	msg = logx.Redact(msg)
	return fmt.Errorf("%s", msg)
}
