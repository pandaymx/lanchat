package core

import (
	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/protocol"
)

// CancelFile 取消传输：取消任务数据路径并通知对端 FILE_CANCEL。
func (c *Client) CancelFile(transferID string) error {
	t, ok := c.getTransfer(transferID)
	if !ok {
		return errTransferGone
	}
	state := t.snapshot().State
	if state == appapi.TransferDone || state == appapi.TransferCanceled || state == appapi.TransferFailed {
		return errInvalidState
	}

	conn, err := c.connectedConn()
	if err == nil {
		_, _ = c.sendTo(conn, protocol.FileCancel, t.snap.PeerID, protocol.FileCancelPayload{
			TransferID: transferID,
		})
	}
	t.cancel()
	t.canceled("canceled by local user")
	return nil
}

// PauseFile M4 不支持暂停（M3 的续传是崩溃恢复而非用户暂停）。
func (c *Client) PauseFile(transferID string) error {
	if _, ok := c.getTransfer(transferID); !ok {
		return errTransferGone
	}
	return errPauseUnsupported
}

// ResumeFile 与 PauseFile 对称，M4 不支持。
func (c *Client) ResumeFile(transferID string) error {
	if _, ok := c.getTransfer(transferID); !ok {
		return errTransferGone
	}
	return errPauseUnsupported
}

// OfferFileToGroup 群组发送属 M9，M4 明确拒绝。
func (c *Client) OfferFileToGroup(group, path string) (string, error) {
	return "", errNotImplemented
}

// ChannelCreate G2 自定义频道属 M9。
func (c *Client) ChannelCreate(name string) (string, error) {
	return "", errNotImplemented
}

// ChannelJoin G2 自定义频道属 M9。
func (c *Client) ChannelJoin(channelID string) error {
	return errNotImplemented
}

// ChannelList M4 无频道概念，返回空切片（非 nil，便于各端直接渲染）。
func (c *Client) ChannelList() []appapi.Channel {
	return []appapi.Channel{}
}
