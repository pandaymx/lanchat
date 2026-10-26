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

// dirBundleCommentPrefix 标识由 LANChat 自动打包的目录树归档（M28：接收端据此自动解包还原目录结构）。
const dirBundleCommentPrefix = "LANCHAT-DIR-TREE-V1:"

// packDirectoryToZip 将非空目录递归打包为临时 .zip 归档供文件传输管道发送（M11b/M28：保留子目录层级、文件权限并写入目录树标识）。
func packDirectoryToZip(dirPath string) (*fileInfo, error) {
	type dirEntry struct {
		fullPath string
		relSlash string
		info     os.FileInfo
	}
	var entries []dirEntry
	var regularCount int
	cleanRoot := filepath.Clean(dirPath)
	err := filepath.Walk(cleanRoot, func(p string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filepath.Clean(p) == cleanRoot {
			return nil
		}
		rel, err := filepath.Rel(cleanRoot, p)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		if info.IsDir() {
			entries = append(entries, dirEntry{fullPath: p, relSlash: strings.TrimSuffix(relSlash, "/") + "/", info: info})
			return nil
		}
		if info.Mode().IsRegular() {
			regularCount++
			entries = append(entries, dirEntry{fullPath: p, relSlash: relSlash, info: info})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if regularCount == 0 && len(entries) == 0 {
		return nil, fmt.Errorf("core: %s is an empty directory", dirPath)
	}

	base := filepath.Base(cleanRoot)
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "archive"
	}

	tmp, err := os.CreateTemp("", "lanchat-dir-*.zip")
	if err != nil {
		return nil, err
	}
	zw := zip.NewWriter(tmp)
	_ = zw.SetComment(dirBundleCommentPrefix + base)

	for _, e := range entries {
		hdr, err := zip.FileInfoHeader(e.info)
		if err != nil {
			_ = zw.Close()
			_ = tmp.Close()
			return nil, err
		}
		hdr.Name = e.relSlash
		if e.info.IsDir() {
			hdr.Method = zip.Store
			if _, err := zw.CreateHeader(hdr); err != nil {
				_ = zw.Close()
				_ = tmp.Close()
				return nil, err
			}
			continue
		}
		hdr.Method = zip.Deflate
		fw, err := zw.CreateHeader(hdr)
		if err != nil {
			_ = zw.Close()
			_ = tmp.Close()
			return nil, err
		}
		src, err := os.Open(e.fullPath)
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
	return &fileInfo{file: tmp, name: base + ".zip", size: st.Size()}, nil
}

// maybeUnpackDirectoryBundle 检查接收完成的归档是否带有 LANChat 目录树标识；若是则安全解包还原完整子目录树与文件权限（M28，含 Zip-Slip 路径穿越防护）。
func maybeUnpackDirectoryBundle(destPath string) (string, bool, error) {
	if destPath == "" {
		return "", false, nil
	}
	zr, err := zip.OpenReader(destPath)
	if err != nil {
		return "", false, nil
	}
	defer func() { _ = zr.Close() }()

	if !strings.HasPrefix(zr.Comment, dirBundleCommentPrefix) {
		return "", false, nil
	}
	dirName := strings.TrimSpace(strings.TrimPrefix(zr.Comment, dirBundleCommentPrefix))
	targetRoot := strings.TrimSuffix(destPath, filepath.Ext(destPath))
	if targetRoot == destPath {
		if dirName != "" {
			targetRoot = filepath.Join(filepath.Dir(destPath), filepath.Base(dirName))
		} else {
			targetRoot = destPath + ".unpacked"
		}
	}
	cleanTargetRoot := filepath.Clean(targetRoot)
	if err := os.MkdirAll(cleanTargetRoot, 0o755); err != nil {
		return "", true, err
	}

	for _, zf := range zr.File {
		relPath := filepath.FromSlash(zf.Name)
		fullOut := filepath.Clean(filepath.Join(cleanTargetRoot, relPath))
		// Zip-Slip 路径穿越防御
		relCheck, relErr := filepath.Rel(cleanTargetRoot, fullOut)
		if relErr != nil || relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) {
			return "", true, fmt.Errorf("core: illegal zip entry path %q", zf.Name)
		}
		if zf.FileInfo().IsDir() || strings.HasSuffix(zf.Name, "/") {
			if err := os.MkdirAll(fullOut, 0o755); err != nil {
				return "", true, err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(fullOut), 0o755); err != nil {
			return "", true, err
		}
		rc, err := zf.Open()
		if err != nil {
			return "", true, err
		}
		perm := zf.Mode().Perm()
		if perm == 0 {
			perm = 0o644
		}
		outFile, err := os.OpenFile(fullOut, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
		if err != nil {
			_ = rc.Close()
			return "", true, err
		}
		_, copyErr := io.Copy(outFile, rc)
		_ = rc.Close()
		closeErr := outFile.Close()
		if copyErr != nil {
			return "", true, copyErr
		}
		if closeErr != nil {
			return "", true, closeErr
		}
	}
	return cleanTargetRoot, true, nil
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
