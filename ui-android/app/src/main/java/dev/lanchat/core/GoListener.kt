package dev.lanchat.core

import dev.lanchat.bindings.mobile.Listener
import dev.lanchat.bindings.mobile.Peer as GoPeer
import dev.lanchat.bindings.mobile.Transfer as GoTransfer
import dev.lanchat.model.Channel
import dev.lanchat.model.Peer
import dev.lanchat.model.Transfer
import org.json.JSONArray

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
                    id = peer.ID.orEmpty(),
                    nickname = peer.nickname.orEmpty(),
                    os = peer.OS.orEmpty(),
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

    override fun onChannelUpdated(payload: ByteArray?) {
        // Go 侧 gomobile 不支持结构体切片，改传 []appapi.Channel 的 JSON。
        if (payload == null) {
            onEvent(CoreEvent.ChannelsUpdated(emptyList()))
            return
        }
        val list = buildList {
            val arr = JSONArray(String(payload, Charsets.UTF_8))
            for (i in 0 until arr.length()) {
                val o = arr.getJSONObject(i)
                add(
                    Channel(
                        id = o.optString("id"),
                        name = o.optString("name"),
                        ownerId = o.optString("ownerId"),
                        members = o.optJSONArray("members")?.let { a ->
                            (0 until a.length()).map { a.getString(it) }
                        } ?: emptyList(),
                    ),
                )
            }
        }
        onEvent(CoreEvent.ChannelsUpdated(list))
    }

    private fun GoTransfer.toModel(): Transfer = Transfer(
        id = ID.orEmpty(),
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
