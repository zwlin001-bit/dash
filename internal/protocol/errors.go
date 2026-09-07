package protocol

// 标准 JSON-RPC 2.0 错误码与 dash 协议错误码
const (
	// JSON-RPC 2.0 标准错误码
	ErrCodeParse          = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602

	// dash 协议自定义错误码 (-32000 ~ -32004)
	ErrCodeTokenInvalid    = -32000 // token 无效或已吊销（agent 收到后停止重连）
	ErrCodeVersionMismatch = -32001 // 协议版本不兼容
	ErrCodeCapDisabled     = -32002 // 能力未开启（如 exec_mode=off 时收到 exec）
	ErrCodeExecTimeout     = -32003 // 执行超时
	ErrCodeRateLimited     = -32004 // 限流
)
