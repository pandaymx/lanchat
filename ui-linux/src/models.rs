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

#[derive(Debug, Clone, Deserialize, Default, PartialEq, Eq)]
pub struct Channel {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub name: String,
    #[serde(default, rename = "ownerId")]
    pub owner_id: String,
    #[serde(default)]
    pub private: bool,
    #[serde(default)]
    pub topic: String,
    #[serde(default)]
    pub members: Vec<String>,
}

#[derive(Debug, Clone, Deserialize, Default)]
pub struct HistoryMessage {
    #[serde(default)]
    pub id: String,
    #[serde(default)]
    pub from: String,
    #[serde(default)]
    pub to: String,
    #[serde(default)]
    pub group: String,
    #[serde(default, rename = "type")]
    pub msg_type: String,
    #[serde(default)]
    pub text: String,
    #[serde(default, rename = "replyTo")]
    pub reply_to: String,
    #[serde(default, rename = "replyQuote")]
    pub reply_quote: String,
    #[serde(default)]
    pub mentions: Vec<String>,
    #[serde(default)]
    pub reactions: std::collections::HashMap<String, Vec<String>>,
    #[serde(default, rename = "editedAt")]
    pub edited_at: i64,
    #[serde(default)]
    pub pinned: bool,
    #[serde(default, rename = "mediaMime")]
    pub media_mime: String,
    #[serde(default, rename = "mediaPath")]
    pub media_path: String,
    #[serde(default)]
    pub revoked: bool,
    #[serde(default, rename = "readBy")]
    pub read_by: Vec<String>,
    #[serde(default)]
    pub ts: i64,
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
    #[serde(default)]
    pub messages: Vec<HistoryMessage>,
}

/// 一条聊天消息（UI 本地模型；来自 msg.received、GetState 历史或自己发送回执）。
#[derive(Debug, Clone)]
pub struct ChatMessage {
    /// 会话归属键：单播为对端 peer_id；频道为 channelID。
    pub peer_id: String,
    /// 频道消息的发送者 ID；单播为空。
    pub sender_id: String,
    /// 非空表示该消息属于 G2 频道（值为 channelID）。
    pub group: String,
    pub msg_id: String,
    pub text: String,
    pub inbound: bool,
    pub revoked: bool,
    pub read_by: Vec<String>,
    pub ts: i64,
}

/// 解析 base64 编码的块位图并生成紧凑块矩阵进度文本（如 `▓▓▓░░░ 3/6 块`）。
pub fn format_matrix_summary(b64: &str, size: i64) -> Option<String> {
    let bytes = decode_base64(b64)?;
    let total_blocks = (size.max(0) as u64).div_ceil(4 * 1024 * 1024).max(1) as usize;
    let have_blocks = bytes
        .iter()
        .map(|b| b.count_ones() as usize)
        .sum::<usize>()
        .min(total_blocks);
    let cells = total_blocks.min(8);
    let filled = if total_blocks > 0 {
        (have_blocks * cells).div_ceil(total_blocks)
    } else {
        0
    }
    .min(cells);
    let bar: String = (0..cells)
        .map(|i| if i < filled { '▓' } else { '░' })
        .collect();
    Some(format!("{bar} {have_blocks}/{total_blocks} 块"))
}

fn decode_base64(input: &str) -> Option<Vec<u8>> {
    let clean: Vec<u8> = input
        .bytes()
        .filter(|b| !b.is_ascii_whitespace() && *b != b'=')
        .collect();
    if clean.is_empty() {
        return Some(Vec::new());
    }
    let val = |b: u8| -> Option<u8> {
        match b {
            b'A'..=b'Z' => Some(b - b'A'),
            b'a'..=b'z' => Some(b - b'a' + 26),
            b'0'..=b'9' => Some(b - b'0' + 52),
            b'+' | b'-' => Some(62),
            b'/' | b'_' => Some(63),
            _ => None,
        }
    };
    let mut out = Vec::with_capacity(clean.len() * 3 / 4);
    for chunk in clean.chunks(4) {
        let b0 = val(chunk[0])?;
        let b1 = chunk.get(1).copied().and_then(val)?;
        out.push((b0 << 2) | (b1 >> 4));
        if let Some(&c2) = chunk.get(2) {
            let b2 = val(c2)?;
            out.push((b1 << 4) | (b2 >> 2));
            if let Some(&c3) = chunk.get(3) {
                let b3 = val(c3)?;
                out.push((b2 << 6) | b3);
            }
        }
    }
    Some(out)
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

    #[test]
    fn matrix_summary_formatting() {
        // 0b0000_0111 = 7 ("Bw==") -> 3 bits set out of 4 blocks (16 MiB)
        let s = format_matrix_summary("Bw==", 16 * 1024 * 1024).unwrap();
        assert_eq!(s, "▓▓▓░ 3/4 块");
    }
}
