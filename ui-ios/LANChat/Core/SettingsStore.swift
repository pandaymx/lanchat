import Foundation
#if canImport(UIKit)
import UIKit
#endif

/// 用户偏好（昵称 / 下载目录），持久化到 UserDefaults。
final class SettingsStore: ObservableObject {
    @Published var nickname: String {
        didSet { defaults.set(nickname, forKey: Keys.nickname) }
    }
    @Published var downloadDir: String {
        didSet { defaults.set(downloadDir, forKey: Keys.downloadDir) }
    }

    private let defaults = UserDefaults.standard

    private enum Keys {
        static let nickname = "nickname"
        static let downloadDir = "downloadDir"
    }

    init() {
        let docs = FileManager.default.urls(for: .documentDirectory, in: .userDomainMask)[0]
        let defaultDir = docs.appendingPathComponent("LANChat", isDirectory: true).path

        if let saved = defaults.string(forKey: Keys.nickname), !saved.isEmpty {
            nickname = saved
        } else {
            #if canImport(UIKit)
            nickname = "iOS-" + UIDevice.current.name
            #else
            nickname = "iOS-user"
            #endif
        }
        downloadDir = defaults.string(forKey: Keys.downloadDir) ?? defaultDir
    }

    /// 确保下载目录存在。
    @discardableResult
    func ensureDownloadDir() -> String {
        try? FileManager.default.createDirectory(atPath: downloadDir,
                                                 withIntermediateDirectories: true)
        return downloadDir
    }
}
