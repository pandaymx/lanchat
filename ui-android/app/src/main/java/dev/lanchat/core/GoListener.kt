package dev.lanchat.core

import dev.lanchat.bindings.mobile.Channel as GoChannel
import dev.lanchat.bindings.mobile.Listener
import dev.lanchat.bindings.mobile.Peer as GoPeer
import dev.lanchat.bindings.mobile.Transfer as GoTransfer
import dev.lanchat.model.Channel
import dev.lanchat.model.Peer
import dev.lanchat.model.Transfer

/**
 * 实现 gomobile 生成的 Listener 接口。
 *
 * 这些方法由 Go 后台 goroutine 调用（非主线程）；构造时传入的 [onEvent]
 * 负责把事件 post 到 Android 主线程后再写入 SharedFlow。
 */
class GoListener(
    private val onEvent: (CoreEvent) -> Unit,
) : Listener {

    override fun onConnChanged(state: String?, reason: String?) {
        onEvent(CoreEvent.ConnChanged(state.orEmpty(), reason.orEmpty()))
    }

    override fun onPeerJoined(peer: GoPeer?) {
        if (peer == null) return
        onEvent(
            CoreEvent.PeerJoined(
                Peer(
                    id = peer.iD.orEmpty(),
                    nickname = peer.nickname.orEmpty(),
                    os = peer.oS.orEmpty(),
                    status = peer.status.orEmpty(),
                ),
            ),
        )
    }

    override fun onPeerLeft(peerID: String?) {
        onEvent(CoreEvent.PeerLeft(peerID.orEmpty()))
    }

    override fun onMessageReceived(
        from: String?,
        group: String?,
        msgID: String?,
        typ: String?,
        text: String?,
    ) {
        onEvent(
            CoreEvent.Message(
                from = from.orEmpty(),
                group = group.orEmpty(),
                msgId = msgID.orEmpty(),
                type = typ.orEmpty(),
                text = text.orEmpty(),
            ),
        )
    }

    override fun onTransferProgress(t: GoTransfer?) {
        if (t == null) return
        onEvent(CoreEvent.Progress(t.toModel()))
    }

    override fun onTransferDone(transferID: String?) {
        onEvent(CoreEvent.Done(transferID.orEmpty()))
    }

    override fun onTransferFailed(transferID: String?, reason: String?) {
        onEvent(CoreEvent.Failed(transferID.orEmpty(), reason.orEmpty()))
    }

    override fun onGroupMatrix(groupID: String?, transferID: String?, haveBitmap: ByteArray?) {
        // M9 群组能力，M6 暂不处理矩阵。
    }

    override fun onChannelUpdated(channels: Array<GoChannel>?) {
        val list = channels?.map {
            Channel(
                id = it.iD.orEmpty(),
                name = it.name.orEmpty(),
                ownerId = it.ownerID.orEmpty(),
                members = it.members?.toList() ?: emptyList(),
            )
        } ?: emptyList()
        onEvent(CoreEvent.ChannelsUpdated(list))
    }

    private fun GoTransfer.toModel(): Transfer = Transfer(
        id = iD.orEmpty(),
        direction = direction.orEmpty(),
        state = state.orEmpty(),
        kind = kind.orEmpty(),
        peerId = peerID.orEmpty(),
        groupId = groupID.orEmpty(),
        name = name.orEmpty(),
        size = size,
        bytesDone = bytesDone,
        speedBps = speedBps,
        viaRelay = viaRelay,
        errorReason = errorReason.orEmpty(),
    )
}
