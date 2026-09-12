import 'package:flutter/material.dart';

import '../hub_client.dart';
import 'chat_screen.dart';

/// 联系人页：在线用户 + 群聊列表（QQ 式第二 tab）。
class ContactsScreen extends StatelessWidget {
  final HubClient client;

  const ContactsScreen({super.key, required this.client});

  List<String> get _knownUsers {
    final users = <String>{
      ...client.onlineUsers.keys,
      ...client.messages.map((m) => m.senderUserId),
    }..remove(client.userId);
    users.remove('');
    return users.toList()..sort();
  }

  @override
  Widget build(BuildContext context) {
    final onlineCount = client.onlineUsers.values.where((v) => v).length;
    final groups = client.conversations.values.where((c) => c.kind == 'group').toList();

    return ListView(
      padding: const EdgeInsets.symmetric(vertical: 4),
      children: [
        _sectionHeader('在线用户 $onlineCount'),
        if (_knownUsers.isEmpty)
          const Padding(
            padding: EdgeInsets.fromLTRB(16, 8, 16, 8),
            child: Text('暂无其他用户', style: TextStyle(fontSize: 13, color: Color(0xFF6B7078))),
          )
        else
          ..._knownUsers.map(
            (u) => ListTile(
              leading: Container(
                width: 40,
                height: 40,
                decoration: BoxDecoration(color: _seedColor(u), shape: BoxShape.circle),
                alignment: Alignment.center,
                child: Stack(
                  children: [
                    Center(
                      child: Text(
                        u[0].toUpperCase(),
                        style: const TextStyle(color: Colors.white, fontSize: 15, fontWeight: FontWeight.w600),
                      ),
                    ),
                    if (client.onlineUsers[u] ?? false)
                      Positioned(
                        right: 1,
                        bottom: 1,
                        child: Container(
                          width: 10,
                          height: 10,
                          decoration: BoxDecoration(
                            color: const Color(0xFF07C160),
                            shape: BoxShape.circle,
                            border: Border.all(color: const Color(0xFF17181C), width: 2),
                          ),
                        ),
                      ),
                  ],
                ),
              ),
              title: Text(
                u,
                style: const TextStyle(color: Color(0xFFE6E8EC), fontSize: 15),
              ),
              subtitle: Text(
                client.onlineUsers[u] ?? false ? '在线' : '离线',
                style: TextStyle(
                  fontSize: 12,
                  color: (client.onlineUsers[u] ?? false)
                      ? const Color(0xFF07C160)
                      : const Color(0xFF6B7078),
                ),
              ),
            ),
          ),
        _sectionHeader('群聊 ${groups.length}'),
        if (groups.isEmpty)
          const Padding(
            padding: EdgeInsets.fromLTRB(16, 8, 16, 8),
            child: Text('暂无群聊，去消息页右上角创建', style: TextStyle(fontSize: 13, color: Color(0xFF6B7078))),
          )
        else
          ...groups.map(
            (g) => ListTile(
              onTap: () {
                client.markRead(g.id);
                Navigator.of(context).push(
                  MaterialPageRoute(
                    builder: (_) => ChatScreen(client: client, conversationId: g.id, title: g.title),
                  ),
                );
              },
              leading: Container(
                width: 40,
                height: 40,
                decoration: BoxDecoration(
                  color: _seedColor(g.id),
                  borderRadius: BorderRadius.circular(10),
                ),
                alignment: Alignment.center,
                child: const Icon(Icons.group, color: Colors.white, size: 22),
              ),
              title: Text(
                g.title.isEmpty ? '群聊' : g.title,
                style: const TextStyle(color: Color(0xFFE6E8EC), fontSize: 15),
              ),
              subtitle: Text(
                '${g.members.length} 人',
                style: const TextStyle(color: Color(0xFF8B919C), fontSize: 12),
              ),
            ),
          ),
      ],
    );
  }

  Widget _sectionHeader(String text) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
      child: Text(
        text,
        style: const TextStyle(fontSize: 12, fontWeight: FontWeight.w600, color: Color(0xFF8B919C)),
      ),
    );
  }

  Color _seedColor(String u) {
    const colors = [
      Color(0xFF5B8FF9),
      Color(0xFF61C766),
      Color(0xFFF6BD16),
      Color(0xFFE86452),
      Color(0xFF9A60B4),
      Color(0xFF2F9E9B),
    ];
    if (u.isEmpty) return colors[0];
    return colors[u.codeUnitAt(0) % colors.length];
  }
}
