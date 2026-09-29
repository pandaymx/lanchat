import SwiftUI

/// 设置界面（iOS 无下载目录选择器，固定在 App 沙盒 Documents/LANChat）。
struct SettingsView: View {
    @EnvironmentObject private var store: AppStore
    @EnvironmentObject private var settings: SettingsStore

    var body: some View {
        NavigationStack {
            Form {
                Section("昵称") {
                    TextField("昵称", text: $settings.nickname)
                        .autocorrectionDisabled()
                    Button("应用昵称") { store.applyNickname() }
                }

                Section("下载目录") {
                    Text(settings.downloadDir)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Text("iOS 版文件统一保存在 App 沙盒内，可在系统「文件」App 中查看。")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }

                Section("能力边界") {
                    Label("仅在前台传输", systemImage: "sun.max")
                        .font(.footnote)
                    Label("超过 1 GiB 的文件发送前会提示", systemImage: "exclamationmark.triangle")
                        .font(.footnote)
                }

                Section("状态") {
                    HStack {
                        Circle()
                            .fill(store.conn == "connected" ? Color.green : Color.gray)
                            .frame(width: 10, height: 10)
                        Text(store.server.isEmpty ? "未连接服务器" : store.server)
                            .font(.footnote)
                    }
                }
            }
            .navigationTitle("设置")
        }
    }
}
