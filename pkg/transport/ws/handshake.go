package ws

// handshake.go 是 client↔hub 传输加密的握手逻辑（wire v2）。
//
// 流程（连接建立后、业务 Hello 前）：
//
//	Client                               Hub
//	  |----- FKHandshake{C: ephPub} ----->|  ECDH(hubPriv, ephPub) → key
//	  |<---- FKHandshakeAck{H,N,Cipher} --|  Cipher = AEAD(key, challenge)
//	  | ECDH(ephPriv, hubPub) → 同 key    |
//	  | 解密挑战 → 校验 TOFU(hubPub)      |
//	  |----- 后续全部帧 AES-256-GCM ----->|
//
// 信任模型：客户端 TOFU hub 公钥（首连信任、变化拒绝）；hub 无需
// 校验客户端身份（局域网免鉴权，ADR-014；加密只防嗅探不防冒充）。

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/pandaymx/lanchat/pkg/protocol"
	"github.com/pandaymx/lanchat/pkg/secure"
)

// handshakeTimeout 是握手帧等待超时（首帧应在连接后立刻发出）。
const handshakeTimeout = 10 * time.Second

// trustFunc 校验对端公钥：返回 nil 表示信任（首次/一致），
// 返回错误则中止握手并关闭连接。
type trustFunc func(pub []byte) error

// clientHandshake 是客户端侧握手：发临时公钥 → 收 hub 公钥+挑战 →
// 派生会话密钥、校验挑战与 TOFU → 启用连接加密。返回会话 AAD。
func clientHandshake(ctx context.Context, c *conn, trust trustFunc) error {
	eph, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("ws: handshake keygen: %w", err)
	}
	hs := protocol.Handshake{
		ProtocolVersion: protocol.ProtocolVersion,
		ClientKey:       base64.StdEncoding.EncodeToString(eph.PublicKey().Bytes()),
	}
	payload, _ := json.Marshal(hs)
	// 明文发出握手帧（此刻连接尚未加密）。
	if err := c.sendPlain(ctx, protocol.Frame{Kind: protocol.FKHandshake, Payload: payload}); err != nil {
		return fmt.Errorf("ws: send handshake: %w", err)
	}

	hctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	ack, err := c.recvPlain(hctx)
	if err != nil {
		return fmt.Errorf("ws: handshake ack: %w", err)
	}
	if ack.Kind != protocol.FKHandshakeAck {
		return fmt.Errorf("ws: unexpected handshake response %s", ack.Kind)
	}
	var ha protocol.HandshakeAck
	if err := json.Unmarshal(ack.Payload, &ha); err != nil {
		return fmt.Errorf("ws: handshake ack decode: %w", err)
	}
	hubPub, err := base64.StdEncoding.DecodeString(ha.HubKey)
	if err != nil || len(hubPub) != 32 {
		return errors.New("ws: invalid hub public key in handshake ack")
	}
	nonce, err := base64.StdEncoding.DecodeString(ha.Nonce)
	if err != nil {
		return fmt.Errorf("ws: handshake nonce: %w", err)
	}
	ciphertext, err := base64.StdEncoding.DecodeString(ha.Cipher)
	if err != nil {
		return fmt.Errorf("ws: handshake cipher: %w", err)
	}

	// 派生会话密钥并验证挑战（证明 hub 持有持久私钥）。
	sess, err := secure.NewSession(eph, hubPub)
	if err != nil {
		return err
	}
	if err := sess.VerifyChallenge(nonce, ciphertext); err != nil {
		return fmt.Errorf("ws: hub challenge failed: %w", err)
	}
	// TOFU 校验 hub 公钥（首连信任并持久化，变化拒绝）。
	if trust != nil {
		if err := trust(hubPub); err != nil {
			return fmt.Errorf("ws: hub trust: %w", err)
		}
	}
	c.EnableEncryption(sess.Key(), sess.AAD())
	return nil
}

// serverHandshake 是 hub 侧握手：收临时公钥 → 派生会话密钥 →
// 回 hub 公钥+挑战 → 启用连接加密。
func serverHandshake(ctx context.Context, c *conn, hubPriv *ecdh.PrivateKey) error {
	hctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	hs, err := c.recvPlain(hctx)
	if err != nil {
		return fmt.Errorf("ws: handshake: %w", err)
	}
	if hs.Kind != protocol.FKHandshake {
		return fmt.Errorf("ws: first frame is %s, want handshake (protocol v%d required)", hs.Kind, protocol.ProtocolVersion)
	}
	var req protocol.Handshake
	if err := json.Unmarshal(hs.Payload, &req); err != nil {
		return fmt.Errorf("ws: handshake decode: %w", err)
	}
	clientPub, err := base64.StdEncoding.DecodeString(req.ClientKey)
	if err != nil || len(clientPub) != 32 {
		return errors.New("ws: invalid client public key")
	}
	if req.ProtocolVersion != protocol.ProtocolVersion {
		return fmt.Errorf("ws: protocol version %d, want %d", req.ProtocolVersion, protocol.ProtocolVersion)
	}

	sess, err := secure.NewSession(hubPriv, clientPub)
	if err != nil {
		return err
	}
	nonce, ciphertext, err := sess.SealChallenge()
	if err != nil {
		return err
	}
	ack := protocol.HandshakeAck{
		HubKey: base64.StdEncoding.EncodeToString(hubPriv.PublicKey().Bytes()),
		Nonce:  base64.StdEncoding.EncodeToString(nonce),
		Cipher: base64.StdEncoding.EncodeToString(ciphertext),
	}
	payload, _ := json.Marshal(ack)
	if err := c.sendPlain(ctx, protocol.Frame{Kind: protocol.FKHandshakeAck, Payload: payload}); err != nil {
		return fmt.Errorf("ws: send handshake ack: %w", err)
	}
	c.EnableEncryption(sess.Key(), sess.AAD())
	return nil
}

// ---- 明文收发（握手期专用） ----
// 直接复用 conn.Send/Recv 即可：此时 enc 仍为 nil，走明文路径。

func (c *conn) sendPlain(ctx context.Context, f protocol.Frame) error { return c.Send(ctx, f) }

func (c *conn) recvPlain(ctx context.Context) (protocol.Frame, error) { return c.Recv(ctx) }
