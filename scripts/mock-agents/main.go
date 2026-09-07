package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/gorilla/websocket"
)

type rpcMsg struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int64 `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func seedNodes(dsn string, count int) error {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	now := time.Now().UnixMilli()
	for i := 1; i <= count; i++ {
		nodeID := fmt.Sprintf("01M1BENCH%017d", i)
		nodeName := fmt.Sprintf("bench-node-%02d", i)
		token := fmt.Sprintf("bench-token-%02d", i)
		tHash := hashToken(token)

		// Upsert node into database
		q := `INSERT INTO nodes (id, name, display_order, is_hidden, agent_token_hash, agent_version, conn_state, created_at_ms, updated_at_ms)
		      VALUES (?, ?, ?, 0, ?, '0.1.0', 'never', ?, ?)
		      ON DUPLICATE KEY UPDATE agent_token_hash = VALUES(agent_token_hash), updated_at_ms = VALUES(updated_at_ms)`
		_, err := db.ExecContext(ctx, q, nodeID, nodeName, i, tHash, now, now)
		if err != nil {
			return fmt.Errorf("seed node %d: %w", i, err)
		}
	}
	return nil
}

func main() {
	endpointFlag := flag.String("endpoint", "http://127.0.0.1:18088", "dashd endpoint URL")
	dsnFlag := flag.String("dsn", "", "mysql dsn to seed nodes")
	nodesFlag := flag.Int("nodes", 30, "number of simulated nodes")
	intervalFlag := flag.Duration("interval", 5*time.Second, "metrics report interval")
	durationFlag := flag.Duration("duration", 60*time.Second, "total run duration")
	flag.Parse()

	if *dsnFlag != "" {
		if err := seedNodes(*dsnFlag, *nodesFlag); err != nil {
			fmt.Fprintf(os.Stderr, "failed to seed nodes: %v\n", err)
			os.Exit(1)
		}
	}

	endpoint := strings.TrimRight(*endpointFlag, "/")
	wsEndpoint := strings.Replace(strings.Replace(endpoint, "http://", "ws://", 1), "https://", "wss://", 1) + "/api/agent/v1/rpc"

	ctx, cancel := context.WithTimeout(context.Background(), *durationFlag)
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	var connectedCount atomic.Int32
	var wg sync.WaitGroup

	for i := 1; i <= *nodesFlag; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			token := fmt.Sprintf("bench-token-%02d", idx)
			dialer := websocket.DefaultDialer
			headers := http.Header{}
			headers.Set("Authorization", "Bearer "+token)

			conn, _, err := dialer.DialContext(ctx, wsEndpoint, headers)
			if err != nil {
				fmt.Fprintf(os.Stderr, "node %d dial ws error: %v\n", idx, err)
				return
			}
			defer conn.Close()

			// 发送握手 agent.hello
			reqID := int64(1)
			helloMsg := rpcMsg{
				JSONRPC: "2.0",
				ID:      &reqID,
				Method:  "agent.hello",
				Params: map[string]any{
					"protocol_version": 1,
					"agent_version":    "0.1.0",
					"boot_at_ms":       time.Now().UnixMilli() - 3600000,
					"capabilities":     []string{"metrics", "facts"},
					"facts_hash":       "mock-hash",
				},
			}
			if err := conn.WriteJSON(helloMsg); err != nil {
				return
			}

			// 读取握手响应
			var helloResp map[string]any
			if err := conn.ReadJSON(&helloResp); err != nil {
				return
			}

			curConnected := connectedCount.Add(1)
			if int(curConnected) == *nodesFlag {
				fmt.Printf("MOCK_AGENTS_ONLINE=%d\n", curConnected)
				_ = os.Stdout.Sync()
			}

			// 定时上报 agent.metrics
			ticker := time.NewTicker(*intervalFlag)
			defer ticker.Stop()

			readDone := make(chan struct{})
			go func() {
				defer close(readDone)
				for {
					var discard map[string]any
					if err := conn.ReadJSON(&discard); err != nil {
						return
					}
				}
			}()

			var totalUp, totalDown int64
			for {
				select {
				case <-ctx.Done():
					return
				case <-readDone:
					return
				case <-ticker.C:
					nowMs := time.Now().UnixMilli()
					upDelta := int64(50000 + rand.Intn(50000))
					downDelta := int64(100000 + rand.Intn(100000))
					totalUp += upDelta
					totalDown += downDelta

					cpuPct := 5.0 + rand.Float64()*15.0
					memUsed := int64(512*1024*1024 + rand.Intn(100*1024*1024))
					uptime := int64(3600 + idx*60)

					reportMsg := rpcMsg{
						JSONRPC: "2.0",
						Method:  "agent.metrics",
						Params: map[string]any{
							"ts_ms":    nowMs,
							"cpu_pct":  cpuPct,
							"mem_used": memUsed,
							"uptime_s": uptime,
							"net": map[string]any{
								"up_bps":     upDelta * 8 / 5,
								"down_bps":   downDelta * 8 / 5,
								"total_up":   totalUp,
								"total_down": totalDown,
							},
						},
					}

					if err := conn.WriteJSON(reportMsg); err != nil {
						return
					}
				}
			}
		}(i)
	}

	wg.Wait()
}
