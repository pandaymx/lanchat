package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/pandaymx/lanchat/internal/protocol"
)

func TestCrossSubnetServerFederationBridge(t *testing.T) {
	opts := Options{
		Listen:            "127.0.0.1:0",
		Path:              "/lanchat",
		AuthMode:          "none",
		HeartbeatInterval: time.Second,
		IdleTimeout:       time.Minute,
		ShutdownGrace:     time.Second,
	}
	srvA, err := New(opts, nil)
	if err != nil {
		t.Fatal(err)
	}
	srvB, err := New(opts, nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srvA.Serve(ctx) }()
	go func() { _ = srvB.Serve(ctx) }()
	<-srvA.Ready()
	<-srvB.Ready()

	srvA.ConfigureFederationSecret("fed-secret-v10")
	srvB.ConfigureFederationSecret("fed-secret-v10")
	if !srvA.AddFederationPeer(srvB.ListenAddr()) || !srvB.AddFederationPeer(srvA.ListenAddr()) {
		t.Fatal("failed to register bi-directional federation peers")
	}
	if len(srvA.FederationPeers()) != 1 || len(srvB.FederationPeers()) != 1 {
		t.Fatalf("unexpected federation peers: A=%v B=%v", srvA.FederationPeers(), srvB.FederationPeers())
	}

	cA := newClient(&websocket.Conn{}, "127.0.0.1")
	cA.id = "alice-subnet-a"
	srvA.hub.reg.add(cA)

	cB := newClient(&websocket.Conn{}, "127.0.0.1")
	cB.id = "bob-subnet-b"
	srvB.hub.reg.add(cB)

	// 1. 子网 A 的 Alice 发送 G1 全员广播，子网 B 的 Bob 应通过联邦桥接实时收到，且不产生环路风暴。
	bcastEnv, err := protocol.NewEnvelope("fed-bcast-1", protocol.TextMsg, protocol.TextPayload{Text: "跨子网联邦广播测试"})
	if err != nil {
		t.Fatal(err)
	}
	bcastEnv.Group = protocol.GroupBroadcast
	srvA.hub.submit(hubEvent{kind: evRoute, client: cA, env: bcastEnv})

	select {
	case got := <-cB.send:
		if got.ID != "fed-bcast-1" || got.From != "alice-subnet-a" {
			t.Fatalf("bob on Server B got unexpected federated broadcast: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for federated broadcast on Server B")
	}

	// 2. 子网 B 的 Bob 向子网 A 的 Alice 发送单播消息，Alice 在子网 A 实时收到。
	uniEnv, err := protocol.NewEnvelope("fed-uni-1", protocol.TextMsg, protocol.TextPayload{Text: "跨子网单播直达"})
	if err != nil {
		t.Fatal(err)
	}
	uniEnv.Group = protocol.GroupUnicast
	uniEnv.To = "alice-subnet-a"
	srvB.hub.submit(hubEvent{kind: evRoute, client: cB, env: uniEnv})

	// 先消费掉 cA 上刚才广播的 ACK
	for {
		select {
		case msg := <-cA.send:
			if msg.ID == "fed-uni-1" && msg.From == "bob-subnet-b" {
				goto unicastOK
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for federated unicast on Server A")
		}
	}
unicastOK:

	// 3. 验证错误 HMAC 签名被 401 拦截，以及重复 Envelope ID 被去重拦截。
	ingestURL := srvB.FederationPeers()[0]
	rawBody, _ := json.Marshal(FederatedEnvelope{
		OriginServer: "srv-external",
		Hops:         1,
		Envelope:     bcastEnv,
	})
	badReq, _ := http.NewRequest(http.MethodPost, ingestURL, bytes.NewReader(rawBody))
	badReq.Header.Set("X-LANChat-Fed-Signature", "sha256=invalid")
	badResp, err := http.DefaultClient.Do(badReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = badResp.Body.Close()
	if badResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on invalid federation HMAC, got %d", badResp.StatusCode)
	}

	// 发送已见过的 fed-bcast-1（正确签名），应触发去重计数 dedupDropped++
	dupReq, _ := http.NewRequest(http.MethodPost, ingestURL, bytes.NewReader(rawBody))
	dupReq.Header.Set("X-LANChat-Fed-Signature", srvA.hub.fed.signBody(rawBody))
	dupResp, err := http.DefaultClient.Do(dupReq)
	if err != nil {
		t.Fatal(err)
	}
	_ = dupResp.Body.Close()
	if dupResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK on dedup drop, got %d", dupResp.StatusCode)
	}
	inA, outA, dedupA := srvA.FederationStats()
	if inA == 0 || outA == 0 || dedupA == 0 {
		t.Fatalf("expected positive federation stats on Server A, got in=%d out=%d dedup=%d", inA, outA, dedupA)
	}
}
