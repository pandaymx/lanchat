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
}

/// 一条聊天消息（本地聚合，UI 使用）。
struct ChatMessage: Identifiable, Equatable {
    let id: String
    let peerId: String
    let text: String
    let inbound: Bool
    let timestamp: Date
    /// 所属频道 ID；nil 表示 1:1 单播消息。
    var group: String?
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
