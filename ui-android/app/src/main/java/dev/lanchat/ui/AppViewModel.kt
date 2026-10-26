package dev.lanchat.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.lanchat.core.CoreEvent
import dev.lanchat.core.ServiceLocator
import dev.lanchat.model.Channel
import dev.lanchat.model.ChatMessage
import dev.lanchat.model.Conversations
import dev.lanchat.model.Peer
import dev.lanchat.model.ServerInfo
import dev.lanchat.model.Transfer
import dev.lanchat.model.formatMatrixSummary
import java.io.File
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch

data class AppUi(
    val conn: String = "disconnected",
    val server: String = "",
    val selfId: String = "",
    val nickname: String = "",
    val downloadDir: String = "",
    val peers: List<Peer> = emptyList(),
    val transfers: List<Transfer> = emptyList(),
    val channels: List<Channel> = emptyList(),
    val messagesByConversation: Map<String, List<ChatMessage>> = emptyMap(),
    val unreadCounts: Map<String, Int> = emptyMap(),
    val matrixSummary: Map<String, String> = emptyMap(),
    val discoveredServers: List<ServerInfo> = emptyList(),
    val busy: Boolean = false,
    val error: String? = null,
)

class AppViewModel : ViewModel() {

    private val engine = ServiceLocator.engine

    private val _ui = MutableStateFlow(AppUi())
    val ui: StateFlow<AppUi> = _ui.asStateFlow()

    init {
        refresh()
        viewModelScope.launch {
            engine.events.collect { event -> handleEvent(event) }
        }
    }

    fun consumeError() {
        _ui.update { it.copy(error = null) }
    }

    fun refresh() {
        launchOp {
            val state = engine.getState()
            _ui.update {
                var convs = it.messagesByConversation
                for (m in state.messages) {
                    convs = Conversations.append(convs, m)
                }
                it.copy(
                    conn = state.conn,
                    server = state.server,
                    selfId = state.selfId,
                    nickname = state.nickname,
                    peers = state.peers,
                    transfers = state.transfers,
                    channels = state.channels,
                    messagesByConversation = convs,
                )
            }
        }
    }

    fun discover() {
        launchOp {
            val servers = engine.browseServers()
            _ui.update { it.copy(discoveredServers = servers) }
        }
    }

    fun connect(addr: String, psk: String) {
        val target = addr.trim().ifEmpty { "auto" }
        launchOp {
            engine.connect(target, psk)
        }
    }

    fun sendText(to: String, text: String) {
        val content = text.trim()
        if (content.isEmpty()) return
        if (content.startsWith("/search ")) {
            val q = content.removePrefix("/search ").trim()
            val hits = Conversations.search(_ui.value.messagesByConversation, Conversations.peerKey(to), q)
            appendMessage(
                ChatMessage(
                    peerId = to,
                    msgId = "search-${System.currentTimeMillis()}",
                    text = "[搜索“$q”：共 ${hits.size} 条匹配]",
                    inbound = false,
                    timestamp = System.currentTimeMillis(),
                ),
            )
            return
        }
        launchOp {
            val msgId = engine.sendText(to, content)
            if (msgId == "e2ee:on" || msgId == "e2ee:off") {
                val notice = if (msgId == "e2ee:on") "[已开启端到端加密]" else "[已关闭端到端加密]"
                appendMessage(
                    ChatMessage(
                        peerId = to,
                        msgId = "e2ee-${System.currentTimeMillis()}",
                        text = notice,
                        inbound = false,
                        timestamp = System.currentTimeMillis(),
                    ),
                )
                return@launchOp
            }
            if (msgId.startsWith("fp:")) {
                val fp = msgId.removePrefix("fp:")
                appendMessage(
                    ChatMessage(
                        peerId = to,
                        msgId = "fp-${System.currentTimeMillis()}",
                        text = "🛡️ [安全码指纹核验] $fp",
                        inbound = false,
                        timestamp = System.currentTimeMillis(),
                    ),
                )
                return@launchOp
            }
            if (msgId.startsWith("bwlimit:")) {
                val lim = msgId.removePrefix("bwlimit:")
                appendMessage(
                    ChatMessage(
                        peerId = to,
                        msgId = "bwlimit-${System.currentTimeMillis()}",
                        text = "⚡ [带宽限速已设置为 $lim MB/s]",
                        inbound = false,
                        timestamp = System.currentTimeMillis(),
                    ),
                )
                return@launchOp
            }
            if (msgId.startsWith("status:")) {
                val st = msgId.removePrefix("status:")
                appendMessage(
                    ChatMessage(
                        peerId = to,
                        msgId = "status-${System.currentTimeMillis()}",
                        text = "📊 [诊断状态] $st",
                        inbound = false,
                        timestamp = System.currentTimeMillis(),
                    ),
                )
                return@launchOp
            }
            if (msgId.startsWith("update:")) {
                val up = msgId.removePrefix("update:")
                appendMessage(
                    ChatMessage(
                        peerId = to,
                        msgId = "update-${System.currentTimeMillis()}",
                        text = "🚀 [版本更新] $up",
                        inbound = false,
                        timestamp = System.currentTimeMillis(),
                    ),
                )
                return@launchOp
            }
            if (content.startsWith("/revoke ")) {
                val targetId = content.removePrefix("/revoke ").trim()
                _ui.update {
                    it.copy(messagesByConversation = Conversations.revokeMessage(it.messagesByConversation, targetId))
                }
                return@launchOp
            }
            if (content.startsWith("/read ")) {
                val targetId = content.removePrefix("/read ").trim()
                _ui.update {
                    it.copy(messagesByConversation = Conversations.markRead(it.messagesByConversation, targetId, it.selfId))
                }
                return@launchOp
            }
            if (content.startsWith("/react ") || content.startsWith("/edit ") || content.startsWith("/pin ")) {
                refresh()
                return@launchOp
            }
            val display = Conversations.formatReplyText(_ui.value.messagesByConversation, content)
            appendMessage(
                ChatMessage(
                    peerId = to,
                    msgId = msgId,
                    text = display,
                    inbound = false,
                    timestamp = System.currentTimeMillis(),
                ),
            )
        }
    }

    fun sendChannelText(channelId: String, text: String) {
        val content = text.trim()
        if (content.isEmpty()) return
        if (content.startsWith("/search ")) {
            val q = content.removePrefix("/search ").trim()
            val hits = Conversations.search(_ui.value.messagesByConversation, Conversations.groupKey(channelId), q)
            appendMessage(
                ChatMessage(
                    peerId = "",
                    msgId = "search-${System.currentTimeMillis()}",
                    text = "[搜索“$q”：共 ${hits.size} 条匹配]",
                    inbound = false,
                    timestamp = System.currentTimeMillis(),
                    group = channelId,
                ),
            )
            return
        }
        launchOp {
            val msgId = engine.sendText(to = "", text = content, group = channelId)
            if (msgId == "e2ee:on" || msgId == "e2ee:off") {
                val notice = if (msgId == "e2ee:on") "[已开启端到端加密]" else "[已关闭端到端加密]"
                appendMessage(
                    ChatMessage(
                        peerId = "",
                        msgId = "e2ee-${System.currentTimeMillis()}",
                        text = notice,
                        inbound = false,
                        timestamp = System.currentTimeMillis(),
                        group = channelId,
                    ),
                )
                return@launchOp
            }
            if (content.startsWith("/revoke ")) {
                val targetId = content.removePrefix("/revoke ").trim()
                _ui.update {
                    it.copy(messagesByConversation = Conversations.revokeMessage(it.messagesByConversation, targetId))
                }
                return@launchOp
            }
            if (content.startsWith("/read ")) {
                val targetId = content.removePrefix("/read ").trim()
                _ui.update {
                    it.copy(messagesByConversation = Conversations.markRead(it.messagesByConversation, targetId, it.selfId))
                }
                return@launchOp
            }
            if (content.startsWith("/react ") || content.startsWith("/edit ") || content.startsWith("/pin ")) {
                refresh()
                return@launchOp
            }
            val display = Conversations.formatReplyText(_ui.value.messagesByConversation, content)
            appendMessage(
                ChatMessage(
                    peerId = "",
                    msgId = msgId,
                    text = display,
                    inbound = false,
                    timestamp = System.currentTimeMillis(),
                    group = channelId,
                ),
            )
        }
    }

    fun sendSticker(to: String, path: String, group: String = "") {
        launchOp {
            val msgId = engine.sendSticker(to, path)
            val name = File(path).name
            appendMessage(
                ChatMessage(
                    peerId = if (group.isEmpty()) to else "",
                    msgId = msgId,
                    text = "[贴纸: $name]",
                    inbound = false,
                    timestamp = System.currentTimeMillis(),
                    group = group,
                ),
            )
        }
    }

    fun offerFile(to: String, path: String) {
        launchOp {
            engine.offerFile(to, path)
            refresh()
        }
    }

    fun offerFileToChannel(channelId: String, path: String) {
        launchOp {
            engine.offerFileToGroup(channelId, path)
            refresh()
        }
    }

    fun createChannel(name: String, topic: String, private: Boolean) {
        val channelName = name.trim()
        if (channelName.isEmpty()) {
            _ui.update { it.copy(error = "请输入频道名称") }
            return
        }
        launchOp {
            engine.channelCreate(channelName, topic.trim(), private)
        }
    }

    fun joinChannel(channelId: String) {
        launchOp {
            engine.channelJoin(channelId)
        }
    }

    fun inviteMember(channelId: String, memberId: String) {
        launchOp {
            engine.channelInvite(channelId, memberId)
        }
    }

    fun leaveChannel(channelId: String) {
        launchOp {
            engine.channelLeave(channelId)
            _ui.update {
                it.copy(
                    messagesByConversation = Conversations.removeGroup(
                        it.messagesByConversation,
                        channelId,
                    ),
                )
            }
        }
    }

    fun refreshChannels() {
        launchOp {
            val channels = engine.channelList()
            _ui.update { it.copy(channels = channels) }
        }
    }

    fun acceptTransfer(transfer: Transfer) {
        launchOp {
            engine.acceptInbound(transfer.id, transfer.name)
            refresh()
        }
    }

    fun rejectTransfer(transfer: Transfer) {
        launchOp {
            engine.respondFile(transfer.id, false, "")
            refresh()
        }
    }

    fun cancelTransfer(transferId: String) {
        launchOp {
            engine.cancelFile(transferId)
            refresh()
        }
    }

    fun pauseTransfer(transferId: String) {
        launchOp {
            engine.pauseFile(transferId)
            refresh()
        }
    }

    fun resumeTransfer(transferId: String) {
        launchOp {
            engine.resumeFile(transferId)
            refresh()
        }
    }

    fun setNickname(name: String) {
        val trimmed = name.trim()
        if (trimmed.isEmpty()) {
            _ui.update { it.copy(error = "请输入有效昵称") }
            return
        }
        launchOp {
            engine.setNickname(trimmed)
            _ui.update { it.copy(nickname = trimmed) }
            refresh()
        }
    }

    fun pickDownloadDir(path: String) {
        val trimmed = path.trim()
        if (trimmed.isEmpty()) return
        launchOp {
            engine.pickDownloadDir(trimmed)
            _ui.update { it.copy(downloadDir = trimmed) }
        }
    }

    fun silentUpdate(customProxy: String = "", checkOnly: Boolean = false) {
        val proxyArg = customProxy.trim()
        val cmd = when {
            checkOnly -> "/update check"
            proxyArg.isEmpty() -> "/update"
            else -> "/update $proxyArg"
        }
        launchOp {
            val res = engine.sendText(to = "", text = cmd, group = "")
            val summary = res.removePrefix("update:")
            _ui.update { it.copy(error = "🚀 $summary") }
            refresh()
        }
    }

    fun clearUnread(conversationKey: String) {
        if (conversationKey.isEmpty()) return
        _ui.update { state ->
            if (conversationKey !in state.unreadCounts) state
            else state.copy(unreadCounts = state.unreadCounts - conversationKey)
        }
    }

    private fun handleEvent(event: CoreEvent) {
        when (event) {
            is CoreEvent.ConnChanged -> {
                _ui.update { it.copy(conn = event.state) }
                if (event.state == "connected") refresh()
                if (event.state == "auth_failed") {
                    _ui.update { it.copy(error = event.reason.ifEmpty { "口令错误，鉴权失败" }) }
                }
            }

            is CoreEvent.PeerJoined -> _ui.update { state ->
                state.copy(peers = state.peers.filter { it.id != event.peer.id } + event.peer)
            }

            is CoreEvent.PeerLeft -> _ui.update { state ->
                state.copy(peers = state.peers.filter { it.id != event.peerId })
            }

            is CoreEvent.Message -> {
                if (event.type == "MSG_REVOKE") {
                    _ui.update { state ->
                        state.copy(
                            messagesByConversation = Conversations.revokeMessage(
                                state.messagesByConversation,
                                event.msgId,
                            ),
                        )
                    }
                    return
                }
                if (event.type == "MSG_READ") {
                    _ui.update { state ->
                        state.copy(
                            messagesByConversation = Conversations.markRead(
                                state.messagesByConversation,
                                event.msgId,
                                event.from,
                            ),
                        )
                    }
                    return
                }
                if (event.type == "MSG_REACT" || event.type == "MSG_EDIT" || event.type == "MSG_PIN") {
                    _ui.update { state ->
                        state.copy(
                            messagesByConversation = Conversations.updateMessageText(
                                state.messagesByConversation,
                                event.msgId,
                                event.text,
                            ),
                        )
                    }
                    return
                }
                val group = event.group
                appendMessage(
                    if (group.isNotEmpty()) {
                        ChatMessage(
                            peerId = event.from,
                            msgId = event.msgId,
                            text = event.text,
                            inbound = true,
                            timestamp = System.currentTimeMillis(),
                            group = group,
                            senderId = event.from,
                        )
                    } else {
                        ChatMessage(
                            peerId = event.from,
                            msgId = event.msgId,
                            text = event.text,
                            inbound = true,
                            timestamp = System.currentTimeMillis(),
                        )
                    },
                )
            }

            is CoreEvent.Progress -> upsertTransfer(event.transfer)
            is CoreEvent.Done -> _ui.update { state ->
                state.copy(
                    transfers = state.transfers.map {
                        if (it.id == event.transferId) it.copy(state = "done") else it
                    },
                )
            }

            is CoreEvent.Failed -> _ui.update { state ->
                state.copy(
                    transfers = state.transfers.map {
                        if (it.id == event.transferId) {
                            it.copy(state = "failed", errorReason = event.reason)
                        } else {
                            it
                        }
                    },
                )
            }

            is CoreEvent.GroupMatrix -> _ui.update { state ->
                val size = state.transfers.firstOrNull { it.id == event.transferId }?.size ?: 0L
                val summary = formatMatrixSummary(event.haveBitmap, size)
                if (summary.isEmpty()) state
                else state.copy(matrixSummary = state.matrixSummary + (event.transferId to summary))
            }

            is CoreEvent.ChannelsUpdated -> refresh()
        }
    }

    private fun upsertTransfer(transfer: Transfer) {
        _ui.update { state ->
            val list = state.transfers.filter { it.id != transfer.id } + transfer
            state.copy(transfers = list.sortedBy { it.id })
        }
    }

    private fun appendMessage(message: ChatMessage) {
        _ui.update { state ->
            val key = Conversations.keyOf(message)
            val updatedUnread = if (message.inbound) {
                state.unreadCounts + (key to ((state.unreadCounts[key] ?: 0) + 1))
            } else {
                state.unreadCounts
            }
            state.copy(
                messagesByConversation = Conversations.append(
                    state.messagesByConversation,
                    message,
                ),
                unreadCounts = updatedUnread,
            )
        }
    }

    private fun launchOp(block: suspend () -> Unit) {
        viewModelScope.launch {
            _ui.update { it.copy(busy = true) }
            try {
                block()
            } catch (e: Exception) {
                _ui.update { it.copy(error = e.message ?: "操作失败") }
            } finally {
                _ui.update { it.copy(busy = false) }
            }
        }
    }
}
