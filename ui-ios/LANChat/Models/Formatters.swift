import Foundation

/// 字节 / 速率格式化（二进制单位）。
enum Formatters {
    private static let byteFormatter: ByteCountFormatter = {
        let f = ByteCountFormatter()
        f.countStyle = .binary
        f.allowedUnits = [.useKB, .useMB, .useGB, .useTB]
        return f
    }()

    static func size(_ bytes: Int64) -> String {
        byteFormatter.string(fromByteCount: bytes)
    }

    static func speed(_ bps: Int64) -> String {
        size(bps) + "/s"
    }
}
