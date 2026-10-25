package core

import (
	"archive/zip"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
