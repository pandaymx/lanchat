package client

// e2e.go 是 Client 层的端到端加密接入。
//
// 策略（渐进式 E2E，与 keyring 配合）：
//   - 本端启用 E2E 时（SetE2E），发送消息先取会话成员 → 在线设备 →
//     逐个查 hub keyring 公钥；**全部目标设备（含自己）都有公钥**才
//     加密（e2e.EncryptMulti，群聊一个 DEK 逐成员封装），否则退回
//     明文——保证「加密的消息所有接收者都能解」。
//   - 无论加密与否，消息都带 E2EKey 自我声明（发送者设备公钥），
//     接收侧 keyring 自动提取（store 落库路径），公钥逐步扩散。
//   - 接收：Encrypted 非空 → 用本端 E2E 私钥解密后替换 Body；
//     失败（非接收者/密钥不匹配）显示占位，不泄露密文。

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/pandaymx/lanchat/pkg/e2e"
	"github.com/pandaymx/lanchat/pkg/protocol"
)

// SetE2E 启用端到端加密：持有身份并向 hub keyring 注册本设备公钥。
//
// id 为空时禁用 E2E（纯明文，老行为）。注册失败不阻断连接——
// keyring 可由消息自我声明补（发送带 E2EKey 时 store 自动提取）。
func (c *Client) SetE2E(id *e2e.Identity) error {
	c.e2eID = id
	if id == nil {
		return nil
	}
	// 注册本设备公钥（幂等）。
	body, _ := json.Marshal(map[string]string{
		"device_id": c.hello.DeviceID,
		"pubkey":    base64.StdEncoding.EncodeToString(id.PublicKey()),
	})
	req, err := http.NewRequest(http.MethodPost, c.fileBase+"/api/v1/e2e/keys", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("e2e register req: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cliLog.Warn("e2e register failed", "err", err)
		return nil // 不阻断：自我声明兜底
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		cliLog.Warn("e2e register status", "code", resp.StatusCode)
	}
	return nil
}

// e2eEnabled 返回是否启用 E2E。
func (c *Client) e2eEnabled() bool { return c.e2eID != nil }

// encryptMessage 就地加密待发消息（Body → Encrypted）。
// 任一目标设备无公钥 → 保持明文（渐进：未知方先收明文 + 自我声明）。
func (c *Client) encryptMessage(ctx context.Context, msg *protocol.StoredMessage) {
	if !c.e2eEnabled() || msg.Body == "" {
		return
	}
	members, err := c.store.ListConversationMembers(ctx, msg.ConversationID)
	if err != nil {
		return
	}
	// 大厅（无成员表）= 全员广播：目标 = 全部在线设备（含自己）。
	// 群聊 = 会话成员里当前在线、且属于本会话的设备（含自己，发送方要能解）。
	isLobby := len(members) == 0
	targets := make([]string, 0, 4)
	c.peersMu.RLock()
	for _, pr := range c.peers {
		if !pr.Online || pr.DeviceID == "" {
			continue
		}
		if !isLobby && !memberIn(pr.UserID, members) {
			continue
		}
		targets = append(targets, pr.DeviceID)
	}
	c.peersMu.RUnlock()
	// 发送者自己必须能解：peers 里不一定有自己（presence 广播可能未含）。
	self := c.hello.DeviceID
	if self != "" && !containsString(targets, self) {
		targets = append(targets, self)
	}
	if len(targets) == 0 {
		return // 无在线接收者：发明文，对方上线后经同步收到
	}
	// 查全部目标设备公钥；任一缺失 → 明文。
	pubs, ok := c.lookupE2EKeys(targets)
	if !ok {
		cliLog.Debug("e2e: not all recipients have keys, plaintext", "conv", msg.ConversationID)
		return
	}
	env, err := e2e.EncryptMulti(pubs, []byte(msg.Body))
	if err != nil {
		cliLog.Warn("e2e encrypt failed, plaintext", "err", err)
		return
	}
	enc, err := env.Marshal()
	if err != nil {
		cliLog.Warn("e2e marshal failed, plaintext", "err", err)
		return
	}
	msg.Encrypted = enc
	msg.Body = ""
}

// lookupE2EKeys 批量查设备公钥；全部存在才返回（保持顺序）。
func (c *Client) lookupE2EKeys(deviceIDs []string) ([][]byte, bool) {
	if len(deviceIDs) == 0 {
		return nil, false
	}
	url := c.fileBase + "/api/v1/e2e/keys?device_id=" + joinComma(deviceIDs)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cliLog.Warn("e2e lookup failed", "err", err)
		return nil, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	var out struct {
		Keys map[string]string `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, false
	}
	pubs := make([][]byte, 0, len(deviceIDs))
	for _, id := range deviceIDs {
		b64, ok := out.Keys[id]
		if !ok {
			return nil, false // 任一缺失 → 不加密
		}
		pub, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, false
		}
		pubs = append(pubs, pub)
	}
	return pubs, true
}

// decryptMessage 就地解密收到的消息（Encrypted → Body）。
// 未启用 E2E 或明文消息不改动。
func (c *Client) decryptMessage(msg *protocol.StoredMessage) {
	if !c.e2eEnabled() || msg.Encrypted == "" {
		return
	}
	env, err := e2e.Unmarshal(msg.Encrypted)
	if err != nil {
		msg.Body = "[加密消息：无法解密]"
		msg.Encrypted = ""
		return
	}
	plain, err := env.Decrypt(c.e2eID.PrivateKey())
	if err != nil {
		msg.Body = "[加密消息：无法解密]"
		msg.Encrypted = ""
		return
	}
	msg.Body = string(plain)
	msg.Encrypted = ""
}

// containsString 判断 s 是否在 list 里。
func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// memberIn 判断 userID 是否在成员列表里。
func memberIn(userID string, members []string) bool {
	for _, m := range members {
		if m == userID {
			return true
		}
	}
	return false
}

// joinComma 把设备 ID 拼成逗号分隔（URL query）。
func joinComma(ids []string) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += ","
		}
		out += id
	}
	return out
}
