import Foundation

enum Formatters {
    static func size(_ bytes: Int64) -> String {
        byteFormatter.string(fromByteCount: bytes)
    }

    static func speed(_ bps: Int64) -> String {
        size(bps) + "/s"
    }

    private static let byteFormatter: ByteCountFormatter = {
        let f = ByteCountFormatter()
        f.allowedUnits = [.useKB, .useMB, .useGB, .useTB]
        f.countStyle = .binary
        return f
    }()
}
