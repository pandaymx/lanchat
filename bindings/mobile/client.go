package mobile

import (
	"encoding/json"

	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/core"
)

// Listener 是 core → 移动端 UI 的事件回调集合（对应 9 个 notification）。
//
// 回调来自 Go 后台 goroutine（收泵 / 传输 runner），宿主侧更新 UI
// 必须自行切回主线程（Android: runOnUiThread / Handler；iOS: DispatchQueue.main）。
//
// gomobile 对 interface 方法的参数类型支持有限：只接受基础类型、[]byte 与
// 单个结构体指针，结构体切片需序列化为 JSON []byte 由宿主自行反序列化。
type Listener interface {
	// OnConnChanged 连接状态变化。
	OnConnChanged(state string, reason string)
	// OnPeerJoined 新用户上线（指针类型：gomobile 不支持值结构体参数）。
	OnPeerJoined(peer *Peer)
	// OnPeerLeft 用户下线。
	OnPeerLeft(peerID string)

	// OnMessageReceived 收到文本 / 表情。
	OnMessageReceived(from string, group string, msgID string, typ string, text string)

	// OnTransferProgress 传输进度更新（指针类型，原因同 OnPeerJoined）。
	OnTransferProgress(t *Transfer)
	// OnTransferDone 传输完成（含校验通过）。
	OnTransferDone(transferID string)
	// OnTransferFailed 传输失败。
	OnTransferFailed(transferID string, reason string)

	// OnGroupMatrix 群组任务的块可用性矩阵更新（G1/G2）。
	OnGroupMatrix(groupID string, transferID string, haveBitmap []byte)
	// OnChannelUpdated 频道列表 / 成员变化。
	// payload 为 []appapi.Channel 的 JSON，宿主侧自行反序列化。
	OnChannelUpdated(payload []byte)
}

// Client 是暴露给移动端的客户端门面，内部持有唯一的 *core.Client。
type Client struct {
	core *core.Client
}

// NewClient 构造客户端并注册事件监听器。
//
// osName 由宿主传入平台标识（如 "android"）；downloadDir 为默认保存目录，
// 可留空后再通过 PickDownloadDir 设置；listener 不可为 nil。
func NewClient(nickname string, osName string, downloadDir string, listener Listener) *Client {
	c := core.New(core.Options{
		Nickname:    nickname,
		OS:          osName,
		DownloadDir: downloadDir,
	})
	c.SetListener(&listenerBridge{out: listener})
	return &Client{core: c}
}

// Close 释放核心资源（幂等）。应绑定到应用 / 前台服务生命周期。
func (c *Client) Close() { c.core.Close() }

// GetStateJSON 返回全量快照（appapi.State 的 JSON）：
// 连接态 + 自己 + 在线表 + 传输 + 频道。结构体切片无法直接跨绑定，故走 JSON。
func (c *Client) GetStateJSON() []byte { return mustJSON(c.core.GetState()) }

// Connect 手动连接指定中心节点。
func (c *Client) Connect(addr string, psk string) error { return c.core.Connect(addr, psk) }

// BrowseServersJSON 触发 mDNS 重扫并返回当前候选（[]appapi.Server 的 JSON）。
func (c *Client) BrowseServersJSON() []byte { return mustJSON(c.core.BrowseServers()) }

// SendText 发送文本；group 为空表示单播，其余为群组（M6 仅单播）。
func (c *Client) SendText(to string, text string, group string) (string, error) {
	return c.core.SendText(to, text, group)
}

// SendSticker 发送本地表情（path 为贴纸名称引用）。
func (c *Client) SendSticker(to string, path string) (string, error) {
	return c.core.SendSticker(to, path)
}

// OfferFile 发起 1:1 文件发送。
func (c *Client) OfferFile(to string, path string) (string, error) {
	return c.core.OfferFile(to, path)
}

// OfferFileToGroup 发起 1:N 发送（M9，当前返回 not implemented）。
func (c *Client) OfferFileToGroup(group string, path string) (string, error) {
	return c.core.OfferFileToGroup(group, path)
}

// RespondFile 应答收到的文件邀请；accept=true 时 dest 为保存路径。
func (c *Client) RespondFile(transferID string, accept bool, dest string) error {
	return c.core.RespondFile(transferID, accept, dest)
}

// PauseFile 暂停传输（当前底层可能返回 not supported）。
func (c *Client) PauseFile(transferID string) error { return c.core.PauseFile(transferID) }

// ResumeFile 恢复传输（当前底层可能返回 not supported）。
func (c *Client) ResumeFile(transferID string) error { return c.core.ResumeFile(transferID) }

// CancelFile 取消传输。
func (c *Client) CancelFile(transferID string) error { return c.core.CancelFile(transferID) }

// SetNickname 修改本机昵称。
func (c *Client) SetNickname(name string) error { return c.core.SetNickname(name) }

// PickDownloadDir 设置默认下载目录。
func (c *Client) PickDownloadDir(path string) error { return c.core.PickDownloadDir(path) }

// ChannelCreate 创建 G2 自定义频道；private 频道仅可经 ChannelInvite 加入。
func (c *Client) ChannelCreate(name, topic string, private bool) (string, error) {
	return c.core.ChannelCreate(name, topic, private)
}

// ChannelJoin 加入 G2 自定义频道（private 频道会被拒绝）。
func (c *Client) ChannelJoin(channelID string) error { return c.core.ChannelJoin(channelID) }

// ChannelInvite 邀请在线成员加入频道（仅 owner）。
func (c *Client) ChannelInvite(channelID, memberID string) error {
	return c.core.ChannelInvite(channelID, memberID)
}

// ChannelLeave 退出 G2 自定义频道。
func (c *Client) ChannelLeave(channelID string) error { return c.core.ChannelLeave(channelID) }

// ChannelListJSON 列出当前可见频道（[]appapi.Channel 的 JSON）。
func (c *Client) ChannelListJSON() []byte { return mustJSON(c.core.ChannelList()) }

// mustJSON 序列化；这些 DTO 只含基础类型，序列化不会失败，失败时返回 nil
// 由宿主按空列表处理。
func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return data
}

// listenerBridge 把 appapi.Listener 事件转成 mobile.Listener（DTO 值拷贝）。
type listenerBridge struct {
	out Listener
}

var _ appapi.Listener = (*listenerBridge)(nil)

func (b *listenerBridge) OnConnChanged(state appapi.ConnState, reason string) {
	b.out.OnConnChanged(string(state), reason)
}

func (b *listenerBridge) OnPeerJoined(peer appapi.Peer) {
	p := Peer{ID: peer.ID, Nickname: peer.Nickname, OS: peer.OS, Status: peer.Status}
	b.out.OnPeerJoined(&p)
}

func (b *listenerBridge) OnPeerLeft(peerID string) { b.out.OnPeerLeft(peerID) }

func (b *listenerBridge) OnMessageReceived(from, group, msgID, typ, text string) {
	b.out.OnMessageReceived(from, group, msgID, typ, text)
}

func (b *listenerBridge) OnTransferProgress(t appapi.Transfer) {
	tr := Transfer{
		ID:          t.ID,
		Direction:   string(t.Direction),
		State:       string(t.State),
		Kind:        string(t.Kind),
		PeerID:      t.PeerID,
		GroupID:     t.GroupID,
		Name:        t.Name,
		Size:        t.Size,
		BytesDone:   t.BytesDone,
		SpeedBps:    t.SpeedBps,
		ViaRelay:    t.ViaRelay,
		ErrorReason: t.ErrorReason,
	}
	b.out.OnTransferProgress(&tr)
}

func (b *listenerBridge) OnTransferDone(transferID string) { b.out.OnTransferDone(transferID) }

func (b *listenerBridge) OnTransferFailed(transferID, reason string) {
	b.out.OnTransferFailed(transferID, reason)
}

func (b *listenerBridge) OnGroupMatrix(groupID, transferID string, haveBitmap []byte) {
	b.out.OnGroupMatrix(groupID, transferID, haveBitmap)
}

func (b *listenerBridge) OnChannelUpdated(channels []appapi.Channel) {
	b.out.OnChannelUpdated(mustJSON(channels))
}
