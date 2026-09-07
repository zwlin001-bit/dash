package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func main() {
	var metricsCount atomic.Int64
	var factsCount atomic.Int64

	mux := http.NewServeMux()

	mux.HandleFunc("/api/agent/v1/rpc", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("mock-server: upgrade error: %v", err)
			return
		}
		defer conn.Close()

		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}

			var req rpcRequest
			if err := json.Unmarshal(msg, &req); err != nil {
				continue
			}

			if req.Method == "agent.hello" && req.ID != nil {
				resp := rpcResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result: json.RawMessage(`{
						"node_id": "01JBX_BENCH",
						"server_time_ms": 1757222400000,
						"interval_fast_s": 5,
						"interval_slow_s": 60,
						"facts_max_interval_s": 1800,
						"collect_conns": true,
						"need_facts": true
					}`),
				}
				b, _ := json.Marshal(resp)
				_ = conn.WriteMessage(websocket.TextMessage, b)
			} else if req.Method == "agent.metrics" {
				metricsCount.Add(1)
			} else if req.Method == "agent.facts" {
				factsCount.Add(1)
			}
		}
	})

	mux.HandleFunc("/api/agent/v1/report", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"server_time_ms":1757222400000,"commands":[]}`))
	})

	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"metrics":%d,"facts":%d}`, metricsCount.Load(), factsCount.Load())
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to listen: %v\n", err)
		os.Exit(1)
	}

	port := listener.Addr().(*net.TCPAddr).Port
	// 告知父脚本服务就绪与端口
	fmt.Printf("MOCK_SERVER_PORT=%d\n", port)
	os.Stdout.Sync()

	server := &http.Server{
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
	}
}
