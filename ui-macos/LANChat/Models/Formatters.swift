import Foundation

enum Formatters {
    static func size(_ bytes: Int64) -> String {
        byteFormatter.string(fromByteCount: bytes)
    }

    static func speed(_ bps: Int64) -> String {
        size(bps) + "/s"
    }

    static func time(_ date: Date) -> String {
        let cal = Calendar.current
        if cal.isDateInToday(date) {
            return todayTimeFormatter.string(from: date)
        }
        return dateTimeFormatter.string(from: date)
    }

    static func sessionTime(_ date: Date) -> String {
        let cal = Calendar.current
        if cal.isDateInToday(date) {
            return shortTimeFormatter.string(from: date)
        }
        return shortDateFormatter.string(from: date)
    }

    private static let byteFormatter: ByteCountFormatter = {
        let f = ByteCountFormatter()
        f.allowedUnits = [.useKB, .useMB, .useGB, .useTB]
        f.countStyle = .binary
        return f
    }()

    private static let todayTimeFormatter: DateFormatter = {
        let f = DateFormatter()
        f.dateFormat = "HH:mm:ss"
        return f
    }()

    private static let dateTimeFormatter: DateFormatter = {
        let f = DateFormatter()
        f.dateFormat = "MM-dd HH:mm"
        return f
    }()

    private static let shortTimeFormatter: DateFormatter = {
        let f = DateFormatter()
        f.dateFormat = "HH:mm"
        return f
    }()

    private static let shortDateFormatter: DateFormatter = {
        let f = DateFormatter()
        f.dateFormat = "MM/dd"
        return f
    }()

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

    /// 拆分消息正文与尾部表情回应徽章（如 `"你好 [👍]"` -> `("你好", ["👍"])`），
    /// 供 UI 将点赞/表情回应渲染在聊天气泡下方而非气泡内部。
    static func splitReactions(_ raw: String) -> (body: String, reactions: [String]) {
        var body = raw
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
}
