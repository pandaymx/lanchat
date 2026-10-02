import Foundation

// gomobile Client（LCMobileClient）的 Swift 封装。
//
// gomobile 把 Go 包绑定为 ObjC（LC* 前缀），经桥接头导入 Swift：
//   - 门面包级函数 NewClient  -> LCMobile.newClient(...)
//   - *mobile.Client          -> LCMobileClient
//   - mobile.Listener         -> LCMobileListener 协议
//
// gomobile 无法绑定结构体切片 / []string，故查询方法均返回 JSON（Data），
// 由本文件统一用 ChannelJSON 等 DTO 解码为本地模型。
//
// Listener 回调来自 Go 后台 goroutine（非主线程），这里统一
// DispatchQueue.main 切回主线程后再投递给 @MainActor 的 AppStore。

/// core → UI 的上行事件（已转为本地模型）。
enum CoreEvent {
    case connChanged(state: String, reason: String)
    case peerJoined(Peer)
    case peerLeft(String)
    case message(from: String, group: String, msgId: String, type: String, text: String)
    case progress(Transfer)
    case done(String)
    case failed(transferId: String, reason: String)
    case channelsUpdated([Channel])
}

/// 对 UI 暴露核心操作的引擎。持有进程内唯一的 gomobile 客户端。
final class ChatEngine {
    private var client: LCMobileClient?
    private let downloadDir: String

    /// 事件回调（已保证在主线程调用）。
    var onEvent: ((CoreEvent) -> Void)?

    init(downloadDir: String) {
        self.downloadDir = downloadDir
    }

    /// 创建核心并注册监听器；幂等。
    func start(nickname: String) {
        guard client == nil else { return }
        let bridge = ListenerBridge { [weak self] event in
            DispatchQueue.main.async {
                self?.onEvent?(event)
            }
        }
        client = LCMobile.newClient(nickname, "ios", downloadDir, bridge)
    }

    func close() {
        client?.close()
        client = nil
    }

    // ---- 同步阻塞 API 包一层后台线程 + async ----

    func getState() async -> FullState {
        await runOnIO { [client] in
            guard let data = client?.stateJSON(),
                  let dto = try? JSONDecoder().decode(StateJSON.self, from: data) else {
                return FullState(conn: "disconnected", server: "", selfId: "", nickname: "",
                                 peers: [], transfers: [], channels: [])
            }
            return dto.toModel()
        }
    }

    func connect(addr: String, psk: String) async throws {
        try await runOnIOThrowing { try $0.connect(addr, psk: psk) }
    }

    func browseServers() async -> [ServerInfo] {
        await runOnIO { client in
            guard let data = client.browseServersJSON(),
                  let dtos = try? JSONDecoder().decode([ServerJSON].self, from: data) else {
                return []
            }
            return dtos.map { $0.toModel() }
        }
    }

    @discardableResult
    func sendText(to: String, text: String) async throws -> String {
        try await runOnIOThrowing { try $0.sendText(to, text: text, group: "") ?? "" }
    }

    @discardableResult
    func offerFile(to: String, path: String) async throws -> String {
        try await runOnIOThrowing { try $0.offerFile(to, path: path) ?? "" }
    }

    func respondFile(transferId: String, accept: Bool, dest: String) async throws {
        try await runOnIOThrowing { try $0.respondFile(transferId, accept: accept, dest: dest) }
    }

    /// 以默认下载目录 + 安全文件名接受入站传输。
    func acceptInbound(transferId: String, name: String) async throws {
        try await respondFile(transferId: transferId, accept: true,
                              dest: joinPath(downloadDir, safeName(name)))
    }

    func cancelFile(_ transferId: String) async throws {
        try await runOnIOThrowing { try $0.cancelFile(transferId) }
    }

    func setNickname(_ name: String) async throws {
        try await runOnIOThrowing { try $0.setNickname(name) }
    }

    // MARK: - 线程包装

    private func runOnIO<T>(_ block: @Sendable (LCMobileClient) -> T) async -> T {
        await withCheckedContinuation { cont in
            DispatchQueue.global(qos: .userInitiated).async { [client] in
                guard let client else {
                    fatalError("ChatEngine used before start()")
                }
                cont.resume(returning: block(client))
            }
        }
    }

    private func runOnIOThrowing<T>(_ block: @Sendable (LCMobileClient) throws -> T) async throws -> T {
        try await withCheckedThrowingContinuation { cont in
            DispatchQueue.global(qos: .userInitiated).async { [client] in
                guard let client else {
                    cont.resume(throwing: EngineError.notStarted)
                    return
                }
                do {
                    cont.resume(returning: try block(client))
                } catch {
                    cont.resume(throwing: error)
                }
            }
        }
    }
}

enum EngineError: LocalizedError {
    case notStarted
    var errorDescription: String? {
        switch self {
        case .notStarted: return "核心尚未启动"
        }
    }
}

// MARK: - JSON DTO（字段对应 internal/appapi 的 json tag）

struct PeerJSON: Decodable {
    let id: String
    let nickname: String
    let os: String
    let status: String

    func toModel() -> Peer {
        Peer(id: id, nickname: nickname, os: os, status: status)
    }
}

struct ServerJSON: Decodable {
    let name: String
    let id: String
    let addr: String
    let version: String
    let authMode: String?

    func toModel() -> ServerInfo {
        ServerInfo(name: name, id: id, addr: addr, version: version,
                   authMode: (authMode?.isEmpty ?? true) ? "psk" : authMode!)
    }
}

struct TransferJSON: Decodable {
    let id: String
    let direction: String
    let state: String
    let kind: String
    let peerId: String?
    let groupId: String?
    let name: String
    let size: Int64
    let bytesDone: Int64
    let speedBps: Int64
    let viaRelay: Bool
    let errorReason: String?

    func toModel() -> Transfer {
        Transfer(id: id, direction: direction, state: state, kind: kind,
                 peerId: peerId ?? "", groupId: groupId ?? "", name: name,
                 size: size, bytesDone: bytesDone, speedBps: speedBps,
                 viaRelay: viaRelay, errorReason: errorReason ?? "")
    }
}

struct ChannelJSON: Decodable {
    let id: String
    let name: String
    let ownerId: String
    let members: [String]?

    func toModel() -> Channel {
        Channel(id: id, name: name, ownerId: ownerId, members: members ?? [])
    }
}

struct StateJSON: Decodable {
    let conn: String
    let server: String?
    let selfId: String?
    let nickname: String
    let peers: [PeerJSON]?
    let transfers: [TransferJSON]?
    let channels: [ChannelJSON]?

    func toModel() -> FullState {
        FullState(
            conn: conn.isEmpty ? "disconnected" : conn,
            server: server ?? "",
            selfId: selfId ?? "",
            nickname: nickname,
            peers: peers?.map { $0.toModel() } ?? [],
            transfers: transfers?.map { $0.toModel() } ?? [],
            channels: channels?.map { $0.toModel() } ?? []
        )
    }
}

/// 拼接目录与文件名。
private func joinPath(_ dir: String, _ name: String) -> String {
    dir.hasSuffix("/") ? dir + name : dir + "/" + name
}

/// 去除路径分隔符，避免文件名逃逸下载目录。
private func safeName(_ name: String) -> String {
    let base = name.split(separator: "/").last
        .flatMap { $0.split(separator: "\\").last }
        .map(String.init) ?? ""
    return base.isEmpty ? "lanchat-received.bin" : base
}
