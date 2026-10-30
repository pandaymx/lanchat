import SwiftUI

@main
struct LANChatApp: App {
    @StateObject private var settings = SettingsStore()
    @StateObject private var store: ChatStore
    @Environment(\.openWindow) private var openWindow

    init() {
        let settings = SettingsStore()
        _settings = StateObject(wrappedValue: settings)
        _store = StateObject(wrappedValue: ChatStore(settings: settings))
    }

    var body: some Scene {
        WindowGroup(id: "main") {
            ContentView(store: store, settings: settings)
                .task { await store.bootstrap() }
        }
        .windowResizability(.contentSize)

        MenuBarExtra {
            MenuBarContent(store: store, openMain: { openWindow(id: "main") })
        } label: {
            HStack(spacing: 3) {
                Image(systemName: store.totalUnreadCount > 0 ? "bubble.left.and.exclamationmark.bubble.right.fill" : "bubble.left.and.bubble.right")
                if store.totalUnreadCount > 0 {
                    Text("\(store.totalUnreadCount)")
                }
            }
        }
        .menuBarExtraStyle(.menu)
    }
}

private struct MenuBarContent: View {
    @ObservedObject var store: ChatStore
    let openMain: () -> Void

    var body: some View {
        Text(store.conn == "connected" ? "状态：已连接" : "状态：未连接")
        Text("在线联系人：\(store.peers.count) · 未读：\(store.totalUnreadCount)")
        Divider()
        Button("打开 LANChat", action: openMain)
        if store.totalUnreadCount > 0 {
            Button("💬 快捷回复最新未读（收到）") {
                Task { await store.quickReplyLatestUnread() }
            }
        }
        if store.hasIncomingCall {
            Button("📞 快捷接听语音来电") {
                openMain()
                Task { await store.quickAcceptIncomingCall() }
            }
        }
        if let pending = store.pendingInboundTransfer {
            Button("📥 快捷接收文件：\(pending.name)") {
                Task { await store.quickAcceptPendingFile() }
            }
        }
        if !store.transfers.isEmpty {
            Divider()
            ForEach(store.transfers.prefix(5)) { t in
                Text("\(t.name)：\(Int(t.fraction * 100))%")
            }
        }
        Divider()
        Button("退出 LANChat") {
            store.shutdown()
            NSApplication.shared.terminate(nil)
        }
    }
}
