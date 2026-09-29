package core

import "github.com/pandaymx/lanchat/internal/appapi"

// noopListener 在 UI 未注册回调时丢弃全部事件。
type noopListener struct{}

func (noopListener) OnConnChanged(appapi.ConnState, string)                   {}
func (noopListener) OnPeerJoined(appapi.Peer)                                 {}
func (noopListener) OnPeerLeft(string)                                        {}
func (noopListener) OnMessageReceived(string, string, string, string, string) {}
func (noopListener) OnTransferProgress(appapi.Transfer)                       {}
func (noopListener) OnTransferDone(string)                                    {}
func (noopListener) OnTransferFailed(string, string)                          {}
func (noopListener) OnGroupMatrix(string, string, []byte)                     {}
func (noopListener) OnChannelUpdated([]appapi.Channel)                        {}

// listener 返回当前回调；未注册时返回丢弃实现，避免每处判空。
func (c *Client) listener() appapi.Listener {
	c.mu.Lock()
	l := c.cb
	c.mu.Unlock()
	if l == nil {
		return noopListener{}
	}
	return l
}
