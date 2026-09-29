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
}
