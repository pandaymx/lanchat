package secure

// trust.go 提供客户端各端共用的 TOFU 信任便捷函数。

import (
	"fmt"
	"net/url"
)

// TrustForURL 返回一个 trustFunc：按 hub URL 的 host:port 校验公钥。
// trustPath 是 TOFU 持久化文件（不存在则首次连接时创建）。
//
// 用法（各端构造 Transport 时）：
//
//	trust, err := secure.TrustForURL(opts.HubURL, filepath.Join(dataDir, "known_hubs.json"))
//	tr := ws.New().WithClientTrust(trust)
func TrustForURL(hubURL, trustPath string) (func(pub []byte) error, error) {
	ts, err := LoadTrustStore(trustPath)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(hubURL)
	if err != nil {
		return nil, fmt.Errorf("secure: parse hub url: %w", err)
	}
	peer := u.Host
	return func(pub []byte) error { return ts.Verify(peer, pub) }, nil
}
