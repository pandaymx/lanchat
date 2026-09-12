import 'dart:async';

import 'package:flutter/material.dart';

import '../hub_client.dart';
import '../protocol.dart';
import 'chat_screen.dart';

/// 历史消息搜索页（v1.1 FKSearchReq）。结果按 seq 降序，点击进对应会话。
class SearchScreen extends StatefulWidget {
  final HubClient client;

  const SearchScreen({super.key, required this.client});

  @override
  State<SearchScreen> createState() => _SearchScreenState();
}

class _SearchScreenState extends State<SearchScreen> {
  final _ctrl = TextEditingController();
  Timer? _debounce;
  bool _searched = false;

  HubClient get client => widget.client;

  @override
  void initState() {
    super.initState();
    client.addListener(_onChanged);
    client.clearSearch();
  }

  @override
  void dispose() {
    client.removeListener(_onChanged);
    _ctrl.dispose();
    _debounce?.cancel();
    client.clearSearch();
    super.dispose();
  }

  void _onChanged() {
    if (mounted) setState(() {});
  }

  void _onQueryChanged(String q) {
    _debounce?.cancel();
    if (q.trim().isEmpty) {
      client.clearSearch();
      setState(() => _searched = false);
      return;
    }
    _debounce = Timer(const Duration(milliseconds: 350), () {
      client.search(q);
      setState(() => _searched = true);
    });
  }

  String _convTitle(String convId) {
    if (convId == lobbyConversationId) return '大厅';
    final c = client.conversations[convId];
    return (c == null || c.title.isEmpty) ? '群聊' : c.title;
  }

  void _openHit(StoredMessage m) {
    client.markRead(m.conversationId);
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => ChatScreen(
          client: client,
          conversationId: m.conversationId,
          title: _convTitle(m.conversationId),
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final results = client.searchResults;
    return Scaffold(
      backgroundColor: const Color(0xFF17181C),
      appBar: AppBar(
        backgroundColor: const Color(0xFF20232A),
        foregroundColor: const Color(0xFFE6E8EC),
        title: TextField(
          controller: _ctrl,
          autofocus: true,
          onChanged: _onQueryChanged,
          style: const TextStyle(color: Color(0xFFE6E8EC), fontSize: 16),
          decoration: const InputDecoration(
            hintText: '搜索历史消息…',
            hintStyle: TextStyle(color: Color(0xFF6B7078)),
            border: InputBorder.none,
          ),
        ),
        actions: [
          if (_ctrl.text.isNotEmpty)
            IconButton(
              icon: const Icon(Icons.clear, size: 20),
              onPressed: () {
                _ctrl.clear();
                _onQueryChanged('');
              },
            ),
        ],
      ),
      body: _buildBody(results),
    );
  }

  Widget _buildBody(List<StoredMessage> results) {
    if (client.searching) {
      return const Center(
        child: CircularProgressIndicator(strokeWidth: 2, color: Color(0xFF2B6BFF)),
      );
    }
    if (client.searchError != null) {
      return Center(
        child: Text('搜索失败: ${client.searchError}', style: const TextStyle(color: Color(0xFFFFB4B4))),
      );
    }
    if (!_searched) {
      return const Center(
        child: Text('输入关键词搜索全部会话', style: TextStyle(color: Color(0xFF6B7078))),
      );
    }
    if (results.isEmpty) {
      return const Center(
        child: Text('没有匹配的消息', style: TextStyle(color: Color(0xFF6B7078))),
      );
    }
    return ListView.separated(
      itemCount: results.length,
      separatorBuilder: (_, __) => const Divider(height: 1, indent: 16, color: Color(0xFF26282E)),
      itemBuilder: (context, i) {
        final m = results[i];
        return ListTile(
          onTap: () => _openHit(m),
          leading: Container(
            width: 38,
            height: 38,
            decoration: BoxDecoration(color: _seedColor(m.senderUserId), shape: BoxShape.circle),
            alignment: Alignment.center,
            child: Text(
              m.senderUserId.isEmpty ? '?' : m.senderUserId[0].toUpperCase(),
              style: const TextStyle(color: Colors.white, fontSize: 14, fontWeight: FontWeight.w600),
            ),
          ),
          title: Text(
            m.body.isEmpty ? (m.file?.name ?? '[文件]') : m.body,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: const TextStyle(color: Color(0xFFE6E8EC), fontSize: 14),
          ),
          subtitle: Text(
            '${m.senderUserId} · ${_convTitle(m.conversationId)}',
            style: const TextStyle(color: Color(0xFF8B919C), fontSize: 12),
          ),
        );
      },
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
