import SwiftUI

/// 频道列表：展示可见频道，支持新建、加入，成员可直接进入群聊。
/// 私有频道对非成员的加入请求会被后端拒绝，经全局错误弹窗提示。
struct ChannelsView: View {
    @EnvironmentObject private var store: AppStore

    @State private var showCreate = false

    var body: some View {
        NavigationStack {
            List {
                if store.channels.isEmpty {
                    Text("暂无频道，点击右上角新建一个")
                        .foregroundStyle(.secondary)
                }
                ForEach(store.channels) { channel in
                    ChannelRow(channel: channel)
                }
            }
            .navigationDestination(for: Channel.self) { channel in
                ChannelChatView(channel: channel)
            }
            .navigationTitle("频道")
            .refreshable { await store.refresh() }
            .toolbar {
                ToolbarItem(placement: .navigationBarTrailing) {
                    Button {
                        showCreate = true
                    } label: {
                        Image(systemName: "plus")
                    }
                }
            }
            .sheet(isPresented: $showCreate) {
                CreateChannelView()
            }
        }
    }
}

/// 单个频道行：成员可进入群聊，非成员显示加入。
private struct ChannelRow: View {
    @EnvironmentObject private var store: AppStore
    let channel: Channel

    private var isMember: Bool { store.isMember(channel) }

    var body: some View {
        Group {
            if isMember {
                NavigationLink(value: channel) { rowContent }
            } else {
                rowContent
            }
        }
    }

    private var rowContent: some View {
        HStack(spacing: 10) {
            Image(systemName: channel.isPrivate ? "lock" : "number")
                .foregroundStyle(.secondary)
            VStack(alignment: .leading, spacing: 2) {
                Text(channel.name)
                if !channel.topic.isEmpty {
                    Text(channel.topic)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                } else {
                    Text("\(channel.members.count) 名成员")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
            Spacer()
            if !isMember {
                Button("加入") { store.joinChannel(channel) }
                    .buttonStyle(.bordered)
                    .font(.caption)
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
                    TextField("频道名称", text: $name)
                        .autocorrectionDisabled()
                    TextField("主题（可选）", text: $topic)
                        .autocorrectionDisabled()
                } footer: {
                    Text("创建后你将成为频道所有者。")
                }

                Section {
                    Toggle("私有频道", isOn: $isPrivate)
                } footer: {
                    Text("私有频道无法从列表直接加入，仅可由你邀请在线成员加入。")
                }
            }
            .navigationTitle("新建频道")
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
                    .disabled(name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
            }
        }
    }
}
