package dev.lanchat.core

import android.os.Handler
import android.os.Looper

/**
 * 进程级单例：唯一的 [ChatEngine]。
 *
 * 前台服务创建并持有核心；Activity/ViewModel 在服务启动后通过这里访问，
 * 从而在用户离开聊天界面时核心与传输仍在后台继续。
 */
object ServiceLocator {
    val engine: ChatEngine by lazy { ChatEngine() }

    /** 把任意线程的代码段 post 到 Android 主线程执行。 */
    val postToMain: (() -> Unit) -> Unit = { block ->
        Handler(Looper.getMainLooper()).post(block)
    }
}
