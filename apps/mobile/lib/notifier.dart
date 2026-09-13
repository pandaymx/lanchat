import 'package:flutter_local_notifications/flutter_local_notifications.dart';
import 'package:shared_preferences/shared_preferences.dart';

/// 新消息系统通知（Android 通知栏）。
class Notifier {
  Notifier._();

  static final Notifier instance = Notifier._();

  final FlutterLocalNotificationsPlugin _plugin = FlutterLocalNotificationsPlugin();
  bool _ready = false;

  Future<void> init() async {
    final ok = await _plugin.initialize(
      settings: const InitializationSettings(
        android: AndroidInitializationSettings('@mipmap/ic_launcher'),
        iOS: DarwinInitializationSettings(),
      ),
      onDidReceiveNotificationResponse: (resp) async {
        // 点击通知：把会话 id 暂存，HomeScreen 打开后自动跳转。
        final p = resp.payload;
        if (p != null && p.isNotEmpty) {
          final prefs = await SharedPreferences.getInstance();
          await prefs.setString('notify_conv', p);
        }
      },
    );
    if (ok == false) return;
    await _plugin
        .resolvePlatformSpecificImplementation<AndroidFlutterLocalNotificationsPlugin>()
        ?.requestNotificationsPermission();
    _ready = true;
  }

  Future<void> show(String title, String body, {String? payload}) async {
    if (!_ready) return;
    await _plugin.show(
      id: 1,
      title: title,
      body: body,
      payload: payload,
      notificationDetails: const NotificationDetails(
        android: AndroidNotificationDetails(
          'lanchat_messages',
          '新消息',
          channelDescription: '收到新的聊天消息',
          importance: Importance.high,
          priority: Priority.high,
          playSound: true,
          enableVibration: true,
        ),
        iOS: DarwinNotificationDetails(),
      ),
    );
  }
}
