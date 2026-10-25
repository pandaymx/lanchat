import SwiftUI

// MARK: - iOS 手机 QQ 视觉主题与公共组件

enum QQTheme {
    /// 手机 QQ 品牌主色 #0099FF
    static let qqBlue = Color(red: 0.0 / 255.0, green: 153.0 / 255.0, blue: 255.0 / 255.0)
    /// 手机 QQ 渐变深蓝 #0072FF
    static let qqDarkBlue = Color(red: 0.0 / 255.0, green: 114.0 / 255.0, blue: 255.0 / 255.0)
    /// 在线绿点 #22C55E
    static let onlineGreen = Color(red: 34.0 / 255.0, green: 197.0 / 255.0, blue: 94.0 / 255.0)
    /// 离开橙点
    static let awayOrange = Color(red: 245.0 / 255.0, green: 158.0 / 255.0, blue: 11.0 / 255.0)
    /// 未读红点 #FF3B30
    static let unreadRed = Color(red: 255.0 / 255.0, green: 59.0 / 255.0, blue: 48.0 / 255.0)

    /// 顶部渐变（#0099FF -> #0072FF）
    static var headerGradient: LinearGradient {
        LinearGradient(
            colors: [qqBlue, qqDarkBlue],
            startPoint: .topLeading,
            endPoint: .bottomTrailing
        )
    }

    /// 联系人多彩头像调色板
    private static let avatarPalette: [Color] = [
        Color(red: 0.0 / 255.0, green: 153.0 / 255.0, blue: 255.0 / 255.0),
        Color(red: 99.0 / 255.0, green: 102.0 / 255.0, blue: 241.0 / 255.0),
        Color(red: 16.0 / 255.0, green: 185.0 / 255.0, blue: 129.0 / 255.0),
        Color(red: 245.0 / 255.0, green: 158.0 / 255.0, blue: 11.0 / 255.0),
        Color(red: 236.0 / 255.0, green: 72.0 / 255.0, blue: 153.0 / 255.0),
        Color(red: 139.0 / 255.0, green: 92.0 / 255.0, blue: 246.0 / 255.0),
        Color(red: 20.0 / 255.0, green: 184.0 / 255.0, blue: 166.0 / 255.0)
    ]

    static func avatarColor(for key: String) -> Color {
        guard !key.isEmpty else { return qqBlue }
        let hash = key.unicodeScalars.reduce(0) { ($0 &* 31) &+ Int($1.value) }
        let idx = abs(hash) % avatarPalette.count
        return avatarPalette[idx]
    }

    static func initial(for name: String) -> String {
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let first = trimmed.first else { return "Q" }
        return String(first).uppercased()
    }

    private static let timeFormatter: DateFormatter = {
        let f = DateFormatter()
        f.dateFormat = "HH:mm"
        return f
    }()

    static func formatTime(_ date: Date) -> String {
        timeFormatter.string(from: date)
    }

    /// 预设 QQ 表情贴纸（点击可直接触发 sendSticker 或插入表情）
    struct StickerItem: Identifiable {
        let id: String
        let emoji: String
        let title: String
    }

    static let presetStickers: [StickerItem] = [
        StickerItem(id: "qq_zan", emoji: "👍", title: "点赞"),
        StickerItem(id: "qq_haha", emoji: "😂", title: "笑哭"),
        StickerItem(id: "qq_heart", emoji: "❤️", title: "比心"),
        StickerItem(id: "qq_fire", emoji: "🔥", title: "火热"),
        StickerItem(id: "qq_party", emoji: "🎉", title: "庆祝"),
        StickerItem(id: "qq_rocket", emoji: "🚀", title: "起飞"),
        StickerItem(id: "qq_ok", emoji: "👌", title: "收到"),
        StickerItem(id: "qq_cool", emoji: "😎", title: "酷炫")
    ]

    /// 生成内置贴纸临时 PNG 文件路径，供 gomobile sendSticker 直接发送
    static func materializeStickerFile(item: StickerItem) -> String {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent("lanchat_stickers", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let fileURL = dir.appendingPathComponent("\(item.id).png")
        if !FileManager.default.fileExists(atPath: fileURL.path) {
            // 1x1 合法透明 PNG 字节流
            let pngBytes: [UInt8] = [
                0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
                0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
                0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
                0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4,
                0x89, 0x00, 0x00, 0x00, 0x0A, 0x49, 0x44, 0x41,
                0x54, 0x78, 0x9C, 0x63, 0x00, 0x01, 0x00, 0x00,
                0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00,
                0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE,
                0x42, 0x60, 0x82
            ]
            try? Data(pngBytes).write(to: fileURL)
        }
        return fileURL.path
    }
}

/// iOS 手机 QQ 顶部个人头像 + 在线状态 + 圆角搜索栏头部组件。
struct QQTopHeaderView: View {
    @EnvironmentObject private var store: AppStore
    @EnvironmentObject private var settings: SettingsStore
    @Binding var searchText: String
    var trailingActionIcon: String? = nil
    var onTrailingAction: (() -> Void)? = nil

    private var isConnected: Bool {
        store.conn == "connected"
    }

    var body: some View {
        VStack(spacing: 10) {
            // 顶部个人信息栏
            HStack(spacing: 12) {
                // 38x38 #0099FF 圆形头像 + 在线绿点
                ZStack(alignment: .bottomTrailing) {
                    Circle()
                        .fill(Color.white.opacity(0.24))
                        .frame(width: 38, height: 38)
                        .overlay(
                            Circle()
                                .fill(QQTheme.qqBlue)
                                .padding(2)
                        )
                        .overlay(
                            Text(QQTheme.initial(for: settings.nickname))
                                .font(.system(size: 16, weight: .bold))
                                .foregroundStyle(.white)
                        )

                    Circle()
                        .fill(isConnected ? QQTheme.onlineGreen : Color.orange)
                        .frame(width: 11, height: 11)
                        .overlay(Circle().stroke(Color.white, lineWidth: 1.8))
                }

                VStack(alignment: .leading, spacing: 2) {
                    Text(settings.nickname)
                        .font(.system(size: 16, weight: .bold))
                        .foregroundStyle(.white)
                        .lineLimit(1)

                    HStack(spacing: 5) {
                        Circle()
                            .fill(isConnected ? QQTheme.onlineGreen : Color.orange)
                            .frame(width: 6, height: 6)
                        Text(isConnected ? "在线 · \(store.server.isEmpty ? "局域网直连" : store.server)" : "连接中")
                            .font(.caption2)
                            .foregroundStyle(.white.opacity(0.92))
                            .lineLimit(1)
                    }
                    .padding(.horizontal, 7)
                    .padding(.vertical, 2)
                    .background(Color.white.opacity(0.18), in: Capsule())
                }

                Spacer()

                if let icon = trailingActionIcon, let action = onTrailingAction {
                    Button(action: action) {
                        Image(systemName: icon)
                            .font(.system(size: 17, weight: .semibold))
                            .foregroundStyle(.white)
                            .frame(width: 34, height: 34)
                            .background(Color.white.opacity(0.18), in: Circle())
                    }
                }
            }

            // 圆角胶囊搜索栏
            HStack(spacing: 8) {
                Text("🔍")
                    .font(.footnote)
                TextField("🔍 搜索联系人、频道或聊天记录", text: $searchText)
                    .font(.footnote)
                    .foregroundStyle(.primary)
                    .autocorrectionDisabled()
                if !searchText.isEmpty {
                    Button {
                        searchText = ""
                    } label: {
                        Image(systemName: "xmark.circle.fill")
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                    }
                }
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
            .background(Color(.systemBackground).opacity(0.95), in: Capsule())
        }
        .padding(.horizontal, 16)
        .padding(.top, 8)
        .padding(.bottom, 12)
        .background(QQTheme.headerGradient)
    }
}

/// 主界面：手机 QQ 底部四标签（消息/联系人、频道、传输、设置）。
struct MainView: View {
    @EnvironmentObject private var store: AppStore
    @State private var selectedTab: Int = 0

    private var activeTransferCount: Int {
        store.transfers.filter { $0.isActive || $0.isPendingIncoming }.count
    }

    var body: some View {
        TabView(selection: $selectedTab) {
            PeersView()
                .tabItem {
                    Label("消息/联系人", systemImage: "message.fill")
                }
                .tag(0)

            ChannelsView()
                .tabItem {
                    Label("频道", systemImage: "number.square.fill")
                }
                .tag(1)

            TransfersView()
                .tabItem {
                    Label("传输", systemImage: "arrow.up.arrow.down.circle.fill")
                }
                .badge(activeTransferCount > 0 ? activeTransferCount : 0)
                .tag(2)

            SettingsView()
                .tabItem {
                    Label("设置", systemImage: "gearshape.fill")
                }
                .tag(3)
        }
        .tint(QQTheme.qqBlue)
    }
}
