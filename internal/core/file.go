package core

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"

	"github.com/google/uuid"

	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/protocol"
	"github.com/pandaymx/lanchat/internal/transfer"
)

// OfferFile 发起 1:1 文件发送：打开文件 → 计算 SHA-256 → 监听 P2P → FILE_OFFER。
func (c *Client) OfferFile(to, path string) (string, error) {
	if to == "" {
		return "", errPeerNotFound
	}
	conn, err := c.connectedConn()
	if err != nil {
		return "", err
	}

	info, err := openFileInfo(path)
	if err != nil {
		return "", err
	}

	sum, err := hashFile(info.file, info.size)
	if err != nil {
		_ = info.file.Close()
		return "", err
	}

	id := uuid.NewString()
	t, err := newTransferTask(c, id, appapi.TransferOutbound, to, info.name, info.size)
	if err != nil {
		_ = info.file.Close()
		return "", err
	}
	if !c.addTransfer(t) {
		_ = info.file.Close()
		return "", errInvalidState
	}

	priv, err := transfer.GenerateX25519()
	if err != nil {
		_ = info.file.Close()
		c.removeTransfer(id)
		return "", err
	}
	t.file = info.file
	t.senderPriv = priv
	t.sha256 = sum

	// 立即发进度（pending）让 UI 建立任务行。
	c.listener().OnTransferProgress(t.snapshot())

	go c.runOutbound(t, conn)
	return id, nil
}

// hashFile 计算全文件 SHA-256（hex）。
func hashFile(f io.ReadSeeker, size int64) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// RespondFile 应答入站邀请：接受则开始接收路径，拒绝则回 FILE_REJECT。
func (c *Client) RespondFile(transferID string, accept bool, dest string) error {
	t, ok := c.getTransfer(transferID)
	if !ok {
		return errTransferGone
	}
	if t.snap.Direction != appapi.TransferInbound {
		return errInvalidState
	}
	if t.snap.State != appapi.TransferPending {
		return errInvalidState
	}

	conn, err := c.connectedConn()
	if err != nil {
		return err
	}

	if !accept {
		t.setState(appapi.TransferCanceled)
		if _, err := c.sendTo(conn, protocol.FileReject, t.snap.PeerID, protocol.FileRejectPayload{
			TransferID: transferID,
		}); err != nil {
			return err
		}
		c.removeTransfer(transferID)
		return nil
	}

	destPath := dest
	if destPath == "" {
		return errEmptyPath
	}

	if _, err := c.sendTo(conn, protocol.FileAccept, t.snap.PeerID, protocol.FileAcceptPayload{
		TransferID: transferID,
	}); err != nil {
		return err
	}
	t.setState(appapi.TransferActive)
	go c.runInbound(t, conn, destPath)
	return nil
}

// progressFn 返回适配 transfer 包的进度回调，更新任务并下发。
func (c *Client) progressFn(t *transferTask) func(int64, int64) {
	return func(bytesDone, speedBps int64) {
		cp := t.setProgress(bytesDone, speedBps)
		c.listener().OnTransferProgress(cp)
	}
}

// offerInbound 收泵收到 FILE_OFFER 时建立入站 pending 任务并提示 UI。
func (c *Client) offerInbound(env *protocol.Envelope) {
	var p protocol.FileOfferPayload
	if err := env.DecodePayload(&p); err != nil {
		return
	}
	if strings.TrimSpace(p.TransferID) == "" {
		return
	}
	t, err := newTransferTask(c, p.TransferID, appapi.TransferInbound, env.From, p.Name, p.Size)
	if err != nil {
		return
	}
	t.token = p.Token
	t.candidates = p.Candidates
	t.senderECDH = p.ECDHPub
	t.sha256 = p.SHA256
	if !c.addTransfer(t) {
		return
	}
	// pending 进度事件让 UI 弹出「接收 / 另存为」。
	c.listener().OnTransferProgress(t.snapshot())
}

// errString 提取错误短原因。
func errString(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, transfer.ErrDialFailed) {
		return "direct connection failed"
	}
	return err.Error()
}
