package discovery

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/grandcat/zeroconf"
)

func TestTextValue(t *testing.T) {
	cases := []struct {
		text []string
		key  string
		want string
	}{
		{[]string{"path=/ws", "version=1.2.3"}, "path", "/ws"},
		{[]string{"path=/ws", "version=1.2.3"}, "version", "1.2.3"},
		{[]string{"path=/ws"}, "version", ""},
		{nil, "path", ""},
		{[]string{"weird"}, "weird", ""}, // 无等号
	}
	for _, c := range cases {
		if got := textValue(c.text, c.key); got != c.want {
			t.Errorf("textValue(%v, %q) = %q, want %q", c.text, c.key, got, c.want)
		}
	}
}

func TestPickAddr(t *testing.T) {
	loopback := net.ParseIP("127.0.0.1")
	lan := net.ParseIP("192.168.1.42")
	lan6 := net.ParseIP("fd00::42")

	if got := pickAddr(&zeroconf.ServiceEntry{AddrIPv4: []net.IP{lan, loopback}}); got != "192.168.1.42" {
		t.Errorf("非 loopback IPv4 应优先，got %s", got)
	}
	if got := pickAddr(&zeroconf.ServiceEntry{AddrIPv4: []net.IP{loopback}}); got != "127.0.0.1" {
		t.Errorf("只剩 loopback 时应回退 loopback，got %s", got)
	}
	if got := pickAddr(&zeroconf.ServiceEntry{AddrIPv6: []net.IP{lan6}}); got != "fd00::42" {
		t.Errorf("IPv6 唯一地址时应采用，got %s", got)
	}
	if got := pickAddr(&zeroconf.ServiceEntry{}); got != "" {
		t.Errorf("无地址应返回空串，got %s", got)
	}
}

// TestBroadcastAndDiscover 端到端：注册一个实例后浏览，应能发现自己。
// 无组播路由的环境（部分容器/CI）发现不到时 Skip，不算失败。
func TestBroadcastAndDiscover(t *testing.T) {
	instance := "lanchat-test-" + t.Name()
	const port = 19876
	shutdown, err := Broadcast(instance, port, map[string]string{
		MetaPath:    "/ws",
		MetaVersion: "test",
	})
	if err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	defer shutdown()

	// 等注册协程把套接字拉起、能响应查询。
	time.Sleep(500 * time.Millisecond)

	instances, err := Discover(context.Background(), 3*time.Second)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(instances) == 0 {
		t.Skip("本环境 mDNS 组播不可用（容器/CI 常见），跳过端到端断言")
	}

	var hit *Instance
	for i := range instances {
		if instances[i].Name == instance {
			hit = &instances[i]
			break
		}
	}
	if hit == nil {
		t.Fatalf("发现 %d 个实例但没有自己 %q: %+v", len(instances), instance, instances)
	}
	if hit.Port != port {
		t.Errorf("Port = %d, want %d", hit.Port, port)
	}
	if hit.Path != "/ws" {
		t.Errorf("Path = %q, want /ws", hit.Path)
	}
	if net.ParseIP(hit.Addr) == nil {
		t.Errorf("Addr = %q 不是合法 IP", hit.Addr)
	}
}
