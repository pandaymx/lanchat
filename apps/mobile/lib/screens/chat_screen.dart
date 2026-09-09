import 'package:flutter/material.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../hub_client.dart';
import '../protocol.dart';
import '../widgets/message_bubble.dart';

/// 聊天页：大厅会话（conv = ""）。
/// 历史加载 → 实时消息 → 输入发送 → 已读标记 → 断线重连。
class ChatScreen extends StatefulWidget {
  final HubClient client;
  final int startCursor;

  const ChatScreen({super.key, required this.client, required this.startCursor});

  @override
  State<ChatScreen> createState() => _ChatScreenState();
}

class _ChatScreenState extends State<ChatScreen> {
  final _inputCtrl = TextEditingController();
  final _scrollCtrl = ScrollController();

  HubClient get client => widget.client;

  @override
  void initState() {
    super.initState();
    client.addListener(_onClientChanged);
    client.start(widget.startCursor);
  }

  @override
  void dispose() {
    client.removeListener(_onClientChanged);
    _saveCursor();
    client.close();
    _inputCtrl.dispose();
    _scrollCtrl.dispose();
    super.dispose();
  }

  void _onClientChanged() {
    if (!mounted) return;
    setState(() {});
    _scrollToBottomIfNear();
  }

  void _scrollToBottomIfNear() {
    if (!_scrollCtrl.hasClients) return;
    final pos = _scrollCtrl.position;
    if (pos.maxScrollExtent - pos.pixels < 120) {
      _scrollToBottom();
    }
  }

  void _scrollToBottom() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (_scrollCtrl.hasClients) {
        _scrollCtrl.animateTo(
          _scrollCtrl.position.maxScrollExtent,
          duration: const Duration(milliseconds: 200),
          curve: Curves.easeOut,
        );
      }
    });
  }

  Future<void> _saveCursor() async {
    final prefs = await SharedPreferences.getInstance();
    if (client.cursor > 0) {
      await prefs.setInt('cursor_${client.userId}', client.cursor);
    }
  }

  void _send() {
    final body = _inputCtrl.text.trim();
    if (body.isEmpty) return;
    client.sendMessage(lobbyConversationId, body);
    _inputCtrl.clear();
    _scrollToBottom();
  }

  @override
  Widget build(BuildContext context) {
    final onlineCount = client.onlineUsers.values.where((v) => v).length;

    return Scaffold(
      backgroundColor: const Color(0xFF1B1D22),
      appBar: AppBar(
        backgroundColor: const Color(0xFF20232A),
        foregroundColor: const Color(0xFFE6E8EC),
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text('大厅', style: TextStyle(fontSize: 17, fontWeight: FontWeight.w600)),
            Text(
              client.connected
                  ? '在线 $onlineCount 人'
                  : client.connectionStatus,
              style: TextStyle(
                fontSize: 11,
                color: client.connected
                    ? const Color(0xFF07C160)
                    : const Color(0xFFE86452),
              ),
            ),
          ],
        ),
        actions: [
          IconButton(
            tooltip: '断开',
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
            child: client.messages.isEmpty && !client.connected
                ? const Center(
                    child: Text(
                      '连接中…',
                      style: TextStyle(color: Color(0xFF8B919C)),
                    ),
                  )
                : ListView.builder(
                    controller: _scrollCtrl,
                    padding: const EdgeInsets.symmetric(vertical: 8),
                    itemCount: client.messages.length,
                    itemBuilder: (context, i) {
                      final msg = client.messages[i];
                      return MessageBubble(
                        message: msg,
                        client: client,
                        selfUserId: client.userId,
                      );
                    },
                  ),
          ),
          _composer(),
        ],
      ),
    );
  }

  Widget _composer() {
    return Container(
      color: const Color(0xFF20232A),
      padding: EdgeInsets.only(
        left: 12,
        right: 12,
        top: 10,
        bottom: MediaQuery.of(context).padding.bottom + 10,
      ),
      child: Row(
        children: [
          IconButton(
            onPressed: null,
            icon: const Icon(Icons.add_circle_outline, color: Color(0xFF8B919C)),
            tooltip: '附件（下一迭代）',
          ),
          Expanded(
            child: TextField(
              controller: _inputCtrl,
              style: const TextStyle(color: Color(0xFFE6E8EC), fontSize: 15),
              minLines: 1,
              maxLines: 4,
              onSubmitted: (_) => _send(),
              decoration: InputDecoration(
                hintText: '发消息…',
                hintStyle: const TextStyle(color: Color(0xFF6B7078)),
                filled: true,
                fillColor: const Color(0xFF2B2D33),
                contentPadding: const EdgeInsets.symmetric(horizontal: 14, vertical: 10),
                border: OutlineInputBorder(
                  borderRadius: BorderRadius.circular(20),
                  borderSide: BorderSide.none,
                ),
              ),
            ),
          ),
          const SizedBox(width: 8),
          IconButton.filled(
            onPressed: _send,
            style: IconButton.styleFrom(backgroundColor: const Color(0xFF2B6BFF)),
            icon: const Icon(Icons.send, color: Colors.white, size: 20),
            tooltip: '发送',
          ),
        ],
      ),
    );
  }
}
