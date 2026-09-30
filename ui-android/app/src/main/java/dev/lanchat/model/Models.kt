package dev.lanchat.model

/** 连接状态（字符串与 Go 侧常量一致）。 */
object ConnState {
    const val DISCONNECTED = "disconnected"
    const val CONNECTING = "connecting"
    const val CONNECTED = "connected"
    const val AUTH_FAILED = "auth_failed"
}

/** 文件传输状态。 */
object TransferState {
    const val PENDING = "pending"
    const val ACTIVE = "active"
    const val PAUSED = "paused"
    const val DONE = "done"
    const val FAILED = "failed"
    const val CANCELED = "canceled"
}

/** 传输方向。 */
object Direction {
    const val INBOUND = "inbound"
    const val OUTBOUND = "outbound"
}

data class Peer(
    val id: String,
    val nickname: String,
    val os: String = "",
    val status: String = "",
)

data class ServerInfo(
    val name: String,
    val id: String,
    val addr: String,
    val version: String = "",
    val authMode: String = "psk",
)

data class Transfer(
    val id: String,
    val direction: String,
    val state: String,
    val kind: String = "unicast",
    val peerId: String = "",
    val groupId: String = "",
    val name: String = "",
    val size: Long = 0,
    val bytesDone: Long = 0,
    val speedBps: Long = 0,
    val viaRelay: Boolean = false,
    val errorReason: String = "",
) {
    val fraction: Float
        get() = if (size > 0) (bytesDone.toFloat() / size).coerceIn(0f, 1f) else 0f
}

data class Channel(
    val id: String,
    val name: String,
    val ownerId: String = "",
    val members: List<String> = emptyList(),
)

data class FullState(
    val conn: String = ConnState.DISCONNECTED,
    val server: String = "",
    val selfId: String = "",
    val nickname: String = "",
    val peers: List<Peer> = emptyList(),
    val transfers: List<Transfer> = emptyList(),
    val channels: List<Channel> = emptyList(),
)

data class ChatMessage(
    val peerId: String,
    val msgId: String,
    val text: String,
    val inbound: Boolean,
    val timestamp: Long,
)
