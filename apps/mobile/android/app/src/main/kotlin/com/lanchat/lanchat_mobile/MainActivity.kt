package com.lanchat.lanchat_mobile

import android.content.Context
import android.net.wifi.WifiManager
import io.flutter.embedding.android.FlutterActivity

class MainActivity : FlutterActivity() {
  // 保持 mDNS 多播锁：Android 默认在屏幕关闭/省电时丢弃多播包，
  // 不拿锁的话局域网 hub 发现（mDNS _lanchat._tcp）收不到广播。
  private var multicastLock: WifiManager.MulticastLock? = null

  override fun onResume() {
    super.onResume()
    acquireMulticastLock()
  }

  override fun onPause() {
    releaseMulticastLock()
    super.onPause()
  }

  private fun acquireMulticastLock() {
    if (multicastLock?.isHeld == true) return
    val wm = getSystemService(Context.WIFI_SERVICE) as WifiManager
    multicastLock = wm.createMulticastLock("lanchat-mdns").apply {
      setReferenceCounted(false)
      acquire()
    }
  }

  private fun releaseMulticastLock() {
    multicastLock?.takeIf { it.isHeld }?.release()
    multicastLock = null
  }
}
