package core

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
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
	if !strings.HasPrefix(path, "data:") {
		c.registerCAS(sum, path)
	}

	// M17: 当发送 < 256 KiB 的图片或 data:image/... 剪贴板图片时，同步发送带缩略图的 STICKER_MSG 内联预览。
	c.maybeSendInlineThumbnail(conn, to, "", info, sum)

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
	if g, ok := c.getGroupTask(transferID); ok {
		if g.source {
			return errInvalidState
		}
		if !accept {
			c.removeGroupTask(transferID)
			g.teardown()
			return nil
		}
		if dest == "" {
			return errEmptyPath
		}
		return c.respondGroupFile(g, dest)
	}
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

	// M24: 内容寻址秒传去重（CAS Instant Transfer）：若本地已有同 SHA-256 且校验通过的文件，
	// 直接本地复制并通知发送方 FILE_DONE，0 字节瞬时完成。
	if t.sha256 != "" && c.tryCASInstantCopy(t.sha256, t.snap.Size, destPath) {
		_, _, _ = maybeUnpackDirectoryBundle(destPath)
		_, _ = c.sendTo(conn, protocol.FileDone, t.snap.PeerID, protocol.FileDonePayload{
			TransferID: transferID,
			SHA256:     t.sha256,
		})
		cp := t.setProgress(t.snap.Size, 0)
		c.listener().OnTransferProgress(cp)
		t.done()
		return nil
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

// registerCAS 记录本地已校验文件的 SHA-256 索引（M24）。
func (c *Client) registerCAS(sha256Hex, localPath string) {
	if sha256Hex == "" || localPath == "" {
		return
	}
	c.mu.Lock()
	if c.casIndex == nil {
		c.casIndex = make(map[string]string)
	}
	c.casIndex[strings.ToLower(sha256Hex)] = localPath
	c.mu.Unlock()
}

// tryCASInstantCopy 检查本地 CAS 索引或下载目录是否已存在同哈希文件，若命中则原子复制到 destPath（M24）。
func (c *Client) tryCASInstantCopy(sha256Hex string, wantSize int64, destPath string) bool {
	targetHash := strings.ToLower(strings.TrimSpace(sha256Hex))
	if targetHash == "" || destPath == "" || wantSize < 0 {
		return false
	}
	c.mu.Lock()
	indexed := c.casIndex[targetHash]
	dlDir := c.downloadDir
	c.mu.Unlock()

	candidates := make([]string, 0, 4)
	if indexed != "" && filepath.Clean(indexed) != filepath.Clean(destPath) {
		candidates = append(candidates, indexed)
	}
	if dlDir != "" {
		if entries, err := os.ReadDir(dlDir); err == nil {
			for _, e := range entries {
				if e.IsDir() || strings.HasSuffix(e.Name(), ".part") || strings.HasSuffix(e.Name(), ".meta") {
					continue
				}
				p := filepath.Join(dlDir, e.Name())
				if filepath.Clean(p) == filepath.Clean(destPath) {
					continue
				}
				if st, err := e.Info(); err == nil && st.Size() == wantSize {
					candidates = append(candidates, p)
				}
			}
		}
	}

	for _, srcPath := range candidates {
		f, err := os.Open(srcPath)
		if err != nil {
			continue
		}
		st, err := f.Stat()
		if err != nil || st.Size() != wantSize {
			_ = f.Close()
			continue
		}
		sum, err := hashFile(f, wantSize)
		if err != nil || !strings.EqualFold(sum, targetHash) {
			_ = f.Close()
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			_ = f.Close()
			continue
		}
		tmpPath := destPath + ".cas.tmp"
		out, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			_ = f.Close()
			continue
		}
		_, copyErr := io.Copy(out, f)
		_ = out.Close()
		_ = f.Close()
		if copyErr != nil {
			_ = os.Remove(tmpPath)
			continue
		}
		if err := os.Rename(tmpPath, destPath); err != nil {
			_ = os.Remove(tmpPath)
			continue
		}
		c.registerCAS(targetHash, destPath)
		return true
	}
	return false
}

// progressFn 返回适配 transfer 包的进度回调，更新任务并下发（暂停时阻塞数据流直至恢复或取消）。
func (c *Client) progressFn(t *transferTask) func(int64, int64) {
	return func(bytesDone, speedBps int64) {
		t.waitIfPaused()
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
