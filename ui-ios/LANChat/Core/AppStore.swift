import Foundation

/// 中央状态：@MainActor 聚合 core 事件，向 UI 暴露全部异步操作。
@MainActor
final class AppStore: ObservableObject {
    @Published var conn: String = "disconnected"
    @Published var server: String = ""
    @Published var selfId: String = ""
    @Published var peers: [Peer] = []
    @Published var transfers: [Transfer] = []
    @Published var discoveredServers: [ServerInfo] = []
    @Published var messagesByPeer: [String: [ChatMessage]] = [:]
    @Published var channels: [Channel] = []
    @Published var busy = false
    @Published var error: String?

    let settings: SettingsStore
    private let engine: ChatEngine

    init(settings: SettingsStore) {
        self.settings = settings
        settings.ensureDownloadDir()
        engine = ChatEngine(downloadDir: settings.downloadDir)

        engine.onEvent = { [weak self] event in
            // ChatEngine 已保证在主线程回调。
            Task { @MainActor in self?.handle(event) }
        }
    }

    /// 应用启动：创建核心并拉取快照。
    func bootstrap() async {
        engine.start(nickname: settings.nickname)
        await applyState(await engine.getState())
        await browseServers()
    }

    func consumeError() { error = nil }

    func refresh() async {
        await applyState(await engine.getState())
    }

    func browseServers() async {
        discoveredServers = await engine.browseServers()
    }

    func connect(addr: String, psk: String) {
        let target = addr.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !target.isEmpty else {
            error = "请输入服务器地址"
            return
        }
        run {
            try await self.engine.connect(addr: target, psk: psk)
            await self.refresh()
        }
    }

    func applyNickname() {
        run { try await self.engine.setNickname(self.settings.nickname) }
    }

    func sendText(to: String, text: String) {
        let content = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !content.isEmpty else { return }
        run {
            let msgId = try await self.engine.sendText(to: to, text: content)
            self.appendMessage(ChatMessage(id: msgId, peerId: to, text: content,
                                           inbound: false, timestamp: Date()))
        }
    }

    /// 发送文件；path 须在 security-scoped 访问期间可用。
    func offerFile(to: String, path: String) {
        run {
            _ = try await self.engine.offerFile(to: to, path: path)
            await self.refresh()
        }
    }

    // MARK: - 频道（M9）

    /// 创建频道；成功后由 channel.updated 事件驱动刷新。
    func createChannel(name: String, topic: String, isPrivate: Bool) {
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            error = "请输入频道名称"
            return
        }
        let cleanedTopic = topic.trimmingCharacters(in: .whitespacesAndNewlines)
        run {
            _ = try await self.engine.channelCreate(name: trimmed,
                                                    topic: cleanedTopic,
                                                    private: isPrivate)
        }
    }

    func joinChannel(_ channel: Channel) {
        run {
            try await self.engine.channelJoin(channelID: channel.id)
            await self.refresh()
        }
    }

    /// owner 邀请在线成员加入频道。
    func inviteMember(_ channel: Channel, memberID: String) {
        run {
            try await self.engine.channelInvite(channelID: channel.id, memberID: memberID)
            await self.refresh()
        }
    }

    /// 退出频道。
    func leaveChannel(_ channel: Channel) {
        run {
            try await self.engine.channelLeave(channelID: channel.id)
            await self.refresh()
        }
    }

    /// 频道群文本：按 group(频道ID) 归类。
    func sendGroupText(channel: Channel, text: String) {
        let content = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !content.isEmpty else { return }
        run {
            let msgId = try await self.engine.sendGroupText(group: channel.id, text: content)
            self.appendMessage(ChatMessage(id: msgId, peerId: self.selfId, text: content,
                                           inbound: false, timestamp: Date(), group: channel.id))
        }
    }

    /// 频道群文件；path 须在 security-scoped 访问期间可用。
    func offerFileToGroup(channel: Channel, path: String) {
        run {
            _ = try await self.engine.offerFileToGroup(group: channel.id, path: path)
            await self.refresh()
        }
    }

    /// 当前用户是否为频道成员。
    func isMember(_ channel: Channel) -> Bool {
        !selfId.isEmpty && channel.members.contains(selfId)
    }

    /// 由用户 ID 解析展示名（频道消息 / 成员列表使用）。
    func displayName(_ id: String) -> String {
        if id == selfId { return settings.nickname }
        return peers.first { $0.id == id }?.nickname ?? id
    }

    func acceptTransfer(_ t: Transfer) {
        run {
            try await self.engine.acceptInbound(transferId: t.id, name: t.name)
            await self.refresh()
        }
    }

    func rejectTransfer(_ t: Transfer) {
        run {
            try await self.engine.respondFile(transferId: t.id, accept: false, dest: "")
            await self.refresh()
        }
    }

    func cancelTransfer(_ t: Transfer) {
        run {
            try await self.engine.cancelFile(t.id)
            await self.refresh()
        }
    }

    // MARK: - 事件处理

    private func handle(_ event: CoreEvent) {
        switch event {
        case let .connChanged(state, reason):
            conn = state
            if state == "connected" {
                Task { await refresh() }
            }
            if state == "auth_failed" {
                error = reason.isEmpty ? "口令错误，鉴权失败" : reason
            }

        case let .peerJoined(peer):
            peers.removeAll { $0.id == peer.id }
            peers.append(peer)

        case let .peerLeft(id):
            peers.removeAll { $0.id == id }

        case let .message(from, group, msgId, _, text):
            // 频道消息按 group(频道ID) 归类；group 为空则按 1:1 对端归类。
            appendMessage(ChatMessage(id: msgId, peerId: from, text: text,
                                      inbound: true, timestamp: Date(),
                                      group: group.isEmpty ? nil : group))

        case let .progress(t):
            upsertTransfer(t)

        case let .done(id):
            if let idx = transfers.firstIndex(where: { $0.id == id }) {
                transfers[idx].state = "done"
                transfers[idx].speedBps = 0
            }

        case let .failed(id, reason):
            if let idx = transfers.firstIndex(where: { $0.id == id }) {
                transfers[idx].state = "failed"
                transfers[idx].errorReason = reason
            }

        case .channelsUpdated:
            Task { await refresh() }
        }
    }

    private func applyState(_ s: FullState) async {
        conn = s.conn
        server = s.server
        selfId = s.selfId
        peers = s.peers
        transfers = s.transfers
        channels = s.channels
    }

    private func upsertTransfer(_ t: Transfer) {
        transfers.removeAll { $0.id == t.id }
        transfers.append(t)
        transfers.sort { $0.id < $1.id }
    }

    private func appendMessage(_ m: ChatMessage) {
        let list = (messagesByPeer[m.peerId] ?? []) + [m]
        messagesByPeer[m.peerId] = list
    }

    private func run(_ block: @escaping () async throws -> Void) {
        Task {
            busy = true
            do {
                try await block()
            } catch {
                self.error = error.localizedDescription
            }
            busy = false
        }
    }
}
