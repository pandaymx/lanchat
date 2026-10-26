package dev.lanchat.ui.screen

import android.content.Intent
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Chat
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.FolderOpen
import androidx.compose.material.icons.filled.Group
import androidx.compose.material.icons.filled.Search
import androidx.compose.material.icons.filled.SwapVert
import androidx.compose.material.icons.filled.Tag
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Badge
import androidx.compose.material3.BadgedBox
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.NavigationBarItemDefaults
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
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.core.content.FileProvider
import dev.lanchat.R
import java.io.File
import dev.lanchat.model.Channel
import dev.lanchat.model.Conversations
import dev.lanchat.model.Peer
import dev.lanchat.ui.AppUi
import dev.lanchat.ui.AppViewModel
import dev.lanchat.ui.theme.QqAvatar
import dev.lanchat.ui.theme.QqOnlineGreen
import dev.lanchat.ui.theme.QqPrimaryBlue
import dev.lanchat.ui.theme.QqUnreadBadgeRed
import dev.lanchat.ui.theme.qqHeaderBrush

private enum class Tab {
    MESSAGES,
    PEERS,
    CHANNELS,
    TRANSFERS,
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun MainScreen(state: AppUi, vm: AppViewModel) {
    var tab by remember { mutableStateOf(Tab.MESSAGES) }
    var openChat by remember { mutableStateOf<Peer?>(null) }
    var openChannelChat by remember { mutableStateOf<Channel?>(null) }
    var searchQuery by remember { mutableStateOf("") }
    var showNicknameDialog by remember { mutableStateOf(false) }
    var showDownloadDirDialog by remember { mutableStateOf(false) }
    var showUpdateDialog by remember { mutableStateOf(false) }
    var showCreateChannelDialog by remember { mutableStateOf(false) }
    val context = LocalContext.current
    val snackbarHostState = remember { SnackbarHostState() }

    LaunchedEffect(state.pendingInstallApkPath) {
        val apkPath = state.pendingInstallApkPath
        if (!apkPath.isNullOrEmpty()) {
            vm.consumePendingInstallApk()
            runCatching {
                val apkFile = File(apkPath)
                if (apkFile.exists()) {
                    val uri = FileProvider.getUriForFile(
                        context,
                        "${context.packageName}.fileprovider",
                        apkFile,
                    )
                    val intent = Intent(Intent.ACTION_VIEW).apply {
                        setDataAndType(uri, "application/vnd.android.package-archive")
                        addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                        addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                    }
                    context.startActivity(intent)
                }
            }.onFailure { e ->
                snackbarHostState.showSnackbar("自动调起安装器失败，请从下载目录手动安装：${e.message ?: ""}")
            }
        }
    }

    BackHandler(
        enabled = openChat != null || openChannelChat != null || searchQuery.isNotEmpty() || tab != Tab.MESSAGES,
    ) {
        when {
            openChat != null -> openChat = null
            openChannelChat != null -> openChannelChat = null
            searchQuery.isNotEmpty() -> searchQuery = ""
            tab != Tab.MESSAGES -> tab = Tab.MESSAGES
        }
    }

    LaunchedEffect(state.error) {
        val error = state.error
        if (!error.isNullOrEmpty()) {
            snackbarHostState.showSnackbar(error)
            vm.consumeError()
        }
    }

    val activePeer = openChat
    if (activePeer != null) {
        LaunchedEffect(activePeer.id) {
            vm.clearUnread(Conversations.peerKey(activePeer.id))
        }
        ChatScreen(
            peer = activePeer,
            state = state,
            vm = vm,
            onBack = { openChat = null },
        )
        return
    }

    val activeChannel = openChannelChat
    if (activeChannel != null) {
        LaunchedEffect(activeChannel.id) {
            vm.clearUnread(Conversations.groupKey(activeChannel.id))
        }
        ChannelChatScreen(
            channel = activeChannel,
            state = state,
            vm = vm,
            onBack = { openChannelChat = null },
        )
        return
    }

    val totalUnread = state.unreadCounts.values.sum()

    Scaffold(
        topBar = {
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .background(qqHeaderBrush())
                    .statusBarsPadding(),
            ) {
                // 手机 QQ 顶部栏：36.dp 圆形头像（点击改昵称）+ 昵称与在线状态点 + 快捷操作
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(horizontal = 16.dp, vertical = 10.dp),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.SpaceBetween,
                ) {
                    Row(
                        verticalAlignment = Alignment.CenterVertically,
                        modifier = Modifier
                            .weight(1f)
                            .padding(end = 8.dp)
                            .clickable { showNicknameDialog = true },
                    ) {
                        QqAvatar(
                            name = state.nickname.ifEmpty { stringResource(R.string.app_name) },
                            seed = state.selfId.ifEmpty { "self" },
                            size = 36.dp,
                            fontSize = 15.sp,
                            showOnlineDot = true,
                            online = state.conn == "connected",
                            containerColor = Color.White.copy(alpha = 0.25f),
                            onClick = { showNicknameDialog = true },
                        )
                        Spacer(Modifier.width(10.dp))
                        Column(modifier = Modifier.weight(1f)) {
                            Text(
                                text = state.nickname.ifEmpty { stringResource(R.string.app_name) },
                                color = Color.White,
                                fontSize = 16.sp,
                                fontWeight = FontWeight.Bold,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                            )
                            Row(verticalAlignment = Alignment.CenterVertically) {
                                Box(
                                    modifier = Modifier
                                        .size(7.dp)
                                        .clip(CircleShape)
                                        .background(QqOnlineGreen),
                                )
                                Spacer(Modifier.width(4.dp))
                                Text(
                                    text = "在线 · 点击修改昵称",
                                    color = Color.White.copy(alpha = 0.88f),
                                    fontSize = 11.sp,
                                    maxLines = 1,
                                    overflow = TextOverflow.Ellipsis,
                                )
                            }
                        }
                    }

                    Row(verticalAlignment = Alignment.CenterVertically) {
                        TextButton(
                            onClick = { showUpdateDialog = true },
                            contentPadding = PaddingValues(horizontal = 8.dp, vertical = 4.dp),
                        ) {
                            Text(
                                text = "🚀 更新",
                                color = Color.White,
                                fontSize = 12.sp,
                                fontWeight = FontWeight.SemiBold,
                                maxLines = 1,
                                softWrap = false,
                            )
                        }
                        IconButton(
                            onClick = { showDownloadDirDialog = true },
                            modifier = Modifier.size(36.dp),
                        ) {
                            Icon(
                                Icons.Filled.FolderOpen,
                                contentDescription = "设置下载目录 📂",
                                tint = Color.White,
                                modifier = Modifier.size(20.dp),
                            )
                        }
                        IconButton(
                            onClick = { showCreateChannelDialog = true },
                            modifier = Modifier.size(36.dp),
                        ) {
                            Icon(
                                Icons.Filled.Add,
                                contentDescription = stringResource(R.string.channel_create),
                                tint = Color.White,
                                modifier = Modifier.size(22.dp),
                            )
                        }
                    }
                }

                // 手机 QQ 胶囊形搜索框：🔍 搜索联系人、频道或聊天记录
                Surface(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(horizontal = 16.dp)
                        .padding(bottom = 12.dp)
                        .height(38.dp),
                    shape = RoundedCornerShape(50),
                    color = Color.White.copy(alpha = 0.92f),
                ) {
                    Row(
                        modifier = Modifier
                            .fillMaxSize()
                            .padding(horizontal = 14.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Icon(
                            Icons.Filled.Search,
                            contentDescription = null,
                            tint = Color(0xFF6B7280),
                            modifier = Modifier.size(18.dp),
                        )
                        Spacer(Modifier.width(8.dp))
                        Box(
                            modifier = Modifier.weight(1f),
                            contentAlignment = Alignment.CenterStart,
                        ) {
                            if (searchQuery.isEmpty()) {
                                Text(
                                    text = "搜索联系人、频道或聊天记录",
                                    color = Color(0xFF9CA3AF),
                                    fontSize = 13.sp,
                                )
                            }
                            BasicTextField(
                                value = searchQuery,
                                onValueChange = { searchQuery = it },
                                singleLine = true,
                                cursorBrush = SolidColor(QqPrimaryBlue),
                                textStyle = TextStyle(
                                    color = Color(0xFF1F2937),
                                    fontSize = 13.sp,
                                ),
                                modifier = Modifier.fillMaxWidth(),
                            )
                        }
                        if (searchQuery.isNotEmpty()) {
                            IconButton(
                                onClick = { searchQuery = "" },
                                modifier = Modifier.size(20.dp),
                            ) {
                                Icon(
                                    Icons.Filled.Close,
                                    contentDescription = "清除",
                                    tint = Color(0xFF6B7280),
                                    modifier = Modifier.size(16.dp),
                                )
                            }
                        }
                    }
                }
            }
        },
        snackbarHost = { SnackbarHost(snackbarHostState) },
        bottomBar = {
            NavigationBar(
                containerColor = MaterialTheme.colorScheme.surface,
            ) {
                NavigationBarItem(
                    selected = tab == Tab.MESSAGES,
                    onClick = { tab = Tab.MESSAGES },
                    icon = {
                        BadgedBox(
                            badge = {
                                if (totalUnread > 0) {
                                    Badge(containerColor = QqUnreadBadgeRed) {
                                        Text(
                                            text = if (totalUnread > 99) "99+" else totalUnread.toString(),
                                            color = Color.White,
                                        )
                                    }
                                }
                            },
                        ) {
                            Icon(Icons.AutoMirrored.Filled.Chat, contentDescription = null)
                        }
                    },
                    label = { Text("消息") },
                    colors = NavigationBarItemDefaults.colors(
                        selectedIconColor = QqPrimaryBlue,
                        selectedTextColor = QqPrimaryBlue,
                        indicatorColor = QqPrimaryBlue.copy(alpha = 0.14f),
                    ),
                )
                NavigationBarItem(
                    selected = tab == Tab.PEERS,
                    onClick = { tab = Tab.PEERS },
                    icon = { Icon(Icons.Filled.Group, contentDescription = null) },
                    label = { Text(stringResource(R.string.tab_peers)) },
                    colors = NavigationBarItemDefaults.colors(
                        selectedIconColor = QqPrimaryBlue,
                        selectedTextColor = QqPrimaryBlue,
                        indicatorColor = QqPrimaryBlue.copy(alpha = 0.14f),
                    ),
                )
                NavigationBarItem(
                    selected = tab == Tab.CHANNELS,
                    onClick = { tab = Tab.CHANNELS },
                    icon = { Icon(Icons.Filled.Tag, contentDescription = null) },
                    label = { Text(stringResource(R.string.tab_channels)) },
                    colors = NavigationBarItemDefaults.colors(
                        selectedIconColor = QqPrimaryBlue,
                        selectedTextColor = QqPrimaryBlue,
                        indicatorColor = QqPrimaryBlue.copy(alpha = 0.14f),
                    ),
                )
                NavigationBarItem(
                    selected = tab == Tab.TRANSFERS,
                    onClick = { tab = Tab.TRANSFERS },
                    icon = { Icon(Icons.Filled.SwapVert, contentDescription = null) },
                    label = { Text(stringResource(R.string.tab_transfers)) },
                    colors = NavigationBarItemDefaults.colors(
                        selectedIconColor = QqPrimaryBlue,
                        selectedTextColor = QqPrimaryBlue,
                        indicatorColor = QqPrimaryBlue.copy(alpha = 0.14f),
                    ),
                )
            }
        },
    ) { padding ->
        when (tab) {
            Tab.MESSAGES, Tab.PEERS -> PeersScreen(
                state = state,
                vm = vm,
                modifier = Modifier
                    .padding(padding)
                    .fillMaxSize(),
                searchQuery = searchQuery,
                onOpenChat = { openChat = it },
                onOpenBroadcast = { openChannelChat = it },
            )

            Tab.CHANNELS -> ChannelsScreen(
                state = state,
                vm = vm,
                modifier = Modifier
                    .padding(padding)
                    .fillMaxSize(),
                searchQuery = searchQuery,
                onOpenChannel = { openChannelChat = it },
            )

            Tab.TRANSFERS -> TransfersScreen(
                state = state,
                vm = vm,
                modifier = Modifier
                    .padding(padding)
                    .fillMaxSize(),
            )
        }
    }

    if (showNicknameDialog) {
        EditNicknameDialog(
            currentNickname = state.nickname,
            onDismiss = { showNicknameDialog = false },
            onConfirm = { newName ->
                vm.setNickname(newName)
                showNicknameDialog = false
            },
        )
    }

    if (showDownloadDirDialog) {
        PickDownloadDirDialog(
            currentDir = state.downloadDir,
            onDismiss = { showDownloadDirDialog = false },
            onConfirm = { dir ->
                vm.pickDownloadDir(dir)
                showDownloadDirDialog = false
            },
        )
    }

    if (showUpdateDialog) {
        SilentUpdateDialog(
            onDismiss = { showUpdateDialog = false },
            onCheckOnly = {
                vm.silentUpdate(checkOnly = true)
                showUpdateDialog = false
            },
            onSilentDownload = { proxy ->
                vm.silentUpdate(customProxy = proxy, checkOnly = false)
                showUpdateDialog = false
            },
        )
    }

    if (showCreateChannelDialog) {
        CreateChannelDialog(
            busy = state.busy,
            onDismiss = { showCreateChannelDialog = false },
            onConfirm = { name, topic, private ->
                vm.createChannel(name, topic, private)
                showCreateChannelDialog = false
                tab = Tab.CHANNELS
            },
        )
    }
}

@Composable
private fun EditNicknameDialog(
    currentNickname: String,
    onDismiss: () -> Unit,
    onConfirm: (String) -> Unit,
) {
    var name by remember { mutableStateOf(currentNickname) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("修改个人昵称") },
        text = {
            OutlinedTextField(
                value = name,
                onValueChange = { name = it },
                singleLine = true,
                label = { Text("昵称") },
                placeholder = { Text("输入局域网显示昵称") },
            )
        },
        confirmButton = {
            TextButton(
                enabled = name.isNotBlank(),
                onClick = { onConfirm(name) },
            ) { Text("保存") }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.dialog_cancel)) }
        },
    )
}

@Composable
private fun PickDownloadDirDialog(
    currentDir: String,
    onDismiss: () -> Unit,
    onConfirm: (String) -> Unit,
) {
    var path by remember { mutableStateOf(currentDir.ifEmpty { "/sdcard/Download/LANChat" }) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("📂 设置文件接收保存目录") },
        text = {
            OutlinedTextField(
                value = path,
                onValueChange = { path = it },
                singleLine = true,
                label = { Text("本地保存路径") },
            )
        },
        confirmButton = {
            TextButton(
                enabled = path.isNotBlank(),
                onClick = { onConfirm(path) },
            ) { Text("确定") }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.dialog_cancel)) }
        },
    )
}

@Composable
private fun SilentUpdateDialog(
    onDismiss: () -> Unit,
    onCheckOnly: () -> Unit,
    onSilentDownload: (String) -> Unit,
) {
    var proxy by remember { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text("🚀 版本更新（国内加速静默下载）") },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Text(
                    text = "自动按优先级轮询国内加速节点（ghfast.top / gh-proxy.com / ghproxy.net / mirror.ghproxy.com / ghps.cc），免浏览器后台静默下载 APK 安装包并校验 SHA-256。",
                    fontSize = 12.sp,
                )
                OutlinedTextField(
                    value = proxy,
                    onValueChange = { proxy = it },
                    singleLine = true,
                    label = { Text("自定义加速代理前缀（可选）") },
                    placeholder = { Text("留空默认自动轮询国内节点") },
                )
            }
        },
        confirmButton = {
            Row {
                TextButton(onClick = onCheckOnly) { Text("🔍 检查版本") }
                TextButton(onClick = { onSilentDownload(proxy) }) { Text("⬇️ 静默下载") }
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.dialog_cancel)) }
        },
    )
}
