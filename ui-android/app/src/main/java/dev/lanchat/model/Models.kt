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
    val private: Boolean = false,
    val topic: String = "",
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
    val messages: List<ChatMessage> = emptyList(),
)

data class ChatMessage(
    val peerId: String,
    val msgId: String,
    val text: String,
    val inbound: Boolean,
    val timestamp: Long,
    /** 群组（频道）ID；空表示 1:1 单播消息。 */
    val group: String = "",
    /** 群消息发送者 ID；仅群消息且入站时有意义。 */
    val senderId: String = "",
    /** M11a: 消息是否已撤回。 */
    val revoked: Boolean = false,
    /** M11a: 已读该消息的对端 ID 列表。 */
    val readBy: List<String> = emptyList(),
    /** 撤回前保存的原始可编辑消息正文（用于点击「重新编辑」恢复到输入框）。 */
    val originalText: String = "",
)

/** 将 group.matrix 的 haveBitmap 转为紧凑块进度摘要（如 "▓▓▓░░░ 4/8 块"）。 */
fun formatMatrixSummary(haveBitmap: ByteArray, size: Long): String {
    if (haveBitmap.isEmpty()) return ""
    val chunkSize = 1024L * 1024L
    val totalChunks = if (size > 0) {
        ((size + chunkSize - 1) / chunkSize).toInt()
    } else {
        haveBitmap.size * 8
    }
    if (totalChunks <= 0) return ""
    var have = 0
    for (i in 0 until totalChunks) {
        val byteIdx = i / 8
        val bitIdx = i % 8
        val bit = byteIdx < haveBitmap.size && ((haveBitmap[byteIdx].toInt() ushr bitIdx) and 1) == 1
        if (bit) have++
    }
    val slots = minOf(totalChunks, 8)
    val bar = buildString(slots) {
        for (s in 0 until slots) {
            val idx = s * totalChunks / slots
            val byteIdx = idx / 8
            val bitIdx = idx % 8
            val bit = byteIdx < haveBitmap.size && ((haveBitmap[byteIdx].toInt() ushr bitIdx) and 1) == 1
            append(if (bit) '▓' else '░')
        }
    }
    return "$bar $have/$totalChunks 块"
}
