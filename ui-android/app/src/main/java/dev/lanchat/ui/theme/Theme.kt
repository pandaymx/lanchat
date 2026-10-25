package dev.lanchat.ui.theme

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.TextUnit
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlin.math.abs

// 手机 QQ 标志性配色体系
val QqPrimaryBlue = Color(0xFF0099FF)       // #0099FF QQ 主色蓝
val QqGradientStart = Color(0xFF00C6FF)     // #00C6FF 顶部渐变起点
val QqGradientEnd = Color(0xFF0072FF)       // #0072FF 顶部渐变终点
val QqChatBgLight = Color(0xFFF2F4F7)       // #F2F4F7 浅色聊天背景
val QqChatBgDark = Color(0xFF111318)        // #111318 深色聊天背景
val QqOutboundBubble = Color(0xFF0099FF)    // #0099FF 发送方气泡（蓝底白字）
val QqOutboundText = Color(0xFFFFFFFF)
val QqInboundBubbleLight = Color(0xFFFFFFFF)// 接收方浅色气泡（白底深字）
val QqInboundBubbleDark = Color(0xFF1E222B) // 接收方深色气泡
val QqUnreadBadgeRed = Color(0xFFFF3B30)    // #FF3B30 未读红点徽章
val QqOnlineGreen = Color(0xFF22C55E)       // #22C55E 在线状态绿点
val QqRelayWarningOrange = Color(0xFFF59E0B)// #F59E0B 中继橙色徽章

private val QqAvatarPalette = listOf(
    Color(0xFF0099FF),
    Color(0xFF00B4D8),
    Color(0xFF6366F1),
    Color(0xFF8B5CF6),
    Color(0xFFEC4899),
    Color(0xFF10B981),
    Color(0xFFF59E0B),
    Color(0xFF3B82F6),
)

fun qqHeaderBrush(): Brush = Brush.horizontalGradient(
    colors = listOf(QqGradientStart, QqGradientEnd),
)

fun qqBannerBrush(): Brush = Brush.linearGradient(
    colors = listOf(QqGradientStart, QqPrimaryBlue, QqGradientEnd),
)

@Composable
fun qqChatBackgroundColor(darkTheme: Boolean = isSystemInDarkTheme()): Color =
    if (darkTheme) QqChatBgDark else QqChatBgLight

@Composable
fun qqInboundBubbleColor(darkTheme: Boolean = isSystemInDarkTheme()): Color =
    if (darkTheme) QqInboundBubbleDark else QqInboundBubbleLight

fun qqAvatarColor(seed: String): Color {
    if (seed.isEmpty()) return QqPrimaryBlue
    val idx = abs(seed.hashCode()) % QqAvatarPalette.size
    return QqAvatarPalette[idx]
}

/** 手机 QQ 风格彩色圆形头像（可选右下角在线绿点与点击事件）。 */
@Composable
fun QqAvatar(
    name: String,
    seed: String = name,
    size: Dp = 44.dp,
    fontSize: TextUnit = 16.sp,
    showOnlineDot: Boolean = false,
    online: Boolean = true,
    containerColor: Color = qqAvatarColor(seed),
    modifier: Modifier = Modifier,
    onClick: (() -> Unit)? = null,
) {
    val displayChar = name.trim().firstOrNull()?.uppercaseChar()?.toString() ?: "Q"
    val dotSize = (size.value * 0.28f).coerceIn(10f, 18f).dp
    Box(
        modifier = modifier
            .size(size)
            .then(if (onClick != null) Modifier.clickable(onClick = onClick) else Modifier),
        contentAlignment = Alignment.Center,
    ) {
        Box(
            modifier = Modifier
                .size(size)
                .clip(CircleShape)
                .background(containerColor),
            contentAlignment = Alignment.Center,
        ) {
            Text(
                text = displayChar,
                color = Color.White,
                fontSize = fontSize,
                fontWeight = FontWeight.Bold,
            )
        }
        if (showOnlineDot) {
            Box(
                modifier = Modifier
                    .align(Alignment.BottomEnd)
                    .size(dotSize)
                    .clip(CircleShape)
                    .background(if (online) QqOnlineGreen else Color(0xFF9CA3AF))
                    .border(2.dp, Color.White, CircleShape),
            )
        }
    }
}

private val LightColors = lightColorScheme(
    primary = QqPrimaryBlue,
    onPrimary = Color.White,
    primaryContainer = Color(0xFFD6EEFF),
    onPrimaryContainer = Color(0xFF003355),
    secondary = QqGradientEnd,
    onSecondary = Color.White,
    secondaryContainer = Color(0xFFE0F2FE),
    background = QqChatBgLight,
    surface = Color.White,
    surfaceVariant = Color(0xFFEEF2F6),
    error = QqUnreadBadgeRed,
)

private val DarkColors = darkColorScheme(
    primary = QqPrimaryBlue,
    onPrimary = Color.White,
    primaryContainer = QqGradientEnd,
    onPrimaryContainer = Color.White,
    secondary = QqGradientStart,
    background = QqChatBgDark,
    surface = QqInboundBubbleDark,
    surfaceVariant = Color(0xFF262B36),
    error = QqUnreadBadgeRed,
)

@Composable
fun LANChatTheme(
    darkTheme: Boolean = isSystemInDarkTheme(),
    content: @Composable () -> Unit,
) {
    MaterialTheme(
        colorScheme = if (darkTheme) DarkColors else LightColors,
        content = content,
    )
}
