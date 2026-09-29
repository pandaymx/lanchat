package core

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"runtime"
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
		return nil, fmt.Errorf("core: %s is a directory", path)
	}
	return &fileInfo{file: f, name: st.Name(), size: st.Size()}, nil
}
