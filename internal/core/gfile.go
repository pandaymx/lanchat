package core

import (
	"encoding/base64"

	"github.com/google/uuid"

	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/protocol"
	"github.com/pandaymx/lanchat/internal/transfer"
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

	// 生成本次群文件的对称密钥：源持有它供每条数据连接派生密钥，
	// 并在成员加入时经 GROUP_KEY 单播分发。
	fileKey, err := transfer.GenerateFileKey()
	if err != nil {
		_ = info.file.Close()
		return "", err
	}

	src := newSourceBlockSource(info.file, info.size)
	g := newSourceGroupTask(c, group, id, info.name, info.size, sum, src, fileKey)
	tr := newBlockTransport(c.selfID, token, g.deliverBlock, g.onPeerTransport)
	tr.blockDirect = c.forceRelay
	tr.onRelayRequest = func(peer string) { c.requestGroupRelay(id, peer) }
	g.tr = tr
	tr.setFileKey(fileKey)
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
	tr := newBlockTransport(c.selfID, token, t.deliverBlock, t.onPeerTransport)
	tr.blockDirect = c.forceRelay
	tr.onRelayRequest = func(peer string) { c.requestGroupRelay(t.id, peer) }
	cand, err := tr.listenOn()
	if err != nil {
		src.close()
		return err
	}

	// 先登记源候选与源签发 token，再回报 JOIN，随后启动 swarm。接收方的
	// fileKey 来自源的 GROUP_KEY，到位前不发起直连拨号（见 setFileKey）。
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

// requestGroupRelay 由 blockTransport 在直连失败时回调：经信令向服务器请求
// 与对端成员配对的中继会话（RELAY_GRANT 由服务器分别下发双方）。
func (c *Client) requestGroupRelay(transferID, peer string) {
	conn, err := c.connectedConn()
	if err != nil {
		return
	}
	_, _ = c.sendTo(conn, protocol.RelayRequest, "", protocol.RelayRequestPayload{
		TransferID: transferID,
		PeerID:     peer,
	})
}

// deliverGroupKey 收到源单播的 GROUP_KEY：解码并登记到群任务，此后才能
// 建立加密块连接（直连或中继）。
func (c *Client) deliverGroupKey(env *protocol.Envelope) {
	var p protocol.GroupKeyPayload
	if err := env.DecodePayload(&p); err != nil || p.TransferID == "" || p.FileKey == "" {
		return
	}
	t, ok := c.getGroupTask(p.TransferID)
	if !ok {
		return
	}
	key, err := base64.StdEncoding.DecodeString(p.FileKey)
	if err != nil || len(key) == 0 {
		return
	}
	t.applyFileKey(key)
}

// deliverGroupRelayGrant 把群文件中继授权投递到对应群任务（PeerID 标明
// 对端成员）；返回是否已按群任务消费。
func (c *Client) deliverGroupRelayGrant(env *protocol.Envelope) bool {
	var p protocol.RelayGrantPayload
	if err := env.DecodePayload(&p); err != nil || p.TransferID == "" || p.PeerID == "" {
		return false
	}
	t, ok := c.getGroupTask(p.TransferID)
	if !ok {
		return false
	}
	t.applyRelayGrant(p)
	return true
}

var _ = appapi.TransferActive
