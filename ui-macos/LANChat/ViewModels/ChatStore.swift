import AppKit
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
    @Published var matrixSummary: [String: String] = [:]
    @Published var unreadCounts: [String: Int] = [:]
    @Published var activeThreadKey: String? = "*"
    @Published var e2eeEnabledThreads: Set<String> = []
    @Published var voiceCall: VoiceCallState? = nil
    @Published var latestScreenFrame: String? = nil
    @Published var daemonConnected = false
    @Published var updateStatus: String = "内置国内加速节点：ghfast.top · gh-proxy.com · ghproxy.net · mirror.ghproxy.com · ghps.cc"
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
    private struct MsgUpdatedParams: Decodable { let message: HistoryMessage }
    private struct ProgressParams: Decodable { let transfer: Transfer }
    private struct TransferIdParams: Decodable { let transferId: String; let reason: String? }
    private struct GroupMatrixParams: Decodable {
        let groupId: String; let transferId: String; let haveBitmap: Data
    }
    private struct ChannelsParams: Decodable { let channels: [Channel] }
    private struct CallStateParams: Decodable { let call: VoiceCallState? }
    private struct ScreenFrameParams: Decodable {
        let from: String?; let group: String?; let frameData: String?; let frame: String?; let fps: Int?
    }

    private func handle(method: String, params: Data) {
        let decoder = JSONDecoder()
        switch method {
        case "conn.changed":
            if let p = try? decoder.decode(ConnParams.self, from: params) {
                conn = p.state
                if p.state == "connected" {
                    Task { await refreshState() }
                }
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
        case "msg.updated":
            if let p = try? decoder.decode(MsgUpdatedParams.self, from: params) {
                applyHistoryMessage(p.message)
            }
        case "msg.received":
            if let p = try? decoder.decode(MsgParams.self, from: params) {
                if p.type == "SCREEN_FRAME" {
                    latestScreenFrame = p.text.isEmpty ? nil : p.text
                    return
                }
                if p.type == "MSG_REVOKE" || p.type == "MSG_BURN" {
                    revokeMessage(id: p.msgId)
                } else if p.type == "CLIP_SYNC" {
                    let prefix = "📋 [剪贴板已同步] "
                    let rawClip = p.text.hasPrefix(prefix) ? String(p.text.dropFirst(prefix.count)) : p.text
                    if !rawClip.isEmpty {
                        NSPasteboard.general.clearContents()
                        NSPasteboard.general.setString(rawClip, forType: .string)
                    }
                    return
                } else if p.type == "MSG_READ" {
                    markMessageRead(id: p.msgId, readerId: p.from)
                } else if p.type == "MSG_REACT" || p.type == "MSG_EDIT" || p.type == "MSG_PIN" {
                    updateMessageText(id: p.msgId, newText: p.text)
                } else {
                    let display = p.text
                    let key = (p.group?.isEmpty == false) ? p.group! : p.from
                    append(ChatMessage(
                        id: p.msgId, peerId: p.from, text: display,
                        inbound: true, timestamp: Date()),
                           threadKey: p.group)
                    if activeThreadKey != key {
                        unreadCounts[key, default: 0] += 1
                    }
                }
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
        case "group.matrix":
            if let p = try? decoder.decode(GroupMatrixParams.self, from: params) {
                let size = transfers.first(where: { $0.id == p.transferId })?.size ?? 0
                matrixSummary[p.transferId] = formatMatrixSummary(haveBitmap: p.haveBitmap, size: size)
            }
        case "channel.updated":
            if let p = try? decoder.decode(ChannelsParams.self, from: params) {
                channels = p.channels
            }
        case "call.state":
            if let wrapped = try? decoder.decode(CallStateParams.self, from: params), let call = wrapped.call {
                voiceCall = call
            } else if let p = try? decoder.decode(VoiceCallState.self, from: params) {
                voiceCall = p
            }
        case "screen.frame":
            if let p = try? decoder.decode(ScreenFrameParams.self, from: params) {
                let f = (p.frameData ?? p.frame ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
                latestScreenFrame = f.isEmpty ? nil : f
            }
        default:
            break
        }
    }

    private func append(_ msg: ChatMessage, threadKey: String? = nil) {
        if Formatters.isSystemDiagnosticMessage(id: msg.id, text: msg.text) {
            return
        }
        let key = (threadKey?.isEmpty == false) ? threadKey! : msg.peerId
        var list = messages[key] ?? []
        var incoming = msg
        if incoming.originalText.isEmpty && !incoming.revoked && incoming.text != "[消息已撤回]" {
            incoming.originalText = Formatters.extractEditableOriginal(incoming.text)
        }
        if !incoming.id.isEmpty, let idx = list.firstIndex(where: { $0.id == incoming.id }) {
            let preserved = list[idx].originalText.isEmpty ? incoming.originalText : list[idx].originalText
            list[idx].text = incoming.text
            list[idx].inbound = incoming.inbound
            list[idx].revoked = incoming.revoked
            list[idx].readBy = incoming.readBy
            list[idx].originalText = preserved
            list[idx].replyQuote = incoming.replyQuote
            list[idx].reactions = incoming.reactions
            list[idx].editedAt = incoming.editedAt
            list[idx].pinned = incoming.pinned
            list[idx].mediaMime = incoming.mediaMime
            list[idx].mediaPath = incoming.mediaPath
            messages[key] = list
            return
        }
        list.append(incoming)
        messages[key] = list
    }

    private func revokeMessage(id: String) {
        guard !id.isEmpty else { return }
        for (key, var list) in messages {
            var changed = false
            for idx in list.indices where list[idx].id == id {
                if list[idx].originalText.isEmpty && list[idx].text != "[消息已撤回]" {
                    list[idx].originalText = Formatters.extractEditableOriginal(list[idx].text)
                }
                list[idx].revoked = true
                list[idx].text = "[消息已撤回]"
                changed = true
            }
            if changed {
                messages[key] = list
            }
        }
    }

    private func updateMessageText(id: String, newText: String) {
        guard !id.isEmpty else { return }
        for (key, var list) in messages {
            var changed = false
            for idx in list.indices where list[idx].id == id && !list[idx].revoked {
                list[idx].text = newText
                changed = true
            }
            if changed {
                messages[key] = list
            }
        }
    }

    private func markMessageRead(id: String, readerId: String) {
        guard !id.isEmpty, !readerId.isEmpty else { return }
        for (key, var list) in messages {
            var changed = false
            for idx in list.indices where list[idx].id == id {
                if !list[idx].readBy.contains(readerId) {
                    list[idx].readBy.append(readerId)
                    changed = true
                }
            }
            if changed {
                messages[key] = list
            }
        }
    }

    func searchMessages(in key: String, query: String) -> [ChatMessage] {
        let q = query.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !q.isEmpty else { return [] }
        return (messages[key] ?? []).filter { !$0.revoked && $0.text.lowercased().contains(q) }
    }

    func markThreadActive(_ key: String) {
        activeThreadKey = key
        unreadCounts[key] = 0
    }

    var totalUnreadCount: Int {
        unreadCounts.values.reduce(0, +)
    }

    func peerNickname(for peerId: String) -> String {
        if peerId == selfId {
            return settings.nickname
        }
        return peers.first(where: { $0.id == peerId })?.nickname
            ?? String(peerId.prefix(8))
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
        for list in messages.values {
            if let orig = list.first(where: { $0.id == targetId && !$0.revoked }) {
                let cleanOrig = Formatters.splitReactions(orig.text).body
                return "「\(cleanOrig)」\n\(body)"
            }
        }
        return "[re:\(targetId)] \(body)"
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
        voiceCall = state.voiceCall
        if let frame = state.screenShare?.latestFrame, !frame.isEmpty {
            latestScreenFrame = frame
        }
        for m in state.messages ?? [] {
            applyHistoryMessage(m)
        }
    }

    private func applyHistoryMessage(_ m: HistoryMessage) {
        let inbound = !selfId.isEmpty && m.from != selfId
        let threadKey: String? = (m.group?.isEmpty == false) ? m.group : nil
        let peerKey = (threadKey != nil) ? m.from : (inbound ? m.from : (m.to ?? ""))
        guard !peerKey.isEmpty || threadKey != nil else { return }
        let isRevoked = m.revoked ?? false
        let display = isRevoked ? "[消息已撤回]" : m.text
        append(ChatMessage(
            id: m.id,
            peerId: peerKey,
            text: display,
            inbound: inbound,
            timestamp: Date(timeIntervalSince1970: TimeInterval(m.ts) / 1000.0),
            revoked: isRevoked,
            readBy: m.readBy ?? [],
            replyQuote: m.replyQuote,
            reactions: m.reactions ?? [:],
            editedAt: m.editedAt ?? 0,
            pinned: m.pinned ?? false,
            mediaMime: m.mediaMime,
            mediaPath: m.mediaPath
        ), threadKey: threadKey)
    }

    func browseServers() async {
        if let list = try? await ipc.call("BrowseServers", params: nil, as: [ServerInfo].self) {
            servers = list
        }
    }

    func connect(addr: String, psk: String) async {
        let trimmed = addr.trimmingCharacters(in: .whitespacesAndNewlines)
        let targetAddr = trimmed.isEmpty ? "auto" : trimmed
        do {
            _ = try await ipc.call("Connect", params: ["addr": targetAddr, "psk": psk])
        } catch {
            self.error = error.localizedDescription
        }
    }

    func sendText(to peerId: String, text: String) async {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }
        if trimmed.hasPrefix("/search ") {
            let q = String(trimmed.dropFirst(8)).trimmingCharacters(in: .whitespaces)
            let hits = searchMessages(in: peerId, query: q)
            updateStatus = "搜索“\(q)”：共 \(hits.count) 条匹配"
            return
        }
        do {
            let r = try await ipc.call("SendText", params: ["to": peerId, "text": text, "group": ""], as: MsgIDResult.self)
            if r.msgID == "e2ee:on" || r.msgID == "e2ee:off" {
                if r.msgID == "e2ee:on" {
                    e2eeEnabledThreads.insert(peerId)
                } else {
                    e2eeEnabledThreads.remove(peerId)
                }
            } else if r.msgID.hasPrefix("fp:") {
                let fp = String(r.msgID.dropFirst(3))
                updateStatus = "🛡️ 安全码指纹核验：\(fp)"
            } else if r.msgID.hasPrefix("bwlimit:") {
                let lim = String(r.msgID.dropFirst(8))
                updateStatus = "⚡ 带宽限速已设置为 \(lim) MB/s"
            } else if r.msgID.hasPrefix("status:") {
                let st = String(r.msgID.dropFirst(7))
                updateStatus = "📊 诊断状态：\(st)"
            } else if r.msgID.hasPrefix("update:") {
                let up = String(r.msgID.dropFirst(7))
                updateStatus = up
            } else if trimmed.hasPrefix("/revoke ") {
                revokeMessage(id: r.msgID)
            } else if trimmed.hasPrefix("/read ") {
                markMessageRead(id: r.msgID, readerId: selfId)
            } else if trimmed.hasPrefix("/react ") || trimmed.hasPrefix("/edit ") || trimmed.hasPrefix("/pin ") {
                // Updated locally in core history; refresh state to sync updated bubble text
                await refreshState()
            } else {
                append(ChatMessage(id: r.msgID, peerId: peerId, text: formatReplyText(text), inbound: false, timestamp: Date()))
            }
        } catch {
            self.error = error.localizedDescription
        }
    }

    func sendSticker(to targetId: String, path: String, threadKey: String? = nil) async {
        guard !path.isEmpty else { return }
        do {
            let r = try await ipc.call("SendSticker", params: ["to": targetId, "path": path], as: MsgIDResult.self)
            append(ChatMessage(id: r.msgID, peerId: targetId, text: path, inbound: false, timestamp: Date()),
                   threadKey: threadKey)
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

    func listChannels() async {
        if let list = try? await ipc.call("ChannelList", params: nil, as: [Channel].self) {
            channels = list
        }
    }

    /// 频道群文本：group 为频道 ID；to 留空（group 优先）。
    func sendChannelText(channelID: String, text: String) async {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }
        if trimmed.hasPrefix("/search ") {
            let q = String(trimmed.dropFirst(8)).trimmingCharacters(in: .whitespaces)
            let hits = searchMessages(in: channelID, query: q)
            updateStatus = "搜索“\(q)”：共 \(hits.count) 条匹配"
            return
        }
        do {
            let r = try await ipc.call(
                "SendText",
                params: ["to": "", "text": text, "group": channelID],
                as: MsgIDResult.self)
            if r.msgID == "e2ee:on" || r.msgID == "e2ee:off" {
                if r.msgID == "e2ee:on" {
                    e2eeEnabledThreads.insert(channelID)
                } else {
                    e2eeEnabledThreads.remove(channelID)
                }
            } else if r.msgID.hasPrefix("bwlimit:") {
                let lim = String(r.msgID.dropFirst(8))
                updateStatus = "⚡ 带宽限速已设置为 \(lim) MB/s"
            } else if r.msgID.hasPrefix("status:") {
                let st = String(r.msgID.dropFirst(7))
                updateStatus = "📊 诊断状态：\(st)"
            } else if r.msgID.hasPrefix("update:") {
                let up = String(r.msgID.dropFirst(7))
                updateStatus = up
            } else if trimmed.hasPrefix("/revoke ") {
                revokeMessage(id: r.msgID)
            } else if trimmed.hasPrefix("/read ") {
                markMessageRead(id: r.msgID, readerId: selfId)
            } else if trimmed.hasPrefix("/react ") || trimmed.hasPrefix("/edit ") || trimmed.hasPrefix("/pin ") {
                await refreshState()
            } else {
                append(ChatMessage(id: r.msgID, peerId: selfId, text: formatReplyText(text),
                                   inbound: false, timestamp: Date()),
                       threadKey: channelID)
            }
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

    func pauseFile(_ transferId: String) async {
        do {
            _ = try await ipc.call("PauseFile", params: ["transferID": transferId])
            await refreshState()
        } catch {
            self.error = error.localizedDescription
        }
    }

    func resumeFile(_ transferId: String) async {
        do {
            _ = try await ipc.call("ResumeFile", params: ["transferID": transferId])
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

    func pickDownloadDir(_ path: String) async {
        do {
            _ = try await ipc.call("PickDownloadDir", params: ["path": path])
            settings.downloadDir = path
        } catch {
            self.error = error.localizedDescription
        }
    }

    // MARK: - 契约 v1.3 显式协作 RPC 方法

    func startVoiceCall(to: String, group: String = "") async {
        do {
            _ = try await ipc.call("StartVoiceCall", params: ["to": to, "group": group])
            await refreshState()
        } catch {
            self.error = error.localizedDescription
        }
    }

    func acceptVoiceCall(to: String, group: String = "", remoteUDP: String = "") async {
        do {
            _ = try await ipc.call("AcceptVoiceCall", params: ["to": to, "group": group, "remoteUDP": remoteUDP])
            await refreshState()
        } catch {
            self.error = error.localizedDescription
        }
    }

    func stopVoiceCall(to: String, group: String = "") async {
        do {
            _ = try await ipc.call("StopVoiceCall", params: ["to": to, "group": group])
            await refreshState()
        } catch {
            self.error = error.localizedDescription
        }
    }

    func startScreenShare(to: String, group: String = "", fps: Int = 15) async {
        do {
            _ = try await ipc.call("StartScreenShare", params: ["to": to, "group": group, "fps": fps])
            await refreshState()
        } catch {
            self.error = error.localizedDescription
        }
    }

    func stopScreenShare(to: String, group: String = "") async {
        do {
            _ = try await ipc.call("StopScreenShare", params: ["to": to, "group": group])
            await refreshState()
        } catch {
            self.error = error.localizedDescription
        }
    }

    func sendScreenAnnotation(to: String, group: String = "", normX: Double, normY: Double, note: String) async {
        do {
            _ = try await ipc.call("SendScreenAnnotation", params: [
                "to": to,
                "group": group,
                "normX": normX,
                "normY": normY,
                "note": note
            ])
        } catch {
            self.error = error.localizedDescription
        }
    }

    func syncFolder(to: String, group: String = "", dirPath: String) async {
        do {
            _ = try await ipc.call("SyncFolder", params: ["to": to, "group": group, "dirPath": dirPath])
        } catch {
            self.error = error.localizedDescription
        }
    }

    func issueTrustCert(subject: String, org: String = "LANChat", ttlSec: Int64 = 86400) async {
        do {
            _ = try await ipc.call("IssueTrustCert", params: ["subject": subject, "org": org, "ttlSec": ttlSec])
        } catch {
            self.error = error.localizedDescription
        }
    }

    func verifyTrustCert(token: String, rootPub: String = "") async {
        do {
            _ = try await ipc.call("VerifyTrustCert", params: ["token": token, "rootPub": rootPub])
        } catch {
            self.error = error.localizedDescription
        }
    }

    func runNetworkDiagnostics() async {
        do {
            _ = try await ipc.call("RunNetworkDiagnostics", params: nil)
        } catch {
            self.error = error.localizedDescription
        }
    }

    // MARK: - 契约 v1.4 消息富交互与安全控制显式 RPC 方法

    func revokeMessageRpc(to: String = "", group: String = "", msgID: String) async {
        do {
            _ = try await ipc.call("RevokeMessage", params: ["to": to, "group": group, "msgID": msgID])
            revokeMessage(id: msgID)
        } catch {
            self.error = error.localizedDescription
        }
    }

    func reactMessageRpc(to: String = "", group: String = "", msgID: String, emoji: String = "👍") async {
        do {
            _ = try await ipc.call("ReactMessage", params: ["to": to, "group": group, "msgID": msgID, "emoji": emoji])
            await refreshState()
        } catch {
            self.error = error.localizedDescription
        }
    }

    func editMessageRpc(to: String = "", group: String = "", msgID: String, newText: String) async {
        do {
            _ = try await ipc.call("EditMessage", params: ["to": to, "group": group, "msgID": msgID, "newText": newText])
            await refreshState()
        } catch {
            self.error = error.localizedDescription
        }
    }

    func pinMessageRpc(to: String = "", group: String = "", msgID: String) async {
        do {
            _ = try await ipc.call("PinMessage", params: ["to": to, "group": group, "msgID": msgID])
            await refreshState()
        } catch {
            self.error = error.localizedDescription
        }
    }

    func markMessageReadRpc(to: String = "", group: String = "", msgID: String) async {
        do {
            _ = try await ipc.call("MarkMessageRead", params: ["to": to, "group": group, "msgID": msgID])
            markMessageRead(id: msgID, readerId: selfId)
        } catch {
            self.error = error.localizedDescription
        }
    }

    func toggleSessionE2EE(threadKey: String) async {
        let enabling = !e2eeEnabledThreads.contains(threadKey)
        do {
            _ = try await ipc.call("SetSessionE2EE", params: ["secret": enabling ? "lanchat-qq-key" : ""])
            if enabling {
                e2eeEnabledThreads.insert(threadKey)
            } else {
                e2eeEnabledThreads.remove(threadKey)
            }
        } catch {
            self.error = error.localizedDescription
        }
    }

    func setAutoClipboard(_ enabled: Bool) async {
        do {
            _ = try await ipc.call("SetAutoClipboard", params: ["enabled": enabled])
        } catch {
            self.error = error.localizedDescription
        }
    }

    func setDND(_ enabled: Bool) async {
        do {
            _ = try await ipc.call("SetDND", params: ["enabled": enabled])
        } catch {
            self.error = error.localizedDescription
        }
    }

    func getHistoryPage(convID: String, beforeTS: Int64 = 0, limit: Int = 50) async -> [HistoryMessage] {
        if let list = try? await ipc.call(
            "GetHistoryPage",
            params: ["convID": convID, "beforeTS": beforeTS, "limit": limit],
            as: [HistoryMessage].self
        ) {
            for m in list {
                applyHistoryMessage(m)
            }
            return list
        }
        return []
    }

    private struct FingerprintResult: Decodable { let fingerprint: String }
    private struct SyncedResult: Decodable { let synced: Int }
    private struct SummaryResult: Decodable { let summary: String }

    func verifyPeerFingerprintRpc(peerID: String) async {
        do {
            let r = try await ipc.call("VerifyPeerFingerprint", params: ["peerID": peerID], as: FingerprintResult.self)
            let fp = r.fingerprint.hasPrefix("fp:") ? String(r.fingerprint.dropFirst(3)) : r.fingerprint
            self.error = "🛡️ 安全码指纹核验：\(fp)"
        } catch {
            self.error = error.localizedDescription
        }
    }

    func sendBurnMessageRpc(to: String = "", group: String = "", text: String, ttlSec: Int = 10) async {
        do {
            _ = try await ipc.call(
                "SendBurnMessage",
                params: ["to": to, "group": group, "text": text, "ttlSec": ttlSec],
                as: MsgIDResult.self
            )
            await refreshState()
        } catch {
            self.error = error.localizedDescription
        }
    }

    func syncHistoryWithPeerRpc(to: String = "", group: String = "", sinceTS: Int64 = 0) async {
        do {
            _ = try await ipc.call(
                "SyncHistoryWithPeer",
                params: ["to": to, "group": group, "sinceTS": sinceTS],
                as: SyncedResult.self
            )
            await refreshState()
        } catch {
            self.error = error.localizedDescription
        }
    }

    func setBandwidthLimitRpc(limitMBps: Double) async {
        do {
            _ = try await ipc.call("SetBandwidthLimit", params: ["limitMBps": limitMBps])
        } catch {
            self.error = error.localizedDescription
        }
    }

    func checkAndSilentUpdate(customProxy: String = "", checkOnly: Bool = false) async {
        let proxyArg = customProxy.trimmingCharacters(in: .whitespacesAndNewlines)
        if checkOnly {
            updateStatus = "正在通过国内加速节点检查最新版本…"
        } else if proxyArg.isEmpty {
            updateStatus = "正在通过国内加速代理静默下载最新安装包（免浏览器）…"
        } else {
            updateStatus = "正在通过 \(proxyArg) 静默下载最新安装包…"
        }
        do {
            let r = try await ipc.call(
                "CheckSilentUpdate",
                params: ["proxy": proxyArg, "download": !checkOnly],
                as: SummaryResult.self
            )
            if r.summary.hasPrefix("update:") {
                updateStatus = String(r.summary.dropFirst(7))
            } else {
                updateStatus = r.summary
            }
            await refreshState()
        } catch {
            updateStatus = "更新操作失败：\(error.localizedDescription)"
            self.error = error.localizedDescription
        }
    }

    // MARK: - 契约 v1.6 频道权限治理与加密离线投递箱显式 RPC 方法

    func channelManageMemberRpc(channelID: String, memberID: String = "", action: String) async {
        do {
            _ = try await ipc.call(
                "ChannelManageMember",
                params: ["channelID": channelID, "memberID": memberID, "action": action]
            )
            await refreshState()
        } catch {
            self.error = error.localizedDescription
        }
    }

    func enqueueOfflineMailboxRpc(to: String, text: String, recipientPub: String = "") async {
        do {
            _ = try await ipc.call(
                "EnqueueOfflineMailbox",
                params: ["to": to, "text": text, "recipientPub": recipientPub],
                as: MsgIDResult.self
            )
            await refreshState()
        } catch {
            self.error = error.localizedDescription
        }
    }

    func drainOfflineMailboxRpc(fromPeer: String = "") async {
        do {
            _ = try await ipc.call(
                "DrainOfflineMailbox",
                params: ["fromPeer": fromPeer]
            )
            await refreshState()
        } catch {
            self.error = error.localizedDescription
        }
    }

    // MARK: - 阶段七 v8.3.0：MenuBarExtra 未读角标常驻与系统通知快捷操作

    var totalUnreadCount: Int {
        unreadCounts.values.reduce(0, +)
    }

    var hasIncomingCall: Bool {
        voiceCall?.state == "ringing"
    }

    var pendingInboundTransfer: Transfer? {
        transfers.first(where: { $0.direction == "inbound" && $0.state == "pending" })
    }

    func syncDockUnreadBadge() {
        let count = totalUnreadCount
        NSApplication.shared.dockTile.badgeLabel = count > 0 ? "\(count)" : nil
    }

    func quickReplyLatestUnread(text: String = "👍 收到，稍后处理") async {
        let targetKey = unreadCounts.first(where: { $0.value > 0 })?.key ?? activeThreadKey ?? "*"
        if targetKey == "*" || channels.contains(where: { $0.id == targetKey }) {
            await sendChannelText(channelID: targetKey, text: text)
        } else {
            await sendText(to: targetKey, text: text)
        }
        unreadCounts[targetKey] = 0
        syncDockUnreadBadge()
    }

    func quickAcceptIncomingCall() async {
        let to = voiceCall?.peerId ?? ""
        let group = voiceCall?.groupId ?? ""
        await acceptVoiceCall(to: to, group: group)
    }

    func quickAcceptPendingFile() async {
        guard let pending = pendingInboundTransfer else { return }
        await respondFile(transferId: pending.id, accept: true)
    }

    func shutdown() {
        ipc.stop()
        if settings.stopDaemonOnExit {
            launcher.stopDaemonIfOwned()
        }
    }
}
