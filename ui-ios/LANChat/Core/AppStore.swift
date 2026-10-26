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
    @Published var matrixSummary: [String: String] = [:]
    @Published var channels: [Channel] = []
    @Published var updateStatus: String = "内置国内加速节点：ghfast.top · gh-proxy.com · ghproxy.net · mirror.ghproxy.com · ghps.cc"
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
        try? await engine.pickDownloadDir(settings.downloadDir)
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
        let trimmed = addr.trimmingCharacters(in: .whitespacesAndNewlines)
        let target = trimmed.isEmpty ? "auto" : trimmed
        run {
            try await self.engine.connect(addr: target, psk: psk)
            await self.refresh()
        }
    }

    func applyNickname() {
        run { try await self.engine.setNickname(self.settings.nickname) }
    }

    func checkAndSilentUpdate(customProxy: String = "", checkOnly: Bool = false) {
        let proxyArg = customProxy.trimmingCharacters(in: .whitespacesAndNewlines)
        let cmd: String
        if checkOnly {
            cmd = "/update check"
            updateStatus = "正在通过国内加速节点检查最新版本…"
        } else if proxyArg.isEmpty {
            cmd = "/update"
            updateStatus = "正在通过国内加速代理静默下载更新包（免浏览器）…"
        } else {
            cmd = "/update \(proxyArg)"
            updateStatus = "正在通过 \(proxyArg) 静默下载更新包…"
        }
        run {
            let msgId = try await self.engine.sendText(to: "", text: cmd)
            if msgId.hasPrefix("update:") {
                self.updateStatus = String(msgId.dropFirst(7))
            } else {
                self.updateStatus = msgId
            }
            await self.refresh()
        }
    }

    func sendText(to: String, text: String) {
        let content = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !content.isEmpty else { return }
        if content.hasPrefix("/search ") {
            let q = String(content.dropFirst(8)).trimmingCharacters(in: .whitespaces)
            let hits = searchMessages(in: to, query: q)
            self.updateStatus = "搜索“\(q)”：共 \(hits.count) 条匹配"
            return
        }
        run {
            let msgId = try await self.engine.sendText(to: to, text: content)
            if msgId == "e2ee:on" || msgId == "e2ee:off" {
                return
            }
            if msgId.hasPrefix("fp:") {
                let fp = String(msgId.dropFirst(3))
                self.error = "🛡️ 安全码指纹核验：\(fp)"
                return
            }
            if msgId.hasPrefix("bwlimit:") {
                let lim = String(msgId.dropFirst(8))
                self.updateStatus = "⚡ 带宽限速已设置为 \(lim) MB/s"
                return
            }
            if msgId.hasPrefix("status:") {
                let st = String(msgId.dropFirst(7))
                self.updateStatus = "📊 诊断状态：\(st)"
                return
            }
            if msgId.hasPrefix("update:") {
                let up = String(msgId.dropFirst(7))
                self.updateStatus = up
                return
            }
            if content.hasPrefix("/revoke ") {
                let targetId = String(content.dropFirst(8)).trimmingCharacters(in: .whitespaces)
                self.revokeMessage(id: targetId)
                return
            }
            if content.hasPrefix("/read ") {
                let targetId = String(content.dropFirst(6)).trimmingCharacters(in: .whitespaces)
                self.markRead(id: targetId, reader: self.selfId)
                return
            }
            if content.hasPrefix("/react ") || content.hasPrefix("/edit ") || content.hasPrefix("/pin ") {
                await self.refresh()
                return
            }
            self.appendMessage(ChatMessage(id: msgId, peerId: to, text: self.formatReplyText(content),
                                           inbound: false, timestamp: Date()))
        }
    }

    func sendSticker(to: String, path: String, group: String? = nil) {
        run {
            let msgId = try await self.engine.sendSticker(to: to, path: path)
            let name = URL(fileURLWithPath: path).lastPathComponent
            self.appendMessage(ChatMessage(id: msgId, peerId: to, text: "[贴纸: \(name)]",
                                           inbound: false, timestamp: Date(), group: group))
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
        if content.hasPrefix("/search ") {
            let q = String(content.dropFirst(8)).trimmingCharacters(in: .whitespaces)
            let hits = searchMessages(in: channel.id, query: q)
            self.updateStatus = "搜索“\(q)”：共 \(hits.count) 条匹配"
            return
        }
        run {
            let msgId = try await self.engine.sendGroupText(group: channel.id, text: content)
            if msgId == "e2ee:on" || msgId == "e2ee:off" {
                return
            }
            if content.hasPrefix("/revoke ") {
                let targetId = String(content.dropFirst(8)).trimmingCharacters(in: .whitespaces)
                self.revokeMessage(id: targetId)
                return
            }
            if content.hasPrefix("/read ") {
                let targetId = String(content.dropFirst(6)).trimmingCharacters(in: .whitespaces)
                self.markRead(id: targetId, reader: self.selfId)
                return
            }
            if content.hasPrefix("/react ") || content.hasPrefix("/edit ") || content.hasPrefix("/pin ") {
                await self.refresh()
                return
            }
            self.appendMessage(ChatMessage(id: msgId, peerId: self.selfId, text: self.formatReplyText(content),
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

    func pauseTransfer(_ t: Transfer) {
        run {
            try await self.engine.pauseFile(t.id)
            await self.refresh()
        }
    }

    func resumeTransfer(_ t: Transfer) {
        run {
            try await self.engine.resumeFile(t.id)
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

        case let .message(from, group, msgId, type, text):
            if type == "MSG_REVOKE" {
                revokeMessage(id: msgId)
                return
            }
            if type == "MSG_READ" {
                markRead(id: msgId, reader: from)
                return
            }
            if type == "MSG_REACT" || type == "MSG_EDIT" || type == "MSG_PIN" {
                updateMessageText(id: msgId, newText: text)
                return
            }
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

        case let .groupMatrix(_, transferId, haveBitmap):
            let size = transfers.first(where: { $0.id == transferId })?.size ?? 0
            let summary = formatMatrixSummary(haveBitmap: haveBitmap, size: size)
            if !summary.isEmpty {
                matrixSummary[transferId] = summary
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
        for m in s.messages {
            appendMessage(m)
        }
    }

    private func upsertTransfer(_ t: Transfer) {
        transfers.removeAll { $0.id == t.id }
        transfers.append(t)
        transfers.sort { $0.id < $1.id }
    }

    private func appendMessage(_ m: ChatMessage) {
        if QQTheme.isSystemDiagnosticMessage(id: m.id, text: m.text) {
            return
        }
        let key = (m.group?.isEmpty == false) ? m.group! : m.peerId
        var list = messagesByPeer[key] ?? []
        if let idx = list.firstIndex(where: { $0.id == m.id }) {
            list[idx] = m
        } else {
            list.append(m)
        }
        messagesByPeer[key] = list
    }

    private func revokeMessage(id: String) {
        guard !id.isEmpty else { return }
        for (key, var list) in messagesByPeer {
            if let idx = list.firstIndex(where: { $0.id == id }) {
                list[idx].revoked = true
                list[idx].text = "消息已撤回"
                messagesByPeer[key] = list
            }
        }
    }

    private func updateMessageText(id: String, newText: String) {
        guard !id.isEmpty else { return }
        for (key, var list) in messagesByPeer {
            if let idx = list.firstIndex(where: { $0.id == id && !$0.revoked }) {
                list[idx].text = newText
                messagesByPeer[key] = list
            }
        }
    }

    private func markRead(id: String, reader: String) {
        guard !id.isEmpty, !reader.isEmpty else { return }
        for (key, var list) in messagesByPeer {
            if let idx = list.firstIndex(where: { $0.id == id }) {
                if !list[idx].readBy.contains(reader) {
                    list[idx].readBy.append(reader)
                    messagesByPeer[key] = list
                }
            }
        }
    }

    func searchMessages(in key: String, query: String) -> [ChatMessage] {
        let q = query.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !q.isEmpty else { return [] }
        return (messagesByPeer[key] ?? []).filter { !$0.revoked && $0.text.lowercased().contains(q) }
    }

    private func formatReplyText(_ raw: String) -> String {
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed == "/voice" || trimmed.hasPrefix("/voice ") {
            let sec = String(trimmed.dropFirst(6)).trimmingCharacters(in: .whitespaces)
            return "🎤 [语音消息 \(sec.isEmpty ? "3" : sec)\"]"
        }
        guard trimmed.hasPrefix("/reply ") else { return raw }
        let rest = String(trimmed.dropFirst(7)).trimmingCharacters(in: .whitespaces)
        let parts = rest.split(separator: " ", maxSplits: 1, omittingEmptySubsequences: true)
        guard parts.count == 2 else { return raw }
        let targetId = String(parts[0])
        let body = String(parts[1])
        for list in messagesByPeer.values {
            if let orig = list.first(where: { $0.id == targetId && !$0.revoked }) {
                let cleanOrig = QQTheme.splitReactions(orig.text).body
                return "「\(cleanOrig)」\n\(body)"
            }
        }
        return "[re:\(targetId)] \(body)"
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
