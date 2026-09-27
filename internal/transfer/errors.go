package transfer

import "errors"

// 传输层公开错误。
var (
	// ErrCanceled 表示传输因 ctx 取消或对端 CANCEL 而中止。
	ErrCanceled = errors.New("transfer: canceled")
	// ErrDialFailed 表示所有候选地址均拨号失败。
	ErrDialFailed = errors.New("transfer: dial failed: no candidate reachable")
	// ErrChecksum 表示全文件 SHA-256 校验不一致。
	ErrChecksum = errors.New("transfer: checksum mismatch")
	// ErrPeerError 表示对端返回 ERROR 帧。
	ErrPeerError = errors.New("transfer: peer error")
	// ErrBadToken 表示一次性 token 校验失败。
	ErrBadToken = errors.New("transfer: bad token")
)
