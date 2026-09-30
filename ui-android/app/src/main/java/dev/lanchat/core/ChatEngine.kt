package dev.lanchat.core

import dev.lanchat.bindings.mobile.Channel as GoChannel
import dev.lanchat.bindings.mobile.Client as GoClient
import dev.lanchat.bindings.mobile.Mobile
import dev.lanchat.bindings.mobile.Peer as GoPeer
import dev.lanchat.bindings.mobile.Server as GoServer
import dev.lanchat.bindings.mobile.Transfer as GoTransfer
import dev.lanchat.bindings.mobile.State as GoState
import dev.lanchat.model.Channel
import dev.lanchat.model.FullState
import dev.lanchat.model.Peer
import dev.lanchat.model.ServerInfo
import dev.lanchat.model.Transfer
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.withContext
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
    data class ChannelsUpdated(val channels: List<Channel>) : CoreEvent
}

/** 对 UI 暴露的核心操作（suspend，内部切 IO）。 */
class ChatEngine {

    private val _events = MutableSharedFlow<CoreEvent>(extraBufferCapacity = 64)
    val events: SharedFlow<CoreEvent> = _events.asSharedFlow()

    private var client: GoClient? = null
    private var downloadDir: String = ""

    /** 在已取得必要参数后创建核心；listener 回调来自 Go 线程，需 post 到主线程。 */
    fun start(nickname: String, downloadDir: String, postToMain: (() -> Unit) -> Unit) {
        if (client != null) return
        this.downloadDir = downloadDir
        val listener = GoListener { event ->
            postToMain { _events.tryEmit(event) }
        }
        client = Mobile.newClient(nickname, "android", downloadDir, listener)
    }

    fun close() {
        client?.close()
        client = null
    }

    suspend fun getState(): FullState = ioOp { client!!.state.toModel() }

    suspend fun connect(addr: String, psk: String): Unit = ioOp { client!!.connect(addr, psk) }

    suspend fun browseServers(): List<ServerInfo> =
        ioOp { client!!.browseServers().map { it.toModel() } }

    suspend fun sendText(to: String, text: String, group: String = ""): String =
        ioOp { client!!.sendText(to, text, group) }

    suspend fun offerFile(to: String, path: String): String =
        ioOp { client!!.offerFile(to, path) }

    suspend fun respondFile(transferId: String, accept: Boolean, dest: String): Unit =
        ioOp { client!!.respondFile(transferId, accept, dest) }

    /** 以默认下载目录 + 文件名接受入站传输。 */
    suspend fun acceptInbound(transferId: String, name: String): Unit =
        respondFile(transferId, true, joinPath(downloadDir, safeName(name)))

    suspend fun cancelFile(transferId: String): Unit =
        ioOp { client!!.cancelFile(transferId) }

    suspend fun setNickname(name: String): Unit = ioOp { client!!.setNickname(name) }

    suspend fun pickDownloadDir(path: String): Unit = ioOp { client!!.pickDownloadDir(path) }

    private suspend fun <T> ioOp(block: () -> T): T =
        withContext(Dispatchers.IO) {
            try {
                block()
            } catch (e: CancellationException) {
                throw e
            }
        }

    // ---- 绑定类型 → 本地模型 ----

    private fun GoState.toModel(): FullState = FullState(
        conn = conn ?: "disconnected",
        server = server.orEmpty(),
        selfId = selfID.orEmpty(),
        nickname = nickname.orEmpty(),
        peers = peers?.map { it.toModel() } ?: emptyList(),
        transfers = transfers?.map { it.toModel() } ?: emptyList(),
        channels = channels?.map { it.toModel() } ?: emptyList(),
    )

    private fun GoPeer.toModel(): Peer =
        Peer(id = iD.orEmpty(), nickname = nickname.orEmpty(), os = oS.orEmpty(), status = status.orEmpty())

    private fun GoServer.toModel(): ServerInfo = ServerInfo(
        name = name.orEmpty(),
        id = iD.orEmpty(),
        addr = addr.orEmpty(),
        version = version.orEmpty(),
        authMode = authMode.orEmpty().ifEmpty { "psk" },
    )

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

    private fun GoChannel.toModel(): Channel = Channel(
        id = iD.orEmpty(),
        name = name.orEmpty(),
        ownerId = ownerID.orEmpty(),
        members = members?.toList() ?: emptyList(),
    )
}

/** 拼接目录与文件名（Go 侧运行时按 Linux 风格处理路径）。 */
private fun joinPath(dir: String, name: String): String =
    if (dir.endsWith("/")) "$dir$name" else "$dir/$name"

/** 去除路径分隔符，避免文件名逃逸下载目录。 */
private fun safeName(name: String): String {
    val base = name.substringAfterLast('/').substringAfterLast('\\')
    return base.ifEmpty { "lanchat-received.bin" }
}
