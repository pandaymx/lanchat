import SwiftUI

/// 主窗口：未连接显示登录页；连接后进入联系人/传输/设置。
struct ContentView: View {
    @ObservedObject var store: ChatStore
    @ObservedObject var settings: SettingsStore

    var body: some View {
        Group {
            if store.conn == "connected" {
                main
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

    private var main: some View {
        TabView {
            NavigationSplitView {
                MainSidebar(store: store)
            } detail: {
                detailPlaceholder
            }
            .tabItem { Label("联系人", systemImage: "person.2") }

            TransfersView(store: store)
                .tabItem { Label("传输", systemImage: "arrow.up.arrow.down") }

            SettingsView(store: store, settings: settings)
                .tabItem { Label("设置", systemImage: "gearshape") }
        }
        .frame(minWidth: 820, minHeight: 560)
    }

    @ViewBuilder
    private var detailPlaceholder: some View {
        Text("选择一位联系人或频道开始聊天")
            .foregroundColor(.secondary)
    }
}

/// 侧栏选中项：区分单播联系人与频道。
enum SidebarRoute: Hashable {
    case peer(String)
    case channel(String)
}

private struct MainSidebar: View {
    @ObservedObject var store: ChatStore
    @State private var selection: SidebarRoute?
    @State private var showCreate = false

    var body: some View {
        List(selection: $selection) {
            Section("联系人") {
                ForEach(store.peers) { peer in
                    NavigationLink(value: SidebarRoute.peer(peer.id)) {
                        HStack {
                            Circle()
                                .fill(peer.status == "busy" ? Color.orange : Color.green)
                                .frame(width: 8, height: 8)
                            VStack(alignment: .leading, spacing: 1) {
                                Text(peer.nickname)
                                Text(peer.os).font(.caption2).foregroundColor(.secondary)
                            }
                        }
                    }
                }
            }

            Section("频道") {
                ForEach(store.channels) { channel in
                    NavigationLink(value: SidebarRoute.channel(channel.id)) {
                        HStack {
                            Image(systemName: channel.isPrivate == true ? "lock" : "number")
                                .foregroundColor(.secondary)
                                .frame(width: 16)
                            VStack(alignment: .leading, spacing: 1) {
                                Text(channel.name)
                                Text(channel.subtitle)
                                    .font(.caption2)
                                    .foregroundColor(.secondary)
                            }
                        }
                    }
                }
            }
        }
        .navigationDestination(for: SidebarRoute.self) { route in
            switch route {
            case let .peer(id):
                if let peer = store.peers.first(where: { $0.id == id }) {
                    ChatView(store: store, peer: peer)
                }
            case let .channel(id):
                if let channel = store.channels.first(where: { $0.id == id }) {
                    ChannelChatView(store: store, channel: channel)
                }
            }
        }
        .navigationTitle("联系人")
        .toolbar {
            Button {
                showCreate = true
            } label: {
                Image(systemName: "plus")
            }
            .help("新建频道")
        }
        .sheet(isPresented: $showCreate) {
            CreateChannelSheet(store: store)
        }
        .overlay {
            if store.peers.isEmpty && store.channels.isEmpty {
                Text("当前没有其他在线用户，也没有频道")
                    .foregroundColor(.secondary)
            }
        }
    }
}
