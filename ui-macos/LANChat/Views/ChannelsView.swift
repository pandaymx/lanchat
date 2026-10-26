import SwiftUI

/// 新建频道弹窗。契约 ChannelCreate：name 必填，topic/private 可缺省。
struct CreateChannelSheet: View {
    @ObservedObject var store: ChatStore
    @Environment(\.dismiss) private var dismiss

    @State private var name = ""
    @State private var topic = ""
    @State private var isPrivate = false

    private var trimmed: String {
        name.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack(spacing: 10) {
                QQAvatar(
                    key: trimmed.isEmpty ? "new-channel" : trimmed,
                    title: trimmed.isEmpty ? "#" : trimmed,
                    size: 40,
                    systemIcon: isPrivate ? "lock.fill" : "number"
                )
                VStack(alignment: .leading, spacing: 2) {
                    Text("新建局域网频道")
                        .font(.system(size: 16, weight: .bold))
                    Text("支持多成员实时群聊与 G2 P2P 频道文件分发")
                        .font(.system(size: 11.5))
                        .foregroundColor(.secondary)
                }
            }

            Divider()

            VStack(alignment: .leading, spacing: 6) {
                Text("频道名称")
                    .font(.system(size: 12, weight: .medium))
                    .foregroundColor(.secondary)
                TextField("例如：研发协同群 / 设计评审", text: $name)
                    .textFieldStyle(.roundedBorder)
                    .onSubmit { create() }
            }

            VStack(alignment: .leading, spacing: 6) {
                Text("频道主题 / 公告（可选）")
                    .font(.system(size: 12, weight: .medium))
                    .foregroundColor(.secondary)
                TextField("简要描述频道用途或群公告", text: $topic)
                    .textFieldStyle(.roundedBorder)
            }

            Toggle(isOn: $isPrivate) {
                VStack(alignment: .leading, spacing: 1) {
                    Text("私有频道（仅可通过邀请加入）")
                        .font(.system(size: 13, weight: .medium))
                    Text("开启后非受邀成员无法主动加入此频道")
                        .font(.system(size: 11))
                        .foregroundColor(.secondary)
                }
            }

            HStack(spacing: 10) {
                Spacer()
                Button("取消", role: .cancel) { dismiss() }
                Button {
                    create()
                } label: {
                    Text("立即创建")
                        .font(.system(size: 13, weight: .semibold))
                        .foregroundColor(.white)
                        .padding(.horizontal, 16)
                        .padding(.vertical, 6)
                        .background(
                            RoundedRectangle(cornerRadius: 8, style: .continuous)
                                .fill(trimmed.isEmpty ? Color.gray.opacity(0.45) : QQPalette.qqBlue)
                        )
                }
                .buttonStyle(.plain)
                .keyboardShortcut(.defaultAction)
                .disabled(trimmed.isEmpty)
            }
            .padding(.top, 4)
        }
        .padding(22)
        .frame(width: 380)
    }

    private func create() {
        guard !trimmed.isEmpty else { return }
        let value = trimmed
        let channelTopic = topic
        let privateValue = isPrivate
        dismiss()
        Task {
            await store.createChannel(name: value,
                                      topic: channelTopic,
                                      isPrivate: privateValue)
        }
    }
}

// MARK: - 局域网全员广播群会话视图 (group = "*")

struct BroadcastChatView: View {
    @ObservedObject var store: ChatStore
    @ObservedObject var settings: SettingsStore
    var showInspector: Binding<Bool>? = nil

    @State private var draft = ""
    @State private var showStickerPopover = false
    @State private var showSearchBar = false
    @State private var searchQuery = ""
    @State private var showOnlinePeers = false
    @State private var replyingTo: ChatMessage? = nil

    private let broadcastKey = "*"

    private var thread: [ChatMessage] {
        let raw = (store.messages[broadcastKey] ?? []).map { msg -> ChatMessage in
            var m = msg
            if m.inbound {
                m.senderName = store.peerNickname(for: msg.peerId)
            } else {
                m.senderName = settings.nickname
            }
            return m
        }
        guard showSearchBar else { return raw }
        let q = searchQuery.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !q.isEmpty else { return raw }
        return raw.filter { !$0.revoked && $0.text.lowercased().contains(q) }
    }

    private var isE2EE: Bool {
        store.e2eeEnabledThreads.contains(broadcastKey)
    }

    private var activeTransfersCount: Int {
        store.transfers.filter { $0.state == "active" || $0.state == "pending" }.count
    }

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()

            if showSearchBar {
                searchHeaderBar
                Divider()
            }

            messagesStream

            Divider()
            composer
        }
        .background(Color(nsColor: .windowBackgroundColor))
        .frame(minWidth: 460, minHeight: 420)
        .onAppear {
            store.markThreadActive(broadcastKey)
        }
        .popover(isPresented: $showOnlinePeers) {
            BroadcastOnlinePeersPopover(store: store, settings: settings)
        }
    }

    private var header: some View {
        HStack(spacing: 12) {
            QQAvatar(
                key: "*",
                title: "广",
                size: 38,
                status: "online",
                emojiBadge: "📢"
            )

            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    Text("📢 局域网全员广播群")
                        .font(.system(size: 15, weight: .bold))
                        .foregroundColor(.primary)

                    Text("置顶全员")
                        .font(.system(size: 10, weight: .semibold))
                        .foregroundColor(QQPalette.qqBlue)
                        .padding(.horizontal, 6)
                        .padding(.vertical, 1.5)
                        .background(Capsule().fill(QQPalette.qqBlue.opacity(0.14)))

                    Text("G1 Swarm")
                        .font(.system(size: 10, weight: .semibold))
                        .foregroundColor(.orange)
                        .padding(.horizontal, 6)
                        .padding(.vertical, 1.5)
                        .background(Capsule().fill(Color.orange.opacity(0.15)))
                }

                Text("在线成员 \(store.peers.count + 1) 人 · 消息与群文件自动向全体在线节点广播分发")
                    .font(.system(size: 11.5))
                    .foregroundColor(.secondary)
                    .lineLimit(1)
            }

            Spacer()

            HStack(spacing: 8) {
                Button {
                    pickAndOfferBroadcast()
                } label: {
                    Text("📁 发送文件")
                        .font(.system(size: 12, weight: .medium))
                        .padding(.horizontal, 10)
                        .padding(.vertical, 5)
                        .background(
                            RoundedRectangle(cornerRadius: 7, style: .continuous)
                                .fill(Color.primary.opacity(0.05))
                        )
                }
                .buttonStyle(.plain)
                .help("通过 G1 Swarm P2P 向全员广播发送文件")

                Button {
                    showOnlinePeers = true
                } label: {
                    Text("👥 成员 (\(store.peers.count + 1))")
                        .font(.system(size: 12, weight: .medium))
                        .padding(.horizontal, 10)
                        .padding(.vertical, 5)
                        .background(
                            RoundedRectangle(cornerRadius: 7, style: .continuous)
                                .fill(Color.primary.opacity(0.05))
                        )
                }
                .buttonStyle(.plain)

                if let showInspector {
                    Button {
                        showInspector.wrappedValue.toggle()
                    } label: {
                        HStack(spacing: 4) {
                            Text("📊 传输面板")
                                .font(.system(size: 12, weight: .medium))
                            if activeTransfersCount > 0 {
                                Text("\(activeTransfersCount)")
                                    .font(.system(size: 10, weight: .bold))
                                    .foregroundColor(.white)
                                    .padding(.horizontal, 5)
                                    .padding(.vertical, 1)
                                    .background(Capsule().fill(QQPalette.qqBlue))
                            }
                        }
                        .foregroundColor(showInspector.wrappedValue ? QQPalette.qqBlue : .primary)
                        .padding(.horizontal, 10)
                        .padding(.vertical, 5)
                        .background(
                            RoundedRectangle(cornerRadius: 7, style: .continuous)
                                .fill(showInspector.wrappedValue ? QQPalette.qqBlue.opacity(0.14) : Color.primary.opacity(0.05))
                        )
                    }
                    .buttonStyle(.plain)
                }
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 11)
        .background(Color(nsColor: .controlBackgroundColor).opacity(0.6))
    }

    private var searchHeaderBar: some View {
        HStack(spacing: 8) {
            Image(systemName: "magnifyingglass")
                .foregroundColor(.secondary)
            TextField("搜索全员广播群聊天记录…", text: $searchQuery)
                .textFieldStyle(.plain)
                .font(.system(size: 12.5))
                .onSubmit {
                    let q = searchQuery.trimmingCharacters(in: .whitespacesAndNewlines)
                    guard !q.isEmpty else { return }
                    Task { await store.sendChannelText(channelID: broadcastKey, text: "/search \(q)") }
                }

            if !searchQuery.isEmpty {
                Text("匹配 \(thread.count) 条")
                    .font(.system(size: 11))
                    .foregroundColor(.secondary)
                Button {
                    searchQuery = ""
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .foregroundColor(.secondary)
                }
                .buttonStyle(.plain)
            }

            Button("关闭") {
                showSearchBar = false
                searchQuery = ""
            }
            .buttonStyle(.plain)
            .font(.system(size: 11.5, weight: .medium))
            .foregroundColor(QQPalette.qqBlue)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
        .background(Color.primary.opacity(0.03))
    }

    private var messagesStream: some View {
        ScrollViewReader { proxy in
            ScrollView {
                if thread.isEmpty {
                    VStack(spacing: 10) {
                        Spacer(minLength: 80)
                        QQAvatar(key: "*", title: "广", size: 54, emojiBadge: "📢")
                            .opacity(0.9)
                        Text("局域网全员广播群 (group = \"*\")")
                            .font(.system(size: 14, weight: .semibold))
                            .foregroundColor(.primary.opacity(0.8))
                        Text("在这里发送的消息与文件将实时同步给局域网内所有在线成员")
                            .font(.system(size: 12))
                            .foregroundColor(.secondary)
                        Spacer(minLength: 80)
                    }
                    .frame(maxWidth: .infinity)
                    .padding()
                } else {
                    LazyVStack(alignment: .leading, spacing: 14) {
                        ForEach(thread) { msg in
                            BubbleRow(
                                message: msg,
                                isSelf: !msg.inbound,
                                senderName: msg.senderName ?? store.peerNickname(for: msg.peerId),
                                senderKey: msg.inbound ? msg.peerId : (store.selfId.isEmpty ? "self" : store.selfId),
                                onReply: { target in
                                    replyingTo = target
                                },
                                onRevoke: { target in
                                    Task { await store.sendChannelText(channelID: broadcastKey, text: "/revoke \(target.id)") }
                                },
                                onMarkRead: { target in
                                    Task { await store.sendChannelText(channelID: broadcastKey, text: "/read \(target.id)") }
                                },
                                onReact: { target in
                                    Task { await store.sendChannelText(channelID: broadcastKey, text: "/react \(target.id) 👍") }
                                },
                                onPin: { target in
                                    Task { await store.sendChannelText(channelID: broadcastKey, text: "/pin \(target.id)") }
                                }
                            )
                            .id(msg.id)
                        }
                    }
                    .padding(.horizontal, 16)
                    .padding(.vertical, 14)
                }
            }
            .onChange(of: thread.count) { _ in
                if let last = thread.last {
                    proxy.scrollTo(last.id, anchor: .bottom)
                }
            }
        }
    }

    private var composer: some View {
        VStack(spacing: 0) {
            if let replyingTo {
                HStack(spacing: 8) {
                    RoundedRectangle(cornerRadius: 2)
                        .fill(QQPalette.qqBlue)
                        .frame(width: 3, height: 22)
                    Text("回复：\(replyingTo.text)")
                        .font(.system(size: 12))
                        .foregroundColor(.secondary)
                        .lineLimit(1)
                    Spacer()
                    Button {
                        self.replyingTo = nil
                    } label: {
                        Image(systemName: "xmark.circle.fill")
                            .foregroundColor(.secondary)
                    }
                    .buttonStyle(.plain)
                }
                .padding(.horizontal, 14)
                .padding(.vertical, 6)
                .background(QQPalette.qqBlue.opacity(0.06))
                Divider()
            }

            HStack(spacing: 6) {
                Button {
                    showStickerPopover.toggle()
                } label: {
                    Text("😊 表情贴纸")
                        .font(.system(size: 12, weight: .medium))
                        .foregroundColor(.primary.opacity(0.85))
                        .padding(.horizontal, 8)
                        .padding(.vertical, 4)
                        .background(
                            RoundedRectangle(cornerRadius: 6, style: .continuous)
                                .fill(showStickerPopover ? QQPalette.qqBlue.opacity(0.14) : Color.clear)
                        )
                }
                .buttonStyle(.plain)
                .popover(isPresented: $showStickerPopover, arrowEdge: .top) {
                    StickerPickerPopover(
                        onSendSticker: { stickerPath in
                            showStickerPopover = false
                            Task { await store.sendSticker(to: broadcastKey, path: stickerPath, threadKey: broadcastKey) }
                        },
                        onInsertEmoji: { emoji in
                            draft += emoji
                            showStickerPopover = false
                        }
                    )
                }

                Button {
                    pickAndOfferBroadcast()
                } label: {
                    Text("📁 发送文件")
                        .font(.system(size: 12, weight: .medium))
                        .foregroundColor(.primary.opacity(0.85))
                        .padding(.horizontal, 8)
                        .padding(.vertical, 4)
                }
                .buttonStyle(.plain)

                Button {
                    let cmd = isE2EE ? "/e2ee off" : "/e2ee on"
                    Task { await store.sendChannelText(channelID: broadcastKey, text: cmd) }
                } label: {
                    Text(isE2EE ? "🔐 已加密 (/e2ee)" : "🔐 端到端加密 (/e2ee)")
                        .font(.system(size: 12, weight: .medium))
                        .foregroundColor(isE2EE ? QQPalette.onlineGreen : .primary.opacity(0.85))
                        .padding(.horizontal, 8)
                        .padding(.vertical, 4)
                        .background(
                            RoundedRectangle(cornerRadius: 6, style: .continuous)
                                .fill(isE2EE ? QQPalette.onlineGreen.opacity(0.14) : Color.clear)
                        )
                }
                .buttonStyle(.plain)

                Button {
                    showSearchBar.toggle()
                } label: {
                    Text("🔍 搜索记录")
                        .font(.system(size: 12, weight: .medium))
                        .foregroundColor(showSearchBar ? QQPalette.qqBlue : .primary.opacity(0.85))
                        .padding(.horizontal, 8)
                        .padding(.vertical, 4)
                        .background(
                            RoundedRectangle(cornerRadius: 6, style: .continuous)
                                .fill(showSearchBar ? QQPalette.qqBlue.opacity(0.14) : Color.clear)
                        )
                }
                .buttonStyle(.plain)

                Spacer()
            }
            .padding(.horizontal, 10)
            .padding(.top, 8)
            .padding(.bottom, 4)

            ZStack(alignment: .topLeading) {
                TextEditor(text: $draft)
                    .font(.system(size: 13.5))
                    .frame(minHeight: 58, maxHeight: 110)
                    .scrollContentBackground(.hidden)
                    .padding(.horizontal, 10)
                    .padding(.vertical, 4)

                if draft.isEmpty {
                    Text(replyingTo != nil ? "输入回复内容…" : "向局域网全体在线成员发送广播消息…")
                        .font(.system(size: 13))
                        .foregroundColor(.secondary.opacity(0.6))
                        .padding(.horizontal, 15)
                        .padding(.vertical, 8)
                        .allowsHitTesting(false)
                }
            }

            HStack {
                Text("G1 Swarm 广播群 · 支持多节点分块并行下载")
                    .font(.system(size: 11))
                    .foregroundColor(.secondary.opacity(0.75))
                Spacer()
                Button {
                    sendBroadcast()
                } label: {
                    Text("发送")
                        .font(.system(size: 13, weight: .semibold))
                        .foregroundColor(.white)
                        .padding(.horizontal, 20)
                        .padding(.vertical, 6)
                        .background(
                            RoundedRectangle(cornerRadius: 8, style: .continuous)
                                .fill(
                                    draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                                        ? Color.gray.opacity(0.45)
                                        : QQPalette.qqBlue
                                )
                        )
                }
                .buttonStyle(.plain)
                .keyboardShortcut(.defaultAction)
                .disabled(draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
            .padding(.horizontal, 14)
            .padding(.bottom, 10)
            .padding(.top, 2)
        }
        .background(Color(nsColor: .controlBackgroundColor).opacity(0.45))
    }

    private func sendBroadcast() {
        let raw = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !raw.isEmpty else { return }
        draft = ""
        let payload: String
        if let target = replyingTo, !raw.hasPrefix("/") {
            payload = "/reply \(target.id) \(raw)"
        } else {
            payload = raw
        }
        replyingTo = nil
        Task { await store.sendChannelText(channelID: broadcastKey, text: payload) }
    }

    private func pickAndOfferBroadcast() {
        let panel = NSOpenPanel()
        panel.allowsMultipleSelection = false
        panel.canChooseDirectories = true
        panel.canChooseFiles = true
        guard panel.runModal() == .OK, let url = panel.url else { return }
        Task { await store.offerFileToChannel(channelID: broadcastKey, path: url.path) }
    }
}

private struct BroadcastOnlinePeersPopover: View {
    @ObservedObject var store: ChatStore
    @ObservedObject var settings: SettingsStore

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("全员群在线成员 (\(store.peers.count + 1))")
                .font(.system(size: 13, weight: .bold))

            Divider()

            HStack(spacing: 8) {
                Circle()
                    .fill(QQPalette.onlineGreen)
                    .frame(width: 8, height: 8)
                Text(settings.nickname)
                    .font(.system(size: 12.5, weight: .semibold))
                Text("本机")
                    .font(.system(size: 10))
                    .foregroundColor(QQPalette.qqBlue)
                    .padding(.horizontal, 5)
                    .padding(.vertical, 1)
                    .background(Capsule().fill(QQPalette.qqBlue.opacity(0.14)))
            }

            ForEach(store.peers) { peer in
                HStack(spacing: 8) {
                    Circle()
                        .fill(peer.status == "busy" ? QQPalette.busyOrange : QQPalette.onlineGreen)
                        .frame(width: 8, height: 8)
                    Text(peer.nickname)
                        .font(.system(size: 12.5))
                    Spacer()
                    Text(peer.os)
                        .font(.system(size: 10.5))
                        .foregroundColor(.secondary)
                }
            }
        }
        .padding(14)
        .frame(width: 230)
    }
}

// MARK: - 自定义频道会话视图 (G2 自定义频道)

/// 频道会话：频道消息气泡（按 group=channelID 归类）+ 群文本 + 群文件。
struct ChannelChatView: View {
    @ObservedObject var store: ChatStore
    let channelID: String
    var showInspector: Binding<Bool>? = nil

    @State private var draft = ""
    @State private var showMembers = false
    @State private var showInvite = false
    @State private var showStickerPopover = false
    @State private var showSearchBar = false
    @State private var searchQuery = ""
    @State private var replyingTo: ChatMessage? = nil

    init(store: ChatStore, channel: Channel, showInspector: Binding<Bool>? = nil) {
        self.store = store
        self.channelID = channel.id
        self.showInspector = showInspector
    }

    private var channel: Channel? {
        store.channels.first(where: { $0.id == channelID })
    }

    private var joined: Bool {
        channel.map { store.isMember($0) } ?? false
    }

    private var isE2EE: Bool {
        store.e2eeEnabledThreads.contains(channelID)
    }

    private var activeTransfersCount: Int {
        store.transfers.filter { $0.state == "active" || $0.state == "pending" }.count
    }

    private var thread: [ChatMessage] {
        guard joined else { return [] }
        let mapped = (store.messages[channelID] ?? []).map { msg -> ChatMessage in
            var m = msg
            if m.inbound {
                m.senderName = store.peers.first(where: { $0.id == msg.peerId })?.nickname
                    ?? String(msg.peerId.prefix(8))
            } else {
                m.senderName = store.peerNickname(for: store.selfId)
            }
            return m
        }
        guard showSearchBar else { return mapped }
        let q = searchQuery.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !q.isEmpty else { return mapped }
        return mapped.filter { !$0.revoked && $0.text.lowercased().contains(q) }
    }

    var body: some View {
        VStack(spacing: 0) {
            if let channel {
                header(channel)
                Divider()
                if showSearchBar && joined {
                    searchHeaderBar(channel)
                    Divider()
                }
                if joined {
                    messages
                    composer(channel)
                } else {
                    joinPrompt(channel)
                }
            } else {
                Spacer()
                Text("频道不存在或已被删除").foregroundColor(.secondary)
                Spacer()
            }
        }
        .background(Color(nsColor: .windowBackgroundColor))
        .frame(minWidth: 460, minHeight: 420)
        .task { await store.listChannels() }
        .onAppear {
            store.markThreadActive(channelID)
        }
        .onChange(of: channelID) { newID in
            store.markThreadActive(newID)
            replyingTo = nil
            searchQuery = ""
        }
        .popover(isPresented: $showMembers) {
            if let channel { MemberList(store: store, channel: channel) }
        }
        .popover(isPresented: $showInvite) {
            if let channel { InviteList(store: store, channel: channel) }
        }
    }

    private func header(_ channel: Channel) -> some View {
        HStack(spacing: 12) {
            QQAvatar(
                key: channel.id,
                title: channel.name,
                size: 38,
                systemIcon: channel.isPrivate == true ? "lock.fill" : "number"
            )

            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    Text(channel.name)
                        .font(.system(size: 15, weight: .bold))
                        .foregroundColor(.primary)

                    if channel.isPrivate == true {
                        Text("🔒 私有")
                            .font(.system(size: 10.5, weight: .semibold))
                            .foregroundColor(.orange)
                            .padding(.horizontal, 6)
                            .padding(.vertical, 1.5)
                            .background(Color.orange.opacity(0.14))
                            .clipShape(Capsule())
                    }

                    if isE2EE {
                        Text("🔐 E2EE 加密")
                            .font(.system(size: 10.5, weight: .semibold))
                            .foregroundColor(QQPalette.onlineGreen)
                            .padding(.horizontal, 6)
                            .padding(.vertical, 1.5)
                            .background(QQPalette.onlineGreen.opacity(0.14))
                            .clipShape(Capsule())
                    }
                }

                if let topic = channel.topic,
                   !topic.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                    Text("\(channel.members.count) 名成员 · \(topic)")
                        .font(.system(size: 11.5))
                        .foregroundColor(.secondary)
                        .lineLimit(1)
                } else {
                    Text("\(channel.members.count) 名成员 · 频道内实时消息与文件共享")
                        .font(.system(size: 11.5))
                        .foregroundColor(.secondary)
                }
            }

            Spacer()

            // 顶部快捷操作栏：📁 发送文件、👥 成员、➕ 邀请成员、🚪 退出频道、📊 传输面板
            HStack(spacing: 6) {
                if joined {
                    Button {
                        pickAndOffer(channel)
                    } label: {
                        Text("📁 发送文件")
                            .font(.system(size: 11.5, weight: .medium))
                            .padding(.horizontal, 9)
                            .padding(.vertical, 5)
                            .background(
                                RoundedRectangle(cornerRadius: 7, style: .continuous)
                                    .fill(Color.primary.opacity(0.05))
                            )
                    }
                    .buttonStyle(.plain)
                    .help("向频道全员发送文件")
                }

                Button {
                    showMembers = true
                } label: {
                    Text("👥 成员(\(channel.members.count))")
                        .font(.system(size: 11.5, weight: .medium))
                        .padding(.horizontal, 9)
                        .padding(.vertical, 5)
                        .background(
                            RoundedRectangle(cornerRadius: 7, style: .continuous)
                                .fill(Color.primary.opacity(0.05))
                        )
                }
                .buttonStyle(.plain)

                if channel.ownerId == store.selfId || joined {
                    Button {
                        showInvite = true
                    } label: {
                        Text("➕ 邀请成员")
                            .font(.system(size: 11.5, weight: .medium))
                            .padding(.horizontal, 9)
                            .padding(.vertical, 5)
                            .background(
                                RoundedRectangle(cornerRadius: 7, style: .continuous)
                                    .fill(Color.primary.opacity(0.05))
                            )
                    }
                    .buttonStyle(.plain)
                }

                if joined {
                    Button {
                        Task { await store.leaveChannel(channelID: channel.id) }
                    } label: {
                        Text("🚪 退出频道")
                            .font(.system(size: 11.5, weight: .medium))
                            .foregroundColor(.red.opacity(0.85))
                            .padding(.horizontal, 9)
                            .padding(.vertical, 5)
                            .background(
                                RoundedRectangle(cornerRadius: 7, style: .continuous)
                                    .fill(Color.red.opacity(0.08))
                            )
                    }
                    .buttonStyle(.plain)
                }

                if let showInspector {
                    Button {
                        showInspector.wrappedValue.toggle()
                    } label: {
                        HStack(spacing: 4) {
                            Text("📊 传输面板")
                                .font(.system(size: 11.5, weight: .medium))
                            if activeTransfersCount > 0 {
                                Text("\(activeTransfersCount)")
                                    .font(.system(size: 10, weight: .bold))
                                    .foregroundColor(.white)
                                    .padding(.horizontal, 5)
                                    .padding(.vertical, 1)
                                    .background(Capsule().fill(QQPalette.qqBlue))
                            }
                        }
                        .foregroundColor(showInspector.wrappedValue ? QQPalette.qqBlue : .primary)
                        .padding(.horizontal, 9)
                        .padding(.vertical, 5)
                        .background(
                            RoundedRectangle(cornerRadius: 7, style: .continuous)
                                .fill(showInspector.wrappedValue ? QQPalette.qqBlue.opacity(0.14) : Color.primary.opacity(0.05))
                        )
                    }
                    .buttonStyle(.plain)
                }
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 11)
        .background(Color(nsColor: .controlBackgroundColor).opacity(0.6))
    }

    private func searchHeaderBar(_ channel: Channel) -> some View {
        HStack(spacing: 8) {
            Image(systemName: "magnifyingglass")
                .foregroundColor(.secondary)
            TextField("搜索 #\(channel.name) 频道聊天记录…", text: $searchQuery)
                .textFieldStyle(.plain)
                .font(.system(size: 12.5))
                .onSubmit {
                    let q = searchQuery.trimmingCharacters(in: .whitespacesAndNewlines)
                    guard !q.isEmpty else { return }
                    Task { await store.sendChannelText(channelID: channel.id, text: "/search \(q)") }
                }

            if !searchQuery.isEmpty {
                Text("匹配 \(thread.count) 条")
                    .font(.system(size: 11))
                    .foregroundColor(.secondary)
                Button {
                    searchQuery = ""
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .foregroundColor(.secondary)
                }
                .buttonStyle(.plain)
            }

            Button("关闭") {
                showSearchBar = false
                searchQuery = ""
            }
            .buttonStyle(.plain)
            .font(.system(size: 11.5, weight: .medium))
            .foregroundColor(QQPalette.qqBlue)
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
        .background(Color.primary.opacity(0.03))
    }

    private var messages: some View {
        ScrollViewReader { proxy in
            ScrollView {
                if thread.isEmpty {
                    VStack(spacing: 10) {
                        Spacer(minLength: 80)
                        Image(systemName: "bubble.left.and.exclamationmark.bubble.right")
                            .font(.system(size: 32))
                            .foregroundColor(.secondary.opacity(0.5))
                        Text("欢迎加入频道，发送第一条频道消息吧")
                            .font(.system(size: 13, weight: .medium))
                            .foregroundColor(.secondary)
                        Spacer(minLength: 80)
                    }
                    .frame(maxWidth: .infinity)
                    .padding()
                } else {
                    LazyVStack(alignment: .leading, spacing: 14) {
                        ForEach(thread) { msg in
                            BubbleRow(
                                message: msg,
                                isSelf: !msg.inbound,
                                senderName: msg.senderName,
                                senderKey: msg.inbound ? msg.peerId : (store.selfId.isEmpty ? "self" : store.selfId),
                                onReply: { target in
                                    replyingTo = target
                                },
                                onRevoke: { target in
                                    Task { await store.sendChannelText(channelID: channelID, text: "/revoke \(target.id)") }
                                },
                                onMarkRead: { target in
                                    Task { await store.sendChannelText(channelID: channelID, text: "/read \(target.id)") }
                                },
                                onReact: { target in
                                    Task { await store.sendChannelText(channelID: channelID, text: "/react \(target.id) 👍") }
                                },
                                onPin: { target in
                                    Task { await store.sendChannelText(channelID: channelID, text: "/pin \(target.id)") }
                                }
                            )
                            .id(msg.id)
                        }
                    }
                    .padding(.horizontal, 16)
                    .padding(.vertical, 14)
                }
            }
            .onChange(of: thread.count) { _ in
                if let last = thread.last { proxy.scrollTo(last.id, anchor: .bottom) }
            }
        }
    }

    private func joinPrompt(_ channel: Channel) -> some View {
        VStack(spacing: 14) {
            Spacer()
            QQAvatar(
                key: channel.id,
                title: channel.name,
                size: 64,
                systemIcon: channel.isPrivate == true ? "lock.fill" : "number"
            )
            Text("# \(channel.name)")
                .font(.system(size: 17, weight: .bold))

            if channel.isPrivate == true {
                Label("这是私有频道，仅受邀成员可以加入", systemImage: "lock.fill")
                    .font(.system(size: 13))
                    .foregroundColor(.secondary)
            } else {
                Text("你尚未加入此频道（当前 \(channel.members.count) 名成员）")
                    .font(.system(size: 13))
                    .foregroundColor(.secondary)
                Button {
                    Task { await store.joinChannel(channel.id) }
                } label: {
                    Text("立即加入频道")
                        .font(.system(size: 13.5, weight: .semibold))
                        .foregroundColor(.white)
                        .padding(.horizontal, 22)
                        .padding(.vertical, 9)
                        .background(
                            RoundedRectangle(cornerRadius: 10, style: .continuous)
                                .fill(QQPalette.qqBlue)
                        )
                }
                .buttonStyle(.plain)
            }
            Spacer()
        }
        .frame(maxWidth: .infinity)
    }

    private func composer(_ channel: Channel) -> some View {
        VStack(spacing: 0) {
            Divider()

            if let replyingTo {
                HStack(spacing: 8) {
                    RoundedRectangle(cornerRadius: 2)
                        .fill(QQPalette.qqBlue)
                        .frame(width: 3, height: 22)
                    Text("回复：\(replyingTo.text)")
                        .font(.system(size: 12))
                        .foregroundColor(.secondary)
                        .lineLimit(1)
                    Spacer()
                    Button {
                        self.replyingTo = nil
                    } label: {
                        Image(systemName: "xmark.circle.fill")
                            .foregroundColor(.secondary)
                    }
                    .buttonStyle(.plain)
                }
                .padding(.horizontal, 14)
                .padding(.vertical, 6)
                .background(QQPalette.qqBlue.opacity(0.06))
                Divider()
            }

            HStack(spacing: 6) {
                Button {
                    showStickerPopover.toggle()
                } label: {
                    Text("😊 表情贴纸")
                        .font(.system(size: 12, weight: .medium))
                        .foregroundColor(.primary.opacity(0.85))
                        .padding(.horizontal, 8)
                        .padding(.vertical, 4)
                        .background(
                            RoundedRectangle(cornerRadius: 6, style: .continuous)
                                .fill(showStickerPopover ? QQPalette.qqBlue.opacity(0.14) : Color.clear)
                        )
                }
                .buttonStyle(.plain)
                .help("向频道发送表情贴纸")
                .popover(isPresented: $showStickerPopover, arrowEdge: .top) {
                    StickerPickerPopover(
                        onSendSticker: { stickerPath in
                            showStickerPopover = false
                            Task { await store.sendSticker(to: channel.id, path: stickerPath, threadKey: channel.id) }
                        },
                        onInsertEmoji: { emoji in
                            draft += emoji
                            showStickerPopover = false
                        }
                    )
                }

                Button {
                    pickAndOffer(channel)
                } label: {
                    Text("📁 发送文件")
                        .font(.system(size: 12, weight: .medium))
                        .foregroundColor(.primary.opacity(0.85))
                        .padding(.horizontal, 8)
                        .padding(.vertical, 4)
                }
                .buttonStyle(.plain)
                .help("向频道发送文件")

                Button {
                    let cmd = isE2EE ? "/e2ee off" : "/e2ee on"
                    Task { await store.sendChannelText(channelID: channel.id, text: cmd) }
                } label: {
                    Text(isE2EE ? "🔐 已加密 (/e2ee)" : "🔐 端到端加密 (/e2ee)")
                        .font(.system(size: 12, weight: .medium))
                        .foregroundColor(isE2EE ? QQPalette.onlineGreen : .primary.opacity(0.85))
                        .padding(.horizontal, 8)
                        .padding(.vertical, 4)
                        .background(
                            RoundedRectangle(cornerRadius: 6, style: .continuous)
                                .fill(isE2EE ? QQPalette.onlineGreen.opacity(0.14) : Color.clear)
                        )
                }
                .buttonStyle(.plain)

                Button {
                    showSearchBar.toggle()
                } label: {
                    Text("🔍 搜索记录")
                        .font(.system(size: 12, weight: .medium))
                        .foregroundColor(showSearchBar ? QQPalette.qqBlue : .primary.opacity(0.85))
                        .padding(.horizontal, 8)
                        .padding(.vertical, 4)
                        .background(
                            RoundedRectangle(cornerRadius: 6, style: .continuous)
                                .fill(showSearchBar ? QQPalette.qqBlue.opacity(0.14) : Color.clear)
                        )
                }
                .buttonStyle(.plain)

                Spacer()
            }
            .padding(.horizontal, 10)
            .padding(.top, 8)
            .padding(.bottom, 4)

            ZStack(alignment: .topLeading) {
                TextEditor(text: $draft)
                    .font(.system(size: 13.5))
                    .frame(minHeight: 58, maxHeight: 110)
                    .scrollContentBackground(.hidden)
                    .padding(.horizontal, 10)
                    .padding(.vertical, 4)

                if draft.isEmpty {
                    Text(replyingTo != nil ? "输入回复内容…" : "在 #\(channel.name) 频道内发言…")
                        .font(.system(size: 13))
                        .foregroundColor(.secondary.opacity(0.6))
                        .padding(.horizontal, 15)
                        .padding(.vertical, 8)
                        .allowsHitTesting(false)
                }
            }

            HStack {
                Text("仅频道内 \(channel.members.count) 名成员可见 · 支持 P2P 群文件共享")
                    .font(.system(size: 11))
                    .foregroundColor(.secondary.opacity(0.75))
                Spacer()
                Button {
                    send(channel)
                } label: {
                    Text("发送")
                        .font(.system(size: 13, weight: .semibold))
                        .foregroundColor(.white)
                        .padding(.horizontal, 20)
                        .padding(.vertical, 6)
                        .background(
                            RoundedRectangle(cornerRadius: 8, style: .continuous)
                                .fill(
                                    draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                                        ? Color.gray.opacity(0.45)
                                        : QQPalette.qqBlue
                                )
                        )
                }
                .buttonStyle(.plain)
                .keyboardShortcut(.defaultAction)
                .disabled(draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
            .padding(.horizontal, 14)
            .padding(.bottom, 10)
            .padding(.top, 2)
        }
        .background(Color(nsColor: .controlBackgroundColor).opacity(0.45))
    }

    private func send(_ channel: Channel) {
        let raw = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !raw.isEmpty else { return }
        draft = ""
        let payload: String
        if let target = replyingTo, !raw.hasPrefix("/") {
            payload = "/reply \(target.id) \(raw)"
        } else {
            payload = raw
        }
        replyingTo = nil
        Task { await store.sendChannelText(channelID: channel.id, text: payload) }
    }

    private func pickAndOffer(_ channel: Channel) {
        let panel = NSOpenPanel()
        panel.allowsMultipleSelection = false
        panel.canChooseDirectories = true
        panel.canChooseFiles = true
        guard panel.runModal() == .OK, let url = panel.url else { return }
        Task { await store.offerFileToChannel(channelID: channel.id, path: url.path) }
    }
}

/// 频道成员列表：展示成员昵称，非在线成员标注。
private struct MemberList: View {
    @ObservedObject var store: ChatStore
    let channel: Channel

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("频道成员 (\(channel.members.count))")
                .font(.system(size: 13, weight: .bold))
            Divider()
            ForEach(channel.members, id: \.self) { id in
                HStack(spacing: 8) {
                    let online = store.peers.first(where: { $0.id == id })
                    let isSelf = (id == store.selfId)
                    Image(systemName: "circle.fill")
                        .font(.system(size: 7))
                        .foregroundColor((online != nil || isSelf) ? QQPalette.onlineGreen : .gray)
                    Text(isSelf ? store.peerNickname(for: id) : (online?.nickname ?? String(id.prefix(8))))
                        .font(.system(size: 12.5))
                    Spacer()
                    if id == channel.ownerId {
                        Text("群主")
                            .font(.system(size: 10, weight: .semibold))
                            .foregroundColor(QQPalette.qqBlue)
                            .padding(.horizontal, 6)
                            .padding(.vertical, 1.5)
                            .background(Capsule().fill(QQPalette.qqBlue.opacity(0.14)))
                    }
                }
            }
        }
        .padding(14)
        .frame(minWidth: 220)
    }
}

/// 邀请成员：列出在线 peers，已是成员者禁用，本人不出现在列表中。
private struct InviteList: View {
    @ObservedObject var store: ChatStore
    let channel: Channel

    private var candidates: [Peer] {
        store.peers.filter {
            $0.id != store.selfId && !channel.members.contains($0.id)
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("邀请在线成员加入 #\(channel.name)")
                .font(.system(size: 13, weight: .bold))
            Divider()
            if candidates.isEmpty {
                Text("暂无可邀请的其他在线成员")
                    .font(.system(size: 12))
                    .foregroundColor(.secondary)
                    .padding(.vertical, 6)
            } else {
                ForEach(candidates) { peer in
                    Button {
                        Task {
                            await store.inviteMember(channelID: channel.id,
                                                     memberID: peer.id)
                        }
                    } label: {
                        HStack(spacing: 8) {
                            Image(systemName: "circle.fill")
                                .font(.system(size: 7))
                                .foregroundColor(QQPalette.onlineGreen)
                            Text(peer.nickname)
                                .font(.system(size: 12.5, weight: .medium))
                            Spacer()
                            Text("邀请")
                                .font(.system(size: 11, weight: .semibold))
                                .foregroundColor(QQPalette.qqBlue)
                                .padding(.horizontal, 8)
                                .padding(.vertical, 2.5)
                                .background(Capsule().fill(QQPalette.qqBlue.opacity(0.12)))
                        }
                        .padding(.vertical, 3)
                    }
                    .buttonStyle(.plain)
                }
            }
        }
        .padding(14)
        .frame(minWidth: 240)
    }
}
