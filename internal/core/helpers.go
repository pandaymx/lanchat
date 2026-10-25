package core

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/coder/websocket"

	"github.com/pandaymx/lanchat/internal/appapi"
	"github.com/pandaymx/lanchat/internal/protocol"
)

// defaultNickname 在未提供昵称时生成「平台-短随机串」，避免多端重名。
func defaultNickname() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s-%s", runtime.GOOS, hex.EncodeToString(b))
}

// fileInfo 收集 OfferFile 所需的文件元信息。
type fileInfo struct {
	file *os.File
	name string
	size int64
}

func openFileInfo(path string) (*fileInfo, error) {
	if path == "" {
		return nil, errEmptyPath
	}
	if strings.HasPrefix(path, "data:image/") {
		return openClipboardDataURI(path)
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errFileNotFound
		}
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if st.IsDir() {
		_ = f.Close()
		return packDirectoryToZip(path)
	}
	return &fileInfo{file: f, name: st.Name(), size: st.Size()}, nil
}

// packDirectoryToZip 将非空目录递归打包为临时 .zip 归档供文件传输管道发送（M11b）。
func packDirectoryToZip(dirPath string) (*fileInfo, error) {
	var files []string
	err := filepath.Walk(dirPath, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.Mode().IsRegular() {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("core: %s is an empty directory", dirPath)
	}

	tmp, err := os.CreateTemp("", "lanchat-dir-*.zip")
	if err != nil {
		return nil, err
	}
	zw := zip.NewWriter(tmp)
	for _, fullPath := range files {
		rel, err := filepath.Rel(dirPath, fullPath)
		if err != nil {
			_ = zw.Close()
			_ = tmp.Close()
			return nil, err
		}
		fw, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			_ = zw.Close()
			_ = tmp.Close()
			return nil, err
		}
		src, err := os.Open(fullPath)
		if err != nil {
			_ = zw.Close()
			_ = tmp.Close()
			return nil, err
		}
		_, copyErr := io.Copy(fw, src)
		_ = src.Close()
		if copyErr != nil {
			_ = zw.Close()
			_ = tmp.Close()
			return nil, copyErr
		}
	}
	if err := zw.Close(); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	st, err := tmp.Stat()
	if err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	base := filepath.Base(filepath.Clean(dirPath))
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "archive"
	}
	return &fileInfo{file: tmp, name: base + ".zip", size: st.Size()}, nil
}

// openClipboardDataURI 将剪贴板内联 `data:image/<ext>;base64,...` 解码为临时图片文件（M11b）。
func openClipboardDataURI(uri string) (*fileInfo, error) {
	comma := strings.IndexByte(uri, ',')
	if comma <= 0 {
		return nil, errors.New("core: invalid clipboard data URI")
	}
	header := uri[:comma]
	b64 := strings.TrimSpace(uri[comma+1:])
	if !strings.HasSuffix(header, ";base64") || b64 == "" {
		return nil, errors.New("core: clipboard data URI must be base64 encoded")
	}
	mime := strings.TrimPrefix(strings.TrimSuffix(header, ";base64"), "data:image/")
	ext := "png"
	switch strings.ToLower(mime) {
	case "jpeg", "jpg":
		ext = "jpg"
	case "gif":
		ext = "gif"
	case "webp":
		ext = "webp"
	case "bmp":
		ext = "bmp"
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) == 0 {
		return nil, errors.New("core: invalid base64 payload in clipboard data URI")
	}
	tmp, err := os.CreateTemp("", "lanchat-clipboard-*."+ext)
	if err != nil {
		return nil, err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	return &fileInfo{
		file: tmp,
		name: "clipboard." + ext,
		size: int64(len(raw)),
	}, nil
}

const maxInlineImageRawBytes = 256 << 10

func imageMimeByExt(name string) string {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
	switch ext {
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	case "bmp":
		return "image/bmp"
	default:
		return ""
	}
}

// buildInlineStickerPayload 为 < 256 KiB 的图片构造 STICKER_MSG 内联缩略图负载（M17）。
func buildInlineStickerPayload(info *fileInfo, sha256Hex string) (protocol.StickerPayload, string, bool) {
	if info == nil || info.file == nil || info.size <= 0 || info.size >= maxInlineImageRawBytes {
		return protocol.StickerPayload{}, "", false
	}
	mime := imageMimeByExt(info.name)
	if mime == "" {
		return protocol.StickerPayload{}, "", false
	}
	if _, err := info.file.Seek(0, io.SeekStart); err != nil {
		return protocol.StickerPayload{}, "", false
	}
	raw, readErr := io.ReadAll(info.file)
	_, _ = info.file.Seek(0, io.SeekStart)
	if readErr != nil || len(raw) == 0 {
		return protocol.StickerPayload{}, "", false
	}

	b64 := base64.StdEncoding.EncodeToString(raw)
	if len(b64) > maxInlineImageRawBytes {
		if img, _, err := image.Decode(bytes.NewReader(raw)); err == nil {
			var buf bytes.Buffer
			if jpeg.Encode(&buf, img, &jpeg.Options{Quality: 65}) == nil {
				compB64 := base64.StdEncoding.EncodeToString(buf.Bytes())
				if len(compB64) <= maxInlineImageRawBytes {
					b64 = compB64
					mime = "image/jpeg"
				}
			}
		}
	}
	if len(b64) > maxInlineImageRawBytes {
		return protocol.StickerPayload{}, "", false
	}
	if sha256Hex == "" {
		sum := sha256.Sum256(raw)
		sha256Hex = hex.EncodeToString(sum[:])
	}
	payload := protocol.StickerPayload{
		Name: info.name,
		Mime: mime,
		B64:  b64,
		Hash: sha256Hex,
	}
	dataURI := "data:" + mime + ";base64," + b64
	return payload, dataURI, true
}

// maybeSendInlineThumbnail 在 OfferFile / OfferFileToGroup 发送小图片或剪贴板图片时，
// 同步发送带内联缩略图的 STICKER_MSG 供五端聊天气泡直接渲染（M17）。
func (c *Client) maybeSendInlineThumbnail(conn *websocket.Conn, to, group string, info *fileInfo, sha256Hex string) {
	payload, dataURI, ok := buildInlineStickerPayload(info, sha256Hex)
	if !ok {
		return
	}
	var (
		env *protocol.Envelope
		err error
	)
	if group != "" {
		env, err = c.sendGroup(conn, protocol.StickerMsg, group, payload)
	} else {
		env, err = c.sendTo(conn, protocol.StickerMsg, to, payload)
	}
	if err != nil || env == nil {
		return
	}
	c.history.append(appapi.ChatMessage{
		ID:    env.ID,
		From:  c.selfIDValue(),
		To:    to,
		Group: group,
		Type:  protocol.StickerMsg,
		Text:  dataURI,
		TS:    env.TS,
	})
}
