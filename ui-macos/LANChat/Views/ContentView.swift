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
                PeersSidebar(store: store)
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
        Text("选择一位联系人开始聊天")
            .foregroundColor(.secondary)
    }
}

private struct PeersSidebar: View {
    @ObservedObject var store: ChatStore
    @State private var selection: String?

    var body: some View {
        List(selection: $selection) {
            ForEach(store.peers) { peer in
                NavigationLink(value: peer.id) {
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
        .navigationDestination(for: String.self) { id in
            if let peer = store.peers.first(where: { $0.id == id }) {
                ChatView(store: store, peer: peer)
            }
        }
        .navigationTitle("联系人")
        .overlay {
            if store.peers.isEmpty {
                Text("当前没有其他在线用户").foregroundColor(.secondary)
            }
        }
    }
}
