package dev.lanchat.ui.screen

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.ElevatedCard
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import dev.lanchat.R
import dev.lanchat.model.Formatters
import dev.lanchat.model.Transfer
import dev.lanchat.model.TransferState
import dev.lanchat.ui.AppUi
import dev.lanchat.ui.AppViewModel
import dev.lanchat.ui.theme.QqOnlineGreen
import dev.lanchat.ui.theme.QqPrimaryBlue
import dev.lanchat.ui.theme.QqRelayWarningOrange
import dev.lanchat.ui.theme.QqUnreadBadgeRed
import dev.lanchat.ui.theme.qqChatBackgroundColor

@Composable
fun TransfersScreen(
    state: AppUi,
    vm: AppViewModel,
    modifier: Modifier = Modifier,
) {
    Column(
        modifier = modifier.background(qqChatBackgroundColor()),
    ) {
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = 16.dp, vertical = 10.dp),
            horizontalArrangement = Arrangement.SpaceBetween,
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                text = "⚡ P2P 高速文件传输 (${state.transfers.size})",
                style = MaterialTheme.typography.titleSmall,
                fontWeight = FontWeight.Bold,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Surface(
                shape = RoundedCornerShape(50),
                color = QqOnlineGreen.copy(alpha = 0.14f),
            ) {
                Text(
                    text = "直连优先 · AES-GCM 中继兜底",
                    color = QqOnlineGreen,
                    fontSize = 11.sp,
                    fontWeight = FontWeight.Medium,
                    modifier = Modifier.padding(horizontal = 10.dp, vertical = 4.dp),
                )
            }
        }

        if (state.transfers.isEmpty()) {
            ElevatedCard(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 16.dp, vertical = 12.dp),
                shape = RoundedCornerShape(18.dp),
            ) {
                Column(
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(28.dp),
                    horizontalAlignment = Alignment.CenterHorizontally,
                ) {
                    Text(
                        text = "暂无文件传输",
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.SemiBold,
                    )
                    Spacer(Modifier.height(6.dp))
                    Text(
                        text = "在聊天窗口点击「📁 发送文件」即可发起 P2P 直连或 Swarm 群发",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
            return
        }

        LazyColumn(
            modifier = Modifier
                .weight(1f)
                .padding(horizontal = 14.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            items(state.transfers, key = { it.id }) { transfer ->
                TransferCard(transfer, state.matrixSummary[transfer.id].orEmpty(), vm)
            }
        }
    }
}

@Composable
private fun TransferCard(transfer: Transfer, matrixSummary: String, vm: AppViewModel) {
    ElevatedCard(
        modifier = Modifier.fillMaxWidth(),
        shape = RoundedCornerShape(18.dp),
        colors = CardDefaults.elevatedCardColors(
            containerColor = MaterialTheme.colorScheme.surface,
        ),
    ) {
        Column(
            modifier = Modifier.padding(14.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            Row(
                modifier = Modifier.fillMaxWidth(),
                horizontalArrangement = Arrangement.SpaceBetween,
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Column(Modifier.weight(1f)) {
                    Text(
                        text = "📄 " + transfer.name.ifEmpty { "文件" },
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.Bold,
                    )
                    Spacer(Modifier.height(2.dp))
                    Text(
                        text = directionLabel(transfer) + " · " + Formatters.size(transfer.size),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }

                Row(horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                    // 直连 / ⚠ 中继 徽章
                    val badgeColor = if (transfer.viaRelay) QqRelayWarningOrange else QqOnlineGreen
                    val badgeText = if (transfer.viaRelay) "⚠ 中继" else "直连"
                    Surface(
                        shape = RoundedCornerShape(50),
                        color = badgeColor.copy(alpha = 0.16f),
                    ) {
                        Text(
                            text = badgeText,
                            color = badgeColor,
                            fontSize = 11.sp,
                            fontWeight = FontWeight.Bold,
                            modifier = Modifier.padding(horizontal = 8.dp, vertical = 4.dp),
                        )
                    }

                    // 状态徽章
                    Surface(
                        shape = RoundedCornerShape(50),
                        color = QqPrimaryBlue.copy(alpha = 0.12f),
                    ) {
                        Text(
                            text = stateLabel(transfer.state),
                            color = QqPrimaryBlue,
                            fontSize = 11.sp,
                            fontWeight = FontWeight.SemiBold,
                            modifier = Modifier.padding(horizontal = 8.dp, vertical = 4.dp),
                        )
                    }
                }
            }

            // Swarm 块可用性矩阵摘要（如 ▓▓▓░░░ 4/8 块）
            if (matrixSummary.isNotEmpty()) {
                Surface(
                    shape = RoundedCornerShape(10.dp),
                    color = QqPrimaryBlue.copy(alpha = 0.08f),
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    Row(
                        modifier = Modifier.padding(horizontal = 10.dp, vertical = 6.dp),
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.SpaceBetween,
                    ) {
                        Text(
                            text = "Swarm 块矩阵",
                            fontSize = 11.sp,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        Text(
                            text = matrixSummary,
                            fontSize = 12.sp,
                            fontFamily = FontFamily.Monospace,
                            fontWeight = FontWeight.SemiBold,
                            color = QqPrimaryBlue,
                        )
                    }
                }
            }

            when (transfer.state) {
                TransferState.PENDING -> {
                    if (transfer.direction == "inbound") {
                        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                            Button(
                                onClick = { vm.acceptTransfer(transfer) },
                                shape = RoundedCornerShape(50),
                                colors = ButtonDefaults.buttonColors(containerColor = QqPrimaryBlue),
                            ) {
                                Text("接收")
                            }
                            OutlinedButton(
                                onClick = { vm.rejectTransfer(transfer) },
                                shape = RoundedCornerShape(50),
                            ) {
                                Text("拒绝")
                            }
                        }
                    } else {
                        Text(
                            text = "等待对方应答…",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }

                TransferState.ACTIVE, TransferState.PAUSED -> {
                    LinearProgressIndicator(
                        progress = { transfer.fraction },
                        color = QqPrimaryBlue,
                        trackColor = QqPrimaryBlue.copy(alpha = 0.16f),
                        modifier = Modifier
                            .fillMaxWidth()
                            .height(8.dp)
                            .clip(RoundedCornerShape(50)),
                    )
                    Row(
                        modifier = Modifier.fillMaxWidth(),
                        horizontalArrangement = Arrangement.SpaceBetween,
                    ) {
                        Text(
                            text = "${Formatters.size(transfer.bytesDone)} / ${Formatters.size(transfer.size)}",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        Text(
                            text = Formatters.speed(transfer.speedBps) +
                                (if (transfer.viaRelay) " · AES-GCM 中继" else " · P2P 直连"),
                            style = MaterialTheme.typography.bodySmall,
                            fontWeight = FontWeight.SemiBold,
                            color = if (transfer.viaRelay) QqRelayWarningOrange else QqPrimaryBlue,
                        )
                    }
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        if (transfer.state == TransferState.ACTIVE) {
                            OutlinedButton(
                                onClick = { vm.pauseTransfer(transfer.id) },
                                shape = RoundedCornerShape(50),
                            ) {
                                Text("暂停")
                            }
                        } else {
                            Button(
                                onClick = { vm.resumeTransfer(transfer.id) },
                                shape = RoundedCornerShape(50),
                                colors = ButtonDefaults.buttonColors(containerColor = QqPrimaryBlue),
                            ) {
                                Text("继续")
                            }
                        }
                        OutlinedButton(
                            onClick = { vm.cancelTransfer(transfer.id) },
                            shape = RoundedCornerShape(50),
                        ) {
                            Text("取消")
                        }
                    }
                }

                TransferState.DONE -> Text(
                    text = "✓ 已完成 · ${Formatters.size(transfer.size)}" +
                        if (matrixSummary.isNotEmpty()) " · $matrixSummary" else "",
                    style = MaterialTheme.typography.bodySmall,
                    color = QqOnlineGreen,
                    fontWeight = FontWeight.SemiBold,
                )

                TransferState.FAILED -> Text(
                    text = transfer.errorReason.ifEmpty { "传输失败" },
                    color = QqUnreadBadgeRed,
                    style = MaterialTheme.typography.bodySmall,
                    fontWeight = FontWeight.Medium,
                )

                else -> Text(
                    text = "已取消",
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}

@Composable
private fun directionLabel(transfer: Transfer): String = when {
    transfer.kind == "channel" && transfer.groupId.isNotEmpty() -> {
        if (transfer.direction == "inbound") {
            stringResource(R.string.transfer_channel_inbound, transfer.groupId)
        } else {
            stringResource(R.string.transfer_channel_outbound, transfer.groupId)
        }
    }

    transfer.kind == "swarm" && transfer.groupId.isNotEmpty() -> {
        if (transfer.direction == "inbound") {
            stringResource(R.string.transfer_swarm_inbound, transfer.groupId)
        } else {
            stringResource(R.string.transfer_swarm_outbound, transfer.groupId)
        }
    }

    transfer.direction == "inbound" -> "接收自 ${transfer.peerId}"
    else -> "发送给 ${transfer.peerId}"
}

private fun stateLabel(state: String): String = when (state) {
    TransferState.PENDING -> "等待"
    TransferState.ACTIVE -> "传输中"
    TransferState.PAUSED -> "已暂停"
    TransferState.DONE -> "完成"
    TransferState.FAILED -> "失败"
    TransferState.CANCELED -> "已取消"
    else -> state
}
