//! 应用共享状态：由 GTK 主线程独占读写。
//! tokio 后台线程只通过 `glib::idle_add_local` 把事件 marshal 回主线程。

use crate::models::{ChatMessage, ConnState, Peer, ServerInfo, State, Transfer};
use std::collections::HashMap;

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
    /// peer_id → 消息列表。
    pub messages: HashMap<String, Vec<ChatMessage>>,
    pub current_chat: Option<String>,
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
