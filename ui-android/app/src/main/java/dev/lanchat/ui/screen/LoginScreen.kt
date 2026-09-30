package dev.lanchat.ui.screen

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBar
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import dev.lanchat.ui.AppUi
import dev.lanchat.ui.AppViewModel

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun LoginScreen(state: AppUi, vm: AppViewModel) {
    var addr by remember { mutableStateOf("") }
    var psk by remember { mutableStateOf("") }

    Scaffold(
        topBar = { TopAppBar(title = { Text("连接 LANChat") }) },
    ) { padding ->
        Column(
            modifier = Modifier
                .padding(padding)
                .padding(16.dp)
                .fillMaxWidth(),
            verticalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            Text(
                "在同一局域网内发现并连接服务器",
                style = MaterialTheme.typography.bodyMedium,
            )

            OutlinedTextField(
                value = addr,
                onValueChange = { addr = it },
                label = { Text("服务器地址（host:port）") },
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
            )

            OutlinedTextField(
                value = psk,
                onValueChange = { psk = it },
                label = { Text("口令 PSK") },
                singleLine = true,
                visualTransformation = PasswordVisualTransformation(),
                modifier = Modifier.fillMaxWidth(),
            )

            Row(
                horizontalArrangement = Arrangement.spacedBy(12.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Button(
                    enabled = !state.busy,
                    onClick = { vm.connect(addr, psk) },
                ) { Text("连接") }
                OutlinedButton(
                    enabled = !state.busy,
                    onClick = { vm.discover() },
                ) { Text("自动发现") }
                if (state.busy) CircularProgressIndicator(modifier = Modifier.height(24.dp))
            }

            state.error?.let {
                Text(it, color = MaterialTheme.colorScheme.error)
            }

            if (state.discoveredServers.isNotEmpty()) {
                Spacer(Modifier.height(8.dp))
                Text("发现的服务器", style = MaterialTheme.typography.titleSmall)
                LazyColumn(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    items(state.discoveredServers) { server ->
                        Card(
                            modifier = Modifier.fillMaxWidth(),
                            onClick = { addr = server.addr },
                        ) {
                            Column(Modifier.padding(12.dp)) {
                                Text(
                                    server.name.ifEmpty { "LANChat 服务器" },
                                    style = MaterialTheme.typography.titleMedium,
                                )
                                Text(server.addr, style = MaterialTheme.typography.bodySmall)
                                Text(
                                    "鉴权：${server.authMode}",
                                    style = MaterialTheme.typography.bodySmall,
                                )
                            }
                        }
                    }
                }
            }
        }
    }
}
