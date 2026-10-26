package dev.lanchat.service

import android.app.Notification
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.wifi.WifiManager
import android.os.Build
import android.os.IBinder
import androidx.core.app.NotificationCompat
import dev.lanchat.LANChatApp
import dev.lanchat.MainActivity
import dev.lanchat.R
import dev.lanchat.core.ServiceLocator

/**
 * 前台服务：进程常驻，持有唯一核心（经 [ServiceLocator]）。
 *
 * - 下载目录使用应用专属外部目录，无需存储权限；
 * - 获取 [WifiManager.MulticastLock] 以接收 mDNS 多播（224.0.0.251:5353）；
 * - 所有系统权限受限操作（前台通知、多播锁）均做异常兜底，防止 ROM 权限策略导致进程闪退。
 */
class CoreService : Service() {

    private var multicastLock: WifiManager.MulticastLock? = null

    override fun onCreate() {
        super.onCreate()
        startForegroundSafe()
        acquireMulticastLock()
        ensureCoreStarted()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        startForegroundSafe()
        ensureCoreStarted()
        return START_STICKY
    }

    private fun startForegroundSafe() {
        runCatching {
            val notification = buildNotification()
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                startForeground(
                    NOTIFICATION_ID,
                    notification,
                    ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC,
                )
            } else {
                startForeground(NOTIFICATION_ID, notification)
            }
        }
    }

    private fun ensureCoreStarted() {
        runCatching {
            ServiceLocator.ensureStarted(this)
        }
    }

    override fun onBind(intent: Intent): IBinder? = null

    private fun acquireMulticastLock() {
        runCatching {
            val wifi = applicationContext.getSystemService(Context.WIFI_SERVICE) as? WifiManager
                ?: return
            multicastLock = wifi.createMulticastLock("lanchat-mdns").apply {
                setReferenceCounted(false)
                acquire()
            }
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
        runCatching {
            multicastLock?.let { if (it.isHeld) it.release() }
        }
        multicastLock = null
        super.onDestroy()
    }

    companion object {
        private const val NOTIFICATION_ID = 1001

        fun start(context: Context) {
            runCatching {
                val intent = Intent(context, CoreService::class.java)
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
                    context.startForegroundService(intent)
                } else {
                    context.startService(intent)
                }
            }
        }
    }
}
