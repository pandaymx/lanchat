import SwiftUI

/// 登录页：macOS QQ (NTQQ) 风格居中卡片，支持 mDNS 中心节点发现与手动地址/PSK 登录。
struct LoginView: View {
    @ObservedObject var store: ChatStore
    @ObservedObject var settings: SettingsStore

    @State private var psk = ""
    @State private var manualAddr = "ws://127.0.0.1:19090/lctp"
    @State private var selected: ServerInfo?
    @State private var isDiscovering = false

    private let qqBlue = Color(red: 0.0, green: 0.60, blue: 1.0) // #0099FF
    private let qqBlueDark = Color(red: 0.0, green: 0.45, blue: 0.92)

    var body: some View {
        ZStack {
            // macOS NTQQ 风格柔和渐变背景
            LinearGradient(
                colors: [
                    Color(red: 0.93, green: 0.96, blue: 1.0).opacity(0.45),
                    Color(nsColor: .windowBackgroundColor),
                    Color(red: 0.90, green: 0.95, blue: 1.0).opacity(0.35)
                ],
                startPoint: .topLeading,
                endPoint: .bottomTrailing
            )
            .ignoresSafeArea()

            ScrollView(.vertical, showsIndicators: false) {
                VStack(spacing: 18) {
                    // 顶部 76x76 QQ 渐变蓝圆形头像 + 在线绿点 + 标题副标题
                    headerSection

                    // 本机昵称设置栏
                    nicknameSection

                    // 局域网中心节点发现卡片
                    discoveryCard

                    // 服务器地址与 PSK 口令输入卡片
                    credentialsCard

                    // 全宽圆角 QQ Blue (#0099FF) 立即登录主按钮
                    loginButton

                    // 底部连接状态与守护进程状态栏
                    footerStatus
                }
                .padding(.horizontal, 28)
                .padding(.vertical, 26)
                .frame(width: 420)
                .background(
                    RoundedRectangle(cornerRadius: 18, style: .continuous)
                        .fill(Color(nsColor: .controlBackgroundColor))
                        .shadow(color: Color.black.opacity(0.10), radius: 20, x: 0, y: 8)
                )
                .overlay(
                    RoundedRectangle(cornerRadius: 18, style: .continuous)
                        .stroke(Color.primary.opacity(0.08), lineWidth: 1)
                )
                .padding(24)
            }
        }
        .frame(minWidth: 480, minHeight: 620)
        .task {
            await triggerDiscovery()
        }
    }

    // MARK: - 顶部品牌头像区

    private var headerSection: some View {
        VStack(spacing: 10) {
            ZStack(alignment: .bottomTrailing) {
                Circle()
                    .fill(
                        LinearGradient(
                            colors: [
                                Color(red: 0.08, green: 0.70, blue: 1.0),
                                qqBlue,
                                qqBlueDark
                            ],
                            startPoint: .topLeading,
                            endPoint: .bottomTrailing
                        )
                    )
                    .frame(width: 76, height: 76)
                    .shadow(color: qqBlue.opacity(0.38), radius: 12, x: 0, y: 6)
                    .overlay(
                        Image(systemName: "bubble.left.and.bubble.right.fill")
                            .font(.system(size: 32, weight: .semibold))
                            .foregroundColor(.white)
                    )

                // 右下角在线状态圆点
                Circle()
                    .fill(Color(red: 0.18, green: 0.80, blue: 0.44))
                    .frame(width: 18, height: 18)
                    .overlay(
                        Circle()
                            .stroke(Color(nsColor: .controlBackgroundColor), lineWidth: 3)
                    )
                    .offset(x: -2, y: -2)
            }
            .padding(.bottom, 2)

            Text("LANChat 局域网即时通信")
                .font(.system(size: 20, weight: .bold))
                .foregroundColor(.primary)

            Text("P2P 高速直连 · 端到端加密聊天")
                .font(.system(size: 12.5, weight: .medium))
                .foregroundColor(.secondary)
        }
    }

    // MARK: - 本机昵称区

    private var nicknameSection: some View {
        HStack(spacing: 10) {
            Text("👤")
                .font(.system(size: 14))
            Text("本机昵称")
                .font(.system(size: 12.5, weight: .medium))
                .foregroundColor(.secondary)
            TextField("输入你的局域网显示昵称", text: $settings.nickname, onCommit: {
                Task { await store.setNickname(settings.nickname) }
            })
            .textFieldStyle(.plain)
            .font(.system(size: 13, weight: .medium))
            .padding(.horizontal, 10)
            .padding(.vertical, 6)
            .background(
                RoundedRectangle(cornerRadius: 8, style: .continuous)
                    .fill(Color(nsColor: .windowBackgroundColor))
            )

            Button {
                Task { await store.setNickname(settings.nickname) }
            } label: {
                Text("保存")
                    .font(.system(size: 11.5, weight: .medium))
                    .foregroundColor(qqBlue)
            }
            .buttonStyle(.plain)
            .help("应用昵称")
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
        .background(
            RoundedRectangle(cornerRadius: 11, style: .continuous)
                .fill(Color.primary.opacity(0.035))
        )
    }

    // MARK: - 局域网中心节点发现卡片

    private var discoveryCard: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text("📡 局域网中心节点")
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundColor(.primary)

                Spacer()

                Button {
                    Task { await triggerDiscovery() }
                } label: {
                    HStack(spacing: 4) {
                        Text("🔄 自动发现")
                            .font(.system(size: 12, weight: .medium))
                    }
                    .foregroundColor(qqBlue)
                    .padding(.horizontal, 9)
                    .padding(.vertical, 4)
                    .background(
                        Capsule()
                            .fill(qqBlue.opacity(0.12))
                    )
                }
                .buttonStyle(.plain)
            }

            if store.servers.isEmpty {
                HStack(spacing: 8) {
                    Image(systemName: "antenna.radiowaves.left.and.right.slash")
                        .font(.system(size: 14))
                        .foregroundColor(.secondary)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(isDiscovering ? "正在通过 mDNS 扫描局域网节点…" : "暂未发现自动广播节点")
                            .font(.system(size: 12, weight: .medium))
                            .foregroundColor(.secondary)
                        Text("点击「🔄 自动发现」重新扫描，或在下方直接输入服务器地址")
                            .font(.system(size: 11))
                            .foregroundColor(.secondary.opacity(0.8))
                    }
                    Spacer()
                }
                .padding(10)
                .background(
                    RoundedRectangle(cornerRadius: 9, style: .continuous)
                        .fill(Color(nsColor: .windowBackgroundColor).opacity(0.7))
                )
            } else {
                VStack(spacing: 6) {
                    ForEach(store.servers) { srv in
                        ServerRow(
                            server: srv,
                            selected: selected?.id == srv.id || manualAddr == srv.addr,
                            qqBlue: qqBlue
                        )
                        .contentShape(Rectangle())
                        .onTapGesture {
                            selected = srv
                            manualAddr = srv.addr
                        }
                    }
                }
            }
        }
        .padding(12)
        .background(
            RoundedRectangle(cornerRadius: 12, style: .continuous)
                .fill(Color.primary.opacity(0.035))
        )
        .overlay(
            RoundedRectangle(cornerRadius: 12, style: .continuous)
                .stroke(Color.primary.opacity(0.06), lineWidth: 1)
        )
    }

    // MARK: - 地址与 PSK 输入区

    private var credentialsCard: some View {
        VStack(alignment: .leading, spacing: 10) {
            VStack(alignment: .leading, spacing: 5) {
                Text("服务器地址")
                    .font(.system(size: 12, weight: .medium))
                    .foregroundColor(.secondary)

                HStack(spacing: 8) {
                    Image(systemName: "network")
                        .foregroundColor(.secondary)
                        .frame(width: 16)
                    TextField("ws://127.0.0.1:19090/lctp", text: $manualAddr)
                        .textFieldStyle(.plain)
                        .font(.system(size: 13, design: .monospaced))
                }
                .padding(.horizontal, 12)
                .padding(.vertical, 9)
                .background(
                    RoundedRectangle(cornerRadius: 10, style: .continuous)
                        .fill(Color(nsColor: .windowBackgroundColor))
                )
                .overlay(
                    RoundedRectangle(cornerRadius: 10, style: .continuous)
                        .stroke(Color.primary.opacity(0.10), lineWidth: 1)
                )
            }

            VStack(alignment: .leading, spacing: 5) {
                Text("预共享口令 (PSK)")
                    .font(.system(size: 12, weight: .medium))
                    .foregroundColor(.secondary)

                HStack(spacing: 8) {
                    Image(systemName: "lock.shield")
                        .foregroundColor(.secondary)
                        .frame(width: 16)
                    SecureField("输入房间连接口令（若无鉴权可留空）", text: $psk)
                        .textFieldStyle(.plain)
                        .font(.system(size: 13))
                        .onSubmit { performLogin() }
                }
                .padding(.horizontal, 12)
                .padding(.vertical, 9)
                .background(
                    RoundedRectangle(cornerRadius: 10, style: .continuous)
                        .fill(Color(nsColor: .windowBackgroundColor))
                )
                .overlay(
                    RoundedRectangle(cornerRadius: 10, style: .continuous)
                        .stroke(Color.primary.opacity(0.10), lineWidth: 1)
                )
            }
        }
    }

    // MARK: - 立即登录按钮

    private var loginButton: some View {
        let trimmedAddr = manualAddr.trimmingCharacters(in: .whitespacesAndNewlines)
        let isConnecting = store.conn == "connecting"

        return Button {
            performLogin()
        } label: {
            HStack(spacing: 8) {
                if isConnecting {
                    ProgressView()
                        .controlSize(.small)
                        .tint(.white)
                } else {
                    Image(systemName: "arrow.right.circle.fill")
                        .font(.system(size: 15, weight: .semibold))
                }
                Text(isConnecting ? "正在登录连接…" : "立即登录")
                    .font(.system(size: 14.5, weight: .semibold))
            }
            .foregroundColor(.white)
            .frame(maxWidth: .infinity)
            .padding(.vertical, 11)
            .background(
                RoundedRectangle(cornerRadius: 11, style: .continuous)
                    .fill(
                        trimmedAddr.isEmpty
                            ? LinearGradient(colors: [Color.gray.opacity(0.5)], startPoint: .leading, endPoint: .trailing)
                            : LinearGradient(colors: [Color(red: 0.05, green: 0.66, blue: 1.0), qqBlue, qqBlueDark], startPoint: .topLeading, endPoint: .bottomTrailing)
                    )
                    .shadow(color: trimmedAddr.isEmpty ? .clear : qqBlue.opacity(0.32), radius: 8, x: 0, y: 4)
            )
        }
        .buttonStyle(.plain)
        .keyboardShortcut(.defaultAction)
        .disabled(trimmedAddr.isEmpty || isConnecting)
    }

    // MARK: - 底部状态栏

    private var footerStatus: some View {
        HStack {
            connBadge
            Spacer()
            HStack(spacing: 5) {
                Circle()
                    .fill(store.daemonConnected ? Color.green : Color.orange)
                    .frame(width: 7, height: 7)
                Text(store.daemonConnected ? "本地引擎就绪" : "本地引擎启动中")
                    .font(.system(size: 11))
                    .foregroundColor(.secondary)
            }
        }
        .padding(.top, 2)
    }

    private var connBadge: some View {
        HStack(spacing: 6) {
            Circle()
                .fill(color(for: store.conn))
                .frame(width: 8, height: 8)
            Text(label(for: store.conn))
                .font(.system(size: 11.5, weight: .medium))
                .foregroundColor(.secondary)
        }
    }

    private func triggerDiscovery() async {
        isDiscovering = true
        await store.browseServers()
        if let first = store.servers.first, manualAddr.isEmpty || manualAddr == "ws://127.0.0.1:19090/lctp" {
            selected = first
            manualAddr = first.addr
        }
        isDiscovering = false
    }

    private func performLogin() {
        let addr = manualAddr.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !addr.isEmpty else { return }
        Task {
            await store.setNickname(settings.nickname)
            await store.connect(addr: addr, psk: psk)
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
        case "connecting": return "连接中…"
        case "auth_failed": return "鉴权失败（口令错误）"
        default: return "未连接中心节点"
        }
    }
}

private struct ServerRow: View {
    let server: ServerInfo
    let selected: Bool
    let qqBlue: Color

    private var authLabel: String {
        let mode = (server.authMode ?? "psk").lowercased()
        if mode == "none" || mode == "open" {
            return "免密开放"
        }
        return "PSK 鉴权"
    }

    private var isPSK: Bool {
        let mode = (server.authMode ?? "psk").lowercased()
        return mode != "none" && mode != "open"
    }

    var body: some View {
        HStack(spacing: 10) {
            Circle()
                .fill(selected ? qqBlue : Color.secondary.opacity(0.18))
                .frame(width: 30, height: 30)
                .overlay(
                    Image(systemName: "server.rack")
                        .font(.system(size: 13, weight: .semibold))
                        .foregroundColor(selected ? .white : .secondary)
                )

            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(server.name)
                        .font(.system(size: 13, weight: .semibold))
                        .foregroundColor(.primary)
                        .lineLimit(1)

                    Text(authLabel)
                        .font(.system(size: 10, weight: .semibold))
                        .foregroundColor(isPSK ? qqBlue : .green)
                        .padding(.horizontal, 6)
                        .padding(.vertical, 1.5)
                        .background(
                            Capsule()
                                .fill((isPSK ? qqBlue : Color.green).opacity(0.14))
                        )
                }

                Text("\(server.linkBadgeText) · \(server.addr) · v\(server.version)")
                    .font(.system(size: 11, design: .monospaced))
                    .foregroundColor(.secondary)
                    .lineLimit(1)
            }

            Spacer()

            if selected {
                Image(systemName: "checkmark.circle.fill")
                    .font(.system(size: 15))
                    .foregroundColor(qqBlue)
            } else {
                Text("填入")
                    .font(.system(size: 11, weight: .medium))
                    .foregroundColor(qqBlue)
                    .padding(.horizontal, 7)
                    .padding(.vertical, 3)
                    .background(
                        Capsule()
                            .stroke(qqBlue.opacity(0.4), lineWidth: 1)
                    )
            }
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 7)
        .background(
            RoundedRectangle(cornerRadius: 9, style: .continuous)
                .fill(selected ? qqBlue.opacity(0.10) : Color(nsColor: .windowBackgroundColor))
        )
        .overlay(
            RoundedRectangle(cornerRadius: 9, style: .continuous)
                .stroke(selected ? qqBlue.opacity(0.45) : Color.clear, lineWidth: 1)
        )
    }
}
