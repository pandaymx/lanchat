import Foundation
import Network

/// daemon Unix socket 上的 JSON-RPC 2.0 客户端。
/// 一行一帧（UTF-8，\n 分隔）：带 id 的请求收到 result/error；
/// 无 id 的消息作为 notification 上报。后台自动重连。
final class IpcClient {
    enum IpcError: LocalizedError {
        case notConnected
        case rpc(Int, String)
        case timeout(String)
        case decode(String)

        var errorDescription: String? {
            switch self {
            case .notConnected: return "未连接到后台服务"
            case let .rpc(_, msg): return msg
            case let .timeout(m): return "请求超时：\(m)"
            case let .decode(m): return "数据解析失败：\(m)"
            }
        }
    }

    /// 收到下行通知：方法名 + 原始 params。
    var onNotification: ((String, Data) -> Void)?
    /// 连接状态变化。
    var onConnChanged: ((Bool) -> Void)?

    private let path: String
    private let queue = DispatchQueue(label: "dev.lanchat.ipc")
    private var connection: NWConnection?

    private var pending: [Int: CheckedContinuation<Data, Error>] = [:]
    private var nextId = 0
    private let lock = NSLock()
    private var buffer = Data()

    var isConnected = false

    init(path: String) {
        self.path = path
    }

    func start() {
        connect()
    }

    func stop() {
        connection?.cancel()
        connection = nil
        failAll(IpcError.notConnected)
    }

    private func connect() {
        let endpoint = NWEndpoint.unix(path: path)
        let conn = NWConnection(to: endpoint, using: .tcp)
        connection = conn
        conn.stateUpdateHandler = { [weak self] state in
            guard let self else { return }
            switch state {
            case .ready:
                self.setConnected(true)
                self.receive()
            case .failed, .cancelled:
                self.setConnected(false)
                self.failAll(IpcError.notConnected)
                self.queue.asyncAfter(deadline: .now() + 1) { [weak self] in
                    self?.reconnectIfNeeded(current: conn)
                }
            default:
                break
            }
        }
        conn.start(queue: queue)
    }

    private func reconnectIfNeeded(current: NWConnection) {
        guard connection === current else { return }
        connection = nil
        connect()
    }

    private func setConnected(_ value: Bool) {
        guard isConnected != value else { return }
        isConnected = value
        DispatchQueue.main.async { self.onConnChanged?(value) }
    }

    private func receive() {
        connection?.receive(minimumIncompleteLength: 1, maximumLength: 64 * 1024) {
            [weak self] data, _, _, _ in
            guard let self else { return }
            if let data, !data.isEmpty {
                self.ingest(data)
                self.receive()
            } else {
                // 对端关闭：状态处理器会触发重连。
                self.connection?.cancel()
            }
        }
    }

    private func ingest(_ chunk: Data) {
        buffer.append(chunk)
        while let nl = buffer.firstIndex(of: 0x0A) {
            let line = buffer.subdata(in: buffer.startIndex..<nl)
            buffer.removeSubrange(buffer.startIndex...nl)
            dispatch(line)
        }
    }

    private func dispatch(_ line: Data) {
        guard let obj = try? JSONSerialization.jsonObject(with: line) as? [String: Any] else {
            return
        }
        if let idNum = obj["id"] as? NSNumber {
            let id = idNum.intValue
            let continuation = takeContinuation(id)
            if let err = obj["error"] as? [String: Any] {
                let code = err["code"] as? Int ?? -32603
                let message = err["message"] as? String ?? "未知错误"
                continuation?.resume(throwing: IpcError.rpc(code, message))
            } else {
                let result = (obj["result"].flatMap { try? JSONSerialization.data(withJSONObject: $0) }) ?? Data("{}".utf8)
                continuation?.resume(returning: result)
            }
        } else if let method = obj["method"] as? String {
            let params = (obj["params"].flatMap { try? JSONSerialization.data(withJSONObject: $0) }) ?? Data("{}".utf8)
            DispatchQueue.main.async { self.onNotification?(method, params) }
        }
    }

    /// 发起 JSON-RPC 请求并异步等待 result Data（20 秒超时）。
    func call(_ method: String, params: Any?) async throws -> Data {
        try await withThrowingTaskGroup(of: Data.self) { group in
            group.addTask { try await self.performCall(method, params: params) }
            group.addTask {
                try await Task.sleep(nanoseconds: 20_000_000_000)
                throw IpcError.timeout(method)
            }
            do {
                let result = try await group.next()!
                group.cancelAll()
                return result
            } catch {
                group.cancelAll()
                throw error
            }
        }
    }

    private func performCall(_ method: String, params: Any?) async throws -> Data {
        let id = allocateId()
        return try await withCheckedThrowingContinuation { continuation in
            storeContinuation(continuation, id: id)
            var payload: [String: Any] = ["jsonrpc": "2.0", "id": id, "method": method]
            if let params { payload["params"] = params }

            guard JSONSerialization.isValidJSONObject(payload),
                  let data = try? JSONSerialization.data(withJSONObject: payload) else {
                takeContinuation(id)?.resume(throwing: IpcError.decode(method))
                return
            }
            var frame = data
            frame.append(0x0A)

            guard isConnected else {
                takeContinuation(id)?.resume(throwing: IpcError.notConnected)
                return
            }
            connection?.send(content: frame, completion: .contentProcessed { [weak self] error in
                if error != nil {
                    self?.takeContinuation(id)?.resume(throwing: IpcError.notConnected)
                }
            })
        }
    }

    /// 发起请求并把 result 解码为指定类型。
    func call<T: Decodable>(_ method: String, params: Any?, as type: T.Type) async throws -> T {
        let data = try await call(method, params: params)
        do {
            return try JSONDecoder().decode(T.self, from: data)
        } catch {
            throw IpcError.decode(error.localizedDescription)
        }
    }

    // MARK: - pending 管理

    private func allocateId() -> Int {
        lock.lock(); defer { lock.unlock() }
        nextId += 1
        return nextId
    }

    private func storeContinuation(_ c: CheckedContinuation<Data, Error>, id: Int) {
        lock.lock(); defer { lock.unlock() }
        pending[id] = c
    }

    private func takeContinuation(_ id: Int) -> CheckedContinuation<Data, Error>? {
        lock.lock(); defer { lock.unlock() }
        return pending.removeValue(forKey: id)
    }

    private func failAll(_ error: Error) {
        lock.lock()
        let all = pending
        pending.removeAll()
        lock.unlock()
        for (_, c) in all { c.resume(throwing: error) }
    }
}
