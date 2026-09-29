package core

import (
	"errors"
	"time"
)

// 超时与直连尝试参数。
const (
	writeTimeout     = 5 * time.Second
	dialTimeout      = 5 * time.Second
	handshakeTimeout = 8 * time.Second
	directTryTimeout = 3 * time.Second
	maxBackoff       = 10 * time.Second
)

// 参数校验类错误。
var (
	errEmptyPath     = errors.New("core: path must not be empty")
	errEmptyNickname = errors.New("core: nickname must not be empty")
	errEmptyText     = errors.New("core: text must not be empty")
	errEmptyAddress  = errors.New("core: server address must not be empty")
	errNotConnected  = errors.New("core: not connected")
	errPeerNotFound  = errors.New("core: peer not found")
	errFileNotFound  = errors.New("core: file not found")
	errTransferGone  = errors.New("core: transfer not found or already finished")
	errInvalidState  = errors.New("core: operation not allowed in current state")
)

// errNotImplemented 是 M4 诚实边界：群组 / 频道属 M9，M4 明确拒绝而非臆造。
var errNotImplemented = errors.New("core: not implemented before M9")

// errAuthFailed 是 establish 阶段鉴权失败的哨兵错误，Connect/watch 据此切换 auth_failed。
var errAuthFailed = errors.New("core: authentication failed")

// errPauseUnsupported 是 M4 对暂停语义的诚实边界（见 control.go）。
var errPauseUnsupported = errors.New("core: pause/resume not supported in M4")
