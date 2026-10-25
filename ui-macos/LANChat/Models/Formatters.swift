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
}
