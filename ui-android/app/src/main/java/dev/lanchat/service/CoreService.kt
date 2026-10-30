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
import androidx.core.app.RemoteInput
import dev.lanchat.LANChatApp
import dev.lanchat.MainActivity
import dev.lanchat.R
import dev.lanchat.core.ServiceLocator
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch

/**
 * 前台服务：进程常驻，持有唯一核心（经 [ServiceLocator]）。
 *
 * - 下载目录使用应用专属外部目录，无需存储权限；
 * - 获取 [WifiManager.MulticastLock] 以接收 mDNS 多播（224.0.0.251:5353）；
 * - 所有系统权限受限操作（前台通知、多播锁）均做异常兜底，防止 ROM 权限策略导致进程闪退。
 */
class CoreService : Service() {

    private var multicastLock: WifiManager.MulticastLock? = null
    private val serviceScope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    override fun onCreate() {
        super.onCreate()
        startForegroundSafe()
        acquireMulticastLock()
        ensureCoreStarted()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        startForegroundSafe()
        ensureCoreStarted()
        handleNotificationQuickAction(intent)
        return START_STICKY
    }

    private fun handleNotificationQuickAction(intent: Intent?) {
        val action = intent?.action ?: return
        val targetPeer = intent.getStringExtra(EXTRA_TARGET_PEER).orEmpty()
        val targetGroup = intent.getStringExtra(EXTRA_TARGET_GROUP).orEmpty()
        val transferId = intent.getStringExtra(EXTRA_TRANSFER_ID).orEmpty()
        serviceScope.launch {
            runCatching {
                when (action) {
                    ACTION_QUICK_REPLY -> {
                        val reply = RemoteInput.getResultsFromIntent(intent)
                            ?.getCharSequence(KEY_TEXT_REPLY)
                            ?.toString()
                            ?.trim()
                            .orEmpty()
                            .ifEmpty { "👍 收到，稍后处理" }
                        if (targetGroup.isNotEmpty()) {
                            ServiceLocator.engine.sendText("", reply, targetGroup)
                        } else if (targetPeer.isNotEmpty()) {
                            ServiceLocator.engine.sendText(targetPeer, reply)
                        } else {
                            ServiceLocator.engine.sendText("", reply, "*")
                        }
                    }
                    ACTION_ACCEPT_CALL -> {
                        ServiceLocator.engine.acceptVoiceCall(targetPeer, targetGroup, "")
                    }
                    ACTION_ACCEPT_FILE -> {
                        val tid = transferId.ifEmpty {
                            ServiceLocator.engine.getState().transfers
                                .firstOrNull { it.direction == "inbound" && it.state == "pending" }
                                ?.id
                                .orEmpty()
                        }
                        if (tid.isNotEmpty()) {
                            ServiceLocator.engine.respondFile(tid, true, "")
                        }
                    }
                }
            }
        }
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
        val mutableFlags = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            PendingIntent.FLAG_UPDATE_CURRENT or PendingIntent.FLAG_MUTABLE
        } else {
            PendingIntent.FLAG_UPDATE_CURRENT
        }
        val replyAction = NotificationCompat.Action.Builder(
            R.drawable.ic_launcher_foreground,
            "快捷回复",
            PendingIntent.getService(
                this,
                101,
                Intent(this, CoreService::class.java).setAction(ACTION_QUICK_REPLY),
                mutableFlags,
            ),
        ).addRemoteInput(
            RemoteInput.Builder(KEY_TEXT_REPLY).setLabel("输入回复内容…").build(),
        ).build()

        val acceptCallAction = NotificationCompat.Action.Builder(
            R.drawable.ic_launcher_foreground,
            "接听来电",
            PendingIntent.getService(
                this,
                102,
                Intent(this, CoreService::class.java).setAction(ACTION_ACCEPT_CALL),
                PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
            ),
        ).build()

        val acceptFileAction = NotificationCompat.Action.Builder(
            R.drawable.ic_launcher_foreground,
            "接收文件",
            PendingIntent.getService(
                this,
                103,
                Intent(this, CoreService::class.java).setAction(ACTION_ACCEPT_FILE),
                PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
            ),
        ).build()

        return NotificationCompat.Builder(this, LANChatApp.CHANNEL_ID)
            .setContentTitle(getString(R.string.notification_title))
            .setContentText(getString(R.string.notification_text))
            .setSmallIcon(R.drawable.ic_launcher_foreground)
            .setOngoing(true)
            .setContentIntent(pending)
            .addAction(replyAction)
            .addAction(acceptCallAction)
            .addAction(acceptFileAction)
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
        const val ACTION_QUICK_REPLY = "dev.lanchat.action.QUICK_REPLY"
        const val ACTION_ACCEPT_CALL = "dev.lanchat.action.ACCEPT_CALL"
        const val ACTION_ACCEPT_FILE = "dev.lanchat.action.ACCEPT_FILE"
        const val EXTRA_TARGET_PEER = "extra_target_peer"
        const val EXTRA_TARGET_GROUP = "extra_target_group"
        const val EXTRA_TRANSFER_ID = "extra_transfer_id"
        const val KEY_TEXT_REPLY = "key_text_reply"

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
