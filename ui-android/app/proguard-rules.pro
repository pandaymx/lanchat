# gomobile 绑定支持库（保留，不要混淆/裁剪，否则 JNI 回调失效）
-keep class dev.lanchat.bindings.** { *; }
-keep interface dev.lanchat.bindings.** { *; }

# 移动端实现的 Listener 由 Go 侧反射调用，需保留
-keep class dev.lanchat.service.GoListener { *; }
