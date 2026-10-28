package dev.lanchat.model

import java.util.Locale

object Formatters {

    fun size(bytes: Long): String {
        if (bytes < 1024) return "$bytes B"
        val units = arrayOf("KB", "MB", "GB", "TB")
        var value = bytes.toDouble()
        var unit = -1
        do {
            value /= 1024.0
            unit++
        } while (value >= 1024 && unit < units.lastIndex)
        return String.format(Locale.getDefault(), "%.1f %s", value, units[unit])
    }

    fun speed(bytesPerSecond: Long): String = "${size(bytesPerSecond)}/s"

    fun isTailscaleAddress(raw: String): Boolean {
        var s = raw.trim()
        for (prefix in listOf("ws://", "wss://", "http://", "https://")) {
            if (s.startsWith(prefix, ignoreCase = true)) {
                s = s.substring(prefix.length)
                break
            }
        }
        s = s.substringBefore('/').substringBefore(':')
        val parts = s.split('.')
        if (parts.size != 4) return false
        val a = parts[0].toIntOrNull() ?: return false
        val b = parts[1].toIntOrNull() ?: return false
        return a == 100 && b in 64..127
    }

    fun linkBadge(server: String, peerStatus: String = ""): String =
        if (isTailscaleAddress(server) || isTailscaleAddress(peerStatus)) {
            "🌐 Tailscale 直连"
        } else {
            "⚡ 局域网直连"
        }
}
