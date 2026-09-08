package exec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"dash/internal/protocol"
)

// TransportSender 抽象执行结果上报所依赖的传输层接口
type TransportSender interface {
	Send(method string, params any) error
}

// Executor 是 server.exec 下行请求的校验与执行调度器。
type Executor struct {
	registry       *Registry
	transport      TransportSender
	defaultTimeout time.Duration
	onResult       func(*protocol.AgentResultParams)
}

// NewExecutor 创建执行调度器实例。
func NewExecutor(reg *Registry, tr TransportSender) *Executor {
	if reg == nil {
		reg = NewRegistry()
	}
	return &Executor{
		registry:       reg,
		transport:      tr,
		defaultTimeout: DefaultTimeout,
	}
}

// SetOnResult 设置结果上报回调（主要供测试与审计）。
func (e *Executor) SetOnResult(fn func(*protocol.AgentResultParams)) {
	e.onResult = fn
}

// Registry 返回底层的白名单注册表。
func (e *Executor) Registry() *Registry {
	return e.registry
}

// SetTransport 更新传输层发送接口。
func (e *Executor) SetTransport(tr TransportSender) {
	e.transport = tr
}

// HandleExec 处理 server.exec 请求（98.md §2.1 与 §2.2）。
// 严格执行安全闸：
// 1. 拦截 forbidden fields（mode, cmd, shell, script）→ -32602；
// 2. 拦截表外 action → -32602，不执行、不落地、不猜；
// 3. 执行完毕后将 8 字段完整的 agent.result 回传服务端。
func (e *Executor) HandleExec(rawParams json.RawMessage) (any, error) {
	// 1. 原始 JSON 字段校验：收到 mode / cmd / shell / script 等一律回 -32602
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(rawParams, &rawMap); err != nil {
		return nil, &protocol.RPCError{
			Code:    protocol.ErrCodeInvalidParams,
			Message: "invalid server.exec json format",
		}
	}

	forbiddenKeys := []string{"mode", "cmd", "shell", "script"}
	for _, fk := range forbiddenKeys {
		if _, exists := rawMap[fk]; exists {
			return nil, &protocol.RPCError{
				Code:    protocol.ErrCodeInvalidParams,
				Message: fmt.Sprintf("forbidden field %q in server.exec", fk),
			}
		}
	}

	// 2. 反序列化为 ServerExecParams
	var p protocol.ServerExecParams
	if err := json.Unmarshal(rawParams, &p); err != nil {
		return nil, &protocol.RPCError{
			Code:    protocol.ErrCodeInvalidParams,
			Message: fmt.Sprintf("parse server.exec params failed: %v", err),
		}
	}

	if strings.TrimSpace(p.RequestID) == "" {
		return nil, &protocol.RPCError{
			Code:    protocol.ErrCodeInvalidParams,
			Message: "missing request_id",
		}
	}
	if strings.TrimSpace(p.Action) == "" {
		return nil, &protocol.RPCError{
			Code:    protocol.ErrCodeInvalidParams,
			Message: "missing action",
		}
	}

	// 3. 白名单注册表检查：不在表里 → 回 -32602，不执行、不落地、不猜
	handler, ok := e.registry.Get(p.Action)
	if !ok {
		return nil, &protocol.RPCError{
			Code:    protocol.ErrCodeInvalidParams,
			Message: fmt.Sprintf("unsupported action %q", p.Action),
		}
	}

	// 4. 执行动作与计时
	timeout := e.defaultTimeout
	if p.TimeoutS > 0 {
		timeout = time.Duration(p.TimeoutS) * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	startedAtMs := time.Now().UnixMilli()
	actionRes, err := handler(ctx, p.Args)
	endedAtMs := time.Now().UnixMilli()

	if endedAtMs < startedAtMs {
		endedAtMs = startedAtMs
	}

	if err != nil && actionRes == nil {
		actionRes = &ActionResult{
			ExitCode:  1,
			Stdout:    "",
			Stderr:    err.Error(),
			Truncated: false,
		}
	}

	// 5. 组装并发送 agent.result（8 个字段必须齐全且语义正确）
	resultParams := &protocol.AgentResultParams{
		RequestID:   p.RequestID,
		OK:          actionRes.ExitCode == 0,
		ExitCode:    actionRes.ExitCode,
		Stdout:      actionRes.Stdout,
		Stderr:      actionRes.Stderr,
		Truncated:   actionRes.Truncated,
		StartedAtMs: startedAtMs,
		EndedAtMs:   endedAtMs,
	}

	if e.transport != nil {
		_ = e.transport.Send(protocol.MethodAgentResult, resultParams)
	}

	if e.onResult != nil {
		e.onResult(resultParams)
	}

	return map[string]interface{}{
		"request_id": p.RequestID,
		"ok":         resultParams.OK,
	}, nil
}
