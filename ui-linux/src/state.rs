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
    pub current_chat: ChatTarget,
    pub discovered: Vec<ServerInfo>,
}

impl AppState {
    pub fn new() -> Self {
        Self::default()
    }

    /// 用 GetState 全量结果覆盖（保留本地聊天记录）。
    pub fn apply_full_state(&mut self, s: State) {
        self.conn = s.conn;
        self.conn_reason.clear();
        self.nickname = s.nickname;
        self.self_id = s.self_id.unwrap_or_default();
        self.server_addr = s.server.unwrap_or_default();
        self.peers = s.peers;
        self.transfers = s.transfers;
        self.channels = s.channels;
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
        self.messages
            .entry(msg.peer_id.clone())
            .or_default()
            .push(msg);
    }

    pub fn unread_count(&self, peer_id: &str) -> usize {
        self.messages
            .get(peer_id)
            .map(|v| v.iter().filter(|m| m.inbound).count())
            .unwrap_or(0)
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
            ts: 1,
        });
        st.push_message(ChatMessage {
            peer_id: "p2".into(),
            sender_id: String::new(),
            group: String::new(),
            msg_id: "m2".into(),
            text: "私聊消息".into(),
            inbound: true,
            ts: 2,
        });
        assert_eq!(st.messages.get("c1").unwrap().len(), 1);
        assert_eq!(st.messages.get("p2").unwrap().len(), 1);
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
