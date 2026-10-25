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
            let inbound = !self.self_id.is_empty() && m.from != self.self_id;
            let (conv_key, sender_id) = if !m.group.is_empty() {
                (m.group.clone(), m.from.clone())
            } else if inbound {
                (m.from.clone(), String::new())
            } else {
                (m.to.clone(), String::new())
            };
            if conv_key.is_empty() {
                continue;
            }
            let text = if m.revoked {
                "[消息已撤回]".to_string()
            } else if m.text.starts_with("data:image/") {
                m.text
            } else if m.msg_type == "STICKER_MSG" || m.msg_type == "sticker" {
                format!("[表情] {}", m.text)
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
        let list = self.messages.entry(msg.peer_id.clone()).or_default();
        if !msg.msg_id.is_empty() {
            if let Some(existing) = list.iter_mut().find(|x| x.msg_id == msg.msg_id) {
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
                    m.revoked = true;
                    m.text = "[消息已撤回]".to_string();
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
        }
        for list in self.messages.values() {
            if let Some(orig) = list.iter().find(|m| m.msg_id == target_id && !m.revoked) {
                return format!("「{}」\n{}", orig.text, body);
            }
        }
        format!("[re:{target_id}] {body}")
    }
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
        assert_eq!(st.search_messages("p2", "私聊").len(), 0);
        assert_eq!(st.messages.get("c1").unwrap().len(), 1);
        assert_eq!(st.messages.get("p2").unwrap()[0].text, "[消息已撤回]");
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
}
