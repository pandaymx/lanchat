// Package ws 提供 core.Transport 的 WebSocket 实现。
//
// 这是 M2 起真 Hub 与真客户端共用的传输层。选型理由见 ADR-002 与 AGENTS.md §2：
//   - WebSocket 是唯一的一期传输实现（全端统一、双向、浏览器原生）
//   - 库用 coder/websocket 而非 gorilla（后者已归档）
//
// 本包只做「字节怎么变成 Frame」，不含任何业务语义：
// 帧的处理统一在 hubstate.Router 里，与 fake transport 共用。
//
// 两个角色：
//   - Dial   —— 客户端拨号到 hub 地址（ws://host:port）
//   - Listen —— Hub 监听端口，accept 出的连接交给 Router
package ws

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/logging"
	"github.com/pandaymx/lanchat/pkg/protocol"
	"github.com/pandaymx/lanchat/pkg/secure"
)

// connLog 是 ws.Conn 收发帧的 logger。包级单例，不引入注入。
var connLog = logging.New("ws.conn")

// conn 把 *websocket.Conn 包装成 core.Conn 与 hubstate.Peer。
//
// 一个对象同时满足两个接口是刻意的：
//   - 客户端侧拿它当 core.Conn（Send=发给 Hub，Recv=收 Hub 的）
//   - Hub 侧拿它当 hubstate.Peer（Send=发给客户端，Recv=收客户端的）
//
// 两个接口的 Send/Recv 方向语义一致（都是"持有者视角"），
// 所以同一份实现两边都能用——这正是它比 fake.conn 省事的地方：
// fake 的管道两头共用需要 revConn 反转，这里不需要。
//
// 并发模型：
//   - 读：同一时刻只允许一个 Recv（readMu 串行化），因为帧是流式的
//   - 写：同一时刻只允许一个 Send（writeMu 串行化），避免半帧交错
//   - 读写之间可以并发（WebSocket 是全双工）
type conn struct {
	ws *websocket.Conn
	// devID 是连接绑定的设备标识。
	// 客户端侧在 Dial 时就已知；Hub 侧是 accept 时的临时值，
	// 等 FKHello 进来后由 Router 用协议声明的值覆盖（见 hubstate.Registry.MarkHello）。
	devID string

	readMu  sync.Mutex
	writeMu sync.Mutex

	// enc 是会话加密状态；nil = 明文连接（握手完成前/未启用）。
	// 启用后 Send/Recv 对整帧 wire body（长度前缀之后的 JSON）加解密，
	// 上层（Router/client 业务逻辑）无感知。
	encMu sync.RWMutex
	enc   *encState

	closeOnce sync.Once
	closeErr  error
	closedCh  chan struct{}
}

// newConn 包装一条已建立的 WebSocket 连接。
func newConn(c *websocket.Conn, deviceID string) *conn {
	return &conn{
		ws:       c,
		devID:    deviceID,
		closedCh: make(chan struct{}),
	}
}

// writeTimeout 是单帧写入的超时。
//
// 没有它，一个对端不读的连接会让 Send 永久阻塞，进而卡死整条广播链。
// 超时的连接会被判死并关闭 —— 这是必要的止损，宁可误杀也不能拖垮 Hub。
const writeTimeout = 10 * time.Second

// encState 是一条已加密连接的状态（会话密钥 + AAD）。
type encState struct {
	key []byte
	aad []byte
}

// EnableEncryption 启用会话加密。必须在连接上的业务帧（FKHello 等）
// 发出之前调用；之后本连接的所有帧都整体加密。
func (c *conn) EnableEncryption(key, aad []byte) {
	c.encMu.Lock()
	c.enc = &encState{key: append([]byte(nil), key...), aad: append([]byte(nil), aad...)}
	c.encMu.Unlock()
}

// isEncrypted 返回本连接是否已启用加密。
func (c *conn) isEncrypted() bool {
	c.encMu.RLock()
	defer c.encMu.RUnlock()
	return c.enc != nil
}

// sealBody 加密一帧的 wire body（长度前缀之后的部分）。
func (c *conn) sealBody(body []byte) ([]byte, error) {
	c.encMu.RLock()
	defer c.encMu.RUnlock()
	if c.enc == nil {
		return body, nil
	}
	s, err := secure.NewSessionFromKey(c.enc.key, c.enc.aad)
	if err != nil {
		return nil, err
	}
	return s.Seal(body)
}

// openBody 解密一帧的 wire body。
func (c *conn) openBody(data []byte) ([]byte, error) {
	c.encMu.RLock()
	defer c.encMu.RUnlock()
	if c.enc == nil {
		return data, nil
	}
	s, err := secure.NewSessionFromKey(c.enc.key, c.enc.aad)
	if err != nil {
		return nil, err
	}
	return s.Open(data)
}

// Send 编码并写出一帧。并发安全（内部串行化）。
//
// 长度前缀 + JSON body 的编码统一由 protocol.EncodeFrame 负责，
// 本方法只负责把它写进 WebSocket 的一条 message。
// 之所以一帧对应一条 WS message（而不是把 WS 当裸字节流）：
// 消息边界由 WebSocket 协议保证，不需要自己再处理粘包。
func (c *conn) Send(ctx context.Context, f protocol.Frame) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	select {
	case <-c.closedCh:
		return core.ErrClosed
	default:
	}

	writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()

	// 用一个 bytes.Buffer 承接 EncodeFrame 的输出，
	// 因为 WebSocket 的 Writer 不支持"先写长度再写 body"的多次 Write 合并成一条 message——
	// 每次 Writer() 调用都是一条独立 message。
	var buf bytes.Buffer
	if err := protocol.EncodeFrame(&buf, f); err != nil {
		connLog.Error("encode frame failed", "kind", f.Kind, "err", err)
		return err
	}
	// 加密在长度前缀之外：wire body 整体 Seal，对端 openBody 后再
	// DecodeFrame，帧类型与内容对线缆不可见。
	body := buf.Bytes()[4:] // 去掉 4 字节长度前缀，用明文长度重算
	sealed, err := c.sealBody(body)
	if err != nil {
		connLog.Error("seal frame failed", "kind", f.Kind, "err", err)
		return err
	}
	var sealedBuf [4]byte
	binary.BigEndian.PutUint32(sealedBuf[:], uint32(len(sealed)))
	wireMsg := make([]byte, 0, 4+len(sealed))
	wireMsg = append(wireMsg, sealedBuf[:]...)
	wireMsg = append(wireMsg, sealed...)
	connLog.Debug("send frame", "kind", f.Kind, "len", len(wireMsg), "dev", c.devID)
	if err := c.ws.Write(writeCtx, websocket.MessageBinary, wireMsg); err != nil {
		// 写失败意味着连接已不可用，直接关掉避免后续重试
		connLog.Error("ws write failed", "kind", f.Kind, "err", err)
		_ = c.Close()
		return err
	}
	return nil
}

// Recv 读一帧。并发安全（内部串行化），但同一时刻只会有一个调用者。
//
// 连接正常关闭时返回 io.EOF —— 这是上层判断"对端走了"的标准信号。
func (c *conn) Recv(ctx context.Context) (protocol.Frame, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()

	select {
	case <-c.closedCh:
		return protocol.Frame{}, io.EOF
	default:
	}

	_, data, err := c.ws.Read(ctx)
	if err != nil {
		// coder/websocket 用 StatusNormalClosure 表示对端正常关闭，
		// 统一翻译成 io.EOF，让上层只需判断一种错误。
		if websocket.CloseStatus(err) == websocket.StatusNormalClosure ||
			websocket.CloseStatus(err) == websocket.StatusGoingAway ||
			errors.Is(err, io.EOF) {
			connLog.Debug("ws closed by peer", "dev", c.devID)
			return protocol.Frame{}, io.EOF
		}
		connLog.Error("ws read failed", "dev", c.devID, "err", err)
		return protocol.Frame{}, err
	}

	// data = 4 字节长度前缀 + body；长度前缀始终明文（Send 侧设计），
	// body 可能已加密。剥前缀 → 解密 → 按明文长度重建前缀 →
	// 交给 DecodeFrame（它期望的是「长度前缀 + body」完整帧）。
	if len(data) < 4 {
		return protocol.Frame{}, io.ErrUnexpectedEOF
	}
	body, err := c.openBody(data[4:])
	if err != nil {
		connLog.Error("open frame failed", "len", len(data), "err", err)
		return protocol.Frame{}, err
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(body)))
	frame, err := protocol.DecodeFrame(io.MultiReader(bytes.NewReader(lenBuf[:]), bytes.NewReader(body)))
	if err != nil {
		connLog.Error("decode frame failed", "len", len(data), "err", err)
		return frame, err
	}
	connLog.Debug("recv frame", "kind", frame.Kind, "len", len(data), "dev", c.devID)
	return frame, nil
}

// Close 关闭连接。幂等。
//
// 用 StatusNormalClosure 告知对端这是优雅关闭而非异常，
// 这样对端的 Recv 会收到 io.EOF 而不是传输层错误——上层据此区分"走了"和"出错了"。
//
// 注意 coder/websocket 的 Close 不接受 ctx：它内部处理关闭握手超时，
// 并会唤醒所有阻塞在该连接上的 goroutine，因此这里不需要（也无法）自己设超时。
func (c *conn) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.ws.Close(websocket.StatusNormalClosure, "bye")
		close(c.closedCh)
	})
	return c.closeErr
}

// Closed 返回连接关闭信号。
func (c *conn) Closed() <-chan struct{} { return c.closedCh }

// LocalDeviceID 实现 core.Conn。
//
// 命名是历史遗留：core.Conn 是客户端视角，所以叫 Local。
// 在 Hub 侧这个值是 accept 时预填的临时 ID，权威身份以 FKHello 为准。
func (c *conn) LocalDeviceID() string { return c.devID }

// DeviceID 实现 hubstate.Peer。语义与 LocalDeviceID 相同。
func (c *conn) DeviceID() string { return c.devID }
