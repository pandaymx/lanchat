//! 与 `api/ipc.schema.json` 对齐的数据类型（驼峰字段）。
//! 仅做反序列化与 UI 展示，不含业务逻辑。

use serde::Deserialize;

#[derive(Debug, Clone, Deserialize, Default, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum ConnState {
    #[default]
    Disconnected,
    Connecting,
    Connected,
    AuthFailed,
}

impl ConnState {
    pub fn as_str(&self) -> &'static str {
        match self {
            ConnState::Disconnected => "disconnected",
            ConnState::Connecting => "connecting",
            ConnState::Connected => "connected",
            ConnState::AuthFailed => "auth_failed",
        }
    }

    pub fn label(&self) -> &'static str {
        match self {
            ConnState::Disconnected => "未连接",
            ConnState::Connecting => "连接中…",
            ConnState::Connected => "已连接",
            ConnState::AuthFailed => "口令错误",
        }
    }
}

#[derive(Debug, Clone, Deserialize, Default, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum TransferState {
    #[default]
    Pending,
    Active,
    Paused,
    Done,
    Failed,
    Canceled,
}

impl TransferState {
    pub fn label(&self) -> &'static str {
        match self {
            TransferState::Pending => "待确认",
            TransferState::Active => "传输中",
            TransferState::Paused => "已暂停",
            TransferState::Done => "已完成",
            TransferState::Failed => "失败",
            TransferState::Canceled => "已取消",
        }
    }
}

#[derive(Debug, Clone, Deserialize, Default, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum TransferDirection {
    #[default]
    Inbound,
    Outbound,
}

impl TransferDirection {
    pub fn label(&self) -> &'static str {
        match self {
            TransferDirection::Inbound => "接收",
            TransferDirection::Outbound => "发送",
        }
    }
}

#[derive(Debug, Clone, Deserialize, Default, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum PathKind {
    #[default]
    Unicast,
    Swarm,
    Channel,
}

#[derive(Debug, Clone, Deserialize, Default)]
pub struct Peer {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub nickname: String,
    #[serde(default)]
    pub os: String,
    #[serde(default)]
    pub status: String,
}

#[derive(Debug, Clone, Deserialize, Default)]
pub struct ServerInfo {
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub addr: String,
    #[serde(default)]
    pub version: String,
    #[serde(default, rename = "authMode")]
    pub auth_mode: String,
}

#[derive(Debug, Clone, Deserialize, Default)]
pub struct Transfer {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub direction: TransferDirection,
    #[serde(default)]
    pub state: TransferState,
    #[serde(default)]
    pub kind: PathKind,
    #[serde(default, rename = "peerId")]
    pub peer_id: String,
    #[serde(default, rename = "groupId")]
    pub group_id: String,
    #[serde(default)]
    pub name: String,
    #[serde(default)]
    pub size: i64,
    #[serde(default, rename = "bytesDone")]
    pub bytes_done: i64,
    #[serde(default, rename = "speedBps")]
    pub speed_bps: i64,
    #[serde(default, rename = "viaRelay")]
    pub via_relay: bool,
    #[serde(default, rename = "errorReason")]
    pub error_reason: Option<String>,
}

impl Transfer {
    pub fn fraction(&self) -> f64 {
        if self.size <= 0 {
            0.0
        } else {
            (self.bytes_done as f64 / self.size as f64).clamp(0.0, 1.0)
        }
    }
}

#[derive(Debug, Clone, Deserialize, Default)]
pub struct Channel {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub name: String,
    #[serde(default, rename = "ownerId")]
    pub owner_id: String,
    #[serde(default)]
    pub members: Vec<String>,
}

#[derive(Debug, Clone, Deserialize, Default)]
pub struct State {
    #[serde(default)]
    pub conn: ConnState,
    #[serde(default)]
    pub server: Option<String>,
    #[serde(default, rename = "selfId")]
    pub self_id: Option<String>,
    #[serde(default)]
    pub nickname: String,
    #[serde(default)]
    pub peers: Vec<Peer>,
    #[serde(default)]
    pub transfers: Vec<Transfer>,
    #[serde(default)]
    pub channels: Vec<Channel>,
}

/// 一条聊天消息（UI 本地模型；来自 msg.received 或自己发送回执）。
#[derive(Debug, Clone)]
pub struct ChatMessage {
    pub peer_id: String,
    pub msg_id: String,
    pub text: String,
    pub inbound: bool,
    pub ts: i64,
}

/// 人类可读字节数。
pub fn format_size(bytes: i64) -> String {
    const UNITS: [&str; 5] = ["B", "KiB", "MiB", "GiB", "TiB"];
    let mut v = bytes as f64;
    let mut u = 0;
    while v >= 1024.0 && u < UNITS.len() - 1 {
        v /= 1024.0;
        u += 1;
    }
    if u == 0 {
        format!("{} {}", bytes, UNITS[0])
    } else {
        format!("{v:.1} {}", UNITS[u])
    }
}

/// 人类可读速率。
pub fn format_speed(bps: i64) -> String {
    format!("{}/s", format_size(bps))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn size_formatting() {
        assert_eq!(format_size(0), "0 B");
        assert_eq!(format_size(1023), "1023 B");
        assert_eq!(format_size(1024), "1.0 KiB");
        assert_eq!(format_size(1 << 30), "1.0 GiB");
    }

    #[test]
    fn state_unknown_fields_tolerated() {
        // 契约纪律：新增字段必须可缺省解析。
        let json = r#"{"conn":"connected","futureField":1,"peers":[]}"#;
        let s: State = serde_json::from_str(json).unwrap();
        assert_eq!(s.conn, ConnState::Connected);
    }

    #[test]
    fn transfer_fraction() {
        let t = Transfer {
            size: 100,
            bytes_done: 25,
            ..Default::default()
        };
        assert!((t.fraction() - 0.25).abs() < 1e-9);
        let t = Transfer {
            size: 0,
            bytes_done: 0,
            ..Default::default()
        };
        assert_eq!(t.fraction(), 0.0);
    }
}
