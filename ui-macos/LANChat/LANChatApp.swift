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
            Image(systemName: "bubble.left.and.bubble.right")
        }
        .menuBarExtraStyle(.menu)
    }
}

private struct MenuBarContent: View {
    @ObservedObject var store: ChatStore
    let openMain: () -> Void

    var body: some View {
        Text(store.conn == "connected" ? "状态：已连接" : "状态：未连接")
        Text("在线联系人：\(store.peers.count)")
        Divider()
        Button("打开 LANChat", action: openMain)
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
