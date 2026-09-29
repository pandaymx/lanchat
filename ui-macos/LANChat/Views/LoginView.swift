import SwiftUI

/// 登录页：mDNS 发现服务器列表，选择后输入 PSK 连接；也可手动填地址。
struct LoginView: View {
    @ObservedObject var store: ChatStore
    @ObservedObject var settings: SettingsStore

    @State private var psk = ""
    @State private var manualAddr = ""
    @State private var selected: ServerInfo?

    var body: some View {
        VStack(spacing: 0) {
            Form {
                Section("本机") {
                    TextField("昵称", text: $settings.nickname, onCommit: {
                        Task { await store.setNickname(settings.nickname) }
                    })
                }

                Section("发现的中心节点") {
                    if store.servers.isEmpty {
                        Text("未发现服务器，点击刷新（需在同一局域网）")
                            .foregroundColor(.secondary)
                    }
                    ForEach(store.servers) { srv in
                        ServerRow(server: srv, selected: selected?.id == srv.id)
                            .contentShape(Rectangle())
                            .onTapGesture {
                                selected = srv
                                manualAddr = srv.addr
                            }
                    }
                }

                Section("连接") {
                    TextField("服务器地址（host:port）", text: $manualAddr)
                    SecureField("口令 PSK", text: $psk)
                }
            }
            .formStyle(.grouped)

            HStack {
                Button("刷新发现") { Task { await store.browseServers() } }
                Spacer()
                connBadge
                Button("连接") {
                    Task { await store.connect(addr: manualAddr, psk: psk) }
                }
                .keyboardShortcut(.defaultAction)
                .disabled(manualAddr.isEmpty)
            }
            .padding()
        }
        .frame(minWidth: 420, minHeight: 460)
        .task { await store.browseServers() }
    }

    private var connBadge: some View {
        HStack(spacing: 6) {
            Circle()
                .fill(color(for: store.conn))
                .frame(width: 8, height: 8)
            Text(label(for: store.conn))
                .font(.caption)
                .foregroundColor(.secondary)
        }
    }

    private func color(for state: String) -> Color {
        switch state {
        case "connected": return .green
        case "connecting": return .orange
        case "auth_failed": return .red
        default: return .gray
        }
    }

    private func label(for state: String) -> String {
        switch state {
        case "connected": return "已连接"
        case "connecting": return "连接中"
        case "auth_failed": return "口令错误"
        default: return "未连接"
        }
    }
}

private struct ServerRow: View {
    let server: ServerInfo
    let selected: Bool

    var body: some View {
        HStack {
            VStack(alignment: .leading, spacing: 2) {
                Text(server.name).font(.body)
                Text("\(server.addr) · v\(server.version)")
                    .font(.caption)
                    .foregroundColor(.secondary)
            }
            Spacer()
            if selected { Image(systemName: "checkmark").foregroundColor(.accentColor) }
        }
    }
}
