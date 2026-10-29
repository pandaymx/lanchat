import SwiftUI

/// 频道 / 全员广播群聊界面（手机 QQ 群聊气泡与底部工具栏风格，支持 group="*" 全员广播群与 G2 自定义频道）。
struct ChannelChatView: View {
    @EnvironmentObject private var store: AppStore
    @EnvironmentObject private var settings: SettingsStore
    @Environment(\.dismiss) private var dismiss
    let channel: Channel

    @State private var draft = ""
    @State private var showPicker = false
    @State private var showStickerFilePicker = false
    @State private var showStickerDrawer = false
    @State private var showSearchBar = false
    @State private var searchQuery = ""
    @State private var e2eeEnabled = false
    @State private var callMuted = false
    @State private var replyingTo: ChatMessage?
    @State private var editingMessage: ChatMessage?
    @State private var editDraft = ""
    @State private var showMembers = false

    private var isBroadcastGroup: Bool {
        channel.id == "*"
    }

    /// 实时频道快照（channel 为进入时的快照，成员 / 主题需跟随 store 更新）。
    private var live: Channel {
        if isBroadcastGroup {
            return Channel(
                id: "*",
                name: "📢 局域网全员广播群",
                ownerId: store.selfId,
                topic: "全员广播群 (group=*) · 在线全员可见 · 支持 G1 Swarm 群发",
                isPrivate: false,
                members: store.peers.map(\.id)
            )
        }
        return store.channels.first { $0.id == channel.id } ?? channel
    }

    private var thread: [ChatMessage] {
        let all = store.messagesByPeer[channel.id] ?? []
        let q = searchQuery.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard showSearchBar, !q.isEmpty else { return all }
        return all.filter { !$0.revoked && $0.text.lowercased().contains(q) }
    }

    var body: some View {
        VStack(spacing: 0) {
            // 频道顶部信息横幅（私有锁 / 主题 / 双栈链路标识 / E2EE 状态）
            ChannelHeader(
                channel: live,
                isBroadcast: isBroadcastGroup,
                e2eeEnabled: e2eeEnabled,
                linkBadge: ServerInfo.linkBadge(server: store.server)
            )

            if ["ringing", "calling", "active", "in_call"].contains(store.voiceCall?.state ?? "")
                && (store.voiceCall?.groupId == live.id || store.voiceCall?.groupId.isEmpty == true) {
                let isRinging = store.voiceCall?.state == "ringing"
                let callerId = (store.voiceCall?.peerId.isEmpty == false) ? store.voiceCall!.peerId : live.ownerId
                let callerTitle = store.displayName(callerId)
                HStack(spacing: 8) {
                    Circle()
                        .fill(QQTheme.avatarColor(for: callerId))
                        .frame(width: 24, height: 24)
                        .overlay(
                            Text(QQTheme.initial(for: callerTitle))
                                .font(.system(size: 11, weight: .bold))
                                .foregroundStyle(.white)
                        )
                    Text(isRinging
                         ? "🔔 [频道语音来电] \(callerTitle) 邀请加入语音"
                         : "📞 频道实时语音进行中 · \(callerTitle)")
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(QQTheme.onlineGreen)
                        .lineLimit(1)
                    Spacer()
                    if isRinging {
                        Button {
                            callMuted = false
                            store.setVoiceMuted(false)
                            store.acceptVoiceCall(
                                to: callerId,
                                group: live.id,
                                remoteUDP: store.voiceCall?.remoteAddr ?? ""
                            )
                        } label: {
                            Text("🟢 接听")
                                .font(.caption2.weight(.bold))
                                .foregroundStyle(.white)
                                .padding(.horizontal, 10)
                                .padding(.vertical, 4)
                                .background(QQTheme.onlineGreen, in: Capsule())
                        }
                    } else {
                        Button {
                            callMuted.toggle()
                            store.setVoiceMuted(callMuted)
                        } label: {
                            Text(callMuted ? "🎙️ 开麦" : "🔇 静音")
                                .font(.caption2.weight(.bold))
                                .foregroundStyle(QQTheme.qqBlue)
                                .padding(.horizontal, 9)
                                .padding(.vertical, 4)
                                .background(QQTheme.qqBlue.opacity(0.14), in: Capsule())
                        }
                    }
                    Button {
                        callMuted = false
                        store.setVoiceMuted(false)
                        store.stopVoiceCall(to: callerId, group: live.id)
                    } label: {
                        Text("🔴 挂断")
                            .font(.caption2.weight(.bold))
                            .foregroundStyle(.white)
                            .padding(.horizontal, 10)
                            .padding(.vertical, 4)
                            .background(Color.red.opacity(0.88), in: Capsule())
                    }
                }
                .padding(.horizontal, 14)
                .padding(.vertical, 7)
                .background(QQTheme.onlineGreen.opacity(0.12))
            }

            // 群聊内搜索栏（点击底部「🔍 搜索」展开）
            if showSearchBar {
                channelSearchHeader
            }

            // 手机 QQ 群聊消息流
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(spacing: 12) {
                        if let firstMsg = thread.first {
                            Button {
                                let beforeMs = Int64(firstMsg.timestamp.timeIntervalSince1970 * 1000.0)
                                store.getHistoryPage(convID: live.id, beforeTS: beforeMs, limit: 50)
                            } label: {
                                Text("📜 加载更早历史消息（或上滑触顶自动加载）")
                                    .font(.caption2.weight(.semibold))
                                    .foregroundStyle(QQTheme.qqBlue)
                                    .padding(.horizontal, 12)
                                    .padding(.vertical, 4)
                                    .background(QQTheme.qqBlue.opacity(0.12), in: Capsule())
                            }
                            .buttonStyle(.plain)
                            .onAppear {
                                if thread.count >= 20 {
                                    let beforeMs = Int64(firstMsg.timestamp.timeIntervalSince1970 * 1000.0)
                                    store.getHistoryPage(convID: live.id, beforeTS: beforeMs, limit: 50)
                                }
                            }
                        }
                        ForEach(thread) { msg in
                            ChannelBubble(
                                message: msg,
                                onReply: { replyingTo = msg },
                                onRevoke: {
                                    store.revokeMessage(to: "", group: live.id, msgID: msg.id)
                                },
                                onMarkRead: {
                                    store.markMessageRead(to: "", group: live.id, msgID: msg.id)
                                },
                                onReact: {
                                    store.reactMessage(to: "", group: live.id, msgID: msg.id, emoji: "👍")
                                },
                                onEdit: {
                                    editDraft = QQTheme.extractEditableOriginal(msg.text)
                                    editingMessage = msg
                                },
                                onPin: {
                                    store.pinMessage(to: "", group: live.id, msgID: msg.id)
                                },
                                onReEdit: { original in
                                    let clean = QQTheme.extractEditableOriginal(original)
                                    if !clean.isEmpty {
                                        draft = clean
                                    }
                                }
                            )
                            .id(msg.id)
                            .padding(.horizontal, 12)
                        }
                    }
                    .padding(.vertical, 12)
                }
                .background(Color(.systemGroupedBackground))
                .onChange(of: thread.last?.id) { _ in
                    if let last = thread.last {
                        withAnimation { proxy.scrollTo(last.id, anchor: .bottom) }
                    }
                }
            }

            // 引用回复预览条
            if let reply = replyingTo {
                replyBanner(for: reply)
            }

            // 可展开 QQ 表情贴纸面板（调用 sendSticker）
            if showStickerDrawer {
                stickerDrawer
            }

            // 手机 QQ 底部快捷工具栏 + 胶囊输入框 + 发送按钮
            qqGroupComposerToolbar
        }
        .navigationTitle(live.name)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .navigationBarTrailing) {
                Button {
                    showMembers = true
                } label: {
                    HStack(spacing: 3) {
                        Image(systemName: "person.2.fill")
                        Text("\(live.members.count)")
                            .font(.caption.weight(.bold))
                    }
                    .foregroundStyle(QQTheme.qqBlue)
                }
            }
        }
        .sheet(isPresented: $showMembers) {
            ChannelMembersView(channel: live, isBroadcast: isBroadcastGroup)
        }
        .sheet(isPresented: $showPicker) {
            FilePicker(scenario: .channel) { url in
                store.offerFileToGroup(channel: live, path: url.path)
                showPicker = false
            }
            .ignoresSafeArea()
        }
        .sheet(isPresented: $showStickerFilePicker) {
            FilePicker(scenario: .channel) { url in
                store.sendSticker(to: live.id, path: url.path, group: live.id)
                showStickerFilePicker = false
            }
            .ignoresSafeArea()
        }
        .alert("✏️ 编辑已发送消息", isPresented: Binding(
            get: { editingMessage != nil },
            set: { if !$0 { editingMessage = nil } }
        )) {
            TextField("修改消息内容", text: $editDraft)
            Button("保存修改") {
                if let target = editingMessage {
                    let trimmed = editDraft.trimmingCharacters(in: .whitespacesAndNewlines)
                    if !trimmed.isEmpty {
                        store.editMessage(to: "", group: live.id, msgID: target.id, newText: trimmed)
                    }
                }
                editingMessage = nil
            }
            Button("取消", role: .cancel) {
                editingMessage = nil
            }
        }
        .onChange(of: store.isMember(live)) { isMember in
            // 退出自定义频道后自动返回列表（全员广播群 group="*" 常驻不退出）。
            if !isBroadcastGroup && !isMember {
                dismiss()
            }
        }
    }

    // MARK: - 群内搜索条

    private var channelSearchHeader: some View {
        HStack(spacing: 8) {
            Image(systemName: "magnifyingglass")
                .foregroundStyle(QQTheme.qqBlue)
            TextField("搜索群聊记录…", text: $searchQuery)
                .font(.footnote)
                .autocorrectionDisabled()
            if !searchQuery.isEmpty {
                Button("定位") {
                    store.sendGroupText(channel: live, text: "/search \(searchQuery)")
                }
                .font(.caption.weight(.bold))
                .foregroundStyle(QQTheme.qqBlue)
            }
            Button {
                searchQuery = ""
                showSearchBar = false
            } label: {
                Image(systemName: "xmark.circle.fill")
                    .foregroundStyle(.secondary)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
        .background(Color(.tertiarySystemGroupedBackground))
    }

    // MARK: - 引用回复预览条

    private func replyBanner(for msg: ChatMessage) -> some View {
        let cleanReply = QQTheme.splitQuote(QQTheme.splitReactions(msg.text).body).body
        return HStack(spacing: 8) {
            Rectangle()
                .fill(QQTheme.qqBlue)
                .frame(width: 3, height: 28)
                .clipShape(Capsule())

            VStack(alignment: .leading, spacing: 2) {
                Text("回复 \(msg.inbound ? store.displayName(msg.peerId) : settings.nickname)")
                    .font(.caption2.weight(.bold))
                    .foregroundStyle(QQTheme.qqBlue)
                Text(cleanReply)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }

            Spacer()

            Button {
                replyingTo = nil
            } label: {
                Image(systemName: "xmark.circle.fill")
                    .foregroundStyle(.secondary)
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 6)
        .background(Color(.secondarySystemGroupedBackground))
    }

    // MARK: - 可展开表情贴纸面板（调用 sendSticker）

    private var stickerDrawer: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text("😊 QQ 群聊表情贴纸")
                    .font(.caption.weight(.bold))
                    .foregroundStyle(.secondary)
                Spacer()
                Button {
                    showStickerFilePicker = true
                } label: {
                    HStack(spacing: 4) {
                        Image(systemName: "photo.badge.plus")
                        Text("从文件发送贴纸")
                    }
                    .font(.caption2.weight(.semibold))
                    .foregroundStyle(QQTheme.qqBlue)
                    .padding(.horizontal, 8)
                    .padding(.vertical, 4)
                    .background(QQTheme.qqBlue.opacity(0.12), in: Capsule())
                }
            }

            LazyVGrid(columns: Array(repeating: GridItem(.flexible(), spacing: 10), count: 4), spacing: 10) {
                ForEach(QQTheme.presetStickers) { item in
                    Button {
                        draft += item.emoji
                        showStickerDrawer = false
                    } label: {
                        VStack(spacing: 4) {
                            Text(item.emoji)
                                .font(.system(size: 26))
                            Text(item.title)
                                .font(.caption2)
                                .foregroundStyle(.primary)
                        }
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 8)
                        .background(
                            RoundedRectangle(cornerRadius: 12, style: .continuous)
                                .fill(Color(.tertiarySystemGroupedBackground))
                        )
                    }
                    .buttonStyle(.plain)
                }
            }
        }
        .padding(12)
        .background(Color(.secondarySystemGroupedBackground))
    }

    // MARK: - 手机 QQ 底部快捷工具栏与胶囊输入区

    private var qqGroupComposerToolbar: some View {
        VStack(spacing: 8) {
            Divider()

            // 快捷功能图标栏：😊 表情贴纸 · 📁 发送文件 · 🔐 E2EE 加密 · 🔍 搜索
            HStack(spacing: 10) {
                Button {
                    withAnimation(.easeInOut(duration: 0.18)) {
                        showStickerDrawer.toggle()
                    }
                } label: {
                    Text("😊 表情贴纸")
                        .font(.caption2.weight(.semibold))
                        .foregroundStyle(showStickerDrawer ? .white : .primary)
                        .padding(.horizontal, 10)
                        .padding(.vertical, 5)
                        .background(
                            showStickerDrawer
                                ? QQTheme.qqBlue
                                : Color(.tertiarySystemGroupedBackground),
                            in: Capsule()
                        )
                }

                Button {
                    showPicker = true
                } label: {
                    Text("📁 发送文件")
                        .font(.caption2.weight(.semibold))
                        .foregroundStyle(.primary)
                        .padding(.horizontal, 10)
                        .padding(.vertical, 5)
                        .background(Color(.tertiarySystemGroupedBackground), in: Capsule())
                }

                Button {
                    e2eeEnabled.toggle()
                    store.setSessionE2EE(e2eeEnabled ? "on" : "off")
                } label: {
                    Text("🔐 E2EE 加密")
                        .font(.caption2.weight(.semibold))
                        .foregroundStyle(e2eeEnabled ? .white : .primary)
                        .padding(.horizontal, 10)
                        .padding(.vertical, 5)
                        .background(
                            e2eeEnabled
                                ? QQTheme.onlineGreen
                                : Color(.tertiarySystemGroupedBackground),
                            in: Capsule()
                        )
                }

                Button {
                    withAnimation(.easeInOut(duration: 0.18)) {
                        showSearchBar.toggle()
                    }
                } label: {
                    Text("🔍 搜索")
                        .font(.caption2.weight(.semibold))
                        .foregroundStyle(showSearchBar ? .white : .primary)
                        .padding(.horizontal, 10)
                        .padding(.vertical, 5)
                        .background(
                            showSearchBar
                                ? QQTheme.qqBlue
                                : Color(.tertiarySystemGroupedBackground),
                            in: Capsule()
                        )
                }

                Spacer(minLength: 0)
            }
            .padding(.horizontal, 12)

            // 胶囊文本输入框 + QQ 蓝「发送」按钮
            HStack(alignment: .bottom, spacing: 10) {
                TextField("在「\(live.name)」中发言…", text: $draft, axis: .vertical)
                    .lineLimit(1...5)
                    .padding(.horizontal, 14)
                    .padding(.vertical, 9)
                    .background(Color(.tertiarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 20, style: .continuous))

                Button {
                    sendCurrentDraft()
                } label: {
                    Text("发送")
                        .font(.subheadline.weight(.bold))
                        .foregroundStyle(.white)
                        .padding(.horizontal, 18)
                        .padding(.vertical, 9)
                        .background(
                            draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                                ? QQTheme.qqBlue.opacity(0.45)
                                : QQTheme.qqBlue,
                            in: Capsule()
                        )
                }
                .disabled(draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
            .padding(.horizontal, 12)
            .padding(.bottom, 8)
        }
        .background(Color(.secondarySystemGroupedBackground))
    }

    private func sendCurrentDraft() {
        let trimmed = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }
        if let reply = replyingTo {
            store.sendGroupText(channel: live, text: "/reply \(reply.id) \(trimmed)")
            replyingTo = nil
        } else {
            store.sendGroupText(channel: live, text: trimmed)
        }
        draft = ""
    }
}

/// 频道信息头部：私有锁标记 + 主题 + Swarm 标识。
private struct ChannelHeader: View {
    let channel: Channel
    let isBroadcast: Bool
    let e2eeEnabled: Bool
    var linkBadge: String = "⚡ 局域网直连"

    var body: some View {
        HStack(spacing: 8) {
            if isBroadcast {
                Text("📢 全员广播")
                    .font(.caption2.weight(.bold))
                    .foregroundStyle(QQTheme.qqBlue)
                    .padding(.horizontal, 7)
                    .padding(.vertical, 2)
                    .background(QQTheme.qqBlue.opacity(0.12), in: Capsule())
            } else if channel.isPrivate {
                HStack(spacing: 3) {
                    Image(systemName: "lock.fill")
                    Text("私有")
                }
                .font(.caption2.weight(.bold))
                .foregroundStyle(.orange)
                .padding(.horizontal, 7)
                .padding(.vertical, 2)
                .background(Color.orange.opacity(0.12), in: Capsule())
            } else {
                Text("# 公开频道")
                    .font(.caption2.weight(.bold))
                    .foregroundStyle(QQTheme.qqBlue)
                    .padding(.horizontal, 7)
                    .padding(.vertical, 2)
                    .background(QQTheme.qqBlue.opacity(0.12), in: Capsule())
            }

            if !channel.topic.isEmpty {
                Text(channel.topic)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }

            Spacer()

            Text(linkBadge)
                .font(.caption2.weight(.medium))
                .foregroundStyle(.secondary)

            if e2eeEnabled {
                Text("🔐 E2EE")
                    .font(.caption2.weight(.bold))
                    .foregroundStyle(QQTheme.onlineGreen)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 7)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(.secondarySystemGroupedBackground))
    }
}

/// 频道消息气泡：复用手机 QQ 36x36 圆形头像 + 昵称时间 + #0099FF 气泡 + 回复/撤回。
private struct ChannelBubble: View {
    @EnvironmentObject private var store: AppStore
    @EnvironmentObject private var settings: SettingsStore
    let message: ChatMessage
    var onReply: (() -> Void)? = nil
    var onRevoke: (() -> Void)? = nil
    var onMarkRead: (() -> Void)? = nil
    var onReact: (() -> Void)? = nil
    var onEdit: (() -> Void)? = nil
    var onPin: (() -> Void)? = nil
    var onReEdit: ((String) -> Void)? = nil

    var body: some View {
        BubbleRow(
            message: message,
            senderName: message.inbound ? store.displayName(message.peerId) : settings.nickname,
            senderAvatarKey: message.inbound ? message.peerId : store.selfId,
            onReply: onReply,
            onRevoke: onRevoke,
            onMarkRead: onMarkRead,
            onReact: onReact,
            onEdit: onEdit,
            onPin: onPin,
            onReEdit: onReEdit
        )
    }
}

/// 频道成员列表：标注所有者；owner 可邀请在线成员，成员可退出频道。
private struct ChannelMembersView: View {
    @EnvironmentObject private var store: AppStore
    @Environment(\.dismiss) private var dismiss
    let channel: Channel
    var isBroadcast: Bool = false

    private var isOwner: Bool { channel.ownerId == store.selfId }

    /// 可邀请的在线用户（排除自己；已为成员者在行内禁用）。
    private var inviteCandidates: [Peer] {
        store.peers.filter { $0.id != store.selfId }
    }

    var body: some View {
        NavigationStack {
            List {
                if !isBroadcast && isOwner {
                    Section("邀请在线成员") {
                        if inviteCandidates.isEmpty {
                            Text("当前没有其他在线用户可邀请")
                                .foregroundStyle(.secondary)
                        }
                        ForEach(inviteCandidates) { peer in
                            HStack(spacing: 10) {
                                Circle()
                                    .fill(QQTheme.avatarColor(for: peer.id))
                                    .frame(width: 32, height: 32)
                                    .overlay(
                                        Text(QQTheme.initial(for: peer.nickname))
                                            .font(.caption.weight(.bold))
                                            .foregroundStyle(.white)
                                    )
                                Text(peer.nickname)
                                Spacer()
                                if channel.members.contains(peer.id) {
                                    Text("已加入")
                                        .font(.caption2)
                                        .foregroundStyle(.secondary)
                                } else {
                                    Button("邀请") {
                                        store.inviteMember(channel, memberID: peer.id)
                                    }
                                    .buttonStyle(.borderedProminent)
                                    .tint(QQTheme.qqBlue)
                                    .font(.caption)
                                }
                            }
                            .disabled(channel.members.contains(peer.id))
                        }
                    }
                }

                Section("成员 (\(channel.members.count))") {
                    ForEach(channel.members, id: \.self) { memberId in
                        HStack(spacing: 10) {
                            Circle()
                                .fill(QQTheme.avatarColor(for: memberId))
                                .frame(width: 32, height: 32)
                                .overlay(
                                    Text(QQTheme.initial(for: store.displayName(memberId)))
                                        .font(.caption.weight(.bold))
                                        .foregroundStyle(.white)
                                )
                            Text(store.displayName(memberId))
                            Spacer()
                            if !isBroadcast && memberId == channel.ownerId {
                                Text("群主")
                                    .font(.caption2.weight(.semibold))
                                    .foregroundStyle(QQTheme.qqBlue)
                                    .padding(.horizontal, 6)
                                    .padding(.vertical, 2)
                                    .background(QQTheme.qqBlue.opacity(0.12), in: Capsule())
                            }
                        }
                    }
                }

                if !isBroadcast {
                    Section {
                        Button(role: .destructive) {
                            store.leaveChannel(channel)
                        } label: {
                            Label("退出频道", systemImage: "rectangle.portrait.and.arrow.right")
                        }
                    }
                }
            }
            .navigationTitle(isBroadcast ? "全员广播群成员" : "频道成员")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .navigationBarTrailing) {
                    Button("完成") { dismiss() }
                }
            }
        }
    }
}
