import SwiftUI

/// 设置界面（手机 QQ 个人中心 / 设置卡片风格）。
struct SettingsView: View {
    @EnvironmentObject private var store: AppStore
    @EnvironmentObject private var settings: SettingsStore
    @State private var searchText = ""
    @State private var customUpdateProxy = ""

    var body: some View {
        NavigationStack {
            VStack(spacing: 0) {
                // 手机 QQ 顶部个人头像 + 搜索栏
                QQTopHeaderView(searchText: $searchText)

                ScrollView {
                    VStack(spacing: 14) {
                        // 1. 个人资料与本机昵称卡片
                        profileCard

                        // 2. 跨设备剪贴板与全局免打扰卡片
                        preferencesCard

                        // 3. 节点与连接状态卡片
                        connectionCard

                        // 3. 沙盒下载目录卡片
                        downloadDirCard

                        // 4. 版本更新与国内代理静默下载卡片
                        updateCard

                        // 5. iOS 诚实能力边界说明卡片
                        boundaryCard
                    }
                    .padding(.horizontal, 14)
                    .padding(.vertical, 14)
                }
                .background(Color(.systemGroupedBackground))
            }
            .navigationBarHidden(true)
        }
    }

    private var profileCard: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack(spacing: 14) {
                ZStack(alignment: .bottomTrailing) {
                    Circle()
                        .fill(QQTheme.headerGradient)
                        .frame(width: 58, height: 58)
                        .overlay(
                            Text(QQTheme.initial(for: settings.nickname))
                                .font(.system(size: 24, weight: .bold))
                                .foregroundStyle(.white)
                        )

                    Circle()
                        .fill(store.conn == "connected" ? QQTheme.onlineGreen : Color.gray)
                        .frame(width: 15, height: 15)
                        .overlay(Circle().stroke(Color(.secondarySystemGroupedBackground), lineWidth: 2.5))
                }

                VStack(alignment: .leading, spacing: 4) {
                    Text(settings.nickname)
                        .font(.system(size: 18, weight: .bold))
                        .foregroundStyle(.primary)
                    Text("设备 ID：\(store.selfId.isEmpty ? "分配中…" : store.selfId)")
                        .font(.caption.monospaced())
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }

                Spacer()
            }

            Divider()

            HStack(spacing: 10) {
                Image(systemName: "pencil.line")
                    .foregroundStyle(QQTheme.qqBlue)
                TextField("修改本机昵称", text: $settings.nickname)
                    .autocorrectionDisabled()
                Button {
                    store.applyNickname()
                } label: {
                    Text("应用昵称")
                        .font(.caption.weight(.bold))
                        .foregroundStyle(.white)
                        .padding(.horizontal, 14)
                        .padding(.vertical, 7)
                        .background(QQTheme.qqBlue, in: Capsule())
                }
            }
        }
        .padding(16)
        .background(
            RoundedRectangle(cornerRadius: 18, style: .continuous)
                .fill(Color(.secondarySystemGroupedBackground))
        )
    }

    private var preferencesCard: some View {
        VStack(alignment: .leading, spacing: 12) {
            Label("会话同步与免打扰设置", systemImage: "slider.horizontal.3")
                .font(.subheadline.weight(.bold))
                .foregroundStyle(.primary)

            Toggle(isOn: Binding(
                get: { store.autoClipboard },
                set: { store.setAutoClipboard($0) }
            )) {
                Text("📋 跨设备剪贴板自动同步")
                    .font(.footnote.weight(.medium))
            }
            .tint(QQTheme.qqBlue)

            Toggle(isOn: Binding(
                get: { store.dndEnabled },
                set: { store.setDND($0) }
            )) {
                Text("🔕 全局免打扰模式 (DND)")
                    .font(.footnote.weight(.medium))
            }
            .tint(QQTheme.qqBlue)
        }
        .padding(16)
        .background(
            RoundedRectangle(cornerRadius: 18, style: .continuous)
                .fill(Color(.secondarySystemGroupedBackground))
        )
    }

    private var connectionCard: some View {
        VStack(alignment: .leading, spacing: 10) {
            Label("当前信令与加密状态", systemImage: "shield.lefthalf.filled")
                .font(.subheadline.weight(.bold))
                .foregroundStyle(.primary)

            HStack(spacing: 8) {
                Circle()
                    .fill(store.conn == "connected" ? QQTheme.onlineGreen : Color.gray)
                    .frame(width: 10, height: 10)
                Text(store.conn == "connected" ? "已连接中心节点" : "未连接服务器")
                    .font(.footnote.weight(.medium))
                Spacer()
                Text(store.server.isEmpty ? "未连接" : store.server)
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
            }

            HStack(spacing: 8) {
                Text("直连优先")
                    .font(.caption2.weight(.semibold))
                    .foregroundStyle(QQTheme.onlineGreen)
                    .padding(.horizontal, 8)
                    .padding(.vertical, 3)
                    .background(QQTheme.onlineGreen.opacity(0.12), in: Capsule())

                Text("AES-GCM 加密中继兜底")
                    .font(.caption2.weight(.semibold))
                    .foregroundStyle(QQTheme.qqBlue)
                    .padding(.horizontal, 8)
                    .padding(.vertical, 3)
                    .background(QQTheme.qqBlue.opacity(0.12), in: Capsule())
            }
        }
        .padding(16)
        .background(
            RoundedRectangle(cornerRadius: 18, style: .continuous)
                .fill(Color(.secondarySystemGroupedBackground))
        )
    }

    private var downloadDirCard: some View {
        VStack(alignment: .leading, spacing: 8) {
            Label("下载目录（App 沙盒）", systemImage: "folder.fill")
                .font(.subheadline.weight(.bold))
                .foregroundStyle(.primary)

            Text(settings.downloadDir)
                .font(.caption.monospaced())
                .foregroundStyle(.secondary)
                .padding(10)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Color(.tertiarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 10))

            Text("iOS 版接收的文件统一保存在 App 沙盒 Documents/LANChat 内，可在系统「文件」App 中直接查看与分享。")
                .font(.caption2)
                .foregroundStyle(.secondary)
        }
        .padding(16)
        .background(
            RoundedRectangle(cornerRadius: 18, style: .continuous)
                .fill(Color(.secondarySystemGroupedBackground))
        )
    }

    private var updateCard: some View {
        VStack(alignment: .leading, spacing: 10) {
            Label("版本更新（国内代理静默下载 · 免浏览器）", systemImage: "arrow.down.circle.fill")
                .font(.subheadline.weight(.bold))
                .foregroundStyle(.primary)

            Text(store.updateStatus)
                .font(.caption)
                .foregroundStyle(.secondary)

            TextField("可选自定义加速代理前缀（留空默认轮询国内节点）", text: $customUpdateProxy)
                .font(.caption)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .padding(8)
                .background(Color(.tertiarySystemGroupedBackground), in: RoundedRectangle(cornerRadius: 8))

            HStack(spacing: 10) {
                Button {
                    store.checkAndSilentUpdate(customProxy: customUpdateProxy, checkOnly: true)
                } label: {
                    Text("🔍 检查新版本")
                        .font(.caption.weight(.semibold))
                        .foregroundStyle(QQTheme.qqBlue)
                        .padding(.horizontal, 12)
                        .padding(.vertical, 7)
                        .background(QQTheme.qqBlue.opacity(0.12), in: Capsule())
                }

                Button {
                    store.checkAndSilentUpdate(customProxy: customUpdateProxy, checkOnly: false)
                } label: {
                    Text("⬇️ 静默下载更新包")
                        .font(.caption.weight(.bold))
                        .foregroundStyle(.white)
                        .padding(.horizontal, 14)
                        .padding(.vertical, 7)
                        .background(QQTheme.qqBlue, in: Capsule())
                }
            }
        }
        .padding(16)
        .background(
            RoundedRectangle(cornerRadius: 18, style: .continuous)
                .fill(Color(.secondarySystemGroupedBackground))
        )
    }

    private var boundaryCard: some View {
        VStack(alignment: .leading, spacing: 10) {
            Label("iOS 诚实能力边界", systemImage: "sun.max.trianglebadge.exclamationmark")
                .font(.subheadline.weight(.bold))
                .foregroundStyle(.orange)

            HStack(alignment: .top, spacing: 8) {
                Image(systemName: "sun.max.fill")
                    .foregroundStyle(QQTheme.qqBlue)
                    .font(.footnote)
                Text("前台控制端 + 中小文件传输（单文件上限 1 GiB）：仅在前台传输，切后台将由 iOS 系统挂起，不支持后台做种。")
                    .font(.footnote)
                    .foregroundStyle(.primary)
            }

            HStack(alignment: .top, spacing: 8) {
                Image(systemName: "exclamationmark.triangle.fill")
                    .foregroundStyle(.orange)
                    .font(.footnote)
                Text("超过 1 GiB 的文件发送前会主动弹窗提示，建议大文件通过桌面端（Windows / Linux / macOS）发起。")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
        }
        .padding(16)
        .background(
            RoundedRectangle(cornerRadius: 18, style: .continuous)
                .fill(Color(.secondarySystemGroupedBackground))
        )
    }
}
