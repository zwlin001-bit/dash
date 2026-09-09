package exec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dash/internal/protocol"
)

type mockSender struct {
	mu   sync.Mutex
	msgs []mockMsg
}

type mockMsg struct {
	method string
	params any
}

func (m *mockSender) Send(method string, params any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.msgs = append(m.msgs, mockMsg{method: method, params: params})
	return nil
}

func (m *mockSender) getMsgs() []mockMsg {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]mockMsg, len(m.msgs))
	copy(out, m.msgs)
	return out
}

// 判据 1：下发一个表外 action → 收到 -32602，且机器上无任何副作用
func TestCriterion1_UnregisteredActionRejected(t *testing.T) {
	reg := NewRegistry()
	sender := &mockSender{}
	exec := NewExecutor(reg, sender)

	// 创建一个探测文件，若被恶意执行则文件会被创建
	tmpDir := t.TempDir()
	canaryFile := filepath.Join(tmpDir, "canary.txt")

	payload := fmt.Sprintf(`{
		"request_id": "req-001",
		"action": "system.destroy",
		"args": {"path": %q},
		"timeout_s": 5
	}`, canaryFile)

	res, err := exec.HandleExec(json.RawMessage(payload))
	if err == nil {
		t.Fatalf("expected error for unregistered action, got nil (res=%v)", res)
	}

	rpcErr, ok := err.(*protocol.RPCError)
	if !ok {
		t.Fatalf("expected *protocol.RPCError, got %T: %v", err, err)
	}
	if rpcErr.Code != protocol.ErrCodeInvalidParams {
		t.Fatalf("expected code %d (-32602), got %d", protocol.ErrCodeInvalidParams, rpcErr.Code)
	}

	// 确认机器上无任何副作用（探针文件未创建）
	if _, statErr := os.Stat(canaryFile); statErr == nil {
		t.Fatalf("side effect detected: canary file was created")
	}

	// 确认未发送任何 agent.result
	if len(sender.getMsgs()) > 0 {
		t.Fatalf("expected no agent.result sent for rejected action, got %d msgs", len(sender.getMsgs()))
	}
}

// 判据 2：下发带 mode / cmd / shell / script 字段的请求 → 一律 -32602
func TestCriterion2_ForbiddenFieldsRejected(t *testing.T) {
	reg := NewRegistry()
	_ = reg.RegisterNoop("test.noop")

	sender := &mockSender{}
	exec := NewExecutor(reg, sender)

	forbiddenTestCases := []struct {
		name    string
		payload string
	}{
		{
			name: "with mode: shell",
			payload: `{
				"request_id": "req-f1",
				"mode": "shell",
				"action": "test.noop",
				"args": {}
			}`,
		},
		{
			name: "with mode: action",
			payload: `{
				"request_id": "req-f2",
				"mode": "action",
				"action": "test.noop",
				"args": {}
			}`,
		},
		{
			name: "with cmd",
			payload: `{
				"request_id": "req-f3",
				"cmd": "whoami",
				"action": "test.noop",
				"args": {}
			}`,
		},
		{
			name: "with shell",
			payload: `{
				"request_id": "req-f4",
				"shell": "/bin/bash",
				"action": "test.noop",
				"args": {}
			}`,
		},
		{
			name: "with script",
			payload: `{
				"request_id": "req-f5",
				"script": "echo hello",
				"action": "test.noop",
				"args": {}
			}`,
		},
	}

	for _, tc := range forbiddenTestCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := exec.HandleExec(json.RawMessage(tc.payload))
			if err == nil {
				t.Fatalf("expected -32602 error for payload %s, got nil", tc.payload)
			}
			rpcErr, ok := err.(*protocol.RPCError)
			if !ok {
				t.Fatalf("expected *protocol.RPCError, got %T: %v", err, err)
			}
			if rpcErr.Code != protocol.ErrCodeInvalidParams {
				t.Fatalf("expected code %d (-32602), got %d (msg: %s)",
					protocol.ErrCodeInvalidParams, rpcErr.Code, rpcErr.Message)
			}
		})
	}
}

// 判据 3：grep -rn 'sudo' agent/ 在降权路径里零命中（只允许"要 root 时自适应"那处）
func TestCriterion3_NoSudoInDropPrivilegePath(t *testing.T) {
	files := []string{"registry.go", "runner.go", "executor.go"}
	for _, f := range files {
		content, err := os.ReadFile(f)
		if err != nil {
			content, err = os.ReadFile(filepath.Join("/opt/git/dash/agent/exec", f))
			if err != nil {
				t.Fatalf("read file %s: %v", f, err)
			}
		}

		lines := strings.Split(string(content), "\n")
		inElev := false
		elevDepth := 0
		for i, line := range lines {
			lineTrimmed := strings.TrimSpace(line)
			if strings.HasPrefix(lineTrimmed, "func Elev(") {
				inElev = true
				elevDepth = 0
			}

			if inElev {
				elevDepth += strings.Count(line, "{") - strings.Count(line, "}")
				if elevDepth == 0 && strings.Contains(line, "}") {
					inElev = false
				}
				continue
			}

			if strings.Contains(line, "sudo") {
				t.Fatalf("file %s:%d contains 'sudo' outside Elev(): %s", f, i+1, line)
			}
		}
	}
}

// 判据 4：在一台没有目标用户的机器上跑 no-op 动作 → 不降权、正常返回，不是错误
func TestCriterion4_NonExistentUserNoDropAndNoError(t *testing.T) {
	// 无论当前是 root 还是非 root，目标用户不存在都不抛错，正常执行命令
	res, err := Run(context.Background(), RunOptions{
		Command: []string{"echo", "hello-noop"},
		Timeout: 5 * time.Second,
		RunAs:   "non_existent_ghost_user_xyz_123456789",
	})
	if err != nil {
		t.Fatalf("expected no error for non-existent user, got %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("expected exit_code 0, got %d (stderr: %s)", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "hello-noop") {
		t.Fatalf("expected stdout to contain 'hello-noop', got %q", res.Stdout)
	}
}

// 判据 5：超时用例返回 exit_code=124；超长输出 truncated=true 且长度收敛
func TestCriterion5_TimeoutExitCode124AndOutputTruncated(t *testing.T) {
	// 1. 超时用例验证
	timeoutRes, err := Run(context.Background(), RunOptions{
		Command: []string{"sleep", "2"},
		Timeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Run timeout error: %v", err)
	}
	if timeoutRes.ExitCode != 124 {
		t.Fatalf("expected exit_code 124 for timeout, got %d", timeoutRes.ExitCode)
	}

	// 2. 超长输出用例验证
	// 打印 30000 字节的大量文本
	bigOutputScript := strings.Repeat("0123456789abcdef", 2000) // 32000 bytes
	truncRes, err := Run(context.Background(), RunOptions{
		Command: []string{"echo", bigOutputScript},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run big output error: %v", err)
	}
	if truncRes.ExitCode != 0 {
		t.Fatalf("expected exit_code 0, got %d", truncRes.ExitCode)
	}
	if !truncRes.Truncated {
		t.Fatalf("expected truncated=true for 32KB output, got false")
	}
	totalLen := len(truncRes.Stdout) + len(truncRes.Stderr)
	if totalLen > OutputTruncateLimit {
		t.Fatalf("expected output length <= %d, got %d", OutputTruncateLimit, totalLen)
	}
}

// 判据 6：agent.result 的 8 个字段都有值且语义正确
func TestCriterion6_AgentResult8FieldsSemantics(t *testing.T) {
	reg := NewRegistry()
	_ = reg.Register("sample.action", func(ctx context.Context, args map[string]interface{}) (*ActionResult, error) {
		return &ActionResult{
			ExitCode:  0,
			Stdout:    "action-output",
			Stderr:    "action-warning",
			Truncated: false,
		}, nil
	})

	var reportedResult *protocol.AgentResultParams
	exec := NewExecutor(reg, nil)
	exec.SetOnResult(func(res *protocol.AgentResultParams) {
		reportedResult = res
	})

	payload := `{
		"request_id": "req-test-8fields",
		"action": "sample.action",
		"args": {"foo": "bar"},
		"timeout_s": 5
	}`

	beforeCall := time.Now().UnixMilli()
	_, err := exec.HandleExec(json.RawMessage(payload))
	afterCall := time.Now().UnixMilli()
	if err != nil {
		t.Fatalf("HandleExec failed: %v", err)
	}

	if reportedResult == nil {
		t.Fatalf("expected agent.result to be reported, got nil")
	}

	// 验证 8 个字段均有值且语义正确
	// 1. RequestID
	if reportedResult.RequestID != "req-test-8fields" {
		t.Errorf("field 1 request_id mismatch: got %q, want 'req-test-8fields'", reportedResult.RequestID)
	}
	// 2. OK
	if !reportedResult.OK {
		t.Errorf("field 2 ok mismatch: expected true for exit_code 0")
	}
	// 3. ExitCode
	if reportedResult.ExitCode != 0 {
		t.Errorf("field 3 exit_code mismatch: got %d, want 0", reportedResult.ExitCode)
	}
	// 4. Stdout
	if reportedResult.Stdout != "action-output" {
		t.Errorf("field 4 stdout mismatch: got %q", reportedResult.Stdout)
	}
	// 5. Stderr
	if reportedResult.Stderr != "action-warning" {
		t.Errorf("field 5 stderr mismatch: got %q", reportedResult.Stderr)
	}
	// 6. Truncated
	if reportedResult.Truncated != false {
		t.Errorf("field 6 truncated mismatch: expected false")
	}
	// 7. StartedAtMs
	if reportedResult.StartedAtMs < beforeCall || reportedResult.StartedAtMs > afterCall {
		t.Errorf("field 7 started_at_ms %d out of bounds [%d, %d]",
			reportedResult.StartedAtMs, beforeCall, afterCall)
	}
	// 8. EndedAtMs
	if reportedResult.EndedAtMs < reportedResult.StartedAtMs || reportedResult.EndedAtMs > afterCall+100 {
		t.Errorf("field 8 ended_at_ms %d invalid relative to start %d and afterCall %d",
			reportedResult.EndedAtMs, reportedResult.StartedAtMs, afterCall)
	}
}

// 验证「永不 shell」：argv 不经由 shell 解释
func TestRunner_NeverShell(t *testing.T) {
	// 若经由 shell 解释，echo hello; echo world 会输出两行并分别执行
	// 作为 argv 数组传递时，它被当作单个参数完整打印
	res, err := Run(context.Background(), RunOptions{
		Command: []string{"echo", "hello; echo world"},
		Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("expected exit_code 0, got %d", res.ExitCode)
	}
	// 必须包含分号，证明未被 shell 拆分执行
	if !strings.Contains(res.Stdout, "hello; echo world") {
		t.Fatalf("expected literal 'hello; echo world', got %q", res.Stdout)
	}
}
