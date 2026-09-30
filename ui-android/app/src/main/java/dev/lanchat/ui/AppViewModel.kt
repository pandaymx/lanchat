package dev.lanchat.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import dev.lanchat.core.CoreEvent
import dev.lanchat.core.ServiceLocator
import dev.lanchat.model.ChatMessage
import dev.lanchat.model.Peer
import dev.lanchat.model.ServerInfo
import dev.lanchat.model.Transfer
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
    val peers: List<Peer> = emptyList(),
    val transfers: List<Transfer> = emptyList(),
    val messagesByPeer: Map<String, List<ChatMessage>> = emptyMap(),
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
                it.copy(
                    conn = state.conn,
                    server = state.server,
                    selfId = state.selfId,
                    nickname = state.nickname,
                    peers = state.peers,
                    transfers = state.transfers,
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
        if (addr.isBlank()) {
            _ui.update { it.copy(error = "请输入服务器地址") }
            return
        }
        launchOp {
            engine.connect(addr.trim(), psk)
        }
    }

    fun sendText(to: String, text: String) {
        val content = text.trim()
        if (content.isEmpty()) return
        launchOp {
            val msgId = engine.sendText(to, content)
            appendMessage(
                ChatMessage(
                    peerId = to,
                    msgId = msgId,
                    text = content,
                    inbound = false,
                    timestamp = System.currentTimeMillis(),
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

            is CoreEvent.Message -> appendMessage(
                ChatMessage(
                    peerId = event.from,
                    msgId = event.msgId,
                    text = event.text,
                    inbound = true,
                    timestamp = System.currentTimeMillis(),
                ),
            )

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
            val list = (state.messagesByPeer[message.peerId] ?: emptyList()) + message
            state.copy(messagesByPeer = state.messagesByPeer + (message.peerId to list))
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
