package dev.lanchat.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

private val Blue = Color(0xFF2563EB)
private val BlueDark = Color(0xFF1D4ED8)

private val LightColors = lightColorScheme(
    primary = Blue,
    primaryContainer = Color(0xFFDBEAFE),
    secondary = Color(0xFF0EA5E9),
)

private val DarkColors = darkColorScheme(
    primary = Color(0xFF93C5FD),
    primaryContainer = BlueDark,
    secondary = Color(0xFF7DD3FC),
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
