package server

import (
	"context"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/pandaymx/lanchat/internal/protocol"
)

// sendQueueCap 是每客户端待发消息队列容量；队列满即判定为慢客户端并踢除。
const sendQueueCap = 256

// helloWait 是首帧 HELLO 的最长等待时间。
const helloWait = 10 * time.Second

// writeWait 是单条 WS 帧的最长写入时间。
const writeWait = 2 * time.Second

// Client 表示一条已接受的 WebSocket 连接。
type Client struct {
	id       string // selfID（=deviceID，缺失则由服务端生成 UUID）
	nickname string
	os       string
	addr     string // 远端地址（鉴权限流用）
	conn     *websocket.Conn

	send     chan *protocol.Envelope
	closed   chan struct{}
	draining bool // 优雅关闭中：写泵需排空 send 再关连接（仅由写泵读取、由 terminate 置位）
	closeO   sync.Once

	registered bool // 是否已完成 register（由 hub goroutine 置位）
	replaced   bool // 是否被同 ID 的新会话顶掉（由 hub goroutine 置位）

	lastSeenMu sync.Mutex
	lastSeen   time.Time
}

func newClient(conn *websocket.Conn, addr string) *Client {
	return &Client{
		addr:     addr,
		conn:     conn,
		send:     make(chan *protocol.Envelope, sendQueueCap),
		closed:   make(chan struct{}),
		lastSeen: time.Now(),
	}
}

// user 返回该客户端对应的在线用户条目。
func (c *Client) user() protocol.User {
	return protocol.User{ID: c.id, Nickname: c.nickname, OS: c.os}
}

func (c *Client) touch() {
	c.lastSeenMu.Lock()
	c.lastSeen = time.Now()
	c.lastSeenMu.Unlock()
}

func (c *Client) seenAt() time.Time {
	c.lastSeenMu.Lock()
	defer c.lastSeenMu.Unlock()
	return c.lastSeen
}

// enqueue 非阻塞投递一条待发消息，队列满或连接已关闭时返回 false。
func (c *Client) enqueue(env *protocol.Envelope) bool {
	select {
	case <-c.closed:
		return false
	default:
	}
	select {
	case c.send <- env:
		return true
	case <-c.closed:
		return false
	default:
		return false
	}
}

// terminate 幂等发起优雅关闭：先置 draining 再 close(closed)，
// 写泵收到 closed 后排空 send 并关闭底层连接，保证已入队的 AUTH_FAIL/ERROR 等帧不丢。
func (c *Client) terminate() {
	c.closeO.Do(func() {
		c.draining = true
		close(c.closed)
	})
}

// writePump 串行消费 send 队列并写 WS；所有 WS 写都只发生在这里。
func (c *Client) writePump() {
	for {
		select {
		case env := <-c.send:
			if !c.writeOne(env) {
				return
			}
		case <-c.closed:
			if !c.draining {
				// 写失败等非优雅路径：直接退出。
				return
			}
			// 优雅关闭：排空已入队消息，再关闭底层连接。
			for {
				select {
				case env := <-c.send:
					if !c.writeOne(env) {
						return
					}
				default:
					_ = c.conn.Close(websocket.StatusNormalClosure, "")
					return
				}
			}
		}
	}
}

// writeOne 写入一条信封，失败则直接退出写泵。
func (c *Client) writeOne(env *protocol.Envelope) bool {
	data, err := env.Marshal()
	if err != nil {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeWait)
	defer cancel()
	if err := c.conn.Write(ctx, websocket.MessageText, data); err != nil {
		return false
	}
	return true
}

// mustEnqueue 构造一条服务器下行信封并入队，入队失败立即终止该客户端（慢客户端）。
func (s *Server) mustEnqueue(c *Client, typ string, payload any) {
	env, err := protocol.NewEnvelope(uuid.NewString(), typ, payload)
	if err != nil {
		return
	}
	env.From = "" // 服务器本身不在在线表内
	if !c.enqueue(env) {
		s.dropClient(c)
	}
}
