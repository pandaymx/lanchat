import Foundation

// 领域模型严格对齐 api/ipc.schema.json 与 internal/appapi。
// 仅使用 schema 中定义的字段；新增字段均以可选/默认值容忍解码。

struct Peer: Codable, Identifiable, Hashable {
    let id: String
    var nickname: String
    var os: String
    var status: String
}

struct ServerInfo: Codable, Identifiable, Hashable {
    let id: String
    var name: String
    var addr: String
    var version: String
    var authMode: String?
}

struct Transfer: Codable, Identifiable, Hashable {
    let id: String
    var direction: String
    var state: String
    var kind: String
    var peerId: String?
    var groupId: String?
    var name: String
    var size: Int64
    var bytesDone: Int64
    var speedBps: Int64
    var viaRelay: Bool
    var errorReason: String?

    var fraction: Float {
        size > 0 ? Float(bytesDone) / Float(size) : 0
    }

    var isIncoming: Bool { direction == "inbound" }
}

struct Channel: Codable, Identifiable, Hashable {
    let id: String
    var name: String
    var ownerId: String
    var isPrivate: Bool?
    var topic: String?
    var members: [String]

    enum CodingKeys: String, CodingKey {
        case id, name, ownerId, members, topic
        case isPrivate = "private"
    }

    /// 侧栏副标题：优先显示主题，无主题时显示成员数。
    var subtitle: String {
        if let topic, !topic.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            return topic
        }
        return "\(members.count) 名成员"
    }
}

struct FullState: Codable {
    var conn: String
    var server: String?
    var selfId: String?
    var nickname: String
    var peers: [Peer]
    var transfers: [Transfer]
    var channels: [Channel]
}

struct ChatMessage: Identifiable, Hashable {
    let id: String
    let peerId: String
    let text: String
    let inbound: Bool
    let timestamp: Date
    /// 频道消息的发送者昵称（单聊不显示，故可缺省）。
    var senderName: String? = nil
}

// 各方法 result 包装。
struct MsgIDResult: Codable { let msgID: String }
struct TransferIDResult: Codable { let transferID: String }
struct ChannelIDResult: Codable { let channelID: String }
struct EmptyResult: Codable {}
