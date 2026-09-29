package dev.lanchat.ui.screen

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.AssistChip
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import dev.lanchat.model.Formatters
import dev.lanchat.model.Transfer
import dev.lanchat.model.TransferState
import dev.lanchat.ui.AppUi
import dev.lanchat.ui.AppViewModel

@Composable
fun TransfersScreen(
    state: AppUi,
    vm: AppViewModel,
    modifier: Modifier = Modifier,
) {
    if (state.transfers.isEmpty()) {
        Column(modifier.padding(24.dp)) {
            Text("暂无文件传输", style = MaterialTheme.typography.bodyLarge)
        }
        return
    }

    LazyColumn(
        modifier = modifier.padding(horizontal = 12.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        items(state.transfers, key = { it.id }) { transfer ->
            TransferCard(transfer, vm)
        }
    }
}

@Composable
private fun TransferCard(transfer: Transfer, vm: AppViewModel) {
    Card(modifier = Modifier.fillMaxWidth()) {
        Column(Modifier.padding(12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
            ) {
                Column(Modifier.weight(1f)) {
                    Text(
                        transfer.name.ifEmpty { "文件" },
                        style = MaterialTheme.typography.titleSmall,
                    )
                    Text(
                        directionLabel(transfer) + " · " + Formatters.size(transfer.size),
                        style = MaterialTheme.typography.bodySmall,
                    )
                }
                AssistChip(onClick = {}, label = { Text(stateLabel(transfer.state)) })
            }

            when (transfer.state) {
                TransferState.PENDING -> {
                    if (transfer.direction == "inbound") {
                        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            Button(onClick = { vm.acceptTransfer(transfer) }) { Text("接收") }
                            OutlinedButton(onClick = { vm.rejectTransfer(transfer) }) {
                                Text("拒绝")
                            }
                        }
                    } else {
                        Text("等待对方应答…", style = MaterialTheme.typography.bodySmall)
                    }
                }

                TransferState.ACTIVE, TransferState.PAUSED -> {
                    LinearProgressIndicator(
                        progress = { transfer.fraction },
                        modifier = Modifier.fillMaxWidth(),
                    )
                    Text(
                        "${Formatters.size(transfer.bytesDone)} / ${Formatters.size(transfer.size)}" +
                            " · ${Formatters.speed(transfer.speedBps)}" +
                            if (transfer.viaRelay) " · 中继" else "",
                        style = MaterialTheme.typography.bodySmall,
                    )
                    OutlinedButton(onClick = { vm.cancelTransfer(transfer.id) }) {
                        Text(if (transfer.state == TransferState.ACTIVE) "取消" else "已暂停")
                    }
                }

                TransferState.DONE -> Text(
                    "已完成 · ${Formatters.size(transfer.size)}",
                    style = MaterialTheme.typography.bodySmall,
                )

                TransferState.FAILED -> Text(
                    transfer.errorReason.ifEmpty { "传输失败" },
                    color = MaterialTheme.colorScheme.error,
                    style = MaterialTheme.typography.bodySmall,
                )

                else -> Text("已取消", style = MaterialTheme.typography.bodySmall)
            }
        }
    }
}

private fun directionLabel(transfer: Transfer): String =
    if (transfer.direction == "inbound") "接收自 ${transfer.peerId}" else "发送给 ${transfer.peerId}"

private fun stateLabel(state: String): String = when (state) {
    TransferState.PENDING -> "等待"
    TransferState.ACTIVE -> "传输中"
    TransferState.PAUSED -> "已暂停"
    TransferState.DONE -> "完成"
    TransferState.FAILED -> "失败"
    TransferState.CANCELED -> "已取消"
    else -> state
}
