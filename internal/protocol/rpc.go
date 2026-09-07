package protocol

import (
	"encoding/json"
	"fmt"
)

// JSONRPCVersion 是 JSON-RPC 2.0 规范的版本字符串。
const JSONRPCVersion = "2.0"

// Request 是 JSON-RPC 2.0 请求/通知信封。
// 当 ID 为 nil 时为 Notification；非 nil 时为 Request。
type Request struct {
	JSONRPC string          `json:"jsonrpc"`          // 恒为 "2.0"
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      *int64          `json:"id,omitempty"`     // nil = notification
}

// Response 是 JSON-RPC 2.0 响应信封。
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int64          `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError 是 JSON-RPC 2.0 错误对象。
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("rpc error: code = %d, message = %s", e.Code, e.Message)
}
