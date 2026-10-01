package core

import (
	"github.com/google/uuid"

	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/protocol"
)

// OfferFileToGroup 发起群组文件 1:N 发送：打开文件 → SHA-256 → 监听块端口
// → GROUP_OFFER（携带块候选与一次性 token）。成员经 GROUP_JOIN 回报候选后
// 由 groupTask 重建 swarm 并供块。返回本次分发 ID。
func (c *Client) OfferFileToGroup(group, path string) (string, error) {
	if group == "" {
		return "", errPeerNotFound
	}
	conn, err := c.connectedConn()
	if err != nil {
		return "", err
	}
	info, err := openFileInfo(path)
	if err != nil {
		return "", err
	}
	sum, err := hashFile(info.file, info.size)
	if err != nil {
		_ = info.file.Close()
		return "", err
	}

	id := uuid.NewString()
	token := uuid.NewString()
	src := newSourceBlockSource(info.file, info.size)
	g := newSourceGroupTask(c, group, id, info.name, info.size, sum, src, nil)
	tr := newBlockTransport(c.selfID, token, g.deliverBlock, func(peer string) {
		c.notifyGroupDown(id, peer)
	})
	g.tr = tr
	cand, err := tr.listenOn()
	if err != nil {
		_ = info.file.Close()
		return "", err
	}

	if !c.addGroupTask(g) {
		_ = info.file.Close()
		tr.closeAll()
		return "", errInvalidState
	}
	go g.run()

	if _, err := c.sendGroup(conn, protocol.GroupOffer, group, protocol.GroupOfferPayload{
		GroupID:    group,
		TransferID: id,
		Name:       info.name,
		Size:       info.size,
		BlockCount: blockCount(info.size),
		SHA256:     sum,
		Seeders:    []string{c.selfID},
		Candidates: cand,
		Token:      token,
	}); err != nil {
		c.removeGroupTask(id)
		g.teardown()
		return "", err
	}

	// pending 进度让 UI 建立任务行。
	g.emitPending()
	return id, nil
}

// offerGroupInbound 收到 GROUP_OFFER 时建立入站 pending 任务并提示 UI。
func (c *Client) offerGroupInbound(env *protocol.Envelope) {
	var p protocol.GroupOfferPayload
	if err := env.DecodePayload(&p); err != nil || p.TransferID == "" {
		return
	}
	g := newReceiverGroupTask(c, env.Group, p.TransferID, p.Name, p.Size, p.SHA256, env.From)
	g.cand[env.From] = p.Candidates
	g.tok[env.From] = p.Token
	if !c.addGroupTask(g) {
		return
	}
	g.emitPending()
}

// respondGroupFile 接收方接受：创建块源与传输、回报 GROUP_JOIN、启动 swarm。
func (c *Client) respondGroupFile(t *groupTask, destPath string) error {
	conn, err := c.connectedConn()
	if err != nil {
		return err
	}
	src, err := newReceiverBlockSource(destPath, t.size, t.sha)
	if err != nil {
		return err
	}
	token := uuid.NewString()
	tr := newBlockTransport(c.selfID, token, t.deliverBlock, func(peer string) {
		c.notifyGroupDown(t.id, peer)
	})
	cand, err := tr.listenOn()
	if err != nil {
		src.close()
		return err
	}

	// 先登记源候选与源签发 token，再回报 JOIN，随后启动 swarm。
	tr.setCandidates(t.peerName, t.cand[t.peerName])
	tr.setPeerToken(t.peerName, t.tok[t.peerName])
	t.src = src
	t.tr = tr

	if _, err := c.sendGroup(conn, protocol.GroupJoin, t.groupID, protocol.GroupJoinPayload{
		GroupID:    t.groupID,
		TransferID: t.id,
		Candidates: cand,
		Token:      token,
	}); err != nil {
		src.close()
		tr.closeAll()
		return err
	}

	// 初始 swarm 只有源一个 seeder；rebuild 与 run 不可并发（rebuild 会写
	// g.swarm），故先 rebuild 再启动 actor。接收方启动时广播的位图可能早于
	// 数据面连接到达、成为迟到帧，源侧以 doneIDs 单调集合处理，见 emitSourceProgress。
	t.rebuild()
	go t.run()
	t.emitPending()
	return nil
}

// notifyGroupDown 通知某成员块连接失败。
func (c *Client) notifyGroupDown(transferID, peer string) {
	t, ok := c.getGroupTask(transferID)
	if !ok {
		return
	}
	t.post(groupEvent{kind: evDown, from: peer})
}

var _ = appapi.TransferActive
