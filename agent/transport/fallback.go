package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"dash/internal/protocol"
)

const (
	defaultHTTPTimeout     = 10 * time.Second
	maxReportResponseBytes = 64 * 1024 // 64 KB 限制，避免内存异常放大
)

// ReportResponse 是 POST /api/agent/v1/report 的服务端响应体。
// 见 docs/12-api-spec.md §2。
type ReportResponse struct {
	ServerTimeMs int64              `json:"server_time_ms"`
	Commands     []protocol.Request `json:"commands"`
}

// HTTPFallbackClient 负责在 WebSocket 不可用时，通过 HTTP POST 进行批量上报并接收回带指令。
type HTTPFallbackClient struct {
	endpoint string
	token    string
	client   *http.Client
}

// NewHTTPFallbackClient 创建 HTTP 回退上报客户端。
func NewHTTPFallbackClient(endpoint, token string, client *http.Client) *HTTPFallbackClient {
	if client == nil {
		client = &http.Client{
			Timeout: defaultHTTPTimeout,
		}
	}
	return &HTTPFallbackClient{
		endpoint: endpoint,
		token:    token,
		client:   client,
	}
}

// PostReport 发送一批 JSON-RPC notification 数组，并返回服务端响应及回带指令。
func (c *HTTPFallbackClient) PostReport(ctx context.Context, batch []*protocol.Request) (*ReportResponse, error) {
	if len(batch) == 0 {
		return &ReportResponse{}, nil
	}

	bodyBytes, err := json.Marshal(batch)
	if err != nil {
		return nil, fmt.Errorf("marshal fallback report batch: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create fallback http request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http fallback post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, &protocol.RPCError{
			Code:    protocol.ErrCodeTokenInvalid,
			Message: "token invalid or revoked (HTTP 401)",
		}
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxReportResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read fallback http response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http report status %d: %s", resp.StatusCode, string(respBody))
	}

	var reportResp ReportResponse
	if err := json.Unmarshal(respBody, &reportResp); err != nil {
		return nil, fmt.Errorf("unmarshal report response: %w", err)
	}

	return &reportResp, nil
}
