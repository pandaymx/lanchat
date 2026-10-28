import SwiftUI
import UniformTypeIdentifiers

// MARK: - macOS NTQQ 视觉调色盘与共享头像组件

enum QQPalette {
    static let qqBlue = Color(red: 0.0, green: 0.60, blue: 1.0)       // #0099FF
    static let qqBlueDark = Color(red: 0.0, green: 0.47, blue: 0.90)  // #0077E6
    static let railBackground = Color(red: 0.118, green: 0.133, blue: 0.176) // #1E222D
    static let onlineGreen = Color(red: 0.18, green: 0.80, blue: 0.44) // #2ECC71
    static let busyOrange = Color(red: 0.96, green: 0.65, blue: 0.14)  // #F5A623
    static let unreadRed = Color(red: 0.96, green: 0.26, blue: 0.21)   // #F44336

    private static let avatarGradients: [[Color]] = [
        [Color(red: 0.05, green: 0.68, blue: 1.0), Color(red: 0.0, green: 0.48, blue: 0.95)],
        [Color(red: 0.36, green: 0.42, blue: 0.98), Color(red: 0.54, green: 0.28, blue: 0.95)],
        [Color(red: 0.12, green: 0.78, blue: 0.62), Color(red: 0.05, green: 0.58, blue: 0.50)],
        [Color(red: 1.0, green: 0.56, blue: 0.22), Color(red: 0.94, green: 0.34, blue: 0.26)],
        [Color(red: 0.94, green: 0.36, blue: 0.64), Color(red: 0.76, green: 0.24, blue: 0.72)],
        [Color(red: 0.18, green: 0.62, blue: 0.96), Color(red: 0.14, green: 0.80, blue: 0.86)]
    ]

    static func gradient(for key: String) -> LinearGradient {
        if key == "*" {
            return LinearGradient(
                colors: [Color(red: 1.0, green: 0.58, blue: 0.15), Color(red: 0.95, green: 0.32, blue: 0.28)],
                startPoint: .topLeading,
                endPoint: .bottomTrailing
            )
        }
        let hash = abs(key.unicodeScalars.reduce(0) { ($0 &* 31) &+ Int($1.value) })
        let pair = avatarGradients[hash % avatarGradients.count]
        return LinearGradient(colors: pair, startPoint: .topLeading, endPoint: .bottomTrailing)
    }

    static func firstChar(of text: String) -> String {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let first = trimmed.first else { return "U" }
        return String(first).uppercased()
    }
}

/// QQ 风格多彩圆形头像（支持首字母、系统图标、右下角在线状态圆点）。
struct QQAvatar: View {
    let key: String
    let title: String
    var size: CGFloat = 38
    var status: String? = nil
    var systemIcon: String? = nil
    var emojiBadge: String? = nil
    var dotBorderColor: Color = Color(nsColor: .windowBackgroundColor)

    var body: some View {
        ZStack(alignment: .bottomTrailing) {
            Circle()
                .fill(QQPalette.gradient(for: key))
                .frame(width: size, height: size)
                .overlay(
                    Group {
                        if let emojiBadge {
                            Text(emojiBadge)
                                .font(.system(size: size * 0.48))
                        } else if let systemIcon {
                            Image(systemName: systemIcon)
                                .font(.system(size: size * 0.44, weight: .semibold))
                                .foregroundColor(.white)
                        } else {
                            Text(QQPalette.firstChar(of: title))
                                .font(.system(size: size * 0.44, weight: .bold, design: .rounded))
                                .foregroundColor(.white)
                        }
                    }
                )

            if let status {
                Circle()
                    .fill(status == "busy" ? QQPalette.busyOrange : QQPalette.onlineGreen)
                    .frame(width: max(9, size * 0.27), height: max(9, size * 0.27))
                    .overlay(
                        Circle()
                            .stroke(dotBorderColor, lineWidth: 2)
                    )
                    .offset(x: 1, y: 1)
            }
        }
    }
}

/// 聊天主区（单聊）：顶部 QQ 会话栏 + 气泡消息流 + 底部多功能工具栏与输入区。
struct ChatView: View {
    @ObservedObject var store: ChatStore
    let peer: Peer
    var showInspector: Binding<Bool>? = nil

    @State private var draft = ""
    @State private var showStickerPopover = false
    @State private var showSearchBar = false
    @State private var searchQuery = ""
    @State private var inVoiceCall = false
    @State private var isScreenSharing = false
    @State private var replyingTo: ChatMessage? = nil

    private var thread: [ChatMessage] {
        let all = store.messages[peer.id] ?? []
        guard showSearchBar else { return all }
        let q = searchQuery.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !q.isEmpty else { return all }
        return all.filter { !$0.revoked && $0.text.lowercased().contains(q) }
    }

    private var isE2EE: Bool {
        store.e2eeEnabledThreads.contains(peer.id)
    }

    private var activeTransfersCount: Int {
        store.transfers.filter { $0.state == "active" || $0.state == "pending" }.count
    }

    private var pinnedMessage: ChatMessage? {
        (store.messages[peer.id] ?? []).last { msg in
            !msg.revoked && (msg.pinned || msg.text.hasPrefix("📌 "))
        }
    }

    var body: some View {
        VStack(spacing: 0) {
            chatHeader
            Divider()

            if let pinned = pinnedMessage {
                HStack(spacing: 8) {
                    Text("📌 [置顶] \(Formatters.splitQuote(Formatters.splitReactions(pinned.text).body).body)")
                        .font(.system(size: 12, weight: .semibold))
                        .foregroundColor(.orange)
                        .lineLimit(1)
                    Spacer()
                }
                .padding(.horizontal, 16)
                .padding(.vertical, 6)
                .background(Color.orange.opacity(0.10))
                Divider()
            }

            if inVoiceCall || ["ringing", "calling", "active", "in_call"].contains(store.voiceCall?.state ?? "") {
                let isRinging = store.voiceCall?.state == "ringing"
                let callerId = store.voiceCall?.peerId ?? peer.id
                let callerTitle = store.peerNickname(for: callerId)
                HStack(spacing: 10) {
                    QQAvatar(key: callerId, title: callerTitle, size: 26, status: "online")
                    Text(isRinging
                         ? "🔔 [来电响铃] \(callerTitle) 邀请你进行实时低延迟语音通话"
                         : "📞 实时低延迟 P2P 语音通话进行中 · \(callerTitle)")
                        .font(.system(size: 12, weight: .semibold))
                        .foregroundColor(QQPalette.onlineGreen)
                    Spacer()
                    if isRinging {
                        Button {
                            inVoiceCall = true
                            Task { await store.acceptVoiceCall(to: callerId) }
                        } label: {
                            Text("🟢 接听")
                                .font(.system(size: 11, weight: .semibold))
                                .foregroundColor(.white)
                                .padding(.horizontal, 10)
                                .padding(.vertical, 3)
                                .background(Capsule().fill(QQPalette.onlineGreen))
                        }
                        .buttonStyle(.plain)
                    }
                    Button {
                        inVoiceCall = false
                        Task { await store.stopVoiceCall(to: callerId) }
                    } label: {
                        Text("🔴 挂断")
                            .font(.system(size: 11, weight: .semibold))
                            .foregroundColor(.white)
                            .padding(.horizontal, 10)
                            .padding(.vertical, 3)
                            .background(Capsule().fill(Color.red.opacity(0.85)))
                    }
                    .buttonStyle(.plain)
                }
                .padding(.horizontal, 16)
                .padding(.vertical, 6)
                .background(QQPalette.onlineGreen.opacity(0.10))
                Divider()
            }

            if let frameStr = store.latestScreenFrame, !frameStr.isEmpty {
                VStack(spacing: 8) {
                    HStack {
                        Text("🖥️ 实时屏幕共享舞台 · 点击画面发送激光笔红点批注")
                            .font(.system(size: 12, weight: .semibold))
                            .foregroundColor(.white)
                        Spacer()
                        Button {
                            Task { await store.sendScreenAnnotation(to: peer.id, normX: 0.5, normY: 0.5, note: "重点关注此处") }
                        } label: {
                            Text("📍 激光批注")
                                .font(.system(size: 11, weight: .semibold))
                                .foregroundColor(.white)
                                .padding(.horizontal, 8)
                                .padding(.vertical, 3)
                                .background(Capsule().fill(QQPalette.qqBlue))
                        }
                        .buttonStyle(.plain)
                    }
                    if let nsImg = decodeDataURIImage(frameStr) {
                        Image(nsImage: nsImg)
                            .resizable()
                            .scaledToFit()
                            .frame(maxHeight: 190)
                            .cornerRadius(8)
                            .onTapGesture {
                                Task { await store.sendScreenAnnotation(to: peer.id, normX: 0.5, normY: 0.5, note: "🔴 激光笔点击批注") }
                            }
                    }
                }
                .padding(.horizontal, 16)
                .padding(.vertical, 8)
                .background(Color.black.opacity(0.85))
                Divider()
            }

            if showSearchBar {
                searchHeaderBar
                Divider()
            }

            messageStream

            Divider()
            composerArea
        }
        .background(Color(nsColor: .windowBackgroundColor))
        .frame(minWidth: 460, minHeight: 420)
        .onAppear {
            store.markThreadActive(peer.id)
        }
        .onChange(of: peer.id) { newId in
            store.markThreadActive(newId)
            replyingTo = nil
            searchQuery = ""
        }
        .onDrop(of: [UTType.fileURL], isTargeted: nil) { providers in
            guard !providers.isEmpty else { return false }
            for provider in providers {
                _ = provider.loadObject(ofClass: URL.self) { url, _ in
                    guard let fileURL = url, fileURL.isFileURL else { return }
                    Task { @MainActor in
                        await store.offerFile(to: peer.id, path: fileURL.path)
                    }
                }
            }
            return true
        }
    }

    // MARK: - 顶部 QQ 聊天栏

    private var chatHeader: some View {
        HStack(spacing: 12) {
            QQAvatar(
                key: peer.id,
                title: peer.nickname,
                size: 38,
                status: peer.status.isEmpty ? "online" : peer.status
            )

            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    Text(peer.nickname)
                        .font(.system(size: 15, weight: .bold))
                        .foregroundColor(.primary)

                    if !peer.os.isEmpty {
                        Text(peer.os)
                            .font(.system(size: 10.5, weight: .medium))
                            .foregroundColor(.secondary)
                            .padding(.horizontal, 6)
                            .padding(.vertical, 1.5)
                            .background(
                                Capsule()
                                    .fill(Color.primary.opacity(0.06))
                            )
                    }

                    if isE2EE {
                        Text("🔐 E2EE 加密")
                            .font(.system(size: 10.5, weight: .semibold))
                            .foregroundColor(QQPalette.onlineGreen)
                            .padding(.horizontal, 6)
                            .padding(.vertical, 1.5)
                            .background(
                                Capsule()
                                    .fill(QQPalette.onlineGreen.opacity(0.14))
                            )
                    }
                }

                HStack(spacing: 5) {
                    Circle()
                        .fill(peer.status == "busy" ? QQPalette.busyOrange : QQPalette.onlineGreen)
                        .frame(width: 6.5, height: 6.5)
                    let linkBadge = (ServerInfo.isTailscaleAddress(store.server) || ServerInfo.isTailscaleAddress(peer.status))
                        ? "🌐 Tailscale 直连"
                        : "⚡ 局域网直连"
                    Text("\(peer.status.isEmpty ? "在线" : peer.status) · \(linkBadge)")
                        .font(.system(size: 11.5))
                        .foregroundColor(.secondary)
                }
            }

            Spacer()

            // 顶部快捷操作按钮组
            HStack(spacing: 8) {
                Button {
                    if inVoiceCall {
                        inVoiceCall = false
                        Task { await store.stopVoiceCall(to: peer.id) }
                    } else {
                        inVoiceCall = true
                        Task { await store.startVoiceCall(to: peer.id) }
                    }
                } label: {
                    HStack(spacing: 4) {
                        Text(inVoiceCall ? "📵 挂断语音" : "📞 语音通话")
                            .font(.system(size: 12, weight: .medium))
                    }
                    .padding(.horizontal, 10)
                    .padding(.vertical, 5)
                    .background(
                        RoundedRectangle(cornerRadius: 7, style: .continuous)
                            .fill(inVoiceCall ? Color.red.opacity(0.14) : Color.primary.opacity(0.05))
                    )
                }
                .buttonStyle(.plain)
                .help("发起或挂断局域网实时语音通话 (StartVoiceCall / StopVoiceCall)")

                Button {
                    if isScreenSharing {
                        isScreenSharing = false
                        Task { await store.stopScreenShare(to: peer.id) }
                    } else {
                        isScreenSharing = true
                        Task { await store.startScreenShare(to: peer.id, fps: 15) }
                    }
                } label: {
                    HStack(spacing: 4) {
                        Text(isScreenSharing ? "⏹ 停止投屏" : "🖥️ 屏幕共享")
                            .font(.system(size: 12, weight: .medium))
                    }
                    .padding(.horizontal, 10)
                    .padding(.vertical, 5)
                    .background(
                        RoundedRectangle(cornerRadius: 7, style: .continuous)
                            .fill(isScreenSharing ? Color.orange.opacity(0.16) : Color.primary.opacity(0.05))
                    )
                }
                .buttonStyle(.plain)
                .help("开启或停止局域网 P2P 屏幕共享 (StartScreenShare / StopScreenShare)")

                Button {
                    Task { await store.syncHistoryWithPeerRpc(to: peer.id) }
                } label: {
                    HStack(spacing: 4) {
                        Text("🔄 漫游同步")
                            .font(.system(size: 12, weight: .medium))
                    }
                    .padding(.horizontal, 10)
                    .padding(.vertical, 5)
                    .background(
                        RoundedRectangle(cornerRadius: 7, style: .continuous)
                            .fill(Color.primary.opacity(0.05))
                    )
                }
                .buttonStyle(.plain)
                .help("向对端请求离线缺失消息增量漫游同步 (SyncHistoryWithPeer)")

                Button {
                    pickAndOffer()
                } label: {
                    HStack(spacing: 4) {
                        Text("📁 发送文件")
                            .font(.system(size: 12, weight: .medium))
                    }
                    .padding(.horizontal, 10)
                    .padding(.vertical, 5)
                    .background(
                        RoundedRectangle(cornerRadius: 7, style: .continuous)
                            .fill(Color.primary.opacity(0.05))
                    )
                }
                .buttonStyle(.plain)
                .help("选择文件或文件夹并通过 P2P 发送")

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
                    .help("展开/收起右侧实时 P2P 传输抽屉")
                }
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 11)
        .background(Color(nsColor: .controlBackgroundColor).opacity(0.6))
    }

    // MARK: - 聊天记录搜索条

    private var searchHeaderBar: some View {
        HStack(spacing: 8) {
            Image(systemName: "magnifyingglass")
                .foregroundColor(.secondary)
            TextField("过滤当前会话消息，或按回车执行 /search…", text: $searchQuery)
                .textFieldStyle(.plain)
                .font(.system(size: 12.5))
                .onSubmit {
                    let q = searchQuery.trimmingCharacters(in: .whitespacesAndNewlines)
                    guard !q.isEmpty else { return }
                    Task { await store.sendText(to: peer.id, text: "/search \(q)") }
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

    // MARK: - 中部 QQ 气泡消息流

    private var messageStream: some View {
        ScrollViewReader { proxy in
            ScrollView {
                if thread.isEmpty {
                    VStack(spacing: 10) {
                        Spacer(minLength: 80)
                        QQAvatar(key: peer.id, title: peer.nickname, size: 54)
                            .opacity(0.85)
                        Text(showSearchBar && !searchQuery.isEmpty ? "没有找到匹配的聊天记录" : "与 \(peer.nickname) 开始局域网私密对话")
                            .font(.system(size: 13.5, weight: .medium))
                            .foregroundColor(.secondary)
                        Text("支持 P2P 高速文件直连、贴纸表情、消息引用回复与撤回")
                            .font(.system(size: 11.5))
                            .foregroundColor(.secondary.opacity(0.75))
                        Spacer(minLength: 80)
                    }
                    .frame(maxWidth: .infinity)
                    .padding()
                } else {
                    LazyVStack(alignment: .leading, spacing: 14) {
                        HStack {
                            Spacer()
                            Button {
                                let beforeTS = Int64(thread.first?.timestamp.timeIntervalSince1970 ?? 0)
                                Task { _ = await store.getHistoryPage(convID: peer.id, beforeTS: beforeTS, limit: 30) }
                            } label: {
                                Text("📜 加载更早历史消息")
                                    .font(.system(size: 11.5))
                                    .foregroundColor(.secondary)
                                    .padding(.horizontal, 12)
                                    .padding(.vertical, 3)
                            }
                            .buttonStyle(.plain)
                            Spacer()
                        }
                        ForEach(thread) { msg in
                            BubbleRow(
                                message: msg,
                                isSelf: !msg.inbound,
                                senderName: msg.inbound ? peer.nickname : store.peerNickname(for: store.selfId),
                                senderKey: msg.inbound ? peer.id : (store.selfId.isEmpty ? "self" : store.selfId),
                                onReply: { target in
                                    replyingTo = target
                                },
                                onEdit: { target in
                                    promptMacEditMessage(initialText: target.text) { newText in
                                        Task { await store.editMessageRpc(to: peer.id, msgID: target.id, newText: newText) }
                                    }
                                },
                                onRevoke: { target in
                                    Task { await store.revokeMessageRpc(to: peer.id, msgID: target.id) }
                                },
                                onMarkRead: { target in
                                    Task { await store.markMessageReadRpc(to: peer.id, msgID: target.id) }
                                },
                                onReact: { target in
                                    Task { await store.reactMessageRpc(to: peer.id, msgID: target.id, emoji: "👍") }
                                },
                                onPin: { target in
                                    Task { await store.pinMessageRpc(to: peer.id, msgID: target.id) }
                                },
                                onReEdit: { original in
                                    let clean = Formatters.extractEditableOriginal(original)
                                    if !clean.isEmpty {
                                        draft = clean
                                    }
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

    // MARK: - 底部 QQ 编辑器与工具栏

    private var composerArea: some View {
        VStack(spacing: 0) {
            if let replyingTo {
                let cleanReply = Formatters.splitQuote(Formatters.splitReactions(replyingTo.text).body).body
                HStack(spacing: 8) {
                    RoundedRectangle(cornerRadius: 2)
                        .fill(QQPalette.qqBlue)
                        .frame(width: 3, height: 22)
                    Text("回复：\(cleanReply)")
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

            // 工具栏：😊 表情贴纸、📁 发送文件、🔐 端到端加密 (/e2ee)、🔍 搜索记录
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
                .help("发送贴纸或插入表情")
                .popover(isPresented: $showStickerPopover, arrowEdge: .top) {
                    StickerPickerPopover(
                        onSendSticker: { stickerPath in
                            showStickerPopover = false
                            Task { await store.sendSticker(to: peer.id, path: stickerPath) }
                        },
                        onInsertEmoji: { emoji in
                            draft += emoji
                            showStickerPopover = false
                        }
                    )
                }

                Button {
                    Task { await store.syncHistoryWithPeerRpc(to: peer.id) }
                } label: {
                    HStack(spacing: 4) {
                        Text("🔄 漫游同步")
                            .font(.system(size: 12, weight: .medium))
                    }
                    .padding(.horizontal, 10)
                    .padding(.vertical, 5)
                    .background(
                        RoundedRectangle(cornerRadius: 7, style: .continuous)
                            .fill(Color.primary.opacity(0.05))
                    )
                }
                .buttonStyle(.plain)
                .help("向对端请求离线缺失消息增量漫游同步 (SyncHistoryWithPeer)")

                Button {
                    pickAndOffer()
                } label: {
                    Text("📁 发送文件/目录")
                        .font(.system(size: 12, weight: .medium))
                        .foregroundColor(.primary.opacity(0.85))
                        .padding(.horizontal, 8)
                        .padding(.vertical, 4)
                }
                .buttonStyle(.plain)
                .help("发送文件或文件夹（目录自动流式打包为 .lanchat-dir.tar.gz 并在接收端自动解包还原）")

                Button {
                    pasteOrSyncClipboard()
                } label: {
                    Text("📋 剪贴板")
                        .font(.system(size: 12, weight: .medium))
                        .foregroundColor(.primary.opacity(0.85))
                        .padding(.horizontal, 8)
                        .padding(.vertical, 4)
                }
                .buttonStyle(.plain)
                .help("一键直发剪贴板截图/文件，或通过 /clip 同步剪贴板文本")

                Button {
                    Task { await store.sendText(to: peer.id, text: "/voice 3") }
                } label: {
                    Text("🎤 语音")
                        .font(.system(size: 12, weight: .medium))
                        .foregroundColor(.primary.opacity(0.85))
                        .padding(.horizontal, 8)
                        .padding(.vertical, 4)
                }
                .buttonStyle(.plain)
                .help("发送语音消息 (/voice)")

                Button {
                    Task { await store.toggleSessionE2EE(threadKey: peer.id) }
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
                .help("切换端到端加密会话状态")

                Button {
                    Task { await store.verifyPeerFingerprintRpc(peerID: peer.id) }
                } label: {
                    Text("🛡️ 安全码")
                        .font(.system(size: 12, weight: .medium))
                        .foregroundColor(.primary.opacity(0.85))
                        .padding(.horizontal, 8)
                        .padding(.vertical, 4)
                }
                .buttonStyle(.plain)
                .help("核验 X25519 端到端安全码指纹 (VerifyPeerFingerprint)")

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
                .help("搜索当前会话聊天记录")

                Spacer()
            }
            .padding(.horizontal, 10)
            .padding(.top, 8)
            .padding(.bottom, 4)

            // 多行文本输入区
            ZStack(alignment: .topLeading) {
                TextEditor(text: $draft)
                    .font(.system(size: 13.5))
                    .frame(minHeight: 58, maxHeight: 110)
                    .scrollContentBackground(.hidden)
                    .padding(.horizontal, 10)
                    .padding(.vertical, 4)

                if draft.isEmpty {
                    Text(replyingTo != nil ? "输入回复内容…" : "输入消息，支持 /reply、/revoke、/e2ee、/search…")
                        .font(.system(size: 13))
                        .foregroundColor(.secondary.opacity(0.6))
                        .padding(.horizontal, 15)
                        .padding(.vertical, 8)
                        .allowsHitTesting(false)
                }
            }

            // 右下角 QQ Blue 发送按钮
            HStack {
                Text("支持拖拽或点击「📁 发送文件」进行 P2P 直连传输")
                    .font(.system(size: 11))
                    .foregroundColor(.secondary.opacity(0.75))
                Spacer()
                Button {
                    send()
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

    private func send() {
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
        Task { await store.sendText(to: peer.id, text: payload) }
    }

    private func pickAndOffer() {
        let panel = NSOpenPanel()
        panel.allowsMultipleSelection = false
        panel.canChooseDirectories = true
        panel.canChooseFiles = true
        guard panel.runModal() == .OK, let url = panel.url else { return }
        Task { await store.offerFile(to: peer.id, path: url.path) }
    }

    private func pasteOrSyncClipboard() {
        let pb = NSPasteboard.general
        if let urls = pb.readObjects(forClasses: [NSURL.self], options: [.urlReadingFileURLsOnly: true]) as? [URL],
           let first = urls.first {
            Task { await store.offerFile(to: peer.id, path: first.path) }
            return
        }
        if let pngData = pb.data(forType: .png), !pngData.isEmpty {
            let uri = "data:image/png;base64," + pngData.base64EncodedString()
            Task { await store.offerFile(to: peer.id, path: uri) }
            return
        }
        if let text = pb.string(forType: .string)?.trimmingCharacters(in: .whitespacesAndNewlines), !text.isEmpty {
            Task { await store.sendText(to: peer.id, text: "/clip \(text)") }
        }
    }
}

// MARK: - QQ 气泡行（居中撤回灰条+重新编辑 / 36x36 左右圆形头像 + 昵称时间 + 气泡内引用块 + 右键菜单 + 点赞胶囊）

struct BubbleRow: View {
    let message: ChatMessage
    let isSelf: Bool
    var senderName: String? = nil
    var senderKey: String? = nil
    var onReply: ((ChatMessage) -> Void)? = nil
    var onEdit: ((ChatMessage) -> Void)? = nil
    var onRevoke: ((ChatMessage) -> Void)? = nil
    var onMarkRead: ((ChatMessage) -> Void)? = nil
    var onReact: ((ChatMessage) -> Void)? = nil
    var onPin: ((ChatMessage) -> Void)? = nil
    var onReEdit: ((String) -> Void)? = nil

    private var resolvedName: String {
        if let senderName, !senderName.isEmpty {
            return senderName
        }
        if let msgSender = message.senderName, !msgSender.isEmpty {
            return msgSender
        }
        return isSelf ? "我" : String(message.peerId.prefix(8))
    }

    private var resolvedAvatarKey: String {
        if let senderKey, !senderKey.isEmpty {
            return senderKey
        }
        return isSelf ? "self" : message.peerId
    }

    private var parsed: (body: String, reactions: [String]) {
        Formatters.splitReactions(message.text)
    }

    private var splitQuoteBody: (quote: String?, body: String) {
        Formatters.splitQuote(parsed.body)
    }

    var body: some View {
        if message.revoked {
            // 桌面 NTQQ 居中灰色系统撤回提示条 + 「重新编辑」蓝色链接
            HStack(spacing: 6) {
                Text(isSelf ? "你撤回了一条消息" : "\"\(resolvedName)\" 撤回了一条消息")
                    .font(.system(size: 12))
                    .foregroundColor(.secondary)

                if isSelf, !message.originalText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty, let onReEdit {
                    Button {
                        onReEdit(message.originalText)
                    } label: {
                        Text("重新编辑")
                            .font(.system(size: 12, weight: .semibold))
                            .foregroundColor(QQPalette.qqBlue)
                    }
                    .buttonStyle(.plain)
                }
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 4)
            .background(
                RoundedRectangle(cornerRadius: 6, style: .continuous)
                    .fill(Color.primary.opacity(0.05))
            )
            .frame(maxWidth: .infinity, alignment: .center)
            .padding(.vertical, 2)
        } else {
            HStack(alignment: .top, spacing: 10) {
                if isSelf {
                    Spacer(minLength: 56)
                } else {
                    QQAvatar(key: resolvedAvatarKey, title: resolvedName, size: 36)
                }

                VStack(alignment: isSelf ? .trailing : .leading, spacing: 4) {
                    // 顶部发送者名称、时间戳与已读标识
                    HStack(spacing: 6) {
                        if !isSelf {
                            Text(resolvedName)
                                .font(.system(size: 11.5, weight: .semibold))
                                .foregroundColor(.secondary)
                        }
                        if isSelf && !message.readBy.isEmpty {
                            Text(message.readBy.count > 1 ? "✓ 已读(\(message.readBy.count))" : "✓ 已读")
                                .font(.system(size: 10.5, weight: .semibold))
                                .foregroundColor(QQPalette.onlineGreen)
                        }
                        Text(Formatters.time(message.timestamp))
                            .font(.system(size: 10.5))
                            .foregroundColor(.secondary.opacity(0.75))
                    }

                    // 气泡主体（含内嵌引用卡片与右键上下文菜单）
                    bubbleContent
                        .contextMenu {
                            if let onReact {
                                Button("👍 点赞回应") {
                                    onReact(message)
                                }
                            }
                            if let onReply {
                                Button("💬 引用回复") {
                                    onReply(message)
                                }
                            }
                            Button("📋 复制文本") {
                                NSPasteboard.general.clearContents()
                                NSPasteboard.general.setString(splitQuoteBody.body, forType: .string)
                            }
                            if let onPin {
                                Button("📌 置顶消息") {
                                    onPin(message)
                                }
                            }
                            Divider()
                            if isSelf, let onEdit {
                                Button("✏️ 编辑消息") {
                                    onEdit(message)
                                }
                            }
                            if isSelf, let onRevoke {
                                Button("↩️ 撤回消息") {
                                    onRevoke(message)
                                }
                            } else if !isSelf, let onMarkRead {
                                Button("✓ 标为已读") {
                                    onMarkRead(message)
                                }
                            }
                        }

                    // 气泡下方：仅在存在点赞/表情回应时展示 QQ 胶囊徽章行
                    if !parsed.reactions.isEmpty {
                        HStack(spacing: 5) {
                            ForEach(parsed.reactions, id: \.self) { emoji in
                                Button {
                                    onReact?(message)
                                } label: {
                                    Text("\(emoji) 1")
                                        .font(.system(size: 11, weight: .semibold))
                                        .foregroundColor(QQPalette.qqBlue)
                                        .padding(.horizontal, 8)
                                        .padding(.vertical, 2)
                                        .background(
                                            Capsule()
                                                .fill(QQPalette.qqBlue.opacity(0.14))
                                        )
                                        .overlay(
                                            Capsule()
                                                .stroke(QQPalette.qqBlue.opacity(0.28), lineWidth: 0.8)
                                        )
                                }
                                .buttonStyle(.plain)
                            }
                        }
                    }
                }

                if isSelf {
                    QQAvatar(key: resolvedAvatarKey, title: resolvedName, size: 36)
                } else {
                    Spacer(minLength: 56)
                }
            }
        }
    }

    private func inlineNSImage(from raw: String) -> NSImage? {
        decodeDataURIImage(raw)
    }

    @ViewBuilder
    private var bubbleContent: some View {
        if let img = inlineNSImage(from: parsed.body) {
            VStack(alignment: .leading, spacing: 4) {
                Image(nsImage: img)
                    .resizable()
                    .scaledToFit()
                    .frame(maxWidth: 220, maxHeight: 160)
                    .clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
                Text("🖼️ 图片缩略图")
                    .font(.system(size: 11))
                    .foregroundColor(isSelf ? .white.opacity(0.85) : .secondary)
            }
            .padding(8)
            .background(
                RoundedRectangle(cornerRadius: 13, style: .continuous)
                    .fill(
                        isSelf
                            ? LinearGradient(
                                colors: [QQPalette.qqBlue, QQPalette.qqBlueDark],
                                startPoint: .topLeading,
                                endPoint: .bottomTrailing
                            )
                            : LinearGradient(
                                colors: [Color(nsColor: .controlBackgroundColor), Color(nsColor: .controlBackgroundColor)],
                                startPoint: .top,
                                endPoint: .bottom
                            )
                    )
            )
        } else {
            VStack(alignment: .leading, spacing: 6) {
                if let quote = splitQuoteBody.quote, !quote.isEmpty {
                    HStack(spacing: 6) {
                        RoundedRectangle(cornerRadius: 1.5)
                            .fill(isSelf ? Color.white.opacity(0.75) : QQPalette.qqBlue)
                            .frame(width: 3, height: 14)
                        Text(quote)
                            .font(.system(size: 11.5))
                            .foregroundColor(isSelf ? Color.white.opacity(0.90) : .secondary)
                            .lineLimit(2)
                    }
                    .padding(.horizontal, 8)
                    .padding(.vertical, 5)
                    .background(
                        RoundedRectangle(cornerRadius: 6, style: .continuous)
                            .fill(isSelf ? Color.white.opacity(0.16) : Color.primary.opacity(0.05))
                    )
                }

                Text(splitQuoteBody.body)
                    .font(.system(size: 13.5))
                    .foregroundColor(isSelf ? .white : .primary)
                    .textSelection(.enabled)

                if splitQuoteBody.body.contains("```") {
                    Text(splitQuoteBody.body.replacingOccurrences(of: "```", with: "").trimmingCharacters(in: .whitespacesAndNewlines))
                        .font(.system(size: 12, design: .monospaced))
                        .foregroundColor(Color(red: 0.89, green: 0.91, blue: 0.94))
                        .padding(8)
                        .background(
                            RoundedRectangle(cornerRadius: 6, style: .continuous)
                                .fill(Color(red: 0.12, green: 0.16, blue: 0.23))
                        )
                        .textSelection(.enabled)
                }
            }
            .padding(.horizontal, 13)
            .padding(.vertical, 9)
            .background(
                RoundedRectangle(cornerRadius: 13, style: .continuous)
                    .fill(
                        isSelf
                            ? LinearGradient(
                                colors: [QQPalette.qqBlue, QQPalette.qqBlueDark],
                                startPoint: .topLeading,
                                endPoint: .bottomTrailing
                            )
                            : LinearGradient(
                                colors: [Color(nsColor: .controlBackgroundColor), Color(nsColor: .controlBackgroundColor)],
                                startPoint: .top,
                                endPoint: .bottom
                            )
                    )
                    .shadow(color: Color.black.opacity(isSelf ? 0.08 : 0.04), radius: 3, x: 0, y: 1)
            )
            .overlay(
                RoundedRectangle(cornerRadius: 13, style: .continuous)
                    .stroke(isSelf ? Color.clear : Color.primary.opacity(0.08), lineWidth: 0.8)
            )
        }
    }
}

// MARK: - 表情与贴纸选择器弹窗 (StickerPickerPopover)

struct StickerPickerPopover: View {
    let onSendSticker: (String) -> Void
    let onInsertEmoji: (String) -> Void

    private let stickers: [(icon: String, label: String, code: String)] = [
        ("🎉", "庆祝", "🎉 庆祝"),
        ("👍", "点赞", "👍 点赞"),
        ("🔥", "火热", "🔥 火热"),
        ("❤️", "比心", "❤️ 比心"),
        ("🚀", "秒传", "🚀 秒传"),
        ("✨", "闪耀", "✨ 闪耀"),
        ("🤝", "收到", "🤝 收到"),
        ("☕", "摸鱼", "☕ 摸鱼"),
        ("👏", "鼓掌", "👏 鼓掌"),
        ("💡", "灵感", "💡 灵感"),
        ("🎯", "搞定", "🎯 搞定"),
        ("🛡️", "加密", "🛡️ 加密")
    ]

    private let emojis: [String] = [
        "😀", "😂", "🥳", "😎", "🤔", "🙌",
        "💪", "🙏", "👀", "💯", "🌟", "🍀"
    ]

    private let columns = Array(repeating: GridItem(.flexible(), spacing: 8), count: 4)
    private let emojiColumns = Array(repeating: GridItem(.flexible(), spacing: 6), count: 6)

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("点击表情插入到输入框")
                .font(.system(size: 12, weight: .semibold))
                .foregroundColor(.secondary)

            LazyVGrid(columns: columns, spacing: 8) {
                ForEach(stickers, id: \.code) { item in
                    Button {
                        onInsertEmoji(item.icon)
                    } label: {
                        VStack(spacing: 3) {
                            Text(item.icon)
                                .font(.system(size: 22))
                            Text(item.label)
                                .font(.system(size: 10.5, weight: .medium))
                                .foregroundColor(.primary)
                        }
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 6)
                        .background(
                            RoundedRectangle(cornerRadius: 8, style: .continuous)
                                .fill(Color.primary.opacity(0.04))
                        )
                    }
                    .buttonStyle(.plain)
                }
            }

            Divider()

            Text("更多常用 Emoji")
                .font(.system(size: 11.5, weight: .semibold))
                .foregroundColor(.secondary)

            LazyVGrid(columns: emojiColumns, spacing: 6) {
                ForEach(emojis, id: \.self) { emoji in
                    Button {
                        onInsertEmoji(emoji)
                    } label: {
                        Text(emoji)
                            .font(.system(size: 18))
                            .frame(width: 32, height: 32)
                            .background(
                                RoundedRectangle(cornerRadius: 6, style: .continuous)
                                    .fill(Color.primary.opacity(0.03))
                            )
                    }
                    .buttonStyle(.plain)
                }
            }

            Divider()

            Button {
                let panel = NSOpenPanel()
                panel.canChooseFiles = true
                panel.canChooseDirectories = false
                panel.allowsMultipleSelection = false
                if panel.runModal() == .OK, let url = panel.url {
                    onSendSticker(url.path)
                }
            } label: {
                Text("🖼️ 选择本地贴纸图片发送…")
                    .font(.system(size: 12, weight: .semibold))
                    .foregroundColor(.white)
                    .frame(maxWidth: .infinity)
                    .padding(.vertical, 6)
                    .background(
                        RoundedRectangle(cornerRadius: 7, style: .continuous)
                            .fill(QQPalette.qqBlue)
                    )
            }
            .buttonStyle(.plain)
        }
        .padding(14)
        .frame(width: 280)
    }
}

func decodeDataURIImage(_ raw: String) -> NSImage? {
    let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
    let clean = trimmed.hasPrefix("[表情] ") ? String(trimmed.dropFirst(5)) : trimmed
    guard clean.hasPrefix("data:image/"),
          let range = clean.range(of: ";base64,") else { return nil }
    let b64 = String(clean[range.upperBound...]).trimmingCharacters(in: .whitespacesAndNewlines)
    guard let data = Data(base64Encoded: b64) else { return nil }
    return NSImage(data: data)
}

func promptMacEditMessage(initialText: String, onSave: @escaping (String) -> Void) {
    let alert = NSAlert()
    alert.messageText = "✏️ 二次编辑消息"
    alert.informativeText = "修改后将实时同步给对端或群组成员："
    alert.addButton(withTitle: "保存修改")
    alert.addButton(withTitle: "取消")
    let inputField = NSTextField(frame: NSRect(x: 0, y: 0, width: 320, height: 24))
    let clean = Formatters.splitQuote(Formatters.splitReactions(initialText).body).body
    inputField.stringValue = clean
    alert.accessoryView = inputField
    if alert.runModal() == .alertFirstButtonReturn {
        let trimmed = inputField.stringValue.trimmingCharacters(in: .whitespacesAndNewlines)
        if !trimmed.isEmpty {
            onSave(trimmed)
        }
    }
}
