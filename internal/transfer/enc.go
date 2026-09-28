// 本文件在中继 TCP 连接之上提供透明的 AES-GCM 加密流：把字节流切成
// 固定大小的明文 block，逐块加密为 length-prefixed 的密文记录。
//
// 线路格式（每条记录）：
//
//	uint32 BE 长度 N || N 字节密封数据
//
// 其中密封数据 = nonce(12B) || ciphertext || gcm tag。nonce 由 8 字节
// 固定前缀 + 4 字节递增计数器构成，保证同一密钥、同一方向内 nonce 唯一。
// 中继只看到这些记录，无法解密。
package transfer

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// EncBlockSize 是单个明文 block 的目标大小。
const EncBlockSize = 32 * 1024

// lenPrefixSize 是每条密文记录的长度前缀字节数。
const lenPrefixSize = 4

// encConn 用 AES-GCM 透明加密一条底层连接。
type encConn struct {
	raw  net.Conn
	aead interface {
		NonceSize() int
		Seal(dst, nonce, plaintext, additionalData []byte) []byte
		Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error)
	}

	readMu sync.Mutex
	// 读侧：先从 raw 读出一整条密封记录，解密后放在 plain，供 Read 返回。
	plain []byte

	writeMu   sync.Mutex
	buf       []byte // 待加密的明文累积缓冲
	noncePref []byte // 8 字节固定 nonce 前缀
	counter   uint32
	closed    bool
}

// newEncConn 用文件密钥在 raw 上构造加密连接。
func newEncConn(raw net.Conn, fileKey []byte) (*encConn, error) {
	gcm, err := newGCM(fileKey)
	if err != nil {
		return nil, err
	}
	prefix := make([]byte, 8) // 固定前缀取 8 字节 0；计数器提供方向内唯一性
	return &encConn{
		raw:       raw,
		aead:      gcm,
		buf:       make([]byte, 0, EncBlockSize),
		noncePref: prefix,
	}, nil
}

// Read 解密记录并返回明文，实现 io.Reader。
func (e *encConn) Read(p []byte) (int, error) {
	if len(e.plain) > 0 {
		n := copy(p, e.plain)
		e.plain = e.plain[n:]
		return n, nil
	}

	e.readMu.Lock()
	defer e.readMu.Unlock()

	var lengthBuf [lenPrefixSize]byte
	if _, err := io.ReadFull(e.raw, lengthBuf[:]); err != nil {
		return 0, err
	}
	n := binary.BigEndian.Uint32(lengthBuf[:])
	if n == 0 {
		return 0, errors.New("transfer: invalid empty encrypted record")
	}
	record := make([]byte, n)
	if _, err := io.ReadFull(e.raw, record); err != nil {
		return 0, err
	}

	ns := e.aead.NonceSize()
	if int(n) < ns {
		return 0, errors.New("transfer: encrypted record too short")
	}
	nonce, ciphertext := record[:ns], record[ns:]
	plain, err := e.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return 0, fmt.Errorf("transfer: decrypt block: %w", err)
	}

	copied := copy(p, plain)
	e.plain = plain[copied:]
	return copied, nil
}

// Write 累积明文并按 EncBlockSize 刷出加密记录，实现 io.Writer。
func (e *encConn) Write(p []byte) (int, error) {
	e.writeMu.Lock()
	defer e.writeMu.Unlock()
	if e.closed {
		return 0, net.ErrClosed
	}

	total := len(p)
	for len(p) > 0 {
		room := EncBlockSize - len(e.buf)
		take := min(room, len(p))
		e.buf = append(e.buf, p[:take]...)
		p = p[take:]
		if len(e.buf) == EncBlockSize {
			if err := e.emitLocked(); err != nil {
				return 0, err
			}
		}
	}
	// 每次 Write 结束即 flush 残留：LCTP 握手帧（HELLO/ACCEPT/FIN）远小于
	// block，若攒着不发会让对端永久等待而死锁。数据帧本身为 MiB 级，
	// 拆成多条记录不影响吞吐。
	if err := e.emitLocked(); err != nil {
		return 0, err
	}
	return total, nil
}

// emitLocked 加密当前缓冲并作为一条记录写出，然后清空缓冲、推进计数器。
func (e *encConn) emitLocked() error {
	if len(e.buf) == 0 {
		return nil
	}
	nonce := e.nextNonce()
	// Seal 用 nil 目标只返回 ciphertext||tag，再手动前置 nonce，
	// 与读取侧「前 12 字节 nonce、其余密文」的切分保持一致。
	ciphertext := e.aead.Seal(nil, nonce, e.buf, nil)
	sealed := make([]byte, 0, len(nonce)+len(ciphertext))
	sealed = append(sealed, nonce...)
	sealed = append(sealed, ciphertext...)

	var prefix [lenPrefixSize]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(sealed)))
	if _, err := e.raw.Write(prefix[:]); err != nil {
		return err
	}
	if _, err := e.raw.Write(sealed); err != nil {
		return err
	}
	e.buf = e.buf[:0]
	return nil
}

// nextNonce 生成 8 字节前缀 + 4 字节计数器的 12 字节 nonce。
func (e *encConn) nextNonce() []byte {
	nonce := make([]byte, 12)
	copy(nonce[:8], e.noncePref)
	binary.BigEndian.PutUint32(nonce[8:], e.counter)
	e.counter++
	return nonce
}

// Flush 把不足一个 block 的残留明文加密发出。
func (e *encConn) Flush() error {
	e.writeMu.Lock()
	defer e.writeMu.Unlock()
	return e.emitLocked()
}

// Close 先 flush 残留明文，再关闭底层连接。
func (e *encConn) Close() error {
	e.writeMu.Lock()
	e.closed = true
	flushErr := e.emitLocked()
	e.writeMu.Unlock()
	closeErr := e.raw.Close()
	if flushErr != nil {
		return flushErr
	}
	return closeErr
}

// CloseWrite flush 残留明文后对底层连接做半关闭（若支持）。
func (e *encConn) CloseWrite() error {
	if err := e.Flush(); err != nil {
		return err
	}
	if cw, ok := e.raw.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}

// SetDeadline 等透传到底层连接，供上层 framer 使用。
func (e *encConn) SetDeadline(t time.Time) error      { return e.raw.SetDeadline(t) }
func (e *encConn) SetReadDeadline(t time.Time) error  { return e.raw.SetReadDeadline(t) }
func (e *encConn) SetWriteDeadline(t time.Time) error { return e.raw.SetWriteDeadline(t) }

// LocalAddr / RemoteAddr 透传。
func (e *encConn) LocalAddr() net.Addr  { return e.raw.LocalAddr() }
func (e *encConn) RemoteAddr() net.Addr { return e.raw.RemoteAddr() }
