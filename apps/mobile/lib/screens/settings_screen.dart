import 'package:flutter/material.dart';
import 'package:package_info_plus/package_info_plus.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:url_launcher/url_launcher.dart';

import '../hub_client.dart';

/// 设置页：连接信息、通知开关、清除本地数据、关于。
class SettingsScreen extends StatefulWidget {
  final HubClient client;

  const SettingsScreen({super.key, required this.client});

  @override
  State<SettingsScreen> createState() => _SettingsScreenState();
}

class _SettingsScreenState extends State<SettingsScreen> {
  HubClient get client => widget.client;
  bool _notifyOn = true;
  String _version = '';
  int _fontScale = 15;

  @override
  void initState() {
    super.initState();
    _load();
    _loadVersion();
  }

  Future<void> _loadVersion() async {
    String v = '';
    try {
      final info = await PackageInfo.fromPlatform();
      v = '${info.version}+${info.buildNumber}';
    } catch (_) {}
    if (!mounted) return;
    setState(() => _version = v);
  }

  Future<void> _load() async {
    final prefs = await SharedPreferences.getInstance();
    if (!mounted) return;
    setState(() {
      _notifyOn = prefs.getBool('notify_enabled') ?? true;
      _fontScale = prefs.getInt('font_scale') ?? 15;
    });
  }

  Future<void> _setFontScale(int v) async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setInt('font_scale', v);
  }

  Future<void> _setNotify(bool v) async {
    setState(() => _notifyOn = v);
    final prefs = await SharedPreferences.getInstance();
    await prefs.setBool('notify_enabled', v);
  }

  /// 打开 GitHub 项目主页。
  Future<void> _openRepo() async {
    const url = 'https://github.com/pandaymx/lanchat';
    try {
      final ok = await launchUrl(Uri.parse(url), mode: LaunchMode.externalApplication);
      if (!ok) {
        if (mounted) {
          ScaffoldMessenger.of(context).showSnackBar(
            const SnackBar(content: Text('无法打开浏览器'), behavior: SnackBarBehavior.floating),
          );
        }
      }
    } catch (_) {}
  }

  Future<void> _clearData(BuildContext context) async {    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: const Color(0xFF20232A),
        title: const Text('清除本地数据', style: TextStyle(color: Color(0xFFE6E8EC))),
        content: const Text('将清除本地游标与记住的连接参数，重新打开后需重新连接。', style: TextStyle(color: Color(0xFF8B919C))),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('取消', style: TextStyle(color: Color(0xFF8B919C))),
          ),
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(true),
            child: const Text('清除', style: TextStyle(color: Color(0xFFE86452))),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    final prefs = await SharedPreferences.getInstance();
    await prefs.clear();
    if (context.mounted) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('本地数据已清除')),
      );
    }
  }

  /// 退出登录：断开连接、清除连接参数，回到连接页。
  Future<void> _logout(BuildContext context) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: const Color(0xFF20232A),
        title: const Text('退出登录', style: TextStyle(color: Color(0xFFE6E8EC))),
        content: const Text('将断开与 hub 的连接，并返回连接页。', style: TextStyle(color: Color(0xFF8B919C))),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('取消', style: TextStyle(color: Color(0xFF8B919C))),
          ),
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(true),
            child: const Text('退出', style: TextStyle(color: Color(0xFFE86452))),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    client.close();
    final prefs = await SharedPreferences.getInstance();
    await prefs.remove('hub_host');
    await prefs.remove('hub_port');
    await prefs.remove('hub_user');
    await prefs.remove('hub_device');
    if (context.mounted) {
      Navigator.of(context).popUntil((r) => r.isFirst);
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: const Color(0xFF17181C),
      appBar: AppBar(
        backgroundColor: const Color(0xFF20232A),
        foregroundColor: const Color(0xFFE6E8EC),
        title: const Text('设置', style: TextStyle(fontSize: 17, fontWeight: FontWeight.w600)),
      ),
      body: ListView(
        children: [
          const Padding(
            padding: EdgeInsets.fromLTRB(16, 12, 16, 4),
            child: Text('连接', style: TextStyle(fontSize: 12, fontWeight: FontWeight.w600, color: Color(0xFF8B919C))),
          ),
          ListTile(
            leading: const Icon(Icons.dns, color: Color(0xFF8B919C)),
            title: const Text('服务器', style: TextStyle(color: Color(0xFFE6E8EC), fontSize: 15)),
            subtitle: Text('${client.host}:${client.port}', style: const TextStyle(color: Color(0xFF8B919C), fontSize: 13)),
          ),
          ListTile(
            leading: const Icon(Icons.person, color: Color(0xFF8B919C)),
            title: const Text('用户', style: TextStyle(color: Color(0xFFE6E8EC), fontSize: 15)),
            subtitle: Text(client.userId, style: const TextStyle(color: Color(0xFF8B919C), fontSize: 13)),
          ),
          ListTile(
            leading: const Icon(Icons.phone_android, color: Color(0xFF8B919C)),
            title: const Text('设备', style: TextStyle(color: Color(0xFFE6E8EC), fontSize: 15)),
            subtitle: Text(client.deviceId, style: const TextStyle(color: Color(0xFF8B919C), fontSize: 13)),
          ),
          const Divider(height: 1, color: Color(0xFF26282E)),
          SwitchListTile(
            secondary: const Icon(Icons.notifications_outlined, color: Color(0xFF8B919C)),
            title: const Text('消息通知', style: TextStyle(color: Color(0xFFE6E8EC), fontSize: 15)),
            subtitle: const Text('新消息到达时在通知栏提醒', style: TextStyle(color: Color(0xFF8B919C), fontSize: 12)),
            value: _notifyOn,
            activeTrackColor: const Color(0xFF2B6BFF),
            onChanged: _setNotify,
          ),
          const Divider(height: 1, color: Color(0xFF26282E)),
          ListTile(
            leading: const Icon(Icons.format_size, color: Color(0xFF8B919C)),
            title: const Text('消息字体大小', style: TextStyle(color: Color(0xFFE6E8EC), fontSize: 15)),
            subtitle: Text('${_fontScale.toInt()}', style: const TextStyle(color: Color(0xFF8B919C), fontSize: 12)),
            trailing: SizedBox(
              width: 140,
              child: Slider(
                value: _fontScale.clamp(12, 20).toDouble(),
                min: 12,
                max: 20,
                divisions: 8,
                activeColor: const Color(0xFF2B6BFF),
                inactiveColor: const Color(0xFF2B2D33),
                label: '${_fontScale.toInt()}',
                onChanged: (v) => setState(() => _fontScale = v.round()),
                onChangeEnd: (v) => _setFontScale(v.round()),
              ),
            ),
          ),
          const Divider(height: 1, color: Color(0xFF26282E)),
          ListTile(
            leading: const Icon(Icons.logout, color: Color(0xFFE86452)),
            title: const Text('退出登录', style: TextStyle(color: Color(0xFFE86452), fontSize: 15)),
            onTap: () => _logout(context),
          ),
          const Divider(height: 1, color: Color(0xFF26282E)),
          ListTile(
            leading: const Icon(Icons.delete_forever, color: Color(0xFFE86452)),
            title: const Text('清除本地数据', style: TextStyle(color: Color(0xFFE86452), fontSize: 15)),
            onTap: () => _clearData(context),
          ),
          const Divider(height: 1, color: Color(0xFF26282E)),
          const Padding(
            padding: EdgeInsets.fromLTRB(16, 12, 16, 4),
            child: Text('关于', style: TextStyle(fontSize: 12, fontWeight: FontWeight.w600, color: Color(0xFF8B919C))),
          ),
          const ListTile(
            leading: Icon(Icons.info_outline, color: Color(0xFF8B919C)),
            title: Text('lanchat 移动端', style: TextStyle(color: Color(0xFFE6E8EC), fontSize: 15)),
            subtitle: Text('局域网即时通讯 · 直连 hub', style: TextStyle(color: Color(0xFF8B919C), fontSize: 13)),
          ),
          ListTile(
            leading: const Icon(Icons.link, color: Color(0xFF8B919C)),
            title: const Text('项目主页', style: TextStyle(color: Color(0xFFE6E8EC), fontSize: 15)),
            subtitle: const Text('github.com/pandaymx/lanchat', style: TextStyle(color: Color(0xFF8B919C), fontSize: 13)),
            trailing: const Icon(Icons.open_in_new, size: 16, color: Color(0xFF6A707A)),
            onTap: _openRepo,
          ),
          if (_version.isNotEmpty)
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 0, 16, 10),
              child: Text(
                '版本 $_version',
                style: const TextStyle(fontSize: 12, color: Color(0xFF6A707A)),
              ),
            ),
        ],
      ),
    );
  }
}
