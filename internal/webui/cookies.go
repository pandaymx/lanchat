package webui

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
)

// SessionCookieName 是浏览器会话 cookie 的名字。
//
// 一枚 cookie 对应一份 webui Session（一条到 hub 的 pkg/client 连接），
// 多 tab 共享 cookie 即共享 Session（M4.4）。
const SessionCookieName = "lanchat_session"

// newSessionID 生成不透明会话 ID：crypto/rand 8 字节 → 16 位 hex。
//
// 为什么不签名（HMAC）：cookie 值只是服务端 map 的 key，权威状态全在
// 服务端，ID 不可猜即足够；签名防篡改是 M6 登录体系的活（-cookie-secret）。
//
// crypto/rand 失败意味着系统熵源不可用（Linux 上 getrandom 几乎不可能失败），
// 此时整个进程的安全性假设都已失效，直接 panic 比返回一个可猜的 ID 安全。
func newSessionID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Errorf("webui: crypto/rand failed: %w", err))
	}
	return hex.EncodeToString(b[:])
}

// readSessionID 从请求 cookie 里取会话 ID；没有 / 非法返回空串。
func readSessionID(r *http.Request) string {
	c, err := r.Cookie(SessionCookieName)
	if err != nil || c.Value == "" {
		return ""
	}
	return c.Value
}

// issueSessionCookie 把会话 ID 写进响应。
//
//   - HttpOnly：JS 读不到，XSS 偷不走 cookie；
//   - SameSite=Lax：同站导航 / 同源 fetch 自动带，跨站不带（防 CSRF 兜底）；
//   - 不设 Secure：M4 走局域网明文 HTTP，M5 上 HTTPS 时再开；
//   - 不设 Max-Age：会话型 cookie，浏览器关闭即丢；服务端另有 30min
//     无活动 TTL 兜底（见 ManagerConfig.SessionTTL）。
func issueSessionCookie(w http.ResponseWriter, id string) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}
