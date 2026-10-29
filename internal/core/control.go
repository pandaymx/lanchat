package core

import (
	"strings"

	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/protocol"
)

// CancelFile 取消传输：支持 1:1 任务（通知对端 FILE_CANCEL）与群组任务（广播 GROUP_LEAVE）。
func (c *Client) CancelFile(transferID string) error {
	if g, ok := c.getGroupTask(transferID); ok {
		cp := g.currentSnapshot()
		if cp.State == appapi.TransferDone || cp.State == appapi.TransferCanceled || cp.State == appapi.TransferFailed {
			return errInvalidState
		}
		if conn, err := c.connectedConn(); err == nil {
			_, _ = c.sendGroup(conn, protocol.GroupLeave, g.groupID, protocol.GroupJoinPayload{
				GroupID:    g.groupID,
				TransferID: transferID,
			})
		}
		c.removeGroupTask(transferID)
		g.teardown()
		cp.State = appapi.TransferCanceled
		cp.SpeedBps = 0
		cp.ErrorReason = "canceled by local user"
		g.cache(cp)
		c.listener().OnTransferFailed(transferID, "canceled by local user")
		return nil
	}

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

// PauseFile 暂停处于 active 状态的传输任务（支持 1:1 与群组任务）。
func (c *Client) PauseFile(transferID string) error {
	if g, ok := c.getGroupTask(transferID); ok {
		cp, err := g.pause()
		if err != nil {
			return err
		}
		c.listener().OnTransferProgress(cp)
		return nil
	}

	t, ok := c.getTransfer(transferID)
	if !ok {
		return errTransferGone
	}
	cp, err := t.pause()
	if err != nil {
		return err
	}
	c.listener().OnTransferProgress(cp)
	return nil
}

// ResumeFile 恢复处于 paused 状态的传输任务（支持 1:1 与群组任务）。
func (c *Client) ResumeFile(transferID string) error {
	if g, ok := c.getGroupTask(transferID); ok {
		cp, err := g.resume()
		if err != nil {
			return err
		}
		c.listener().OnTransferProgress(cp)
		return nil
	}

	t, ok := c.getTransfer(transferID)
	if !ok {
		return errTransferGone
	}
	cp, err := t.resume()
	if err != nil {
		return err
	}
	c.listener().OnTransferProgress(cp)
	return nil
}

// ChannelCreate 创建 G2 自定义频道。服务器创建后会下发 CHANNEL_LIST，
// 频道 ID 通过 OnChannelUpdated 事件获得，故此处返回空字符串。
func (c *Client) ChannelCreate(name, topic string, private bool) (string, error) {
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		return "", errEmptyText
	}
	conn, err := c.connectedConn()
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	c.desiredChannels["pending:"+trimmedName] = desiredChannel{
		Name:        trimmedName,
		Topic:       strings.TrimSpace(topic),
		Private:     private,
		CreatedByMe: true,
	}
	c.mu.Unlock()
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
	c.mu.Lock()
	if ch, ok := c.channels[channelID]; ok && !ch.Private {
		c.desiredChannels[channelID] = desiredChannel{
			ID:      ch.ID,
			Name:    ch.Name,
			Topic:   ch.Topic,
			Private: ch.Private,
		}
	}
	c.mu.Unlock()
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
	c.mu.Lock()
	if gov := c.channelGov[channelID]; gov != nil && gov.kicked != nil {
		delete(gov.kicked, memberID)
	}
	c.mu.Unlock()
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
	c.mu.Lock()
	delete(c.desiredChannels, channelID)
	for k, dc := range c.desiredChannels {
		if dc.ID == channelID {
			delete(c.desiredChannels, k)
		}
	}
	c.mu.Unlock()
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
