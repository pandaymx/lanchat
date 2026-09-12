import 'package:audioplayers/audioplayers.dart';
import 'package:flutter/foundation.dart';

/// 全局音频播放单例（同一时刻只播一个语音）。
class AudioCtl {
  AudioCtl._();

  static final AudioPlayer _player = AudioPlayer();
  static final ValueNotifier<bool> playing = ValueNotifier(false);
  static String? currentUrl;

  /// 点击播放/停止。返回当前是否在播放该 url。
  static Future<bool> toggle(String url) async {
    if (currentUrl == url && playing.value) {
      await _player.stop();
      playing.value = false;
      return false;
    }
    try {
      await _player.stop();
      await _player.play(UrlSource(url));
      currentUrl = url;
      playing.value = true;
      return true;
    } catch (e) {
      debugPrint('audio play error: $e');
      playing.value = false;
      return false;
    }
  }

  static Future<void> stop() async {
    await _player.stop();
    playing.value = false;
  }
}
