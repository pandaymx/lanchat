import Foundation

/// 中央状态：持有 daemon + IPC，聚合连接态 / 在线表 / 传输 / 聊天消息，
/// 并把 appapi 方法以 async 形式暴露给 UI。
@MainActor
final class ChatStore: ObservableObject {
    @Published var conn: String = "disconnected"
    @Published var server: String = ""
    @Published var selfId: String = ""
    @Published var peers: [Peer] = []
    @Published var transfers: [Transfer] = []
    @Published var channels: [Channel] = []
    @Published var servers: [ServerInfo] = []
    @Published var messages: [String: [ChatMessage]] = [:]
    @Published var daemonConnected = false
    @Published var error: String?
    @Published var ready = false

    private let settings: SettingsStore
    private let launcher: DaemonLauncher
    private let ipc: IpcClient

    init(settings: SettingsStore) {
        self.settings = settings
        let socket = SettingsStore.socketPath()
        launcher = DaemonLauncher(socketPath: socket)
        ipc = IpcClient(path: socket)
        ipc.onNotification = { [weak self] method, params in
            Task { @MainActor in self?.handle(method: method, params: params) }
        }
        ipc.onConnChanged = { [weak self] connected in
            Task { @MainActor in
                self?.daemonConnected = connected
                if connected { await self?.refreshState() }
            }
        }
    }

    func bootstrap() async {
        guard !ready else { return }
        ready = true
        try? FileManager.default.createDirectory(
            atPath: settings.downloadDir, withIntermediateDirectories: true)
        do {
            try await launcher.ensureRunning(
                nickname: settings.nickname, downloadDir: settings.downloadDir)
            ipc.start()
        } catch {
            self.error = error.localizedDescription
        }
    }

    // MARK: - 通知处理

    private struct ConnParams: Decodable { let state: String; let reason: String? }
    private struct PeerParams: Decodable { let peer: Peer }
    private struct PeerLeftParams: Decodable { let peerId: String }
    private struct MsgParams: Decodable {
        let from: String; let group: String?; let msgId: String
        let type: String; let text: String
    }
    private struct ProgressParams: Decodable { let transfer: Transfer }
    private struct TransferIdParams: Decodable { let transferId: String; let reason: String? }
    private struct ChannelsParams: Decodable { let channels: [Channel] }

    private func handle(method: String, params: Data) {
        let decoder = JSONDecoder()
        switch method {
        case "conn.changed":
            if let p = try? decoder.decode(ConnParams.self, from: params) {
                conn = p.state
            }
        case "peer.joined":
            if let p = try? decoder.decode(PeerParams.self, from: params) {
                if let idx = peers.firstIndex(where: { $0.id == p.peer.id }) {
                    peers[idx] = p.peer
                } else {
                    peers.append(p.peer)
                }
            }
        case "peer.left":
            if let p = try? decoder.decode(PeerLeftParams.self, from: params) {
                peers.removeAll { $0.id == p.peerId }
            }
        case "msg.received":
            if let p = try? decoder.decode(MsgParams.self, from: params) {
                append(ChatMessage(
                    id: p.msgId, peerId: p.from, text: p.text,
                    inbound: true, timestamp: Date()),
                       threadKey: p.group)
            }
        case "transfer.progress":
            if let p = try? decoder.decode(ProgressParams.self, from: params) {
                upsertTransfer(p.transfer)
            }
        case "transfer.done":
            if let p = try? decoder.decode(TransferIdParams.self, from: params) {
                markTransfer(id: p.transferId, state: "done")
            }
        case "transfer.failed":
            if let p = try? decoder.decode(TransferIdParams.self, from: params) {
                markTransfer(id: p.transferId, state: "failed", reason: p.reason)
            }
        case "channel.updated":
            if let p = try? decoder.decode(ChannelsParams.self, from: params) {
                channels = p.channels
            }
        default:
            break
        }
    }

    private func append(_ msg: ChatMessage, threadKey: String? = nil) {
        let key = (threadKey?.isEmpty == false) ? threadKey! : msg.peerId
        var list = messages[key] ?? []
        list.append(msg)
        messages[key] = list
    }

    private func upsertTransfer(_ t: Transfer) {
        if let idx = transfers.firstIndex(where: { $0.id == t.id }) {
            transfers[idx] = t
        } else {
            transfers.append(t)
        }
    }

    private func markTransfer(id: String, state: String, reason: String? = nil) {
        guard let idx = transfers.firstIndex(where: { $0.id == id }) else { return }
        var t = transfers[idx]
        t.state = state
        if let reason { t.errorReason = reason }
        transfers[idx] = t
    }

    // MARK: - API 操作

    func refreshState() async {
        guard let state = try? await ipc.call("GetState", params: nil, as: FullState.self) else { return }
        conn = state.conn
        server = state.server ?? ""
        selfId = state.selfId ?? ""
        peers = state.peers
        transfers = state.transfers
        channels = state.channels
    }

    func browseServers() async {
        if let list = try? await ipc.call("BrowseServers", params: nil, as: [ServerInfo].self) {
            servers = list
        }
    }

    func connect(addr: String, psk: String) async {
        do {
            _ = try await ipc.call("Connect", params: ["addr": addr, "psk": psk])
        } catch {
            self.error = error.localizedDescription
        }
    }

    func sendText(to peerId: String, text: String) async {
        guard !text.isEmpty else { return }
        do {
            let r = try await ipc.call("SendText", params: ["to": peerId, "text": text, "group": ""], as: MsgIDResult.self)
            append(ChatMessage(id: r.msgID, peerId: peerId, text: text, inbound: false, timestamp: Date()))
        } catch {
            self.error = error.localizedDescription
        }
    }

    func offerFile(to peerId: String, path: String) async {
        do {
            _ = try await ipc.call("OfferFile", params: ["to": peerId, "path": path], as: TransferIDResult.self)
            await refreshState()
        } catch {
            self.error = error.localizedDescription
        }
    }

    // MARK: - 频道（G2 自定义频道，方法集合严格对齐 ipc.schema.json）

    func createChannel(name: String, topic: String, isPrivate: Bool) async {
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }
        let trimmedTopic = topic.trimmingCharacters(in: .whitespacesAndNewlines)
        do {
            _ = try await ipc.call("ChannelCreate",
                                   params: ["name": trimmed,
                                            "topic": trimmedTopic,
                                            "private": isPrivate],
                                   as: ChannelIDResult.self)
            await refreshState()
        } catch {
            self.error = error.localizedDescription
        }
    }

    func joinChannel(_ channelID: String) async {
        do {
            _ = try await ipc.call("ChannelJoin", params: ["channelID": channelID])
            await refreshState()
        } catch {
            self.error = error.localizedDescription
        }
    }

    /// 邀请在线成员加入频道（后端仅允许 owner，越权会返回错误提示）。
    func inviteMember(channelID: String, memberID: String) async {
        do {
            _ = try await ipc.call("ChannelInvite",
                                   params: ["channelID": channelID,
                                            "memberID": memberID])
            await refreshState()
        } catch {
            self.error = error.localizedDescription
        }
    }

    /// 退出频道；退出后频道消息不再对本端可见。
    func leaveChannel(channelID: String) async {
        do {
            _ = try await ipc.call("ChannelLeave", params: ["channelID": channelID])
            messages[channelID] = nil
            await refreshState()
        } catch {
            self.error = error.localizedDescription
        }
    }

    /// 频道群文本：group 为频道 ID；to 留空（group 优先）。
    func sendChannelText(channelID: String, text: String) async {
        guard !text.isEmpty else { return }
        do {
            let r = try await ipc.call(
                "SendText",
                params: ["to": "", "text": text, "group": channelID],
                as: MsgIDResult.self)
            append(ChatMessage(id: r.msgID, peerId: selfId, text: text,
                               inbound: false, timestamp: Date()),
                   threadKey: channelID)
        } catch {
            self.error = error.localizedDescription
        }
    }

    func offerFileToChannel(channelID: String, path: String) async {
        do {
            _ = try await ipc.call("OfferFileToGroup",
                                   params: ["group": channelID, "path": path],
                                   as: TransferIDResult.self)
            await refreshState()
        } catch {
            self.error = error.localizedDescription
        }
    }

    func isMember(_ channel: Channel) -> Bool {
        channel.members.contains(selfId)
    }

    func respondFile(transferId: String, accept: Bool) async {
        do {
            _ = try await ipc.call("RespondFile",
                                   params: ["transferID": transferId, "accept": accept,
                                            "dest": settings.downloadDir])
            if accept {
                try? FileManager.default.createDirectory(
                    atPath: settings.downloadDir, withIntermediateDirectories: true)
            }
            await refreshState()
        } catch {
            self.error = error.localizedDescription
        }
    }

    func cancelFile(_ transferId: String) async {
        do {
            _ = try await ipc.call("CancelFile", params: ["transferID": transferId])
            await refreshState()
        } catch {
            self.error = error.localizedDescription
        }
    }

    func setNickname(_ name: String) async {
        do {
            _ = try await ipc.call("SetNickname", params: ["name": name])
            settings.nickname = name
        } catch {
            self.error = error.localizedDescription
        }
    }

    func shutdown() {
        ipc.stop()
        if settings.stopDaemonOnExit {
            launcher.stopDaemonIfOwned()
        }
    }
}
