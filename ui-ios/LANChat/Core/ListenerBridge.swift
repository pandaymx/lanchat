import Foundation

/// 实现 gomobile 生成的 LCMobileListener 协议。
///
/// 这些方法由 Go 后台 goroutine 调用（非主线程）；这里先做 DTO 转换，
/// 再通过 ChatEngine 的派发闭包切回主线程。
final class ListenerBridge: NSObject, LCMobileListener {
    private let emit: (CoreEvent) -> Void

    init(emit: @escaping (CoreEvent) -> Void) {
        self.emit = emit
    }

    func onConnChanged(_ state: String?, reason: String?) {
        emit(.connChanged(state: state ?? "", reason: reason ?? ""))
    }

    func onPeerJoined(_ peer: LCPeer?) {
        guard let peer else { return }
        emit(.peerJoined(ChatEngine.toModel(peer)))
    }

    func onPeerLeft(_ peerID: String?) {
        emit(.peerLeft(peerID ?? ""))
    }

    func onMessageReceived(_ from: String?, group: String?, msgID: String?,
                           typ: String?, text: String?) {
        emit(.message(from: from ?? "", group: group ?? "", msgId: msgID ?? "",
                      type: typ ?? "", text: text ?? ""))
    }

    func onTransferProgress(_ t: LCTransfer?) {
        guard let t else { return }
        emit(.progress(ChatEngine.toModel(t)))
    }

    func onTransferDone(_ transferID: String?) {
        emit(.done(transferID ?? ""))
    }

    func onTransferFailed(_ transferID: String?, reason: String?) {
        emit(.failed(transferId: transferID ?? "", reason: reason ?? ""))
    }

    func onGroupMatrix(_ groupID: String?, transferID: String?, haveBitmap: Data?) {
        // M9 群组能力，M8 不处理矩阵。
    }

    func onChannelUpdated(_ payload: Data?) {
        // Go 侧 gomobile 不支持结构体切片，改传 []appapi.Channel 的 JSON。
        guard let payload,
              let dtos = try? JSONDecoder().decode([ChannelJSON].self, from: payload) else {
            emit(.channelsUpdated([]))
            return
        }
        emit(.channelsUpdated(dtos.map { $0.toModel() }))
    }
}
