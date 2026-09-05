package core

import "errors"

// 通用 sentinel errors，调用者用 errors.Is 判断。
//
// 每条错误都附短描述，使用前请阅读：这是契约的一部分。
var (
	// ErrClosed 在对已关闭的 Conn/Subscription/Store 调用方法时返回。
	ErrClosed = errors.New("core: closed")

	// ErrNotFound 在 GetUser/GetDevice/GetConversation/GetCursor 等查找方法未命中时返回。
	// 调用方可用 errors.Is(err, ErrNotFound) 判断。
	ErrNotFound = errors.New("core: not found")

	// ErrProtocolMismatch 客户端的 Hello.ProtocolVersion 与服务端期望不符。
	// Hub 会在 FKError 中带上该 Code 后关连接。
	ErrProtocolMismatch = errors.New("core: protocol mismatch")

	// ErrUnauthorized Auth 失败（M8 鉴权实装时启用，目前预留）。
	ErrUnauthorized = errors.New("core: unauthorized")

	// ErrRateLimited 触发了速率限制（M8 引入，目前 stub）。
	ErrRateLimited = errors.New("core: rate limited")
)
