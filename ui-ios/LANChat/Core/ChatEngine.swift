import Foundation

// gomobile Client（LCMobileClient）的 Swift 封装。
//
// gomobile 把 Go 包绑定为 ObjC（LC* 前缀），经桥接头导入 Swift：
//   - 门面包级函数 NewClient  -> LCMobileNewClient(...) / LCMobileClient(init:...)
//   - *mobile.Client          -> LCMobileClient
//   - mobile.Listener 协议    -> LCMobileListenerProtocol
//     （同名 @interface LCMobileListener 是供子类化的基类）
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
    case groupMatrix(groupId: String, transferId: String, haveBitmap: Data)
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
        client = LCMobileClient(nickname, osName: "ios",
                                downloadDir: downloadDir, listener: bridge)
    }

    func close() {
        client?.close()
        client = nil
    }

    // ---- 同步阻塞 API 包一层后台线程 + async ----

    func getState() async -> FullState {
        await runOnIO { c in
            guard let data = c.getStateJSON(),
                  let dto = try? JSONDecoder().decode(StateJSON.self, from: data) else {
                return FullState(conn: "disconnected", server: "", selfId: "", nickname: "",
                                 peers: [], transfers: [], channels: [], messages: [])
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
        try await runOnIOThrowing { c in
            var err: NSError?
            let msgID = c.sendText(to, text: text, group: "", error: &err)
            if let err { throw err }
            return msgID
        }
    }

    /// 频道群聊文本：group 为频道 ID，to 留空（对齐 core.SendText 语义）。
    @discardableResult
    func sendGroupText(group: String, text: String) async throws -> String {
        // gomobile 把 (string, error) 生成成 error: NSErrorPointer 出参，不自动 throw。
        try await runOnIOThrowing { c in
            var err: NSError?
            let msgID = c.sendText("", text: text, group: group, error: &err)
            if let err { throw err }
            return msgID
        }
    }

    @discardableResult
    func sendSticker(to: String, path: String) async throws -> String {
        try await runOnIOThrowing { c in
            var err: NSError?
            let msgID = c.sendSticker(to, path: path, error: &err)
            if let err { throw err }
            return msgID
        }
    }

    @discardableResult
    func offerFile(to: String, path: String) async throws -> String {
        try await runOnIOThrowing { c in
            var err: NSError?
            let transferID = c.offerFile(to, path: path, error: &err)
            if let err { throw err }
            return transferID
        }
    }

    /// 频道群文件（kind=channel）；底层就绪前由 Go 返回错误。
    @discardableResult
    func offerFileToGroup(group: String, path: String) async throws -> String {
        try await runOnIOThrowing { c in
            var err: NSError?
            let transferID = c.offerFile(toGroup: group, path: path, error: &err)
            if let err { throw err }
            return transferID
        }
    }

    @discardableResult
    func channelCreate(name: String, topic: String, private isPrivate: Bool) async throws -> String {
        try await runOnIOThrowing { c in
            var err: NSError?
            let channelID = c.channelCreate(name, topic: topic, private: isPrivate, error: &err)
            if let err { throw err }
            return channelID
        }
    }

    func channelJoin(channelID: String) async throws {
        try await runOnIOThrowing { try $0.channelJoin(channelID) }
    }

    /// 邀请在线成员加入频道（仅 owner）。
    func channelInvite(channelID: String, memberID: String) async throws {
        try await runOnIOThrowing { try $0.channelInvite(channelID, memberID: memberID) }
    }

    /// 退出频道。
    func channelLeave(channelID: String) async throws {
        try await runOnIOThrowing { try $0.channelLeave(channelID) }
    }

    func channelList() async -> [Channel] {
        await runOnIO { client in
            guard let data = client.channelListJSON(),
                  let dtos = try? JSONDecoder().decode([ChannelJSON].self, from: data) else {
                return []
            }
            return dtos.map { $0.toModel() }
        }
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

    func pauseFile(_ transferId: String) async throws {
        try await runOnIOThrowing { try $0.pauseFile(transferId) }
    }

    func resumeFile(_ transferId: String) async throws {
        try await runOnIOThrowing { try $0.resumeFile(transferId) }
    }

    func pickDownloadDir(_ path: String) async throws {
        try await runOnIOThrowing { try $0.pickDownloadDir(path) }
    }

    func setNickname(_ name: String) async throws {
        try await runOnIOThrowing { try $0.setNickname(name) }
    }

    // MARK: - 契约 v1.3 显式协作绑定方法

    @discardableResult
    func startVoiceCall(to: String, group: String = "") async throws -> String {
        try await runOnIOThrowing { c in
            var err: NSError?
            let status = c.startVoiceCall(to, group: group, error: &err)
            if let err { throw err }
            return status
        }
    }

    @discardableResult
    func acceptVoiceCall(to: String, group: String = "", remoteUDP: String = "") async throws -> String {
        try await runOnIOThrowing { c in
            var err: NSError?
            let status = c.acceptVoiceCall(to, group: group, peerAddr: remoteUDP, error: &err)
            if let err { throw err }
            return status
        }
    }

    func stopVoiceCall(to: String, group: String = "") async throws {
        try await runOnIOThrowing { try $0.stopVoiceCall(to, group: group) }
    }

    func setVoiceMuted(_ muted: Bool) async {
        await runOnIO { $0.setVoiceMuted(muted) }
    }

    @discardableResult
    func startScreenShare(to: String, group: String = "", fps: Int = 15) async throws -> String {
        try await runOnIOThrowing { c in
            var err: NSError?
            let status = c.startScreenShare(to, group: group, fps: fps, error: &err)
            if let err { throw err }
            return status
        }
    }

    func stopScreenShare(to: String, group: String = "") async throws {
        try await runOnIOThrowing { try $0.stopScreenShare(to, group: group) }
    }

    func sendScreenAnnotation(to: String, group: String = "", normX: Double, normY: Double, note: String = "") async throws {
        try await runOnIOThrowing { try $0.sendScreenAnnotation(to, group: group, normX: normX, normY: normY, note: note) }
    }

    @discardableResult
    func syncFolder(to: String, group: String = "", dirPath: String) async throws -> Int {
        try await runOnIOThrowing { c in
            var synced: Int = 0
            try c.syncFolder(to, group: group, dirPath: dirPath, ret0_: &synced)
            return synced
        }
    }

    @discardableResult
    func issueTrustCert(subject: String, org: String = "LANChat", ttlSec: Int64 = 86400) async throws -> String {
        try await runOnIOThrowing { c in
            var err: NSError?
            let tok = c.issueTrustCert(subject, org: org, ttlSec: ttlSec, error: &err)
            if let err { throw err }
            return tok
        }
    }

    @discardableResult
    func verifyTrustCert(token: String, rootPub: String = "") async throws -> String {
        try await runOnIOThrowing { c in
            var err: NSError?
            let ident = c.verifyTrustCert(token, rootPub: rootPub, error: &err)
            if let err { throw err }
            return ident
        }
    }

    func runNetworkDiagnostics() async -> String {
        await runOnIO { $0.runNetworkDiagnostics() }
    }

    // MARK: - 契约 v1.4 消息富交互与安全控制显式 RPC 绑定方法

    func revokeMessage(to: String, group: String = "", msgID: String) async throws {
        try await runOnIOThrowing { try $0.revokeMessage(to, group: group, msgID: msgID) }
    }

    func reactMessage(to: String, group: String = "", msgID: String, emoji: String = "👍") async throws {
        try await runOnIOThrowing { try $0.reactMessage(to, group: group, msgID: msgID, emoji: emoji) }
    }

    func editMessage(to: String, group: String = "", msgID: String, newText: String) async throws {
        try await runOnIOThrowing { try $0.editMessage(to, group: group, msgID: msgID, newText: newText) }
    }

    func pinMessage(to: String, group: String = "", msgID: String) async throws {
        try await runOnIOThrowing { try $0.pinMessage(to, group: group, msgID: msgID) }
    }

    func markMessageRead(to: String, group: String = "", msgID: String) async throws {
        try await runOnIOThrowing { try $0.markMessageRead(to, group: group, msgID: msgID) }
    }

    func setSessionE2EE(_ secret: String) async throws {
        try await runOnIOThrowing { try $0.setSessionE2EE(secret) }
    }

    func setAutoClipboard(_ enabled: Bool) async {
        await runOnIO { $0.setAutoClipboard(enabled) }
    }

    func setDND(_ enabled: Bool) async {
        await runOnIO { $0.setDND(enabled) }
    }

    func getHistoryPage(convID: String, beforeTS: Int64 = 0, limit: Int = 50, selfId: String = "") async -> [ChatMessage] {
        await runOnIO { c in
            guard let data = c.historyPageJSON(convID, beforeTs: beforeTS, limit: limit),
                  let dtos = try? JSONDecoder().decode([HistoryMessageJSON].self, from: data) else {
                return []
            }
            return dtos.map { $0.toModel(selfId: selfId) }
        }
    }

    // MARK: - 契约 v1.5 系统与高级消息显式 RPC 绑定方法

    @discardableResult
    func verifyPeerFingerprint(peerID: String) async -> String {
        await runOnIO { $0.verifyPeerFingerprint(peerID) }
    }

    @discardableResult
    func sendBurnMessage(to: String, group: String = "", text: String, ttlSec: Int = 10) async throws -> String {
        try await runOnIOThrowing { c in
            var err: NSError?
            let msgID = c.sendBurnMessage(to, group: group, text: text, ttlSec: ttlSec, error: &err)
            if let err { throw err }
            return msgID
        }
    }

    @discardableResult
    func syncHistoryWithPeer(to: String, group: String = "", sinceTS: Int64 = 0) async throws -> Int {
        try await runOnIOThrowing { c in
            var synced: Int = 0
            try c.syncHistory(withPeer: to, group: group, sinceTS: sinceTS, ret0_: &synced)
            return synced
        }
    }

    func setBandwidthLimit(_ limitMBps: Double) async {
        await runOnIO { _ = $0.setBandwidthLimit(limitMBps) }
    }

    @discardableResult
    func checkSilentUpdate(proxy: String = "", download: Bool = false) async throws -> String {
        try await runOnIOThrowing { c in
            var err: NSError?
            let summary = c.checkSilentUpdate(proxy, download: download, error: &err)
            if let err { throw err }
            return summary
        }
    }

    // MARK: - 契约 v1.6 频道权限治理与加密离线投递箱显式 RPC 绑定方法

    func channelManageMember(channelID: String, memberID: String = "", action: String) async throws {
        try await runOnIOThrowing { try $0.channelManageMember(channelID, memberID: memberID, action: action) }
    }

    @discardableResult
    func enqueueOfflineMailbox(to: String, text: String, recipientPub: String = "") async throws -> String {
        try await runOnIOThrowing { c in
            var err: NSError?
            let msgID = c.enqueueOfflineMailbox(to, text: text, recipientPub: recipientPub, error: &err)
            if let err { throw err }
            return msgID
        }
    }

    @discardableResult
    func drainOfflineMailbox(fromPeer: String = "") async -> String {
        await runOnIO { $0.drainOfflineMailbox(fromPeer) }
    }

    // MARK: - 契约 v1.7 多设备配对、频道 Webhook 机器人与加密归档显式 RPC 绑定方法

    @discardableResult
    func pairDevice(action: String = "generate", code: String = "", peerID: String = "") async throws -> String {
        try await runOnIOThrowing { c in
            var err: NSError?
            let res = c.pairDevice(action, code: code, peerID: peerID, error: &err)
            if let err { throw err }
            return res
        }
    }

    @discardableResult
    func listPairedDevices(filter: String = "") async -> String {
        await runOnIO { $0.listPairedDevices(filter) }
    }

    @discardableResult
    func manageChannelWebhook(
        channelID: String,
        action: String = "create",
        name: String = "CI-Bot",
        listenAddr: String = "127.0.0.1:0"
    ) async throws -> String {
        try await runOnIOThrowing { c in
            var err: NSError?
            let res = c.manageChannelWebhook(channelID, action: action, name: name, listenAddr: listenAddr, error: &err)
            if let err { throw err }
            return res
        }
    }

    @discardableResult
    func exportHistoryArchive(destPath: String, passphrase: String) async throws -> String {
        try await runOnIOThrowing { c in
            var err: NSError?
            let res = c.exportHistoryArchive(destPath, passphrase: passphrase, error: &err)
            if let err { throw err }
            return res
        }
    }

    @discardableResult
    func importHistoryArchive(srcPath: String, passphrase: String) async throws -> String {
        try await runOnIOThrowing { c in
            var err: NSError?
            let res = c.importHistoryArchive(srcPath, passphrase: passphrase, error: &err)
            if let err { throw err }
            return res
        }
    }

    // MARK: - 线程包装

    private func runOnIO<T>(_ block: @escaping @Sendable (LCMobileClient) -> T) async -> T {
        await withCheckedContinuation { cont in
            DispatchQueue.global(qos: .userInitiated).async { [client] in
                guard let client else {
                    fatalError("ChatEngine used before start()")
                }
                cont.resume(returning: block(client))
            }
        }
    }

    private func runOnIOThrowing<T>(_ block: @escaping @Sendable (LCMobileClient) throws -> T) async throws -> T {
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
    let topic: String?
    let isPrivate: Bool?
    let members: [String]?
    let admins: [String]?
    let muted: [String]?
    let announcementAck: [String]?

    enum CodingKeys: String, CodingKey {
        case id, name, ownerId, topic, members, admins, muted, announcementAck
        case isPrivate = "private"
    }

    func toModel() -> Channel {
        Channel(id: id, name: name, ownerId: ownerId,
                topic: topic ?? "", isPrivate: isPrivate ?? false,
                members: members ?? [],
                admins: admins ?? [],
                muted: muted ?? [],
                announcementAck: announcementAck ?? [])
    }
}

struct HistoryMessageJSON: Decodable {
    let id: String
    let from: String
    let to: String?
    let group: String?
    let type: String
    let text: String
    let replyTo: String?
    let replyQuote: String?
    let mentions: [String]?
    let reactions: [String: [String]]?
    let editedAt: Int64?
    let pinned: Bool?
    let mediaMime: String?
    let mediaPath: String?
    let ts: Int64
    let revoked: Bool?
    let readBy: [String]?

    func toModel(selfId: String) -> ChatMessage {
        let inbound = !selfId.isEmpty ? (from != selfId) : false
        let peer = inbound ? from : ((to?.isEmpty == false) ? to! : from)
        let date = ts > 0 ? Date(timeIntervalSince1970: TimeInterval(ts) / 1000.0) : Date()
        return ChatMessage(id: id, peerId: peer, text: text,
                           inbound: inbound, timestamp: date,
                           group: (group?.isEmpty == false) ? group : nil,
                           revoked: revoked ?? false,
                           readBy: readBy ?? [],
                           replyQuote: replyQuote,
                           reactions: reactions ?? [:],
                           editedAt: editedAt ?? 0,
                           pinned: pinned ?? false,
                           mediaMime: mediaMime,
                           mediaPath: mediaPath)
    }
}

struct VoiceCallStateJSON: Decodable {
    let state: String?
    let peerId: String?
    let groupId: String?
    let localAddr: String?
    let remoteAddr: String?
    let framesIn: Int64?
    let framesOut: Int64?

    func toModel() -> VoiceCallState {
        VoiceCallState(
            state: (state?.isEmpty ?? true) ? "idle" : state!,
            peerId: peerId ?? "",
            groupId: groupId ?? "",
            localAddr: localAddr ?? "",
            remoteAddr: remoteAddr ?? "",
            framesIn: framesIn ?? 0,
            framesOut: framesOut ?? 0
        )
    }
}

struct ScreenShareStateJSON: Decodable {
    let active: Bool?
    let peerId: String?
    let groupId: String?
    let fps: Int?
    let latestFrame: String?

    func toModel() -> ScreenShareState {
        ScreenShareState(
            active: active ?? false,
            peerId: peerId ?? "",
            groupId: groupId ?? "",
            fps: fps ?? 0,
            latestFrame: latestFrame ?? ""
        )
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
    let messages: [HistoryMessageJSON]?
    let voiceCall: VoiceCallStateJSON?
    let screenShare: ScreenShareStateJSON?

    func toModel() -> FullState {
        let myId = selfId ?? ""
        return FullState(
            conn: conn.isEmpty ? "disconnected" : conn,
            server: server ?? "",
            selfId: myId,
            nickname: nickname,
            peers: peers?.map { $0.toModel() } ?? [],
            transfers: transfers?.map { $0.toModel() } ?? [],
            channels: channels?.map { $0.toModel() } ?? [],
            messages: messages?.map { $0.toModel(selfId: myId) } ?? [],
            voiceCall: voiceCall?.toModel(),
            screenShare: screenShare?.toModel()
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
