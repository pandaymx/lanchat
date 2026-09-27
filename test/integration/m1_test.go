package integration

import (
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/pandaymx/lanchat/internal/protocol"
	"github.com/pandaymx/lanchat/internal/server"
)

// TestThreePeersSeeEachOther 验证三客户端连接后在线表互见、
// 存量客户端收到 USER_JOIN，且最终 revision 一致。
func TestThreePeersSeeEachOther(t *testing.T) {
	url := startTestServer(t)

	a := dialClient(t, url, testPSK, "Alice")
	// A 是首个用户，其 USER_LIST 只有自己。
	aList := userIDs(t, a.userList)
	if len(aList) != 1 {
		t.Fatalf("A 初始在线数 = %d，期望 1（仅自己）", len(aList))
	}

	b := dialClient(t, url, testPSK, "Bob")
	// B 的 USER_LIST 含 A 与自己。
	bUsers := userIDs(t, b.userList)
	if len(bUsers) != 2 || bUsers[a.id].Nickname != "Alice" {
		t.Fatalf("B 在线表 = %v，期望含 A 与自己", bUsers)
	}
	// A 收到 B 的 USER_JOIN。
	a.recvType(protocol.UserJoin)

	c := dialClient(t, url, testPSK, "Carol")
	// C 的 USER_LIST 含 A、B 与自己。
	cUsers := userIDs(t, c.userList)
	if len(cUsers) != 3 {
		t.Fatalf("C 在线数 = %d，期望 3", len(cUsers))
	}

	// A、B 各收到 C 的 USER_JOIN（A 此前已读完 B 的 join）。
	for _, peer := range []*testClient{a, b} {
		env := peer.recvType(protocol.UserJoin)
		var up protocol.UserUpdatePayload
		if err := env.DecodePayload(&up); err != nil {
			t.Fatal(err)
		}
		if up.User.ID != c.id {
			t.Fatalf("USER_JOIN 用户 = %s，期望 %s", up.User.ID, c.id)
		}
	}

	// 三方最终 revision 一致：再请求一次全量表对账。
	for _, peer := range []*testClient{a, b, c} {
		peer.sendTo(protocol.UserListReq, "", nil)
		list := peer.recvType(protocol.UserList)
		users := userIDs(t, list)
		var p protocol.UserListPayload
		_ = list.DecodePayload(&p)
		if p.Revision != 3 {
			t.Errorf("最终 revision = %d，期望 3", p.Revision)
		}
		if len(users) != 3 {
			t.Errorf("最终在线数 = %d，期望 3", len(users))
		}
	}
}

// TestWrongPSKRejected 验证错误口令被 AUTH_FAIL 拒绝，且不入在线表。
func TestWrongPSKRejected(t *testing.T) {
	url := startTestServer(t)

	ctx, cancel := contextWithTimeout(readTimeout)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	hello, _ := protocol.NewEnvelope(uuid.NewString(), protocol.Hello, protocol.HelloPayload{
		Nickname: "BadGuy", DeviceID: uuid.NewString(), PSK: "wrong", OS: "test",
	})
	data, _ := hello.Marshal()
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
	_, rdata, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("读取 AUTH_FAIL: %v", err)
	}
	env, err := protocol.UnmarshalEnvelope(rdata)
	if err != nil {
		t.Fatal(err)
	}
	if env.Type != protocol.AuthFail {
		t.Fatalf("收到 %s，期望 AUTH_FAIL", env.Type)
	}
	var af protocol.AuthFailPayload
	if err := env.DecodePayload(&af); err != nil {
		t.Fatal(err)
	}
	if !af.Retryable || af.Reason != "bad_psk" {
		t.Fatalf("AUTH_FAIL = %+v，期望 bad_psk/Retryable", af)
	}
	// 随后连接应被关闭。
	_ = conn.CloseRead(ctx)

	good := dialClient(t, url, testPSK, "GoodGuy")
	users := userIDs(t, good.userList)
	if len(users) != 1 {
		t.Fatalf("正确客户端在线表 = %v，不应含被拒者", users)
	}
}

// TestTextRouting 验证文本中转、From 不可伪造、MSG_ACK、离线与超长处理。
func TestTextRouting(t *testing.T) {
	url := startTestServer(t)
	a := dialClient(t, url, testPSK, "Alice")
	b := dialClient(t, url, testPSK, "Bob")
	a.recvType(protocol.UserJoin) // 读掉 B 的 USER_JOIN

	// 客户端试图伪造 From，服务器应强制覆盖。
	env, _ := protocol.NewEnvelope(uuid.NewString(), protocol.TextMsg, protocol.TextPayload{
		Text: "你好，Bob",
	})
	env.To = b.id
	env.From = "fake-spoofer"
	a.send(env)

	got := b.recvType(protocol.TextMsg)
	if got.From != a.id {
		t.Fatalf("B 收到 from=%q，期望 %s", got.From, a.id)
	}
	var tp protocol.TextPayload
	if err := got.DecodePayload(&tp); err != nil || tp.Text != "你好，Bob" {
		t.Fatalf("文本负载 = %+v，期望 '你好，Bob'", tp)
	}

	ack := a.recvType(protocol.MsgAck)
	if ack.ReplyTo != env.ID {
		t.Fatalf("ACK replyTo %s，期望 %s", ack.ReplyTo, env.ID)
	}
	var ap protocol.MsgAckPayload
	_ = ack.DecodePayload(&ap)
	if ap.Status != "delivered" {
		t.Fatalf("ACK status = %s，期望 delivered", ap.Status)
	}

	// 发给不存在的 To：undeliverable。
	missing := a.sendTo(protocol.TextMsg, "nonexistent", protocol.TextPayload{Text: "hi"})
	ack2 := a.recvType(protocol.MsgAck)
	var ap2 protocol.MsgAckPayload
	_ = ack2.DecodePayload(&ap2)
	if ack2.ReplyTo != missing.ID || ap2.Status != "undeliverable" {
		t.Fatalf("离线 ACK = %+v replyTo=%s", ap2, ack2.ReplyTo)
	}

	// 超 4 KiB：ERROR。
	a.sendTo(protocol.TextMsg, b.id, protocol.TextPayload{Text: strings.Repeat("x", 4097)})
	a.recvType(protocol.Error)
}

// TestStickerRouting 验证内联表情中转。
func TestStickerRouting(t *testing.T) {
	url := startTestServer(t)
	a := dialClient(t, url, testPSK, "Alice")
	b := dialClient(t, url, testPSK, "Bob")
	a.recvType(protocol.UserJoin)

	sticker := protocol.StickerPayload{
		Name: "smile.png",
		Mime: "image/png",
		B64:  strings.Repeat("A", 1024),
		Hash: "deadbeef",
	}
	a.sendTo(protocol.StickerMsg, b.id, sticker)

	got := b.recvType(protocol.StickerMsg)
	if got.From != a.id {
		t.Fatalf("B 收到 from %q，期望 %s", got.From, a.id)
	}
	var sp protocol.StickerPayload
	if err := got.DecodePayload(&sp); err != nil {
		t.Fatal(err)
	}
	if sp.Name != sticker.Name || sp.B64 != sticker.B64 || sp.Hash != sticker.Hash {
		t.Fatalf("表情负载不匹配: %+v", sp)
	}

	a.recvType(protocol.MsgAck)
}

// TestHeartbeatAck 验证心跳应答带 revision 与 serverTime。
func TestHeartbeatAck(t *testing.T) {
	url := startTestServer(t)
	a := dialClient(t, url, testPSK, "Alice")

	a.sendTo(protocol.Heartbeat, "", nil)
	ack := a.recvType(protocol.HeartbeatAck)
	var hp protocol.HeartbeatPayload
	if err := ack.DecodePayload(&hp); err != nil {
		t.Fatal(err)
	}
	if hp.ServerTime == 0 {
		t.Fatal("ServerTime 不应为 0")
	}
	if hp.Revision != 1 {
		t.Fatalf("Revision = %d，期望 1", hp.Revision)
	}
	if time.Now().UnixMilli() < hp.ServerTime-1000 {
		t.Fatalf("ServerTime = %d 不合理", hp.ServerTime)
	}
}

// TestIdleTimeoutDisconnects 验证空闲超时断连并广播 USER_LEAVE。
func TestIdleTimeoutDisconnects(t *testing.T) {
	url := startTestServerOpts(t, func(o *server.Options) {
		o.IdleTimeout = 400 * time.Millisecond
	})
	a := dialClient(t, url, testPSK, "Alice")
	// 拨号后立即保活，避免在激进超时下与 B 一起被判空闲。
	stopHB := a.startHeartbeats(150 * time.Millisecond)
	defer stopHB()

	b := dialClient(t, url, testPSK, "Bob")
	// join 前可能已夹心跳 ACK，用 recvUntil 跳过。
	a.recvUntil(protocol.UserJoin, 3*time.Second)

	// B 不发心跳；collector 因连接关闭而退出。
	b.waitForClose(3 * time.Second)

	// A 收到 B 的 USER_LEAVE（期间可能夹杂 HEARTBEAT_ACK，跳过）。
	leave := a.recvUntil(protocol.UserLeave, 3*time.Second)
	var up protocol.UserUpdatePayload
	if err := leave.DecodePayload(&up); err != nil {
		t.Fatal(err)
	}
	if up.User.ID != b.id {
		t.Fatalf("USER_LEAVE 用户 = %s，期望 %s", up.User.ID, b.id)
	}

	// 最终在线表只剩 A：停心跳后请求全量表对账。
	stopHB()
	a.sendTo(protocol.UserListReq, "", nil)
	users := userIDs(t, a.recvUntil(protocol.UserList, 3*time.Second))
	if len(users) != 1 {
		t.Fatalf("最终在线数 = %d，期望 1", len(users))
	}
}

// TestSameDeviceReplacesOldSession 验证同 deviceID 重连顶掉旧会话，
// revision 不抖动、在线用户不重复。
func TestSameDeviceReplacesOldSession(t *testing.T) {
	url := startTestServer(t)
	deviceID := uuid.NewString()

	a := dialClientWith(t, url, testPSK, "Alice", deviceID)
	b := dialClient(t, url, testPSK, "Bob")
	a.recvType(protocol.UserJoin)

	// 同 deviceID 再次连接。
	dialClientWith(t, url, testPSK, "Alice-New", deviceID)

	// 旧连接 A 应被关闭。
	a.waitForClose(2 * time.Second)

	// B 不应收到额外 USER_JOIN（替换不增加 revision）。
	b.noMsg(300 * time.Millisecond)

	// 最终全量表：deviceID 只出现一次，且昵称为新值。
	b.sendTo(protocol.UserListReq, "", nil)
	list := b.recvType(protocol.UserList)
	var p protocol.UserListPayload
	if err := list.DecodePayload(&p); err != nil {
		t.Fatal(err)
	}
	if p.Revision != 2 {
		t.Fatalf("最终 revision = %d，期望 2（A join + B join，替换不计）", p.Revision)
	}
	countA := 0
	for _, u := range p.Users {
		if u.ID == deviceID {
			countA++
			if u.Nickname != "Alice-New" {
				t.Fatalf("替换后昵称 = %q，期望 Alice-New", u.Nickname)
			}
		}
	}
	if countA != 1 {
		t.Fatalf("deviceID 出现 %d 次，期望恰好 1", countA)
	}
}
