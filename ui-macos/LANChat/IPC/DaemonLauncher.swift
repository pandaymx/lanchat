import Foundation

/// 确保 lanchat-daemon 在运行：socket 不可达时拉起进程。
/// daemon 可常驻；仅本进程拉起的实例可在退出时一并关闭。
final class DaemonLauncher {
    enum LaunchError: LocalizedError {
        case missing
        case exitedEarly(Int32)
        case timeout

        var errorDescription: String? {
            switch self {
            case .missing:
                return "未找到 lanchat-daemon，可设置环境变量 LANCHAT_DAEMON_PATH 指定其位置"
            case let .exitedEarly(code):
                return "后台服务启动后立即退出（代码 \(code)）"
            case .timeout:
                return "等待后台服务就绪超时"
            }
        }
    }

    let socketPath: String
    private var process: Process?

    /// daemon 是否由本进程拉起（决定退出时能否随 UI 关闭）。
    var launchedByUs: Bool { process != nil }

    init(socketPath: String) {
        self.socketPath = socketPath
    }

    func ensureRunning(nickname: String?, downloadDir: String?) async throws {
        if isSocketAvailable() { return }

        let executable = try resolveDaemonPath()
        let proc = Process()
        proc.executableURL = executable
        proc.arguments = ["--socket", socketPath]
        if let nickname, !nickname.isEmpty {
            proc.arguments?.append(contentsOf: ["--nickname", nickname])
        }
        if let downloadDir, !downloadDir.isEmpty {
            proc.arguments?.append(contentsOf: ["--download-dir", downloadDir])
        }
        // 丢弃输出，避免管道阻塞。
        proc.standardOutput = FileHandle(forWritingAtPath: "/dev/null")
        proc.standardError = FileHandle(forWritingAtPath: "/dev/null")

        do {
            try proc.run()
        } catch {
            throw LaunchError.missing
        }
        process = proc

        // 等待 socket 出现（最多约 10 秒）。
        for _ in 0..<100 {
            try await Task.sleep(nanoseconds: 100_000_000)
            if proc.isRunning == false {
                throw LaunchError.exitedEarly(proc.terminationStatus)
            }
            if isSocketAvailable() { return }
        }
        throw LaunchError.timeout
    }

    /// 探测 socket 文件是否存在（daemon 绑定后即创建）。
    private func isSocketAvailable() -> Bool {
        FileManager.default.fileExists(atPath: socketPath)
    }

    /// 解析 daemon 路径：环境变量 → App Bundle Resources → PATH。
    private func resolveDaemonPath() throws -> URL {
        var candidates: [String] = []
        if let env = ProcessInfo.processInfo.environment["LANCHAT_DAEMON_PATH"], !env.isEmpty {
            candidates.append(env)
        }
        if let bundled = Bundle.main.url(forResource: "lanchat-daemon", withExtension: nil)?.path {
            candidates.append(bundled)
        }
        if let path = which("lanchat-daemon") {
            candidates.append(path)
        }
        for c in candidates where FileManager.default.isExecutableFile(atPath: c) {
            return URL(fileURLWithPath: c)
        }
        throw LaunchError.missing
    }

    private func which(_ name: String) -> String? {
        let task = Process()
        task.executableURL = URL(fileURLWithPath: "/usr/bin/which")
        task.arguments = [name]
        let pipe = Pipe()
        task.standardOutput = pipe
        task.standardError = FileHandle(forWritingAtPath: "/dev/null")
        do { try task.run() } catch { return nil }
        task.waitUntilExit()
        guard task.terminationStatus == 0 else { return nil }
        let out = String(data: pipe.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8)?
            .trimmingCharacters(in: .whitespacesAndNewlines)
        return (out?.isEmpty == false) ? out : nil
    }

    /// 关闭由本进程拉起的 daemon（用户选择退出并停止后台时调用）。
    func stopDaemonIfOwned() {
        guard let proc = process, proc.isRunning else { return }
        proc.terminate()
        proc.waitUntilExit()
        process = nil
    }
}
