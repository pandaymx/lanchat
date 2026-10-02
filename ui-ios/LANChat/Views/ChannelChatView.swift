import SwiftUI

/// 频道群聊：气泡展示频道消息，按 group(频道ID) 发送与归类。
struct ChannelChatView: View {
    @EnvironmentObject private var store: AppStore
    @Environment(\.dismiss) private var dismiss
    let channel: Channel

    @State private var draft = ""
    @State private var showPicker = false
    @State private var showMembers = false

    /// 实时频道快照（channel 为进入时的快照，成员 / 主题需跟随 store 更新）。
    private var live: Channel {
        store.channels.first { $0.id == channel.id } ?? channel
    }

    private var thread: [ChatMessage] {
        store.messagesByPeer[channel.id] ?? []
    }

    var body: some View {
        VStack(spacing: 0) {
            if !live.topic.isEmpty || live.isPrivate {
                ChannelHeader(channel: live)
            }
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(spacing: 8) {
                        ForEach(thread) { msg in
                            ChannelBubble(message: msg)
                                .padding(.horizontal)
                        }
                    }
                    .padding(.vertical, 8)
                }
                .onChange(of: thread.count) { _ in
                    if let last = thread.last {
                        withAnimation { proxy.scrollTo(last.id, anchor: .bottom) }
                    }
                }
            }

            Divider()
            HStack(alignment: .bottom, spacing: 8) {
                Button {
                    showPicker = true
                } label: {
                    Image(systemName: "paperclip")
                        .font(.title3)
                        .frame(width: 34, height: 34)
                }
                TextField("消息", text: $draft, axis: .vertical)
                    .lineLimit(1...5)
                    .padding(8)
                    .background(Color(.secondarySystemBackground), in: RoundedRectangle(cornerRadius: 16))
                Button {
                    store.sendGroupText(channel: channel, text: draft)
                    draft = ""
                } label: {
                    Text("发送").fontWeight(.semibold)
                }
                .disabled(draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 8)
        }
        .navigationTitle(channel.name)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .navigationBarTrailing) {
                Button {
                    showMembers = true
                } label: {
                    Image(systemName: "person.2")
                }
            }
        }
        .sheet(isPresented: $showMembers) {
            ChannelMembersView(channel: live)
        }
        .sheet(isPresented: $showPicker) {
            FilePicker(scenario: .channel) { url in
                store.offerFileToGroup(channel: channel, path: url.path)
                showPicker = false
            }
            .ignoresSafeArea()
        }
        .onChange(of: store.isMember(live)) { isMember in
            // 退出频道后自动返回列表。
            if !isMember { dismiss() }
        }
    }
}

/// 频道信息头部：私有锁标记 + 主题。
private struct ChannelHeader: View {
    let channel: Channel

    var body: some View {
        HStack(spacing: 6) {
            if channel.isPrivate {
                Image(systemName: "lock")
                    .font(.caption2)
            }
            if !channel.topic.isEmpty {
                Text(channel.topic)
                    .font(.caption)
                    .lineLimit(1)
            }
        }
        .foregroundStyle(.secondary)
        .padding(.horizontal, 12)
        .padding(.vertical, 6)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(.secondarySystemBackground))
    }
}

/// 频道消息气泡：入站显示发送者名称。
private struct ChannelBubble: View {
    @EnvironmentObject private var store: AppStore
    let message: ChatMessage

    var body: some View {
        HStack {
            if !message.inbound { Spacer(minLength: 40) }
            VStack(alignment: .leading, spacing: 2) {
                if message.inbound {
                    Text(store.displayName(message.peerId))
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
                Text(message.text)
                    .padding(.horizontal, 12).padding(.vertical, 8)
                    .background(
                        message.inbound
                            ? Color(.tertiarySystemBackground)
                            : Color.accentColor.opacity(0.22),
                        in: RoundedRectangle(cornerRadius: 16)
                    )
                    .foregroundStyle(.primary)
                    .textSelection(.enabled)
            }
            if message.inbound { Spacer(minLength: 40) }
        }
    }
}

/// 频道成员列表：标注所有者；owner 可邀请在线成员，成员可退出频道。
private struct ChannelMembersView: View {
    @EnvironmentObject private var store: AppStore
    @Environment(\.dismiss) private var dismiss
    let channel: Channel

    private var isOwner: Bool { channel.ownerId == store.selfId }

    /// 可邀请的在线用户（排除自己；已为成员者在行内禁用）。
    private var inviteCandidates: [Peer] {
        store.peers.filter { $0.id != store.selfId }
    }

    var body: some View {
        NavigationStack {
            List {
                if isOwner {
                    Section("邀请成员") {
                        if inviteCandidates.isEmpty {
                            Text("当前没有其他在线用户可邀请")
                                .foregroundStyle(.secondary)
                        }
                        ForEach(inviteCandidates) { peer in
                            HStack {
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
                                    .buttonStyle(.bordered)
                                    .font(.caption)
                                }
                            }
                            .disabled(channel.members.contains(peer.id))
                        }
                    }
                }

                Section("成员 \(channel.members.count)") {
                    ForEach(channel.members, id: \.self) { memberId in
                        HStack {
                            Text(store.displayName(memberId))
                            Spacer()
                            if memberId == channel.ownerId {
                                Text("所有者")
                                    .font(.caption2)
                                    .foregroundStyle(.secondary)
                            }
                        }
                    }
                }

                Section {
                    Button(role: .destructive) {
                        store.leaveChannel(channel)
                    } label: {
                        Label("退出频道", systemImage: "rectangle.portrait.and.arrow.right")
                    }
                }
            }
            .navigationTitle("频道成员")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .navigationBarTrailing) {
                    Button("完成") { dismiss() }
                }
            }
        }
    }
}
