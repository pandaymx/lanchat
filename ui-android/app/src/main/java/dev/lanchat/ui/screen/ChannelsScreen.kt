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
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.PersonAdd
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.filled.Tag
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.AssistChip
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.Checkbox
import androidx.compose.material3.ElevatedCard
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import dev.lanchat.R
import dev.lanchat.model.Channel
import dev.lanchat.model.Conversations
import dev.lanchat.model.Peer
import dev.lanchat.ui.AppUi
import dev.lanchat.ui.AppViewModel
import dev.lanchat.ui.theme.QqAvatar
import dev.lanchat.ui.theme.QqOnlineGreen
import dev.lanchat.ui.theme.QqPrimaryBlue
import dev.lanchat.ui.theme.QqRelayWarningOrange
import dev.lanchat.ui.theme.QqUnreadBadgeRed
import dev.lanchat.ui.theme.qqChatBackgroundColor

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ChannelsScreen(
    state: AppUi,
    vm: AppViewModel,
    modifier: Modifier = Modifier,
    searchQuery: String = "",
    onOpenChannel: (Channel) -> Unit,
) {
    var showCreate by remember { mutableStateOf(false) }
    var inviteTarget by remember { mutableStateOf<Channel?>(null) }

    val q = searchQuery.trim().lowercase()
    val filteredChannels = if (q.isEmpty()) {
        state.channels
    } else {
        state.channels.filter { ch ->
            ch.name.lowercase().contains(q) ||
                ch.topic.lowercase().contains(q) ||
                ch.id.lowercase().contains(q)
        }
    }

    Column(
        modifier = modifier.background(qqChatBackgroundColor()),
    ) {
        // 顶部频道操作栏
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = 16.dp, vertical = 10.dp),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = "#️⃣ G2 自定义频道 (${filteredChannels.size})",
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedButton(
                    onClick = { vm.refreshChannels() },
                    shape = RoundedCornerShape(50),
                ) {
                    Icon(
                        Icons.Filled.Refresh,
                        contentDescription = null,
                        modifier = Modifier.size(16.dp),
                    )
                    Spacer(Modifier.width(4.dp))
                    Text(stringResource(R.string.channels_refresh), fontSize = 12.sp)
                }
                ExtendedFloatingActionButton(
                    onClick = { showCreate = true },
                    containerColor = QqPrimaryBlue,
                    contentColor = Color.White,
                    shape = RoundedCornerShape(50),
                ) {
                    Icon(Icons.Filled.Add, contentDescription = stringResource(R.string.channel_create))
                    Spacer(Modifier.width(4.dp))
                    Text("+ " + stringResource(R.string.channel_create), fontWeight = FontWeight.Bold)
                }
            }
        }

        LazyColumn(
            modifier = Modifier
                .weight(1f)
                .padding(horizontal = 14.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            if (filteredChannels.isEmpty()) {
                item {
                    ElevatedCard(
                        modifier = Modifier
                            .fillMaxWidth()
                            .padding(vertical = 16.dp),
                        shape = RoundedCornerShape(18.dp),
                    ) {
                        Column(
                            modifier = Modifier
                                .fillMaxWidth()
                                .padding(24.dp),
                            horizontalAlignment = Alignment.CenterHorizontally,
                        ) {
                            Text(
                                text = stringResource(R.string.channels_empty),
                                style = MaterialTheme.typography.bodyLarge,
                                fontWeight = FontWeight.Medium,
                            )
                            Spacer(Modifier.height(10.dp))
                            Button(
                                onClick = { showCreate = true },
                                shape = RoundedCornerShape(50),
                                colors = ButtonDefaults.buttonColors(containerColor = QqPrimaryBlue),
                            ) {
                                Text("+ " + stringResource(R.string.channel_create))
                            }
                        }
                    }
                }
            }
            items(filteredChannels, key = { it.id }) { channel ->
                val unread = state.unreadCounts[Conversations.groupKey(channel.id)] ?: 0
                ChannelCard(
                    channel = channel,
                    selfId = state.selfId,
                    unreadCount = unread,
                    onOpen = { onOpenChannel(channel) },
                    onJoin = { vm.joinChannel(channel.id) },
                    onLeave = { vm.leaveChannel(channel.id) },
                    onInvite = { inviteTarget = channel },
                )
            }
        }
    }

    if (showCreate) {
        CreateChannelDialog(
            busy = state.busy,
            onDismiss = { showCreate = false },
            onConfirm = { name, topic, private ->
                vm.createChannel(name, topic, private)
                showCreate = false
            },
        )
    }

    val target = inviteTarget
    if (target != null) {
        InviteMembersDialog(
            channel = target,
            peers = state.peers,
            busy = state.busy,
            onDismiss = { inviteTarget = null },
            onConfirm = { memberIds ->
                memberIds.forEach { vm.inviteMember(target.id, it) }
                inviteTarget = null
            },
        )
    }
}

@Composable
private fun ChannelCard(
    channel: Channel,
    selfId: String,
    unreadCount: Int,
    onOpen: () -> Unit,
    onJoin: () -> Unit,
    onLeave: () -> Unit,
    onInvite: () -> Unit,
) {
    val joined = channel.members.contains(selfId)
    val isOwner = channel.ownerId == selfId && selfId.isNotEmpty()
    ElevatedCard(
        modifier = Modifier.fillMaxWidth(),
        shape = RoundedCornerShape(18.dp),
        colors = CardDefaults.elevatedCardColors(
            containerColor = MaterialTheme.colorScheme.surface,
        ),
    ) {
        Column(
            modifier = Modifier.padding(14.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.SpaceBetween,
                modifier = Modifier.fillMaxWidth(),
            ) {
                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    modifier = Modifier.weight(1f),
                ) {
                    QqAvatar(
                        name = if (channel.private) "🔒" else "#",
                        seed = channel.id,
                        size = 42.dp,
                        fontSize = 16.sp,
                    )
                    Spacer(Modifier.width(10.dp))
                    Column {
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            if (channel.private) {
                                Icon(
                                    Icons.Filled.Lock,
                                    contentDescription = stringResource(R.string.channel_private_icon),
                                    tint = QqRelayWarningOrange,
                                    modifier = Modifier.size(16.dp),
                                )
                            } else {
                                Icon(
                                    Icons.Filled.Tag,
                                    contentDescription = null,
                                    tint = QqPrimaryBlue,
                                    modifier = Modifier.size(16.dp),
                                )
                            }
                            Text(
                                text = channel.name.ifEmpty { channel.id },
                                modifier = Modifier.padding(start = 4.dp),
                                style = MaterialTheme.typography.titleMedium,
                                fontWeight = FontWeight.Bold,
                            )
                        }
                        Text(
                            text = stringResource(R.string.channel_members_count, channel.members.size),
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }

                Row(
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(6.dp),
                ) {
                    Surface(
                        shape = RoundedCornerShape(50),
                        color = if (channel.private) {
                            QqRelayWarningOrange.copy(alpha = 0.15f)
                        } else {
                            QqOnlineGreen.copy(alpha = 0.15f)
                        },
                    ) {
                        Text(
                            text = if (channel.private) "🔒 私有" else "公开频道",
                            color = if (channel.private) QqRelayWarningOrange else QqOnlineGreen,
                            fontSize = 11.sp,
                            fontWeight = FontWeight.SemiBold,
                            modifier = Modifier.padding(horizontal = 8.dp, vertical = 3.dp),
                        )
                    }
                    if (unreadCount > 0) {
                        Box(
                            modifier = Modifier
                                .size(20.dp)
                                .clip(CircleShape)
                                .background(QqUnreadBadgeRed),
                            contentAlignment = Alignment.Center,
                        ) {
                            Text(
                                text = unreadCount.toString(),
                                color = Color.White,
                                fontSize = 10.sp,
                                fontWeight = FontWeight.Bold,
                            )
                        }
                    }
                }
            }

            if (channel.topic.isNotEmpty()) {
                Text(
                    text = channel.topic,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }

            Row(
                horizontalArrangement = Arrangement.spacedBy(8.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                if (joined) {
                    Button(
                        onClick = onOpen,
                        shape = RoundedCornerShape(50),
                        colors = ButtonDefaults.buttonColors(containerColor = QqPrimaryBlue),
                    ) {
                        Text(stringResource(R.string.channel_open))
                    }
                    if (isOwner) {
                        AssistChip(
                            onClick = onInvite,
                            label = { Text(stringResource(R.string.channel_invite)) },
                            leadingIcon = {
                                Icon(Icons.Filled.PersonAdd, contentDescription = null)
                            },
                        )
                    }
                    OutlinedButton(
                        onClick = onLeave,
                        shape = RoundedCornerShape(50),
                    ) {
                        Text(stringResource(R.string.channel_leave))
                    }
                } else {
                    Button(
                        onClick = onJoin,
                        shape = RoundedCornerShape(50),
                        colors = ButtonDefaults.buttonColors(containerColor = QqPrimaryBlue),
                    ) {
                        Text(stringResource(R.string.channel_join))
                    }
                }
            }
        }
    }
}

@Composable
internal fun CreateChannelDialog(
    busy: Boolean,
    onDismiss: () -> Unit,
    onConfirm: (name: String, topic: String, private: Boolean) -> Unit,
) {
    var name by remember { mutableStateOf("") }
    var topic by remember { mutableStateOf("") }
    var private by remember { mutableStateOf(false) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.channel_create)) },
        text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(
                    value = name,
                    onValueChange = { name = it },
                    singleLine = true,
                    enabled = !busy,
                    label = { Text(stringResource(R.string.channel_name_label)) },
                )
                OutlinedTextField(
                    value = topic,
                    onValueChange = { topic = it },
                    singleLine = true,
                    enabled = !busy,
                    label = { Text(stringResource(R.string.channel_topic_label)) },
                )
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(top = 4.dp),
                    horizontalArrangement = Arrangement.SpaceBetween,
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Text(stringResource(R.string.channel_private))
                    Switch(checked = private, enabled = !busy, onCheckedChange = { private = it })
                }
            }
        },
        confirmButton = {
            TextButton(
                enabled = !busy && name.isNotBlank(),
                onClick = { onConfirm(name, topic, private) },
            ) { Text(stringResource(R.string.dialog_create)) }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.dialog_cancel)) }
        },
    )
}

@Composable
internal fun InviteMembersDialog(
    channel: Channel,
    peers: List<Peer>,
    busy: Boolean,
    onDismiss: () -> Unit,
    onConfirm: (List<String>) -> Unit,
) {
    val candidates = peers.filter { it.id !in channel.members }
    val selected = remember { mutableStateOf(emptySet<String>()) }

    AlertDialog(
        onDismissRequest = onDismiss,
        title = {
            Text(stringResource(R.string.channel_invite_title, channel.name.ifEmpty { channel.id }))
        },
        text = {
            if (candidates.isEmpty()) {
                Text(stringResource(R.string.channel_no_invitable_peers))
            } else {
                LazyColumn {
                    items(candidates, key = { it.id }) { peer ->
                        Row(
                            modifier = Modifier
                                .fillMaxWidth()
                                .clickable(enabled = !busy) {
                                    selected.value = selected.value.toggle(peer.id)
                                }
                                .padding(vertical = 4.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            Checkbox(
                                checked = peer.id in selected.value,
                                enabled = !busy,
                                onCheckedChange = {
                                    selected.value = selected.value.toggle(peer.id)
                                },
                            )
                            Text(peer.nickname.ifEmpty { peer.id })
                        }
                    }
                }
            }
        },
        confirmButton = {
            TextButton(
                enabled = !busy && selected.value.isNotEmpty(),
                onClick = { onConfirm(selected.value.toList()) },
            ) { Text(stringResource(R.string.channel_invite_confirm)) }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.dialog_cancel)) }
        },
    )
}

private fun Set<String>.toggle(value: String): Set<String> =
    if (value in this) this - value else this + value
