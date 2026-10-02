package dev.lanchat.ui.screen

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.PersonAdd
import androidx.compose.material.icons.filled.Tag
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.AssistChip
import androidx.compose.material3.Card
import androidx.compose.material3.Checkbox
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FloatingActionButton
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
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
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import dev.lanchat.R
import dev.lanchat.model.Channel
import dev.lanchat.model.Peer
import dev.lanchat.ui.AppUi
import dev.lanchat.ui.AppViewModel

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ChannelsScreen(
    state: AppUi,
    vm: AppViewModel,
    modifier: Modifier = Modifier,
    onOpenChannel: (Channel) -> Unit,
) {
    var showCreate by remember { mutableStateOf(false) }
    var inviteTarget by remember { mutableStateOf<Channel?>(null) }

    Column(modifier) {
        LazyColumn(
            modifier = Modifier
                .weight(1f)
                .padding(horizontal = 12.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            if (state.channels.isEmpty()) {
                item {
                    Text(
                        stringResource(R.string.channels_empty),
                        modifier = Modifier.padding(24.dp),
                        style = MaterialTheme.typography.bodyLarge,
                    )
                }
            }
            items(state.channels, key = { it.id }) { channel ->
                ChannelCard(
                    channel = channel,
                    selfId = state.selfId,
                    onOpen = { onOpenChannel(channel) },
                    onJoin = { vm.joinChannel(channel.id) },
                    onInvite = { inviteTarget = channel },
                )
            }
        }

        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(12.dp),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            OutlinedButton(onClick = { vm.refreshChannels() }) {
                Text(stringResource(R.string.channels_refresh))
            }
            FloatingActionButton(onClick = { showCreate = true }) {
                Icon(Icons.Filled.Add, contentDescription = stringResource(R.string.channel_create))
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
    onOpen: () -> Unit,
    onJoin: () -> Unit,
    onInvite: () -> Unit,
) {
    val joined = channel.members.contains(selfId)
    val isOwner = channel.ownerId == selfId && selfId.isNotEmpty()
    Card(modifier = Modifier.fillMaxWidth()) {
        Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                if (channel.private) {
                    Icon(
                        Icons.Filled.Lock,
                        contentDescription = stringResource(R.string.channel_private_icon),
                    )
                } else {
                    Icon(Icons.Filled.Tag, contentDescription = null)
                }
                Text(
                    channel.name.ifEmpty { channel.id },
                    modifier = Modifier.padding(start = 8.dp),
                    style = MaterialTheme.typography.titleSmall,
                )
            }
            if (channel.topic.isNotEmpty()) {
                Text(channel.topic, style = MaterialTheme.typography.bodySmall)
            }
            Text(
                stringResource(R.string.channel_members_count, channel.members.size),
                style = MaterialTheme.typography.bodySmall,
            )
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                if (joined) {
                    AssistChip(onClick = onOpen, label = {
                        Text(stringResource(R.string.channel_open))
                    })
                    if (isOwner) {
                        AssistChip(onClick = onInvite, label = {
                            Text(stringResource(R.string.channel_invite))
                        }, leadingIcon = {
                            Icon(Icons.Filled.PersonAdd, contentDescription = null)
                        })
                    }
                } else {
                    OutlinedButton(onClick = onJoin) {
                        Text(stringResource(R.string.channel_join))
                    }
                }
            }
        }
    }
}

@Composable
private fun CreateChannelDialog(
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
