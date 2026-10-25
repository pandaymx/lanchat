import SwiftUI

/// iOS 手机 QQ 风格登录 / 节点连接界面。
struct LoginView: View {
    @EnvironmentObject private var store: AppStore
    @EnvironmentObject private var settings: SettingsStore

    @State private var manualAddr = ""
    @State private var psk = ""
    @State private var isBrowsing = false

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(spacing: 18) {
                    // 1. 顶部手机 QQ 渐变 Hero Header（#0099FF -> #0072FF）
                    heroHeader

                    VStack(spacing: 16) {
                        // 2. iOS 诚实能力边界横幅
                        capabilityBoundaryBanner

                        // 3. 局域网自动发现卡片
                        discoveryCard

                        // 4. 登录表单卡片（本机昵称 + 服务器地址 + PSK 口令 + 立即登录）
                        loginFormCard
                    }
                    .padding(.horizontal, 16)
                    .padding(.bottom, 28)
                }
            }
            .background(Color(.systemGroupedBackground).ignoresSafeArea())
            .ignoresSafeArea(edges: .top)
            .refreshable {
                await store.browseServers()
            }
        }
    }

    // MARK: - 顶部 Hero 区域

    private var heroHeader: some View {
        VStack(spacing: 12) {
            Spacer().frame(height: 36)

            // 80x80 圆形头像徽章 + 在线绿点
            ZStack(alignment: .bottomTrailing) {
                Circle()
                    .fill(Color.white.opacity(0.22))
                    .frame(width: 86, height: 86)

                Circle()
                    .fill(Color.white)
                    .frame(width: 80, height: 80)
                    .shadow(color: Color.black.opacity(0.15), radius: 8, x: 0, y: 4)
                    .overlay(
                        VStack(spacing: 2) {
                            Image(systemName: "bubble.left.and.bubble.right.fill")
                                .font(.system(size: 28, weight: .bold))
                                .foregroundStyle(QQTheme.qqBlue)
                            Text(QQTheme.initial(for: settings.nickname))
                                .font(.system(size: 13, weight: .heavy))
                                .foregroundStyle(QQTheme.qqDarkBlue)
                        }
                    )

                Circle()
                    .fill(QQTheme.onlineGreen)
                    .frame(width: 20, height: 20)
                    .overlay(Circle().stroke(Color.white, lineWidth: 3))
                    .offset(x: -2, y: -2)
            }

            Text("LANChat 局域网即时通信")
                .font(.system(size: 22, weight: .bold))
                .foregroundStyle(.white)

            Text("P2P 高速直连 · 端到端加密")
                .font(.subheadline)
                .foregroundStyle(.white.opacity(0.9))

            // 当前连接状态胶囊
            HStack(spacing: 6) {
                statusDot
                Text(statusText)
                    .font(.caption.weight(.medium))
                    .foregroundStyle(.white)
                if store.busy {
                    ProgressView()
                        .tint(.white)
                        .scaleEffect(0.75)
                }
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 5)
            .background(Color.white.opacity(0.18), in: Capsule())
            .padding(.bottom, 18)
        }
        .frame(maxWidth: .infinity)
        .background(
            QQTheme.headerGradient
                .clipShape(RoundedRectangle(cornerRadius: 28, style: .continuous))
        )
    }

    // MARK: - iOS 能力边界提示横幅

    private var capabilityBoundaryBanner: some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: "info.circle.fill")
                .font(.title3)
                .foregroundStyle(QQTheme.qqBlue)

            VStack(alignment: .leading, spacing: 3) {
                Text("前台控制端 + 中小文件传输（单文件上限 1 GiB）")
                    .font(.footnote.weight(.semibold))
                    .foregroundStyle(.primary)
                Text("iOS 端仅在前台保持 P2P 传输，不支持后台做种；超过 1 GiB 文件发送前将主动提醒。")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }
            Spacer(minLength: 0)
        }
        .padding(12)
        .background(
            RoundedRectangle(cornerRadius: 16, style: .continuous)
                .fill(QQTheme.qqBlue.opacity(0.10))
                .overlay(
                    RoundedRectangle(cornerRadius: 16, style: .continuous)
                        .stroke(QQTheme.qqBlue.opacity(0.25), lineWidth: 1)
                )
        )
    }

    // MARK: - 局域网自动发现卡片

    private var discoveryCard: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Label("发现的中心节点", systemImage: "antenna.radiowaves.left.and.right")
                    .font(.subheadline.weight(.semibold))
                    .foregroundStyle(.primary)

                Spacer()

                Button {
                    isBrowsing = true
                    Task {
                        await store.browseServers()
                        isBrowsing = false
                    }
                } label: {
                    HStack(spacing: 4) {
                        if isBrowsing {
                            ProgressView()
                                .scaleEffect(0.7)
                        }
                        Text("📡 局域网自动发现")
                            .font(.caption.weight(.semibold))
                    }
                    .foregroundStyle(QQTheme.qqBlue)
                    .padding(.horizontal, 10)
                    .padding(.vertical, 6)
                    .background(QQTheme.qqBlue.opacity(0.12), in: Capsule())
                }
            }

            if store.discoveredServers.isEmpty {
                HStack(spacing: 8) {
                    Image(systemName: "wifi.exclamationmark")
                        .foregroundStyle(.secondary)
                    Text("暂未发现局域网节点，可点击「📡 局域网自动发现」或下方手动输入地址")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                .padding(.vertical, 8)
            } else {
                VStack(spacing: 8) {
                    ForEach(store.discoveredServers) { srv in
                        Button {
                            manualAddr = srv.addr
                        } label: {
                            HStack(spacing: 10) {
                                Circle()
                                    .fill(QQTheme.qqBlue.opacity(0.15))
                                    .frame(width: 38, height: 38)
                                    .overlay(
                                        Image(systemName: "server.rack")
                                            .font(.subheadline.weight(.semibold))
                                            .foregroundStyle(QQTheme.qqBlue)
                                    )

                                VStack(alignment: .leading, spacing: 2) {
                                    HStack(spacing: 6) {
                                        Text(srv.name)
                                            .font(.subheadline.weight(.semibold))
                                            .foregroundStyle(.primary)
                                        if !srv.version.isEmpty {
                                            Text("v\(srv.version)")
                                                .font(.caption2)
                                                .foregroundStyle(.secondary)
                                        }
                                    }
                                    Text(srv.addr)
                                        .font(.caption.monospaced())
                                        .foregroundStyle(.secondary)
                                }

                                Spacer()

                                Text(srv.authMode.uppercased())
                                    .font(.caption2.weight(.semibold))
                                    .foregroundStyle(QQTheme.qqBlue)
                                    .padding(.horizontal, 8)
                                    .padding(.vertical, 3)
                                    .background(QQTheme.qqBlue.opacity(0.12), in: Capsule())

                                if manualAddr == srv.addr {
                                    Image(systemName: "checkmark.circle.fill")
                                        .foregroundStyle(QQTheme.qqBlue)
                                }
                            }
                            .padding(10)
                            .background(
                                RoundedRectangle(cornerRadius: 12, style: .continuous)
                                    .fill(manualAddr == srv.addr
                                          ? QQTheme.qqBlue.opacity(0.08)
                                          : Color(.tertiarySystemGroupedBackground))
                            )
                        }
                        .buttonStyle(.plain)
                    }
                }
            }
        }
        .padding(16)
        .background(
            RoundedRectangle(cornerRadius: 20, style: .continuous)
                .fill(Color(.secondarySystemGroupedBackground))
                .shadow(color: Color.black.opacity(0.04), radius: 8, x: 0, y: 2)
        )
    }

    // MARK: - 登录表单卡片

    private var loginFormCard: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("账号与节点连接")
                .font(.subheadline.weight(.semibold))
                .foregroundStyle(.primary)

            // 本机昵称设置
            HStack(spacing: 10) {
                Image(systemName: "person.fill")
                    .foregroundStyle(QQTheme.qqBlue)
                    .frame(width: 22)
                TextField("本机昵称", text: $settings.nickname)
                    .autocorrectionDisabled()
                Button("应用昵称") {
                    store.applyNickname()
                }
                .font(.caption.weight(.semibold))
                .foregroundStyle(QQTheme.qqBlue)
                .padding(.horizontal, 10)
                .padding(.vertical, 5)
                .background(QQTheme.qqBlue.opacity(0.12), in: Capsule())
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 10)
            .background(Color(.tertiarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 14))

            // 服务器地址输入
            HStack(spacing: 10) {
                Image(systemName: "network")
                    .foregroundStyle(QQTheme.qqBlue)
                    .frame(width: 22)
                TextField("服务器地址（如 192.168.1.10:8080）", text: $manualAddr)
                    .keyboardType(.numbersAndPunctuation)
                    .autocapitalization(.none)
                    .disableAutocorrection(true)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 12)
            .background(Color(.tertiarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 14))

            // PSK 预共享口令输入
            HStack(spacing: 10) {
                Image(systemName: "lock.fill")
                    .foregroundStyle(QQTheme.qqBlue)
                    .frame(width: 22)
                SecureField("口令 PSK（默认鉴权）", text: $psk)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 12)
            .background(Color(.tertiarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 14))

            // 全宽胶囊 QQ 蓝「立即登录」按钮
            Button {
                store.connect(addr: manualAddr, psk: psk)
            } label: {
                HStack(spacing: 8) {
                    if store.busy {
                        ProgressView()
                            .tint(.white)
                    } else {
                        Image(systemName: "arrow.right.circle.fill")
                    }
                    Text("立即登录")
                        .font(.system(size: 17, weight: .bold))
                }
                .foregroundStyle(.white)
                .frame(maxWidth: .infinity)
                .padding(.vertical, 14)
                .background(QQTheme.headerGradient, in: Capsule())
                .shadow(color: QQTheme.qqBlue.opacity(0.35), radius: 8, x: 0, y: 4)
            }
            .disabled(store.busy)
            .padding(.top, 4)
        }
        .padding(16)
        .background(
            RoundedRectangle(cornerRadius: 20, style: .continuous)
                .fill(Color(.secondarySystemGroupedBackground))
                .shadow(color: Color.black.opacity(0.04), radius: 8, x: 0, y: 2)
        )
    }

    // MARK: - 状态辅助

    private var statusDot: some View {
        let color: Color
        switch store.conn {
        case "connected": color = QQTheme.onlineGreen
        case "connecting": color = .orange
        case "auth_failed": color = .red
        default: color = .white.opacity(0.75)
        }
        return Circle().fill(color).frame(width: 9, height: 9)
    }

    private var statusText: String {
        switch store.conn {
        case "connected": return "已连接"
        case "connecting": return "连接中…"
        case "auth_failed": return "鉴权失败"
        default: return "未连接服务器"
        }
    }
}
