package dev.lanchat.ui.screen

import android.graphics.BitmapFactory
import android.net.Uri
import android.util.Base64
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.PersonAdd
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import dev.lanchat.R
import dev.lanchat.model.Channel
import dev.lanchat.model.ChatMessage
import dev.lanchat.model.Conversations
import dev.lanchat.model.Peer
import dev.lanchat.ui.AppUi
import dev.lanchat.ui.AppViewModel
import dev.lanchat.ui.theme.QqAvatar
import dev.lanchat.ui.theme.QqOnlineGreen
import dev.lanchat.ui.theme.QqOutboundBubble
import dev.lanchat.ui.theme.QqOutboundText
import dev.lanchat.ui.theme.QqPrimaryBlue
import dev.lanchat.ui.theme.qqChatBackgroundColor
import dev.lanchat.ui.theme.qqHeaderBrush
import dev.lanchat.ui.theme.qqInboundBubbleColor
import java.io.File
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import kotlinx.coroutines.launch

private data class PresetSticker(val emoji: String, val label: String, val code: String)

private val PRESET_STICKERS = listOf(
    PresetSticker("👍", "点赞", "thumbsup.png"),
    PresetSticker("🎉", "庆祝", "party.png"),
    PresetSticker("❤️", "比心", "heart.png"),
    PresetSticker("🔥", "火热", "fire.png"),
    PresetSticker("🚀", "起飞", "rocket.png"),
    PresetSticker("😂", "笑哭", "laugh.png"),
    PresetSticker("👏", "鼓掌", "clap.png"),
    PresetSticker("✨", "闪耀", "sparkle.png"),
)

/** 1:1 单聊入口。 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ChatScreen(
    peer: Peer,
    state: AppUi,
    vm: AppViewModel,
    onBack: () -> Unit,
) {
    val sub = buildString {
        append("在线 · P2P 直连")
        if (peer.os.isNotEmpty()) append(" · ${peer.os}")
    }
    ConversationScaffold(
        title = peer.nickname.ifEmpty { peer.id },
        avatarSeed = peer.id,
        selfName = state.nickname.ifEmpty { "我" },
        selfSeed = state.selfId.ifEmpty { "self" },
        subtitle = sub,
        messages = Conversations.messagesOf(
            state.messagesByConversation,
            Conversations.peerKey(peer.id),
        ),
        onBack = onBack,
        onSend = { vm.sendText(peer.id, it) },
        onAttach = { path -> vm.offerFile(peer.id, path) },
        onSendSticker = { path -> vm.sendSticker(peer.id, path) },
        showSender = false,
        peerNameOf = { peer.nickname.ifEmpty { peer.id } },
    )
}

/** 频道群聊入口（含 G1 全员广播群 "*" 与 G2 自定义频道）。 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ChannelChatScreen(
    channel: Channel,
    state: AppUi,
    vm: AppViewModel,
    onBack: () -> Unit,
) {
    var showInvite by remember { mutableStateOf(false) }
    val isBroadcast = channel.id == "*"
    val isOwner = !isBroadcast && channel.ownerId == state.selfId && state.selfId.isNotEmpty()
    val sub = buildString {
        append("${channel.members.size.coerceAtLeast(1)} 人")
        if (channel.topic.isNotEmpty()) append(" · ${channel.topic}")
    }

    ConversationScaffold(
        title = channel.name.ifEmpty { channel.id },
        avatarSeed = channel.id,
        selfName = state.nickname.ifEmpty { "我" },
        selfSeed = state.selfId.ifEmpty { "self" },
        subtitle = sub,
        private = channel.private,
        messages = Conversations.messagesOf(
            state.messagesByConversation,
            Conversations.groupKey(channel.id),
        ),
        onBack = onBack,
        onSend = { vm.sendChannelText(channel.id, it) },
        onAttach = { path -> vm.offerFileToChannel(channel.id, path) },
        onSendSticker = { path -> vm.sendSticker(channel.id, path, group = channel.id) },
        showSender = true,
        peerNameOf = { id -> peerDisplayName(state, id) },
        largeFileHint = true,
        actions = {
            if (isOwner) {
                IconButton(onClick = { showInvite = true }) {
                    Icon(
                        Icons.Filled.PersonAdd,
                        contentDescription = stringResource(R.string.channel_invite),
                        tint = Color.White,
                    )
                }
            }
            if (!isBroadcast) {
                TextButton(
                    onClick = {
                        vm.leaveChannel(channel.id)
                        onBack()
                    },
                ) {
                    Text(
                        text = stringResource(R.string.channel_leave),
                        color = Color.White,
                        fontSize = 12.sp,
                    )
                }
            }
        },
    )

    if (showInvite) {
        InviteMembersDialog(
            channel = channel,
            peers = state.peers,
            busy = state.busy,
            onConfirm = { memberIds ->
                memberIds.forEach { vm.inviteMember(channel.id, it) }
                showInvite = false
            },
            onDismiss = { showInvite = false },
        )
    }
}

@OptIn(ExperimentalMaterial3Api::class, ExperimentalLayoutApi::class)
@Composable
private fun ConversationScaffold(
    title: String,
    avatarSeed: String = title,
    selfName: String = "我",
    selfSeed: String = "self",
    messages: List<ChatMessage>,
    onBack: () -> Unit,
    onSend: (String) -> Unit,
    onAttach: (String) -> Unit,
    onSendSticker: (String) -> Unit = {},
    showSender: Boolean,
    peerNameOf: @Composable (String) -> String,
    largeFileHint: Boolean = false,
    subtitle: String = "",
    private: Boolean = false,
    actions: @Composable androidx.compose.foundation.layout.RowScope.() -> Unit = {},
) {
    val context = LocalContext.current
    var input by remember { mutableStateOf("") }
    var replyTarget by remember { mutableStateOf<ChatMessage?>(null) }
    var showStickerPanel by remember { mutableStateOf(false) }
    var showE2eeDialog by remember { mutableStateOf(false) }
    var showSearchDialog by remember { mutableStateOf(false) }
    var e2eeActive by remember { mutableStateOf(false) }
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

    val stickerPicker = rememberLauncherForActivityResult(
        ActivityResultContracts.GetContent(),
    ) { uri: Uri? ->
        if (uri != null) {
            val staged = stageContentUri(context, uri)
            if (staged != null) {
                onSendSticker(staged.absolutePath)
                showStickerPanel = false
            }
        }
    }

    LaunchedEffect(messages.size) {
        if (messages.isNotEmpty()) listState.animateScrollToItem(messages.size - 1)
    }

    Scaffold(
        topBar = {
            // 手机 QQ 聊天顶部栏：返回按钮 + 目标圆形头像 + 昵称/频道名 + 在线副标题 + 快捷动作
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .background(qqHeaderBrush())
                    .statusBarsPadding()
                    .padding(horizontal = 8.dp, vertical = 10.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                IconButton(onClick = onBack) {
                    Icon(
                        Icons.AutoMirrored.Filled.ArrowBack,
                        contentDescription = stringResource(R.string.action_back),
                        tint = Color.White,
                    )
                }
                QqAvatar(
                    name = title,
                    seed = avatarSeed,
                    size = 36.dp,
                    fontSize = 15.sp,
                    showOnlineDot = true,
                    online = true,
                    containerColor = Color.White.copy(alpha = 0.24f),
                )
                Spacer(Modifier.width(10.dp))
                Column(modifier = Modifier.weight(1f)) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        if (private) {
                            Icon(
                                Icons.Filled.Lock,
                                contentDescription = stringResource(R.string.channel_private_icon),
                                tint = Color.White,
                                modifier = Modifier
                                    .size(15.dp)
                                    .padding(end = 4.dp),
                            )
                        }
                        Text(
                            text = title,
                            color = Color.White,
                            fontSize = 16.sp,
                            fontWeight = FontWeight.Bold,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                    if (subtitle.isNotEmpty()) {
                        Text(
                            text = subtitle,
                            color = Color.White.copy(alpha = 0.88f),
                            fontSize = 11.sp,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                }
                IconButton(onClick = { filePicker.launch("*/*") }) {
                    Icon(
                        Icons.Filled.Folder,
                        contentDescription = stringResource(R.string.action_send_file),
                        tint = Color.White,
                    )
                }
                actions()
            }
        },
        snackbarHost = { SnackbarHost(snackbarHostState) },
    ) { padding ->
        Column(
            modifier = Modifier
                .padding(padding)
                .fillMaxSize()
                .background(qqChatBackgroundColor()),
        ) {
            // 手机 QQ 消息气泡流列表 (#F2F4F7 背景)
            LazyColumn(
                state = listState,
                modifier = Modifier
                    .weight(1f)
                    .fillMaxWidth()
                    .padding(horizontal = 12.dp, vertical = 8.dp),
                verticalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                items(messages, key = { it.msgId }) { message ->
                    MessageBubbleRow(
                        message = message,
                        targetTitle = title,
                        targetSeed = avatarSeed,
                        selfName = selfName,
                        selfSeed = selfSeed,
                        showSender = showSender,
                        peerNameOf = peerNameOf,
                        onReply = { replyTarget = message },
                        onRevoke = { onSend("/revoke ${message.msgId}") },
                        onMarkRead = { onSend("/read ${message.msgId}") },
                        onReact = { onSend("/react ${message.msgId} 👍") },
                        onPin = { onSend("/pin ${message.msgId}") },
                    )
                }
            }

            // 引用回复预览条
            val activeReply = replyTarget
            if (activeReply != null) {
                Surface(
                    color = QqPrimaryBlue.copy(alpha = 0.10f),
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Row(
                        modifier = Modifier
                            .fillMaxWidth()
                            .padding(horizontal = 14.dp, vertical = 6.dp),
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.SpaceBetween,
                    ) {
                        Text(
                            text = "↩ 回复：「${activeReply.text}」",
                            fontSize = 12.sp,
                            color = QqPrimaryBlue,
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                            modifier = Modifier.weight(1f),
                        )
                        IconButton(
                            onClick = { replyTarget = null },
                            modifier = Modifier.size(20.dp),
                        ) {
                            Icon(
                                Icons.Filled.Close,
                                contentDescription = "取消回复",
                                tint = QqPrimaryBlue,
                                modifier = Modifier.size(16.dp),
                            )
                        }
                    }
                }
            }

            // 手机 QQ 底部输入栏 + 多功能工具图标行
            Surface(
                color = MaterialTheme.colorScheme.surfaceVariant,
                tonalElevation = 3.dp,
                modifier = Modifier
                    .fillMaxWidth()
                    .navigationBarsPadding(),
            ) {
                Column(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(horizontal = 10.dp, vertical = 8.dp),
                    verticalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    // 第一行：白色圆角输入框 + QQ 蓝胶囊「发送」按钮
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(8.dp),
                    ) {
                        Surface(
                            shape = RoundedCornerShape(22.dp),
                            color = MaterialTheme.colorScheme.surface,
                            modifier = Modifier
                                .weight(1f)
                                .height(42.dp),
                        ) {
                            Box(
                                modifier = Modifier
                                    .fillMaxSize()
                                    .padding(horizontal = 14.dp),
                                contentAlignment = Alignment.CenterStart,
                            ) {
                                if (input.isEmpty()) {
                                    Text(
                                        text = if (activeReply != null) {
                                            "输入回复内容…"
                                        } else {
                                            stringResource(R.string.chat_input_placeholder)
                                        },
                                        color = Color(0xFF9CA3AF),
                                        fontSize = 14.sp,
                                    )
                                }
                                BasicTextField(
                                    value = input,
                                    onValueChange = { input = it },
                                    maxLines = 3,
                                    textStyle = TextStyle(
                                        color = MaterialTheme.colorScheme.onSurface,
                                        fontSize = 14.sp,
                                    ),
                                    modifier = Modifier.fillMaxWidth(),
                                )
                            }
                        }

                        Button(
                            onClick = {
                                val raw = input.trim()
                                if (raw.isNotEmpty()) {
                                    val payload = if (activeReply != null && !raw.startsWith("/")) {
                                        "/reply ${activeReply.msgId} $raw"
                                    } else {
                                        raw
                                    }
                                    onSend(payload)
                                    input = ""
                                    replyTarget = null
                                }
                            },
                            enabled = input.isNotBlank(),
                            shape = RoundedCornerShape(50),
                            colors = ButtonDefaults.buttonColors(
                                containerColor = QqPrimaryBlue,
                                contentColor = Color.White,
                            ),
                            modifier = Modifier.height(40.dp),
                        ) {
                            Text(
                                text = stringResource(R.string.action_send),
                                fontWeight = FontWeight.Bold,
                                fontSize = 13.sp,
                            )
                        }
                    }

                    // 第二行：手机 QQ 底部四图标工具栏（😊 表情贴纸、📁 发送文件、🔐 E2EE 加密、🔍 搜索记录）
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.SpaceEvenly,
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        ComposerToolItem(
                            label = "😊 表情贴纸",
                            active = showStickerPanel,
                            onClick = { showStickerPanel = !showStickerPanel },
                        )
                        ComposerToolItem(
                            label = "📁 发送文件",
                            active = false,
                            onClick = { filePicker.launch("*/*") },
                        )
                        ComposerToolItem(
                            label = "🎤 语音",
                            active = false,
                            onClick = { onSend("/voice 3") },
                        )
                        ComposerToolItem(
                            label = if (e2eeActive) "🔐 E2EE 已开" else "🔐 E2EE 加密",
                            active = e2eeActive,
                            onClick = { showE2eeDialog = true },
                        )
                        ComposerToolItem(
                            label = "🛡️ 安全码",
                            active = false,
                            onClick = { onSend("/fingerprint") },
                        )
                        ComposerToolItem(
                            label = "🔍 搜索记录",
                            active = false,
                            onClick = { showSearchDialog = true },
                        )
                    }

                    // 可展开的表情贴纸网格面板
                    if (showStickerPanel) {
                        Surface(
                            shape = RoundedCornerShape(16.dp),
                            color = MaterialTheme.colorScheme.surface,
                            modifier = Modifier.fillMaxWidth(),
                        ) {
                            Column(
                                modifier = Modifier.padding(12.dp),
                                verticalArrangement = Arrangement.spacedBy(8.dp),
                            ) {
                                Row(
                                    modifier = Modifier.fillMaxWidth(),
                                    horizontalArrangement = Arrangement.SpaceBetween,
                                    verticalAlignment = Alignment.CenterVertically,
                                ) {
                                    Text(
                                        text = "QQ 表情贴纸",
                                        style = MaterialTheme.typography.labelMedium,
                                        fontWeight = FontWeight.Bold,
                                    )
                                    TextButton(onClick = { stickerPicker.launch("image/*") }) {
                                        Text("🖼️ 从相册选图发送", fontSize = 12.sp, color = QqPrimaryBlue)
                                    }
                                }
                                FlowRow(
                                    modifier = Modifier.fillMaxWidth(),
                                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                                    verticalArrangement = Arrangement.spacedBy(8.dp),
                                ) {
                                    PRESET_STICKERS.forEach { sticker ->
                                        Surface(
                                            shape = RoundedCornerShape(12.dp),
                                            color = QqPrimaryBlue.copy(alpha = 0.08f),
                                            modifier = Modifier.clickable {
                                                input += sticker.emoji
                                                showStickerPanel = false
                                            },
                                        ) {
                                            Row(
                                                modifier = Modifier.padding(horizontal = 10.dp, vertical = 6.dp),
                                                verticalAlignment = Alignment.CenterVertically,
                                            ) {
                                                Text(text = sticker.emoji, fontSize = 18.sp)
                                                Spacer(Modifier.width(4.dp))
                                                Text(text = sticker.label, fontSize = 12.sp)
                                            }
                                        }
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }
    }

    if (showE2eeDialog) {
        E2eeKeyDialog(
            active = e2eeActive,
            onDismiss = { showE2eeDialog = false },
            onEnable = { secret ->
                onSend("/e2ee $secret")
                e2eeActive = true
                showE2eeDialog = false
            },
            onDisable = {
                onSend("/e2ee off")
                e2eeActive = false
                showE2eeDialog = false
            },
        )
    }

    if (showSearchDialog) {
        SearchMessagesDialog(
            onDismiss = { showSearchDialog = false },
            onSearch = { q ->
                onSend("/search $q")
                showSearchDialog = false
            },
        )
    }
}

@Composable
private fun ComposerToolItem(
    label: String,
    active: Boolean,
    onClick: () -> Unit,
) {
    Surface(
        shape = RoundedCornerShape(50),
        color = if (active) QqPrimaryBlue.copy(alpha = 0.16f) else Color.Transparent,
        modifier = Modifier.clickable(onClick = onClick),
    ) {
        Text(
            text = label,
            color = if (active) QqPrimaryBlue else MaterialTheme.colorScheme.onSurfaceVariant,
            fontSize = 12.sp,
            fontWeight = if (active) FontWeight.Bold else FontWeight.Medium,
            modifier = Modifier.padding(horizontal = 10.dp, vertical = 5.dp),
        )
    }
}

@Composable
private fun MessageBubbleRow(
    message: ChatMessage,
    targetTitle: String,
    targetSeed: String,
    selfName: String,
    selfSeed: String,
    showSender: Boolean,
    peerNameOf: @Composable (String) -> String,
    onReply: () -> Unit,
    onRevoke: () -> Unit,
    onMarkRead: () -> Unit,
    onReact: () -> Unit = {},
    onPin: () -> Unit = {},
) {
    // 已撤回消息居中灰色提示条
    if (message.revoked) {
        Box(
            modifier = Modifier.fillMaxWidth(),
            contentAlignment = Alignment.Center,
        ) {
            Surface(
                shape = RoundedCornerShape(50),
                color = Color.Black.copy(alpha = 0.08f),
            ) {
                Text(
                    text = "一条消息已撤回",
                    fontSize = 11.sp,
                    fontStyle = FontStyle.Italic,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(horizontal = 12.dp, vertical = 4.dp),
                )
            }
        }
        return
    }

    val senderDisplay = if (message.inbound) {
        if (showSender && message.senderId.isNotEmpty()) peerNameOf(message.senderId) else targetTitle
    } else {
        selfName
    }
    val senderSeed = if (message.inbound) {
        message.senderId.ifEmpty { targetSeed }
    } else {
        selfSeed
    }
    val timeStr = formatBubbleTime(message.timestamp)

    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = if (message.inbound) Arrangement.Start else Arrangement.End,
        verticalAlignment = Alignment.Top,
    ) {
        // 接收方左侧 36.dp 圆形头像
        if (message.inbound) {
            QqAvatar(
                name = senderDisplay,
                seed = senderSeed,
                size = 36.dp,
                fontSize = 14.sp,
            )
            Spacer(Modifier.width(8.dp))
        }

        Column(
            horizontalAlignment = if (message.inbound) Alignment.Start else Alignment.End,
            modifier = Modifier.widthIn(max = 280.dp),
        ) {
            // 发送者昵称 + 时间 + 已读回执
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(6.dp),
            ) {
                if (message.inbound && showSender) {
                    Text(
                        text = senderDisplay,
                        fontSize = 11.sp,
                        fontWeight = FontWeight.SemiBold,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                if (timeStr.isNotEmpty()) {
                    Text(
                        text = timeStr,
                        fontSize = 10.sp,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                if (!message.inbound && message.readBy.isNotEmpty()) {
                    Text(
                        text = "✓ 已读",
                        fontSize = 10.sp,
                        color = QqOnlineGreen,
                        fontWeight = FontWeight.Bold,
                    )
                }
            }

            Spacer(Modifier.height(3.dp))

            // 手机 QQ 圆角气泡：入站白底深字，出站 #0099FF 蓝底白字
            val bubbleShape = if (message.inbound) {
                RoundedCornerShape(topStart = 4.dp, topEnd = 16.dp, bottomStart = 16.dp, bottomEnd = 16.dp)
            } else {
                RoundedCornerShape(topStart = 16.dp, topEnd = 4.dp, bottomStart = 16.dp, bottomEnd = 16.dp)
            }
            val bubbleBg = if (message.inbound) qqInboundBubbleColor() else QqOutboundBubble
            val bubbleText = if (message.inbound) MaterialTheme.colorScheme.onSurface else QqOutboundText

            val (cleanBody, reactions) = remember(message.text) { Conversations.splitReactions(message.text) }
            val inlineBitmap = remember(cleanBody) { decodeInlineBitmap(cleanBody) }

            Surface(
                shape = bubbleShape,
                color = bubbleBg,
                shadowElevation = 1.dp,
            ) {
                Column(modifier = Modifier.padding(horizontal = 13.dp, vertical = 9.dp)) {
                    if (inlineBitmap != null) {
                        Image(
                            bitmap = inlineBitmap,
                            contentDescription = "内联图片缩略图",
                            contentScale = ContentScale.Fit,
                            modifier = Modifier
                                .size(width = 180.dp, height = 130.dp)
                                .clip(RoundedCornerShape(10.dp)),
                        )
                        Spacer(Modifier.height(4.dp))
                        Text(
                            text = "🖼️ [图片缩略图]",
                            color = bubbleText,
                            fontSize = 12.sp,
                        )
                    } else {
                        Text(
                            text = cleanBody,
                            color = bubbleText,
                            fontSize = 15.sp,
                        )
                    }
                }
            }

            // 气泡下方：已触发的点赞/表情回应胶囊徽章行
            if (reactions.isNotEmpty()) {
                Row(
                    modifier = Modifier.padding(top = 4.dp),
                    horizontalArrangement = Arrangement.spacedBy(6.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    reactions.forEach { emoji ->
                        Surface(
                            shape = RoundedCornerShape(50),
                            color = QqPrimaryBlue.copy(alpha = 0.14f),
                            modifier = Modifier.clickable(onClick = onReact),
                        ) {
                            Text(
                                text = "$emoji 1",
                                fontSize = 11.sp,
                                fontWeight = FontWeight.SemiBold,
                                color = QqPrimaryBlue,
                                modifier = Modifier.padding(horizontal = 8.dp, vertical = 2.dp),
                            )
                        }
                    }
                }
            }

            // 气泡下方快捷操作微标签：👍 点赞 / ↩ 回复 / 📌 置顶 / 撤回 / 已读
            if (message.msgId.isNotEmpty() && !message.msgId.startsWith("search-") && !message.msgId.startsWith("e2ee-")) {
                Row(
                    modifier = Modifier.padding(top = 2.dp),
                    horizontalArrangement = Arrangement.spacedBy(10.dp),
                ) {
                    Text(
                        text = "👍 点赞",
                        fontSize = 10.sp,
                        color = QqPrimaryBlue,
                        fontWeight = FontWeight.SemiBold,
                        modifier = Modifier.clickable(onClick = onReact),
                    )
                    Text(
                        text = "↩ 回复",
                        fontSize = 10.sp,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.clickable(onClick = onReply),
                    )
                    Text(
                        text = "📌 置顶",
                        fontSize = 10.sp,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.clickable(onClick = onPin),
                    )
                    if (!message.inbound) {
                        Text(
                            text = "撤回",
                            fontSize = 10.sp,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.clickable(onClick = onRevoke),
                        )
                    } else {
                        Text(
                            text = "标为已读",
                            fontSize = 10.sp,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.clickable(onClick = onMarkRead),
                        )
                    }
                }
            }
        }

        // 发送方右侧 36.dp 圆形头像
        if (!message.inbound) {
            Spacer(Modifier.width(8.dp))
            QqAvatar(
                name = selfName,
                seed = selfSeed,
                size = 36.dp,
                fontSize = 14.sp,
                containerColor = QqPrimaryBlue,
            )
        }
    }
}

@Composable
private fun E2eeKeyDialog(
    active: Boolean,
    onDismiss: () -> Unit,
    onEnable: (String) -> Unit,
    onDisable: () -> Unit,
) {
    var key by remember { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("🔐 端到端加密 (E2EE)") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(
                    text = "输入会话共享密钥后，双方文本消息将使用 AES-256-GCM 端到端加密传输。",
                    style = MaterialTheme.typography.bodySmall,
                )
                OutlinedTextField(
                    value = key,
                    onValueChange = { key = it },
                    singleLine = true,
                    label = { Text("E2EE 会话密钥") },
                    placeholder = { Text("例如：top-secret-key") },
                )
            }
        },
        confirmButton = {
            TextButton(
                enabled = key.isNotBlank(),
                onClick = { onEnable(key.trim()) },
            ) { Text("开启加密") }
        },
        dismissButton = {
            Row {
                if (active) {
                    TextButton(onClick = onDisable) { Text("关闭加密") }
                }
                TextButton(onClick = onDismiss) { Text(stringResource(R.string.dialog_cancel)) }
            }
        },
    )
}

@Composable
private fun SearchMessagesDialog(
    onDismiss: () -> Unit,
    onSearch: (String) -> Unit,
) {
    var query by remember { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("🔍 搜索当前会话记录") },
        text = {
            OutlinedTextField(
                value = query,
                onValueChange = { query = it },
                singleLine = true,
                label = { Text("搜索关键字") },
            )
        },
        confirmButton = {
            TextButton(
                enabled = query.isNotBlank(),
                onClick = { onSearch(query.trim()) },
            ) { Text("搜索") }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.dialog_cancel)) }
        },
    )
}

private fun formatBubbleTime(ts: Long): String {
    if (ts <= 0L) return ""
    return SimpleDateFormat("HH:mm", Locale.getDefault()).format(Date(ts))
}

private fun decodeInlineBitmap(text: String): ImageBitmap? {
    val trimmed = text.trim()
    if (!trimmed.startsWith("data:image/", ignoreCase = true)) return null
    val marker = ";base64,"
    val idx = trimmed.indexOf(marker)
    if (idx < 0) return null
    val b64 = trimmed.substring(idx + marker.length).trim()
    if (b64.isEmpty()) return null
    return try {
        val raw = Base64.decode(b64, Base64.DEFAULT)
        BitmapFactory.decodeByteArray(raw, 0, raw.size)?.asImageBitmap()
    } catch (_: Exception) {
        null
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
