package core

import (
	"strings"

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

// ChannelCreate 创建 G2 自定义频道。服务器创建后会下发 CHANNEL_LIST，
// 频道 ID 通过 OnChannelUpdated 事件获得，故此处返回空字符串。
func (c *Client) ChannelCreate(name, topic string, private bool) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", errEmptyText
	}
	conn, err := c.connectedConn()
	if err != nil {
		return "", err
	}
	if _, err := c.sendTo(conn, protocol.ChannelCreate, "", protocol.ChannelCreatePayload{
		Name: name, Topic: topic, Private: private,
	}); err != nil {
		return "", err
	}
	return "", nil
}

// ChannelJoin 加入 G2 自定义频道（public 频道任意在线成员可加入）。
func (c *Client) ChannelJoin(channelID string) error {
	if channelID == "" {
		return errPeerNotFound
	}
	conn, err := c.connectedConn()
	if err != nil {
		return err
	}
	_, err = c.sendTo(conn, protocol.ChannelJoin, "", protocol.ChannelJoinPayload{
		ChannelID: channelID,
	})
	return err
}

// ChannelInvite 邀请在线成员加入频道（仅 owner，权限由服务器强制）。
func (c *Client) ChannelInvite(channelID, memberID string) error {
	if channelID == "" || memberID == "" {
		return errPeerNotFound
	}
	conn, err := c.connectedConn()
	if err != nil {
		return err
	}
	_, err = c.sendTo(conn, protocol.ChannelInvite, "", protocol.ChannelInvitePayload{
		ChannelID: channelID, MemberID: memberID,
	})
	return err
}

// ChannelLeave 退出 G2 自定义频道。
func (c *Client) ChannelLeave(channelID string) error {
	if channelID == "" {
		return errPeerNotFound
	}
	conn, err := c.connectedConn()
	if err != nil {
		return err
	}
	_, err = c.sendTo(conn, protocol.ChannelLeave, "", protocol.ChannelLeavePayload{
		ChannelID: channelID,
	})
	return err
}

// ChannelList 主动请求频道列表并同步返回当前本地缓存。
// 最新列表由服务器经 CHANNEL_LIST 下发后通过 OnChannelUpdated 上抛。
func (c *Client) ChannelList() []appapi.Channel {
	conn, err := c.connectedConn()
	if err == nil {
		_, _ = c.sendTo(conn, protocol.ChannelList, "", nil)
	}
	c.mu.Lock()
	out := make([]appapi.Channel, 0, len(c.channels))
	for _, ch := range c.channels {
		out = append(out, ch)
	}
	c.mu.Unlock()
	return out
}
