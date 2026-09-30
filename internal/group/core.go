package group

import "github.com/pandaymx/lanchat/internal/protocol"

// BlockSource 是 swarm 取/存块的最小数据依赖，由外层（接线 core/transfer
// 的一侧）实现；测试中用内存实现替代真实文件。
type BlockSource interface {
	// BlockCount 返回文件的总块数。
	BlockCount() int
	// ReadBlock 返回第 idx 块的完整数据（最后一块可短于 BlockSize）。
	ReadBlock(idx int) ([]byte, error)
	// WriteBlock 落盘第 idx 块；idx 必须落在 [0, BlockCount)。
	WriteBlock(idx int, data []byte) error
}

// member 是一次群组任务中的单个成员状态。
type member struct {
	id      string
	have    *bitmap
	done    bool
	failed  bool
	seeder  bool // 是否可向他人供块（最初的源或已完成的成员）
	pending bool // 是否排入后续轮次、尚未开始取块
}

// MatrixEvent 是上抛给 UI 的成员矩阵快照（方案 §9.5）。
type MatrixEvent struct {
	GroupID    string
	TransferID string
	// HaveBitmap 为本机视角的块拥有位图（GROUP_PROGRESS 用）。
	HaveBitmap []byte
	// Done/Active/Failed 为成员计数。
	Done, Active, Failed int
	// Total 成员总数。
	Total int
}

// ChannelEventKind 频道事件类型。
type ChannelEventKind int

const (
	// ChannelChanged 频道列表或成员变化。
	ChannelChanged ChannelEventKind = iota + 1
	// ChannelMessage 收到一条仅频道成员可见的文本。
	ChannelMessage
)

// ChannelEvent 是频道层上抛给 UI 的事件。
type ChannelEvent struct {
	Kind     ChannelEventKind
	Channels []ChannelInfo
	// 以下字段仅 ChannelMessage 使用。
	GroupID string
	From    string
	MsgID   string
	Text    string
}

// EventSink 是本包向外层上抛事件的出口。
type EventSink interface {
	OnMatrix(MatrixEvent)
	OnChannel(ChannelEvent)
}

// ChannelInfo 是频道的对外只读视图（对齐 appapi.Channel 的值拷贝）。
type ChannelInfo struct {
	ID      string
	Name    string
	OwnerID string
	Private bool
	Members []string
}

// frameType 把块交换帧常量收敛为本地别名，避免散落裸数字。
var (
	frameBitfield = protocol.FrameBitfield
	frameRequest  = protocol.FrameRequest
	frameBlock    = protocol.FrameBlock
	frameHave     = protocol.FrameHave
)
