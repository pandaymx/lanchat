package dev.lanchat.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.lanchat.core.CoreEvent
import dev.lanchat.core.ServiceLocator
import dev.lanchat.model.Channel
import dev.lanchat.model.ChatMessage
import dev.lanchat.model.Conversations
import dev.lanchat.model.Peer
import dev.lanchat.model.ScreenShareState
import dev.lanchat.model.ServerInfo
import dev.lanchat.model.Transfer
import dev.lanchat.model.VoiceCallState
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
    val screenFramesByConversation: Map<String, String> = emptyMap(),
    val voiceCall: VoiceCallState? = null,
    val screenShare: ScreenShareState? = null,
    val autoClipboard: Boolean = false,
    val dndEnabled: Boolean = false,
    val discoveredServers: List<ServerInfo> = emptyList(),
    val pendingInstallApkPath: String? = null,
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

    fun consumePendingInstallApk() {
        _ui.update { it.copy(pendingInstallApkPath = null) }
    }

    fun clearScreenFrame(convKey: String) {
        _ui.update { it.copy(screenFramesByConversation = it.screenFramesByConversation - convKey) }
    }

    fun refresh() {
        launchOp {
            val state = engine.getState()
            _ui.update {
                var convs = it.messagesByConversation
                for (m in state.messages) {
                    convs = Conversations.append(convs, m)
                }
                var frames = it.screenFramesByConversation
                val ss = state.screenShare
                if (ss != null && ss.latestFrame.isNotEmpty()) {
                    val key = when {
                        ss.groupId.isNotEmpty() -> Conversations.groupKey(ss.groupId)
                        ss.peerId.isNotEmpty() -> Conversations.peerKey(ss.peerId)
                        else -> ""
                    }
                    if (key.isNotEmpty()) {
                        frames = frames + (key to ss.latestFrame)
                    }
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
                    screenFramesByConversation = frames,
                    voiceCall = state.voiceCall,
                    screenShare = state.screenShare,
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
            _ui.update { it.copy(error = "🔍 搜索“$q”：共 ${hits.size} 条匹配") }
            return
        }
        launchOp {
            val msgId = engine.sendText(to, content)
            if (msgId == "e2ee:on" || msgId == "e2ee:off") {
                val notice = if (msgId == "e2ee:on") "🔐 已开启端到端加密" else "🔐 已关闭端到端加密"
                _ui.update { it.copy(error = notice) }
                return@launchOp
            }
            if (msgId.startsWith("fp:")) {
                val fp = msgId.removePrefix("fp:")
                _ui.update { it.copy(error = "🛡️ 安全码指纹核验：$fp") }
                return@launchOp
            }
            if (msgId.startsWith("bwlimit:")) {
                val lim = msgId.removePrefix("bwlimit:")
                _ui.update { it.copy(error = "⚡ 带宽限速已设置为 $lim MB/s") }
                return@launchOp
            }
            if (msgId.startsWith("status:")) {
                val st = msgId.removePrefix("status:")
                _ui.update { it.copy(error = "📊 诊断状态：$st") }
                return@launchOp
            }
            if (msgId.startsWith("update:")) {
                val up = msgId.removePrefix("update:")
                _ui.update { it.copy(error = "🚀 版本更新：$up") }
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
            _ui.update { it.copy(error = "🔍 搜索“$q”：共 ${hits.size} 条匹配") }
            return
        }
        launchOp {
            val msgId = engine.sendText(to = "", text = content, group = channelId)
            if (msgId == "e2ee:on" || msgId == "e2ee:off") {
                val notice = if (msgId == "e2ee:on") "🔐 已开启端到端加密" else "🔐 已关闭端到端加密"
                _ui.update { it.copy(error = notice) }
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
            val channels = engine.channelList()
            _ui.update {
                it.copy(
                    channels = channels,
                    error = "✅ 已创建频道「$channelName」",
                )
            }
        }
    }

    fun joinChannel(channelId: String) {
        launchOp {
            engine.channelJoin(channelId)
            val channels = engine.channelList()
            _ui.update { it.copy(channels = channels) }
        }
    }

    fun inviteMember(channelId: String, memberId: String) {
        launchOp {
            engine.channelInvite(channelId, memberId)
            val channels = engine.channelList()
            _ui.update { it.copy(channels = channels) }
        }
    }

    fun leaveChannel(channelId: String) {
        launchOp {
            engine.channelLeave(channelId)
            val channels = engine.channelList()
            _ui.update {
                it.copy(
                    channels = channels,
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
            val state = engine.getState()
            val merged = if (state.channels.size >= channels.size) state.channels else channels
            _ui.update {
                it.copy(
                    channels = merged,
                    error = "🔄 频道列表已刷新（共 ${merged.size} 个频道）",
                )
            }
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
            val apkPath = if (!checkOnly && summary.contains("-> ")) {
                summary.substringAfterLast("-> ").substringBefore(" |").trim().takeIf {
                    it.endsWith(".apk", ignoreCase = true) && File(it).exists()
                }
            } else {
                null
            }
            _ui.update {
                it.copy(
                    error = "🚀 $summary",
                    pendingInstallApkPath = apkPath,
                )
            }
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

    fun startVoiceCall(to: String, group: String = "") {
        launchOp {
            engine.startVoiceCall(to, group)
            refresh()
        }
    }

    fun acceptVoiceCall(to: String, group: String = "", remoteUDP: String = "") {
        launchOp {
            engine.acceptVoiceCall(to, group, remoteUDP)
            refresh()
        }
    }

    fun stopVoiceCall(to: String, group: String = "") {
        launchOp {
            engine.stopVoiceCall(to, group)
            _ui.update { it.copy(voiceCall = VoiceCallState(state = "idle")) }
            refresh()
        }
    }

    fun setVoiceMuted(muted: Boolean) {
        launchOp {
            engine.setVoiceMuted(muted)
        }
    }

    fun startScreenShare(to: String, group: String = "", fps: Long = 15L) {
        launchOp {
            engine.startScreenShare(to, group, fps)
            refresh()
        }
    }

    fun stopScreenShare(to: String, group: String = "") {
        launchOp {
            engine.stopScreenShare(to, group)
            val convKey = if (group.isNotEmpty()) Conversations.groupKey(group) else Conversations.peerKey(to)
            _ui.update {
                it.copy(
                    screenShare = null,
                    screenFramesByConversation = it.screenFramesByConversation - convKey,
                )
            }
            refresh()
        }
    }

    fun sendScreenAnnotation(
        to: String,
        group: String = "",
        normX: Double,
        normY: Double,
        note: String = "",
    ) {
        launchOp {
            engine.sendScreenAnnotation(to, group, normX, normY, note)
        }
    }

    fun revokeMessage(to: String, group: String = "", msgID: String) {
        launchOp {
            engine.revokeMessage(to, group, msgID)
            _ui.update {
                it.copy(messagesByConversation = Conversations.revokeMessage(it.messagesByConversation, msgID))
            }
        }
    }

    fun markMessageRead(to: String, group: String = "", msgID: String) {
        launchOp {
            engine.markMessageRead(to, group, msgID)
            _ui.update {
                it.copy(messagesByConversation = Conversations.markRead(it.messagesByConversation, msgID, it.selfId))
            }
        }
    }

    fun reactMessage(to: String, group: String = "", msgID: String, emoji: String = "👍") {
        launchOp {
            engine.reactMessage(to, group, msgID, emoji)
            refresh()
        }
    }

    fun editMessage(to: String, group: String = "", msgID: String, newText: String) {
        launchOp {
            engine.editMessage(to, group, msgID, newText)
            refresh()
        }
    }

    fun pinMessage(to: String, group: String = "", msgID: String) {
        launchOp {
            engine.pinMessage(to, group, msgID)
            refresh()
        }
    }

    fun setSessionE2EE(secret: String) {
        launchOp {
            engine.setSessionE2EE(secret)
            val isOff = secret.isEmpty() || secret.equals("off", ignoreCase = true) || secret.equals("false", ignoreCase = true)
            val notice = if (isOff) "🔐 已关闭端到端加密" else "🔐 已开启端到端加密"
            _ui.update { it.copy(error = notice) }
        }
    }

    fun setAutoClipboard(enabled: Boolean) {
        launchOp {
            engine.setAutoClipboard(enabled)
            _ui.update {
                it.copy(
                    autoClipboard = enabled,
                    error = if (enabled) "📋 已开启跨设备剪贴板自动同步" else "📋 已关闭跨设备剪贴板自动同步",
                )
            }
        }
    }

    fun setDND(enabled: Boolean) {
        launchOp {
            engine.setDND(enabled)
            _ui.update {
                it.copy(
                    dndEnabled = enabled,
                    error = if (enabled) "🔕 已开启全局免打扰模式" else "🔔 已关闭全局免打扰模式",
                )
            }
        }
    }

    fun getHistoryPage(convID: String, beforeTS: Long = 0L, limit: Long = 50L) {
        launchOp {
            val history = engine.getHistoryPage(convID, beforeTS, limit, _ui.value.selfId)
            _ui.update { state ->
                var convs = state.messagesByConversation
                for (m in history) {
                    convs = Conversations.append(convs, m)
                }
                state.copy(messagesByConversation = convs)
            }
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
                if (event.type == "MSG_REVOKE" || event.type == "MSG_BURN") {
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
                if (event.type == "CLIP_SYNC") {
                    _ui.update { it.copy(error = event.text) }
                    return
                }
                if (event.type == "VOICE_CALL_OFFER") {
                    val remoteUDP = event.text.substringAfter("]").trim()
                    _ui.update {
                        it.copy(
                            voiceCall = VoiceCallState(
                                state = "ringing",
                                peerId = event.from,
                                groupId = event.group,
                                remoteAddr = remoteUDP,
                            ),
                        )
                    }
                    refresh()
                    return
                }
                if (event.type == "VOICE_CALL") {
                    if (event.text.contains("已结束")) {
                        _ui.update { it.copy(voiceCall = VoiceCallState(state = "idle")) }
                    } else {
                        val remoteUDP = event.text.substringAfter("]").trim()
                        _ui.update {
                            it.copy(
                                voiceCall = VoiceCallState(
                                    state = "active",
                                    peerId = event.from,
                                    groupId = event.group,
                                    remoteAddr = remoteUDP,
                                ),
                            )
                        }
                    }
                    refresh()
                    return
                }
                if (event.type == "SCREEN_FRAME") {
                    val convKey = if (event.group.isNotEmpty()) {
                        Conversations.groupKey(event.group)
                    } else {
                        Conversations.peerKey(event.from)
                    }
                    _ui.update { state ->
                        state.copy(
                            screenFramesByConversation = state.screenFramesByConversation + (convKey to event.text),
                        )
                    }
                    return
                }
                if (event.type == "SCREEN_SHARE" && event.text.contains("已结束")) {
                    val convKey = if (event.group.isNotEmpty()) {
                        Conversations.groupKey(event.group)
                    } else {
                        Conversations.peerKey(event.from)
                    }
                    _ui.update { state ->
                        state.copy(
                            screenFramesByConversation = state.screenFramesByConversation - convKey,
                        )
                    }
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

            is CoreEvent.ChannelsUpdated -> {
                _ui.update { it.copy(channels = event.channels) }
                refresh()
            }
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
            } catch (e: kotlin.coroutines.cancellation.CancellationException) {
                throw e
            } catch (t: Throwable) {
                _ui.update { it.copy(error = t.message ?: "操作失败") }
            } finally {
                _ui.update { it.copy(busy = false) }
            }
        }
    }
}
