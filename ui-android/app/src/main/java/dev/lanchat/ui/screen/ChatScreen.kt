package dev.lanchat.ui.screen

import android.Manifest
import android.content.pm.PackageManager
import android.graphics.BitmapFactory
import android.media.AudioManager
import android.media.MediaPlayer
import android.media.MediaRecorder
import android.media.ToneGenerator
import android.net.Uri
import android.os.Build
import android.util.Base64
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.isImeVisible
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Folder
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.PersonAdd
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
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
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.core.content.ContextCompat
import dev.lanchat.R
import dev.lanchat.model.Channel
import dev.lanchat.model.ChatMessage
import dev.lanchat.model.Conversations
import dev.lanchat.model.Formatters
import dev.lanchat.model.Peer
import dev.lanchat.model.VoiceCallState
import dev.lanchat.ui.AppUi
import dev.lanchat.ui.AppViewModel
import dev.lanchat.ui.theme.QqAvatar
import dev.lanchat.ui.theme.QqOnlineGreen
import dev.lanchat.ui.theme.QqOutboundBubble
import dev.lanchat.ui.theme.QqOutboundText
import dev.lanchat.ui.theme.QqPrimaryBlue
import dev.lanchat.ui.theme.QqUnreadBadgeRed
import dev.lanchat.ui.theme.qqChatBackgroundColor
import dev.lanchat.ui.theme.qqHeaderBrush
import dev.lanchat.ui.theme.qqInboundBubbleColor
import java.io.File
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale
import kotlinx.coroutines.delay
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
    val linkBadge = Formatters.linkBadge(state.server, peer.status)
    val sub = buildString {
        append("在线 · $linkBadge")
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
        voiceCall = state.voiceCall,
        screenFrame = state.screenFramesByConversation[Conversations.peerKey(peer.id)]
            ?: state.screenShare?.latestFrame?.takeIf { it.isNotEmpty() && state.screenShare.peerId == peer.id },
        onClearScreenFrame = { vm.clearScreenFrame(Conversations.peerKey(peer.id)) },
        onStartVoiceCall = { vm.startVoiceCall(peer.id) },
        onAcceptVoiceCall = { callerId, remoteUDP ->
            vm.acceptVoiceCall(callerId.ifEmpty { peer.id }, remoteUDP = remoteUDP)
        },
        onStopVoiceCall = { callerId -> vm.stopVoiceCall(callerId.ifEmpty { peer.id }) },
        onSetVoiceMuted = { muted -> vm.setVoiceMuted(muted) },
        onStartScreenShare = { vm.startScreenShare(peer.id, fps = 15L) },
        onStopScreenShare = { vm.stopScreenShare(peer.id) },
        onAnnotateScreen = { nx, ny, note ->
            vm.sendScreenAnnotation(peer.id, normX = nx, normY = ny, note = note)
        },
        onRevokeMessage = { msgId -> vm.revokeMessage(peer.id, msgID = msgId) },
        onMarkMessageRead = { msgId -> vm.markMessageRead(peer.id, msgID = msgId) },
        onReactMessage = { msgId, emoji -> vm.reactMessage(peer.id, msgID = msgId, emoji = emoji) },
        onEditMessage = { msgId, newText -> vm.editMessage(peer.id, msgID = msgId, newText = newText) },
        onPinMessage = { msgId -> vm.pinMessage(peer.id, msgID = msgId) },
        onSetSessionE2EE = { secret -> vm.setSessionE2EE(secret) },
        onLoadHistoryPage = { beforeTs -> vm.getHistoryPage(peer.id, beforeTS = beforeTs, limit = 50L) },
        onSyncHistory = { vm.syncHistoryWithPeer(peer.id) },
        onBack = onBack,
        onSend = { vm.sendText(peer.id, it) },
        onAttach = { path -> vm.offerFile(peer.id, path) },
        onSendSticker = { path -> vm.sendSticker(peer.id, path) },
        showSender = false,
        peerNameOf = { id -> peerDisplayName(state, id).ifEmpty { peer.nickname.ifEmpty { peer.id } } },
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
    val linkBadge = Formatters.linkBadge(state.server)
    val sub = buildString {
        append("${channel.members.size.coerceAtLeast(1)} 人 · $linkBadge")
        if (channel.topic.isNotEmpty()) append(" · ${channel.topic}")
    }

    ConversationScaffold(
        title = channel.name.ifEmpty { channel.id },
        avatarLabel = if (isBroadcast) "群" else channel.name.ifEmpty { channel.id },
        avatarSeed = channel.id,
        selfName = state.nickname.ifEmpty { "我" },
        selfSeed = state.selfId.ifEmpty { "self" },
        subtitle = sub,
        private = channel.private,
        messages = Conversations.messagesOf(
            state.messagesByConversation,
            Conversations.groupKey(channel.id),
        ),
        voiceCall = state.voiceCall,
        screenFrame = state.screenFramesByConversation[Conversations.groupKey(channel.id)]
            ?: state.screenShare?.latestFrame?.takeIf { it.isNotEmpty() && state.screenShare.groupId == channel.id },
        onClearScreenFrame = { vm.clearScreenFrame(Conversations.groupKey(channel.id)) },
        onStartVoiceCall = { vm.startVoiceCall("", group = channel.id) },
        onAcceptVoiceCall = { callerId, remoteUDP ->
            vm.acceptVoiceCall(callerId, group = channel.id, remoteUDP = remoteUDP)
        },
        onStopVoiceCall = { callerId -> vm.stopVoiceCall(callerId, group = channel.id) },
        onSetVoiceMuted = { muted -> vm.setVoiceMuted(muted) },
        onStartScreenShare = { vm.startScreenShare("", group = channel.id, fps = 15L) },
        onStopScreenShare = { vm.stopScreenShare("", group = channel.id) },
        onAnnotateScreen = { nx, ny, note ->
            vm.sendScreenAnnotation("", group = channel.id, normX = nx, normY = ny, note = note)
        },
        onRevokeMessage = { msgId -> vm.revokeMessage("", group = channel.id, msgID = msgId) },
        onMarkMessageRead = { msgId -> vm.markMessageRead("", group = channel.id, msgID = msgId) },
        onReactMessage = { msgId, emoji -> vm.reactMessage("", group = channel.id, msgID = msgId, emoji = emoji) },
        onEditMessage = { msgId, newText -> vm.editMessage("", group = channel.id, msgID = msgId, newText = newText) },
        onPinMessage = { msgId -> vm.pinMessage("", group = channel.id, msgID = msgId) },
        onSetSessionE2EE = { secret -> vm.setSessionE2EE(secret) },
        onLoadHistoryPage = { beforeTs -> vm.getHistoryPage(channel.id, beforeTS = beforeTs, limit = 50L) },
        onSyncHistory = { vm.syncHistoryWithPeer("", group = channel.id) },
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
    avatarLabel: String = title,
    avatarSeed: String = title,
    selfName: String = "我",
    selfSeed: String = "self",
    messages: List<ChatMessage>,
    voiceCall: VoiceCallState? = null,
    screenFrame: String? = null,
    onClearScreenFrame: () -> Unit = {},
    onStartVoiceCall: () -> Unit = {},
    onAcceptVoiceCall: (String, String) -> Unit = { _, _ -> },
    onStopVoiceCall: (String) -> Unit = {},
    onSetVoiceMuted: (Boolean) -> Unit = {},
    onStartScreenShare: () -> Unit = {},
    onStopScreenShare: () -> Unit = {},
    onAnnotateScreen: (Double, Double, String) -> Unit = { _, _, _ -> },
    onRevokeMessage: (String) -> Unit = {},
    onMarkMessageRead: (String) -> Unit = {},
    onReactMessage: (String, String) -> Unit = { _, _ -> },
    onEditMessage: (String, String) -> Unit = { _, _ -> },
    onPinMessage: (String) -> Unit = {},
    onSetSessionE2EE: (String) -> Unit = {},
    onLoadHistoryPage: (Long) -> Unit = {},
    onSyncHistory: () -> Unit = {},
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
    val clipboard = LocalClipboardManager.current
    var input by remember { mutableStateOf("") }
    var replyTarget by remember { mutableStateOf<ChatMessage?>(null) }
    var editTarget by remember { mutableStateOf<ChatMessage?>(null) }
    var showStickerPanel by remember { mutableStateOf(false) }
    var showVoicePanel by remember { mutableStateOf(false) }
    var isRecording by remember { mutableStateOf(false) }
    var recordingSec by remember { mutableIntStateOf(0) }
    var recorderRef by remember { mutableStateOf<MediaRecorder?>(null) }
    var recordFileRef by remember { mutableStateOf<File?>(null) }
    var recordStartMs by remember { mutableLongStateOf(0L) }
    var playingMsgId by remember { mutableStateOf<String?>(null) }
    var mediaPlayerRef by remember { mutableStateOf<MediaPlayer?>(null) }
    var showE2eeDialog by remember { mutableStateOf(false) }
    var showSearchDialog by remember { mutableStateOf(false) }
    var e2eeActive by remember { mutableStateOf(false) }
    var inVoiceCall by remember { mutableStateOf(false) }
    var callMuted by remember { mutableStateOf(false) }
    var callDurationSec by remember { mutableIntStateOf(0) }
    var isScreenSharing by remember { mutableStateOf(false) }
    var lightboxBitmap by remember { mutableStateOf<ImageBitmap?>(null) }
    var lastAutoPagedTs by remember { mutableLongStateOf(0L) }
    val listState = rememberLazyListState()
    val snackbarHostState = remember { SnackbarHostState() }
    val scope = rememberCoroutineScope()

    fun releaseActiveRecorder(deleteFile: Boolean) {
        val rec = recorderRef
        val f = recordFileRef
        recorderRef = null
        recordFileRef = null
        isRecording = false
        try {
            rec?.stop()
        } catch (_: Exception) {
        } finally {
            try {
                rec?.release()
            } catch (_: Exception) {
            }
        }
        if (deleteFile) {
            f?.delete()
        }
    }

    fun releaseActivePlayer() {
        val mp = mediaPlayerRef
        mediaPlayerRef = null
        playingMsgId = null
        try {
            mp?.stop()
        } catch (_: Exception) {
        } finally {
            try {
                mp?.release()
            } catch (_: Exception) {
            }
        }
    }

    DisposableEffect(Unit) {
        onDispose {
            releaseActiveRecorder(deleteFile = true)
            releaseActivePlayer()
        }
    }

    BackHandler {
        when {
            isRecording -> releaseActiveRecorder(deleteFile = true)
            showStickerPanel || showVoicePanel -> {
                showStickerPanel = false
                showVoicePanel = false
            }
            else -> onBack()
        }
    }

    LaunchedEffect(isRecording) {
        if (isRecording) {
            recordingSec = 0
            while (isRecording) {
                delay(1000L)
                recordingSec += 1
            }
        }
    }

    LaunchedEffect(inVoiceCall) {
        if (inVoiceCall) {
            callDurationSec = 0
            while (inVoiceCall) {
                delay(1000L)
                callDurationSec += 1
            }
        }
    }

    fun startVoiceRecording() {
        releaseActivePlayer()
        releaseActiveRecorder(deleteFile = true)
        try {
            val outDir = File(context.cacheDir, "voice_rec").apply { mkdirs() }
            val outFile = File(outDir, "rec_${System.currentTimeMillis()}.m4a")
            val rec = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
                MediaRecorder(context)
            } else {
                @Suppress("DEPRECATION")
                MediaRecorder()
            }
            rec.setAudioSource(MediaRecorder.AudioSource.MIC)
            rec.setOutputFormat(MediaRecorder.OutputFormat.MPEG_4)
            rec.setAudioEncoder(MediaRecorder.AudioEncoder.AAC)
            rec.setAudioSamplingRate(16000)
            rec.setAudioEncodingBitRate(24000)
            rec.setAudioChannels(1)
            rec.setOutputFile(outFile.absolutePath)
            rec.prepare()
            rec.start()
            recorderRef = rec
            recordFileRef = outFile
            recordStartMs = System.currentTimeMillis()
            isRecording = true
        } catch (e: Exception) {
            isRecording = false
            scope.launch {
                snackbarHostState.showSnackbar("无法启动麦克风录音：${e.message ?: "请检查麦克风权限"}")
            }
        }
    }

    fun finishVoiceRecording(send: Boolean) {
        val rec = recorderRef
        val outFile = recordFileRef
        val elapsedSec = ((System.currentTimeMillis() - recordStartMs) / 1000L).toInt().coerceAtLeast(1)
        recorderRef = null
        recordFileRef = null
        isRecording = false
        try {
            rec?.stop()
        } catch (_: Exception) {
        } finally {
            try {
                rec?.release()
            } catch (_: Exception) {
            }
        }
        if (send && outFile != null && outFile.exists() && outFile.length() > 0L) {
            val bytes = runCatching { outFile.readBytes() }.getOrNull()
            outFile.delete()
            if (bytes != null && bytes.isNotEmpty() && bytes.size <= 512 * 1024) {
                val b64 = Base64.encodeToString(bytes, Base64.NO_WRAP)
                onSend("🎤 [语音消息 ${elapsedSec}\"] data:audio/mp4;base64,$b64")
            } else {
                onSend("/voice $elapsedSec")
            }
            showVoicePanel = false
        } else {
            outFile?.delete()
        }
    }

    fun playVoiceClip(msgId: String, clip: VoiceClip) {
        if (playingMsgId == msgId) {
            releaseActivePlayer()
            return
        }
        releaseActivePlayer()
        val audioBytes = clip.audioBytes
        if (audioBytes != null && audioBytes.isNotEmpty()) {
            try {
                val playDir = File(context.cacheDir, "voice_play").apply { mkdirs() }
                val playFile = File(playDir, "play_${msgId.hashCode()}.m4a")
                playFile.writeBytes(audioBytes)
                val mp = MediaPlayer()
                mp.setDataSource(playFile.absolutePath)
                mp.setOnCompletionListener {
                    releaseActivePlayer()
                    playFile.delete()
                }
                mp.setOnErrorListener { _, _, _ ->
                    releaseActivePlayer()
                    playFile.delete()
                    true
                }
                mp.prepare()
                mp.start()
                mediaPlayerRef = mp
                playingMsgId = msgId
            } catch (e: Exception) {
                releaseActivePlayer()
                scope.launch {
                    snackbarHostState.showSnackbar("语音播放失败：${e.message ?: "音频格式不支持"}")
                }
            }
        } else {
            playingMsgId = msgId
            scope.launch {
                runCatching {
                    val tg = ToneGenerator(AudioManager.STREAM_MUSIC, 80)
                    tg.startTone(ToneGenerator.TONE_PROP_BEEP, 180)
                    delay(220L)
                    tg.release()
                }
                snackbarHostState.showSnackbar("🔊 已播放语音消息 (${clip.durationSec}\")")
                playingMsgId = null
            }
        }
    }

    val audioPermLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { granted ->
        if (granted) {
            startVoiceRecording()
        } else {
            scope.launch {
                snackbarHostState.showSnackbar("需要授予麦克风权限才能录制语音消息")
            }
        }
    }

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

    val screenFramePicker = rememberLauncherForActivityResult(
        ActivityResultContracts.GetContent(),
    ) { uri: Uri? ->
        if (uri != null) {
            val staged = stageContentUri(context, uri)
            if (staged != null) {
                isScreenSharing = true
                onSend("/screenshare snap ${staged.absolutePath}")
            }
        }
    }

    val multiAlbumPicker = rememberLauncherForActivityResult(
        ActivityResultContracts.GetMultipleContents(),
    ) { uris: List<Uri> ->
        if (uris.isNotEmpty()) {
            val stagedPaths = uris.take(9).mapNotNull { u ->
                stageContentUri(context, u)?.absolutePath
            }
            if (stagedPaths.isNotEmpty()) {
                onSend("/album ${stagedPaths.joinToString(",")}")
                stagedPaths.forEach { p -> onSendSticker(p) }
            }
        }
    }

    val imeVisible = WindowInsets.isImeVisible

    LaunchedEffect(messages.lastOrNull()?.msgId, imeVisible) {
        if (messages.isNotEmpty()) listState.animateScrollToItem(messages.size - 1)
    }

    // 向上滑到顶部时自动无缝分页加载更早历史消息并保持滚动锚点（v7.1.0）
    LaunchedEffect(listState.firstVisibleItemIndex) {
        if (listState.firstVisibleItemIndex == 0 && messages.size >= 15) {
            val earliestTs = messages.first().timestamp
            if (earliestTs > 0L && earliestTs != lastAutoPagedTs) {
                lastAutoPagedTs = earliestTs
                onLoadHistoryPage(earliestTs)
            }
        }
    }

    LaunchedEffect(imeVisible) {
        if (imeVisible) {
            showStickerPanel = false
            if (!isRecording) {
                showVoicePanel = false
            }
        }
    }

    Scaffold(
        contentWindowInsets = WindowInsets(0, 0, 0, 0),
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
                    name = avatarLabel,
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
                val isCallActive = inVoiceCall || (voiceCall?.state in listOf("calling", "active", "in_call"))
                TextButton(
                    onClick = {
                        if (isCallActive || voiceCall?.state == "ringing") {
                            inVoiceCall = false
                            callMuted = false
                            onStopVoiceCall(voiceCall?.peerId.orEmpty())
                        } else {
                            inVoiceCall = true
                            callMuted = false
                            onStartVoiceCall()
                        }
                    },
                    contentPadding = PaddingValues(horizontal = 8.dp, vertical = 4.dp),
                ) {
                    Text(
                        text = if (isCallActive) "📵 挂断" else "📞 语音",
                        color = if (isCallActive) Color(0xFFFFD6D6) else Color.White,
                        fontSize = 12.sp,
                        fontWeight = FontWeight.SemiBold,
                        maxLines = 1,
                        softWrap = false,
                    )
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
            val callState = voiceCall?.state.orEmpty()
            val isRinging = callState == "ringing"
            val isCallActiveBanner = inVoiceCall || callState in listOf("ringing", "calling", "active", "in_call")
            if (isCallActiveBanner) {
                val callerId = voiceCall?.peerId?.takeIf { it.isNotEmpty() } ?: avatarSeed
                val callerTitle = peerNameOf(callerId).ifEmpty { title }
                val mm = (callDurationSec / 60).toString().padStart(2, '0')
                val ss = (callDurationSec % 60).toString().padStart(2, '0')
                Surface(
                    color = QqOnlineGreen.copy(alpha = 0.14f),
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Row(
                        modifier = Modifier
                            .fillMaxWidth()
                            .padding(horizontal = 14.dp, vertical = 8.dp),
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.SpaceBetween,
                    ) {
                        Row(
                            verticalAlignment = Alignment.CenterVertically,
                            modifier = Modifier.weight(1f),
                        ) {
                            QqAvatar(
                                name = callerTitle,
                                seed = callerId,
                                size = 26.dp,
                                fontSize = 11.sp,
                                showOnlineDot = true,
                                online = true,
                            )
                            Spacer(Modifier.width(8.dp))
                            Text(
                                text = if (isRinging) {
                                    "🔔 [来电响铃] $callerTitle 邀请你进行实时低延迟语音通话"
                                } else {
                                    "📞 局域网实时语音中 · $callerTitle · $mm:$ss" + if (callMuted) " (已静音)" else ""
                                },
                                fontSize = 12.sp,
                                fontWeight = FontWeight.SemiBold,
                                color = Color(0xFF15803D),
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                            )
                        }
                        Row(
                            horizontalArrangement = Arrangement.spacedBy(8.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            if (isRinging) {
                                Button(
                                    onClick = {
                                        inVoiceCall = true
                                        callMuted = false
                                        onAcceptVoiceCall(callerId, voiceCall?.remoteAddr.orEmpty())
                                    },
                                    shape = RoundedCornerShape(16.dp),
                                    colors = ButtonDefaults.buttonColors(
                                        containerColor = QqOnlineGreen,
                                        contentColor = Color.White,
                                    ),
                                    contentPadding = PaddingValues(horizontal = 12.dp, vertical = 2.dp),
                                    modifier = Modifier.height(28.dp),
                                ) {
                                    Text(
                                        text = "🟢 接听",
                                        fontSize = 11.sp,
                                        fontWeight = FontWeight.Bold,
                                    )
                                }
                            } else {
                                OutlinedButton(
                                    onClick = {
                                        callMuted = !callMuted
                                        onSetVoiceMuted(callMuted)
                                    },
                                    shape = RoundedCornerShape(16.dp),
                                    contentPadding = PaddingValues(horizontal = 10.dp, vertical = 2.dp),
                                    modifier = Modifier.height(28.dp),
                                ) {
                                    Text(
                                        text = if (callMuted) "🎙️ 开麦" else "🔇 静音",
                                        fontSize = 11.sp,
                                    )
                                }
                            }
                            Button(
                                onClick = {
                                    inVoiceCall = false
                                    callMuted = false
                                    onSetVoiceMuted(false)
                                    onStopVoiceCall(callerId)
                                },
                                shape = RoundedCornerShape(16.dp),
                                colors = ButtonDefaults.buttonColors(
                                    containerColor = QqUnreadBadgeRed,
                                    contentColor = Color.White,
                                ),
                                contentPadding = PaddingValues(horizontal = 12.dp, vertical = 2.dp),
                                modifier = Modifier.height(28.dp),
                            ) {
                                Text(
                                    text = "🔴 挂断",
                                    fontSize = 11.sp,
                                    fontWeight = FontWeight.Bold,
                                )
                            }
                        }
                    }
                }
            }

            // 手机 QQ 消息气泡流列表 (#F2F4F7 背景)
            if (isScreenSharing || !screenFrame.isNullOrEmpty()) {
                val decodedFrameBitmap = remember(screenFrame) {
                    screenFrame?.let { decodeInlineBitmap(it) }
                }
                Surface(
                    color = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.95f),
                    tonalElevation = 4.dp,
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Column(
                        modifier = Modifier
                            .fillMaxWidth()
                            .padding(horizontal = 12.dp, vertical = 8.dp),
                        verticalArrangement = Arrangement.spacedBy(6.dp),
                    ) {
                        Row(
                            modifier = Modifier.fillMaxWidth(),
                            verticalAlignment = Alignment.CenterVertically,
                            horizontalArrangement = Arrangement.SpaceBetween,
                        ) {
                            Text(
                                text = if (decodedFrameBitmap != null) {
                                    "🖥️ 正在接收 P2P 实时屏幕共享画布 · 点击画面发送激光笔批注"
                                } else {
                                    "🖥️ 已开启 P2P 屏幕共享（15fps）"
                                },
                                fontSize = 12.sp,
                                fontWeight = FontWeight.SemiBold,
                                color = QqPrimaryBlue,
                                modifier = Modifier.weight(1f),
                            )
                            Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                                OutlinedButton(
                                    onClick = { onAnnotateScreen(0.50, 0.50, "激光笔标记重点") },
                                    shape = RoundedCornerShape(14.dp),
                                    contentPadding = PaddingValues(horizontal = 8.dp, vertical = 2.dp),
                                    modifier = Modifier.height(26.dp),
                                ) {
                                    Text("📍 批注", fontSize = 11.sp)
                                }
                                OutlinedButton(
                                    onClick = { screenFramePicker.launch("image/*") },
                                    shape = RoundedCornerShape(14.dp),
                                    contentPadding = PaddingValues(horizontal = 10.dp, vertical = 2.dp),
                                    modifier = Modifier.height(26.dp),
                                ) {
                                    Text("📸 推送画面", fontSize = 11.sp)
                                }
                                Button(
                                    onClick = {
                                        if (isScreenSharing) {
                                            isScreenSharing = false
                                        }
                                        onStopScreenShare()
                                        onClearScreenFrame()
                                    },
                                    shape = RoundedCornerShape(14.dp),
                                    colors = ButtonDefaults.buttonColors(
                                        containerColor = QqUnreadBadgeRed,
                                        contentColor = Color.White,
                                    ),
                                    contentPadding = PaddingValues(horizontal = 10.dp, vertical = 2.dp),
                                    modifier = Modifier.height(26.dp),
                                ) {
                                    Text("⏹ 关闭投屏", fontSize = 11.sp)
                                }
                            }
                        }
                        if (decodedFrameBitmap != null) {
                            Image(
                                bitmap = decodedFrameBitmap,
                                contentDescription = "P2P 屏幕共享画面",
                                contentScale = ContentScale.Fit,
                                modifier = Modifier
                                    .fillMaxWidth()
                                    .heightIn(min = 120.dp, max = 240.dp)
                                    .clip(RoundedCornerShape(12.dp))
                                    .background(Color.Black)
                                    .pointerInput(Unit) {
                                        detectTapGestures { offset ->
                                            val nx = (offset.x / size.width.coerceAtLeast(1)).toDouble().coerceIn(0.0, 1.0)
                                            val ny = (offset.y / size.height.coerceAtLeast(1)).toDouble().coerceIn(0.0, 1.0)
                                            onAnnotateScreen(nx, ny, "🔴 激光笔点击批注")
                                        }
                                    },
                            )
                        }
                    }
                }
            }

            LazyColumn(
                state = listState,
                modifier = Modifier
                    .weight(1f)
                    .fillMaxWidth()
                    .padding(horizontal = 12.dp, vertical = 8.dp),
                verticalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                if (messages.isNotEmpty()) {
                    item(key = "history-load-more") {
                        Box(
                            modifier = Modifier.fillMaxWidth(),
                            contentAlignment = Alignment.Center,
                        ) {
                            OutlinedButton(
                                onClick = { onLoadHistoryPage(messages.first().timestamp) },
                                shape = RoundedCornerShape(50),
                                contentPadding = PaddingValues(horizontal = 14.dp, vertical = 2.dp),
                                modifier = Modifier.height(28.dp),
                            ) {
                                Text("📜 加载更早历史消息", fontSize = 11.sp)
                            }
                        }
                    }
                }
                items(messages, key = { it.msgId }) { message ->
                    MessageBubbleRow(
                        message = message,
                        targetTitle = title,
                        targetSeed = avatarSeed,
                        selfName = selfName,
                        selfSeed = selfSeed,
                        showSender = showSender,
                        peerNameOf = peerNameOf,
                        isPlayingVoice = playingMsgId == message.msgId,
                        onPlayVoice = { clip -> playVoiceClip(message.msgId, clip) },
                        onPreviewImage = { bmp -> lightboxBitmap = bmp },
                        onReply = { replyTarget = message },
                        onRevoke = { onRevokeMessage(message.msgId) },
                        onMarkRead = { onMarkMessageRead(message.msgId) },
                        onReact = { onReactMessage(message.msgId, "👍") },
                        onEdit = { editTarget = message },
                        onPin = { onPinMessage(message.msgId) },
                        onReEdit = { original ->
                            val clean = Conversations.extractEditableOriginal(original)
                            if (clean.isNotEmpty()) {
                                input = clean
                            }
                        },
                        onCopy = { text ->
                            clipboard.setText(AnnotatedString(text))
                            scope.launch {
                                snackbarHostState.showSnackbar("已复制到剪贴板")
                            }
                        },
                    )
                }
            }

            // 引用回复预览条（手机 QQ 样式）
            val activeReply = replyTarget
            if (activeReply != null) {
                val replyClean = remember(activeReply.text) {
                    Conversations.stripInlineMediaPayload(
                        Conversations.splitQuote(Conversations.splitReactions(activeReply.text).first).second,
                    )
                }
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
                            text = "回复：$replyClean",
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

            // 手机 QQ 底部输入栏 + 多功能工具图标行（随软键盘自动平滑抬升，不遮挡输入框与工具栏）
            Surface(
                color = MaterialTheme.colorScheme.surfaceVariant,
                tonalElevation = 3.dp,
                shadowElevation = 6.dp,
                modifier = Modifier
                    .fillMaxWidth()
                    .navigationBarsPadding()
                    .imePadding(),
            ) {
                Column(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(horizontal = 10.dp, vertical = 8.dp),
                    verticalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    // 第一行：圆角输入框 + QQ 蓝胶囊「发送」按钮
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        verticalAlignment = Alignment.Bottom,
                        horizontalArrangement = Arrangement.spacedBy(8.dp),
                    ) {
                        Surface(
                            shape = RoundedCornerShape(21.dp),
                            color = MaterialTheme.colorScheme.surface,
                            border = BorderStroke(
                                width = 1.dp,
                                color = if (input.isNotEmpty()) {
                                    QqPrimaryBlue.copy(alpha = 0.55f)
                                } else {
                                    QqPrimaryBlue.copy(alpha = 0.25f)
                                },
                            ),
                            modifier = Modifier
                                .weight(1f)
                                .heightIn(min = 42.dp, max = 108.dp),
                        ) {
                            BasicTextField(
                                value = input,
                                onValueChange = { input = it },
                                maxLines = 4,
                                cursorBrush = SolidColor(QqPrimaryBlue),
                                textStyle = TextStyle(
                                    color = MaterialTheme.colorScheme.onSurface,
                                    fontSize = 15.sp,
                                    lineHeight = 20.sp,
                                ),
                                modifier = Modifier
                                    .fillMaxWidth()
                                    .padding(horizontal = 14.dp, vertical = 10.dp),
                                decorationBox = { innerTextField ->
                                    Box(
                                        modifier = Modifier.fillMaxWidth(),
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
                                                fontSize = 15.sp,
                                                lineHeight = 20.sp,
                                            )
                                        }
                                        innerTextField()
                                    }
                                },
                            )
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
                            shape = RoundedCornerShape(21.dp),
                            contentPadding = PaddingValues(horizontal = 18.dp, vertical = 0.dp),
                            colors = ButtonDefaults.buttonColors(
                                containerColor = QqPrimaryBlue,
                                contentColor = Color.White,
                                disabledContainerColor = QqPrimaryBlue.copy(alpha = 0.42f),
                                disabledContentColor = Color.White.copy(alpha = 0.72f),
                            ),
                            modifier = Modifier.height(42.dp),
                        ) {
                            Text(
                                text = stringResource(R.string.action_send),
                                fontWeight = FontWeight.Bold,
                                fontSize = 14.sp,
                            )
                        }
                    }

                    // 第二行：手机 QQ 底部轻量工具栏（😊 表情、📁 文件、🎤 语音、🔐 加密、🛡️ 安全码、🔍 记录）
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.SpaceBetween,
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        ComposerToolItem(
                            label = "😊 表情",
                            active = showStickerPanel,
                            onClick = {
                                showStickerPanel = !showStickerPanel
                                if (showStickerPanel && !isRecording) {
                                    showVoicePanel = false
                                }
                            },
                        )
                        ComposerToolItem(
                            label = "🖼️ 多图",
                            active = false,
                            onClick = { multiAlbumPicker.launch("image/*") },
                        )
                        ComposerToolItem(
                            label = "📁 文件",
                            active = false,
                            onClick = { filePicker.launch("*/*") },
                        )
                        ComposerToolItem(
                            label = if (isRecording) "🔴 ${recordingSec}\"" else "🎤 语音",
                            active = showVoicePanel || isRecording,
                            onClick = {
                                showVoicePanel = !showVoicePanel
                                if (showVoicePanel) {
                                    showStickerPanel = false
                                } else if (isRecording) {
                                    finishVoiceRecording(send = false)
                                }
                            },
                        )
                        ComposerToolItem(
                            label = if (e2eeActive) "🔐 已开" else "🔐 加密",
                            active = e2eeActive,
                            onClick = { showE2eeDialog = true },
                        )
                        ComposerToolItem(
                            label = if (isScreenSharing) "🖥️ 投屏中" else "🖥️ 投屏",
                            active = isScreenSharing,
                            onClick = {
                                if (isScreenSharing) {
                                    isScreenSharing = false
                                    onStopScreenShare()
                                } else {
                                    isScreenSharing = true
                                    onStartScreenShare()
                                }
                            },
                        )
                        ComposerToolItem(
                            label = "🔍 记录",
                            active = false,
                            onClick = { showSearchDialog = true },
                        )
                    }

                    // 可展开的手机 QQ 语音录制面板（支持麦克风真实 AAC 录音、实时计时与一键发送/取消）
                    if (showVoicePanel) {
                        Surface(
                            shape = RoundedCornerShape(16.dp),
                            color = MaterialTheme.colorScheme.surface,
                            border = BorderStroke(1.dp, QqPrimaryBlue.copy(alpha = 0.22f)),
                            modifier = Modifier.fillMaxWidth(),
                        ) {
                            Column(
                                modifier = Modifier
                                    .fillMaxWidth()
                                    .padding(horizontal = 16.dp, vertical = 14.dp),
                                horizontalAlignment = Alignment.CenterHorizontally,
                                verticalArrangement = Arrangement.spacedBy(12.dp),
                            ) {
                                Text(
                                    text = if (isRecording) {
                                        "🔴 正在录音 ${recordingSec}\" · ▂ ▄ ▆ █ ▆ ▄ ▂"
                                    } else {
                                        "🎤 点击下方按钮录制真实语音消息"
                                    },
                                    color = if (isRecording) QqUnreadBadgeRed else MaterialTheme.colorScheme.onSurfaceVariant,
                                    fontSize = 13.sp,
                                    fontWeight = FontWeight.SemiBold,
                                )

                                Row(
                                    verticalAlignment = Alignment.CenterVertically,
                                    horizontalArrangement = Arrangement.spacedBy(20.dp),
                                ) {
                                    if (isRecording) {
                                        OutlinedButton(
                                            onClick = { finishVoiceRecording(send = false) },
                                            shape = RoundedCornerShape(50),
                                        ) {
                                            Text("取消录音", fontSize = 13.sp)
                                        }
                                        Box(
                                            modifier = Modifier
                                                .size(68.dp)
                                                .clip(CircleShape)
                                                .background(QqUnreadBadgeRed)
                                                .clickable { finishVoiceRecording(send = true) },
                                            contentAlignment = Alignment.Center,
                                        ) {
                                            Column(horizontalAlignment = Alignment.CenterHorizontally) {
                                                Text("⏹️", fontSize = 20.sp)
                                                Text(
                                                    text = "发送",
                                                    color = Color.White,
                                                    fontSize = 12.sp,
                                                    fontWeight = FontWeight.Bold,
                                                )
                                            }
                                        }
                                    } else {
                                        Box(
                                            modifier = Modifier
                                                .size(68.dp)
                                                .clip(CircleShape)
                                                .background(QqPrimaryBlue)
                                                .clickable {
                                                    val hasPerm = ContextCompat.checkSelfPermission(
                                                        context,
                                                        Manifest.permission.RECORD_AUDIO,
                                                    ) == PackageManager.PERMISSION_GRANTED
                                                    if (hasPerm) {
                                                        startVoiceRecording()
                                                    } else {
                                                        audioPermLauncher.launch(Manifest.permission.RECORD_AUDIO)
                                                    }
                                                },
                                            contentAlignment = Alignment.Center,
                                        ) {
                                            Column(horizontalAlignment = Alignment.CenterHorizontally) {
                                                Text("🎤", fontSize = 22.sp)
                                                Text(
                                                    text = "开始录音",
                                                    color = Color.White,
                                                    fontSize = 11.sp,
                                                    fontWeight = FontWeight.Bold,
                                                )
                                            }
                                        }
                                    }
                                }
                            }
                        }
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
                onSetSessionE2EE(secret)
                e2eeActive = true
                showE2eeDialog = false
            },
            onDisable = {
                onSetSessionE2EE("off")
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
            onSyncHistory = {
                onSyncHistory()
                showSearchDialog = false
            },
        )
    }

    val activeEdit = editTarget
    if (activeEdit != null) {
        EditMessageDialog(
            initialText = Conversations.extractEditableOriginal(activeEdit.text),
            onDismiss = { editTarget = null },
            onConfirm = { newText ->
                onEditMessage(activeEdit.msgId, newText)
                editTarget = null
            },
        )
    }

    val previewBmp = lightboxBitmap
    if (previewBmp != null) {
        AlertDialog(
            onDismissRequest = { lightboxBitmap = null },
            title = { Text("🖼️ 原图灯箱预览") },
            text = {
                Image(
                    bitmap = previewBmp,
                    contentDescription = "原图灯箱预览",
                    contentScale = ContentScale.Fit,
                    modifier = Modifier
                        .fillMaxWidth()
                        .heightIn(min = 180.dp, max = 420.dp)
                        .clip(RoundedCornerShape(12.dp))
                        .background(Color.Black),
                )
            },
            confirmButton = {
                TextButton(onClick = { lightboxBitmap = null }) {
                    Text("关闭")
                }
            },
        )
    }
}

@Composable
private fun EditMessageDialog(
    initialText: String,
    onDismiss: () -> Unit,
    onConfirm: (String) -> Unit,
) {
    var draft by remember(initialText) { mutableStateOf(initialText) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("✏️ 编辑已发送消息") },
        text = {
            OutlinedTextField(
                value = draft,
                onValueChange = { draft = it },
                maxLines = 4,
                label = { Text("修改消息内容") },
            )
        },
        confirmButton = {
            TextButton(
                enabled = draft.isNotBlank(),
                onClick = { onConfirm(draft.trim()) },
            ) { Text("保存修改") }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.dialog_cancel)) }
        },
    )
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
            fontSize = 11.sp,
            fontWeight = if (active) FontWeight.Bold else FontWeight.Medium,
            modifier = Modifier.padding(horizontal = 6.dp, vertical = 4.dp),
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
    onEdit: () -> Unit = {},
    onPin: () -> Unit = {},
    onReEdit: (String) -> Unit = {},
    onCopy: (String) -> Unit = {},
    isPlayingVoice: Boolean = false,
    onPlayVoice: (VoiceClip) -> Unit = {},
    onPreviewImage: (ImageBitmap) -> Unit = {},
) {
    val senderDisplay = if (message.inbound) {
        if (showSender && message.senderId.isNotEmpty()) peerNameOf(message.senderId) else targetTitle
    } else {
        selfName
    }

    // 已撤回消息：复刻手机 QQ 居中灰色系统提示胶囊 + 「重新编辑」蓝色链接
    if (message.revoked) {
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .padding(vertical = 2.dp),
            contentAlignment = Alignment.Center,
        ) {
            Surface(
                shape = RoundedCornerShape(8.dp),
                color = MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.85f),
            ) {
                Row(
                    modifier = Modifier.padding(horizontal = 12.dp, vertical = 5.dp),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(6.dp),
                ) {
                    Text(
                        text = if (message.inbound) {
                            "\"$senderDisplay\" 撤回了一条消息"
                        } else {
                            "你撤回了一条消息"
                        },
                        fontSize = 12.sp,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    if (!message.inbound && message.originalText.isNotBlank()) {
                        Text(
                            text = "重新编辑",
                            fontSize = 12.sp,
                            fontWeight = FontWeight.SemiBold,
                            color = QqPrimaryBlue,
                            modifier = Modifier.clickable { onReEdit(message.originalText) },
                        )
                    }
                }
            }
        }
        return
    }

    val senderSeed = if (message.inbound) {
        message.senderId.ifEmpty { targetSeed }
    } else {
        selfSeed
    }
    val timeStr = formatBubbleTime(message.timestamp)
    var showMenu by remember { mutableStateOf(false) }

    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = if (message.inbound) Arrangement.Start else Arrangement.End,
        verticalAlignment = Alignment.Top,
    ) {
        // 接收方左侧 40.dp 圆形头像（与气泡顶部平齐）
        if (message.inbound) {
            QqAvatar(
                name = senderDisplay,
                seed = senderSeed,
                size = 40.dp,
                fontSize = 15.sp,
            )
            Spacer(Modifier.width(8.dp))
        }

        Column(
            horizontalAlignment = if (message.inbound) Alignment.Start else Alignment.End,
        ) {
            // 仅在群聊入站消息时展示发送者昵称，避免单独一行时间把气泡顶到头像下方造成错位
            if (message.inbound && showSender) {
                Text(
                    text = senderDisplay,
                    fontSize = 11.sp,
                    fontWeight = FontWeight.SemiBold,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.padding(start = 2.dp, bottom = 3.dp),
                )
            }

            // 手机 QQ 圆角气泡：入站白/深灰底，出站 #0099FF 蓝底白字；与 40.dp 头像等高对齐
            val bubbleShape = if (message.inbound) {
                RoundedCornerShape(topStart = 4.dp, topEnd = 16.dp, bottomStart = 16.dp, bottomEnd = 16.dp)
            } else {
                RoundedCornerShape(topStart = 16.dp, topEnd = 4.dp, bottomStart = 16.dp, bottomEnd = 16.dp)
            }
            val bubbleBg = if (message.inbound) qqInboundBubbleColor() else QqOutboundBubble
            val bubbleText = if (message.inbound) MaterialTheme.colorScheme.onSurface else QqOutboundText

            val (cleanBody, reactions) = remember(message.text) { Conversations.splitReactions(message.text) }
            val (quotedText, bodyText) = remember(cleanBody) { Conversations.splitQuote(cleanBody) }
            val inlineBitmap = remember(cleanBody) { decodeInlineBitmap(cleanBody) }
            val voiceClip = remember(bodyText) { parseVoiceClip(bodyText) }
            val displayBodyText = remember(bodyText) { Conversations.stripInlineMediaPayload(bodyText) }
            val canAct = message.msgId.isNotEmpty() &&
                !message.msgId.startsWith("search-") &&
                !message.msgId.startsWith("e2ee-")

            Row(
                verticalAlignment = Alignment.Bottom,
                horizontalArrangement = Arrangement.spacedBy(6.dp),
            ) {
                // 出站消息：已读回执与时间显示在气泡左下侧，不挤压气泡与头像垂直对齐
                if (!message.inbound && (timeStr.isNotEmpty() || message.readBy.isNotEmpty())) {
                    Column(
                        horizontalAlignment = Alignment.End,
                        modifier = Modifier.padding(bottom = 2.dp),
                    ) {
                        if (message.readBy.isNotEmpty()) {
                            Text(
                                text = "✓ 已读",
                                fontSize = 10.sp,
                                lineHeight = 12.sp,
                                color = QqOnlineGreen,
                                fontWeight = FontWeight.Bold,
                            )
                        }
                        if (timeStr.isNotEmpty()) {
                            Text(
                                text = timeStr,
                                fontSize = 10.sp,
                                lineHeight = 12.sp,
                                color = MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.78f),
                            )
                        }
                    }
                }

                Box(modifier = Modifier.widthIn(max = 250.dp)) {
                    Surface(
                        shape = bubbleShape,
                        color = bubbleBg,
                        shadowElevation = 1.dp,
                        border = if (message.inbound) {
                            BorderStroke(0.5.dp, QqPrimaryBlue.copy(alpha = 0.16f))
                        } else {
                            null
                        },
                        modifier = Modifier
                            .heightIn(min = 40.dp)
                            .then(
                                when {
                                    voiceClip != null -> Modifier.clickable { onPlayVoice(voiceClip) }
                                    canAct -> Modifier.clickable { showMenu = true }
                                    else -> Modifier
                                },
                            ),
                    ) {
                        Column(
                            modifier = Modifier.padding(horizontal = 14.dp, vertical = 9.dp),
                            verticalArrangement = Arrangement.Center,
                        ) {
                            // 手机 QQ 气泡内嵌引用回复卡片
                            if (!quotedText.isNullOrBlank()) {
                                Surface(
                                    shape = RoundedCornerShape(6.dp),
                                    color = if (message.inbound) {
                                        Color.Black.copy(alpha = 0.08f)
                                    } else {
                                        Color.White.copy(alpha = 0.16f)
                                    },
                                    modifier = Modifier.padding(bottom = 6.dp),
                                ) {
                                    Row(
                                        modifier = Modifier.padding(horizontal = 8.dp, vertical = 5.dp),
                                        verticalAlignment = Alignment.CenterVertically,
                                    ) {
                                        Box(
                                            modifier = Modifier
                                                .width(3.dp)
                                                .height(14.dp)
                                                .background(
                                                    color = if (message.inbound) {
                                                        QqPrimaryBlue
                                                    } else {
                                                        Color.White.copy(alpha = 0.78f)
                                                    },
                                                    shape = RoundedCornerShape(2.dp),
                                                ),
                                        )
                                        Spacer(Modifier.width(6.dp))
                                        Text(
                                            text = quotedText,
                                            color = if (message.inbound) {
                                                MaterialTheme.colorScheme.onSurfaceVariant
                                            } else {
                                                Color.White.copy(alpha = 0.90f)
                                            },
                                            fontSize = 12.sp,
                                            maxLines = 2,
                                            overflow = TextOverflow.Ellipsis,
                                        )
                                    }
                                }
                            }

                            if (inlineBitmap != null) {
                                Image(
                                    bitmap = inlineBitmap,
                                    contentDescription = "内联图片缩略图（点击放大预览）",
                                    contentScale = ContentScale.Fit,
                                    modifier = Modifier
                                        .size(width = 180.dp, height = 130.dp)
                                        .clip(RoundedCornerShape(10.dp))
                                        .clickable { onPreviewImage(inlineBitmap) },
                                )
                                Spacer(Modifier.height(4.dp))
                                Text(
                                    text = "🖼️ [图片 · 点击放大]",
                                    color = bubbleText,
                                    fontSize = 12.sp,
                                )
                            } else if (voiceClip != null) {
                                Column(verticalArrangement = Arrangement.spacedBy(4.dp)) {
                                    Row(
                                        verticalAlignment = Alignment.CenterVertically,
                                        horizontalArrangement = Arrangement.spacedBy(8.dp),
                                    ) {
                                        Text(
                                            text = if (isPlayingVoice) "🔊" else "▶️",
                                            fontSize = 15.sp,
                                            color = bubbleText,
                                        )
                                        Text(
                                            text = if (isPlayingVoice) {
                                                "播放中 ▂▄▆█▄▂ ${voiceClip.durationSec}\""
                                            } else {
                                                "🎤 语音 ▂▄▆█▄▂ ${voiceClip.durationSec}\""
                                            },
                                            color = bubbleText,
                                            fontSize = 14.sp,
                                            fontWeight = FontWeight.Medium,
                                            lineHeight = 18.sp,
                                        )
                                        if (canAct) {
                                            Surface(
                                                shape = CircleShape,
                                                color = if (message.inbound) {
                                                    MaterialTheme.colorScheme.onSurface.copy(alpha = 0.08f)
                                                } else {
                                                    Color.White.copy(alpha = 0.18f)
                                                },
                                                modifier = Modifier.clickable { showMenu = true },
                                            ) {
                                                Text(
                                                    text = "⋯",
                                                    color = bubbleText,
                                                    fontSize = 12.sp,
                                                    fontWeight = FontWeight.Bold,
                                                    modifier = Modifier.padding(horizontal = 6.dp, vertical = 1.dp),
                                                )
                                            }
                                        }
                                    }
                                    Box(
                                        modifier = Modifier
                                            .width(126.dp)
                                            .height(4.dp)
                                            .clip(RoundedCornerShape(2.dp))
                                            .background(bubbleText.copy(alpha = 0.25f)),
                                    ) {
                                        Box(
                                            modifier = Modifier
                                                .fillMaxWidth(if (isPlayingVoice) 0.72f else 0.18f)
                                                .height(4.dp)
                                                .background(bubbleText),
                                        )
                                    }
                                }
                            } else if (displayBodyText.contains("```")) {
                                val rawCode = remember(displayBodyText) {
                                    displayBodyText
                                        .substringAfter("```")
                                        .substringBeforeLast("```")
                                        .trimStart('\r', '\n')
                                        .trimEnd()
                                }
                                Surface(
                                    shape = RoundedCornerShape(8.dp),
                                    color = Color(0xFF1E293B),
                                    modifier = Modifier.fillMaxWidth(),
                                ) {
                                    Column(
                                        modifier = Modifier.padding(horizontal = 10.dp, vertical = 8.dp),
                                        verticalArrangement = Arrangement.spacedBy(6.dp),
                                    ) {
                                        Row(
                                            modifier = Modifier.fillMaxWidth(),
                                            horizontalArrangement = Arrangement.SpaceBetween,
                                            verticalAlignment = Alignment.CenterVertically,
                                        ) {
                                            Text(
                                                text = "💻 代码片段",
                                                color = Color(0xFF94A3B8),
                                                fontSize = 11.sp,
                                                fontWeight = FontWeight.SemiBold,
                                            )
                                            Text(
                                                text = "📋 复制代码",
                                                color = Color(0xFF38BDF8),
                                                fontSize = 11.sp,
                                                fontWeight = FontWeight.SemiBold,
                                                modifier = Modifier.clickable { onCopy(rawCode) },
                                            )
                                        }
                                        Text(
                                            text = rawCode,
                                            color = Color(0xFFF8FAFC),
                                            fontSize = 12.sp,
                                            fontFamily = FontFamily.Monospace,
                                            lineHeight = 17.sp,
                                        )
                                    }
                                }
                            } else {
                                Text(
                                    text = displayBodyText,
                                    color = bubbleText,
                                    fontSize = 15.sp,
                                    lineHeight = 20.sp,
                                )
                            }
                        }
                    }

                    // 手机 QQ 点击气泡弹出菜单（播放语音 / 点赞回应 / 引用回复 / 复制文本 / 置顶消息 / 撤回或已读）
                    DropdownMenu(
                        expanded = showMenu,
                        onDismissRequest = { showMenu = false },
                    ) {
                        if (voiceClip != null) {
                            DropdownMenuItem(
                                text = { Text(if (isPlayingVoice) "⏹️ 停止播放" else "🔊 播放语音") },
                                onClick = {
                                    showMenu = false
                                    onPlayVoice(voiceClip)
                                },
                            )
                        }
                        DropdownMenuItem(
                            text = { Text("👍 点赞回应") },
                            onClick = {
                                showMenu = false
                                onReact()
                            },
                        )
                        DropdownMenuItem(
                            text = { Text("💬 引用回复") },
                            onClick = {
                                showMenu = false
                                onReply()
                            },
                        )
                        DropdownMenuItem(
                            text = { Text("📋 复制文本") },
                            onClick = {
                                showMenu = false
                                onCopy(displayBodyText)
                            },
                        )
                        DropdownMenuItem(
                            text = { Text("📌 置顶消息") },
                            onClick = {
                                showMenu = false
                                onPin()
                            },
                        )
                        if (!message.inbound) {
                            DropdownMenuItem(
                                text = { Text("✏️ 编辑消息") },
                                onClick = {
                                    showMenu = false
                                    onEdit()
                                },
                            )
                            DropdownMenuItem(
                                text = { Text("↩️ 撤回消息") },
                                onClick = {
                                    showMenu = false
                                    onRevoke()
                                },
                            )
                        } else {
                            DropdownMenuItem(
                                text = { Text("✓ 标为已读") },
                                onClick = {
                                    showMenu = false
                                    onMarkRead()
                                },
                            )
                        }
                    }
                }

                // 入站消息：时间显示在气泡右下侧
                if (message.inbound && timeStr.isNotEmpty()) {
                    Text(
                        text = timeStr,
                        fontSize = 10.sp,
                        lineHeight = 12.sp,
                        color = MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.78f),
                        modifier = Modifier.padding(bottom = 2.dp),
                    )
                }
            }

            // 气泡下方：仅在存在点赞/表情回应时展示 QQ 胶囊徽章行
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
        }

        // 发送方右侧 40.dp 圆形头像（与气泡顶部平齐）
        if (!message.inbound) {
            Spacer(Modifier.width(8.dp))
            QqAvatar(
                name = selfName,
                seed = selfSeed,
                size = 40.dp,
                fontSize = 15.sp,
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
    onSyncHistory: () -> Unit = {},
) {
    var query by remember { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("🔍 搜索记录 & 多端漫游同步") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(
                    value = query,
                    onValueChange = { query = it },
                    singleLine = true,
                    label = { Text("搜索关键字") },
                )
                OutlinedButton(
                    onClick = onSyncHistory,
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Text("🔄 从对端同步历史消息 (/sync)")
                }
            }
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

private data class VoiceClip(
    val displayLabel: String,
    val durationSec: Int,
    val audioBytes: ByteArray?,
)

private val VOICE_DURATION_REGEX = Regex("""(\d+)\s*["s秒]""")

private fun parseVoiceClip(text: String): VoiceClip? {
    val trimmed = text.trim()
    if (!trimmed.startsWith("🎤 [语音消息")) return null
    val cleanLabel = Conversations.stripInlineMediaPayload(trimmed)
    val durationSec = VOICE_DURATION_REGEX.find(cleanLabel)
        ?.groupValues
        ?.getOrNull(1)
        ?.toIntOrNull()
        ?.coerceIn(1, 300)
        ?: 3
    val audioIdx = trimmed.indexOf("data:audio/", ignoreCase = true)
    val audioBytes = if (audioIdx >= 0) {
        val marker = ";base64,"
        val b64Idx = trimmed.indexOf(marker, startIndex = audioIdx)
        if (b64Idx >= 0) {
            val b64 = trimmed.substring(b64Idx + marker.length).trim()
            try {
                if (b64.isNotEmpty()) Base64.decode(b64, Base64.DEFAULT) else null
            } catch (_: Exception) {
                null
            }
        } else {
            null
        }
    } else {
        null
    }
    return VoiceClip(
        displayLabel = cleanLabel,
        durationSec = durationSec,
        audioBytes = audioBytes,
    )
}
