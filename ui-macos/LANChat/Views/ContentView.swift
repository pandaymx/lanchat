import SwiftUI

/// 侧栏选中项：支持置顶全员广播群 ("*")、单播联系人与自定义频道。
enum SidebarRoute: Hashable {
    case broadcast
    case peer(String)
    case channel(String)
}

/// 左侧 64pt 深色导航栏标签页枚举。
enum QQDockTab: String, Hashable {
    case messages
    case contacts
    case channels
    case transfers
    case settings
}

/// 主窗口：未连接显示 QQ 风格登录页；连接后进入 macOS QQ (NTQQ) 经典多栏布局。
struct ContentView: View {
    @ObservedObject var store: ChatStore
    @ObservedObject var settings: SettingsStore

    @State private var activeTab: QQDockTab = .messages
    @State private var selection: SidebarRoute? = .broadcast
    @State private var showInspector: Bool = false
    @State private var showCreateChannel: Bool = false
    @State private var searchText: String = ""

    var body: some View {
        Group {
            if store.conn == "connected" {
                ntqqMainWorkspace
            } else {
                LoginView(store: store, settings: settings)
            }
        }
        .alert("提示", isPresented: Binding(
            get: { store.error != nil },
            set: { if !$0 { store.error = nil } }
        )) {
            Button("好", role: .cancel) { store.error = nil }
        } message: {
            Text(store.error ?? "")
        }
    }

    // MARK: - macOS NTQQ 四栏工作区布局

    private var ntqqMainWorkspace: some View {
        HStack(spacing: 0) {
            // Column 1: 64pt 宽深色左侧图标导航轨 (#1E222D)
            QQLeftDockRail(
                store: store,
                settings: settings,
                activeTab: $activeTab,
                showInspector: $showInspector
            )

            // Column 2: 260pt 宽会话 / 联系人 / 频道列表栏
            if activeTab == .messages || activeTab == .contacts || activeTab == .channels {
                QQSessionListColumn(
                    store: store,
                    activeTab: activeTab,
                    selection: $selection,
                    searchText: $searchText,
                    showCreateChannel: $showCreateChannel
                )
                .frame(width: 260)

                Divider()
            }

            // Column 3: 主会话 / 传输 / 设置区域
            mainCenterColumn
                .frame(maxWidth: .infinity, maxHeight: .infinity)

            // Column 4: 可折叠右侧实时 P2P 传输抽屉 (Inspector Drawer)
            if showInspector && (activeTab == .messages || activeTab == .contacts || activeTab == .channels) {
                Divider()
                TransfersView(
                    store: store,
                    isInspector: true,
                    onClose: { showInspector = false }
                )
                .frame(width: 295)
            }
        }
        .frame(minWidth: 960, minHeight: 620)
        .sheet(isPresented: $showCreateChannel) {
            CreateChannelSheet(store: store)
        }
    }

    // MARK: - Column 3 主内容区路由

    @ViewBuilder
    private var mainCenterColumn: some View {
        switch activeTab {
        case .transfers:
            TransfersView(store: store)
        case .settings:
            SettingsView(store: store, settings: settings)
        case .messages, .contacts, .channels:
            switch selection {
            case .broadcast:
                BroadcastChatView(
                    store: store,
                    settings: settings,
                    showInspector: $showInspector
                )
            case let .peer(id):
                if let peer = store.peers.first(where: { $0.id == id }) {
                    ChatView(
                        store: store,
                        peer: peer,
                        showInspector: $showInspector
                    )
                } else {
                    detailPlaceholder
                }
            case let .channel(id):
                if let channel = store.channels.first(where: { $0.id == id }) {
                    ChannelChatView(
                        store: store,
                        channel: channel,
                        showInspector: $showInspector
                    )
                } else {
                    detailPlaceholder
                }
            case .none:
                detailPlaceholder
            }
        }
    }

    private var detailPlaceholder: some View {
        VStack(spacing: 14) {
            QQAvatar(key: "*", title: "Q", size: 64, systemIcon: "bubble.left.and.bubble.right.fill")
                .opacity(0.85)
            Text("选择一位联系人或频道开始聊天")
                .font(.system(size: 15, weight: .semibold))
                .foregroundColor(.secondary)
            Button {
                selection = .broadcast
            } label: {
                Text("进入 📢 局域网全员广播群")
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundColor(.white)
                    .padding(.horizontal, 18)
                    .padding(.vertical, 8)
                    .background(
                        RoundedRectangle(cornerRadius: 8, style: .continuous)
                            .fill(QQPalette.qqBlue)
                    )
            }
            .buttonStyle(.plain)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Color(nsColor: .windowBackgroundColor))
    }
}

// MARK: - Column 1: 64pt 宽深色左侧导航轨 (#1E222D)

private struct QQLeftDockRail: View {
    @ObservedObject var store: ChatStore
    @ObservedObject var settings: SettingsStore
    @Binding var activeTab: QQDockTab
    @Binding var showInspector: Bool

    private var activeTransfersCount: Int {
        store.transfers.filter { $0.state == "active" || $0.state == "pending" }.count
    }

    var body: some View {
        VStack(spacing: 14) {
            // 顶部：当前用户 40x40 圆形头像 + 在线绿点
            Button {
                activeTab = .settings
            } label: {
                VStack(spacing: 4) {
                    QQAvatar(
                        key: store.selfId.isEmpty ? settings.nickname : store.selfId,
                        title: settings.nickname,
                        size: 40,
                        status: "online",
                        dotBorderColor: QQPalette.railBackground
                    )
                    Text(settings.nickname)
                        .font(.system(size: 9.5, weight: .medium))
                        .foregroundColor(.white.opacity(0.75))
                        .lineLimit(1)
                        .frame(maxWidth: 54)
                }
            }
            .buttonStyle(.plain)
            .padding(.top, 18)
            .help("当前登录：\(settings.nickname)")

            Divider()
                .background(Color.white.opacity(0.10))
                .padding(.horizontal, 12)

            // 中部垂直图标导航项：💬 消息、👥 联系人、#️⃣ 频道、📁 传输
            VStack(spacing: 10) {
                dockItem(
                    tab: .messages,
                    emoji: "💬",
                    title: "消息",
                    badge: store.totalUnreadCount
                )

                dockItem(
                    tab: .contacts,
                    emoji: "👥",
                    title: "联系人",
                    badge: store.peers.count
                )

                dockItem(
                    tab: .channels,
                    emoji: "#️⃣",
                    title: "频道",
                    badge: store.channels.count
                )

                dockItem(
                    tab: .transfers,
                    emoji: "📁",
                    title: "传输",
                    badge: activeTransfersCount
                )
            }

            Spacer()

            // 右侧传输抽屉快捷开关
            Button {
                if activeTab == .transfers || activeTab == .settings {
                    activeTab = .messages
                    showInspector = true
                } else {
                    showInspector.toggle()
                }
            } label: {
                VStack(spacing: 2) {
                    Image(systemName: "sidebar.right")
                        .font(.system(size: 15, weight: .medium))
                        .foregroundColor(showInspector ? QQPalette.qqBlue : .white.opacity(0.65))
                    Text("抽屉")
                        .font(.system(size: 9.5))
                        .foregroundColor(showInspector ? QQPalette.qqBlue : .white.opacity(0.55))
                }
                .frame(width: 48, height: 42)
                .background(
                    RoundedRectangle(cornerRadius: 10, style: .continuous)
                        .fill(showInspector ? Color.white.opacity(0.10) : Color.clear)
                )
            }
            .buttonStyle(.plain)
            .help("展开/收起右侧实时传输抽屉")

            // 底部设置齿轮图标：⚙️ 设置
            dockItem(
                tab: .settings,
                emoji: "⚙️",
                title: "设置",
                badge: 0
            )
            .padding(.bottom, 16)
        }
        .frame(width: 64)
        .frame(maxHeight: .infinity)
        .background(QQPalette.railBackground)
    }

    private func dockItem(tab: QQDockTab, emoji: String, title: String, badge: Int) -> some View {
        let isSelected = (activeTab == tab)

        return Button {
            activeTab = tab
        } label: {
            ZStack(alignment: .topTrailing) {
                VStack(spacing: 3) {
                    Text(emoji)
                        .font(.system(size: 18))
                    Text(title)
                        .font(.system(size: 10, weight: isSelected ? .semibold : .regular))
                        .foregroundColor(isSelected ? .white : .white.opacity(0.65))
                }
                .frame(width: 48, height: 48)
                .background(
                    RoundedRectangle(cornerRadius: 11, style: .continuous)
                        .fill(isSelected ? QQPalette.qqBlue : Color.clear)
                )

                if badge > 0 {
                    Text(badge > 99 ? "99+" : "\(badge)")
                        .font(.system(size: 9, weight: .bold))
                        .foregroundColor(.white)
                        .padding(.horizontal, 4.5)
                        .padding(.vertical, 1.5)
                        .background(
                            Capsule()
                                .fill(tab == .messages || tab == .transfers ? QQPalette.unreadRed : Color.white.opacity(0.28))
                        )
                        .offset(x: 2, y: -2)
                }
            }
        }
        .buttonStyle(.plain)
    }
}

// MARK: - Column 2: 260pt 宽会话 / 联系人 / 频道列表栏

private struct QQSessionListColumn: View {
    @ObservedObject var store: ChatStore
    let activeTab: QQDockTab
    @Binding var selection: SidebarRoute?
    @Binding var searchText: String
    @Binding var showCreateChannel: Bool

    private var normalizedQuery: String {
        searchText.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
    }

    private var filteredPeers: [Peer] {
        let base: [Peer]
        if normalizedQuery.isEmpty {
            base = store.peers
        } else {
            base = store.peers.filter { peer in
                if peer.nickname.lowercased().contains(normalizedQuery) || peer.os.lowercased().contains(normalizedQuery) {
                    return true
                }
                return !store.searchMessages(in: peer.id, query: normalizedQuery).isEmpty
            }
        }
        guard activeTab == .messages else { return base }
        return base.sorted { a, b in
            let tsA = store.messages[a.id]?.last?.timestamp ?? .distantPast
            let tsB = store.messages[b.id]?.last?.timestamp ?? .distantPast
            return tsA > tsB
        }
    }

    private var filteredChannels: [Channel] {
        guard !normalizedQuery.isEmpty else { return store.channels }
        return store.channels.filter { channel in
            if channel.name.lowercased().contains(normalizedQuery)
                || (channel.topic?.lowercased().contains(normalizedQuery) == true) {
                return true
            }
            return !store.searchMessages(in: channel.id, query: normalizedQuery).isEmpty
        }
    }

    var body: some View {
        VStack(spacing: 0) {
            // 顶部搜索框 + 快速新建频道 "+" 按钮
            HStack(spacing: 8) {
                HStack(spacing: 6) {
                    Image(systemName: "magnifyingglass")
                        .font(.system(size: 12))
                        .foregroundColor(.secondary)

                    TextField("搜索联系人、频道或聊天记录…", text: $searchText)
                        .textFieldStyle(.plain)
                        .font(.system(size: 12))

                    if !searchText.isEmpty {
                        Button {
                            searchText = ""
                        } label: {
                            Image(systemName: "xmark.circle.fill")
                                .font(.system(size: 11))
                                .foregroundColor(.secondary)
                        }
                        .buttonStyle(.plain)
                    }
                }
                .padding(.horizontal, 9)
                .padding(.vertical, 6)
                .background(
                    RoundedRectangle(cornerRadius: 8, style: .continuous)
                        .fill(Color(nsColor: .windowBackgroundColor))
                )

                Button {
                    showCreateChannel = true
                } label: {
                    Image(systemName: "plus")
                        .font(.system(size: 13, weight: .semibold))
                        .foregroundColor(.primary.opacity(0.8))
                        .frame(width: 28, height: 28)
                        .background(
                            RoundedRectangle(cornerRadius: 8, style: .continuous)
                                .fill(Color(nsColor: .windowBackgroundColor))
                        )
                }
                .buttonStyle(.plain)
                .help("新建频道")
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 10)
            .background(Color(nsColor: .controlBackgroundColor).opacity(0.65))

            Divider()

            ScrollView {
                LazyVStack(spacing: 4) {
                    // 置顶全员广播群会话卡片 (group = "*")
                    broadcastSessionCard

                    // 频道区
                    if activeTab == .messages || activeTab == .channels {
                        if !filteredChannels.isEmpty {
                            sectionHeader("频道 (\(filteredChannels.count))")
                            ForEach(filteredChannels) { channel in
                                channelSessionCard(channel)
                            }
                        } else if activeTab == .channels {
                            emptyHint("暂无频道，点击右上角「+」创建频道")
                        }
                    }

                    // 在线联系人区
                    if activeTab == .messages || activeTab == .contacts {
                        sectionHeader("在线联系人 (\(filteredPeers.count))")
                        if filteredPeers.isEmpty {
                            emptyHint(normalizedQuery.isEmpty ? "当前暂无其他在线用户" : "未找到匹配的联系人")
                        } else {
                            ForEach(filteredPeers) { peer in
                                peerSessionCard(peer)
                            }
                        }
                    }
                }
                .padding(.horizontal, 8)
                .padding(.vertical, 8)
            }
        }
        .background(Color(nsColor: .controlBackgroundColor).opacity(0.35))
    }

    private func sectionHeader(_ text: String) -> some View {
        HStack {
            Text(text)
                .font(.system(size: 11, weight: .semibold))
                .foregroundColor(.secondary)
            Spacer()
        }
        .padding(.horizontal, 8)
        .padding(.top, 8)
        .padding(.bottom, 2)
    }

    private func emptyHint(_ text: String) -> some View {
        Text(text)
            .font(.system(size: 11.5))
            .foregroundColor(.secondary.opacity(0.8))
            .frame(maxWidth: .infinity)
            .padding(.vertical, 18)
    }

    // MARK: - 置顶全员广播群卡片

    private var broadcastSessionCard: some View {
        let isSelected = (selection == .broadcast)
        let lastMsg = store.messages["*"]?.last
        let unread = store.unreadCounts["*", default: 0]

        return Button {
            selection = .broadcast
            store.markThreadActive("*")
        } label: {
            HStack(spacing: 10) {
                QQAvatar(
                    key: "*",
                    title: "广",
                    size: 42,
                    status: "online",
                    emojiBadge: "📢"
                )

                VStack(alignment: .leading, spacing: 3) {
                    HStack(spacing: 4) {
                        Text("📢 局域网全员广播群")
                            .font(.system(size: 13, weight: .bold))
                            .foregroundColor(.primary)
                            .lineLimit(1)

                        Spacer(minLength: 4)

                        if let lastMsg {
                            Text(Formatters.sessionTime(lastMsg.timestamp))
                                .font(.system(size: 10.5))
                                .foregroundColor(.secondary)
                        } else {
                            Text("置顶")
                                .font(.system(size: 9.5, weight: .semibold))
                                .foregroundColor(QQPalette.qqBlue)
                                .padding(.horizontal, 5)
                                .padding(.vertical, 1)
                                .background(Capsule().fill(QQPalette.qqBlue.opacity(0.14)))
                        }
                    }

                    HStack(spacing: 4) {
                        Text(lastMsg?.text ?? "全体在线成员 (\(store.peers.count + 1) 人) · Swarm 广播")
                            .font(.system(size: 11.5))
                            .foregroundColor(.secondary)
                            .lineLimit(1)

                        Spacer(minLength: 4)

                        if unread > 0 {
                            unreadBadge(unread)
                        }
                    }
                }
            }
            .padding(.horizontal, 9)
            .padding(.vertical, 8)
            .background(
                RoundedRectangle(cornerRadius: 10, style: .continuous)
                    .fill(isSelected ? QQPalette.qqBlue.opacity(0.15) : Color.primary.opacity(0.03))
            )
            .overlay(
                RoundedRectangle(cornerRadius: 10, style: .continuous)
                    .stroke(isSelected ? QQPalette.qqBlue.opacity(0.45) : Color.clear, lineWidth: 1)
            )
        }
        .buttonStyle(.plain)
    }

    // MARK: - 联系人卡片

    private func peerSessionCard(_ peer: Peer) -> some View {
        let isSelected = (selection == .peer(peer.id))
        let lastMsg = store.messages[peer.id]?.last
        let unread = store.unreadCounts[peer.id, default: 0]

        return Button {
            selection = .peer(peer.id)
            store.markThreadActive(peer.id)
        } label: {
            HStack(spacing: 10) {
                QQAvatar(
                    key: peer.id,
                    title: peer.nickname,
                    size: 42,
                    status: peer.status.isEmpty ? "online" : peer.status
                )

                VStack(alignment: .leading, spacing: 3) {
                    HStack(spacing: 4) {
                        Text(peer.nickname)
                            .font(.system(size: 13, weight: .semibold))
                            .foregroundColor(.primary)
                            .lineLimit(1)

                        Spacer(minLength: 4)

                        if let lastMsg {
                            Text(Formatters.sessionTime(lastMsg.timestamp))
                                .font(.system(size: 10.5))
                                .foregroundColor(.secondary)
                        } else if !peer.os.isEmpty {
                            Text(peer.os)
                                .font(.system(size: 10))
                                .foregroundColor(.secondary)
                                .padding(.horizontal, 5)
                                .padding(.vertical, 1)
                                .background(Capsule().fill(Color.primary.opacity(0.06)))
                        }
                    }

                    HStack(spacing: 4) {
                        Text(lastMsg?.text ?? (peer.status.isEmpty ? "[\(peer.os)] 在线 · 点击发起会话" : peer.status))
                            .font(.system(size: 11.5))
                            .foregroundColor(.secondary)
                            .lineLimit(1)

                        Spacer(minLength: 4)

                        if unread > 0 {
                            unreadBadge(unread)
                        }
                    }
                }
            }
            .padding(.horizontal, 9)
            .padding(.vertical, 8)
            .background(
                RoundedRectangle(cornerRadius: 10, style: .continuous)
                    .fill(isSelected ? QQPalette.qqBlue.opacity(0.15) : Color.clear)
            )
            .overlay(
                RoundedRectangle(cornerRadius: 10, style: .continuous)
                    .stroke(isSelected ? QQPalette.qqBlue.opacity(0.45) : Color.clear, lineWidth: 1)
            )
        }
        .buttonStyle(.plain)
    }

    // MARK: - 频道卡片

    private func channelSessionCard(_ channel: Channel) -> some View {
        let isSelected = (selection == .channel(channel.id))
        let lastMsg = store.messages[channel.id]?.last
        let unread = store.unreadCounts[channel.id, default: 0]

        return Button {
            selection = .channel(channel.id)
            store.markThreadActive(channel.id)
        } label: {
            HStack(spacing: 10) {
                QQAvatar(
                    key: channel.id,
                    title: channel.name,
                    size: 42,
                    systemIcon: channel.isPrivate == true ? "lock.fill" : "number"
                )

                VStack(alignment: .leading, spacing: 3) {
                    HStack(spacing: 4) {
                        Text(channel.name)
                            .font(.system(size: 13, weight: .semibold))
                            .foregroundColor(.primary)
                            .lineLimit(1)

                        Spacer(minLength: 4)

                        if let lastMsg {
                            Text(Formatters.sessionTime(lastMsg.timestamp))
                                .font(.system(size: 10.5))
                                .foregroundColor(.secondary)
                        } else {
                            Text("\(channel.members.count)人")
                                .font(.system(size: 10))
                                .foregroundColor(.secondary)
                        }
                    }

                    HStack(spacing: 4) {
                        Text(lastMsg?.text ?? channel.subtitle)
                            .font(.system(size: 11.5))
                            .foregroundColor(.secondary)
                            .lineLimit(1)

                        Spacer(minLength: 4)

                        if unread > 0 {
                            unreadBadge(unread)
                        }
                    }
                }
            }
            .padding(.horizontal, 9)
            .padding(.vertical, 8)
            .background(
                RoundedRectangle(cornerRadius: 10, style: .continuous)
                    .fill(isSelected ? QQPalette.qqBlue.opacity(0.15) : Color.clear)
            )
            .overlay(
                RoundedRectangle(cornerRadius: 10, style: .continuous)
                    .stroke(isSelected ? QQPalette.qqBlue.opacity(0.45) : Color.clear, lineWidth: 1)
            )
        }
        .buttonStyle(.plain)
    }

    private func unreadBadge(_ count: Int) -> some View {
        Text(count > 99 ? "99+" : "\(count)")
            .font(.system(size: 10, weight: .bold))
            .foregroundColor(.white)
            .padding(.horizontal, 5.5)
            .padding(.vertical, 1.5)
            .background(
                Capsule()
                    .fill(QQPalette.unreadRed)
            )
    }
}
