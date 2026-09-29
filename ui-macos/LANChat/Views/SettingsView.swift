import SwiftUI

/// 设置页：昵称、下载目录、退出时是否停止后台。
struct SettingsView: View {
    @ObservedObject var store: ChatStore
    @ObservedObject var settings: SettingsStore

    var body: some View {
        Form {
            Section("个人") {
                TextField("昵称", text: $settings.nickname)
                Button("应用昵称") { Task { await store.setNickname(settings.nickname) } }
            }

            Section("下载") {
                HStack {
                    Text(settings.downloadDir).lineLimit(1).truncationMode(.middle)
                    Spacer()
                    Button("更改…") { pickFolder() }
                }
            }

            Section("后台") {
                Toggle("退出应用时同时停止后台服务", isOn: $settings.stopDaemonOnExit)
            }

            Section {
                HStack {
                    Text("后台连接")
                    Spacer()
                    Text(store.daemonConnected ? "已连接" : "未连接")
                        .foregroundColor(store.daemonConnected ? .green : .red)
                }
            }
        }
        .formStyle(.grouped)
        .frame(width: 460)
    }

    private func pickFolder() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.allowsMultipleSelection = false
        guard panel.runModal() == .OK, let url = panel.url else { return }
        settings.downloadDir = url.path
    }
}
