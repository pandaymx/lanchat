package server

import (
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/pandaymx/lanchat/internal/protocol"
)

const (
	// maxAuthFails 是单个 IP 在 failWindow 内允许的失败次数。
	maxAuthFails = 5
	// failWindow 是失败计数滑动窗口长度。
	failWindow = time.Minute
	// cooldown 是超过失败上限后该 IP 的冷却时长。
	cooldown = time.Minute
)

// failEntry 记录单个 IP 的鉴权失败计数与冷却截止。
type failEntry struct {
	count       int
	windowStart time.Time
	until       time.Time
}

// authenticator 负责 HELLO 的 PSK 校验与按 IP 的失败限流。
// 可被多个连接的 readPump 并发调用，故内部加锁。
type authenticator struct {
	mode    string
	pskHash string

	mu    sync.Mutex
	fails map[string]*failEntry
	now   func() time.Time
}

func newAuthenticator(mode, pskHash string) *authenticator {
	return &authenticator{
		mode:    mode,
		pskHash: pskHash,
		fails:   make(map[string]*failEntry),
		now:     time.Now,
	}
}

// helloOK 校验来自 remoteIP 的 HELLO 是否可接纳。
// 返回值：reason 为拒绝原因（AUTH_FAIL.reason），ok 为是否通过。
// 注意：psk 为明文，本函数及日志均不输出其内容。
func (a *authenticator) helloOK(remoteIP, psk string) (reason string, ok bool) {
	if a.mode == "none" {
		return "", true
	}

	now := a.now()
	a.mu.Lock()
	e := a.fails[remoteIP]
	if e != nil && e.until.After(now) {
		a.mu.Unlock()
		return "too_many_failures", false
	}
	a.mu.Unlock()

	if err := bcrypt.CompareHashAndPassword([]byte(a.pskHash), []byte(psk)); err == nil {
		a.mu.Lock()
		delete(a.fails, remoteIP)
		a.mu.Unlock()
		return "", true
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if e == nil || now.Sub(e.windowStart) > failWindow {
		e = &failEntry{windowStart: now}
		a.fails[remoteIP] = e
	}
	e.count++
	// 第 5 次失败后即进入冷却，使第 6 次尝试直接被拦。
	if e.count >= maxAuthFails {
		e.until = now.Add(cooldown)
	}
	return "bad_psk", false
}

// negotiateFeatures 取客户端能力与服务端实际启用特性的交集。
// relayEnabled 表示服务端中继数据面是否启用；其余特性 M3 暂不启用。
func negotiateFeatures(caps protocol.ClientCaps, relayEnabled bool) []string {
	serverFeatures := map[string]bool{
		"resume": true, // M3 起支持断点续传
	}
	if relayEnabled {
		serverFeatures["relay"] = true
	}
	features := make([]string, 0)
	for _, f := range featureList(caps) {
		if serverFeatures[f] {
			features = append(features, f)
		}
	}
	return features
}

func featureList(caps protocol.ClientCaps) []string {
	feats := make([]string, 0, 5)
	if caps.Resume {
		feats = append(feats, "resume")
	}
	if caps.Relay {
		feats = append(feats, "relay")
	}
	if caps.TLS {
		feats = append(feats, "tls")
	}
	if caps.Swarm {
		feats = append(feats, "swarm")
	}
	if caps.Group {
		feats = append(feats, "group")
	}
	return feats
}
