// 本文件实现经中继的数据路径：两端各自拨号到中继数据面 TCP，先以 relayID
// 明文行完成中继握手（仅为配对标识，不含密钥），再在连接上叠加 AES-GCM
// 加密层。之后的数据面（LCTP 握手 / DATA / ACK / FIN）全部为密文。
//
// 文件密钥 K_file 在两端独立持有（经信令上的端到端 X25519 信封协商），
// 中继永远拿不到，因此无法解密。
package transfer

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"
)

// relayDialTimeout 是到中继数据面的拨号上限。
const relayDialTimeout = 5 * time.Second

// RelayConfig 描述一次经中继的传输参数（发送/接收共用）。
type RelayConfig struct {
	RelayAddr  string // 中继数据面 TCP 地址 host:port
	RelayID    string // 配对标识
	FileKey    []byte // 端到端文件密钥（32B）
	TransferID string
	Token      string

	// 文件级参数（直接映射到 SendConfig / ReceiveConfig）。
	Name   string
	Size   int64
	SHA256 string
}

// relayConnect 拨号中继、发送 relayID 握手行，再叠加加密层。
func relayConnect(ctx context.Context, rc RelayConfig) (*encConn, error) {
	if rc.RelayAddr == "" || rc.RelayID == "" || len(rc.FileKey) == 0 {
		return nil, fmt.Errorf("transfer: relay addr/id/key required")
	}
	dialer := net.Dialer{Timeout: relayDialTimeout}
	raw, err := dialer.DialContext(ctx, "tcp", rc.RelayAddr)
	if err != nil {
		return nil, fmt.Errorf("transfer: dial relay: %w", err)
	}

	// 中继握手：一行 relayID，仅用于配对。
	if _, err := fmt.Fprintf(raw, "%s\n", rc.RelayID); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("transfer: relay handshake: %w", err)
	}

	enc, err := newEncConn(raw, rc.FileKey)
	if err != nil {
		_ = raw.Close()
		return nil, err
	}
	return enc, nil
}

// RelaySend 经中继发送文件：发送方与中继建立加密连接并在其上完成 LCTP 发送。
func RelaySend(ctx context.Context, rc RelayConfig, file io.ReaderAt, progress func(bytesDone, speedBps int64)) error {
	enc, err := relayConnect(ctx, rc)
	if err != nil {
		return err
	}
	defer enc.Close()

	cfg := SendConfig{
		TransferID: rc.TransferID,
		Name:       rc.Name,
		Size:       rc.Size,
		SHA256:     rc.SHA256,
		Token:      rc.Token,
	}
	return serveSendConnFile(ctx, cfg, enc, file, progress)
}

// RelayReceive 经中继接收文件：接收方与中继建立加密连接并在其上完成 LCTP 接收。
func RelayReceive(ctx context.Context, rc RelayConfig, destPath string, progress func(bytesDone, speedBps int64)) error {
	enc, err := relayConnect(ctx, rc)
	if err != nil {
		return err
	}
	defer enc.Close()

	cfg := ReceiveConfig{
		TransferID: rc.TransferID,
		Token:      rc.Token,
		DestPath:   destPath,
		Size:       rc.Size,
		SHA256:     rc.SHA256,
	}
	return receiveOnConn(ctx, cfg, enc, progress)
}
