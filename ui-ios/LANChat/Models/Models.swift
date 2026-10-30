import Foundation

// 领域模型：字段严格对齐 api/ipc.schema.json / internal/appapi。
// 与 gomobile 生成的 LC* 类型解耦，UI 只依赖这些 Swift 原生结构。

/// 在线用户。
struct Peer: Identifiable, Equatable, Hashable {
    let id: String
    var nickname: String
    var os: String
    var status: String
}

/// mDNS 发现的中心节点候选。
struct ServerInfo: Identifiable, Equatable, Hashable {
    var name: String
    var id: String
    var addr: String
    var version: String
    var authMode: String

    static func isTailscaleAddress(_ raw: String) -> Bool {
        var s = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        for prefix in ["ws://", "wss://", "http://", "https://"] {
            if s.lowercased().hasPrefix(prefix) {
                s = String(s.dropFirst(prefix.count))
                break
            }
        }
        if let slashIdx = s.firstIndex(of: "/") {
            s = String(s[..<slashIdx])
        }
        if let colonIdx = s.firstIndex(of: ":") {
            s = String(s[..<colonIdx])
        }
        let parts = s.split(separator: ".")
        guard parts.count == 4,
              let a = UInt8(parts[0]),
              let b = UInt8(parts[1]) else {
            return false
        }
        return a == 100 && (64...127).contains(b)
    }

    static func linkBadge(server: String, status: String = "") -> String {
        (isTailscaleAddress(server) || isTailscaleAddress(status))
            ? "🌐 Tailscale 直连"
            : "⚡ 局域网直连"
    }
}

/// 文件传输快照。
struct Transfer: Identifiable, Equatable, Hashable {
    var id: String
    var direction: String
    var state: String
    var kind: String
    var peerId: String
    var groupId: String
    var name: String
    var size: Int64
    var bytesDone: Int64
    var speedBps: Int64
    var viaRelay: Bool
    var errorReason: String

    var fraction: Float {
        size > 0 ? Float(bytesDone) / Float(size) : 0
    }

    var isIncoming: Bool { direction == "inbound" }
    var isActive: Bool { state == "active" }
    var isPendingIncoming: Bool { state == "pending" && isIncoming }
}

/// G2 自定义频道（M9）。
struct Channel: Identifiable, Equatable, Hashable {
    var id: String
    var name: String
    var ownerId: String
    /// 频道主题（可缺省）。
    var topic: String
    /// 是否为私有频道；私有频道仅可经 owner 邀请加入。
    var isPrivate: Bool
    var members: [String]
    var admins: [String] = []
    var muted: [String] = []
    var announcementAck: [String] = []
}

/// 契约 v1.2 实时语音通话状态快照。
struct VoiceCallState: Equatable, Hashable {
    var state: String = "idle"
    var peerId: String = ""
    var groupId: String = ""
    var localAddr: String = ""
    var remoteAddr: String = ""
    var framesIn: Int64 = 0
    var framesOut: Int64 = 0
}

/// 契约 v1.2 实时屏幕共享状态快照。
struct ScreenShareState: Equatable, Hashable {
    var active: Bool = false
    var peerId: String = ""
    var groupId: String = ""
    var fps: Int = 0
    var latestFrame: String = ""
}

/// GetState 返回的全量快照。
struct FullState {
    var conn: String
    var server: String
    var selfId: String
    var nickname: String
    var peers: [Peer]
    var transfers: [Transfer]
    var channels: [Channel]
    var messages: [ChatMessage] = []
    var voiceCall: VoiceCallState? = nil
    var screenShare: ScreenShareState? = nil
}

/// 一条聊天消息（本地聚合，UI 使用）。
struct ChatMessage: Identifiable, Equatable {
    let id: String
    let peerId: String
    var text: String
    let inbound: Bool
    let timestamp: Date
    /// 所属频道 ID；nil 表示 1:1 单播消息。
    var group: String?
    var revoked: Bool = false
    var readBy: [String] = []
    /// 撤回前保留的原始消息文本，供本人点击「重新编辑」恢复到输入框。
    var originalText: String = ""
    /// v1.1 结构化字段。
    var replyQuote: String? = nil
    var reactions: [String: [String]] = [:]
    var editedAt: Int64 = 0
    var pinned: Bool = false
    var mediaMime: String? = nil
    var mediaPath: String? = nil
}

/// 将 group.matrix 的 haveBitmap 转为紧凑块进度摘要（如 "▓▓▓░░░ 4/8 块"）。
func formatMatrixSummary(haveBitmap: Data, size: Int64) -> String {
    guard !haveBitmap.isEmpty else { return "" }
    let chunkSize: Int64 = 1024 * 1024
    let totalChunks: Int
    if size > 0 {
        totalChunks = Int((size + chunkSize - 1) / chunkSize)
    } else {
        totalChunks = haveBitmap.count * 8
    }
    guard totalChunks > 0 else { return "" }
    var have = 0
    let slots = min(totalChunks, 8)
    var bar = ""
    for i in 0..<totalChunks {
        let byteIdx = i / 8
        let bitIdx = i % 8
        let bit = byteIdx < haveBitmap.count && ((haveBitmap[byteIdx] >> bitIdx) & 1) == 1
        if bit { have += 1 }
    }
    for s in 0..<slots {
        let idx = s * totalChunks / slots
        let byteIdx = idx / 8
        let bitIdx = idx % 8
        let bit = byteIdx < haveBitmap.count && ((haveBitmap[byteIdx] >> bitIdx) & 1) == 1
        bar.append(bit ? "▓" : "░")
    }
    return "\(bar) \(have)/\(totalChunks) 块"
}
