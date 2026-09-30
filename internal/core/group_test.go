package core

import (
	"testing"
	"time"

	"github.com/pandaymx/lanchat/internal/appapi"
)

// TestG1Broadcast G1 全体广播：发送者外的所有在线成员都应收到，自己不收。
func TestG1Broadcast(t *testing.T) {
	url := startServer(t)
	la := &recordingListener{}
	a := New(Options{Nickname: "alice", Listener: la})
	t.Cleanup(a.Close)
	lb := &recordingListener{}
	b := New(Options{Nickname: "bob", Listener: lb})
	t.Cleanup(b.Close)
	lc := &recordingListener{}
	c := New(Options{Nickname: "carol", Listener: lc})
	t.Cleanup(c.Close)

	for _, cl := range []struct {
		cl *Client
		l  *recordingListener
	}{
		{a, la}, {b, lb}, {c, lc},
	} {
		if err := cl.cl.Connect(url, testPSK); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := a.SendText("", "hello all", "*"); err != nil {
		t.Fatal(err)
	}

	waitFor(t, time.Second, func() bool { return len(lb.recvdSnapshot()) == 1 })
	waitFor(t, time.Second, func() bool { return len(lc.recvdSnapshot()) == 1 })

	for _, l := range []*recordingListener{lb, lc} {
		msgs := l.recvdSnapshot()
		m := msgs[0]
		if m.group != "*" || m.text != "hello all" || m.from != a.GetState().SelfID {
			t.Fatalf("广播消息路由错误: %+v", m)
		}
	}
	// 发送者自己不应收到自己的广播。
	if len(la.recvdSnapshot()) != 0 {
		t.Fatalf("发送者收到了自己的广播: %+v", la.recvdSnapshot())
	}
}

// TestG2ChannelMembersOnly G2 频道：消息仅成员可见，非成员发送的消息不下发。
func TestG2ChannelMembersOnly(t *testing.T) {
	url := startServer(t)
	la := &recordingListener{}
	a := New(Options{Nickname: "alice", Listener: la})
	t.Cleanup(a.Close)
	lb := &recordingListener{}
	b := New(Options{Nickname: "bob", Listener: lb})
	t.Cleanup(b.Close)
	lc := &recordingListener{}
	c := New(Options{Nickname: "carol", Listener: lc})
	t.Cleanup(c.Close)

	if err := a.Connect(url, testPSK); err != nil {
		t.Fatal(err)
	}
	if err := b.Connect(url, testPSK); err != nil {
		t.Fatal(err)
	}
	if err := c.Connect(url, testPSK); err != nil {
		t.Fatal(err)
	}

	// a 创建频道，ID 经 CHANNEL_LIST 下发。
	if _, err := a.ChannelCreate("dev"); err != nil {
		t.Fatal(err)
	}
	var channelID string
	waitFor(t, time.Second, func() bool {
		chs := la.channelSnapshot()
		if len(chs) == 1 {
			channelID = chs[0].ID
		}
		return channelID != ""
	})

	// b 加入；c 不加入。必须等到 b 真正出现在频道成员列表中
	// （a 创建时已有一次列表推送，仅看到频道不代表已加入）。
	bID := b.GetState().SelfID
	if err := b.ChannelJoin(channelID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool {
		return memberIn(lb.channelSnapshot(), channelID, bID)
	})

	// 主动拉取频道列表：c 应能看到该频道（列表对全体可见，成员资格另行判断）。
	waitFor(t, time.Second, func() bool {
		_ = c.ChannelList()
		for _, ch := range lc.channelSnapshot() {
			if ch.ID == channelID {
				return true
			}
		}
		return false
	})

	// a 在频道内发言：b 应收到，c 不应收到。发言前确认 a、b 均已在频道内。
	aID := a.GetState().SelfID
	waitFor(t, time.Second, func() bool {
		return memberIn(la.channelSnapshot(), channelID, aID) &&
			memberIn(lb.channelSnapshot(), channelID, bID)
	})
	if _, err := a.SendText("", "channel hi", channelID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool { return len(lb.recvdSnapshot()) >= 1 })
	if msgs := lb.recvdSnapshot(); msgs[0].group != channelID || msgs[0].text != "channel hi" {
		t.Fatalf("b 收到的频道消息错误: %+v", msgs[0])
	}

	// b 在频道内发言：a 应收到（验证成员可互发）。
	if _, err := b.SendText("", "from bob", channelID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool { return len(la.recvdSnapshot()) >= 1 })

	// 留出时间确认 c 不会误收，然后断言 c 全程零消息。
	time.Sleep(150 * time.Millisecond)
	if len(lc.recvdSnapshot()) != 0 {
		t.Fatalf("非成员 c 收到了频道消息: %+v", lc.recvdSnapshot())
	}

	// c 从未加入，其本地频道缓存中自己不是成员（Members 不含 c）。
	if memberIn(lc.channelSnapshot(), channelID, c.GetState().SelfID) {
		t.Fatal("非成员出现在频道成员列表中")
	}
}

// memberIn 判断频道列表中指定频道是否含某成员。
func memberIn(channels []appapi.Channel, channelID, memberID string) bool {
	for _, ch := range channels {
		if ch.ID != channelID {
			continue
		}
		for _, m := range ch.Members {
			if m == memberID {
				return true
			}
		}
	}
	return false
}

// TestChannelCreateValidation 频道名称校验。
func TestChannelCreateValidation(t *testing.T) {
	url := startServer(t)
	l := &recordingListener{}
	c := New(Options{Nickname: "alice", Listener: l})
	t.Cleanup(c.Close)
	if err := c.Connect(url, testPSK); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ChannelCreate("   "); err != errEmptyText {
		t.Fatalf("空名称应报 errEmptyText，得到 %v", err)
	}
}
