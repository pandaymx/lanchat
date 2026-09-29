import SwiftUI

/// 登录 / 连接界面。
struct LoginView: View {
    @EnvironmentObject private var store: AppStore
    @EnvironmentObject private var settings: SettingsStore

    @State private var manualAddr = ""
    @State private var psk = ""

    var body: some View {
        NavigationStack {
            Form {
                Section("本机昵称") {
                    TextField("昵称", text: $settings.nickname)
                        .autocorrectionDisabled()
                    Button("应用昵称") { store.applyNickname() }
                }

                Section("发现的中心节点") {
                    if store.discoveredServers.isEmpty {
                        Text("未发现节点，请下拉刷新或手动输入地址")
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                    }
                    ForEach(store.discoveredServers) { srv in
                        Button {
                            manualAddr = srv.addr
                        } label: {
                            HStack {
                                VStack(alignment: .leading) {
                                    Text(srv.name).foregroundStyle(.primary)
                                    Text(srv.addr)
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                }
                                Spacer()
                                Text(srv.authMode)
                                    .font(.caption2)
                                    .padding(.horizontal, 6).padding(.vertical, 2)
                                    .background(.tertiary, in: Capsule())
                            }
                        }
                    }
                }

                Section("连接") {
                    TextField("地址（host:port）", text: $manualAddr)
                        .keyboardType(.numbersAndPunctuation)
                        .autocapitalization(.none)
                        .disableAutocorrection(true)
                    SecureField("口令 PSK", text: $psk)
                }

                Section {
                    HStack {
                        statusDot
                        Text(statusText).font(.footnote)
                        Spacer()
                        if store.busy { ProgressView() }
                    }
                    Button {
                        store.connect(addr: manualAddr, psk: psk)
                    } label: {
                        Text("连接").frame(maxWidth: .infinity)
                    }
                    .disabled(store.busy)
                }
            }
            .navigationTitle("LANChat")
            .refreshable { await store.browseServers() }
        }
    }

    private var statusDot: some View {
        let color: Color
        switch store.conn {
        case "connected": color = .green
        case "connecting": color = .orange
        case "auth_failed": color = .red
        default: color = .gray
        }
        return Circle().fill(color).frame(width: 10, height: 10)
    }

    private var statusText: String {
        switch store.conn {
        case "connected": return "已连接"
        case "connecting": return "连接中…"
        case "auth_failed": return "鉴权失败"
        default: return "未连接"
        }
    }
}
