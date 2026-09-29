package core

// addTransfer 注册任务；返回 false 表示 ID 已存在。
func (c *Client) addTransfer(t *transferTask) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.transfers[t.snap.ID]; ok {
		return false
	}
	c.transfers[t.snap.ID] = t
	return true
}

func (c *Client) getTransfer(id string) (*transferTask, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.transfers[id]
	return t, ok
}

// removeTransfer 从注册表摘除任务（快照不再包含）。
func (c *Client) removeTransfer(id string) {
	c.mu.Lock()
	delete(c.transfers, id)
	c.mu.Unlock()
}

// peerNickname 返回在线对端昵称（未知则返回空）。
func (c *Client) peerNickname(id string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.peers[id].Nickname
}
