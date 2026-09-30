package dev.lanchat.ui.screen

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Chat
import androidx.compose.material.icons.filled.SwapVert
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import dev.lanchat.model.Peer
import dev.lanchat.ui.AppUi
import dev.lanchat.ui.AppViewModel

private enum class Tab(val label: String) {
    PEERS("联系人"),
    TRANSFERS("传输"),
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun MainScreen(state: AppUi, vm: AppViewModel) {
    var tab by remember { mutableStateOf(Tab.PEERS) }
    var openChat by remember { mutableStateOf<Peer?>(null) }

    val activePeer = openChat
    if (activePeer != null) {
        ChatScreen(
            peer = activePeer,
            state = state,
            vm = vm,
            onBack = { openChat = null },
        )
        return
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    Text(state.nickname.ifEmpty { "LANChat" })
                },
            )
        },
        bottomBar = {
            NavigationBar {
                NavigationBarItem(
                    selected = tab == Tab.PEERS,
                    onClick = { tab = Tab.PEERS },
                    icon = { Icon(Icons.AutoMirrored.Filled.Chat, contentDescription = null) },
                    label = { Text(Tab.PEERS.label) },
                )
                NavigationBarItem(
                    selected = tab == Tab.TRANSFERS,
                    onClick = { tab = Tab.TRANSFERS },
                    icon = { Icon(Icons.Filled.SwapVert, contentDescription = null) },
                    label = { Text(Tab.TRANSFERS.label) },
                )
            }
        },
    ) { padding ->
        when (tab) {
            Tab.PEERS -> PeersScreen(
                state = state,
                vm = vm,
                modifier = Modifier
                    .padding(padding)
                    .fillMaxSize(),
                onOpenChat = { openChat = it },
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
}
