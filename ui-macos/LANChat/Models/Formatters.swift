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
}
