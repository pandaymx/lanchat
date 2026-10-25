package dev.lanchat.ui.screen

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import dev.lanchat.model.Channel
import dev.lanchat.model.Conversations
import dev.lanchat.model.Peer
import dev.lanchat.ui.AppUi
import dev.lanchat.ui.AppViewModel
import dev.lanchat.ui.theme.QqAvatar
import dev.lanchat.ui.theme.QqOnlineGreen
import dev.lanchat.ui.theme.QqPrimaryBlue
import dev.lanchat.ui.theme.QqUnreadBadgeRed
import java.text.SimpleDateFormat
import java.util.Date
import java.util.Locale

@Composable
fun PeersScreen(
    state: AppUi,
    vm: AppViewModel,
    modifier: Modifier = Modifier,
    searchQuery: String = "",
    onOpenChat: (Peer) -> Unit,
    onOpenBroadcast: (Channel) -> Unit = {},
) {
    val q = searchQuery.trim().lowercase()
    val filteredPeers = if (q.isEmpty()) {
        state.peers
    } else {
        state.peers.filter { peer ->
            peer.nickname.lowercase().contains(q) ||
                peer.id.lowercase().contains(q) ||
                peer.os.lowercase().contains(q) ||
                Conversations.search(
                    state.messagesByConversation,
                    Conversations.peerKey(peer.id),
                    q,
                ).isNotEmpty()
        }
    }

    val broadcastChannel = Channel(
        id = "*",
        name = "📢 局域网全员广播群",
        ownerId = state.selfId,
        private = false,
        topic = "G1 全员广播 · P2P Swarm 分发",
        members = state.peers.map { it.id },
    )
    val broadcastKey = Conversations.groupKey("*")
    val broadcastMessages = Conversations.messagesOf(state.messagesByConversation, broadcastKey)
    val broadcastLast = broadcastMessages.lastOrNull()
    val broadcastUnread = state.unreadCounts[broadcastKey] ?: 0

    LazyColumn(
        modifier = modifier.background(MaterialTheme.colorScheme.surface),
    ) {
        // 置顶会话：📢 局域网全员广播群 (G1)
        item(key = "pinned-broadcast") {
            SessionRowCard(
                avatarLabel = "群",
                avatarSeed = "broadcast-all",
                avatarColor = QqPrimaryBlue,
                showOnlineDot = true,
                title = "📢 局域网全员广播群",
                badgeText = "置顶 · ${state.peers.size + 1} 人在线",
                badgeColor = QqPrimaryBlue,
                subtitle = broadcastLast?.text
                    ?: "全员在线广播 · 支持 P2P Swarm 群文件分发",
                timeText = broadcastLast?.timestamp?.let { formatShortTime(it) } ?: "刚刚",
                unreadCount = broadcastUnread,
                pinned = true,
                onClick = { onOpenBroadcast(broadcastChannel) },
            )
            HorizontalDivider(
                thickness = 0.5.dp,
                color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.5f),
            )
        }

        if (filteredPeers.isEmpty()) {
            item(key = "empty-peers") {
                Column(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(horizontal = 24.dp, vertical = 36.dp),
                    horizontalAlignment = Alignment.CenterHorizontally,
                ) {
                    Text(
                        text = if (q.isEmpty()) "当前没有其他在线用户" else "没有匹配的联系人或聊天记录",
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.SemiBold,
                    )
                    Spacer(Modifier.height(6.dp))
                    Text(
                        text = if (q.isEmpty()) {
                            "等待同一局域网内的其他设备加入…"
                        } else {
                            "请尝试其他搜索关键词"
                        },
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        } else {
            items(filteredPeers, key = { it.id }) { peer ->
                val convKey = Conversations.peerKey(peer.id)
                val messages = Conversations.messagesOf(state.messagesByConversation, convKey)
                val lastMsg = messages.lastOrNull()
                val unread = state.unreadCounts[convKey] ?: 0
                val tag = buildString {
                    append(peer.os.ifEmpty { "LAN" })
                    append(" · ")
                    append(peer.status.ifEmpty { "在线" })
                }

                SessionRowCard(
                    avatarLabel = peer.nickname.ifEmpty { peer.id },
                    avatarSeed = peer.id,
                    showOnlineDot = true,
                    title = peer.nickname.ifEmpty { peer.id },
                    badgeText = tag,
                    badgeColor = QqOnlineGreen,
                    subtitle = lastMsg?.text ?: "[在线] 点击发起 P2P 直连聊天",
                    timeText = lastMsg?.timestamp?.let { formatShortTime(it) } ?: "",
                    unreadCount = unread,
                    pinned = false,
                    onClick = { onOpenChat(peer) },
                )
                HorizontalDivider(
                    modifier = Modifier.padding(start = 76.dp),
                    thickness = 0.5.dp,
                    color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.4f),
                )
            }
        }
    }
}

@Composable
private fun SessionRowCard(
    avatarLabel: String,
    avatarSeed: String,
    avatarColor: Color? = null,
    showOnlineDot: Boolean,
    title: String,
    badgeText: String,
    badgeColor: Color,
    subtitle: String,
    timeText: String,
    unreadCount: Int,
    pinned: Boolean,
    onClick: () -> Unit,
) {
    val bgColor = if (pinned) {
        QqPrimaryBlue.copy(alpha = 0.05f)
    } else {
        MaterialTheme.colorScheme.surface
    }
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .background(bgColor)
            .clickable(onClick = onClick)
            .padding(horizontal = 16.dp, vertical = 12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (avatarColor != null) {
            QqAvatar(
                name = avatarLabel,
                seed = avatarSeed,
                size = 48.dp,
                fontSize = 18.sp,
                showOnlineDot = showOnlineDot,
                containerColor = avatarColor,
            )
        } else {
            QqAvatar(
                name = avatarLabel,
                seed = avatarSeed,
                size = 48.dp,
                fontSize = 18.sp,
                showOnlineDot = showOnlineDot,
            )
        }

        Spacer(Modifier.width(12.dp))

        Column(modifier = Modifier.weight(1f)) {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(6.dp),
            ) {
                Text(
                    text = title,
                    style = MaterialTheme.typography.titleMedium,
                    fontWeight = FontWeight.Bold,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                Surface(
                    shape = RoundedCornerShape(6.dp),
                    color = badgeColor.copy(alpha = 0.14f),
                ) {
                    Text(
                        text = badgeText,
                        color = badgeColor,
                        fontSize = 10.sp,
                        fontWeight = FontWeight.Medium,
                        modifier = Modifier.padding(horizontal = 6.dp, vertical = 2.dp),
                    )
                }
            }
            Spacer(Modifier.height(4.dp))
            Text(
                text = subtitle,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }

        Spacer(Modifier.width(8.dp))

        Column(
            horizontalAlignment = Alignment.End,
            verticalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            if (timeText.isNotEmpty()) {
                Text(
                    text = timeText,
                    fontSize = 11.sp,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            if (unreadCount > 0) {
                Box(
                    modifier = Modifier
                        .size(width = if (unreadCount > 9) 24.dp else 18.dp, height = 18.dp)
                        .clip(CircleShape)
                        .background(QqUnreadBadgeRed),
                    contentAlignment = Alignment.Center,
                ) {
                    Text(
                        text = if (unreadCount > 99) "99+" else unreadCount.toString(),
                        color = Color.White,
                        fontSize = 10.sp,
                        fontWeight = FontWeight.Bold,
                    )
                }
            }
        }
    }
}

private fun formatShortTime(ts: Long): String {
    if (ts <= 0L) return ""
    return SimpleDateFormat("HH:mm", Locale.getDefault()).format(Date(ts))
}
