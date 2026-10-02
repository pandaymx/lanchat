import SwiftUI

/// 主界面：联系人 / 频道 / 传输 / 设置 四个标签。
struct MainView: View {
    var body: some View {
        TabView {
            PeersView()
                .tabItem { Label("联系人", systemImage: "person.2.fill") }
            ChannelsView()
                .tabItem { Label("频道", systemImage: "number") }
            TransfersView()
                .tabItem { Label("传输", systemImage: "arrow.up.arrow.down.circle.fill") }
            SettingsView()
                .tabItem { Label("设置", systemImage: "gearshape.fill") }
        }
    }
}
