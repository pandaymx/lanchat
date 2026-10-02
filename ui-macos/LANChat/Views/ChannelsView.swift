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
            Text("新建频道").font(.headline)
            TextField("频道名称", text: $name)
                .textFieldStyle(.roundedBorder)
                .onSubmit { create() }
            TextField("主题（可选）", text: $topic)
                .textFieldStyle(.roundedBorder)
            Toggle(isOn: $isPrivate) {
                Text("私有频道（仅可通过邀请加入）")
            }
            HStack {
                Spacer()
                Button("取消", role: .cancel) { dismiss() }
                Button("创建") { create() }
                    .keyboardShortcut(.defaultAction)
                    .disabled(trimmed.isEmpty)
            }
        }
        .padding(20)
        .frame(width: 340)
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

/// 频道会话：频道消息气泡（按 group=channelID 归类）+ 群文本 + 群文件。
struct ChannelChatView: View {
    @ObservedObject var store: ChatStore
    let channelID: String

    @State private var draft = ""
    @State private var showMembers = false
    @State private var showInvite = false

    init(store: ChatStore, channel: Channel) {
        self.store = store
        channelID = channel.id
    }

    private var channel: Channel? {
        store.channels.first(where: { $0.id == channelID })
    }

    private var joined: Bool {
        channel.map { store.isMember($0) } ?? false
    }

    private var thread: [ChatMessage] {
        guard joined else { return [] }
        return (store.messages[channelID] ?? []).map { msg in
            var m = msg
            if m.inbound {
                m.senderName = store.peers.first(where: { $0.id == msg.peerId })?.nickname
                    ?? String(msg.peerId.prefix(8))
            }
            return m
        }
    }

    var body: some View {
        VStack(spacing: 0) {
            if let channel {
                header(channel)
                Divider()
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
        .frame(minWidth: 480, minHeight: 420)
        .popover(isPresented: $showMembers) {
            if let channel { MemberList(store: store, channel: channel) }
        }
        .popover(isPresented: $showInvite) {
            if let channel { InviteList(store: store, channel: channel) }
        }
    }

    private func header(_ channel: Channel) -> some View {
        HStack(spacing: 10) {
            Image(systemName: channel.isPrivate == true ? "lock" : "number")
                .foregroundColor(.secondary)
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(channel.name).font(.headline)
                    if channel.isPrivate == true {
                        Text("私有")
                            .font(.caption2)
                            .padding(.horizontal, 5)
                            .padding(.vertical, 1)
                            .background(Color.secondary.opacity(0.15))
                            .clipShape(Capsule())
                    }
                }
                if let topic = channel.topic,
                   !topic.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                    Text(topic)
                        .font(.caption)
                        .foregroundColor(.secondary)
                        .lineLimit(1)
                } else {
                    Text("\(channel.members.count) 名成员")
                        .font(.caption)
                        .foregroundColor(.secondary)
                }
            }
            Spacer()
            Button("成员") { showMembers = true }
            if channel.ownerId == store.selfId {
                Button("邀请成员") { showInvite = true }
            }
            if joined {
                Button("退出频道") {
                    Task { await store.leaveChannel(channelID: channel.id) }
                }
            }
        }
        .padding()
    }

    private var messages: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 8) {
                    ForEach(thread) { msg in
                        BubbleRow(message: msg, isSelf: !msg.inbound)
                            .id(msg.id)
                    }
                }
                .padding()
            }
            .onChange(of: thread.count) { _ in
                if let last = thread.last { proxy.scrollTo(last.id, anchor: .bottom) }
            }
        }
    }

    private func joinPrompt(_ channel: Channel) -> some View {
        VStack(spacing: 12) {
            Spacer()
            if channel.isPrivate == true {
                Label("这是私有频道，仅受邀成员可以加入", systemImage: "lock")
                    .foregroundColor(.secondary)
            } else {
                Text("你尚未加入此频道").foregroundColor(.secondary)
                Button("加入频道") {
                    Task { await store.joinChannel(channel.id) }
                }
            }
            Spacer()
        }
    }

    private func composer(_ channel: Channel) -> some View {
        VStack(spacing: 0) {
            Divider()
            HStack(alignment: .bottom, spacing: 8) {
                Button {
                    pickAndOffer(channel)
                } label: {
                    Image(systemName: "paperclip")
                }
                .help("向频道发送文件")

                TextEditor(text: $draft)
                    .frame(minHeight: 36, maxHeight: 120)
                    .padding(6)
                    .background(Color(nsColor: .controlBackgroundColor))
                    .cornerRadius(8)

                Button("发送") { send(channel) }
                    .keyboardShortcut(.defaultAction)
                    .disabled(draft.trimmingCharacters(in: .whitespaces).isEmpty)
            }
            .padding()
        }
    }

    private func send(_ channel: Channel) {
        let text = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return }
        draft = ""
        Task { await store.sendChannelText(channelID: channel.id, text: text) }
    }

    private func pickAndOffer(_ channel: Channel) {
        let panel = NSOpenPanel()
        panel.allowsMultipleSelection = false
        panel.canChooseDirectories = false
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
        VStack(alignment: .leading, spacing: 8) {
            Text("频道成员").font(.headline)
            ForEach(channel.members, id: \.self) { id in
                HStack(spacing: 8) {
                    let online = store.peers.first(where: { $0.id == id })
                    Image(systemName: "circle.fill")
                        .font(.system(size: 7))
                        .foregroundColor(online != nil ? .green : .gray)
                    Text(online?.nickname ?? String(id.prefix(8)))
                    if id == channel.ownerId {
                        Text("群主")
                            .font(.caption2)
                            .foregroundColor(.secondary)
                    }
                }
            }
        }
        .padding(16)
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
        VStack(alignment: .leading, spacing: 8) {
            Text("邀请成员").font(.headline)
            if candidates.isEmpty {
                Text("没有可邀请的在线成员")
                    .font(.callout)
                    .foregroundColor(.secondary)
            } else {
                ForEach(candidates) { peer in
                    Button {
                        Task { await store.inviteMember(channelID: channel.id,
                                                       memberID: peer.id) }
                    } label: {
                        HStack(spacing: 8) {
                            Image(systemName: "circle.fill")
                                .font(.system(size: 7))
                                .foregroundColor(.green)
                            Text(peer.nickname)
                            Spacer()
                            Image(systemName: "envelope")
                                .foregroundColor(.secondary)
                        }
                    }
                }
            }
        }
        .padding(16)
        .frame(minWidth: 240)
    }
}
