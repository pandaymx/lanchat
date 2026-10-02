import Foundation

/// 轻量偏好：昵称、默认下载目录、退出时是否停止 daemon。
final class SettingsStore: ObservableObject {
    @Published var nickname: String {
        didSet { defaults.set(nickname, forKey: Keys.nickname) }
    }
    @Published var downloadDir: String {
        didSet { defaults.set(downloadDir, forKey: Keys.downloadDir) }
    }
    @Published var stopDaemonOnExit: Bool {
        didSet { defaults.set(stopDaemonOnExit, forKey: Keys.stopDaemon) }
    }

    private let defaults = UserDefaults.standard

    init() {
        let defaults = UserDefaults.standard
        nickname = defaults.string(forKey: Keys.nickname) ?? ("macOS-" + (Host.current().localizedName ?? "user"))
        downloadDir = defaults.string(forKey: Keys.downloadDir) ?? SettingsStore.defaultDownloadDir()
        stopDaemonOnExit = defaults.bool(forKey: Keys.stopDaemon)
    }

    static func defaultDownloadDir() -> String {
        let home = FileManager.default.homeDirectoryForCurrentUser
        return home.appendingPathComponent("Downloads/LANChat").path
    }

    static func socketPath() -> String {
        let base = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Application Support/LANChat")
        try? FileManager.default.createDirectory(at: base, withIntermediateDirectories: true)
        return base.appendingPathComponent("lanchat.sock").path
    }

    private enum Keys {
        static let nickname = "nickname"
        static let downloadDir = "downloadDir"
        static let stopDaemon = "stopDaemonOnExit"
    }
}
