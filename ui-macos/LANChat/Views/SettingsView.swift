import SwiftUI

/// 设置页：macOS QQ (NTQQ) 风格设置面板（个人资料、下载目录、后台守护进程与安全状态）。
struct SettingsView: View {
    @ObservedObject var store: ChatStore
    @ObservedObject var settings: SettingsStore
    @State private var customUpdateProxy: String = ""

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                // 标题头
                HStack(spacing: 10) {
                    Image(systemName: "gearshape.fill")
                        .font(.system(size: 18))
                        .foregroundColor(QQPalette.qqBlue)
                    VStack(alignment: .leading, spacing: 2) {
                        Text("偏好设置")
                            .font(.system(size: 17, weight: .bold))
                        Text("管理个人名片、文件接收目录与本地守护进程行为")
                            .font(.system(size: 12))
                            .foregroundColor(.secondary)
                    }
                    Spacer()
                }
                .padding(.bottom, 4)

                // 个人名片卡片
                VStack(alignment: .leading, spacing: 14) {
                    Text("👤 个人资料")
                        .font(.system(size: 13, weight: .bold))

                    HStack(spacing: 14) {
                        QQAvatar(
                            key: store.selfId.isEmpty ? settings.nickname : store.selfId,
                            title: settings.nickname,
                            size: 54,
                            status: "online"
                        )

                        VStack(alignment: .leading, spacing: 6) {
                            HStack(spacing: 8) {
                                TextField("显示昵称", text: $settings.nickname)
                                    .textFieldStyle(.roundedBorder)
                                    .frame(maxWidth: 240)
                                    .onSubmit {
                                        Task { await store.setNickname(settings.nickname) }
                                    }

                                Button {
                                    Task { await store.setNickname(settings.nickname) }
                                } label: {
                                    Text("应用昵称")
                                        .font(.system(size: 12.5, weight: .semibold))
                                        .foregroundColor(.white)
                                        .padding(.horizontal, 14)
                                        .padding(.vertical, 6)
                                        .background(
                                            RoundedRectangle(cornerRadius: 7, style: .continuous)
                                                .fill(QQPalette.qqBlue)
                                        )
                                }
                                .buttonStyle(.plain)
                            }

                            HStack(spacing: 12) {
                                Text("节点 ID：\(store.selfId.isEmpty ? "自动分配" : store.selfId)")
                                    .font(.system(size: 11, design: .monospaced))
                                    .foregroundColor(.secondary)
                                if !store.server.isEmpty {
                                    Text("中心节点：\(store.server)")
                                        .font(.system(size: 11, design: .monospaced))
                                        .foregroundColor(.secondary)
                                }
                            }
                        }
                        Spacer()
                    }
                }
                .padding(16)
                .background(
                    RoundedRectangle(cornerRadius: 12, style: .continuous)
                        .fill(Color(nsColor: .controlBackgroundColor))
                )
                .overlay(
                    RoundedRectangle(cornerRadius: 12, style: .continuous)
                        .stroke(Color.primary.opacity(0.07), lineWidth: 1)
                )

                // 文件接收与下载目录卡片
                VStack(alignment: .leading, spacing: 12) {
                    Text("📁 文件传输与默认下载目录")
                        .font(.system(size: 13, weight: .bold))

                    HStack(spacing: 10) {
                        Image(systemName: "folder.fill")
                            .foregroundColor(QQPalette.qqBlue)
                        Text(settings.downloadDir)
                            .font(.system(size: 12.5, design: .monospaced))
                            .lineLimit(1)
                            .truncationMode(.middle)
                        Spacer()
                        Button("在访达中打开") {
                            NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: settings.downloadDir)
                        }
                        Button("更改…") {
                            pickFolder()
                        }
                    }
                    .padding(10)
                    .background(
                        RoundedRectangle(cornerRadius: 8, style: .continuous)
                            .fill(Color(nsColor: .windowBackgroundColor))
                    )

                    Text("接收的 P2P 直连文件与频道群文件将自动保存在此目录（支持 .part + .meta 断点续传）。")
                        .font(.system(size: 11.5))
                        .foregroundColor(.secondary)
                }
                .padding(16)
                .background(
                    RoundedRectangle(cornerRadius: 12, style: .continuous)
                        .fill(Color(nsColor: .controlBackgroundColor))
                )
                .overlay(
                    RoundedRectangle(cornerRadius: 12, style: .continuous)
                        .stroke(Color.primary.opacity(0.07), lineWidth: 1)
                )

                // 后台守护进程与安全状态卡片
                VStack(alignment: .leading, spacing: 12) {
                    Text("⚙️ 后台服务与连接状态")
                        .font(.system(size: 13, weight: .bold))

                    Toggle("退出应用时同时停止后台服务 (lanchat-daemon)", isOn: $settings.stopDaemonOnExit)
                        .font(.system(size: 13))

                    Divider()

                    HStack {
                        Text("本地 IPC 守护进程连接")
                            .font(.system(size: 12.5))
                            .foregroundColor(.secondary)
                        Spacer()
                        HStack(spacing: 6) {
                            Circle()
                                .fill(store.daemonConnected ? QQPalette.onlineGreen : .red)
                                .frame(width: 8, height: 8)
                            Text(store.daemonConnected ? "已连接" : "未连接")
                                .font(.system(size: 12.5, weight: .semibold))
                                .foregroundColor(store.daemonConnected ? QQPalette.onlineGreen : .red)
                        }
                    }

                    HStack {
                        Text("数据面安全策略")
                            .font(.system(size: 12.5))
                            .foregroundColor(.secondary)
                        Spacer()
                        Text("P2P TCP 直连优先 · AES-GCM 加密中继兜底 · SHA-256 完整性校验")
                            .font(.system(size: 11.5, weight: .medium))
                            .foregroundColor(.secondary)
                    }
                }
                .padding(16)
                .background(
                    RoundedRectangle(cornerRadius: 12, style: .continuous)
                        .fill(Color(nsColor: .controlBackgroundColor))
                )
                .overlay(
                    RoundedRectangle(cornerRadius: 12, style: .continuous)
                        .stroke(Color.primary.opacity(0.07), lineWidth: 1)
                )

                // 版本更新与国内代理静默下载卡片
                VStack(alignment: .leading, spacing: 12) {
                    Text("🚀 版本更新（国内代理静默下载 · 免浏览器）")
                        .font(.system(size: 13, weight: .bold))

                    Text(store.updateStatus)
                        .font(.system(size: 12))
                        .foregroundColor(.secondary)

                    HStack(spacing: 10) {
                        TextField("可选自定义加速代理前缀（留空默认自动轮询 ghfast.top / gh-proxy.com 等国内节点）", text: $customUpdateProxy)
                            .textFieldStyle(.roundedBorder)

                        Button("🔍 检查版本") {
                            Task { await store.checkAndSilentUpdate(customProxy: customUpdateProxy, checkOnly: true) }
                        }

                        Button {
                            Task { await store.checkAndSilentUpdate(customProxy: customUpdateProxy, checkOnly: false) }
                        } label: {
                            Text("⬇️ 静默下载更新包")
                                .font(.system(size: 12.5, weight: .semibold))
                                .foregroundColor(.white)
                                .padding(.horizontal, 14)
                                .padding(.vertical, 6)
                                .background(
                                    RoundedRectangle(cornerRadius: 7, style: .continuous)
                                        .fill(QQPalette.qqBlue)
                                )
                        }
                        .buttonStyle(.plain)
                    }
                }
                .padding(16)
                .background(
                    RoundedRectangle(cornerRadius: 12, style: .continuous)
                        .fill(Color(nsColor: .controlBackgroundColor))
                )
                .overlay(
                    RoundedRectangle(cornerRadius: 12, style: .continuous)
                        .stroke(Color.primary.opacity(0.07), lineWidth: 1)
                )
            }
            .padding(24)
            .frame(maxWidth: 680)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .background(Color(nsColor: .windowBackgroundColor))
    }

    private func pickFolder() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = false
        panel.canChooseDirectories = true
        panel.allowsMultipleSelection = false
        guard panel.runModal() == .OK, let url = panel.url else { return }
        Task { await store.pickDownloadDir(url.path) }
    }
}
