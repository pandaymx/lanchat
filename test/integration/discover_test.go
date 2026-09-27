package integration

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/internal/discover"
)

// multicastAvailable 验证两个独立 UDP socket 之间多播环回的真实收发能力。
// 仅检查能否绑定或单 socket 自发自收都不够：有的环境可绑定多播地址、同 socket
// 也能收到自己，但内核不会把多播投递给同一主机上的其它 socket（dnssd 的
// responder 与 browser 正是两个独立 socket），导致活测试超时。
// 此处用 224.0.0.252:5354 避开 5353 端口。
func multicastAvailable() bool {
	group, err := net.ResolveUDPAddr("udp4", "224.0.0.252:5354")
	if err != nil {
		return false
	}
	// 接收侧：加入多播组。
	rcv, err := net.ListenMulticastUDP("udp4", nil, group)
	if err != nil {
		return false
	}
	defer rcv.Close()

	// 发送侧：独立 socket（不绑组），近似 dnssd responder。
	snd, err := net.DialUDP("udp4", nil, group)
	if err != nil {
		return false
	}
	defer snd.Close()

	payload := []byte("lanchat-mcast-probe")
	if _, err := snd.Write(payload); err != nil {
		return false
	}
	_ = rcv.SetReadDeadline(time.Now().Add(800 * time.Millisecond))
	buf := make([]byte, len(payload))
	n, _, err := rcv.ReadFromUDP(buf)
	if err != nil || n != len(payload) {
		return false
	}
	return true
}

// TestMdnsRegisterAndBrowse 验证服务端注册后，客户端能浏览到该实例，
// 且 TXT 字段与地址一致。不支持多播的环境自动跳过。
//
// dnssd responder 注册前要先 ProbeService（随机 0–250ms 延迟 + 最多 3 轮
// probe、每轮等待 250ms），通过后才发含 TXT 的首次通告，消息积压时整体
// 可能到启动后约 3s。因此注册后等待通告就绪再浏览：浏览器发出的 PTR 查询
// 会得到 PTR + SRV + TXT 的完整响应，首个回调即带 ID，避免偷听到 probe
// 包（无 TXT）产生残条目。
func TestMdnsRegisterAndBrowse(t *testing.T) {
	if !multicastAvailable() {
		t.Skip("mDNS 多播不可用，跳过活测试")
	}

	meta := discover.ServerMeta{
		Version: "test",
		Path:    "/lctp",
		Name:    "Integration",
		Auth:    "psk",
		ID:      "it-" + t.Name(),
	}
	adv, err := discover.NewAdvertiser(7799, meta)
	if err != nil {
		t.Fatalf("注册 mDNS: %v", err)
	}
	t.Cleanup(adv.Close)

	// 等待 probe 完成并发出含 TXT 的首次通告。
	time.Sleep(4 * time.Second)

	browser := discover.NewBrowser()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	found := make(chan []discover.Server, 1)
	go func() {
		_ = browser.Browse(ctx, func(servers []discover.Server) {
			for _, s := range servers {
				if s.ID == meta.ID {
					select {
					case found <- servers:
					default:
					}
					return
				}
			}
		})
	}()

	select {
	case servers := <-found:
		var srv discover.Server
		for _, s := range servers {
			if s.ID == meta.ID {
				srv = s
			}
		}
		if srv.Name != meta.Name || srv.Path != meta.Path || srv.Auth != "psk" {
			t.Fatalf("TXT 字段不一致: %+v", srv)
		}
		if srv.Port != 7799 || len(srv.Addresses) == 0 {
			t.Fatalf("端口/地址异常: port=%d addrs=%v", srv.Port, srv.Addresses)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("4s 内未通过 mDNS 发现注册实例")
	}
}
