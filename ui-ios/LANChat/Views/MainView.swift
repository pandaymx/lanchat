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

    static func isSystemDiagnosticText(_ raw: String) -> Bool {
        let t = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        return t.hasPrefix("[安全码指纹]") ||
            t.hasPrefix("🛡️ [安全码指纹核验]") ||
            t.hasPrefix("📊 [诊断状态]") ||
            t.hasPrefix("🚀 [版本更新]") ||
            t.hasPrefix("⚡ [带宽限速已设置为") ||
            t.hasPrefix("[已开启端到端加密]") ||
            t.hasPrefix("[已关闭端到端加密]") ||
            t.hasPrefix("[搜索“")
    }

    static func isSystemDiagnosticMessage(id: String, text: String) -> Bool {
        id.hasPrefix("fp-") || id.hasPrefix("status-") || id.hasPrefix("update-") ||
            isSystemDiagnosticText(text)
    }

    private static func isReactionEmojiToken(_ token: String) -> Bool {
        if token.isEmpty || token.utf8.count > 24 { return false }
        for scalar in token.unicodeScalars {
            let v = scalar.value
            if (v >= 0x30 && v <= 0x39) || (v >= 0x41 && v <= 0x5A) || (v >= 0x61 && v <= 0x7A) ||
                (v >= 0x4E00 && v <= 0x9FFF) || v == 0x3A || v == 0x22 || v == 0x20 {
                return false
            }
        }
        return true
    }

    private static func extractTrailingReactions(_ raw: String) -> (body: String, reactions: [String]) {
        var body = raw
        if body.hasPrefix("[表情] ") {
            body = String(body.dropFirst("[表情] ".count))
        }
        var reversed: [String] = []
        while body.hasSuffix("]"), let range = body.range(of: " [", options: .backwards) {
            let start = range.upperBound
            let end = body.index(before: body.endIndex)
            let token = String(body[start..<end])
            if !isReactionEmojiToken(token) {
                break
            }
            reversed.append(token)
            body = String(body[..<range.lowerBound])
        }
        return (body, reversed.reversed())
    }

    /// 拆分消息正文与尾部表情回应徽章（如 `"你好 [👍]"` -> `("你好", ["👍"])`），
    /// 供 UI 将点赞/表情回应渲染在聊天气泡下方而非气泡内部，并自动剥离旧版引用块中的诊断与回应尾巴。
    static func splitReactions(_ raw: String) -> (body: String, reactions: [String]) {
        let extracted = extractTrailingReactions(raw)
        var body = extracted.body
        if body.hasPrefix("「"), let quoteEnd = body.range(of: "」\n") {
            let qStart = body.index(after: body.startIndex)
            let quotedRaw = String(body[qStart..<quoteEnd.lowerBound])
            let replyBody = String(body[quoteEnd.upperBound...])
            let cleanQuoted = extractTrailingReactions(quotedRaw).body.trimmingCharacters(in: .whitespacesAndNewlines)
            if cleanQuoted.isEmpty || isSystemDiagnosticText(cleanQuoted) {
                body = replyBody
            } else {
                body = "「\(cleanQuoted)」\n\(replyBody)"
            }
        }
        return (body, extracted.reactions)
    }

    /// 拆分引用回复头与实际消息正文（如 `"「原消息」\n回复内容"` -> `("原消息", "回复内容")`）。
    static func splitQuote(_ cleanBody: String) -> (quote: String?, body: String) {
        let trimmed = cleanBody.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.hasPrefix("「"), let quoteEnd = trimmed.range(of: "」\n") {
            let qStart = trimmed.index(after: trimmed.startIndex)
            let quote = String(trimmed[qStart..<quoteEnd.lowerBound]).trimmingCharacters(in: .whitespacesAndNewlines)
            let rest = String(trimmed[quoteEnd.upperBound...])
            if !quote.isEmpty {
                return (quote, rest)
            }
        }
        return (nil, cleanBody)
    }

    /// 从可能包含引用前缀与表情回应后缀的消息文本中提取可重新编辑的纯正文。
    static func extractEditableOriginal(_ raw: String) -> String {
        let noReacts = splitReactions(raw).body
        let (_, body) = splitQuote(noReacts)
        let trimmed = body.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed == "消息已撤回" || trimmed == "[消息已撤回]" || trimmed == "[已撤回]" || trimmed.hasPrefix("data:image/") {
            return ""
        }
        return trimmed
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
