// Package hubapi 提供 hub 的 HTTP API（与 WebSocket 同端口，M9）。
//
// 文件传输的数据面：
//
//	POST /api/files          multipart 上传 → {fid,name,sz,m}
//	GET  /api/files/{fileID}  流式下载（http.ServeContent，支持 Range 断点续传）
//
// 与 hubstate 的分工：hubstate 只管 WS 帧语义（纯状态机，不 import
// net/http）；文件这类「请求-响应」数据面放本包。两者在 cmd/hub 里用
// 同一个 mux 组装（ws.Transport.WithHandler）。
package hubapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/pandaymx/lanchat/pkg/core"
	"github.com/pandaymx/lanchat/pkg/hubfile"
	"github.com/pandaymx/lanchat/pkg/logging"
)

// FilesAPI 是文件上传/下载端点（无鉴权，与 WS 现状一致；FileID 是
// 32 hex 随机值，不可枚举，局域网 MVP 够用）。
type FilesAPI struct {
	svc    *hubfile.Service
	logger *logging.ComponentLogger
}

// NewFilesAPI 构造文件 API。svc 必填。
func NewFilesAPI(svc *hubfile.Service) *FilesAPI {
	return &FilesAPI{svc: svc, logger: logging.New("hub.api")}
}

// Routes 把文件端点挂到 mux（Go 1.22 的 method + path pattern）。
// 两条 pattern 都指回本 handler：method+path 已在 mux 层匹配，ServeHTTP
// 内部只做二选一分发；{fileID} 的 PathValue 由 ServeMux 写入 request
// context，handleDownload 内仍可读。
func (a *FilesAPI) Routes(mux *http.ServeMux) {
	mux.Handle("POST /api/files", a)
	mux.Handle("GET /api/files/{fileID}", a)
}

// ServeHTTP 实现 http.Handler：按请求分发上传/下载（cmd/hub 直接用
// WithHandler(pattern, filesAPI) 挂载时走这里）。
func (a *FilesAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/files":
		a.handleUpload(w, r)
	case r.Method == http.MethodGet:
		a.handleDownload(w, r)
	default:
		http.NotFound(w, r)
	}
}

// uploadResponse 是 POST /api/files 的 JSON 响应体（与 protocol.FileRef 同构）。
type uploadResponse struct {
	FileID string `json:"fid"`
	Name   string `json:"n"`
	Size   int64  `json:"sz"`
	Mime   string `json:"m"`
}

// handleUpload 接收 multipart 上传：字段名 file，可选携带文件名与
// Content-Type；返回 FileRef JSON，客户端随后发一条带该引用的消息。
func (a *FilesAPI) handleUpload(w http.ResponseWriter, r *http.Request) {
	max := a.svc.MaxSize()
	if max > 0 {
		// 请求体级保护：超过上限直接截断（与 hubfile.LimitReader 双保险，
		// 超限统一映射 413）。
		r.Body = http.MaxBytesReader(w, r.Body, max)
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
			return
		}
		a.logger.Warn("upload: parse multipart failed", "err", err)
		http.Error(w, "invalid multipart body", http.StatusBadRequest)
		return
	}
	if r.MultipartForm != nil {
		defer func() { _ = r.MultipartForm.RemoveAll() }()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		a.logger.Warn("upload: file field missing", "err", err)
		http.Error(w, "multipart field \"file\" required", http.StatusBadRequest)
		return
	}
	defer func() { _ = file.Close() }()

	ref, err := a.svc.Save(r.Context(), file, header.Filename, header.Header.Get("Content-Type"))
	if err != nil {
		if errors.Is(err, hubfile.ErrTooLarge) {
			http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
			return
		}
		a.logger.Error("upload: save failed", "name", header.Filename, "err", err)
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(uploadResponse{FileID: ref.FileID, Name: ref.Name, Size: ref.Size, Mime: ref.Mime})
}

// handleDownload 按 FileID 流式返回文件。
//
// 用 http.ServeContent：自动处理 Range（断点续传）/ If-Modified-Since /
// Content-Length。Content-Disposition 走 RFC 2231 编码（中文文件名
// 浏览器照常显示）。图片内联预览不受影响：<img> 请求会忽略 disposition。
func (a *FilesAPI) handleDownload(w http.ResponseWriter, r *http.Request) {
	fileID := r.PathValue("fileID")
	meta, rc, err := a.svc.Open(r.Context(), fileID)
	if err != nil {
		if errors.Is(err, core.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		a.logger.Error("download: open failed", "file", fileID, "err", err)
		http.Error(w, "open failed", http.StatusInternalServerError)
		return
	}
	defer func() { _ = rc.Close() }()
	seek, ok := rc.(io.ReadSeeker)
	if !ok {
		a.logger.Error("download: not seekable", "file", fileID)
		http.Error(w, "not seekable", http.StatusInternalServerError)
		return
	}
	if meta.Mime == "" {
		meta.Mime = "application/octet-stream"
	}
	// RFC 2231：中文/特殊字符文件名自动编码；浏览器照常显示原文件名。
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": meta.Name})
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("Content-Type", meta.Mime)
	http.ServeContent(w, r, meta.Name, time.UnixMilli(meta.CreatedAt), seek)
}
