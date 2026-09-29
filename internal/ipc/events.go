package ipc

import "github.com/pandaymx/lanchat/internal/appapi"

// eventBridge 实现 appapi.Listener：把 core 事件转换为 JSON-RPC
// notification 并广播给所有已连接 UI。notification 名与 params
// 字段对齐 api/ipc.schema.json notifications。
type eventBridge struct {
	srv *Server
}

var _ appapi.Listener = eventBridge{}

func (e eventBridge) OnConnChanged(state appapi.ConnState, reason string) {
	e.srv.broadcast("conn.changed", struct {
		State  appapi.ConnState `json:"state"`
		Reason string           `json:"reason"`
	}{State: state, Reason: reason})
}

func (e eventBridge) OnPeerJoined(peer appapi.Peer) {
	e.srv.broadcast("peer.joined", struct {
		Peer appapi.Peer `json:"peer"`
	}{Peer: peer})
}

func (e eventBridge) OnPeerLeft(peerID string) {
	e.srv.broadcast("peer.left", struct {
		PeerID string `json:"peerId"`
	}{PeerID: peerID})
}

func (e eventBridge) OnMessageReceived(from, group, msgID, typ, text string) {
	e.srv.broadcast("msg.received", struct {
		From  string `json:"from"`
		Group string `json:"group,omitempty"`
		MsgID string `json:"msgId"`
		Type  string `json:"type"`
		Text  string `json:"text"`
	}{From: from, Group: group, MsgID: msgID, Type: typ, Text: text})
}

func (e eventBridge) OnTransferProgress(t appapi.Transfer) {
	e.srv.broadcast("transfer.progress", struct {
		Transfer appapi.Transfer `json:"transfer"`
	}{Transfer: t})
}

func (e eventBridge) OnTransferDone(transferID string) {
	e.srv.broadcast("transfer.done", struct {
		TransferID string `json:"transferId"`
	}{TransferID: transferID})
}

func (e eventBridge) OnTransferFailed(transferID, reason string) {
	e.srv.broadcast("transfer.failed", struct {
		TransferID string `json:"transferId"`
		Reason     string `json:"reason"`
	}{TransferID: transferID, Reason: reason})
}

func (e eventBridge) OnGroupMatrix(groupID, transferID string, haveBitmap []byte) {
	e.srv.broadcast("group.matrix", struct {
		GroupID    string `json:"groupId"`
		TransferID string `json:"transferId"`
		HaveBitmap []byte `json:"haveBitmap"`
	}{GroupID: groupID, TransferID: transferID, HaveBitmap: haveBitmap})
}

func (e eventBridge) OnChannelUpdated(channels []appapi.Channel) {
	e.srv.broadcast("channel.updated", struct {
		Channels []appapi.Channel `json:"channels"`
	}{Channels: channels})
}
