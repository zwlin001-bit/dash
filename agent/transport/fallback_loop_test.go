package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"dash/internal/protocol"
)

// 判据 7：防循环：构造一个"执行结果会触发新任务"的场景，确认每轮只补一次同步
func TestTransport_LoopPreventionSingleSupplementarySync(t *testing.T) {
	var requestCount atomic.Int32
	var mu sync.Mutex
	var receivedBatches [][]*protocol.Request

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqNum := requestCount.Add(1)

		var batch []*protocol.Request
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Errorf("decode batch error: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		mu.Lock()
		receivedBatches = append(receivedBatches, batch)
		mu.Unlock()

		resp := ReportResponse{
			ServerTimeMs: 1757222400000,
		}

		switch reqNum {
		case 1:
			// 正常上报轮次：服务端下发 task-1
			resp.Commands = []protocol.Request{
				{
					JSONRPC: "2.0",
					Method:  protocol.MethodServerExec,
					Params:  json.RawMessage(`{"request_id":"req-001","action":"test.step1","args":{}}`),
				},
			}
		case 2:
			// ★ 触发新任务场景：服务端收到 task-1 的执行结果回传，下发 task-2
			resp.Commands = []protocol.Request{
				{
					JSONRPC: "2.0",
					Method:  protocol.MethodServerExec,
					Params:  json.RawMessage(`{"request_id":"req-002","action":"test.step2","args":{}}`),
				},
			}
		default:
			// 若出现死循环，请求数将继续增长
			resp.Commands = []protocol.Request{
				{
					JSONRPC: "2.0",
					Method:  protocol.MethodServerExec,
					Params:  json.RawMessage(`{"request_id":"req-loop","action":"test.loop","args":{}}`),
				},
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := Config{
		ServerURL: server.URL,
		Token:     "test-token",
	}
	tr, err := New(cfg)
	if err != nil {
		t.Fatalf("create transport error: %v", err)
	}
	defer tr.Close()

	wst, ok := tr.(*wsTransport)
	if !ok {
		t.Fatalf("expected *wsTransport")
	}

	// 注册 Handler：模拟执行器执行动作并在完成后回传 agent.result
	wst.OnServerMessage(func(method string, params json.RawMessage) (any, error) {
		if method == protocol.MethodServerExec {
			var p struct {
				RequestID string `json:"request_id"`
			}
			_ = json.Unmarshal(params, &p)

			// 回传 agent.result
			res := &protocol.AgentResultParams{
				RequestID: p.RequestID,
				OK:        true,
				ExitCode:  0,
				Stdout:    "done:" + p.RequestID,
			}
			_ = wst.Send(protocol.MethodAgentResult, res)
			return map[string]interface{}{"ok": true}, nil
		}
		return nil, nil
	})

	// 初始化状态为 FallbackHTTP
	wst.ctx, wst.cancel = context.WithCancel(context.Background())
	wst.transitionTo(StateFallbackHTTP)

	// 先排队一条 metrics 报文，作为本轮主上报的内容
	_ = wst.Send(protocol.MethodAgentMetrics, json.RawMessage(`{"ts_ms":1757222400000}`))

	// --- 触发第一轮 flush ---
	err = wst.flushFallbackBatch()
	if err != nil {
		t.Fatalf("flushFallbackBatch error: %v", err)
	}

	// 核心断言：本轮执行后，服务端收到的请求数必须恰好为 2
	// （第 1 次为常规 metrics 上报，第 2 次为 task-1 结果的补充同步）
	// 防循环机制生效：task-2 不会在本轮立刻再次触发补充同步，杜绝死循环
	firstRoundReqs := requestCount.Load()
	if firstRoundReqs != 2 {
		t.Fatalf("expected exactly 2 HTTP requests in round 1 (1 normal + 1 supplementary), got %d", firstRoundReqs)
	}

	// 检查第 2 次请求确实携带了 task-1 的 agent.result
	mu.Lock()
	if len(receivedBatches) != 2 {
		mu.Unlock()
		t.Fatalf("expected 2 received batches, got %d", len(receivedBatches))
	}
	batch2 := receivedBatches[1]
	mu.Unlock()

	foundResult1 := false
	for _, req := range batch2 {
		if req.Method == protocol.MethodAgentResult {
			foundResult1 = true
			break
		}
	}
	if !foundResult1 {
		t.Fatalf("expected batch 2 to contain agent.result for task-1")
	}

	// --- 触发第二轮 flush（剩下的等下一轮）---
	err = wst.flushFallbackBatch()
	if err != nil {
		t.Fatalf("second round flushFallbackBatch error: %v", err)
	}

	// 第二轮中，留存的 task-2 被执行，其结果作为第二轮的常规/补充上报交上去
	secondRoundReqs := requestCount.Load()
	if secondRoundReqs < 3 {
		t.Fatalf("expected at least 3 total HTTP requests after second round, got %d", secondRoundReqs)
	}
}
