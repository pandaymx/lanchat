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

// waitFor 在 d 时间内等 kind 帧，否则 t.Fatal。
func (c *wsClient) waitFor(kind protocol.FrameKind, d time.Duration) []byte {
	c.t.Helper()
	deadline := time.NewTimer(d)
	defer deadline.Stop()
	for {
		select {
		case rf := <-c.readCh:
			if rf.err != nil {
				c.t.Fatalf("read err while waiting for %s: %v", kind, rf.err)
			}
			if rf.kind == kind {
				return rf.body
			}
			c.t.Logf("skip frame kind=%s while waiting for %s", rf.kind, kind)
		case <-deadline.C:
			c.t.Fatalf("timeout waiting for %s after %s", kind, d)
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
