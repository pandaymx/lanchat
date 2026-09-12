import 'dart:io';

import 'package:flutter/material.dart';
import 'package:image_picker/image_picker.dart';

import '../api.dart';
import '../hub_client.dart';
import '../protocol.dart';
import '../widgets/message_bubble.dart';

/// 聊天页：指定会话（大厅 conv="" 或群聊）。
/// 历史加载 → 实时消息 → 文本/图片发送 → 已读 → 断线重连。
class ChatScreen extends StatefulWidget {
  final HubClient client;
  final String conversationId;
  final String title;

  const ChatScreen({
    super.key,
    required this.client,
    required this.conversationId,
    required this.title,
  });

  @override
  State<ChatScreen> createState() => _ChatScreenState();
}

class _ChatScreenState extends State<ChatScreen> {
  final _inputCtrl = TextEditingController();
  final _scrollCtrl = ScrollController();
  bool _uploading = false;

  HubClient get client => widget.client;
  String get convId => widget.conversationId;
  bool get _isLobby => convId == lobbyConversationId;

  @override
  void initState() {
    super.initState();
    client.addListener(_onClientChanged);
    // 进入会话即已读（列表页已 markRead，这里兜底新消息）。
    WidgetsBinding.instance.addPostFrameCallback((_) => client.markRead(convId));
  }

  @override
  void dispose() {
    client.removeListener(_onClientChanged);
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

  void _send() {
    final body = _inputCtrl.text.trim();
    if (body.isEmpty) return;
    client.sendMessage(convId, body);
    _inputCtrl.clear();
    _scrollToBottom();
  }

  Future<void> _pickAndSendImage() async {
    final picker = ImagePicker();
    final XFile? picked;
    try {
      picked = await picker.pickImage(source: ImageSource.gallery, imageQuality: 85);
    } catch (e) {
      _toast('无法打开图库: $e');
      return;
    }
    if (picked == null) return;
    setState(() => _uploading = true);
    try {
      final api = HubApi(host: client.host, port: client.port);
      final file = File(picked.path);
      final ref = await api.uploadFile(file, mime: 'image/jpeg');
      if (!mounted) return;
      client.sendFileMessage(convId, ref);
      _scrollToBottom();
    } catch (e) {
      _toast('上传失败: $e');
    } finally {
      if (mounted) setState(() => _uploading = false);
    }
  }

  void _toast(String msg) {
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(msg)));
  }

  @override
  Widget build(BuildContext context) {
    final messages = client.messagesOf(convId);
    final onlineCount = client.onlineUsers.values.where((v) => v).length;
    final title = widget.title.isEmpty && _isLobby ? '大厅' : widget.title;

    return Scaffold(
      backgroundColor: const Color(0xFF1B1D22),
      appBar: AppBar(
        backgroundColor: const Color(0xFF20232A),
        foregroundColor: const Color(0xFFE6E8EC),
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(title.isEmpty ? '群聊' : title,
                style: const TextStyle(fontSize: 17, fontWeight: FontWeight.w600)),
            Text(
              client.connected ? '在线 $onlineCount 人' : client.connectionStatus,
              style: TextStyle(
                fontSize: 11,
                color: client.connected ? const Color(0xFF07C160) : const Color(0xFFE86452),
              ),
            ),
          ],
        ),
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
            child: messages.isEmpty && !client.connected
                ? const Center(
                    child: Text('连接中…', style: TextStyle(color: Color(0xFF8B919C))),
                  )
                : ListView.builder(
                    controller: _scrollCtrl,
                    padding: const EdgeInsets.symmetric(vertical: 8),
                    itemCount: messages.length,
                    itemBuilder: (context, i) => MessageBubble(
                      message: messages[i],
                      client: client,
                      selfUserId: client.userId,
                    ),
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
        left: 8,
        right: 12,
        top: 10,
        bottom: MediaQuery.of(context).padding.bottom + 10,
      ),
      child: Row(
        children: [
          IconButton(
            onPressed: _uploading ? null : _pickAndSendImage,
            icon: _uploading
                ? const SizedBox(
                    width: 20,
                    height: 20,
                    child: CircularProgressIndicator(strokeWidth: 2, color: Color(0xFF8B919C)),
                  )
                : const Icon(Icons.add_circle_outline, color: Color(0xFF8B919C)),
            tooltip: '发送图片',
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
