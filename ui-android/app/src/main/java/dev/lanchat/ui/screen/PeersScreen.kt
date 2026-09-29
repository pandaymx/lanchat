package dev.lanchat.ui.screen

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Person
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import dev.lanchat.model.Peer
import dev.lanchat.ui.AppUi
import dev.lanchat.ui.AppViewModel

@Composable
fun PeersScreen(
    state: AppUi,
    vm: AppViewModel,
    modifier: Modifier = Modifier,
    onOpenChat: (Peer) -> Unit,
) {
    if (state.peers.isEmpty()) {
        Column(modifier.padding(24.dp)) {
            Text(
                "当前没有其他在线用户",
                style = MaterialTheme.typography.bodyLarge,
            )
            Text(
                "等待同一局域网内的其他设备加入…",
                style = MaterialTheme.typography.bodySmall,
            )
        }
        return
    }

    LazyColumn(modifier) {
        items(state.peers, key = { it.id }) { peer ->
            ListItem(
                leadingContent = { Icon(Icons.Filled.Person, contentDescription = null) },
                headlineContent = { Text(peer.nickname.ifEmpty { peer.id }) },
                supportingContent = {
                    Text(
                        buildString {
                            append(peer.os.ifEmpty { "unknown" })
                            if (peer.status.isNotEmpty()) append(" · ${peer.status}")
                        },
                    )
                },
                modifier = Modifier
                    .fillMaxWidth()
                    .clickable { onOpenChat(peer) },
            )
            HorizontalDivider()
        }
    }
}
