// mesh_integration_test.go 验证两个嵌入式 hub 通过真实 HTTP 端点
// 互相同步（ADR-014 wire v2，传输加密）：A 发消息落库（盖上 node-a），
// B 从 A 拉增量后消息一致；反向同理。同步通道全程 Envelope 加密。
package hubserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/pkg/mesh"
	"github.com/pandaymx/lanchat/pkg/protocol"
	"github.com/pandaymx/lanchat/pkg/store/libsql"
	wstransport "github.com/pandaymx/lanchat/pkg/transport/ws"
)

// freePort 分配一个空闲端口（测试用：监听 :0 拿端口后立即释放，
// TOCTOU 窗口在 CI 环境可接受）。
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// startMeshHub 起一个启用 mesh 的嵌入式 hub（libsql 临时库、无 mDNS、
// 固定空闲端口、独立身份密钥），返回 Server 与 base URL。
func startMeshHub(t *testing.T, nodeID string, peers []string) (*Server, string) {
	t.Helper()
	return startMeshHubOn(t, nodeID, peers, freePort(t))
}

// startMeshHubOn 与 startMeshHub 相同，但端口由调用方指定——双向互指
// 测试需要在启动前确定对端 URL。
func startMeshHubOn(t *testing.T, nodeID string, peers []string, port int) (*Server, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	dataDir := t.TempDir()
	srv, err := Start(ctx, Config{
		Addr:      "127.0.0.1:" + strconv.Itoa(port),
		DataDir:   dataDir,
		DBPath:    filepath.Join(dataDir, "mesh.db"),
		MDNS:      false,
		Mesh:      true,
		MeshPeers: peers,
		NodeID:    nodeID,
	})
	if err != nil {
		cancel()
		t.Fatalf("start mesh hub %s: %v", nodeID, err)
	}
	// LIFO：cancel 后注册 → 先执行 cancel（meshLoop/run 靠 ctx 退出）。
	t.Cleanup(cancel)
	t.Cleanup(func() { _ = srv.Close() })
	return srv, "http://" + srv.Addr()
}

// TestMeshTwoHubsSync：A/B 互指为邻居，各自发消息后收敛一致
// （后台 meshLoop 走加密通道）。
func TestMeshTwoHubsSync(t *testing.T) {
	ctx := context.Background()

	srvA, urlA := startMeshHub(t, "node-a", nil)
	srvB, _ := startMeshHub(t, "node-b", []string{urlA})
	_ = srvA // B 单向拉 A 即可验证（meshLoop 启动即同步一轮）

	// A 本地落一条消息（经 store 直写，模拟客户端发消息）。
	storeA, ok := srvA.store.(*libsql.Store)
	if !ok {
		t.Fatalf("store type %T, want *libsql.Store", srvA.store)
	}
	_, err := storeA.AppendMessage(ctx, protocol.StoredMessage{
		ID:             "m-a-1",
		ConversationID: "conv-1",
		SenderUserID:   "alice",
		SenderDeviceID: "dev-a",
		Body:           "mesh hello",
		ServerSeq:      1,
		CreatedAt:      time.Now().UnixMilli(),
		NodeID:         "node-a",
	})
	if err != nil {
		t.Fatalf("A append: %v", err)
	}

	// B 等 meshLoop 拉到（轮询最多 10s）。
	storeB, _ := srvB.store.(*libsql.Store)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		msgs, err := storeB.SyncMessages(ctx, "node-a", 0, 0)
		if err != nil {
			t.Fatalf("B sync query: %v", err)
		}
		if len(msgs) == 1 {
			if msgs[0].Body != "mesh hello" || msgs[0].ConversationID != "conv-1" {
				t.Fatalf("B got wrong message: %+v", msgs[0])
			}
			return // 收敛成功
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("B did not converge with A within 10s")
}

// TestMeshEndpointDirect：不经后台循环，客户端侧直连加密端点
// 一轮同步（TOFU 首连取公钥 → 加密请求 → 解密响应）。
func TestMeshEndpointDirect(t *testing.T) {
	ctx := context.Background()
	srvA, urlA := startMeshHub(t, "node-a", nil)
	srvB, _ := startMeshHub(t, "node-b", nil)

	storeA, _ := srvA.store.(*libsql.Store)
	_, err := storeA.AppendMessage(ctx, protocol.StoredMessage{
		ID:             "m-a-2",
		ConversationID: "conv-9",
		SenderUserID:   "alice",
		Body:           "direct sync",
		ServerSeq:      1,
		CreatedAt:      time.Now().UnixMilli(),
		NodeID:         "node-a",
	})
	if err != nil {
		t.Fatal(err)
	}

	// 客户端独立身份 + TOFU 表（走完整加密流程）。
	id, err := mesh.LoadOrCreateIdentity(filepath.Join(t.TempDir(), "cid.bin"))
	if err != nil {
		t.Fatal(err)
	}
	known, err := mesh.LoadKnownKeys(filepath.Join(t.TempDir(), "cknown.json"))
	if err != nil {
		t.Fatal(err)
	}

	storeB, _ := srvB.store.(*libsql.Store)
	resps, err := mesh.SyncPeer(ctx, urlA, id, known, protocol.SyncRequest{Limit: mesh.DefaultLimit})
	if err != nil {
		t.Fatalf("SyncPeer: %v", err)
	}
	n, err := mesh.Apply(ctx, storeB, resps)
	if err != nil || n != 1 {
		t.Fatalf("Apply: n=%d err=%v", n, err)
	}
	msgs, err := storeB.SyncMessages(ctx, "node-a", 0, 0)
	if err != nil || len(msgs) != 1 || msgs[0].Body != "direct sync" {
		t.Fatalf("B after direct sync: %+v err=%v", msgs, err)
	}

	// 第二轮（同 TOFU 表，公钥已信任；游标已推进到 1）：空增量。
	resps2, err := mesh.SyncPeer(ctx, urlA, id, known,
		protocol.SyncRequest{Cursor: map[string]uint64{"node-a": 1}, Limit: mesh.DefaultLimit})
	if err != nil {
		t.Fatalf("second SyncPeer: %v", err)
	}
	if n2, _ := mesh.Apply(ctx, storeB, resps2); n2 != 0 {
		t.Fatalf("second pull got %d messages, want 0", n2)
	}
}

// TestMeshTOFUReject：客户端 TOFU 表里已有不同公钥 → 拒绝。
func TestMeshTOFUReject(t *testing.T) {
	ctx := context.Background()
	srvA, urlA := startMeshHub(t, "node-a", nil)

	// 客户端先 TOFU 记一个假公钥，再同步 → 必须被拒。
	id, _ := mesh.LoadOrCreateIdentity(filepath.Join(t.TempDir(), "cid.bin"))
	known, _ := mesh.LoadKnownKeys(filepath.Join(t.TempDir(), "cknown.json"))
	fake, _ := mesh.LoadOrCreateIdentity(filepath.Join(t.TempDir(), "fake.bin"))
	if err := known.Trust(urlA, fake.PublicKey()); err != nil {
		t.Fatal(err)
	}

	_, err := mesh.SyncPeer(ctx, urlA, id, known, protocol.SyncRequest{Limit: mesh.DefaultLimit})
	if err == nil {
		t.Fatal("SyncPeer with mismatched TOFU key succeeded, want error")
	}
	_ = srvA
}

// storeLen 返回 store 里全部消息条数（跨源节点）。
func storeLen(ctx context.Context, s *libsql.Store) (int, error) {
	cursor, err := s.SourceCursor(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for node := range cursor {
		msgs, err := s.SyncMessages(ctx, node, 0, 0)
		if err != nil {
			return 0, err
		}
		total += len(msgs)
	}
	return total, nil
}

// TestMeshBidirectionalConverge：A/B 互指为邻居，各自离线期间写本地
// 消息，上线后经后台 meshLoop 双向收敛，互不丢帧、不重复（M-b 验收）。
func TestMeshBidirectionalConverge(t *testing.T) {
	ctx := context.Background()
	portA, portB := freePort(t), freePort(t)
	urlA := "http://127.0.0.1:" + strconv.Itoa(portA)
	urlB := "http://127.0.0.1:" + strconv.Itoa(portB)
	srvA, _ := startMeshHubOn(t, "node-a", []string{urlB}, portA)
	srvB, _ := startMeshHubOn(t, "node-b", []string{urlA}, portB)

	storeA, ok := srvA.store.(*libsql.Store)
	if !ok {
		t.Fatalf("store type %T, want *libsql.Store", srvA.store)
	}
	storeB, ok := srvB.store.(*libsql.Store)
	if !ok {
		t.Fatalf("store type %T, want *libsql.Store", srvB.store)
	}

	now := time.Now().UnixMilli()
	for i, body := range []string{"a offline 1", "a offline 2"} {
		if _, err := storeA.AppendMessage(ctx, protocol.StoredMessage{
			ID: fmt.Sprintf("a-%d", i+1), ConversationID: "conv-1",
			SenderUserID: "alice", SenderDeviceID: "dev-a",
			Body: body, ServerSeq: uint64(i + 1), CreatedAt: now + int64(i), NodeID: "node-a",
		}); err != nil {
			t.Fatalf("A append %d: %v", i, err)
		}
	}
	if _, err := storeB.AppendMessage(ctx, protocol.StoredMessage{
		ID: "b-1", ConversationID: "conv-1",
		SenderUserID: "bob", SenderDeviceID: "dev-b",
		Body: "b offline 1", ServerSeq: 1, CreatedAt: now, NodeID: "node-b",
	}); err != nil {
		t.Fatalf("B append: %v", err)
	}

	// 等双方都收敛到 3 条（各自 2 条 + 对方 1 条，无重复）。
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		na, errA := storeLen(ctx, storeA)
		nb, errB := storeLen(ctx, storeB)
		if errA == nil && errB == nil && na == 3 && nb == 3 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	na, _ := storeLen(ctx, storeA)
	nb, _ := storeLen(ctx, storeB)
	if na != 3 {
		t.Fatalf("node-a has %d messages, want 3", na)
	}
	if nb != 3 {
		t.Fatalf("node-b has %d messages, want 3", nb)
	}

	// 内容集合一致：A 有 b-offline-1，B 有两条 a-offline。
	bodies := map[string]map[string]bool{}
	for node, s := range map[string]*libsql.Store{"a": storeA, "b": storeB} {
		bodies[node] = map[string]bool{}
		cursor, _ := s.SourceCursor(ctx)
		for src := range cursor {
			msgs, _ := s.SyncMessages(ctx, src, 0, 0)
			for _, m := range msgs {
				bodies[node][m.Body] = true
			}
		}
	}
	for _, want := range []string{"a offline 1", "a offline 2", "b offline 1"} {
		if !bodies["a"][want] {
			t.Fatalf("node-a missing %q (bodies=%v)", want, bodies["a"])
		}
		if !bodies["b"][want] {
			t.Fatalf("node-b missing %q (bodies=%v)", want, bodies["b"])
		}
	}

	// M-c：每个节点上所有消息的 LocalSeq 必须非零且互不重复（本地视图序
	// 单调）——mesh 同步消息由接收节点重新分配，广播闭环（DeliverSynced
	// 推给客户端）用的就是这条权威值，源节点的 LocalSeq 不得泄漏到本地。
	for node, s := range map[string]*libsql.Store{"a": storeA, "b": storeB} {
		seen := map[uint64]bool{}
		cursor, _ := s.SourceCursor(ctx)
		for src := range cursor {
			msgs, _ := s.SyncMessages(ctx, src, 0, 0)
			for _, m := range msgs {
				if m.LocalSeq == 0 {
					t.Fatalf("node-%s message %q: LocalSeq == 0", node, m.Body)
				}
				if seen[m.LocalSeq] {
					t.Fatalf("node-%s: duplicate LocalSeq %d (message %q)", node, m.LocalSeq, m.Body)
				}
				seen[m.LocalSeq] = true
			}
		}
	}
}

// TestMeshDeliverToLocalClient：广播闭环——A 的本地消息经 mesh 同步到 B
// 落库后，B 上已连接的 WS 客户端实时收到 FKDeliver，且重复同步不重复推送。
func TestMeshDeliverToLocalClient(t *testing.T) {
	ctx := context.Background()
	portA, portB := freePort(t), freePort(t)
	urlA := "http://127.0.0.1:" + strconv.Itoa(portA)
	urlB := "http://127.0.0.1:" + strconv.Itoa(portB)
	srvA, _ := startMeshHubOn(t, "node-a", []string{urlB}, portA)
	_, _ = startMeshHubOn(t, "node-b", []string{urlA}, portB)

	// B 上连一个 WS 客户端并完成应用层握手（测试信任一切公钥）。
	tr := wstransport.New().WithClientTrust(func([]byte) error { return nil })
	connB, err := tr.Dial(ctx, urlB, protocol.Hello{DeviceID: "dev-b"})
	if err != nil {
		t.Fatalf("dial B hub: %v", err)
	}
	defer func() { _ = connB.Close() }()
	hello, _ := json.Marshal(protocol.Hello{
		ProtocolVersion: protocol.ProtocolVersion,
		DeviceID:        "dev-b",
		UserID:          "bob",
	})
	if err := connB.Send(ctx, protocol.Frame{Kind: protocol.FKHello, Payload: hello}); err != nil {
		t.Fatalf("send hello: %v", err)
	}

	// 收帧循环（握手后 hub 会推 presence/read/conv 快照，全部消费掉）。
	recvCh := make(chan protocol.Frame, 32)
	go func() {
		for {
			f, err := connB.Recv(ctx)
			if err != nil {
				return
			}
			recvCh <- f
		}
	}()

	// A 本地直写一条大厅消息（模拟 A 客户端已发送并落库）。
	storeA, ok := srvA.store.(*libsql.Store)
	if !ok {
		t.Fatalf("store type %T, want *libsql.Store", srvA.store)
	}
	if _, err := storeA.AppendMessage(ctx, protocol.StoredMessage{
		ID: "a-1", ConversationID: "", // 大厅
		SenderUserID: "alice", SenderDeviceID: "dev-a",
		Body: "mesh deliver hello", ServerSeq: 1, CreatedAt: time.Now().UnixMilli(), NodeID: "node-a",
	}); err != nil {
		t.Fatalf("A append: %v", err)
	}

	// 等 B 客户端在 mesh 周期内收到 FKDeliver。
	deadline := time.Now().Add(15 * time.Second)
	got := 0
	for time.Now().Before(deadline) {
		select {
		case f := <-recvCh:
			if f.Kind == protocol.FKDeliver {
				var m protocol.StoredMessage
				if err := json.Unmarshal(f.Payload, &m); err == nil && m.Body == "mesh deliver hello" {
					got++
				}
			}
		case <-time.After(200 * time.Millisecond):
		}
		if got >= 1 {
			break
		}
	}
	if got != 1 {
		t.Fatalf("B client received %d deliveries, want exactly 1", got)
	}

	// 再等一个完整 meshLoop 周期（5s）+ 余量：重复同步不得重复推送。
	time.Sleep(6500 * time.Millisecond)
	select {
	case f := <-recvCh:
		var m protocol.StoredMessage
		if f.Kind == protocol.FKDeliver && json.Unmarshal(f.Payload, &m) == nil && m.Body == "mesh deliver hello" {
			t.Fatal("duplicate FKDeliver after extra mesh cycle")
		}
	case <-time.After(200 * time.Millisecond):
	}
}
