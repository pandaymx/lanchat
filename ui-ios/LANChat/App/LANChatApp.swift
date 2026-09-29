import SwiftUI

@main
struct LANChatApp: App {
    @StateObject private var settings = SettingsStore()
    @StateObject private var store: AppStore

    init() {
        let settings = SettingsStore()
        _settings = StateObject(wrappedValue: settings)
        _store = StateObject(wrappedValue: AppStore(settings: settings))
    }

    var body: some Scene {
        WindowGroup {
            RootView()
                .environmentObject(store)
                .environmentObject(settings)
                .task { await store.bootstrap() }
        }
    }
}

/// 根据连接状态切换登录 / 主界面。
struct RootView: View {
    @EnvironmentObject private var store: AppStore

    var body: some View {
        Group {
            if store.conn == "connected" {
                MainView()
            } else {
                LoginView()
            }
        }
        .alert("提示", isPresented: Binding(
            get: { store.error != nil },
            set: { if !$0 { store.consumeError() } }
        )) {
            Button("好", role: .cancel) { store.consumeError() }
        } message: {
            Text(store.error ?? "")
        }
    }
}
