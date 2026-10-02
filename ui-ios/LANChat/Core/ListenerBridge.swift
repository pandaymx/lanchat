import Foundation

/// 实现 gomobile 生成的 LCMobileListenerProtocol 协议。
///
/// 注意：gomobile 对可由宿主实现的接口同时生成
///   - @protocol LCMobileListener（Swift 导入名：LCMobileListenerProtocol）
///   - @interface LCMobileListener（供子类化的基类，占用 LCMobileListener 名字）
/// 因此协议一致性必须写 LCMobileListenerProtocol。
///
/// 这些方法由 Go 后台 goroutine 调用（非主线程）；这里先做 DTO 转换，
/// 再通过 ChatEngine 的派发闭包切回主线程。
final class ListenerBridge: NSObject, LCMobileListenerProtocol {
    private let emit: (CoreEvent) -> Void

    init(emit: @escaping (CoreEvent) -> Void) {
        self.emit = emit
    }

    func onConnChanged(_ state: String?, reason: String?) {
        emit(.connChanged(state: state ?? "", reason: reason ?? ""))
    }

    func onPeerJoined(_ peer: LCMobilePeer?) {
        guard let peer else { return }
        emit(.peerJoined(Self.toModel(peer)))
    }

    func onPeerLeft(_ peerID: String?) {
        emit(.peerLeft(peerID ?? ""))
    }

    func onMessageReceived(_ from: String?, group: String?, msgID: String?,
                           typ: String?, text: String?) {
        emit(.message(from: from ?? "", group: group ?? "", msgId: msgID ?? "",
                      type: typ ?? "", text: text ?? ""))
    }

    func onTransferProgress(_ t: LCMobileTransfer?) {
        guard let t else { return }
        emit(.progress(Self.toModel(t)))
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

    // MARK: - gomobile 对象 -> 本地模型

    private static func toModel(_ p: LCMobilePeer) -> Peer {
        Peer(id: p.id_, nickname: p.nickname, os: p.os, status: p.status)
    }

    private static func toModel(_ t: LCMobileTransfer) -> Transfer {
        Transfer(id: t.id_, direction: t.direction, state: t.state, kind: t.kind,
                 peerId: t.peerID, groupId: t.groupID, name: t.name,
                 size: t.size, bytesDone: t.bytesDone, speedBps: t.speedBps,
                 viaRelay: t.viaRelay, errorReason: t.errorReason)
    }
}
