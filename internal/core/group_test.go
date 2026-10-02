package core

import (
	"crypto/rand"
	"os"
	"path/filepath"
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
	if _, err := a.ChannelCreate("dev", "", false); err != nil {
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

// TestG2PrivateInviteAndLeave private 频道：直接加入被拒，owner 邀请后成为成员，
// 成员退出后频道消息不再送达。
func TestG2PrivateInviteAndLeave(t *testing.T) {
	url := startServer(t)
	la := &recordingListener{}
	a := New(Options{Nickname: "alice", Listener: la})
	t.Cleanup(a.Close)
	lb := &recordingListener{}
	b := New(Options{Nickname: "bob", Listener: lb})
	t.Cleanup(b.Close)

	if err := a.Connect(url, testPSK); err != nil {
		t.Fatal(err)
	}
	if err := b.Connect(url, testPSK); err != nil {
		t.Fatal(err)
	}

	// 创建 private 频道，带 topic。
	if _, err := a.ChannelCreate("私密", "内部讨论", true); err != nil {
		t.Fatal(err)
	}
	var channelID string
	waitFor(t, time.Second, func() bool {
		for _, ch := range la.channelSnapshot() {
			if ch.Name == "私密" {
				channelID = ch.ID
				return ch.Private && ch.Topic == "内部讨论"
			}
		}
		return false
	})

	bID := b.GetState().SelfID

	// b 直接加入 private 频道：服务器拒绝，b 不应进入成员列表。
	if err := b.ChannelJoin(channelID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if memberIn(lb.channelSnapshot(), channelID, bID) {
		t.Fatal("未受邀成员不应出现在 private 频道中")
	}

	// owner a 邀请 b。
	if err := a.ChannelInvite(channelID, bID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool {
		return memberIn(lb.channelSnapshot(), channelID, bID)
	})

	// b 现在能收到频道消息。
	if _, err := a.SendText("", "private hi", channelID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool { return len(lb.recvdSnapshot()) >= 1 })

	// b 退出后不再收到频道消息。
	if err := b.ChannelLeave(channelID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool {
		return !memberIn(lb.channelSnapshot(), channelID, bID)
	})
	if _, err := a.SendText("", "after leave", channelID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if msgs := lb.recvdSnapshot(); len(msgs) > 1 {
		t.Fatalf("退出后 b 不应再收到消息: %+v", msgs)
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
	if _, err := c.ChannelCreate("   ", "", false); err != errEmptyText {
		t.Fatalf("空名称应报 errEmptyText，得到 %v", err)
	}
}

// swarmNode 是群组文件测试中的一个节点：核心、回调与落盘目录。
type swarmNode struct {
	cl  *Client
	l   *recordingListener
	dir string
}

func newSwarmNode(t *testing.T, nick string) swarmNode {
	t.Helper()
	l := &recordingListener{}
	cl := New(Options{Nickname: nick, Listener: l})
	t.Cleanup(cl.Close)
	return swarmNode{cl: cl, l: l, dir: t.TempDir()}
}

func connectSwarmNodes(t *testing.T, url string, nodes ...swarmNode) {
	t.Helper()
	for _, n := range nodes {
		if err := n.cl.Connect(url, testPSK); err != nil {
			t.Fatal(err)
		}
	}
}

// waitForGroupTask 等待该节点出现群组文件任务（GROUP_OFFER 为异步投递）。
func (n swarmNode) waitForGroupTask(t *testing.T) appapi.Transfer {
	t.Helper()
	var found appapi.Transfer
	waitFor(t, 2*time.Second, func() bool {
		for _, tr := range n.cl.GetState().Transfers {
			if tr.Kind == appapi.PathSwarm || tr.Kind == appapi.PathChannel {
				found = tr
				return true
			}
		}
		return false
	})
	return found
}

// writeRandomFile 写入 size 字节随机内容并返回路径与内容。
func writeRandomFile(t *testing.T, dir, name string, size int) (string, []byte) {
	t.Helper()
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, data
}

const swarmTestSize = 3 * swarmChunk // 3 块：覆盖多块请求与成员间供块。

// swarmWaitTimeout 是群文件传输完成的等待上限：-race 全量并发负载下加密
// 数据面 + 12 MiB 落盘偶发较慢，放宽以避免环境性 flaky。
const swarmWaitTimeout = 30 * time.Second

// swarmChunk 是测试用“块大小”。真实 BlockSize 为 4 MiB，测试以整倍数控制块数，
// 并在读完后比对原始内容。
const swarmChunk = 4 << 20

// TestGroupFileSwarm G1 群组文件 1:N：源向全体发送，两名接收者均收齐，
// 且已完成的接收者可向后来的成员供块（swarm 扩散）。
func TestGroupFileSwarm(t *testing.T) {
	url := startServer(t)
	src := newSwarmNode(t, "alice")
	r1 := newSwarmNode(t, "bob")
	r2 := newSwarmNode(t, "carol")
	connectSwarmNodes(t, url, src, r1, r2)

	srcPath, data := writeRandomFile(t, src.dir, "big.bin", swarmTestSize)
	tid, err := src.cl.OfferFileToGroup("*", srcPath)
	if err != nil {
		t.Fatal(err)
	}

	// 两名接收者都收到 GROUP_OFFER 并接受。
	for _, n := range []swarmNode{r1, r2} {
		got := n.waitForGroupTask(t)
		if got.ID != tid {
			t.Fatalf("接收任务 ID 不匹配: got %s want %s", got.ID, tid)
		}
		dest := filepath.Join(n.dir, "big.bin")
		if err := n.cl.RespondFile(tid, true, dest); err != nil {
			t.Fatal(err)
		}
	}

	// 三方均完成：源的 OnTransferDone（全部成员收齐）+ 两名接收者各自完成。
	waitFor(t, swarmWaitTimeout, func() bool {
		_, _, done, _ := src.l.snapshot()
		return len(done) == 1
	})
	for _, n := range []swarmNode{r1, r2} {
		waitFor(t, swarmWaitTimeout, func() bool {
			_, _, done, _ := n.l.snapshot()
			return len(done) == 1
		})
	}

	// 落盘内容必须与源逐字节一致。
	for _, n := range []swarmNode{r1, r2} {
		got, err := os.ReadFile(filepath.Join(n.dir, "big.bin"))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(data) {
			t.Fatalf("接收内容与源不一致 (len got=%d want=%d)", len(got), len(data))
		}
	}

	// 全部任务行应已清理。
	for _, n := range []swarmNode{src, r1, r2} {
		if len(n.cl.GetState().Transfers) != 0 {
			t.Fatalf("完成后仍残留任务: %+v", n.cl.GetState().Transfers)
		}
	}
}

// TestGroupFileN1 N=1 退化：群组里只有一个接收者时等同单播，仍能收齐。
func TestGroupFileN1(t *testing.T) {
	url := startServer(t)
	src := newSwarmNode(t, "alice")
	r := newSwarmNode(t, "bob")
	connectSwarmNodes(t, url, src, r)

	srcPath, data := writeRandomFile(t, src.dir, "one.bin", swarmTestSize)
	tid, err := src.cl.OfferFileToGroup("*", srcPath)
	if err != nil {
		t.Fatal(err)
	}

	got := r.waitForGroupTask(t)
	if got.ID != tid {
		t.Fatalf("接收任务 ID 不匹配: got %s want %s", got.ID, tid)
	}
	dest := filepath.Join(r.dir, "one.bin")
	if err := r.cl.RespondFile(tid, true, dest); err != nil {
		t.Fatal(err)
	}

	waitFor(t, swarmWaitTimeout, func() bool {
		_, _, done, _ := src.l.snapshot()
		return len(done) == 1
	})
	waitFor(t, swarmWaitTimeout, func() bool {
		_, _, done, _ := r.l.snapshot()
		return len(done) == 1
	})

	rcv, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(rcv) != string(data) {
		t.Fatal("N=1 接收内容与源不一致")
	}
}

// TestGroupFileRelayFallback 直连失败时回退中继：接收方在接受前把源候选
// 替换为不可达地址，迫使裁决拨号方直连失败 → RELAY_REQUEST/GRANT → 中继
// 数据面完成。验证落盘内容一致且任务进度曾带 ViaRelay=true。
func TestGroupFileRelayFallback(t *testing.T) {
	url := startServerWithRelay(t)
	src := newSwarmNode(t, "alice")
	rcv := newSwarmNode(t, "bob")
	// 双方都禁止直连：单条 TCP 双向承载帧，必须让「外拨」与「入站」两个
	// 方向同时失败，才能确定性迫使传输经中继配对完成。
	src.cl.forceRelay = true
	rcv.cl.forceRelay = true
	connectSwarmNodes(t, url, src, rcv)

	srcPath, data := writeRandomFile(t, src.dir, "relay.bin", swarmTestSize)
	tid, err := src.cl.OfferFileToGroup("*", srcPath)
	if err != nil {
		t.Fatal(err)
	}

	got := rcv.waitForGroupTask(t)
	if got.ID != tid {
		t.Fatalf("接收任务 ID 不匹配: got %s want %s", got.ID, tid)
	}

	dest := filepath.Join(rcv.dir, "relay.bin")
	if err := rcv.cl.RespondFile(tid, true, dest); err != nil {
		t.Fatal(err)
	}

	waitFor(t, 20*time.Second, func() bool {
		_, _, done, _ := rcv.l.snapshot()
		return len(done) == 1
	})

	out, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(data) {
		t.Fatal("中继回退后落盘内容与源不一致")
	}

	// 进度流中应出现过 ViaRelay=true（⚠ 中继徽章）。
	relayed := false
	for _, p := range rcv.l.progressSnapshot() {
		if p.ID == tid && p.ViaRelay {
			relayed = true
		}
	}
	if !relayed {
		t.Fatal("接收方进度未出现 ViaRelay=true，回退可能未发生")
	}
}

// TestGroupFileReject 接收方拒绝后任务被清理，且不影响源继续分发。
func TestGroupFileReject(t *testing.T) {
	url := startServer(t)
	src := newSwarmNode(t, "alice")
	r := newSwarmNode(t, "bob")
	connectSwarmNodes(t, url, src, r)

	srcPath, _ := writeRandomFile(t, src.dir, "r.bin", swarmTestSize)
	tid, err := src.cl.OfferFileToGroup("*", srcPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.waitForGroupTask(t); got.ID != tid {
		t.Fatalf("接收任务 ID 不匹配: got %s want %s", got.ID, tid)
	}
	if err := r.cl.RespondFile(tid, false, ""); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool {
		return len(r.cl.GetState().Transfers) == 0
	})
}
