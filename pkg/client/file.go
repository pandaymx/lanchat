package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/protocol"
)

// ErrFileUnconfigured 表示未调用 SetFileBase 就使用文件 API（M9）。
var ErrFileUnconfigured = errors.New("client: file base not configured")

// HTTPBaseFromWS 把 ws:// 形式的 hub 地址换算成 HTTP 基址。
//
// M9 文件传输的数据面（POST/GET /api/files）挂在 hub 的 HTTP 端口上，
// 与 WebSocket 同监听、同端口——客户端手头只有 ws URL 时用它推导
// http://host:port，不用再记第二个地址。
func HTTPBaseFromWS(wsURL string) string {
	// 裸地址（"127.0.0.1:9000"）补 ws://：与 ws.Dial 的 normalizeTarget
	// 同一约定，DialOptions.HubURL 允许裸地址。
	if !strings.Contains(wsURL, "://") {
		wsURL = "ws://" + wsURL
	}
	u, err := url.Parse(wsURL)
	if err != nil {
		return ""
	}
	switch u.Scheme {
	case "wss":
		u.Scheme = "https"
	default:
		u.Scheme = "http"
	}
	u.Path = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// SetFileBase 配置 hub 的 HTTP 基址（文件传输数据面）。
// 不设置时 UploadFile / DownloadFile 返回 ErrFileUnconfigured。
func (c *Client) SetFileBase(base string) {
	c.fileBase = base
}

// UploadFile 上传本地文件并发送一条带附件引用的消息（TUI /file 命令路径）。
//
// mime 为空时按扩展名推断，推断不出回落 application/octet-stream。
// 上传成功即发消息；消息经 hub 广播回环，事件流里会出现自己的文件消息。
func (c *Client) UploadFile(ctx context.Context, convID, path, mime string) error {
	if c.closed.Load() {
		return core.ErrClosed
	}
	if c.fileBase == "" {
		return ErrFileUnconfigured
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if st.IsDir() {
		return fmt.Errorf("open %s: is a directory", path)
	}
	if mime == "" {
		mime = detectMime(path)
	}
	ref, err := c.upload(ctx, filepath.Base(path), mime, f)
	if err != nil {
		return err
	}
	cliLog.Info("file uploaded", "file", ref.FileID, "name", ref.Name, "size", ref.Size)
	return c.SendFileMessage(ctx, convID, ref)
}

// SendFileMessage 发送一条携带文件附件引用的消息（Web 端代理上传后路径；
// body 留空，文件卡片由两端 UI 渲染）。
func (c *Client) SendFileMessage(ctx context.Context, convID string, ref protocol.FileRef) error {
	if c.closed.Load() {
		return core.ErrClosed
	}
	nonce := newNonce()
	msg := protocol.StoredMessage{
		ID:             "local-" + nonce,
		ClientNonce:    nonce,
		ConversationID: convID,
		SenderUserID:   c.hello.UserID,
		SenderDeviceID: c.hello.DeviceID,
		CreatedAt:      time.Now().UnixMilli(),
		File:           &ref,
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal file message: %w", err)
	}
	cliLog.Debug("send FKMessage with file", "conv", convID, "file", ref.FileID, "name", ref.Name)
	if err := c.conn.Send(ctx, protocol.Frame{Kind: protocol.FKMessage, Payload: payload}); err != nil {
		cliLog.Error("send file message failed", "conv", convID, "err", err)
		return err
	}
	_ = c.store.AppendMessage(ctx, msg)
	return nil
}

// DownloadFile 把 hub 上的文件保存到 dest（父目录自动创建；已存在覆盖）。
func (c *Client) DownloadFile(ctx context.Context, fileID, dest string) error {
	if c.closed.Load() {
		return core.ErrClosed
	}
	if c.fileBase == "" {
		return ErrFileUnconfigured
	}
	if fileID == "" {
		return errors.New("client: empty file id")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.fileBase+"/api/files/"+fileID, nil)
	if err != nil {
		return fmt.Errorf("build download request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", fileID, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return core.ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", fileID, resp.Status)
	}
	if dir := filepath.Dir(dest); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	out, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("create %s: %w", dest, err)
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		_ = out.Close()
		_ = os.Remove(dest)
		return fmt.Errorf("save %s: %w", dest, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close %s: %w", dest, err)
	}
	cliLog.Info("file downloaded", "file", fileID, "dest", dest)
	return nil
}

// upload 内部实现：流式 multipart POST（不整文件缓冲，大文件内存友好）。
func (c *Client) upload(ctx context.Context, name, mimeType string, r io.Reader) (protocol.FileRef, error) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, name))
		if mimeType != "" {
			h.Set("Content-Type", mimeType)
		}
		fw, err := mw.CreatePart(h)
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		if _, err := io.Copy(fw, r); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		if err := mw.Close(); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		_ = pw.Close()
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.fileBase+"/api/files", pr)
	if err != nil {
		_ = pw.Close()
		return protocol.FileRef{}, fmt.Errorf("build upload request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return protocol.FileRef{}, fmt.Errorf("upload %s: %w", name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return protocol.FileRef{}, fmt.Errorf("upload %s: %s (%s)", name, resp.Status, string(body))
	}
	var out struct {
		FileID string `json:"fid"`
		Name   string `json:"n"`
		Size   int64  `json:"sz"`
		Mime   string `json:"m"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return protocol.FileRef{}, fmt.Errorf("upload %s: decode response: %w", name, err)
	}
	if out.FileID == "" {
		return protocol.FileRef{}, errors.New("upload: empty file id in response")
	}
	return protocol.FileRef{FileID: out.FileID, Name: out.Name, Size: out.Size, Mime: out.Mime}, nil
}

// detectMime 按扩展名推断 MIME；无匹配回落 octet-stream。
func detectMime(path string) string {
	if m := mime.TypeByExtension(filepath.Ext(path)); m != "" {
		return m
	}
	return "application/octet-stream"
}
