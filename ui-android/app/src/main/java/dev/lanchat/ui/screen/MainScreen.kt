package dev.lanchat.ui.screen

import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Chat
import androidx.compose.material.icons.filled.SwapVert
import androidx.compose.material.icons.filled.Tag
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import dev.lanchat.R
import dev.lanchat.model.Channel
import dev.lanchat.model.Peer
import dev.lanchat.ui.AppUi
import dev.lanchat.ui.AppViewModel

private enum class Tab {
    PEERS,
    CHANNELS,
    TRANSFERS,
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun MainScreen(state: AppUi, vm: AppViewModel) {
    var tab by remember { mutableStateOf(Tab.PEERS) }
    var openChat by remember { mutableStateOf<Peer?>(null) }
    var openChannelChat by remember { mutableStateOf<Channel?>(null) }
    val snackbarHostState = remember { SnackbarHostState() }

    LaunchedEffect(state.error) {
        val error = state.error
        if (!error.isNullOrEmpty()) {
            snackbarHostState.showSnackbar(error)
            vm.consumeError()
        }
    }

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

    val activeChannel = openChannelChat
    if (activeChannel != null) {
        ChannelChatScreen(
            channel = activeChannel,
            state = state,
            vm = vm,
            onBack = { openChannelChat = null },
        )
        return
    }

    Scaffold(
        topBar = {
            TopAppBar(
                title = {
                    Text(state.nickname.ifEmpty { stringResource(R.string.app_name) })
                },
            )
        },
        snackbarHost = { SnackbarHost(snackbarHostState) },
        bottomBar = {
            NavigationBar {
                NavigationBarItem(
                    selected = tab == Tab.PEERS,
                    onClick = { tab = Tab.PEERS },
                    icon = { Icon(Icons.AutoMirrored.Filled.Chat, contentDescription = null) },
                    label = { Text(stringResource(R.string.tab_peers)) },
                )
                NavigationBarItem(
                    selected = tab == Tab.CHANNELS,
                    onClick = { tab = Tab.CHANNELS },
                    icon = { Icon(Icons.Filled.Tag, contentDescription = null) },
                    label = { Text(stringResource(R.string.tab_channels)) },
                )
                NavigationBarItem(
                    selected = tab == Tab.TRANSFERS,
                    onClick = { tab = Tab.TRANSFERS },
                    icon = { Icon(Icons.Filled.SwapVert, contentDescription = null) },
                    label = { Text(stringResource(R.string.tab_transfers)) },
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

            Tab.CHANNELS -> ChannelsScreen(
                state = state,
                vm = vm,
                modifier = Modifier
                    .padding(padding)
                    .fillMaxSize(),
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
}
