// 临时集成测试 client：连本机 hub、发 FKHello + FKMessage、等 FKDeliver 回来。
//
// 用完即弃，验证 cmd/hub wire 通即可，不进项目仓库。
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/pandaymx/lanchat/pkg/protocol"
)

// wsClient 是一次性 WS 客户端封装。建链、发帧、读帧都有 helper。
type wsClient struct {
	t       *testing.T
	ws      *websocket.Conn
	readCh  chan readFrame
	cancel  context.CancelFunc
	closed  bool
	closeMu sync.Mutex
}

type readFrame struct {
	kind protocol.FrameKind
	body []byte
	err  error
}

// dial 建立 WS 连接并发 FKHello。t.Helper 让失败行号指到调用方。
func dial(t *testing.T, addr string, hello protocol.Hello) *wsClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	url := "ws://" + addr + "/ws"
	ws, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("dial %s: %v", url, err)
	}
	ws.SetReadLimit(1 << 24)

	c := &wsClient{
		t:      t,
		ws:     ws,
		readCh: make(chan readFrame, 16),
	}

	ctx2, cancel2 := context.WithCancel(context.Background())
	c.cancel = cancel2
	go c.readLoop(ctx2)

	helloBytes, err := json.Marshal(hello)
	if err != nil {
		t.Fatalf("marshal hello: %v", err)
	}
	if err := c.writeFrame(ctx, protocol.FKHello, helloBytes); err != nil {
		t.Fatalf("write FKHello: %v", err)
	}
	return c
}

// readLoop 持续从 ws 读帧并塞进 readCh。
func (c *wsClient) readLoop(ctx context.Context) {
	for {
		_, data, err := c.ws.Read(ctx)
		if err != nil {
			c.readCh <- readFrame{err: err}
			return
		}
		f, err := protocol.DecodeFrame(bytes.NewReader(data))
		if err != nil {
			c.t.Logf("decode err: %v", err)
			continue
		}
		c.readCh <- readFrame{kind: f.Kind, body: f.Payload}
	}
}

// send 把 v 编码成长度前缀帧写入 ws。
func (c *wsClient) send(ctx context.Context, kind protocol.FrameKind, v any) {
	c.t.Helper()
	payload, err := json.Marshal(v)
	if err != nil {
		c.t.Fatalf("encode %s: %v", kind, err)
	}
	if err := c.writeFrame(ctx, kind, payload); err != nil {
		c.t.Fatalf("write %s: %v", kind, err)
	}
}

// writeFrame 把 Frame 编码成长度前缀 + JSON body 写入 ws。
func (c *wsClient) writeFrame(ctx context.Context, kind protocol.FrameKind, payload []byte) error {
	var buf bytes.Buffer
	if err := protocol.EncodeFrame(&buf, protocol.Frame{Kind: kind, Payload: payload}); err != nil {
		return err
	}
	return c.ws.Write(ctx, websocket.MessageBinary, buf.Bytes())
}

// tryWait 在 d 时间内等 kind 帧，否则返回 (nil, false)。
// 与 waitFor 的区别：不 t.Fatal，给 eventually 重试复用。
// 读到非匹配帧继续等，过期返回 (nil, false)。
func (c *wsClient) tryWait(kind protocol.FrameKind, d time.Duration) ([]byte, bool) {
	c.t.Helper()
	deadline := time.NewTimer(d)
	defer deadline.Stop()
	for {
		select {
		case rf := <-c.readCh:
			if rf.err != nil {
				return nil, false
			}
			if rf.kind == kind {
				return rf.body, true
			}
			// skip non-matching frame
		case <-deadline.C:
			return nil, false
		}
	}
}

// eventually 在 total 时间内每 probe 重试 tryWait，直到收到 kind 帧。
// 用于 race + cover 同时跑时偶发 5s 内来不及的 flaky 场景：
// 单次 waitFor 在 race 高负载下可能因调度延迟 5s 内不来，
// eventually 给一个总预算上界并按 probe 间隔轮询，避免一次失败即 Fatal。
//
// 用法：c.eventually(protocol.FKDeliver, 10*time.Second, 200*time.Millisecond)
//
//	= 累计最多 10s，每 200ms 探测一次，过期 Fatal。
func (c *wsClient) eventually(kind protocol.FrameKind, total, probe time.Duration) []byte {
	c.t.Helper()
	deadline := time.Now().Add(total)
	for {
		body, ok := c.tryWait(kind, probe)
		if ok {
			return body
		}
		if time.Now().After(deadline) {
			c.t.Fatalf("eventually: never got %s after %s", kind, total)
		}
	}
}

// close 关 ws。
func (c *wsClient) close() {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	c.cancel()
	_ = c.ws.Close(websocket.StatusNormalClosure, "bye")
}

// awaitReady 等到 Hub 已处理本连接的 FKHello，再做下一步动作。
//
// 用 FKPing→FKPong 往返做屏障：serveLoop 是按到达顺序处理帧的，
// FKPong 返回等价于「Hub 已经看到并处理完了你之前发的所有帧」，
// 也就意味着 hubstate.Registry.MarkHello 已调用，helloOK=true。
//
// 不做这个屏障的后果（已实测）：
//
//	alice := dial(...)
//	bob   := dial(...)
//	alice.send(FKMessage, ...)
//	↑ 此刻 Bob 的 FKHello 还在 TCP 缓冲里，Hub serveLoop 还没读到，
//	Hub 收到 FKMessage 走 broadcast 时 AllPeers 只看到 alice 一条，
//	bob 永远收不到 FKDeliver —— eventually 必超时。
//
// 用法：dial 后立刻 awaitReady；多连接场景下要等每个都 awaitReady 才发消息。
func (c *wsClient) awaitReady() {
	c.t.Helper()
	if err := c.writeFrame(context.Background(), protocol.FKPing, nil); err != nil {
		c.t.Fatalf("awaitReady: write ping: %v", err)
	}
	_, ok := c.tryWait(protocol.FKPong, 5*time.Second)
	if !ok {
		c.t.Fatalf("awaitReady: never got pong (hub not processing frames?)")
	}
}
