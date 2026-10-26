# gomobile 绑定支持库与 Go Seq 运行时（保留，不要混淆/裁剪，否则 JNI 回调失效）
-keep class dev.lanchat.bindings.** { *; }
-keep interface dev.lanchat.bindings.** { *; }
-keep class go.** { *; }
-keep interface go.** { *; }
-keepclasseswithmembernames class * {
    native <methods>;
}

# 移动端实现的 Listener、模型与前台服务需保留
-keep class dev.lanchat.** { *; }
-keep interface dev.lanchat.** { *; }
