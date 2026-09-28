package transfer

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net"
	"testing"
)

// encPair 在回环 TCP 上建立一对 encConn，返回 client/server 及清理函数。
func encPair(t *testing.T, serverKey, clientKey []byte) (*encConn, *encConn) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	type res struct {
		c   *encConn
		err error
	}
	srvCh := make(chan res, 1)
	go func() {
		raw, aerr := ln.Accept()
		if aerr != nil {
			srvCh <- res{err: aerr}
			return
		}
		c, cerr := newEncConn(raw, serverKey)
		srvCh <- res{c: c, err: cerr}
	}()

	raw, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	client, err := newEncConn(raw, clientKey)
	if err != nil {
		t.Fatalf("client enc: %v", err)
	}
	s := <-srvCh
	if s.err != nil {
		t.Fatalf("server: %v", s.err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = s.c.Close()
	})
	return client, s.c
}

// TestEncStreamRoundTrip 验证跨多个 block 的明文可正确还原。
func TestEncStreamRoundTrip(t *testing.T) {
	key, _ := generateFileKey()
	client, server := encPair(t, key, key)

	// 约 3.x 个 block，保证产生多条记录且末块不满。
	payload := make([]byte, EncBlockSize*3+1234)
	if _, err := rand.Read(payload); err != nil {
		t.Fatalf("rand: %v", err)
	}

	copyDone := make(chan error, 1)
	go func() {
		_, err := client.Write(payload)
		copyDone <- err
		_ = client.CloseWrite()
	}()

	got, err := io.ReadAll(server)
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("解密出的明文与原文不一致")
	}
	if err := <-copyDone; err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestEncStreamFlushSmall 验证不足一个 block 的数据在 Flush 后也能被读到。
func TestEncStreamFlushSmall(t *testing.T) {
	key, _ := generateFileKey()
	client, server := encPair(t, key, key)

	msg := []byte("small message")
	if _, err := client.Write(msg); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := client.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(server, buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(buf, msg) {
		t.Fatal("小数据 flush 后还原不一致")
	}
}

// TestEncStreamWrongKey 验证密钥不一致时解密失败。
func TestEncStreamWrongKey(t *testing.T) {
	serverKey, _ := generateFileKey()
	clientKey, _ := generateFileKey()
	client, server := encPair(t, serverKey, clientKey)

	if _, err := client.Write([]byte("secret")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := client.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}
	buf := make([]byte, 6)
	if _, err := io.ReadFull(server, buf); err == nil {
		t.Fatal("密钥不一致时解密应当失败")
	}
}

// TestEncCiphertextHidesPlaintext 验证在线路上抓不到明文特征。
func TestEncCiphertextHidesPlaintext(t *testing.T) {
	key, _ := generateFileKey()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	// 嗅探端：Accept 后把对端字节读入 capture，再连一个真正的解密端不现实，
	// 因此这里直接作为接收方捕获原始字节并断言不含明文。
	rawCh := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept()
		rawCh <- c
	}()

	dialRaw, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	client, err := newEncConn(dialRaw, key)
	if err != nil {
		t.Fatalf("enc: %v", err)
	}
	secret := []byte("PLAINTEXT-MARKER-aaaaaaaaaaaaaaaa")
	if _, err := client.Write(secret); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := client.Flush(); err != nil {
		t.Fatalf("flush: %v", err)
	}

	raw := <-rawCh
	// 先读 4 字节长度前缀，再按长度读一条记录，无需等待连接 EOF。
	var lbuf [4]byte
	if _, err := io.ReadFull(raw, lbuf[:]); err != nil {
		t.Fatalf("read len: %v", err)
	}
	n := int(binary.BigEndian.Uint32(lbuf[:]))
	capture := make([]byte, n)
	if _, err := io.ReadFull(raw, capture); err != nil {
		t.Fatalf("read record: %v", err)
	}
	if bytes.Contains(capture, secret) {
		t.Fatal("中继线路上出现了明文特征")
	}
}
