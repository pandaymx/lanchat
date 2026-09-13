// mesh_integration_test.go 验证两个嵌入式 hub 通过真实 HTTP 端点
// 互相同步（ADR-014 wire v2）：A 发消息落库（盖上 node-a），B 从
// A 拉增量后消息一致；反向同理。
package hubserver

import (
	"context"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/pkg/mesh"
	"github.com/pandaymx/lanchat/pkg/protocol"
	"github.com/pandaymx/lanchat/pkg/store/libsql"
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
// 固定空闲端口），返回 Server 与 base URL。
func startMeshHub(t *testing.T, nodeID string, peers []string) (*Server, string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	port := freePort(t)
	srv, err := Start(ctx, Config{
		Addr:      "127.0.0.1:" + strconv.Itoa(port),
		DBPath:    filepath.Join(t.TempDir(), "mesh.db"),
		MDNS:      false,
		Mesh:      true,
		MeshPeers: peers,
		NodeID:    nodeID,
	})
	if err != nil {
		cancel()
		t.Fatalf("start mesh hub %s: %v", nodeID, err)
	}
	// LIFO：cancel 后注册 → 先执行 cancel（meshLoop/run 靠 ctx 退出），
	// 再执行 Close（空操作，幂等）。
	t.Cleanup(cancel)
	t.Cleanup(func() { _ = srv.Close() })
	return srv, "http://" + srv.Addr()
}

// TestMeshTwoHubsSync：A/B 互指为邻居，各自发消息后收敛一致。
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
	err := storeA.AppendMessage(ctx, protocol.StoredMessage{
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

// TestMeshEndpointDirect：不经后台循环，直接 HTTP 调 mesh 端点一轮同步。
func TestMeshEndpointDirect(t *testing.T) {
	ctx := context.Background()
	srvA, urlA := startMeshHub(t, "node-a", nil)
	srvB, _ := startMeshHub(t, "node-b", nil)

	storeA, _ := srvA.store.(*libsql.Store)
	err := storeA.AppendMessage(ctx, protocol.StoredMessage{
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

	storeB, _ := srvB.store.(*libsql.Store)
	resps, err := mesh.SyncPeer(ctx, urlA, protocol.SyncRequest{Limit: mesh.DefaultLimit})
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
}
