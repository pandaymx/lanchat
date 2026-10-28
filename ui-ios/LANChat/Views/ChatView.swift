import SwiftUI

/// 1:1 聊天界面（手机 QQ 聊天气泡与底部工具栏风格）。
struct ChatView: View {
    @EnvironmentObject private var store: AppStore
    @EnvironmentObject private var settings: SettingsStore
    let peer: Peer

    @State private var draft = ""
    @State private var showPicker = false
    @State private var showStickerFilePicker = false
    @State private var showStickerDrawer = false
    @State private var showSearchBar = false
    @State private var searchQuery = ""
    @State private var e2eeEnabled = false
    @State private var inVoiceCall = false
    @State private var callMuted = false
    @State private var isScreenSharing = false
    @State private var replyingTo: ChatMessage?
    @State private var editingMessage: ChatMessage?
    @State private var editDraft = ""

    private var thread: [ChatMessage] {
        let all = store.messagesByPeer[peer.id] ?? []
        let q = searchQuery.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard showSearchBar, !q.isEmpty else { return all }
        return all.filter { !$0.revoked && $0.text.lowercased().contains(q) }
    }

    private var pinnedMessage: ChatMessage? {
        (store.messagesByPeer[peer.id] ?? []).last { msg in
            !msg.revoked && (msg.pinned || msg.text.hasPrefix("📌 "))
        }
    }

    private var activeScreenFrame: String? {
        if let f = store.screenFramesByConversation[peer.id], !f.isEmpty {
            return f
        }
        if let ss = store.screenShare, !ss.latestFrame.isEmpty, (ss.peerId == peer.id || ss.peerId.isEmpty) {
            return ss.latestFrame
        }
        return nil
    }

    var body: some View {
        VStack(spacing: 0) {
            // 顶部状态副横幅（OS / 在线状态 / 双栈直连徽章 / E2EE 指示）
            chatStatusSubheader

            if let pinned = pinnedMessage {
                HStack(spacing: 6) {
                    Text("📌 [置顶] \(QQTheme.splitQuote(QQTheme.splitReactions(pinned.text).body).body)")
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.orange)
                        .lineLimit(1)
                    Spacer()
                }
                .padding(.horizontal, 14)
                .padding(.vertical, 5)
                .background(Color.orange.opacity(0.12))
            }

            if inVoiceCall || ["ringing", "calling", "active", "in_call"].contains(store.voiceCall?.state ?? "") {
                let isRinging = store.voiceCall?.state == "ringing"
                let callerId = (store.voiceCall?.peerId.isEmpty == false) ? store.voiceCall!.peerId : peer.id
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
                         ? "🔔 [来电响铃] \(callerTitle) 邀请实时语音通话"
                         : "📞 实时低延迟语音通话中 · \(callerTitle)")
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(QQTheme.onlineGreen)
                        .lineLimit(1)
                    Spacer()
                    if isRinging {
                        Button {
                            inVoiceCall = true
                            callMuted = false
                            store.setVoiceMuted(false)
                            store.acceptVoiceCall(
                                to: callerId,
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
                        inVoiceCall = false
                        callMuted = false
                        store.setVoiceMuted(false)
                        store.stopVoiceCall(to: callerId)
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

            if isScreenSharing || activeScreenFrame != nil {
                VStack(spacing: 6) {
                    HStack(spacing: 8) {
                        Text(activeScreenFrame != nil
                             ? "🖥️ 实时屏幕共享画布 · 点击画面发送激光笔红点批注"
                             : "🖥️ 实时屏幕共享中 · 支持激光笔坐标批注")
                            .font(.caption.weight(.semibold))
                            .foregroundStyle(.white)
                            .lineLimit(1)
                        Spacer()
                        Button {
                            store.sendScreenAnnotation(to: peer.id, normX: 0.5, normY: 0.5, note: "重点关注此处")
                        } label: {
                            Text("📍 激光批注")
                                .font(.caption2.weight(.bold))
                                .foregroundStyle(.white)
                                .padding(.horizontal, 8)
                                .padding(.vertical, 3)
                                .background(QQTheme.qqBlue, in: Capsule())
                        }
                        Button {
                            isScreenSharing = false
                            store.stopScreenShare(to: peer.id)
                            store.clearScreenFrame(for: peer.id)
                        } label: {
                            Text("⏹ 关闭")
                                .font(.caption2.weight(.bold))
                                .foregroundStyle(.white)
                                .padding(.horizontal, 8)
                                .padding(.vertical, 3)
                                .background(Color.red.opacity(0.85), in: Capsule())
                        }
                    }
                    if let frameStr = activeScreenFrame, let uiImg = decodeScreenUIImage(from: frameStr) {
                        GeometryReader { geo in
                            Image(uiImage: uiImg)
                                .resizable()
                                .scaledToFit()
                                .frame(width: geo.size.width, height: geo.size.height)
                                .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
                                .contentShape(Rectangle())
                                .gesture(
                                    DragGesture(minimumDistance: 0)
                                        .onEnded { value in
                                            let w = max(geo.size.width, 1)
                                            let h = max(geo.size.height, 1)
                                            let nx = min(max(Double(value.location.x / w), 0.0), 1.0)
                                            let ny = min(max(Double(value.location.y / h), 0.0), 1.0)
                                            store.sendScreenAnnotation(
                                                to: peer.id,
                                                normX: nx,
                                                normY: ny,
                                                note: "🔴 激光笔点击批注"
                                            )
                                        }
                                )
                        }
                        .frame(height: 170)
                    }
                }
                .padding(.horizontal, 14)
                .padding(.vertical, 8)
                .background(Color.black.opacity(0.86))
            }

            // 聊天内搜索条（点击底部「🔍 搜索」展开）
            if showSearchBar {
                chatSearchHeader
            }

            // 手机 QQ 聊天气泡流
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(spacing: 12) {
                        if let firstMsg = thread.first {
                            Button {
                                let beforeMs = Int64(firstMsg.timestamp.timeIntervalSince1970 * 1000.0)
                                store.getHistoryPage(convID: peer.id, beforeTS: beforeMs, limit: 50)
                            } label: {
                                Text("📜 加载更早历史消息")
                                    .font(.caption2.weight(.semibold))
                                    .foregroundStyle(QQTheme.qqBlue)
                                    .padding(.horizontal, 12)
                                    .padding(.vertical, 4)
                                    .background(QQTheme.qqBlue.opacity(0.12), in: Capsule())
                            }
                            .buttonStyle(.plain)
                        }
                        ForEach(thread) { msg in
                            BubbleRow(
                                message: msg,
                                senderName: msg.inbound ? peer.nickname : settings.nickname,
                                senderAvatarKey: msg.inbound ? peer.id : store.selfId,
                                onReply: { replyingTo = msg },
                                onRevoke: {
                                    store.revokeMessage(to: peer.id, msgID: msg.id)
                                },
                                onMarkRead: {
                                    store.markMessageRead(to: peer.id, msgID: msg.id)
                                },
                                onReact: {
                                    store.reactMessage(to: peer.id, msgID: msg.id, emoji: "👍")
                                },
                                onEdit: {
                                    editDraft = QQTheme.extractEditableOriginal(msg.text)
                                    editingMessage = msg
                                },
                                onPin: {
                                    store.pinMessage(to: peer.id, msgID: msg.id)
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
                .onChange(of: thread.count) { _ in
                    if let last = thread.last {
                        withAnimation { proxy.scrollTo(last.id, anchor: .bottom) }
                    }
                }
            }

            // 引用回复预览条
            if let reply = replyingTo {
                replyBanner(for: reply)
            }

            // 可展开 QQ 表情贴纸面板（点击「😊 表情贴纸」展开）
            if showStickerDrawer {
                stickerDrawer
            }

            // 手机 QQ 底部快捷工具栏 + 胶囊输入框 + 发送按钮
            qqComposerToolbar
        }
        .navigationTitle(peer.nickname)
        .navigationBarTitleDisplayMode(.inline)
        .sheet(isPresented: $showPicker) {
            FilePicker(scenario: .unicast) { url in
                store.offerFile(to: peer.id, path: url.path)
                showPicker = false
            }
            .ignoresSafeArea()
        }
        .sheet(isPresented: $showStickerFilePicker) {
            FilePicker(scenario: .unicast) { url in
                store.sendSticker(to: peer.id, path: url.path)
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
                        store.editMessage(to: peer.id, msgID: target.id, newText: trimmed)
                    }
                }
                editingMessage = nil
            }
            Button("取消", role: .cancel) {
                editingMessage = nil
            }
        }
    }

    // MARK: - 顶部会话状态条

    private var chatStatusSubheader: some View {
        let linkBadge = ServerInfo.linkBadge(server: store.server, status: peer.status)
        let callActive = inVoiceCall || ["ringing", "calling", "active", "in_call"].contains(store.voiceCall?.state ?? "")
        return HStack(spacing: 8) {
            Circle()
                .fill(peer.status == "away" ? QQTheme.awayOrange : QQTheme.onlineGreen)
                .frame(width: 8, height: 8)
            Text("\(peer.os.uppercased()) · \(peer.status == "away" ? "离开" : "在线") · \(linkBadge)")
                .font(.caption2.weight(.medium))
                .foregroundStyle(.secondary)
                .lineLimit(1)

            Spacer()

            Button {
                if callActive {
                    inVoiceCall = false
                    store.stopVoiceCall(to: peer.id)
                } else {
                    inVoiceCall = true
                    store.startVoiceCall(to: peer.id)
                }
            } label: {
                Text(callActive ? "📵 挂断" : "📞 语音通话")
                    .font(.caption2.weight(.semibold))
                    .foregroundStyle(callActive ? .red : QQTheme.qqBlue)
                    .padding(.horizontal, 8)
                    .padding(.vertical, 2)
                    .background((callActive ? Color.red : QQTheme.qqBlue).opacity(0.12), in: Capsule())
            }

            Button {
                if isScreenSharing {
                    isScreenSharing = false
                    store.stopScreenShare(to: peer.id)
                } else {
                    isScreenSharing = true
                    store.startScreenShare(to: peer.id, fps: 15)
                }
            } label: {
                Text(isScreenSharing ? "⏹ 停止投屏" : "🖥️ 投屏")
                    .font(.caption2.weight(.semibold))
                    .foregroundStyle(isScreenSharing ? .orange : QQTheme.qqBlue)
                    .padding(.horizontal, 8)
                    .padding(.vertical, 2)
                    .background((isScreenSharing ? Color.orange : QQTheme.qqBlue).opacity(0.12), in: Capsule())
            }

            Button {
                store.sendText(to: peer.id, text: "/sync")
            } label: {
                Text("🔄 同步")
                    .font(.caption2.weight(.semibold))
                    .foregroundStyle(QQTheme.qqBlue)
                    .padding(.horizontal, 8)
                    .padding(.vertical, 2)
                    .background(QQTheme.qqBlue.opacity(0.12), in: Capsule())
            }

            if e2eeEnabled {
                HStack(spacing: 3) {
                    Image(systemName: "lock.shield.fill")
                    Text("E2EE 端到端加密中")
                }
                .font(.caption2.weight(.semibold))
                .foregroundStyle(QQTheme.onlineGreen)
                .padding(.horizontal, 8)
                .padding(.vertical, 2)
                .background(QQTheme.onlineGreen.opacity(0.12), in: Capsule())
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 6)
        .background(Color(.secondarySystemGroupedBackground))
    }

    // MARK: - 会话内搜索栏

    private var chatSearchHeader: some View {
        HStack(spacing: 8) {
            Image(systemName: "magnifyingglass")
                .foregroundStyle(QQTheme.qqBlue)
            TextField("搜索聊天记录…", text: $searchQuery)
                .font(.footnote)
                .autocorrectionDisabled()
            if !searchQuery.isEmpty {
                Button("定位") {
                    store.sendText(to: peer.id, text: "/search \(searchQuery)")
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
                Text("回复 \(msg.inbound ? peer.nickname : settings.nickname)")
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
                Text("😊 QQ 表情贴纸")
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

    private var qqComposerToolbar: some View {
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
                    store.sendText(to: peer.id, text: "/voice 3")
                } label: {
                    Text("🎤 语音")
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
                    store.sendText(to: peer.id, text: "/fingerprint")
                } label: {
                    Text("🛡️ 安全码")
                        .font(.caption2.weight(.semibold))
                        .foregroundStyle(.primary)
                        .padding(.horizontal, 10)
                        .padding(.vertical, 5)
                        .background(Color(.tertiarySystemGroupedBackground), in: Capsule())
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
                TextField("输入消息…", text: $draft, axis: .vertical)
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
            store.sendText(to: peer.id, text: "/reply \(reply.id) \(trimmed)")
            replyingTo = nil
        } else {
            store.sendText(to: peer.id, text: trimmed)
        }
        draft = ""
    }

    private func decodeScreenUIImage(from raw: String) -> UIImage? {
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        let b64: String
        if let range = trimmed.range(of: ";base64,") {
            b64 = String(trimmed[range.upperBound...]).trimmingCharacters(in: .whitespacesAndNewlines)
        } else {
            b64 = trimmed
        }
        guard let data = Data(base64Encoded: b64) else { return nil }
        return UIImage(data: data)
    }
}

/// 手机 QQ 单条消息行：居中撤回灰条+重新编辑 / 36x36 圆形头像 + 昵称时间 + 气泡内引用卡片 + 长按菜单 + 点赞胶囊。
struct BubbleRow: View {
    let message: ChatMessage
    var senderName: String = ""
    var senderAvatarKey: String = ""
    var onReply: (() -> Void)? = nil
    var onRevoke: (() -> Void)? = nil
    var onMarkRead: (() -> Void)? = nil
    var onReact: (() -> Void)? = nil
    var onEdit: (() -> Void)? = nil
    var onPin: (() -> Void)? = nil
    var onReEdit: ((String) -> Void)? = nil

    private var parsed: (body: String, reactions: [String]) {
        QQTheme.splitReactions(message.text)
    }

    private var splitQuoteBody: (quote: String?, body: String) {
        QQTheme.splitQuote(parsed.body)
    }

    var body: some View {
        if message.revoked {
            // 手机 QQ 居中灰色系统撤回提示胶囊 + 「重新编辑」蓝色链接
            HStack(spacing: 6) {
                Text(message.inbound
                     ? "\"\(senderName.isEmpty ? message.peerId : senderName)\" 撤回了一条消息"
                     : "你撤回了一条消息")
                    .font(.caption)
                    .foregroundStyle(.secondary)

                if !message.inbound,
                   !message.originalText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
                   let onReEdit {
                    Button {
                        onReEdit(message.originalText)
                    } label: {
                        Text("重新编辑")
                            .font(.caption.weight(.semibold))
                            .foregroundStyle(QQTheme.qqBlue)
                    }
                    .buttonStyle(.plain)
                }
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 4)
            .background(Color.primary.opacity(0.06), in: RoundedRectangle(cornerRadius: 6, style: .continuous))
            .frame(maxWidth: .infinity, alignment: .center)
            .padding(.vertical, 2)
        } else {
            HStack(alignment: .top, spacing: 8) {
                if message.inbound {
                    avatarView
                } else {
                    Spacer(minLength: 44)
                }

                VStack(alignment: message.inbound ? .leading : .trailing, spacing: 4) {
                    // 发送者昵称 + 时间戳 + 已读标识
                    HStack(spacing: 6) {
                        if message.inbound && !senderName.isEmpty {
                            Text(senderName)
                                .font(.caption2.weight(.medium))
                                .foregroundStyle(.secondary)
                        }
                        if !message.inbound && !message.readBy.isEmpty {
                            Text("✓ 已读")
                                .font(.caption2.weight(.semibold))
                                .foregroundStyle(QQTheme.onlineGreen)
                        }
                        Text(QQTheme.formatTime(message.timestamp))
                            .font(.caption2)
                            .foregroundStyle(.tertiary)
                    }

                    // 手机 QQ 圆角聊天气泡（入站白色/次级背景，出站 #0099FF 蓝底白字，支持引用块与内联图片）
                    Group {
                        if let img = inlineUIImage(from: parsed.body) {
                            VStack(alignment: .leading, spacing: 4) {
                                Image(uiImage: img)
                                    .resizable()
                                    .scaledToFit()
                                    .frame(maxWidth: 210, maxHeight: 150)
                                    .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
                                Text("🖼️ 图片缩略图")
                                    .font(.caption2)
                                    .foregroundStyle(bubbleForeground.opacity(0.85))
                            }
                            .padding(8)
                            .background(
                                bubbleBackground,
                                in: RoundedRectangle(cornerRadius: 18, style: .continuous)
                            )
                        } else {
                            VStack(alignment: .leading, spacing: 6) {
                                if let quote = splitQuoteBody.quote, !quote.isEmpty {
                                    HStack(spacing: 6) {
                                        RoundedRectangle(cornerRadius: 1.5)
                                            .fill(message.inbound ? QQTheme.qqBlue : Color.white.opacity(0.78))
                                            .frame(width: 3, height: 14)
                                        Text(quote)
                                            .font(.caption)
                                            .foregroundStyle(message.inbound ? .secondary : Color.white.opacity(0.90))
                                            .lineLimit(2)
                                    }
                                    .padding(.horizontal, 8)
                                    .padding(.vertical, 5)
                                    .background(
                                        message.inbound
                                            ? Color.primary.opacity(0.05)
                                            : Color.white.opacity(0.16),
                                        in: RoundedRectangle(cornerRadius: 6, style: .continuous)
                                    )
                                }

                                Text(splitQuoteBody.body)
                                    .font(.system(size: 15))
                                    .foregroundStyle(bubbleForeground)
                                    .textSelection(.enabled)

                                if splitQuoteBody.body.contains("```") {
                                    Text(splitQuoteBody.body.replacingOccurrences(of: "```", with: "").trimmingCharacters(in: .whitespacesAndNewlines))
                                        .font(.system(size: 12, design: .monospaced))
                                        .foregroundStyle(Color(red: 0.89, green: 0.91, blue: 0.94))
                                        .padding(8)
                                        .background(
                                            Color(red: 0.12, green: 0.16, blue: 0.23),
                                            in: RoundedRectangle(cornerRadius: 8, style: .continuous)
                                        )
                                        .textSelection(.enabled)
                                }
                            }
                            .padding(.horizontal, 14)
                            .padding(.vertical, 10)
                            .background(
                                bubbleBackground,
                                in: RoundedRectangle(cornerRadius: 18, style: .continuous)
                            )
                        }
                    }
                    .contextMenu {
                        if let onReact {
                            Button(action: onReact) {
                                Label("👍 点赞回应", systemImage: "hand.thumbsup")
                            }
                        }

                        Button {
                            onReply?()
                        } label: {
                            Label("💬 引用回复", systemImage: "arrowshape.turn.up.left")
                        }

                        Button {
                            UIPasteboard.general.string = splitQuoteBody.body
                        } label: {
                            Label("📋 复制文本", systemImage: "doc.on.doc")
                        }

                        if let onPin {
                            Button(action: onPin) {
                                Label("📌 置顶消息", systemImage: "pin")
                            }
                        }

                        if message.inbound {
                            Button {
                                onMarkRead?()
                            } label: {
                                Label("✓ 标为已读", systemImage: "checkmark.circle")
                            }
                        } else {
                            if let onEdit {
                                Button(action: onEdit) {
                                    Label("✏️ 编辑消息", systemImage: "pencil")
                                }
                            }
                            Button(role: .destructive) {
                                onRevoke?()
                            } label: {
                                Label("↩️ 撤回消息", systemImage: "arrow.uturn.backward")
                            }
                        }
                    }

                    // 气泡下方：仅在存在点赞/表情回应时展示 QQ 胶囊徽章行
                    if !parsed.reactions.isEmpty {
                        HStack(spacing: 5) {
                            ForEach(parsed.reactions, id: \.self) { emoji in
                                Button {
                                    onReact?()
                                } label: {
                                    Text("\(emoji) 1")
                                        .font(.caption2.weight(.semibold))
                                        .foregroundStyle(QQTheme.qqBlue)
                                        .padding(.horizontal, 8)
                                        .padding(.vertical, 2)
                                        .background(QQTheme.qqBlue.opacity(0.14), in: Capsule())
                                }
                                .buttonStyle(.plain)
                            }
                        }
                    }
                }

                if message.inbound {
                    Spacer(minLength: 44)
                } else {
                    avatarView
                }
            }
        }
    }

    private var avatarView: some View {
        Circle()
            .fill(message.inbound ? QQTheme.avatarColor(for: senderAvatarKey) : QQTheme.qqBlue)
            .frame(width: 36, height: 36)
            .overlay(
                Text(QQTheme.initial(for: senderName.isEmpty ? message.peerId : senderName))
                    .font(.system(size: 14, weight: .bold))
                    .foregroundStyle(.white)
            )
    }

    private var bubbleBackground: Color {
        message.inbound ? Color(.secondarySystemGroupedBackground) : QQTheme.qqBlue
    }

    private var bubbleForeground: Color {
        message.inbound ? .primary : .white
    }

    private func inlineUIImage(from raw: String) -> UIImage? {
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard trimmed.hasPrefix("data:image/"),
              let range = trimmed.range(of: ";base64,") else { return nil }
        let b64 = String(trimmed[range.upperBound...]).trimmingCharacters(in: .whitespacesAndNewlines)
        guard let data = Data(base64Encoded: b64) else { return nil }
        return UIImage(data: data)
    }
}
