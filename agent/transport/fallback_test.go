package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"dash/internal/protocol"
)

func TestHTTPFallbackPost_Success(t *testing.T) {
	expectedToken := "test-agent-token-xyz"
	var receivedBody []byte
	var receivedAuth string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/api/agent/v1/report" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var err error
		receivedBody = make([]byte, 1024)
		n, _ := r.Body.Read(receivedBody)
		receivedBody = receivedBody[:n]
		_ = err

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"server_time_ms": 1757222400123,
			"commands": [
				{"jsonrpc":"2.0","method":"server.config","params":{"interval_fast_s":10}}
			]
		}`))
	}))
	defer ts.Close()

	client := NewHTTPFallbackClient(ts.URL+"/api/agent/v1/report", expectedToken, ts.Client())

	metricsReq := &protocol.Request{
		JSONRPC: protocol.JSONRPCVersion,
		Method:  protocol.MethodAgentMetrics,
		Params:  json.RawMessage(`{"ts_ms":1757222400000}`),
	}

	resp, err := client.PostReport(context.Background(), []*protocol.Request{metricsReq})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedAuth != "Bearer "+expectedToken {
		t.Fatalf("expected Authorization Bearer %s, got %s", expectedToken, receivedAuth)
	}

	if resp.ServerTimeMs != 1757222400123 {
		t.Fatalf("expected ServerTimeMs 1757222400123, got %d", resp.ServerTimeMs)
	}

	if len(resp.Commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(resp.Commands))
	}
	if resp.Commands[0].Method != protocol.MethodServerConfig {
		t.Fatalf("expected method %s, got %s", protocol.MethodServerConfig, resp.Commands[0].Method)
	}
}

func TestHTTPFallbackPost_Unauthorized(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer ts.Close()

	client := NewHTTPFallbackClient(ts.URL, "bad-token", ts.Client())
	_, err := client.PostReport(context.Background(), []*protocol.Request{
		{JSONRPC: protocol.JSONRPCVersion, Method: protocol.MethodAgentMetrics},
	})

	if err == nil {
		t.Fatalf("expected error on 401, got nil")
	}

	var rpcErr *protocol.RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("expected RPCError, got %T: %v", err, err)
	}
	if rpcErr.Code != protocol.ErrCodeTokenInvalid {
		t.Fatalf("expected code %d, got %d", protocol.ErrCodeTokenInvalid, rpcErr.Code)
	}
}

func TestHTTPFallbackPost_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`internal server error`))
	}))
	defer ts.Close()

	client := NewHTTPFallbackClient(ts.URL, "token", ts.Client())
	_, err := client.PostReport(context.Background(), []*protocol.Request{
		{JSONRPC: protocol.JSONRPCVersion, Method: protocol.MethodAgentMetrics},
	})

	if err == nil {
		t.Fatalf("expected error on 500, got nil")
	}
}

func TestHTTPFallbackPost_EmptyBatch(t *testing.T) {
	client := NewHTTPFallbackClient("http://127.0.0.1:0", "token", nil)
	resp, err := client.PostReport(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error on empty batch: %v", err)
	}
	if resp == nil {
		t.Fatalf("expected non-nil response")
	}
}
