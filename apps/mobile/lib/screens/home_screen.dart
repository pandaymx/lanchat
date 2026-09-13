import 'package:flutter/material.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../hub_client.dart';
import '../notifier.dart';
import '../protocol.dart';
import 'chat_screen.dart';
import 'contacts_screen.dart';
import 'search_screen.dart';
import 'settings_screen.dart';

/// 会话列表页（QQ 风格）：大厅 + 群聊，未读角标，最后消息预览。
class HomeScreen extends StatefulWidget {
  final HubClient client;
  final int startCursor;

  const HomeScreen({super.key, required this.client, required this.startCursor});

  @override
  State<HomeScreen> createState() => _HomeScreenState();
}

class _HomeScreenState extends State<HomeScreen> with WidgetsBindingObserver {
  HubClient get client => widget.client;
  int _tab = 0;
  final Set<String> _pinned = {};
  final Set<String> _muted = {};
  bool _appForeground = true; // 前台不弹系统通知
  DateTime? _lastBackAt; // 双击返回退出

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    client.addListener(_onChanged);
    client.start(widget.startCursor);
    _loadConvPrefs();
    _loadNotifyPref();
    _loadDrafts();
    WidgetsBinding.instance.addPostFrameCallback((_) => _openNotifyConv());
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    _appForeground = state == AppLifecycleState.resumed;
  }

  /// 通知点击跳转：App 从后台打开后进入对应会话。
  Future<void> _openNotifyConv() async {
    final prefs = await SharedPreferences.getInstance();
    final convId = prefs.getString('notify_conv');
    if (convId == null || convId.isEmpty) return;
    await prefs.remove('notify_conv');
    if (!mounted || !client.connected) return;
    final conv = client.conversations[convId];
    if (conv == null) return; // 会话尚未加载（首屏拉取中），跳过自动跳转
    client.markRead(convId);
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => ChatScreen(client: client, conversationId: convId, title: conv.title),
      ),
    );
  }

  Future<void> _loadConvPrefs() async {
    final prefs = await SharedPreferences.getInstance();
    final pins = prefs.getStringList('pinned_convs') ?? const [];
    final mutes = prefs.getStringList('muted_convs') ?? const [];
    if (!mounted) return;
    setState(() {
      _pinned
        ..clear()
        ..addAll(pins);
      _muted
        ..clear()
        ..addAll(mutes);
    });
  }

  Future<void> _saveConvPrefs() async {
    final prefs = await SharedPreferences.getInstance();
    await prefs.setStringList('pinned_convs', _pinned.toList());
    await prefs.setStringList('muted_convs', _muted.toList());
  }

  void _togglePin(ConversationSnapshot conv) {
    setState(() {
      if (!_pinned.remove(conv.id)) _pinned.add(conv.id);
    });
    _saveConvPrefs();
  }

  void _toggleMute(ConversationSnapshot conv) {
    setState(() {
      if (!_muted.remove(conv.id)) _muted.add(conv.id);
    });
    _saveConvPrefs();
  }

  void _onConvLongPress(ConversationSnapshot conv) {
    final pinned = _pinned.contains(conv.id);
    final muted = _muted.contains(conv.id);
    final title = conv.id == lobbyConversationId ? '大厅' : (conv.title.isEmpty ? '群聊' : conv.title);
    showModalBottomSheet<void>(
      context: context,
      backgroundColor: const Color(0xFF20232A),
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
      ),
      builder: (ctx) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Padding(
              padding: const EdgeInsets.all(16),
              child: Text(
                title,
                style: const TextStyle(fontSize: 16, fontWeight: FontWeight.w700, color: Color(0xFFE6E8EC)),
              ),
            ),
            ListTile(
              leading: Icon(pinned ? Icons.push_pin : Icons.push_pin_outlined,
                  color: pinned ? const Color(0xFF2B6BFF) : const Color(0xFF8B919C)),
              title: Text(pinned ? '取消置顶' : '置顶会话',
                  style: const TextStyle(color: Color(0xFFE6E8EC), fontSize: 15)),
              onTap: () {
                Navigator.of(ctx).pop();
                _togglePin(conv);
              },
            ),
            ListTile(
              leading: Icon(muted ? Icons.notifications_off : Icons.notifications_off_outlined,
                  color: muted ? const Color(0xFFE86452) : const Color(0xFF8B919C)),
              title: Text(muted ? '取消免打扰' : '消息免打扰',
                  style: const TextStyle(color: Color(0xFFE6E8EC), fontSize: 15)),
              onTap: () {
                Navigator.of(ctx).pop();
                _toggleMute(conv);
              },
            ),
            if (client.unreadCount(conv.id) > 0)
              ListTile(
                leading: const Icon(Icons.done_all, color: Color(0xFF2B6BFF)),
                title: const Text('标记已读', style: TextStyle(color: Color(0xFFE6E8EC), fontSize: 15)),
                onTap: () {
                  Navigator.of(ctx).pop();
                  client.markRead(conv.id);
                },
              ),
            const SizedBox(height: 8),
          ],
        ),
      ),
    );
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    client.removeListener(_onChanged);
    _saveCursor();
    client.close();
    super.dispose();
  }

  void _onChanged() {
    if (mounted) setState(() {});
    _maybeNotifyNewMessages();
  }

  final Map<String, int> _lastUnread = {};
  bool _notifyEnabled = true;
  bool _bannerShowing = false;
  final Map<String, String> _drafts = {}; // convId -> 未发送草稿

  /// 读取全部会话草稿（prefs 前缀 draft_）。
  Future<void> _loadDrafts() async {
    final prefs = await SharedPreferences.getInstance();
    final next = <String, String>{};
    for (final k in prefs.getKeys()) {
      if (k.startsWith('draft_')) {
        final v = prefs.getString(k) ?? '';
        if (v.isNotEmpty) next[k.substring(6)] = v;
      }
    }
    if (!mounted) return;
    setState(() => _drafts
      ..clear()
      ..addAll(next));
  }

  Future<void> _loadNotifyPref() async {
    final prefs = await SharedPreferences.getInstance();
    if (!mounted) return;
    setState(() => _notifyEnabled = prefs.getBool('notify_enabled') ?? true);
  }

  /// 未读增量 → 系统通知（前台横幅、免打扰会话、通知开关关闭时不弹）。
  void _maybeNotifyNewMessages() {
    if (_muted.isEmpty && _lastUnread.isEmpty) return;
    if (!_notifyEnabled) return;
    final now = <String, int>{};
    String? banner;
    String? bannerConv;
    for (final c in client.sortedConversations) {
      final u = client.unreadCount(c.id);
      now[c.id] = u;
      final prev = _lastUnread[c.id] ?? 0;
      if (u > prev && !_muted.contains(c.id)) {
        final last = client.lastMessageOf(c.id);
        final isLobby = c.id == lobbyConversationId;
        final title = isLobby ? '大厅' : (c.title.isEmpty ? '群聊' : c.title);
        final preview = _preview(last);
        if (preview.isNotEmpty) {
          if (_appForeground) {
            banner ??= '$title：$preview';
            bannerConv ??= c.id;
          } else {
            Notifier.instance.show('$title：$preview', '来自 ${last?.senderUserId ?? ''}', payload: c.id);
          }
        }
      }
    }
    _lastUnread
      ..clear()
      ..addAll(now);
    if (banner != null) _showBanner(banner, bannerConv);
  }

  /// 前台新消息横幅（SnackBar，弱提示；「查看」直达会话）。
  void _showBanner(String text, String? convId) {
    if (!mounted || _bannerShowing) return;
    _bannerShowing = true;
    final conv = convId == null ? null : client.conversations[convId];
    ScaffoldMessenger.of(context)
      ..hideCurrentSnackBar()
      ..showSnackBar(SnackBar(
        content: Row(
          children: [
            const Icon(Icons.chat_bubble, size: 16, color: Color(0xFF2B6BFF)),
            const SizedBox(width: 8),
            Expanded(child: Text(text, maxLines: 1, overflow: TextOverflow.ellipsis)),
          ],
        ),
        action: conv == null
            ? null
            : SnackBarAction(
                label: '查看',
                textColor: const Color(0xFF2B6BFF),
                onPressed: () => _openChat(conv),
              ),
        behavior: SnackBarBehavior.floating,
        duration: const Duration(seconds: 2),
        margin: const EdgeInsets.only(bottom: 76, left: 12, right: 12),
        backgroundColor: const Color(0xFF2B2D33),
      ))
      .closed.then((_) => _bannerShowing = false);
  }

  Future<void> _saveCursor() async {
    final prefs = await SharedPreferences.getInstance();
    if (client.cursor > 0) {
      await prefs.setInt('cursor_${client.userId}', client.cursor);
    }
  }

  void _openChat(ConversationSnapshot conv) {
    client.markRead(conv.id);
    Navigator.of(context)
        .push(
          MaterialPageRoute(
            builder: (_) => ChatScreen(client: client, conversationId: conv.id, title: conv.title),
          ),
        )
        .then((_) => _loadDrafts());
  }

  void _openCreateGroup() {
    final candidates = <String>{
      ...client.onlineUsers.keys,
      ...client.messages.map((m) => m.senderUserId),
    }..remove(client.userId);
    candidates.remove('');
    if (candidates.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('还没有可邀请的成员（等待其他人上线）')),
      );
      return;
    }
    showModalBottomSheet<void>(
      context: context,
      isScrollControlled: true,
      backgroundColor: const Color(0xFF20232A),
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
      ),
      builder: (_) => _CreateGroupSheet(client: client, candidates: candidates.toList()..sort()),
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
      final f = m.file!;
      final lower = f.name.toLowerCase();
      if (f.mime.startsWith('image/')) return '[图片] ${f.name}';
      if (f.mime.startsWith('video/') || lower.endsWith('.mp4') || lower.endsWith('.mov') || lower.endsWith('.webm')) {
        return '[视频] ${f.name}';
      }
      if (f.mime.startsWith('audio/') || lower.endsWith('.m4a') || lower.endsWith('.aac') || lower.endsWith('.mp3') || lower.endsWith('.wav')) {
        return '[语音] ${f.name}';
      }
      return '[文件] ${f.name}';
    }
    if (m.body.isEmpty) return '';
    final one = m.body.replaceAll('\n', ' ');
    return one.length > 28 ? '${one.substring(0, 28)}…' : one;
  }

  @override
  Widget build(BuildContext context) {
    final onlineCount = client.onlineUsers.values.where((v) => v).length;
    final convs = client.sortedConversations.toList()
      ..sort((a, b) {
        final pa = _pinned.contains(a.id) ? 0 : 1;
        final pb = _pinned.contains(b.id) ? 0 : 1;
        return pa - pb; // 稳定排序：置顶优先，其余保持时间降序
      });
    final totalUnread = convs.fold<int>(0, (acc, c) => acc + client.unreadCount(c.id));

    return PopScope(
      canPop: false,
      onPopInvokedWithResult: (didPop, _) {
        if (didPop) return;
        final now = DateTime.now();
        if (_lastBackAt != null && now.difference(_lastBackAt!) < const Duration(seconds: 2)) {
          client.close();
          Navigator.of(context).pop();
          return;
        }
        _lastBackAt = now;
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(
            content: Text('再按一次退出 lanchat'),
            duration: Duration(seconds: 2),
            behavior: SnackBarBehavior.floating,
            backgroundColor: Color(0xFF2B2D33),
          ),
        );
      },
      child: Scaffold(
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
            tooltip: '搜索历史消息',
            icon: const Icon(Icons.search),
            onPressed: () {
              Navigator.of(context).push(
                MaterialPageRoute(builder: (_) => SearchScreen(client: client)),
              );
            },
          ),
          IconButton(
            tooltip: '设置',
            icon: const Icon(Icons.settings_outlined),
            onPressed: () {
              Navigator.of(context).push(
                MaterialPageRoute(builder: (_) => SettingsScreen(client: client)),
              );
            },
          ),
          IconButton(
            tooltip: '创建群聊',
            icon: const Icon(Icons.group_add),
            onPressed: _openCreateGroup,
          ),
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
            GestureDetector(
              onTap: _refresh,
              child: Container(
                width: double.infinity,
                color: const Color(0xFF3A2A2A),
                padding: const EdgeInsets.symmetric(vertical: 6, horizontal: 12),
                child: Row(
                  mainAxisAlignment: MainAxisAlignment.center,
                  children: [
                    const Icon(Icons.refresh, size: 13, color: Color(0xFFFFB4B4)),
                    const SizedBox(width: 6),
                    Text(
                      client.connectionStatus,
                      style: const TextStyle(fontSize: 12, color: Color(0xFFFFB4B4)),
                    ),
                    const SizedBox(width: 6),
                    const Text('点击重连',
                        style: TextStyle(fontSize: 12, color: Color(0xFFFFB4B4), fontWeight: FontWeight.w600)),
                  ],
                ),
              ),
            ),
          Expanded(
            child: _tab == 0 ? _messagesTab(convs) : ContactsScreen(client: client),
          ),
        ],
      ),
      bottomNavigationBar: BottomNavigationBar(
        currentIndex: _tab,
        backgroundColor: const Color(0xFF20232A),
        selectedItemColor: const Color(0xFF2B6BFF),
        unselectedItemColor: const Color(0xFF8B919C),
        type: BottomNavigationBarType.fixed,
        onTap: (i) => setState(() => _tab = i),
        items: [
          BottomNavigationBarItem(
            icon: Badge(
              isLabelVisible: totalUnread > 0,
              backgroundColor: const Color(0xFFE86452),
              label: Text(totalUnread > 99 ? '99+' : '$totalUnread',
                  style: const TextStyle(fontSize: 9, color: Colors.white)),
              child: const Icon(Icons.forum_outlined),
            ),
            activeIcon: Badge(
              isLabelVisible: totalUnread > 0,
              backgroundColor: const Color(0xFFE86452),
              label: Text(totalUnread > 99 ? '99+' : '$totalUnread',
                  style: const TextStyle(fontSize: 9, color: Colors.white)),
              child: const Icon(Icons.forum),
            ),
            label: '消息',
          ),
          const BottomNavigationBarItem(
            icon: Icon(Icons.people_outline),
            activeIcon: Icon(Icons.people),
            label: '联系人',
          ),
        ],
      ),
      ),
    );
  }

  /// 下拉刷新：断开重连（保留当前游标），重新拉取会话。
  Future<void> _refresh() async {
    try {
      await client.start(client.cursor);
    } catch (_) {}
    await Future<void>.delayed(const Duration(milliseconds: 400));
  }

  Widget _messagesTab(List<ConversationSnapshot> convs) {
    if (client.connected && convs.isEmpty) {
      return const Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            SizedBox(
              width: 26,
              height: 26,
              child: CircularProgressIndicator(strokeWidth: 2, color: Color(0xFF8B919C)),
            ),
            SizedBox(height: 12),
            Text('加载中…', style: TextStyle(color: Color(0xFF8B919C), fontSize: 13)),
          ],
        ),
      );
    }
    return RefreshIndicator(
      onRefresh: _refresh,
      color: const Color(0xFF2B6BFF),
      backgroundColor: const Color(0xFF2B2D33),
      child: ListView.separated(
            itemCount: convs.length,
            separatorBuilder: (_, __) => const Divider(
              height: 1,
              indent: 72,
              color: Color(0xFF26282E),
            ),
            itemBuilder: (context, i) {
              final conv = convs[i];
              return Dismissible(
                key: ValueKey('conv_${conv.id}'),
                direction: DismissDirection.horizontal,
                background: Container(
                  color: const Color(0xFF2B6BFF),
                  padding: const EdgeInsets.only(left: 20),
                  alignment: Alignment.centerLeft,
                  child: const Icon(Icons.push_pin, color: Colors.white),
                ),
                secondaryBackground: Container(
                  color: const Color(0xFFE86452),
                  padding: const EdgeInsets.only(right: 20),
                  alignment: Alignment.centerRight,
                  child: const Icon(Icons.notifications_off, color: Colors.white),
                ),
                confirmDismiss: (direction) async {
                  if (direction == DismissDirection.startToEnd) {
                    _togglePin(conv);
                  } else {
                    _toggleMute(conv);
                  }
                  return false; // 不真正移除，仅触发动作
                },
                child: _convTile(conv),
              );
            },
          ),
    );
  }

  Widget _convTile(ConversationSnapshot conv) {
    final last = client.lastMessageOf(conv.id);
    final unread = client.unreadCount(conv.id);
    final isLobby = conv.id == lobbyConversationId;
    final isPinned = _pinned.contains(conv.id);
    final isMuted = _muted.contains(conv.id);
    final title = isLobby ? '大厅' : (conv.title.isEmpty ? '群聊' : conv.title);
    final subtitle = isLobby ? '所有人' : '${conv.members.length} 人';
    final draft = _drafts[conv.id];

    return ListTile(
      onTap: () => _openChat(conv),
      onLongPress: () => _onConvLongPress(conv),
      leading: Container(
        width: 46,
        height: 46,
        decoration: BoxDecoration(
          color: _convColor(conv.id),
          borderRadius: BorderRadius.circular(12),
        ),
        alignment: Alignment.center,
        clipBehavior: Clip.antiAlias,
        child: isLobby
            ? const Icon(Icons.forum, color: Colors.white, size: 24)
            : _groupAvatar(conv.members),
      ),
      title: Row(
        children: [
          if (isPinned) ...[
            const Icon(Icons.push_pin, size: 13, color: Color(0xFF2B6BFF)),
            const SizedBox(width: 4),
          ],
          Flexible(
            child: Text(
              title,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(color: Color(0xFFE6E8EC), fontSize: 15, fontWeight: FontWeight.w600),
            ),
          ),
        ],
      ),
      subtitle: draft != null
          ? Text(
              '草稿: $draft',
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(color: Color(0xFFE0A23E), fontSize: 12),
            )
          : Text(
              '${_preview(last)} · $subtitle',
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(color: Color(0xFF8B919C), fontSize: 12),
      ),
      trailing: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        crossAxisAlignment: CrossAxisAlignment.end,
        children: [
          Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              if (isMuted) ...[
                const Icon(Icons.notifications_off, size: 13, color: Color(0xFF6B7078)),
                const SizedBox(width: 4),
              ],
              Text(
                _timeLabel(last?.createdAt ?? 0),
                style: const TextStyle(color: Color(0xFF6B7078), fontSize: 11),
              ),
            ],
          ),
          const SizedBox(height: 4),
          if (unread > 0)
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
              decoration: BoxDecoration(
                color: isMuted ? const Color(0xFF6B7078) : const Color(0xFFE86452),
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

  /// 群头像：前 4 名成员 2×2 色块拼图（成员名为种子）。
  Widget _groupAvatar(List<String> members) {
    if (members.isEmpty) {
      return const Icon(Icons.group, color: Colors.white, size: 24);
    }
    final shown = members.take(4).toList();
    final cells = shown.map((u) {
      return Container(
        color: _convColor(u),
        alignment: Alignment.center,
        child: Text(
          u.isEmpty ? '?' : u[0].toUpperCase(),
          style: const TextStyle(color: Colors.white, fontSize: 11, fontWeight: FontWeight.w600),
        ),
      );
    }).toList();
    if (shown.length == 1) return cells[0];
    if (shown.length == 2) {
      return Column(
        children: [
          Expanded(child: Row(children: [Expanded(child: cells[0]), Expanded(child: cells[1])])),
        ],
      );
    }
    if (shown.length == 3) {
      return Column(
        children: [
          Expanded(child: Row(children: [Expanded(child: cells[0]), Expanded(child: cells[1])])),
          Expanded(child: cells[2]),
        ],
      );
    }
    return Column(
      children: [
        Expanded(child: Row(children: [Expanded(child: cells[0]), Expanded(child: cells[1])])),
        Expanded(child: Row(children: [Expanded(child: cells[2]), Expanded(child: cells[3])])),
      ],
    );
  }
}

/// 建群底部弹层：群名 + 成员多选。
class _CreateGroupSheet extends StatefulWidget {
  final HubClient client;
  final List<String> candidates;

  const _CreateGroupSheet({required this.client, required this.candidates});

  @override
  State<_CreateGroupSheet> createState() => _CreateGroupSheetState();
}

class _CreateGroupSheetState extends State<_CreateGroupSheet> {
  final _titleCtrl = TextEditingController();
  final Set<String> _selected = {};

  @override
  void dispose() {
    _titleCtrl.dispose();
    super.dispose();
  }

  void _create() {
    final title = _titleCtrl.text.trim();
    if (title.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('请填写群名称')),
      );
      return;
    }
    widget.client.createConversation(title, _selected.toList());
    Navigator.of(context).pop();
  }

  @override
  Widget build(BuildContext context) {
    final keyboardInset = MediaQuery.of(context).viewInsets.bottom;
    return Padding(
      padding: EdgeInsets.only(bottom: keyboardInset),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          const Padding(
            padding: EdgeInsets.all(16),
            child: Text(
              '创建群聊',
              style: TextStyle(fontSize: 17, fontWeight: FontWeight.w700, color: Color(0xFFE6E8EC)),
            ),
          ),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: TextField(
              controller: _titleCtrl,
              autofocus: true,
              style: const TextStyle(color: Color(0xFFE6E8EC), fontSize: 15),
              decoration: InputDecoration(
                hintText: '群名称',
                hintStyle: const TextStyle(color: Color(0xFF6B7078)),
                filled: true,
                fillColor: const Color(0xFF2B2D33),
                contentPadding: const EdgeInsets.symmetric(horizontal: 14, vertical: 12),
                border: OutlineInputBorder(
                  borderRadius: BorderRadius.circular(10),
                  borderSide: BorderSide.none,
                ),
              ),
            ),
          ),
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 16, 16, 4),
            child: Text(
              '邀请成员（已选 ${_selected.length}）',
              style: const TextStyle(fontSize: 12, color: Color(0xFF8B919C)),
            ),
          ),
          Flexible(
            child: ListView(
              shrinkWrap: true,
              children: widget.candidates.map((u) {
                final online = widget.client.onlineUsers[u] ?? false;
                final checked = _selected.contains(u);
                return CheckboxListTile(
                  value: checked,
                  dense: true,
                  activeColor: const Color(0xFF2B6BFF),
                  onChanged: (v) => setState(() {
                    if (v == true) {
                      _selected.add(u);
                    } else {
                      _selected.remove(u);
                    }
                  }),
                  title: Text(
                    u,
                    style: const TextStyle(color: Color(0xFFE6E8EC), fontSize: 14),
                  ),
                  secondary: Container(
                    width: 34,
                    height: 34,
                    decoration: BoxDecoration(color: _seedColor(u), shape: BoxShape.circle),
                    alignment: Alignment.center,
                    child: Text(
                      u[0].toUpperCase(),
                      style: const TextStyle(color: Colors.white, fontSize: 14, fontWeight: FontWeight.w600),
                    ),
                  ),
                  subtitle: online
                      ? const Text('在线', style: TextStyle(fontSize: 11, color: Color(0xFF07C160)))
                      : null,
                );
              }).toList(),
            ),
          ),
          Padding(
            padding: const EdgeInsets.all(16),
            child: FilledButton(
              onPressed: _create,
              style: FilledButton.styleFrom(
                backgroundColor: const Color(0xFF2B6BFF),
                padding: const EdgeInsets.symmetric(vertical: 14),
              ),
              child: const Text('创建', style: TextStyle(fontSize: 15, fontWeight: FontWeight.w600)),
            ),
          ),
          const SizedBox(height: 8),
        ],
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
    return colors[u.codeUnitAt(0) % colors.length];
  }
}
