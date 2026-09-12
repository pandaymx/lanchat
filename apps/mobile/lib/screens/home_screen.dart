import 'package:flutter/material.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../hub_client.dart';
import '../protocol.dart';
import 'chat_screen.dart';

/// 会话列表页（QQ 风格）：大厅 + 群聊，未读角标，最后消息预览。
class HomeScreen extends StatefulWidget {
  final HubClient client;
  final int startCursor;

  const HomeScreen({super.key, required this.client, required this.startCursor});

  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends State<HomeScreen> {
  HubClient get client => widget.client;

  @override
  void initState() {
    super.initState();
    client.addListener(_onChanged);
    client.start(widget.startCursor);
  }

  @override
  void dispose() {
    client.removeListener(_onChanged);
    _saveCursor();
    client.close();
    super.dispose();
  }

  void _onChanged() {
    if (mounted) setState(() {});
  }

  Future<void> _saveCursor() async {
    final prefs = await SharedPreferences.getInstance();
    if (client.cursor > 0) {
      await prefs.setInt('cursor_${client.userId}', client.cursor);
    }
  }

  void _openChat(ConversationSnapshot conv) {
    client.markRead(conv.id);
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => ChatScreen(client: client, conversationId: conv.id, title: conv.title),
      ),
    );
  }

  String _timeLabel(int atMs) {
    if (atMs <= 0) return '';
    final t = DateTime.fromMillisecondsSinceEpoch(atMs);
    final now = DateTime.now();
    final today = DateTime(now.year, now.month, now.day);
    final day = DateTime(t.year, t.month, t.day);
    String two(int v) => v.toString().padLeft(2, '0');
    if (day == today) return '${two(t.hour)}:${two(t.minute)}';
    final yesterday = today.subtract(const Duration(days: 1));
    if (day == yesterday) return '昨天';
    return '${t.month}/${t.day}';
  }

  String _preview(StoredMessage? m) {
    if (m == null) return '暂无消息';
    if (m.file != null) {
      if (m.file!.mime.startsWith('image/')) return '[图片] ${m.file!.name}';
      return '[文件] ${m.file!.name}';
    }
    if (m.body.isEmpty) return '';
    final one = m.body.replaceAll('\n', ' ');
    return one.length > 28 ? '${one.substring(0, 28)}…' : one;
  }

  @override
  Widget build(BuildContext context) {
    final onlineCount = client.onlineUsers.values.where((v) => v).length;
    final convs = client.sortedConversations;

    return Scaffold(
      backgroundColor: const Color(0xFF17181C),
      appBar: AppBar(
        backgroundColor: const Color(0xFF20232A),
        foregroundColor: const Color(0xFFE6E8EC),
        title: Row(
          children: [
            const Text('lanchat', style: TextStyle(fontSize: 18, fontWeight: FontWeight.w700)),
            const SizedBox(width: 8),
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
              decoration: BoxDecoration(
                color: client.connected
                    ? const Color(0x1A07C160)
                    : const Color(0x1AE86452),
                borderRadius: BorderRadius.circular(999),
              ),
              child: Text(
                client.connected ? '在线 $onlineCount' : client.connectionStatus,
                style: TextStyle(
                  fontSize: 11,
                  color: client.connected ? const Color(0xFF07C160) : const Color(0xFFE86452),
                ),
              ),
            ),
          ],
        ),
        actions: [
          IconButton(
            tooltip: '断开连接',
            icon: const Icon(Icons.link_off),
            onPressed: () {
              client.close();
              Navigator.of(context).pop();
            },
          ),
        ],
      ),
      body: Column(
        children: [
          if (!client.connected)
            Container(
              width: double.infinity,
              color: const Color(0xFF3A2A2A),
              padding: const EdgeInsets.symmetric(vertical: 6, horizontal: 12),
              child: Text(
                client.connectionStatus,
                style: const TextStyle(fontSize: 12, color: Color(0xFFFFB4B4)),
                textAlign: TextAlign.center,
              ),
            ),
          Expanded(
            child: client.connected && convs.isEmpty
                ? const Center(
                    child: Text('加载中…', style: TextStyle(color: Color(0xFF8B919C))),
                  )
                : ListView.separated(
                    itemCount: convs.length,
                    separatorBuilder: (_, __) => const Divider(
                      height: 1,
                      indent: 72,
                      color: Color(0xFF26282E),
                    ),
                    itemBuilder: (context, i) {
                      final conv = convs[i];
                      return _convTile(conv);
                    },
                  ),
          ),
        ],
      ),
    );
  }

  Widget _convTile(ConversationSnapshot conv) {
    final last = client.lastMessageOf(conv.id);
    final unread = client.unreadCount(conv.id);
    final isLobby = conv.id == lobbyConversationId;
    final title = isLobby ? '大厅' : (conv.title.isEmpty ? '群聊' : conv.title);
    final subtitle = isLobby ? '所有人' : '${conv.members.length} 人';

    return ListTile(
      onTap: () => _openChat(conv),
      leading: Container(
        width: 46,
        height: 46,
        decoration: BoxDecoration(
          color: _convColor(conv.id),
          borderRadius: BorderRadius.circular(12),
        ),
        alignment: Alignment.center,
        child: Icon(
          isLobby ? Icons.forum : Icons.group,
          color: Colors.white,
          size: 24,
        ),
      ),
      title: Text(
        title,
        style: const TextStyle(color: Color(0xFFE6E8EC), fontSize: 15, fontWeight: FontWeight.w600),
      ),
      subtitle: Text(
        '${_preview(last)} · $subtitle',
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
        style: const TextStyle(color: Color(0xFF8B919C), fontSize: 12),
      ),
      trailing: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        crossAxisAlignment: CrossAxisAlignment.end,
        children: [
          Text(
            _timeLabel(last?.createdAt ?? 0),
            style: const TextStyle(color: Color(0xFF6B7078), fontSize: 11),
          ),
          const SizedBox(height: 4),
          if (unread > 0)
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
              decoration: BoxDecoration(
                color: const Color(0xFFE86452),
                borderRadius: BorderRadius.circular(999),
              ),
              constraints: const BoxConstraints(minWidth: 18),
              alignment: Alignment.center,
              child: Text(
                unread > 99 ? '99+' : '$unread',
                style: const TextStyle(color: Colors.white, fontSize: 10, fontWeight: FontWeight.w600),
              ),
            )
          else
            const SizedBox(height: 18),
        ],
      ),
    );
  }

  Color _convColor(String id) {
    const colors = [
      Color(0xFF2B6BFF),
      Color(0xFF07C160),
      Color(0xFF9A60B4),
      Color(0xFFE86452),
      Color(0xFFF6BD16),
      Color(0xFF2F9E9B),
    ];
    if (id.isEmpty) return colors[0];
    return colors[id.codeUnitAt(0) % colors.length];
  }
}
