import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'notifier.dart';
import 'screens/connect_screen.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  // 深色沉浸：状态栏/导航栏图标白色，与深色主题一致。
  SystemChrome.setSystemUIOverlayStyle(
    const SystemUiOverlayStyle(
      statusBarColor: Colors.transparent,
      statusBarIconBrightness: Brightness.light,
      statusBarBrightness: Brightness.dark,
      systemNavigationBarColor: Color(0xFF17181C),
      systemNavigationBarIconBrightness: Brightness.light,
    ),
  );
  await Notifier.instance.init();
  runApp(const LanchatApp());
}

class LanchatApp extends StatelessWidget {
  const LanchatApp({super.key});

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'lanchat',
      debugShowCheckedModeBanner: false,
      theme: ThemeData(
        brightness: Brightness.dark,
        scaffoldBackgroundColor: const Color(0xFF17181C),
        colorScheme: const ColorScheme.dark(
          primary: Color(0xFF2B6BFF),
          surface: Color(0xFF1B1D22),
        ),
        useMaterial3: true,
      ),
      home: const ConnectScreen(),
    );
  }
}
