package dev.lanchat.service

import android.app.Notification
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.net.wifi.WifiManager
import android.app.Service
import android.os.Build
import android.os.IBinder
import androidx.core.app.NotificationCompat
import dev.lanchat.LANChatApp
import dev.lanchat.MainActivity
import dev.lanchat.R
import dev.lanchat.core.ServiceLocator
import java.io.File

/**
 * 前台服务：进程常驻，持有唯一核心（经 [ServiceLocator]）。
 *
 * - 下载目录使用应用专属外部目录，无需存储权限；
 * - 获取 [WifiManager.MulticastLock] 以接收 mDNS 多播（224.0.0.251:5353）；
 * - startId 采用自管理计数，最后一个 start 结束时 stopSelf。
 */
class CoreService : Service() {

    private var multicastLock: WifiManager.MulticastLock? = null

    override fun onCreate() {
        super.onCreate()
        startForeground(NOTIFICATION_ID, buildNotification())
        acquireMulticastLock()
        ensureCoreStarted()
    }

    private fun ensureCoreStarted() {
        val downloadDir = File(getExternalFilesDir(null), "downloads").absolutePath
        File(downloadDir).mkdirs()
        val nickname = getSharedPreferences(PREFS, Context.MODE_PRIVATE)
            .getString(KEY_NICKNAME, getString(R.string.default_nickname))
            ?: getString(R.string.default_nickname)
        ServiceLocator.engine.start(nickname, downloadDir, ServiceLocator.postToMain)
    }

    override fun onBind(intent: Intent): IBinder? = null

    private fun acquireMulticastLock() {
        val wifi = applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager
        multicastLock = wifi.createMulticastLock("lanchat-mdns").apply {
            setReferenceCounted(true)
            acquire()
        }
    }

    private fun buildNotification(): Notification {
        val pending = PendingIntent.getActivity(
            this,
            0,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE,
        )
        return NotificationCompat.Builder(this, LANChatApp.CHANNEL_ID)
            .setContentTitle(getString(R.string.notification_title))
            .setContentText(getString(R.string.notification_text))
            .setSmallIcon(R.drawable.ic_launcher_foreground)
            .setOngoing(true)
            .setContentIntent(pending)
            .build()
    }

    override fun onDestroy() {
        multicastLock?.let { if (it.isHeld) it.release() }
        multicastLock = null
        super.onDestroy()
    }

    companion object {
        private const val NOTIFICATION_ID = 1001
        private const val PREFS = "lanchat"
        private const val KEY_NICKNAME = "nickname"

        fun start(context: Context) {
            val intent = Intent(context, CoreService::class.java)
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                context.startForegroundService(intent)
            } else {
                context.startService(intent)
            }
        }
    }
}
