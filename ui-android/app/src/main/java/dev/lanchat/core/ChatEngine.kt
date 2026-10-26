package dev.lanchat.core

import dev.lanchat.bindings.mobile.Client as GoClient
import dev.lanchat.bindings.mobile.Mobile
import dev.lanchat.model.Channel
import dev.lanchat.model.ChatMessage
import dev.lanchat.model.FullState
import dev.lanchat.model.Peer
import dev.lanchat.model.ServerInfo
import dev.lanchat.model.Transfer
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject
import kotlin.coroutines.cancellation.CancellationException

/** 核心上行事件（已转换为本地模型，线程由实现方切回主线程）。 */
sealed interface CoreEvent {
    data class ConnChanged(val state: String, val reason: String) : CoreEvent
    data class PeerJoined(val peer: Peer) : CoreEvent
    data class PeerLeft(val peerId: String) : CoreEvent
    data class Message(
        val from: String,
        val group: String,
        val msgId: String,
        val type: String,
        val text: String,
    ) : CoreEvent
    data class Progress(val transfer: Transfer) : CoreEvent
    data class Done(val transferId: String) : CoreEvent
    data class Failed(val transferId: String, val reason: String) : CoreEvent
    data class GroupMatrix(
        val groupId: String,
        val transferId: String,
        val haveBitmap: ByteArray,
    ) : CoreEvent
    data class ChannelsUpdated(val channels: List<Channel>) : CoreEvent
}

/** 对 UI 暴露的核心操作（suspend，内部切 IO）。 */
class ChatEngine {

    private val _events = MutableSharedFlow<CoreEvent>(extraBufferCapacity = 64)
    val events: SharedFlow<CoreEvent> = _events.asSharedFlow()

    @Volatile
    private var client: GoClient? = null
    private var downloadDir: String = ""

    @Volatile
    var initError: String? = null
        private set

    /** 在已取得必要参数后创建核心；listener 回调来自 Go 线程，需 post 到主线程。 */
    @Synchronized
    fun start(nickname: String, downloadDir: String, postToMain: (() -> Unit) -> Unit) {
        if (client != null) return
        this.downloadDir = downloadDir
        val listener = GoListener { event ->
            postToMain { _events.tryEmit(event) }
        }
        try {
            client = Mobile.newClient(nickname, "android", downloadDir, listener)
            initError = null
        } catch (t: Throwable) {
            initError = t.message ?: t.javaClass.simpleName
        }
    }

    @Synchronized
    fun close() {
        runCatching { client?.close() }
        client = null
    }

    private fun requireClient(): GoClient {
        val c = client
        if (c != null) return c
        val reason = initError ?: "核心尚未就绪"
        throw IllegalStateException(reason)
    }

    suspend fun getState(): FullState = ioOp {
        val c = client ?: return@ioOp FullState()
        decodeState(c.stateJSON)
    }

    suspend fun connect(addr: String, psk: String): Unit = ioOp { requireClient().connect(addr, psk) }

    // gomobile 只把 Get* 前缀的 Go 方法生成成 Java getter（stateJSON 可用属性语法），
    // BrowseServersJSON / ChannelListJSON 没有 Get 前缀，生成的是普通方法，必须带括号调用。
    suspend fun browseServers(): List<ServerInfo> =
        ioOp { decodeServers(requireClient().browseServersJSON()) }

    suspend fun sendText(to: String, text: String, group: String = ""): String =
        ioOp { requireClient().sendText(to, text, group) }

    suspend fun sendSticker(to: String, path: String): String =
        ioOp { requireClient().sendSticker(to, path) }

    suspend fun offerFile(to: String, path: String): String =
        ioOp { requireClient().offerFile(to, path) }

    suspend fun offerFileToGroup(group: String, path: String): String =
        ioOp { requireClient().offerFileToGroup(group, path) }

    suspend fun respondFile(transferId: String, accept: Boolean, dest: String): Unit =
        ioOp { requireClient().respondFile(transferId, accept, dest) }

    /** 以默认下载目录 + 文件名接受入站传输。 */
    suspend fun acceptInbound(transferId: String, name: String): Unit =
        respondFile(transferId, true, joinPath(downloadDir, safeName(name)))

    suspend fun cancelFile(transferId: String): Unit =
        ioOp { requireClient().cancelFile(transferId) }

    suspend fun pauseFile(transferId: String): Unit =
        ioOp { requireClient().pauseFile(transferId) }

    suspend fun resumeFile(transferId: String): Unit =
        ioOp { requireClient().resumeFile(transferId) }

    suspend fun setNickname(name: String): Unit = ioOp { requireClient().setNickname(name) }

    suspend fun pickDownloadDir(path: String): Unit = ioOp { requireClient().pickDownloadDir(path) }

    /** 创建频道；private 频道仅可经邀请加入。返回值频道 ID 当前为空（以 channel.updated 为准）。 */
    suspend fun channelCreate(name: String, topic: String, private: Boolean): Unit =
        ioOp { requireClient().channelCreate(name, topic, private) }

    suspend fun channelJoin(channelId: String): Unit =
        ioOp { requireClient().channelJoin(channelId) }

    suspend fun channelInvite(channelId: String, memberId: String): Unit =
        ioOp { requireClient().channelInvite(channelId, memberId) }

    suspend fun channelLeave(channelId: String): Unit =
        ioOp { requireClient().channelLeave(channelId) }

    suspend fun channelList(): List<Channel> =
        ioOp { decodeChannels(requireClient().channelListJSON()) }

    private suspend fun <T> ioOp(block: () -> T): T =
        withContext(Dispatchers.IO) {
            try {
                block()
            } catch (e: CancellationException) {
                throw e
            }
        }
}

// ---- Go JSON 快照 → 本地模型 ----

internal fun decodeState(payload: ByteArray?): FullState {
    if (payload == null) return FullState()
    val json = JSONObject(String(payload))
    val selfId = json.optString("selfId")
    return FullState(
        conn = json.optString("conn", "disconnected"),
        server = json.optString("server"),
        selfId = selfId,
        nickname = json.optString("nickname"),
        peers = json.optJSONArray("peers").toPeerList(),
        transfers = json.optJSONArray("transfers").toTransferList(),
        channels = json.optJSONArray("channels").toChannelList(),
        messages = json.optJSONArray("messages").toMessageList(selfId),
    )
}

internal fun decodeServers(payload: ByteArray?): List<ServerInfo> {
    val array = payload?.toJsonArray() ?: return emptyList()
    return buildList {
        for (i in 0 until array.length()) {
            val item = array.optJSONObject(i) ?: continue
            add(
                ServerInfo(
                    name = item.optString("name"),
                    id = item.optString("id"),
                    addr = item.optString("addr"),
                    version = item.optString("version"),
                    authMode = item.optString("authMode").ifEmpty { "psk" },
                ),
            )
        }
    }
}

internal fun decodeChannels(payload: ByteArray?): List<Channel> =
    payload.toJsonArray().toChannelList()

private fun JSONArray?.toChannelList(): List<Channel> {
    if (this == null) return emptyList()
    return buildList {
        for (i in 0 until length()) {
            val item = optJSONObject(i) ?: continue
            add(
                Channel(
                    id = item.optString("id"),
                    name = item.optString("name"),
                    ownerId = item.optString("ownerId"),
                    private = item.optBoolean("private", false),
                    topic = item.optString("topic"),
                    members = item.optJSONArray("members").toStringList(),
                ),
            )
        }
    }
}

private fun JSONArray?.toPeerList(): List<Peer> {
    if (this == null) return emptyList()
    return buildList {
        for (i in 0 until length()) {
            val item = optJSONObject(i) ?: continue
            add(
                Peer(
                    id = item.optString("id"),
                    nickname = item.optString("nickname"),
                    os = item.optString("os"),
                    status = item.optString("status"),
                ),
            )
        }
    }
}

private fun JSONArray?.toTransferList(): List<Transfer> {
    if (this == null) return emptyList()
    return buildList {
        for (i in 0 until length()) {
            val item = optJSONObject(i) ?: continue
            add(
                Transfer(
                    id = item.optString("id"),
                    direction = item.optString("direction"),
                    state = item.optString("state"),
                    kind = item.optString("kind"),
                    peerId = item.optString("peerId"),
                    groupId = item.optString("groupId"),
                    name = item.optString("name"),
                    size = item.optLong("size"),
                    bytesDone = item.optLong("bytesDone"),
                    speedBps = item.optLong("speedBps"),
                    viaRelay = item.optBoolean("viaRelay", false),
                    errorReason = item.optString("errorReason"),
                ),
            )
        }
    }
}

private fun JSONArray?.toMessageList(selfId: String): List<ChatMessage> {
    if (this == null) return emptyList()
    return buildList {
        for (i in 0 until length()) {
            val item = optJSONObject(i) ?: continue
            val from = item.optString("from")
            val to = item.optString("to")
            val group = item.optString("group")
            val inbound = if (selfId.isNotEmpty()) from != selfId else false
            val peerId = if (inbound) from else to.ifEmpty { from }
            add(
                ChatMessage(
                    peerId = peerId,
                    msgId = item.optString("id"),
                    text = item.optString("text"),
                    inbound = inbound,
                    timestamp = item.optLong("ts", System.currentTimeMillis()),
                    group = group,
                    senderId = if (group.isNotEmpty() && inbound) from else "",
                    revoked = item.optBoolean("revoked", false),
                    readBy = item.optJSONArray("readBy").toStringList(),
                    replyQuote = item.optString("replyQuote"),
                    reactions = item.optJSONObject("reactions").toReactionMap(),
                    editedAt = item.optLong("editedAt", 0L),
                    pinned = item.optBoolean("pinned", false),
                    mediaMime = item.optString("mediaMime"),
                    mediaPath = item.optString("mediaPath"),
                ),
            )
        }
    }
}

private fun JSONObject?.toReactionMap(): Map<String, List<String>> {
    if (this == null) return emptyMap()
    val keys = keys()
    val map = mutableMapOf<String, List<String>>()
    while (keys.hasNext()) {
        val k = keys.next()
        map[k] = optJSONArray(k).toStringList()
    }
    return map
}

private fun JSONArray?.toStringList(): List<String> {
    if (this == null) return emptyList()
    return buildList {
        for (i in 0 until length()) add(optString(i))
    }
}

private fun ByteArray?.toJsonArray(): JSONArray? = this?.let { JSONArray(String(it)) }

/** 拼接目录与文件名（Go 侧运行时按 Linux 风格处理路径）。 */
private fun joinPath(dir: String, name: String): String =
    if (dir.endsWith("/")) "$dir$name" else "$dir/$name"

/** 去除路径分隔符，避免文件名逃逸下载目录。 */
private fun safeName(name: String): String {
    val base = name.substringAfterLast('/').substringAfterLast('\\')
    return base.ifEmpty { "lanchat-received.bin" }
}
