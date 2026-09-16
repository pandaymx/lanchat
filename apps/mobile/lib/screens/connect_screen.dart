import 'package:flutter/material.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../embedded_hub.dart';
import '../hub_client.dart';
import 'home_screen.dart';

/// 连接页：host / port / 用户名 / 设备名。
/// 参数持久化（shared_preferences），下次打开免输入。
class ConnectScreen extends StatefulWidget {
  const ConnectScreen({super.key});

  @override
  State<ConnectScreen> createState() => _ConnectScreenState();
}

class _ConnectScreenState extends State<ConnectScreen> {
  final _hostCtrl = TextEditingController();
  final _portCtrl = TextEditingController(text: '9000');
  final _userCtrl = TextEditingController();
  final _deviceCtrl = TextEditingController();
  bool _connecting = false;
  /// 本机模式：连接时先起进程内嵌入式 hub（默认开，失败回退手动地址）。
  bool _embedded = true;
  List<String> _recent = const [];

  @override
  void initState() {
    super.initState();
    _loadSaved();
  }

  Future<void> _loadSaved() async {
    final prefs = await SharedPreferences.getInstance();
    if (!mounted) return;
    setState(() {
      _hostCtrl.text = prefs.getString('hub_host') ?? '';
      _portCtrl.text = prefs.getString('hub_port') ?? '9000';
      _userCtrl.text = prefs.getString('hub_user') ?? '';
      _deviceCtrl.text = prefs.getString('hub_device') ?? '';
      _recent = prefs.getStringList('recent_conns') ?? const [];
    });
  }

  Future<void> _rememberRecent(String host, int port, String user) async {
    final entry = '$host:$port:$user';
    final next = [
      entry,
      ..._recent.where((e) => e != entry),
    ].take(5).toList();
    if (!mounted) return;
    setState(() => _recent = next);
    final prefs = await SharedPreferences.getInstance();
    await prefs.setStringList('recent_conns', next);
  }

  Future<void> _connect() async {
    var host = _hostCtrl.text.trim();
    var port = int.tryParse(_portCtrl.text.trim()) ?? 9000;
    final user = _userCtrl.text.trim();
    var device = _deviceCtrl.text.trim();
    if (host.isEmpty || user.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('请填写 hub 地址和用户名')),
      );
      return;
    }
    if (device.isEmpty) {
      device = 'flutter-${DateTime.now().millisecondsSinceEpoch % 100000}';
    }

    // 本机模式：先起进程内嵌入式 hub，拿本地 ws 地址；失败回退手动。
    var embeddedAddr = _embedded ? await EmbeddedHub.start(device) : null;
    if (embeddedAddr != null) {
      final uri = Uri.parse(embeddedAddr);
      host = uri.host;
      port = uri.port;
    }
    setState(() => _connecting = true);

    final prefs = await SharedPreferences.getInstance();
    await prefs.setString('hub_host', host);
    await prefs.setString('hub_port', port.toString());
    await prefs.setString('hub_user', user);
    await prefs.setString('hub_device', device);

    final client = HubClient(host: host, port: port, userId: user, deviceId: device);
    final cursor = prefs.getInt('cursor_$user') ?? 0;
    await _rememberRecent(host, port, user);

    if (!mounted) return;
    Navigator.of(context).pushReplacement(
      MaterialPageRoute(
        builder: (_) => HomeScreen(client: client, startCursor: cursor),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('连接 hub')),
      body: Container(
        decoration: const BoxDecoration(
          gradient: LinearGradient(
            begin: Alignment.topCenter,
            end: Alignment.bottomCenter,
            colors: [Color(0xFF20232A), Color(0xFF17181C)],
          ),
        ),
        child: SafeArea(
          child: Center(
            child: SingleChildScrollView(
              padding: const EdgeInsets.symmetric(horizontal: 32, vertical: 24),
              child: Column(
                mainAxisAlignment: MainAxisAlignment.center,
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  ClipRRect(
                    borderRadius: BorderRadius.circular(16),
                    child: Image.asset(
                      'assets/icon_512.png',
                      width: 56,
                      height: 56,
                    ),
                  ),
                  const SizedBox(height: 12),
                  const Text(
                    'lanchat',
                    textAlign: TextAlign.center,
                    style: TextStyle(
                      fontSize: 26,
                      fontWeight: FontWeight.w700,
                      color: Color(0xFFE6E8EC),
                    ),
                  ),
                  const SizedBox(height: 4),
                  const Text(
                    '局域网即时通讯',
                    textAlign: TextAlign.center,
                    style: TextStyle(fontSize: 13, color: Color(0xFF8B919C)),
                  ),
                  SwitchListTile(
                    value: _embedded,
                    onChanged: (v) => setState(() => _embedded = v),
                    title: const Text('本机模式（内嵌 hub）'),
                    subtitle: const Text('启动时本机自动起 hub，无需外部地址'),
                    contentPadding: EdgeInsets.zero,
                    activeThumbColor: const Color(0xFF4E8CFF),
                    activeTrackColor: const Color(0xFF2A3A5C),
                    inactiveThumbColor: const Color(0xFF8B919C),
                    inactiveTrackColor: const Color(0xFF2E3138),
                    tileColor: const Color(0xFF262A32),
                    shape: RoundedRectangleBorder(
                      borderRadius: BorderRadius.circular(12),
                    ),
                  ),
                  const SizedBox(height: 16),
                  _field(_hostCtrl, 'hub 地址', Icons.dns, hint: '192.168.1.10'),
                  const SizedBox(height: 12),
                  _field(_portCtrl, '端口', Icons.numbers, keyboard: TextInputType.number),
                  const SizedBox(height: 12),
                  _field(_userCtrl, '用户名', Icons.person),
                  const SizedBox(height: 12),
                  _field(_deviceCtrl, '设备名（可选）', Icons.phone_android),
                  if (_recent.isNotEmpty) ...[
                    const SizedBox(height: 16),
                    Wrap(
                      spacing: 8,
                      runSpacing: 8,
                      children: _recent.map((e) {
                        final parts = e.split(':');
                        final label = parts.length == 3 ? '${parts[0]}:${parts[1]} · ${parts[2]}' : e;
                        return ActionChip(
                          backgroundColor: const Color(0xFF26282E),
                          side: BorderSide.none,
                          label: Text(
                            label,
                            style: const TextStyle(fontSize: 12, color: Color(0xFFB6BAC2)),
                          ),
                          onPressed: () {
                            if (parts.length == 3) {
                              _hostCtrl.text = parts[0];
                              _portCtrl.text = parts[1];
                              _userCtrl.text = parts[2];
                            }
                          },
                        );
                      }).toList(),
                    ),
                  ],
                  const SizedBox(height: 28),
                  SizedBox(
                    height: 48,
                    child: FilledButton(
                      onPressed: _connecting ? null : _connect,
                      style: FilledButton.styleFrom(
                        backgroundColor: const Color(0xFF2B6BFF),
                        foregroundColor: Colors.white,
                        shape: RoundedRectangleBorder(
                          borderRadius: BorderRadius.circular(12),
                        ),
                      ),
                      child: _connecting
                          ? const SizedBox(
                              width: 20,
                              height: 20,
                              child: CircularProgressIndicator(strokeWidth: 2, color: Colors.white),
                            )
                          : const Text('连接', style: TextStyle(fontSize: 16)),
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }

  Widget _field(
    TextEditingController ctrl,
    String label,
    IconData icon, {
    String? hint,
    TextInputType? keyboard,
  }) {
    return TextField(
      controller: ctrl,
      keyboardType: keyboard,
      style: const TextStyle(color: Color(0xFFE6E8EC)),
      decoration: InputDecoration(
        labelText: label,
        hintText: hint,
        prefixIcon: Icon(icon, color: const Color(0xFF8B919C)),
        filled: true,
        fillColor: const Color(0xFF26282E),
        labelStyle: const TextStyle(color: Color(0xFF8B919C)),
        border: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: BorderSide.none,
        ),
        focusedBorder: OutlineInputBorder(
          borderRadius: BorderRadius.circular(12),
          borderSide: const BorderSide(color: Color(0xFF2B6BFF)),
        ),
      ),
    );
  }
}
