package dev.lanchat.ui.screen

import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.Send
import androidx.compose.material.icons.filled.AttachFile
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.PersonAdd
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import dev.lanchat.R
import dev.lanchat.model.Channel
import dev.lanchat.model.ChatMessage
import dev.lanchat.model.Conversations
import dev.lanchat.model.Peer
import dev.lanchat.ui.AppUi
import dev.lanchat.ui.AppViewModel
import kotlinx.coroutines.launch
import java.io.File

/** 1:1 单聊入口。 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ChatScreen(
    peer: Peer,
    state: AppUi,
    vm: AppViewModel,
    onBack: () -> Unit,
) {
    ConversationScaffold(
        title = peer.nickname.ifEmpty { peer.id },
        messages = Conversations.messagesOf(
            state.messagesByConversation,
            Conversations.peerKey(peer.id),
        ),
        onBack = onBack,
        onSend = { vm.sendText(peer.id, it) },
        onAttach = { path -> vm.offerFile(peer.id, path) },
        showSender = false,
        peerNameOf = { "" },
    )
}

/** 频道群聊入口。 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ChannelChatScreen(
    channel: Channel,
    state: AppUi,
    vm: AppViewModel,
    onBack: () -> Unit,
) {
    var showInvite by remember { mutableStateOf(false) }
    val isOwner = channel.ownerId == state.selfId && state.selfId.isNotEmpty()

    ConversationScaffold(
        title = channel.name.ifEmpty { channel.id },
        subtitle = channel.topic,
        private = channel.private,
        messages = Conversations.messagesOf(
            state.messagesByConversation,
            Conversations.groupKey(channel.id),
        ),
        onBack = onBack,
        onSend = { vm.sendChannelText(channel.id, it) },
        onAttach = { path -> vm.offerFileToChannel(channel.id, path) },
        showSender = true,
        peerNameOf = { id -> peerDisplayName(state, id) },
        largeFileHint = true,
        actions = {
            if (isOwner) {
                IconButton(onClick = { showInvite = true }) {
                    Icon(
                        Icons.Filled.PersonAdd,
                        contentDescription = stringResource(R.string.channel_invite),
                    )
                }
            }
            TextButton(onClick = {
                vm.leaveChannel(channel.id)
                onBack()
            }) { Text(stringResource(R.string.channel_leave)) }
        },
    )

    if (showInvite) {
        InviteMembersDialog(
            channel = channel,
            peers = state.peers,
            busy = state.busy,
            onDismiss = { showInvite = false },
            onConfirm = { memberIds ->
                memberIds.forEach { vm.inviteMember(channel.id, it) }
                showInvite = false
            },
        )
    }
}

// TopAppBar 仍是 Material3 实验 API，调用方必须显式 opt-in，否则 release 编译报
// "This material API is experimental"。
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ConversationScaffold(
    title: String,
    messages: List<ChatMessage>,
    onBack: () -> Unit,
    onSend: (String) -> Unit,
    onAttach: (String) -> Unit,
    showSender: Boolean,
    peerNameOf: @Composable (String) -> String,
    largeFileHint: Boolean = false,
    subtitle: String = "",
    private: Boolean = false,
    actions: @Composable androidx.compose.foundation.layout.RowScope.() -> Unit = {},
) {
    val context = LocalContext.current
    var input by remember { mutableStateOf("") }
    val listState = rememberLazyListState()
    val snackbarHostState = remember { SnackbarHostState() }
    val scope = rememberCoroutineScope()

    val filePicker = rememberLauncherForActivityResult(
        ActivityResultContracts.GetContent(),
    ) { uri: Uri? ->
        if (uri != null) {
            val staged = stageContentUri(context, uri)
            if (staged != null) {
                onAttach(staged.absolutePath)
                if (largeFileHint && staged.length() >= LARGE_FILE_BYTES) {
                    scope.launch {
                        snackbarHostState.showSnackbar(
                            context.getString(R.string.channel_large_file_hint),
                        )
                    }
                }
            }
        }
    }

    LaunchedEffect(messages.size) {
        if (messages.isNotEmpty()) listState.animateScrollToItem(messages.size - 1)
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    Column {
                        Row(verticalAlignment = androidx.compose.ui.Alignment.CenterVertically) {
                            if (private) {
                                Icon(
                                    Icons.Filled.Lock,
                                    contentDescription = stringResource(R.string.channel_private_icon),
                                    modifier = Modifier.padding(end = 4.dp),
                                )
                            }
                            Text(title, maxLines = 1)
                        }
                        if (subtitle.isNotEmpty()) {
                            Text(
                                subtitle,
                                style = MaterialTheme.typography.labelSmall,
                                maxLines = 1,
                            )
                        }
                    }
                },
                navigationIcon = {
                    IconButton(onClick = onBack) {
                        Icon(
                            Icons.AutoMirrored.Filled.ArrowBack,
                            contentDescription = stringResource(R.string.action_back),
                        )
                    }
                },
                actions = actions,
            )
        },
        snackbarHost = { SnackbarHost(snackbarHostState) },
    ) { padding ->
        Column(
            modifier = Modifier
                .padding(padding)
                .fillMaxSize(),
        ) {
            LazyColumn(
                state = listState,
                modifier = Modifier
                    .weight(1f)
                    .fillMaxWidth()
                    .padding(horizontal = 12.dp),
                verticalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                items(messages, key = { it.msgId }) { message ->
                    MessageBubble(message, showSender, peerNameOf)
                }
            }

            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(8.dp),
                verticalAlignment = androidx.compose.ui.Alignment.CenterVertically,
            ) {
                IconButton(onClick = { filePicker.launch("*/*") }) {
                    Icon(
                        Icons.Filled.AttachFile,
                        contentDescription = stringResource(R.string.action_send_file),
                    )
                }
                OutlinedTextField(
                    value = input,
                    onValueChange = { input = it },
                    modifier = Modifier.weight(1f),
                    placeholder = { Text(stringResource(R.string.chat_input_placeholder)) },
                    maxLines = 4,
                )
                IconButton(
                    onClick = {
                        onSend(input)
                        input = ""
                    },
                    enabled = input.isNotBlank(),
                ) {
                    Icon(
                        Icons.AutoMirrored.Filled.Send,
                        contentDescription = stringResource(R.string.action_send),
                    )
                }
            }
        }
    }
}

@Composable
private fun MessageBubble(
    message: ChatMessage,
    showSender: Boolean,
    peerNameOf: @Composable (String) -> String,
) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = if (message.inbound) Arrangement.Start else Arrangement.End,
    ) {
        Card(
            colors = CardDefaults.cardColors(
                containerColor = if (message.inbound) {
                    MaterialTheme.colorScheme.surfaceVariant
                } else {
                    MaterialTheme.colorScheme.primaryContainer
                },
            ),
            modifier = Modifier.widthIn(max = 300.dp),
        ) {
            Column(modifier = Modifier.padding(horizontal = 12.dp, vertical = 8.dp)) {
                if (showSender && message.inbound && message.senderId.isNotEmpty()) {
                    Text(
                        peerNameOf(message.senderId),
                        style = MaterialTheme.typography.labelSmall,
                    )
                }
                Text(message.text)
            }
        }
    }
}

/** 触发群文件温和提示的阈值：512 MiB。 */
private const val LARGE_FILE_BYTES = 512L * 1024 * 1024

/** 解析成员 ID 的显示名：优先在线昵称，离线时退回短 ID。 */
fun peerDisplayName(state: AppUi, id: String): String {
    val peer = state.peers.firstOrNull { it.id == id }
    return peer?.nickname?.ifEmpty { id } ?: id
}

/**
 * 把 SAF 内容 URI 暂存到应用缓存目录，返回可被 Go 直接读取的文件路径。
 * 调用方负责后续清理（暂存文件位于缓存目录，系统可回收）。
 */
private fun stageContentUri(context: android.content.Context, uri: Uri): File? {
    val resolver = context.contentResolver
    val name = queryDisplayName(resolver, uri)?.replace('/', '_') ?: "lanchat-file"
    val target = File(context.cacheDir, "outgoing/${System.currentTimeMillis()}-$name")
    target.parentFile?.mkdirs()
    resolver.openInputStream(uri)?.use { input ->
        target.outputStream().use { output -> input.copyTo(output) }
    } ?: return null
    return target
}

private fun queryDisplayName(
    resolver: android.content.ContentResolver,
    uri: Uri,
): String? {
    var result: String? = null
    resolver.query(uri, null, null, null, null)?.use { cursor ->
        val index = cursor.getColumnIndex(android.provider.OpenableColumns.DISPLAY_NAME)
        if (index >= 0 && cursor.moveToFirst()) {
            result = cursor.getString(index)
        }
    }
    return result
}
