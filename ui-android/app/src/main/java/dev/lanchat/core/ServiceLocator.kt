package dev.lanchat.core

import android.content.Context
import android.os.Handler
import android.os.Looper
import dev.lanchat.R
import java.io.File

/**
 * 进程级单例：唯一的 [ChatEngine]。
 *
 * Activity / Application / 前台服务均可通过 [ensureStarted] 幂等初始化核心，
 * 避免 Activity 首帧渲染早于 Service.onCreate 导致 client 未初始化而空指针闪退。
 */
object ServiceLocator {
    private const val PREFS = "lanchat"
    private const val KEY_NICKNAME = "nickname"

    val engine: ChatEngine by lazy { ChatEngine() }

    /** 把任意线程的代码段 post 到 Android 主线程执行。 */
    val postToMain: (() -> Unit) -> Unit = { block ->
        Handler(Looper.getMainLooper()).post(block)
    }

    @Synchronized
    fun ensureStarted(context: Context) {
        val appCtx = context.applicationContext ?: context
        val baseDir = appCtx.getExternalFilesDir(null) ?: appCtx.filesDir
        val downloadDir = File(baseDir, "downloads").apply { mkdirs() }.absolutePath
        val fallbackNick = runCatching { appCtx.getString(R.string.default_nickname) }
            .getOrDefault("Android")
        val nickname = runCatching {
            appCtx.getSharedPreferences(PREFS, Context.MODE_PRIVATE)
                .getString(KEY_NICKNAME, fallbackNick)
                .orEmpty()
                .ifEmpty { fallbackNick }
        }.getOrDefault(fallbackNick)
        engine.start(nickname, downloadDir, postToMain)
    }
}
