import SwiftUI

/// 频道列表（手机 QQ 群聊 / 频道卡片风格）：展示可见频道，支持新建、加入、退出、邀请，成员可直接进入群聊。
struct ChannelsView: View {
    @EnvironmentObject private var store: AppStore

    @State private var searchText = ""
    @State private var showCreate = false
    @State private var invitingChannel: Channel?

    private var filteredChannels: [Channel] {
        let q = searchText.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !q.isEmpty else { return store.channels }
        return store.channels.filter { ch in
            ch.name.lowercased().contains(q) || ch.topic.lowercased().contains(q)
        }
    }

    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                // 手机 QQ 顶部个人头像 + 搜索栏 + 新建频道按钮
                QQTopHeaderView(
                    searchText: $searchText,
                    trailingActionIcon: "plus",
                    onTrailingAction: { showCreate = true }
                )

                ScrollView {
                    LazyVStack(spacing: 12) {
                        // 顶部工具条：频道统计 + 「+ 新建频道」按钮
                        HStack {
                            Text("G2 自定义频道 (\(filteredChannels.count))")
                                .font(.caption.weight(.semibold))
                                .foregroundStyle(.secondary)

                            Spacer()

                            Button {
                                showCreate = true
                            } label: {
                                HStack(spacing: 4) {
                                    Image(systemName: "plus.circle.fill")
                                    Text("+ 新建频道")
                                }
                                .font(.caption.weight(.semibold))
                                .foregroundStyle(.white)
                                .padding(.horizontal, 12)
                                .padding(.vertical, 6)
                                .background(QQTheme.headerGradient, in: Capsule())
                            }
                        }
                        .padding(.horizontal, 4)

                        if filteredChannels.isEmpty {
                            emptyChannelsCard
                        } else {
                            ForEach(filteredChannels) { channel in
                                ChannelCard(
                                    channel: channel,
                                    onInvite: { invitingChannel = channel }
                                )
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
            .navigationDestination(for: Channel.self) { channel in
                ChannelChatView(channel: channel)
            }
            .sheet(isPresented: $showCreate) {
                CreateChannelView()
            }
            .sheet(item: $invitingChannel) { ch in
                ChannelInviteSheet(channel: ch)
            }
        }
    }

    private var emptyChannelsCard: some View {
        VStack(spacing: 12) {
            Circle()
                .fill(QQTheme.qqBlue.opacity(0.12))
                .frame(width: 60, height: 60)
                .overlay(
                    Text("#")
                        .font(.system(size: 28, weight: .bold))
                        .foregroundStyle(QQTheme.qqBlue)
                )

            Text(searchText.isEmpty ? "暂无频道，点击右上角或下方按钮新建" : "未找到匹配的频道")
                .font(.subheadline.weight(.medium))
                .foregroundStyle(.secondary)

            Button {
                showCreate = true
            } label: {
                Text("+ 新建频道")
                    .font(.footnote.weight(.bold))
                    .foregroundStyle(.white)
                    .padding(.horizontal, 18)
                    .padding(.vertical, 9)
                    .background(QQTheme.headerGradient, in: Capsule())
            }
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

/// 单个 QQ 频道卡片：# 头像、私有锁徽章、主题、成员数、加入/退出/邀请操作。
private struct ChannelCard: View {
    @EnvironmentObject private var store: AppStore
    let channel: Channel
    let onInvite: () -> Void

    private var isMember: Bool { store.isMember(channel) }
    private var isOwner: Bool { channel.ownerId == store.selfId }
    private var lastMessage: ChatMessage? { store.messagesByPeer[channel.id]?.last }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 12) {
                // # 频道头像 + 私有锁角标
                ZStack(alignment: .bottomTrailing) {
                    Circle()
                        .fill(channel.isPrivate
                              ? Color.orange.opacity(0.18)
                              : QQTheme.qqBlue.opacity(0.16))
                        .frame(width: 48, height: 48)
                        .overlay(
                            Text("#")
                                .font(.system(size: 22, weight: .heavy, design: .rounded))
                                .foregroundStyle(channel.isPrivate ? Color.orange : QQTheme.qqBlue)
                        )

                    if channel.isPrivate {
                        Circle()
                            .fill(Color.orange)
                            .frame(width: 18, height: 18)
                            .overlay(
                                Image(systemName: "lock.fill")
                                    .font(.system(size: 9, weight: .bold))
                                    .foregroundStyle(.white)
                            )
                    }
                }

                VStack(alignment: .leading, spacing: 4) {
                    HStack(spacing: 6) {
                        Text(channel.name)
                            .font(.system(size: 16, weight: .bold))
                            .foregroundStyle(.primary)
                            .lineLimit(1)

                        // 私有 / 公开 锁徽章
                        HStack(spacing: 3) {
                            Image(systemName: channel.isPrivate ? "lock.fill" : "globe")
                                .font(.system(size: 9))
                            Text(channel.isPrivate ? "私有" : "公开")
                                .font(.system(size: 10, weight: .semibold))
                        }
                        .foregroundStyle(channel.isPrivate ? Color.orange : QQTheme.qqBlue)
                        .padding(.horizontal, 6)
                        .padding(.vertical, 2)
                        .background(
                            (channel.isPrivate ? Color.orange : QQTheme.qqBlue).opacity(0.12),
                            in: Capsule()
                        )

                        Spacer()

                        Text("\(channel.members.count) 名成员")
                            .font(.caption2.weight(.medium))
                            .foregroundStyle(.secondary)
                            .padding(.horizontal, 7)
                            .padding(.vertical, 2)
                            .background(Color(.tertiarySystemGroupedBackground), in: Capsule())
                    }

                    if !channel.topic.isEmpty {
                        Text(channel.topic)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .lineLimit(2)
                    } else if let last = lastMessage {
                        Text("\(store.displayName(last.peerId)): \(last.text)")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .lineLimit(1)
                    } else {
                        Text("群组 Swarm 分发与多成员实时聊天频道")
                            .font(.caption)
                            .foregroundStyle(.tertiary)
                            .lineLimit(1)
                    }
                }
            }

            Divider()

            // 底部操作栏：进入群聊 / 加入 / 邀请 / 退出
            HStack(spacing: 10) {
                if isMember {
                    NavigationLink(value: channel) {
                        HStack(spacing: 4) {
                            Image(systemName: "bubble.left.and.bubble.right.fill")
                            Text("进入频道")
                        }
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.white)
                        .padding(.horizontal, 14)
                        .padding(.vertical, 7)
                        .background(QQTheme.qqBlue, in: Capsule())
                    }
                } else {
                    Button {
                        store.joinChannel(channel)
                    } label: {
                        HStack(spacing: 4) {
                            Image(systemName: "person.badge.plus")
                            Text("加入频道")
                        }
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(.white)
                        .padding(.horizontal, 14)
                        .padding(.vertical, 7)
                        .background(QQTheme.qqBlue, in: Capsule())
                    }
                }

                if isOwner || isMember {
                    Button(action: onInvite) {
                        HStack(spacing: 4) {
                            Image(systemName: "person.crop.circle.badge.plus")
                            Text("邀请")
                        }
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(QQTheme.qqBlue)
                        .padding(.horizontal, 12)
                        .padding(.vertical, 7)
                        .background(QQTheme.qqBlue.opacity(0.12), in: Capsule())
                    }
                }

                Spacer()

                if isMember {
                    Button(role: .destructive) {
                        store.leaveChannel(channel)
                    } label: {
                        HStack(spacing: 4) {
                            Image(systemName: "rectangle.portrait.and.arrow.right")
                            Text("退出")
                        }
                        .font(.caption.weight(.medium))
                        .foregroundStyle(.red)
                        .padding(.horizontal, 10)
                        .padding(.vertical, 7)
                        .background(Color.red.opacity(0.10), in: Capsule())
                    }
                }
            }
        }
        .padding(14)
        .background(
            RoundedRectangle(cornerRadius: 18, style: .continuous)
                .fill(Color(.secondarySystemGroupedBackground))
        )
    }
}

/// 邀请在线成员加入频道弹窗。
struct ChannelInviteSheet: View {
    @EnvironmentObject private var store: AppStore
    @Environment(\.dismiss) private var dismiss
    let channel: Channel

    private var live: Channel {
        store.channels.first { $0.id == channel.id } ?? channel
    }

    private var candidates: [Peer] {
        store.peers.filter { $0.id != store.selfId }
    }

    var body: some View {
        NavigationStack {
            List {
                Section("邀请在线联系人加入「\(live.name)」") {
                    if candidates.isEmpty {
                        Text("当前没有其他在线用户可邀请")
                            .foregroundStyle(.secondary)
                    }
                    ForEach(candidates) { peer in
                        HStack(spacing: 10) {
                            Circle()
                                .fill(QQTheme.avatarColor(for: peer.id))
                                .frame(width: 34, height: 34)
                                .overlay(
                                    Text(QQTheme.initial(for: peer.nickname))
                                        .font(.caption.weight(.bold))
                                        .foregroundStyle(.white)
                                )
                            VStack(alignment: .leading, spacing: 2) {
                                Text(peer.nickname)
                                    .font(.subheadline.weight(.medium))
                                Text(peer.os)
                                    .font(.caption2)
                                    .foregroundStyle(.secondary)
                            }
                            Spacer()
                            if live.members.contains(peer.id) {
                                Text("已加入")
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                            } else {
                                Button("邀请") {
                                    store.inviteMember(live, memberID: peer.id)
                                }
                                .font(.caption.weight(.semibold))
                                .foregroundStyle(.white)
                                .padding(.horizontal, 12)
                                .padding(.vertical, 5)
                                .background(QQTheme.qqBlue, in: Capsule())
                            }
                        }
                    }
                }
            }
            .navigationTitle("邀请成员")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .navigationBarTrailing) {
                    Button("完成") { dismiss() }
                }
            }
        }
    }
}

/// 新建频道表单：名称 + 可选主题 + 是否私有。
struct CreateChannelView: View {
    @EnvironmentObject private var store: AppStore
    @Environment(\.dismiss) private var dismiss

    @State private var name = ""
    @State private var topic = ""
    @State private var isPrivate = false

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    TextField("频道名称（如 研发讨论组）", text: $name)
                        .autocorrectionDisabled()
                    TextField("频道主题（可选）", text: $topic)
                        .autocorrectionDisabled()
                } header: {
                    Text("频道信息")
                } footer: {
                    Text("创建后你将成为频道所有者，支持群聊文本与 G1/G2 Swarm 文件分发。")
                }

                Section {
                    Toggle("🔒 私有频道", isOn: $isPrivate)
                        .tint(QQTheme.qqBlue)
                } header: {
                    Text("权限设置")
                } footer: {
                    Text("私有频道无法从列表直接加入，仅可由所有者邀请在线成员加入。")
                }
            }
            .navigationTitle("+ 新建频道")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .navigationBarLeading) {
                    Button("取消") { dismiss() }
                }
                ToolbarItem(placement: .navigationBarTrailing) {
                    Button("创建") {
                        store.createChannel(name: name, topic: topic, isPrivate: isPrivate)
                        dismiss()
                    }
                    .fontWeight(.bold)
                    .foregroundStyle(QQTheme.qqBlue)
                    .disabled(name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
            }
        }
    }
}
