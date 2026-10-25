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
    @State private var replyingTo: ChatMessage?

    private var thread: [ChatMessage] {
        let all = store.messagesByPeer[peer.id] ?? []
        let q = searchQuery.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard showSearchBar, !q.isEmpty else { return all }
        return all.filter { !$0.revoked && $0.text.lowercased().contains(q) }
    }

    var body: some View {
        VStack(spacing: 0) {
            // 顶部状态副横幅（OS / 在线状态 / E2EE 指示）
            chatStatusSubheader

            // 聊天内搜索条（点击底部「🔍 搜索」展开）
            if showSearchBar {
                chatSearchHeader
            }

            // 手机 QQ 聊天气泡流
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(spacing: 12) {
                        ForEach(thread) { msg in
                            BubbleRow(
                                message: msg,
                                senderName: msg.inbound ? peer.nickname : settings.nickname,
                                senderAvatarKey: msg.inbound ? peer.id : store.selfId,
                                onReply: { replyingTo = msg },
                                onRevoke: {
                                    store.sendText(to: peer.id, text: "/revoke \(msg.id)")
                                },
                                onMarkRead: {
                                    store.sendText(to: peer.id, text: "/read \(msg.id)")
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
    }

    // MARK: - 顶部会话状态条

    private var chatStatusSubheader: some View {
        HStack(spacing: 8) {
            Circle()
                .fill(peer.status == "away" ? QQTheme.awayOrange : QQTheme.onlineGreen)
                .frame(width: 8, height: 8)
            Text("\(peer.os.uppercased()) · \(peer.status == "away" ? "离开" : "在线")")
                .font(.caption2.weight(.medium))
                .foregroundStyle(.secondary)

            Spacer()

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
        HStack(spacing: 8) {
            Rectangle()
                .fill(QQTheme.qqBlue)
                .frame(width: 3, height: 28)
                .clipShape(Capsule())

            VStack(alignment: .leading, spacing: 2) {
                Text("回复 \(msg.inbound ? peer.nickname : settings.nickname)")
                    .font(.caption2.weight(.bold))
                    .foregroundStyle(QQTheme.qqBlue)
                Text(msg.text)
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
                        let path = QQTheme.materializeStickerFile(item: item)
                        store.sendSticker(to: peer.id, path: path)
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
                    store.sendText(to: peer.id, text: e2eeEnabled ? "/e2ee on" : "/e2ee off")
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
}

/// 手机 QQ 单条消息行：36x36 圆形头像（左入站 / 右出站）+ 昵称与时间 + 圆角气泡 + ✓ 已读 + 回复/撤回菜单。
struct BubbleRow: View {
    let message: ChatMessage
    var senderName: String = ""
    var senderAvatarKey: String = ""
    var onReply: (() -> Void)? = nil
    var onRevoke: (() -> Void)? = nil
    var onMarkRead: (() -> Void)? = nil

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            if message.inbound {
                avatarView
            } else {
                Spacer(minLength: 44)
            }

            VStack(alignment: message.inbound ? .leading : .trailing, spacing: 4) {
                // 发送者昵称 + 时间戳
                HStack(spacing: 6) {
                    if !senderName.isEmpty {
                        Text(senderName)
                            .font(.caption2.weight(.medium))
                            .foregroundStyle(.secondary)
                    }
                    Text(QQTheme.formatTime(message.timestamp))
                        .font(.caption2)
                        .foregroundStyle(.tertiary)
                }

                // 手机 QQ 圆角聊天气泡（入站白色/次级背景，出站 #0099FF 蓝底白字，支持 M17 内联图片缩略图）
                Group {
                    if !message.revoked, let img = inlineUIImage(from: message.text) {
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
                        Text(message.text)
                            .font(.system(size: 15))
                            .italic(message.revoked)
                            .padding(.horizontal, 14)
                            .padding(.vertical, 10)
                            .background(
                                bubbleBackground,
                                in: RoundedRectangle(cornerRadius: 18, style: .continuous)
                            )
                            .foregroundStyle(bubbleForeground)
                            .textSelection(.enabled)
                    }
                }
                .contextMenu {
                        if !message.revoked {
                            Button {
                                onReply?()
                            } label: {
                                Label("回复", systemImage: "arrowshape.turn.up.left")
                            }

                            if message.inbound {
                                Button {
                                    onMarkRead?()
                                } label: {
                                    Label("标为已读", systemImage: "checkmark.circle")
                                }
                            } else {
                                Button(role: .destructive) {
                                    onRevoke?()
                                } label: {
                                    Label("撤回", systemImage: "arrow.uturn.backward")
                                }
                            }
                        }
                    }

                // 底部状态与快捷操作（✓ 已读 / 回复 / 撤回）
                HStack(spacing: 8) {
                    if !message.inbound && !message.revoked && !message.readBy.isEmpty {
                        Text("✓ 已读")
                            .font(.caption2.weight(.semibold))
                            .foregroundStyle(QQTheme.qqBlue)
                    }

                    if !message.revoked {
                        if let onReply {
                            Button("回复", action: onReply)
                                .font(.caption2)
                                .foregroundStyle(.secondary)
                        }
                        if !message.inbound, let onRevoke {
                            Button("撤回", action: onRevoke)
                                .font(.caption2)
                                .foregroundStyle(.secondary)
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
        if message.revoked {
            return Color(.tertiarySystemGroupedBackground)
        }
        return message.inbound ? Color(.secondarySystemGroupedBackground) : QQTheme.qqBlue
    }

    private var bubbleForeground: Color {
        if message.revoked {
            return .secondary
        }
        return message.inbound ? .primary : .white
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
