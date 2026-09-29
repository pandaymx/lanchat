// Package ipc 实现 LANChat daemon 服务端侧的进程间通信：
// 在桌面端监听 Unix socket（Linux/macOS）或命名管道（Windows），
// 以 JSON-RPC 2.0 暴露 internal/appapi 契约，并把 core 事件作为
// notification 推送给已连接的 UI。
//
// 唯一真源为 internal/appapi 与 api/ipc.schema.json；本包不得实现
// 契约之外的方法或字段。
package ipc

import "encoding/json"

// rpcVersion 是 JSON-RPC 2.0 固定版本号。
const rpcVersion = "2.0"

// Request 是 JSON-RPC 2.0 请求（notification 时 ID 为 nil）。
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// isNotification 判定是否为不带 ID 的通知（无需回包）。
func (r Request) isNotification() bool { return len(r.ID) == 0 }

// Response 是 JSON-RPC 2.0 响应。
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError 是 JSON-RPC 2.0 错误对象。
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return e.Message }

// JSON-RPC 2.0 标准错误码。
const (
	codeParseError   = -32700
	codeInvalidReq   = -32600
	codeNoSuchMethod = -32601
	codeInvalidParam = -32602
	codeInternal     = -32603
)

func errResponse(id json.RawMessage, code int, message string) Response {
	return Response{
		JSONRPC: rpcVersion,
		ID:      id,
		Error:   &RPCError{Code: code, Message: message},
	}
}

func resultResponse(id json.RawMessage, result interface{}) Response {
	return Response{
		JSONRPC: rpcVersion,
		ID:      id,
		Result:  result,
	}
}

// notification 是 daemon → UI 的下行事件信封（schema notifications）。
type notification struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

// encode 序列化为一行并追加换行（帧分隔）。
func encode(v interface{}) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
