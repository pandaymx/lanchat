package group

// Msg 是群组块交换层在成员之间传递的一条消息。
//
// 外层接线时：To/From 映射到 P2P/中继对端，Frame 映射到 LCTP 帧类型，
// Payload 用 blocks.go 的编码；测试中的内存 mesh 直接传递本结构。
type Msg struct {
	From    string
	To      string
	Frame   byte
	Payload []byte
}
