package core

import (
	"strconv"
	"strings"
	"time"

	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/protocol"
	"github.com/pandaymx/lanchat/internal/transfer"
)

// toPeer 协议 User → appapi.Peer。
func toPeer(u protocol.User) appapi.Peer {
	return appapi.Peer{ID: u.ID, Nickname: u.Nickname, OS: u.OS, Status: u.Status}
}

func containsMember(members []string, id string) bool {
	for _, m := range members {
		if m == id {
			return true
		}
	}
	return false
}

// applyUserList 用全量在线表替换 peers；重连后 UI 依赖 join 事件重建视图，
// 因此对新增用户逐一发 OnPeerJoined，对消失用户发 OnPeerLeft。
func (c *Client) applyUserList(env *protocol.Envelope) {
	var p protocol.UserListPayload
	if err := env.DecodePayload(&p); err != nil {
		return
	}

	c.mu.Lock()
	selfID := c.selfID
	old := c.peers
	next := make(map[string]appapi.Peer, len(p.Users))
	var joined []appapi.Peer
	var left []string
	pubChanged := false
	for _, u := range p.Users {
		if u.ID == selfID {
			continue
		}
		if u.PubKey != "" && c.peerPubs[u.ID] != u.PubKey {
			c.peerPubs[u.ID] = u.PubKey
			pubChanged = true
		}
		peer := toPeer(u)
		next[u.ID] = peer
		if _, existed := old[u.ID]; !existed {
			joined = append(joined, peer)
		}
	}
	for id := range old {
		if _, keep := next[id]; !keep {
			left = append(left, id)
		}
	}
	c.peers = next
	c.mu.Unlock()
	if pubChanged {
		c.persistKeyDirectory()
	}

	l := c.listener()
	for _, peer := range joined {
		l.OnPeerJoined(peer)
	}
	for _, id := range left {
		l.OnPeerLeft(id)
	}
	c.distributePendingChannelKeys()
	c.scheduleAutoRoamPeers(joined)
}

// applyChannelList 用服务器下发的全量频道表替换本地缓存并通知 UI；
// 同时执行 M18 断线/切主频道自愈与 M19 频道群密钥派生及 ECDH 自动分发。
func (c *Client) applyChannelList(in []protocol.Channel) {
	c.mu.Lock()
	next := make(map[string]appapi.Channel, len(in))
	for _, ch := range in {
		members := make([]string, 0, len(ch.Members))
		var admins, muted, acks []string
		if gov := c.channelGov[ch.ID]; gov != nil {
			for _, m := range ch.Members {
				if !gov.kicked[m] {
					members = append(members, m)
				}
			}
			admins = append([]string(nil), gov.admins...)
			muted = append([]string(nil), gov.muted...)
			acks = append([]string(nil), gov.announcementAck...)
		} else {
			members = append(members, ch.Members...)
		}
		next[ch.ID] = appapi.Channel{
			ID: ch.ID, Name: ch.Name, OwnerID: ch.OwnerID,
			Private: ch.Private, Topic: ch.Topic, Members: members,
			Admins: admins, Muted: muted, AnnouncementAck: acks,
		}
	}

	selfID := c.selfID
	c.channels = next

	for _, ch := range next {
		pendingKey := "pending:" + strings.TrimSpace(ch.Name)
		if pendingDC, ok := c.desiredChannels[pendingKey]; ok {
			delete(c.desiredChannels, pendingKey)
			c.desiredChannels[ch.ID] = desiredChannel{
				ID:          ch.ID,
				Name:        ch.Name,
				Topic:       ch.Topic,
				Private:     ch.Private,
				CreatedByMe: pendingDC.CreatedByMe || ch.OwnerID == selfID,
			}
		}
		if ch.OwnerID == selfID || containsMember(ch.Members, selfID) {
			prev := c.desiredChannels[ch.ID]
			c.desiredChannels[ch.ID] = desiredChannel{
				ID:          ch.ID,
				Name:        ch.Name,
				Topic:       ch.Topic,
				Private:     ch.Private,
				CreatedByMe: prev.CreatedByMe || ch.OwnerID == selfID,
			}
		}
		if ch.OwnerID == selfID && c.identityPriv != nil && c.channelKeys[ch.ID] == "" {
			if k, err := transfer.DeriveChannelKey(c.identityPriv, ch.ID); err == nil && k != "" {
				c.channelKeys[ch.ID] = k
			}
		}
	}

	var rejoinIDs []string
	var recreateList []desiredChannel
	for key, dc := range c.desiredChannels {
		if strings.HasPrefix(key, "pending:") {
			continue
		}
		if ch, exists := next[dc.ID]; exists {
			if !containsMember(ch.Members, selfID) && !ch.Private {
				rejoinIDs = append(rejoinIDs, dc.ID)
			}
			continue
		}
		// 原频道 ID 在当前服务器上不存在（如服务端重启或 M14 房主切换），尝试按同名匹配或由创建者自动重建。
		var matched *appapi.Channel
		for _, cand := range next {
			if cand.Name == dc.Name {
				cp := cand
				matched = &cp
				break
			}
		}
		if matched != nil {
			delete(c.desiredChannels, key)
			dc.ID = matched.ID
			c.desiredChannels[matched.ID] = dc
			if !containsMember(matched.Members, selfID) && !matched.Private {
				rejoinIDs = append(rejoinIDs, matched.ID)
			}
		} else if dc.CreatedByMe && dc.Name != "" {
			delete(c.desiredChannels, key)
			c.desiredChannels["pending:"+dc.Name] = dc
			recreateList = append(recreateList, dc)
		}
	}

	snapshot := make([]appapi.Channel, 0, len(next))
	for _, ch := range next {
		snapshot = append(snapshot, ch)
	}
	c.mu.Unlock()

	c.listener().OnChannelUpdated(snapshot)

	if conn, err := c.connectedConn(); err == nil {
		for _, chID := range rejoinIDs {
			_, _ = c.sendTo(conn, protocol.ChannelJoin, "", protocol.ChannelJoinPayload{ChannelID: chID})
		}
		for _, dc := range recreateList {
			_, _ = c.sendTo(conn, protocol.ChannelCreate, "", protocol.ChannelCreatePayload{
				Name: dc.Name, Topic: dc.Topic, Private: dc.Private,
			})
		}
	}
	c.distributePendingChannelKeys()
	c.autoRoamChannels(snapshot)
}

type pendingKeyGrant struct {
	memberID string
	peerPub  string
	chID     string
	chKey    string
}

// distributePendingChannelKeys 由频道 Owner 将频道对称密钥通过 M15 X25519 ECDH 单播分发给尚未拿到密钥的成员（M19）。
func (c *Client) distributePendingChannelKeys() {
	c.mu.Lock()
	selfID := c.selfID
	idPriv := c.identityPriv
	idPub := c.identityPub
	if selfID == "" || idPriv == nil {
		c.mu.Unlock()
		return
	}
	var grants []pendingKeyGrant
	for _, ch := range c.channels {
		if ch.OwnerID != selfID {
			continue
		}
		chKey := c.channelKeys[ch.ID]
		if chKey == "" {
			if derived, err := transfer.DeriveChannelKey(idPriv, ch.ID); err == nil && derived != "" {
				chKey = derived
				c.channelKeys[ch.ID] = chKey
			}
		}
		if chKey == "" {
			continue
		}
		if c.channelKeySent[ch.ID] == nil {
			c.channelKeySent[ch.ID] = map[string]bool{}
		}
		for _, memberID := range ch.Members {
			if memberID == "" || memberID == selfID || c.channelKeySent[ch.ID][memberID] {
				continue
			}
			peerPub := c.peerPubs[memberID]
			if peerPub == "" {
				continue
			}
			c.channelKeySent[ch.ID][memberID] = true
			grants = append(grants, pendingKeyGrant{
				memberID: memberID,
				peerPub:  peerPub,
				chID:     ch.ID,
				chKey:    chKey,
			})
		}
	}
	c.mu.Unlock()

	if len(grants) == 0 {
		return
	}
	conn, err := c.connectedConn()
	if err != nil {
		return
	}
	for _, g := range grants {
		ctrlPlain := "sys:chkey:" + g.chID + ":" + g.chKey
		sealed, err := transfer.SealECDHText(idPriv, g.peerPub, ctrlPlain)
		if err != nil {
			continue
		}
		_, _ = c.sendTo(conn, protocol.TextMsg, g.memberID, protocol.TextPayload{
			Text:      sealed,
			SenderPub: idPub,
		})
	}
}

// applyJoin 增量上线。
func (c *Client) applyJoin(env *protocol.Envelope) {
	var p protocol.UserUpdatePayload
	if err := env.DecodePayload(&p); err != nil {
		return
	}
	c.mu.Lock()
	if p.User.ID == c.selfID {
		c.mu.Unlock()
		return
	}
	pubChanged := false
	if p.User.PubKey != "" && c.peerPubs[p.User.ID] != p.User.PubKey {
		c.peerPubs[p.User.ID] = p.User.PubKey
		pubChanged = true
	}
	c.peers[p.User.ID] = toPeer(p.User)
	peer := c.peers[p.User.ID]
	c.mu.Unlock()
	if pubChanged {
		c.persistKeyDirectory()
	}
	c.listener().OnPeerJoined(peer)
	c.distributePendingChannelKeys()
	c.scheduleAutoRoamPeers([]appapi.Peer{peer})
}

// applyLeave 增量下线。
func (c *Client) applyLeave(env *protocol.Envelope) {
	var p protocol.UserUpdatePayload
	if err := env.DecodePayload(&p); err != nil {
		return
	}
	c.mu.Lock()
	if _, ok := c.peers[p.User.ID]; !ok {
		c.mu.Unlock()
		return
	}
	delete(c.peers, p.User.ID)
	delete(c.lastAutoRoam, "peer:"+p.User.ID)
	for k := range c.lastAutoRoam {
		if strings.HasSuffix(k, ":"+p.User.ID) {
			delete(c.lastAutoRoam, k)
		}
	}
	for _, sentMap := range c.channelKeySent {
		delete(sentMap, p.User.ID)
	}
	c.mu.Unlock()
	c.listener().OnPeerLeft(p.User.ID)
}

// applyPresence 更新资料；UI 经 OnPeerJoined 复用刷新视图。
func (c *Client) applyPresence(env *protocol.Envelope) {
	var p protocol.UserUpdatePayload
	if err := env.DecodePayload(&p); err != nil {
		return
	}
	c.mu.Lock()
	if p.User.ID == c.selfID {
		c.mu.Unlock()
		return
	}
	pubChanged := false
	if p.User.PubKey != "" && c.peerPubs[p.User.ID] != p.User.PubKey {
		c.peerPubs[p.User.ID] = p.User.PubKey
		pubChanged = true
	}
	c.peers[p.User.ID] = toPeer(p.User)
	peer := c.peers[p.User.ID]
	c.mu.Unlock()
	if pubChanged {
		c.persistKeyDirectory()
	}
	c.listener().OnPeerJoined(peer)
	c.distributePendingChannelKeys()
}

// currentSelfID 返回当前会话在信令服务器上的节点 ID（未连接时回退为 deviceID）。
func (c *Client) currentSelfID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.selfID != "" {
		return c.selfID
	}
	return c.deviceID
}

// scheduleAutoRoamPeers 异步延迟触发上线增量漫游，避开对端或本端握手期帧竞争（v7.1.0）。
func (c *Client) scheduleAutoRoamPeers(joined []appapi.Peer) {
	if len(joined) == 0 {
		return
	}
	peersCopy := append([]appapi.Peer(nil), joined...)
	go func() {
		select {
		case <-c.runCtx.Done():
			return
		case <-time.After(45 * time.Millisecond):
		}
		c.autoRoamWithJoinedPeers(peersCopy)
	}()
}

// canPeerAccessConv 校验指定对端是否有权漫游拉取目标会话的历史记录（保障 G2 频道成员边界安全，v7.1.0）。
func (c *Client) canPeerAccessConv(peerID, convID string) bool {
	if peerID == "" {
		return false
	}
	if convID == "" || convID == "*" || convID == peerID {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if convID == c.selfID {
		return true
	}
	ch, ok := c.channels[convID]
	if !ok {
		return false
	}
	return containsMember(ch.Members, c.selfID) && containsMember(ch.Members, peerID)
}

// autoRoamWithJoinedPeers 在节点上线或重连后，携带本地最新 TS 游标向对端静默发起增量历史补齐（v7.1.0）。
func (c *Client) autoRoamWithJoinedPeers(joined []appapi.Peer) {
	if len(joined) == 0 {
		return
	}
	if _, err := c.connectedConn(); err != nil {
		return
	}
	now := time.Now().UnixMilli()
	type chSyncTarget struct {
		peerID string
		chID   string
	}
	c.mu.Lock()
	selfID := c.selfID
	var targets []string
	var sharedChs []chSyncTarget
	for _, p := range joined {
		if p.ID == "" || p.ID == selfID {
			continue
		}
		key := "peer:" + p.ID
		if now-c.lastAutoRoam[key] < 1500 {
			continue
		}
		c.lastAutoRoam[key] = now
		targets = append(targets, p.ID)
		for _, ch := range c.channels {
			if containsMember(ch.Members, selfID) && containsMember(ch.Members, p.ID) {
				sharedChs = append(sharedChs, chSyncTarget{peerID: p.ID, chID: ch.ID})
			}
		}
	}
	c.mu.Unlock()

	c.broadcastKeyDirectoryToPeers(targets)
	for _, peerID := range targets {
		bcastSince := c.history.latestTimestampForConv("*")
		_ = c.sendControlText(peerID, "", "sys:sync:autoreq:*:"+strconv.FormatInt(bcastSince, 10))
	}
	for _, sc := range sharedChs {
		chSince := c.history.latestTimestampForConv(sc.chID)
		_ = c.sendControlText(sc.peerID, "", "sys:sync:autoreq:"+sc.chID+":"+strconv.FormatInt(chSince, 10))
	}
}

// autoRoamChannels 在频道成员变更或频道列表同步后，对本端已加入的频道向在线成员静默请求增量历史补齐（v7.1.0）。
func (c *Client) autoRoamChannels(channels []appapi.Channel) {
	if len(channels) == 0 {
		return
	}
	if _, err := c.connectedConn(); err != nil {
		return
	}
	now := time.Now().UnixMilli()
	type chSyncTarget struct {
		peerID string
		chID   string
	}
	c.mu.Lock()
	selfID := c.selfID
	var syncList []chSyncTarget
	for _, ch := range channels {
		if ch.ID == "" || !containsMember(ch.Members, selfID) {
			continue
		}
		for _, memberID := range ch.Members {
			if memberID == "" || memberID == selfID {
				continue
			}
			if _, online := c.peers[memberID]; !online {
				continue
			}
			key := "ch:" + ch.ID + ":" + memberID
			if now-c.lastAutoRoam[key] < 1500 {
				continue
			}
			c.lastAutoRoam[key] = now
			syncList = append(syncList, chSyncTarget{peerID: memberID, chID: ch.ID})
		}
	}
	c.mu.Unlock()

	for _, item := range syncList {
		sinceTS := c.history.latestTimestampForConv(item.chID)
		_ = c.sendControlText(item.peerID, "", "sys:sync:autoreq:"+item.chID+":"+strconv.FormatInt(sinceTS, 10))
	}
}
