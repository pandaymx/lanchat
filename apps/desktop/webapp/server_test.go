package webapp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/hubstate"
	"github.com/pandaymx/lanchat/pkg/protocol"
	"github.com/pandaymx/lanchat/pkg/store/memory"
	wstransport "github.com/pandaymx/lanchat/pkg/transport/ws"
)

// newTestServer 起一个桌面端本地服务。HubURL 指向不可达端口：验证
// 「hub 不在线时窗口骨架也能打开」——首页返回 503（webui 惰性拨号失败
// 的既有行为），HTTP 服务本身活着，hub 恢复后自愈。
func newTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := Start(Options{
		HubURL:      "ws://127.0.0.1:1/ws",
		User:        "tester",
		Version:     "test",
		DialTimeout: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// getStatus 轮询 GET 直到拿到 HTTP 响应（不要求 200），返回状态码与 body。
func getStatus(t *testing.T, url string) (int, string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			return resp.StatusCode, string(body)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("GET %s no response within deadline", url)
	return 0, ""
}

// newTestHub 起一个真实内存 hub（ws transport + hubstate.Router），返回
// ws URL。Listen 拿不到随机端口，这里从 19000 起逐个尝试固定回环端口。
func newTestHub(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	store := memory.New()
	router := hubstate.NewRouter(&hubstate.RouterConfig{Store: store})
	onConn := func(conn core.Conn, _ protocol.Hello) error {
		p, ok := conn.(hubstate.Peer)
		if !ok {
			_ = conn.Close()
			return fmt.Errorf("incompatible peer type %T", conn)
		}
		router.Attach(ctx, p)
		return nil
	}

	for port := 19000; port < 19100; port++ {
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		errCh := make(chan error, 1)
		go func(a string) { errCh <- wstransport.New().Listen(ctx, a, onConn) }(addr)
		select {
		case err := <-errCh:
			if err != nil {
				continue // 端口占用等，试下一个
			}
		case <-time.After(150 * time.Millisecond):
			// Listen 阻塞运行中 = 成功绑定
			return "ws://" + addr + wstransport.DefaultPath
		}
	}
	t.Fatal("no free hub port")
	return ""
}

func TestServer_ServeAndClose(t *testing.T) {
	s := newTestServer(t)

	// 回环地址，不是局域网接口。
	if !strings.HasPrefix(s.URL(), "http://127.0.0.1:") {
		t.Errorf("URL = %q, want loopback", s.URL())
	}
	// hub 不可达：首页返回 503（惰性拨号失败），但 HTTP 服务活着。
	status, _ := getStatus(t, s.URL()+"/")
	if status != http.StatusServiceUnavailable {
		t.Errorf("home status = %d, want 503 (hub unreachable)", status)
	}

	// Close 后端口释放，连接应失败。
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	resp, err := http.Get(s.URL())
	if err == nil {
		_ = resp.Body.Close()
		t.Error("GET after Close succeeded, want connection error")
	}
}

func TestServer_WithHub(t *testing.T) {
	hubURL := newTestHub(t)

	s, err := Start(Options{
		HubURL:  hubURL,
		User:    "tester",
		Version: "test",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = s.Close() }()

	// hub 在线：首页正常渲染聊天骨架。
	status, body := getStatus(t, s.URL()+"/")
	if status != http.StatusOK {
		t.Fatalf("home status = %d, want 200", status)
	}
	if !strings.Contains(body, "lanchat") {
		t.Errorf("home body missing lanchat: %.120q", body)
	}
}

func TestServer_Restart(t *testing.T) {
	s1 := newTestServer(t)
	u1 := s1.URL()
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// s1.Close 已释放；newTestServer 的 Cleanup 对 s1 是幂等二次 Close，
	// http.Server.Shutdown 幂等，无副作用。

	// 关掉后立刻再起：端口不冲突、服务可用（模拟窗口重开）。
	s2 := newTestServer(t)
	if s2.URL() == u1 {
		t.Logf("same port reused: %s", u1) // 允许，不强制
	}
	status, _ := getStatus(t, s2.URL()+"/")
	if status != http.StatusServiceUnavailable {
		t.Errorf("home status = %d, want 503 (hub unreachable)", status)
	}
}
