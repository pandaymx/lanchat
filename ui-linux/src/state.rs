//! 应用共享状态：由 GTK 主线程独占读写。
//! tokio 后台线程只通过 `glib::idle_add_local` 把事件 marshal 回主线程。

use crate::models::{Channel, ChatMessage, ConnState, Peer, ServerInfo, State, Transfer};
use std::collections::HashMap;

/// 当前打开的会话。
#[derive(Debug, Clone, PartialEq, Eq, Default)]
pub enum ChatTarget {
    #[default]
    None,
    /// 单播会话（peer_id）。
    Peer(String),
    /// G2 频道会话（channelID）。
    Channel(String),
}

impl ChatTarget {
    /// 会话在 `messages` map 中的归属键。
    pub fn key(&self) -> Option<&str> {
        match self {
            ChatTarget::None => None,
            ChatTarget::Peer(id) | ChatTarget::Channel(id) => Some(id),
        }
    }

    pub fn is_channel(&self) -> bool {
        matches!(self, ChatTarget::Channel(_))
    }
}

/// 左侧 QQ 导航栏页签。
#[derive(Debug, Clone, Copy, PartialEq, Eq, Default)]
pub enum NavTab {
    #[default]
    Sessions,
    Contacts,
    Channels,
}

#[derive(Default)]
pub struct AppState {
    pub conn: ConnState,
    pub conn_reason: String,
    pub nickname: String,
    pub self_id: String,
    pub server_addr: String,
    pub peers: Vec<Peer>,
    /// id → transfer，保持插入顺序。
    pub transfers: Vec<Transfer>,
    /// G2 自定义频道。
    pub channels: Vec<Channel>,
    /// 会话键（peer_id 或 channelID）→ 消息列表。
    pub messages: HashMap<String, Vec<ChatMessage>>,
    /// 会话键 → 已查阅的入站消息数量（用于计算未读红点）。
    pub seen_inbound: HashMap<String, usize>,
    /// transfer_id → 群发块矩阵摘要（group.matrix）。
    pub matrix_summary: HashMap<String, String>,
    pub current_chat: ChatTarget,
    pub discovered: Vec<ServerInfo>,
    pub nav_tab: NavTab,
    pub search_filter: String,
    pub e2ee_enabled: bool,
    /// msg_id → 撤回前保存的原始可编辑消息正文（用于点击「重新编辑」恢复到输入框）。
    pub revoked_originals: HashMap<String, String>,
    /// 当前正在引用回复的目标消息（用于输入框上方 QQ 引用回复预览条）。
    pub reply_target: Option<ChatMessage>,
}

impl AppState {
    pub fn new() -> Self {
        Self::default()
    }

    /// 用 GetState 全量结果覆盖（合并核心持久化的聊天历史记录）。
    pub fn apply_full_state(&mut self, s: State) {
        self.conn = s.conn;
        self.conn_reason.clear();
        self.nickname = s.nickname;
        self.self_id = s.self_id.unwrap_or_default();
        self.server_addr = s.server.unwrap_or_default();
        self.peers = s.peers;
        self.transfers = s.transfers;
        self.channels = s.channels;
        for m in s.messages {
            self.apply_history_message(m);
        }
    }

    pub fn apply_history_message(&mut self, m: crate::models::HistoryMessage) {
        let inbound = !self.self_id.is_empty() && m.from != self.self_id;
        let (conv_key, sender_id) = if !m.group.is_empty() {
            (m.group.clone(), m.from.clone())
        } else if inbound {
            (m.from.clone(), String::new())
        } else {
            (m.to.clone(), String::new())
        };
        if conv_key.is_empty() {
            return;
        }
        let text = if m.revoked {
            "[消息已撤回]".to_string()
        } else {
            m.text
        };
        self.push_message(ChatMessage {
            peer_id: conv_key,
            sender_id,
            group: m.group,
            msg_id: m.id,
            text,
            inbound,
            revoked: m.revoked,
            read_by: m.read_by,
            ts: m.ts,
        });
    }

    pub fn upsert_transfer(&mut self, t: Transfer) {
        if let Some(slot) = self.transfers.iter_mut().find(|x| x.id == t.id) {
            *slot = t;
        } else {
            self.transfers.push(t);
        }
    }

    pub fn remove_transfer(&mut self, id: &str) -> Option<Transfer> {
        self.transfers
            .iter()
            .position(|x| x.id == id)
            .map(|i| self.transfers.remove(i))
    }

    pub fn peer(&self, id: &str) -> Option<&Peer> {
        self.peers.iter().find(|p| p.id == id)
    }

    pub fn channel(&self, id: &str) -> Option<&Channel> {
        self.channels.iter().find(|c| c.id == id)
    }

    /// 当前 self 是否在频道成员列表中。
    pub fn is_channel_member(&self, channel_id: &str) -> bool {
        self.channel(channel_id)
            .map(|c| c.members.iter().any(|m| m == &self.self_id))
            .unwrap_or(false)
    }

    /// 当前 self 是否为频道 owner。
    pub fn is_channel_owner(&self, channel_id: &str) -> bool {
        self.channel(channel_id)
            .map(|c| c.owner_id == self.self_id)
            .unwrap_or(false)
    }

    pub fn push_message(&mut self, msg: ChatMessage) {
        if is_system_diagnostic_message(&msg.msg_id, &msg.text) {
            return;
        }
        let list = self.messages.entry(msg.peer_id.clone()).or_default();
        if !msg.msg_id.is_empty() {
            if let Some(existing) = list.iter_mut().find(|x| x.msg_id == msg.msg_id) {
                if msg.revoked && !existing.revoked {
                    let orig = extract_editable_original(&existing.text);
                    if !orig.is_empty() {
                        self.revoked_originals.insert(existing.msg_id.clone(), orig);
                    }
                }
                existing.text = msg.text;
                existing.revoked = msg.revoked;
                existing.read_by = msg.read_by;
                return;
            }
        }
        list.push(msg);
    }

    pub fn revoke_message(&mut self, msg_id: &str) -> bool {
        if msg_id.is_empty() {
            return false;
        }
        let mut updated = false;
        for list in self.messages.values_mut() {
            for m in list.iter_mut() {
                if m.msg_id == msg_id {
                    if !m.revoked {
                        let orig = extract_editable_original(&m.text);
                        if !orig.is_empty() {
                            self.revoked_originals.insert(m.msg_id.clone(), orig);
                        }
                    }
                    m.revoked = true;
                    m.text = "[消息已撤回]".to_string();
                    updated = true;
                }
            }
        }
        updated
    }

    pub fn update_message_text(&mut self, msg_id: &str, new_text: &str) -> bool {
        if msg_id.is_empty() || new_text.is_empty() {
            return false;
        }
        let mut updated = false;
        for list in self.messages.values_mut() {
            for m in list.iter_mut() {
                if m.msg_id == msg_id && !m.revoked {
                    m.text = new_text.to_string();
                    updated = true;
                }
            }
        }
        updated
    }

    pub fn mark_read(&mut self, msg_id: &str, reader_id: &str) -> bool {
        if msg_id.is_empty() || reader_id.is_empty() {
            return false;
        }
        let mut updated = false;
        for list in self.messages.values_mut() {
            for m in list.iter_mut() {
                if m.msg_id == msg_id {
                    if !m.read_by.iter().any(|r| r == reader_id) {
                        m.read_by.push(reader_id.to_string());
                    }
                    updated = true;
                }
            }
        }
        updated
    }

    pub fn unread_count(&self, peer_id: &str) -> usize {
        self.messages
            .get(peer_id)
            .map(|v| v.iter().filter(|m| m.inbound).count())
            .unwrap_or(0)
    }

    /// 标记指定会话的入站消息全部已浏览（清除 QQ 红点角标）。
    pub fn mark_conv_seen(&mut self, conv_key: &str) {
        let total = self.unread_count(conv_key);
        self.seen_inbound.insert(conv_key.to_string(), total);
    }

    /// 获取指定会话尚未浏览的入站未读数。
    pub fn unseen_count(&self, conv_key: &str) -> usize {
        let total = self.unread_count(conv_key);
        let seen = self.seen_inbound.get(conv_key).copied().unwrap_or(0);
        total.saturating_sub(seen)
    }

    /// 获取指定会话的最新一条消息。
    pub fn last_message(&self, conv_key: &str) -> Option<&ChatMessage> {
        self.messages.get(conv_key).and_then(|v| v.last())
    }

    /// 在指定会话（或空键表示全部会话）中按关键字检索未撤回消息（M12）。
    pub fn search_messages(&self, conv_key: &str, query: &str) -> Vec<ChatMessage> {
        let q = query.trim().to_lowercase();
        if q.is_empty() {
            return Vec::new();
        }
        let mut out = Vec::new();
        for (k, list) in &self.messages {
            if !conv_key.is_empty() && k != conv_key {
                continue;
            }
            for m in list {
                if !m.revoked && m.text.to_lowercase().contains(&q) {
                    out.push(m.clone());
                }
            }
        }
        out
    }

    /// 将 `/reply <msgID> <body>` 展开为带原消息引用的展示文本（M12）。
    pub fn format_reply_text(&self, raw: &str) -> String {
        let trimmed = raw.trim();
        let Some(rest) = trimmed.strip_prefix("/reply ") else {
            return raw.to_string();
        };
        let mut parts = rest.trim().splitn(2, ' ');
        let target_id = parts.next().unwrap_or("").trim();
        let body = parts.next().unwrap_or("").trim();
        if target_id.is_empty() || body.is_empty() {
            return raw.to_string();
        };
        for list in self.messages.values() {
            if let Some(orig) = list.iter().find(|m| m.msg_id == target_id && !m.revoked) {
                let (clean_orig, _) = split_reactions(&orig.text);
                return format!("「{}」\n{}", clean_orig, body);
            }
        }
        format!("[re:{target_id}] {body}")
    }
}

fn is_reaction_emoji_token(token: &str) -> bool {
    if token.is_empty() || token.len() > 24 {
        return false;
    }
    !token.chars().any(|c| {
        c.is_ascii_alphanumeric()
            || ('\u{4E00}'..='\u{9FFF}').contains(&c)
            || c == ':'
            || c == '"'
            || c == ' '
    })
}

fn is_system_diagnostic_text(text: &str) -> bool {
    let t = text.trim();
    t.starts_with("[安全码指纹]")
        || t.starts_with("🛡️ [安全码指纹")
        || t.starts_with("📊 [诊断状态]")
        || t.starts_with("🚀 [版本更新]")
        || t.starts_with("⚡ [带宽限速")
}

fn is_system_diagnostic_message(msg_id: &str, text: &str) -> bool {
    for prefix in ["fp-", "status-", "update-", "search-", "e2ee-", "bwlimit-"] {
        if msg_id.starts_with(prefix) {
            return true;
        }
    }
    is_system_diagnostic_text(text)
}

fn strip_trailing_reactions(raw: &str) -> (&str, Vec<String>) {
    let mut body = raw;
    let mut rev_reactions = Vec::new();
    while let Some(idx) = body.rfind(" [") {
        if !body.ends_with(']') {
            break;
        }
        let token = &body[idx + 2..body.len() - 1];
        if !is_reaction_emoji_token(token) {
            break;
        }
        rev_reactions.push(token.to_string());
        body = &body[..idx];
    }
    rev_reactions.reverse();
    (body, rev_reactions)
}

/// 拆分消息正文与尾部表情回应徽章（如 `"你好 [👍] [❤️]"` -> `("你好", vec!["👍", "❤️"])`），
/// 同时剥离引用块 `「...」\n` 内遗留的 `[👍]` 或 `[安全码指纹]`，供 UI 将点赞渲染在聊天气泡下方。
pub fn split_reactions(raw: &str) -> (String, Vec<String>) {
    let (body_slice, reactions) = strip_trailing_reactions(raw);
    let body_slice = body_slice.strip_prefix("[表情] ").unwrap_or(body_slice);
    if let Some(after_open) = body_slice.strip_prefix('「') {
        if let Some(close_idx) = after_open.find("」\n") {
            let quoted = &after_open[..close_idx];
            let rest = &after_open[close_idx + "」\n".len()..];
            let (clean_quoted, _) = strip_trailing_reactions(quoted);
            let clean_quoted = clean_quoted.strip_prefix("[表情] ").unwrap_or(clean_quoted);
            if is_system_diagnostic_text(clean_quoted) {
                return (rest.to_string(), reactions);
            }
            return (format!("「{clean_quoted}」\n{rest}"), reactions);
        }
    }
    (body_slice.to_string(), reactions)
}

/// 拆分 `split_reactions` 后的正文是否包含引用块 `「...」\n`。
pub fn split_quote(clean: &str) -> (Option<String>, String) {
    if let Some(after_open) = clean.strip_prefix('「') {
        if let Some(close_idx) = after_open.find("」\n") {
            let quoted = &after_open[..close_idx];
            let rest = &after_open[close_idx + "」\n".len()..];
            return (Some(quoted.to_string()), rest.to_string());
        }
    }
    (None, clean.to_string())
}

fn extract_editable_original(raw: &str) -> String {
    let (clean, _) = split_reactions(raw);
    let (_, body) = split_quote(&clean);
    let trimmed = body.trim();
    if trimmed.is_empty()
        || trimmed == "[消息已撤回]"
        || trimmed == "消息已撤回"
        || trimmed.starts_with("data:image/")
    {
        return String::new();
    }
    trimmed.to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn full_state_loads_channels() {
        let json = serde_json::json!({
            "conn": "connected",
            "nickname": "me",
            "selfId": "self-1",
            "peers": [],
            "transfers": [],
            "channels": [
                {"id": "c1", "name": "运维", "ownerId": "self-1", "members": ["self-1", "p2"]}
            ]
        });
        let raw: State = serde_json::from_value(json).unwrap();
        let mut st = AppState::new();
        st.apply_full_state(raw);
        assert_eq!(st.channels.len(), 1);
        assert_eq!(st.channel("c1").unwrap().name, "运维");
        assert!(st.is_channel_member("c1"));
        assert!(!st.is_channel_member("missing"));
    }

    #[test]
    fn channel_messages_partition_by_group() {
        let mut st = AppState::new();
        st.push_message(ChatMessage {
            peer_id: "c1".into(),
            sender_id: "p2".into(),
            group: "c1".into(),
            msg_id: "m1".into(),
            text: "频道消息".into(),
            inbound: true,
            revoked: false,
            read_by: Vec::new(),
            ts: 1,
        });
        st.push_message(ChatMessage {
            peer_id: "p2".into(),
            sender_id: String::new(),
            group: String::new(),
            msg_id: "m2".into(),
            text: "私聊消息".into(),
            inbound: true,
            revoked: false,
            read_by: Vec::new(),
            ts: 2,
        });
        assert!(st.mark_read("m1", "p3"));
        assert_eq!(st.search_messages("c1", "频道").len(), 1);
        assert_eq!(st.format_reply_text("/reply m1 收到"), "「频道消息」\n收到");
        assert!(st.revoke_message("m2"));
        assert_eq!(
            st.revoked_originals.get("m2").map(|s| s.as_str()),
            Some("私聊消息")
        );
        assert_eq!(st.search_messages("p2", "私聊").len(), 0);
        assert_eq!(st.messages.get("c1").unwrap().len(), 1);
        assert_eq!(st.messages.get("p2").unwrap()[0].text, "[消息已撤回]");
        let (q, b) = split_quote("「频道消息」\n收到");
        assert_eq!(q.as_deref(), Some("频道消息"));
        assert_eq!(b, "收到");
        st.current_chat = ChatTarget::Channel("c1".into());
        assert!(st.current_chat.is_channel());
        assert_eq!(st.current_chat.key(), Some("c1"));
    }

    #[test]
    fn channel_owner_private_topic() {
        let json = serde_json::json!({
            "conn": "connected",
            "selfId": "self-1",
            "peers": [],
            "transfers": [],
            "channels": [
                {"id": "c2", "name": "核心", "ownerId": "self-1",
                 "private": true, "topic": "内核讨论", "members": ["self-1"]}
            ]
        });
        let raw: State = serde_json::from_value(json).unwrap();
        let mut st = AppState::new();
        st.apply_full_state(raw);
        let ch = st.channel("c2").unwrap();
        assert!(ch.private);
        assert_eq!(ch.topic, "内核讨论");
        assert!(st.is_channel_owner("c2"));
        assert!(!st.is_channel_owner("missing"));
    }

    #[test]
    fn split_reactions_extracts_emoji_badges() {
        let (body, reactions) = split_reactions("初版发版说明 [👍] [❤️]");
        assert_eq!(body, "初版发版说明");
        assert_eq!(reactions, vec!["👍", "❤️"]);

        let (pinned_body, r2) = split_reactions("📌 [置顶] 重要通知 [👍]");
        assert_eq!(pinned_body, "📌 [置顶] 重要通知");
        assert_eq!(r2, vec!["👍"]);

        let (voice_body, r3) = split_reactions("🎤 [语音消息 3\"]");
        assert_eq!(voice_body, "🎤 [语音消息 3\"]");
        assert!(r3.is_empty());

        let (clean_reply, r4) = split_reactions("「[安全码指纹] 本机: F3A8-8288-9F58 [👍]」\n你好");
        assert_eq!(clean_reply, "你好");
        assert!(r4.is_empty());

        let (quoted_reply, r5) = split_reactions("「初版发版说明 [👍]」\n收到 [🎉]");
        assert_eq!(quoted_reply, "「初版发版说明」\n收到");
        assert_eq!(r5, vec!["🎉"]);
    }
}
