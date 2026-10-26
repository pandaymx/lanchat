package dev.lanchat.ui.screen

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.Radar
import androidx.compose.material.icons.filled.Storage
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ElevatedCard
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.OutlinedTextFieldDefaults
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import dev.lanchat.ui.AppUi
import dev.lanchat.ui.AppViewModel
import dev.lanchat.ui.theme.QqAvatar
import dev.lanchat.ui.theme.QqOnlineGreen
import dev.lanchat.ui.theme.QqPrimaryBlue
import dev.lanchat.ui.theme.qqBannerBrush
import dev.lanchat.ui.theme.qqChatBackgroundColor

@Composable
fun LoginScreen(state: AppUi, vm: AppViewModel) {
    var addr by remember { mutableStateOf("") }
    var psk by remember { mutableStateOf("") }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .background(qqChatBackgroundColor())
            .verticalScroll(rememberScrollState())
            .imePadding(),
    ) {
        // 手机 QQ 风格顶部渐变弧形横幅 + 80.dp 居中圆形头像（带在线绿点）
        Box(
            modifier = Modifier
                .fillMaxWidth()
                .height(250.dp)
                .clip(RoundedCornerShape(bottomStart = 32.dp, bottomEnd = 32.dp))
                .background(qqBannerBrush()),
            contentAlignment = Alignment.Center,
        ) {
            Column(
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.Center,
                modifier = Modifier.padding(top = 20.dp),
            ) {
                QqAvatar(
                    name = state.nickname.ifEmpty { "Q" },
                    seed = state.selfId.ifEmpty { "LANChat" },
                    size = 80.dp,
                    fontSize = 30.sp,
                    showOnlineDot = true,
                    online = true,
                    containerColor = Color.White.copy(alpha = 0.24f),
                )
                Spacer(Modifier.height(14.dp))
                Text(
                    text = "LANChat 局域网即时通信",
                    color = Color.White,
                    fontSize = 22.sp,
                    fontWeight = FontWeight.Bold,
                )
                Spacer(Modifier.height(4.dp))
                Text(
                    text = "P2P 高速直连 · 端到端加密",
                    color = Color.White.copy(alpha = 0.88f),
                    fontSize = 13.sp,
                )
            }
        }

        // 悬浮圆角登录卡片
        ElevatedCard(
            modifier = Modifier
                .fillMaxWidth()
                .padding(horizontal = 20.dp)
                .offset(y = (-24).dp),
            shape = RoundedCornerShape(24.dp),
            elevation = CardDefaults.elevatedCardElevation(defaultElevation = 8.dp),
            colors = CardDefaults.elevatedCardColors(
                containerColor = MaterialTheme.colorScheme.surface,
            ),
        ) {
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(20.dp),
                verticalArrangement = Arrangement.spacedBy(14.dp),
            ) {
                // 局域网自动发现横栏按钮
                Row(
                    modifier = Modifier.fillMaxWidth(),
                    horizontalArrangement = Arrangement.SpaceBetween,
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Text(
                        text = "节点接入",
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.Bold,
                    )
                    OutlinedButton(
                        enabled = !state.busy,
                        onClick = { vm.discover() },
                        shape = RoundedCornerShape(50),
                    ) {
                        Icon(
                            Icons.Filled.Radar,
                            contentDescription = null,
                            tint = QqPrimaryBlue,
                            modifier = Modifier.size(16.dp),
                        )
                        Spacer(Modifier.width(6.dp))
                        Text("📡 局域网自动发现", color = QqPrimaryBlue, fontSize = 13.sp)
                    }
                }

                // 自动发现的节点卡片列表（点击自动填入地址）
                if (state.discoveredServers.isNotEmpty()) {
                    Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        Text(
                            text = "已发现的局域网中心节点（点击填入）",
                            style = MaterialTheme.typography.labelMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        LazyColumn(
                            modifier = Modifier.height(
                                (state.discoveredServers.size.coerceAtMost(3) * 64).dp,
                            ),
                            verticalArrangement = Arrangement.spacedBy(6.dp),
                        ) {
                            items(state.discoveredServers) { server ->
                                Card(
                                    modifier = Modifier
                                        .fillMaxWidth()
                                        .clickable { addr = server.addr },
                                    shape = RoundedCornerShape(14.dp),
                                    colors = CardDefaults.cardColors(
                                        containerColor = QqPrimaryBlue.copy(alpha = 0.08f),
                                    ),
                                ) {
                                    Row(
                                        modifier = Modifier
                                            .fillMaxWidth()
                                            .padding(horizontal = 12.dp, vertical = 10.dp),
                                        verticalAlignment = Alignment.CenterVertically,
                                        horizontalArrangement = Arrangement.SpaceBetween,
                                    ) {
                                        Column(Modifier.weight(1f)) {
                                            Text(
                                                text = server.name.ifEmpty { "LANChat 中心节点" },
                                                style = MaterialTheme.typography.bodyMedium,
                                                fontWeight = FontWeight.SemiBold,
                                            )
                                            Text(
                                                text = server.addr,
                                                style = MaterialTheme.typography.bodySmall,
                                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                            )
                                        }
                                        Surface(
                                            shape = RoundedCornerShape(50),
                                            color = QqOnlineGreen.copy(alpha = 0.16f),
                                        ) {
                                            Text(
                                                text = "鉴权：${server.authMode}",
                                                color = QqOnlineGreen,
                                                fontSize = 11.sp,
                                                fontWeight = FontWeight.Medium,
                                                modifier = Modifier.padding(horizontal = 8.dp, vertical = 4.dp),
                                            )
                                        }
                                    }
                                }
                            }
                        }
                    }
                }

                // 服务器地址输入框（兼容 lanchat://join?addr=... 房间快连码）
                OutlinedTextField(
                    value = addr,
                    onValueChange = { addr = it },
                    label = { Text("服务器地址 / lanchat:// 房间快连码") },
                    placeholder = { Text("ws://192.168.x.x:19090/lctp 或 lanchat://join?...") },
                    leadingIcon = {
                        Icon(
                            Icons.Filled.Storage,
                            contentDescription = null,
                            tint = QqPrimaryBlue,
                        )
                    },
                    singleLine = true,
                    shape = RoundedCornerShape(16.dp),
                    colors = OutlinedTextFieldDefaults.colors(
                        focusedBorderColor = QqPrimaryBlue,
                        focusedLabelColor = QqPrimaryBlue,
                    ),
                    modifier = Modifier.fillMaxWidth(),
                )

                // PSK 口令输入框
                OutlinedTextField(
                    value = psk,
                    onValueChange = { psk = it },
                    label = { Text("口令 PSK") },
                    placeholder = { Text("输入局域网共享预置口令") },
                    leadingIcon = {
                        Icon(
                            Icons.Filled.Lock,
                            contentDescription = null,
                            tint = QqPrimaryBlue,
                        )
                    },
                    singleLine = true,
                    visualTransformation = PasswordVisualTransformation(),
                    shape = RoundedCornerShape(16.dp),
                    colors = OutlinedTextFieldDefaults.colors(
                        focusedBorderColor = QqPrimaryBlue,
                        focusedLabelColor = QqPrimaryBlue,
                    ),
                    modifier = Modifier.fillMaxWidth(),
                )

                state.error?.let {
                    Text(
                        text = it,
                        color = MaterialTheme.colorScheme.error,
                        style = MaterialTheme.typography.bodySmall,
                    )
                }

                // 全宽胶囊形 QQ 蓝「立即登录」按钮
                Button(
                    enabled = !state.busy,
                    onClick = { vm.connect(addr, psk) },
                    modifier = Modifier
                        .fillMaxWidth()
                        .height(50.dp),
                    shape = RoundedCornerShape(50),
                    colors = ButtonDefaults.buttonColors(
                        containerColor = QqPrimaryBlue,
                        contentColor = Color.White,
                    ),
                ) {
                    if (state.busy) {
                        CircularProgressIndicator(
                            color = Color.White,
                            strokeWidth = 2.dp,
                            modifier = Modifier.size(20.dp),
                        )
                        Spacer(Modifier.width(10.dp))
                    }
                    Text(
                        text = "立即登录",
                        fontSize = 16.sp,
                        fontWeight = FontWeight.Bold,
                    )
                }

                // 移动端一键房主模式（内置 Hub + mDNS 广播，无需电脑）
                OutlinedButton(
                    enabled = !state.busy,
                    onClick = { vm.connect("local", psk) },
                    modifier = Modifier
                        .fillMaxWidth()
                        .height(46.dp),
                    shape = RoundedCornerShape(50),
                ) {
                    Text(
                        text = "🏠 本机开启局域网房间（免电脑房主模式）",
                        color = QqPrimaryBlue,
                        fontSize = 14.sp,
                        fontWeight = FontWeight.SemiBold,
                    )
                }
            }
        }
    }
}
