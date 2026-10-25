import SwiftUI

/// 消息 / 联系人列表（手机 QQ 会话列表风格）。
struct PeersView: View {
    @EnvironmentObject private var store: AppStore
    @State private var searchText = ""

    /// 局域网全员广播群（group = "*"，通过 ChannelChatView 承载 G1 全员群聊与 Swarm 广播）。
    private var broadcastChannel: Channel {
        Channel(
            id: "*",
            name: "📢 局域网全员广播群",
            ownerId: store.selfId,
            topic: "全员广播群 (group=*) · 在线全员可见 · 支持 G1 Swarm 群发",
            isPrivate: false,
            members: store.peers.map(\.id)
        )
    }

    /// 按搜索关键词过滤在线联系人（匹配昵称、OS 或最近聊天记录）。
    private var filteredPeers: [Peer] {
        let q = searchText.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !q.isEmpty else { return store.peers }
        return store.peers.filter { peer in
            if peer.nickname.lowercased().contains(q) || peer.os.lowercased().contains(q) {
                return true
            }
            let msgs = store.messagesByPeer[peer.id] ?? []
            return msgs.contains { !$0.revoked && $0.text.lowercased().contains(q) }
        }
    }

    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                // 手机 QQ 顶部个人头像 + 在线状态 + 搜索栏
                QQTopHeaderView(
                    searchText: $searchText,
                    trailingActionIcon: "arrow.clockwise",
                    onTrailingAction: {
                        Task { await store.refresh() }
                    }
                )

                ScrollView {
                    LazyVStack(spacing: 10) {
                        // 1. 置顶：📢 局域网全员广播群 (group = "*")
                        NavigationLink {
                            ChannelChatView(channel: broadcastChannel)
                        } label: {
                            BroadcastSessionRow(
                                channel: broadcastChannel,
                                lastMessage: store.messagesByPeer["*"]?.last,
                                unreadCount: unreadCount(for: "*"),
                                onlineCount: store.peers.count
                            )
                        }
                        .buttonStyle(.plain)

                        // 2. 在线联系人分段标题
                        HStack {
                            Text("在线联系人 (\(filteredPeers.count))")
                                .font(.caption.weight(.semibold))
                                .foregroundStyle(.secondary)
                            Spacer()
                            Text("P2P 直连优先")
                                .font(.caption2)
                                .foregroundStyle(QQTheme.qqBlue)
                        }
                        .padding(.horizontal, 6)
                        .padding(.top, 4)

                        if filteredPeers.isEmpty {
                            emptyPeersCard
                        } else {
                            ForEach(filteredPeers) { peer in
                                NavigationLink {
                                    ChatView(peer: peer)
                                } label: {
                                    PeerSessionRow(
                                        peer: peer,
                                        lastMessage: store.messagesByPeer[peer.id]?.last,
                                        unreadCount: unreadCount(for: peer.id)
                                    )
                                }
                                .buttonStyle(.plain)
                            }
                        }
                    }
                    .padding(.horizontal, 14)
                    .padding(.vertical, 12)
                }
                .background(Color(.systemGroupedBackground))
                .refreshable {
                    await store.refresh()
                }
            }
            .navigationBarHidden(true)
        }
    }

    private func unreadCount(for key: String) -> Int {
        let msgs = store.messagesByPeer[key] ?? []
        return msgs.filter { $0.inbound && !$0.revoked && !$0.readBy.contains(store.selfId) }.count
    }

    private var emptyPeersCard: some View {
        VStack(spacing: 10) {
            Image(systemName: "person.2.wave.2")
                .font(.system(size: 34))
                .foregroundStyle(QQTheme.qqBlue.opacity(0.65))
            Text(searchText.isEmpty ? "当前没有其他在线用户" : "未找到匹配的联系人或聊天记录")
                .font(.subheadline.weight(.medium))
                .foregroundStyle(.secondary)
            Text("下拉刷新在线列表，或点击上方「📢 局域网全员广播群」发送全员消息")
                .font(.caption2)
                .foregroundStyle(.tertiary)
                .multilineTextAlignment(.center)
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 36)
        .padding(.horizontal, 16)
        .background(
            RoundedRectangle(cornerRadius: 18, style: .continuous)
                .fill(Color(.secondarySystemGroupedBackground))
        )
    }
}

/// 置顶全员广播群行（group = "*"）。
private struct BroadcastSessionRow: View {
    let channel: Channel
    let lastMessage: ChatMessage?
    let unreadCount: Int
    let onlineCount: Int

    var body: some View {
        HStack(spacing: 12) {
            // 48x48 广播群渐变圆头像
            ZStack(alignment: .bottomTrailing) {
                Circle()
                    .fill(QQTheme.headerGradient)
                    .frame(width: 48, height: 48)
                    .overlay(
                        Text("📢")
                            .font(.system(size: 22))
                    )

                Circle()
                    .fill(QQTheme.onlineGreen)
                    .frame(width: 12, height: 12)
                    .overlay(Circle().stroke(Color(.secondarySystemGroupedBackground), lineWidth: 2))
            }

            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 6) {
                    Text("📢 局域网全员广播群")
                        .font(.system(size: 16, weight: .bold))
                        .foregroundStyle(.primary)
                        .lineLimit(1)

                    Text("置顶")
                        .font(.system(size: 10, weight: .bold))
                        .foregroundStyle(QQTheme.qqBlue)
                        .padding(.horizontal, 6)
                        .padding(.vertical, 2)
                        .background(QQTheme.qqBlue.opacity(0.14), in: Capsule())

                    Spacer()

                    if let last = lastMessage {
                        Text(QQTheme.formatTime(last.timestamp))
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }
                }

                HStack(spacing: 6) {
                    Text(lastMessage?.text ?? "全员广播频道 (group=*) · \(onlineCount) 人在线")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)

                    Spacer()

                    if unreadCount > 0 {
                        Text("\(min(unreadCount, 99))")
                            .font(.system(size: 11, weight: .bold))
                            .foregroundStyle(.white)
                            .padding(.horizontal, 6)
                            .padding(.vertical, 2)
                            .background(QQTheme.unreadRed, in: Capsule())
                    }
                }
            }
        }
        .padding(12)
        .background(
            RoundedRectangle(cornerRadius: 16, style: .continuous)
                .fill(Color(.secondarySystemGroupedBackground))
                .overlay(
                    RoundedRectangle(cornerRadius: 16, style: .continuous)
                        .stroke(QQTheme.qqBlue.opacity(0.22), lineWidth: 1)
                )
        )
    }
}

/// 手机 QQ 会话行：彩色圆形头像 + 在线点 + 粗体昵称 + OS/状态徽章 + 最新消息预览 + 红色未读胶囊。
private struct PeerSessionRow: View {
    let peer: Peer
    let lastMessage: ChatMessage?
    let unreadCount: Int

    private var isAway: Bool {
        peer.status == "away"
    }

    var body: some View {
        HStack(spacing: 12) {
            // 48x48 彩色圆形头像 + 右下角在线状态圆点
            ZStack(alignment: .bottomTrailing) {
                Circle()
                    .fill(QQTheme.avatarColor(for: peer.id))
                    .frame(width: 48, height: 48)
                    .overlay(
                        Text(QQTheme.initial(for: peer.nickname))
                            .font(.system(size: 19, weight: .bold))
                            .foregroundStyle(.white)
                    )

                Circle()
                    .fill(isAway ? QQTheme.awayOrange : QQTheme.onlineGreen)
                    .frame(width: 13, height: 13)
                    .overlay(Circle().stroke(Color(.secondarySystemGroupedBackground), lineWidth: 2))
            }

            VStack(alignment: .leading, spacing: 5) {
                HStack(spacing: 6) {
                    Text(peer.nickname)
                        .font(.system(size: 16, weight: .bold))
                        .foregroundStyle(.primary)
                        .lineLimit(1)

                    // OS 与在线状态徽章
                    Text(peer.os.uppercased())
                        .font(.system(size: 10, weight: .semibold))
                        .foregroundStyle(QQTheme.qqBlue)
                        .padding(.horizontal, 6)
                        .padding(.vertical, 2)
                        .background(QQTheme.qqBlue.opacity(0.12), in: Capsule())

                    Text(isAway ? "离开" : "在线")
                        .font(.system(size: 10, weight: .medium))
                        .foregroundStyle(isAway ? QQTheme.awayOrange : QQTheme.onlineGreen)
                        .padding(.horizontal, 6)
                        .padding(.vertical, 2)
                        .background((isAway ? QQTheme.awayOrange : QQTheme.onlineGreen).opacity(0.12), in: Capsule())

                    Spacer()

                    if let last = lastMessage {
                        Text(QQTheme.formatTime(last.timestamp))
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }
                }

                HStack(spacing: 8) {
                    Text(lastMessage?.text ?? "[在线] 点击发起 P2P 加密聊天与文件直连")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)

                    Spacer()

                    if unreadCount > 0 {
                        Text(unreadCount > 99 ? "99+" : "\(unreadCount)")
                            .font(.system(size: 11, weight: .bold))
                            .foregroundStyle(.white)
                            .padding(.horizontal, 6)
                            .padding(.vertical, 2)
                            .background(QQTheme.unreadRed, in: Capsule())
                    }
                }
            }
        }
        .padding(12)
        .background(
            RoundedRectangle(cornerRadius: 16, style: .continuous)
                .fill(Color(.secondarySystemGroupedBackground))
        )
    }
}
