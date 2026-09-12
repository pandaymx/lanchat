import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:image_picker/image_picker.dart';
import 'package:record/record.dart';

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
  final _inputFocus = FocusNode();
  final _scrollCtrl = ScrollController();
  bool _uploading = false;
  StoredMessage? _replyTo;

  final AudioRecorder _recorder = AudioRecorder();
  bool _recording = false;
  Timer? _recordTimer;
  int _recordSeconds = 0;
  Timer? _typingTimer;

  HubClient get client => widget.client;
  String get convId => widget.conversationId;
  bool get _isLobby => convId == lobbyConversationId;

  @override
  void initState() {
    super.initState();
    client.addListener(_onClientChanged);
    _scrollCtrl.addListener(_onScroll);
    // 进入会话即已读（列表页已 markRead，这里兜底新消息）。
    WidgetsBinding.instance.addPostFrameCallback((_) => client.markRead(convId));
  }

  void _onScroll() {
    if (!_scrollCtrl.hasClients) return;
    final pos = _scrollCtrl.position;
    // 接近顶部 → 加载更早历史
    if (pos.pixels < 60 && !client.loadingEarlier) {
      client.loadEarlier(convId);
    }
  }

  @override
  void dispose() {
    client.removeListener(_onClientChanged);
    _inputCtrl.dispose();
    _inputFocus.dispose();
    _scrollCtrl.dispose();
    _recordTimer?.cancel();
    _typingTimer?.cancel();
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
    if (body.isEmpty) return;    if (_replyTo != null) {
      client.sendMessage(convId, body,
          replyTo: ReplyRef(
            id: _replyTo!.id,
            senderUserId: _replyTo!.senderUserId,
            body: _replyTo!.body,
          ));
    } else {
      client.sendMessage(convId, body);
    }
    _inputCtrl.clear();
    setState(() => _replyTo = null);
    _scrollToBottom();
  }

  /// 输入变化：防抖上发 typing（M7.3，hub 盖戳转发给同会话成员）。
  void _onInputChanged(String _) {
    _typingTimer?.cancel();
    _typingTimer = Timer(const Duration(milliseconds: 500), () {
      if (_inputCtrl.text.trim().isNotEmpty) {
        client.sendTyping(convId);
      }
    });
  }

  void _onMessageLongPress(StoredMessage m) {
    showModalBottomSheet<void>(
      context: context,
      backgroundColor: const Color(0xFF20232A),
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
      ),
      builder: (sheetCtx) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              leading: const Icon(Icons.reply, color: Color(0xFFE6E8EC)),
              title: const Text('回复', style: TextStyle(color: Color(0xFFE6E8EC))),
              onTap: () {
                Navigator.of(sheetCtx).pop();
                setState(() {
                  _replyTo = m;
                  _inputFocus.requestFocus();
                });
              },
            ),
            ListTile(
              leading: const Icon(Icons.copy, color: Color(0xFFE6E8EC)),
              title: const Text('复制', style: TextStyle(color: Color(0xFFE6E8EC))),
              onTap: () {
                Navigator.of(sheetCtx).pop();
                if (m.body.isNotEmpty) {
                  final data = ClipboardData(text: m.body);
                  Clipboard.setData(data);
                  _toast('已复制');
                }
              },
            ),
          ],
        ),
      ),
    );
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

  /// 点击麦克风：开始/停止录音。停止后自动上传并发送语音消息。
  Future<void> _toggleRecord() async {
    if (_recording) {
      final path = await _recorder.stop();
      _recordTimer?.cancel();
      setState(() => _recording = false);
      if (path != null) {
        if (_recordSeconds >= 1) {
          await _sendAudio(File(path));
        } else {
          try {
            File(path).deleteSync();
          } catch (_) {}
          _toast('录音太短，未发送');
        }
      }
    } else {
      final ok = await _recorder.hasPermission();
      if (!ok) {
        _toast('需要麦克风权限才能录音');
        return;
      }
      final path = '${Directory.systemTemp.path}/lm_voice_${DateTime.now().millisecondsSinceEpoch}.m4a';
      try {
        await _recorder.start(
          const RecordConfig(encoder: AudioEncoder.aacLc),
          path: path,
        );
      } catch (e) {
        _toast('录音启动失败: $e');
        return;
      }
      setState(() {
        _recording = true;
        _recordSeconds = 0;
      });
      _recordTimer = Timer.periodic(const Duration(seconds: 1), (_) {
        setState(() => _recordSeconds++);
        if (_recordSeconds >= 120) _toggleRecord(); // 最长 2 分钟自动停
      });
    }
  }

  Future<void> _sendAudio(File file) async {
    setState(() => _uploading = true);
    try {
      final api = HubApi(host: client.host, port: client.port);
      final ref = await api.uploadFile(file, mime: 'audio/m4a');
      if (!mounted) return;
      client.sendFileMessage(convId, ref);
      _scrollToBottom();
    } catch (e) {
      _toast('语音上传失败: $e');
    } finally {
      if (mounted) setState(() => _uploading = false);
    }
  }

  void _openGroupInfo() {
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => _GroupInfoScreen(client: client, conversationId: convId),
      ),
    );
  }

  /// 按天分组：相邻同一天的消息之间不插条，跨天插日期分隔条。
  List<Object> _buildItems(List<StoredMessage> messages) {
    final items = <Object>[];
    DateTime? prevDay;
    for (final m in messages) {
      final t = DateTime.fromMillisecondsSinceEpoch(m.createdAt);
      final day = DateTime(t.year, t.month, t.day);
      if (prevDay == null || day != prevDay) {
        items.add(day);
        prevDay = day;
      }
      items.add(m);
    }
    return items;
  }

  void _openFullImage(StoredMessage m) {
    final url = 'http://${client.host}:${client.port}/api/files/${m.file!.fileId}';
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => _FullImageViewer(url: url, name: m.file!.name),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final messages = client.messagesOf(convId);
    final items = _buildItems(messages);
    final onlineCount = client.onlineUsers.values.where((v) => v).length;
    final title = widget.title.isEmpty && _isLobby ? '大厅' : widget.title;
    final typer = client.typingUsers[convId];

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
              typer != null
                  ? '$typer 正在输入…'
                  : (client.connected ? '在线 $onlineCount 人' : client.connectionStatus),
              style: TextStyle(
                fontSize: 11,
                color: typer != null
                    ? const Color(0xFF2B6BFF)
                    : (client.connected ? const Color(0xFF07C160) : const Color(0xFFE86452)),
              ),
            ),
          ],
        ),
        actions: [
          if (!_isLobby)
            PopupMenuButton<String>(
              color: const Color(0xFF2B2D33),
              icon: const Icon(Icons.more_vert, size: 20),
              onSelected: (v) {
                if (v == 'info') _openGroupInfo();
              },
              itemBuilder: (_) => const [
                PopupMenuItem(value: 'info', child: Text('群信息', style: TextStyle(color: Color(0xFFE6E8EC)))),
              ],
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
            child: messages.isEmpty && !client.connected
                ? const Center(
                    child: Text('连接中…', style: TextStyle(color: Color(0xFF8B919C))),
                  )
                : ListView.builder(
                    controller: _scrollCtrl,
                    padding: const EdgeInsets.symmetric(vertical: 8),
                    itemCount: items.length + (client.loadingEarlier ? 1 : 0),
                    itemBuilder: (context, i) {
                      if (client.loadingEarlier && i == 0) {
                        return const Padding(
                          padding: EdgeInsets.symmetric(vertical: 10),
                          child: Center(
                            child: SizedBox(
                              width: 18,
                              height: 18,
                              child: CircularProgressIndicator(strokeWidth: 2, color: Color(0xFF8B919C)),
                            ),
                          ),
                        );
                      }
                      final item = items[i - (client.loadingEarlier ? 1 : 0)];
                      if (item is DateTime) return _dateDivider(item);
                      final m = item as StoredMessage;
                      return MessageBubble(
                        message: m,
                        client: client,
                        selfUserId: client.userId,
                        onTap: m.file != null && (m.file!.mime.startsWith('image/')) ? () => _openFullImage(m) : null,
                        onLongPress: () => _onMessageLongPress(m),
                      );
                    },
                  ),
          ),
          if (_replyTo != null) _replyBar(),
          if (_recording) _recordingBar(),
          _composer(),
        ],
      ),
    );
  }

  Widget _dateDivider(DateTime day) {
    final now = DateTime.now();
    final today = DateTime(now.year, now.month, now.day);
    final yesterday = today.subtract(const Duration(days: 1));
    String label;
    if (day == today) {
      label = '今天';
    } else if (day == yesterday) {
      label = '昨天';
    } else {
      label = '${day.month}月${day.day}日';
    }
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 8),
      child: Center(
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 3),
          decoration: BoxDecoration(
            color: const Color(0xFF2B2D33),
            borderRadius: BorderRadius.circular(999),
          ),
          child: Text(
            label,
            style: const TextStyle(fontSize: 11, color: Color(0xFF8B919C)),
          ),
        ),
      ),
    );
  }

  Widget _recordingBar() {
    return Container(
      width: double.infinity,
      color: const Color(0xFF3A2A2A),
      padding: const EdgeInsets.symmetric(vertical: 8, horizontal: 12),
      child: Row(
        children: [
          Container(
            width: 10,
            height: 10,
            decoration: const BoxDecoration(color: Color(0xFFE86452), shape: BoxShape.circle),
          ),
          const SizedBox(width: 8),
          Text(
            '录音中 $_recordSeconds 秒 · 点击下方麦克风停止',
            style: const TextStyle(fontSize: 13, color: Color(0xFFFFB4B4)),
          ),
        ],
      ),
    );
  }

  Widget _replyBar() {
    final m = _replyTo!;
    return Container(
      width: double.infinity,
      color: const Color(0xFF2B2D33),
      padding: const EdgeInsets.fromLTRB(12, 8, 4, 8),
      child: Row(
        children: [
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(
                  '回复 ${m.senderUserId}',
                  style: const TextStyle(fontSize: 12, fontWeight: FontWeight.w600, color: Color(0xFF2B6BFF)),
                ),
                Text(
                  m.body.isEmpty ? (m.file?.name ?? '') : m.body,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(fontSize: 12, color: Color(0xFF8B919C)),
                ),
              ],
            ),
          ),
          IconButton(
            onPressed: () => setState(() => _replyTo = null),
            icon: const Icon(Icons.close, size: 18, color: Color(0xFF8B919C)),
          ),
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
          IconButton(
            onPressed: _toggleRecord,
            icon: _recording
                ? const Icon(Icons.mic, color: Color(0xFFE86452))
                : const Icon(Icons.mic_none, color: Color(0xFF8B919C)),
            tooltip: _recording ? '停止录音并发送' : '录音',
          ),
          Expanded(
            child: TextField(
              controller: _inputCtrl,
              focusNode: _inputFocus,
              onChanged: _onInputChanged,
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

/// 图片全屏查看页（黑底 + 双指缩放 + 点击关闭）。
class _FullImageViewer extends StatelessWidget {
  final String url;
  final String name;

  const _FullImageViewer({required this.url, required this.name});

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: Colors.black,
      appBar: AppBar(
        backgroundColor: Colors.black,
        foregroundColor: Colors.white,
        title: Text(name, style: const TextStyle(fontSize: 14)),
      ),
      body: GestureDetector(
        onTap: () => Navigator.of(context).pop(),
        child: Center(
          child: InteractiveViewer(
            maxScale: 5,
            child: Image.network(
              url,
              fit: BoxFit.contain,
              loadingBuilder: (context, child, progress) {
                if (progress == null) return child;
                return const CircularProgressIndicator(color: Colors.white54);
              },
              errorBuilder: (context, error, stack) => const Text(
                '图片加载失败',
                style: TextStyle(color: Colors.white54),
              ),
            ),
          ),
        ),
      ),
    );
  }
}


/// 群信息页：成员列表 + 退出群聊（FKConvLeave）。
class _GroupInfoScreen extends StatefulWidget {
  final HubClient client;
  final String conversationId;

  const _GroupInfoScreen({required this.client, required this.conversationId});

  @override
  State<_GroupInfoScreen> createState() => _GroupInfoScreenState();
}

class _GroupInfoScreenState extends State<_GroupInfoScreen> {
  HubClient get client => widget.client;
  String get convId => widget.conversationId;

  @override
  void initState() {
    super.initState();
    client.addListener(_onChanged);
  }

  @override
  void dispose() {
    client.removeListener(_onChanged);
    super.dispose();
  }

  void _onChanged() {
    if (mounted) setState(() {});
  }

  Future<void> _invite() async {
    final conv = client.conversations[convId];
    final members = conv?.members ?? const <String>[];
    final known = <String>{
      ...client.onlineUsers.keys,
      ...client.messages.map((m) => m.senderUserId),
    }..remove(client.userId);
    known.removeAll(members);
    known.remove('');
    if (known.isEmpty) {
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('没有可邀请的新成员')),
      );
      return;
    }
    final picked = await showModalBottomSheet<List<String>>(
      context: context,
      backgroundColor: const Color(0xFF20232A),
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
      ),
      builder: (_) => _InviteSheet(client: client, candidates: known.toList()..sort()),
    );
    if (picked != null && picked.isNotEmpty && mounted) {
      client.inviteMembers(convId, picked);
    }
  }

  Future<void> _leave() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        backgroundColor: const Color(0xFF20232A),
        title: const Text('退出群聊', style: TextStyle(color: Color(0xFFE6E8EC))),
        content: const Text('退出后将不再收到该群消息，确定退出？', style: TextStyle(color: Color(0xFF8B919C))),
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
    if (confirmed != true || !mounted) return;
    client.leaveConversation(convId);
    Navigator.of(context).popUntil((r) => r.isFirst);
  }

  @override
  Widget build(BuildContext context) {
    final conv = client.conversations[convId];
    final title = conv?.title ?? '群聊';
    final members = conv?.members ?? const <String>[];

    return Scaffold(
      backgroundColor: const Color(0xFF17181C),
      appBar: AppBar(
        backgroundColor: const Color(0xFF20232A),
        foregroundColor: const Color(0xFFE6E8EC),
        title: Text(title, style: const TextStyle(fontSize: 17, fontWeight: FontWeight.w600)),
      ),
      body: ListView(
        padding: const EdgeInsets.symmetric(vertical: 8),
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(16, 12, 16, 4),
            child: Text(
              '成员 ${members.length} 人',
              style: const TextStyle(fontSize: 12, fontWeight: FontWeight.w600, color: Color(0xFF8B919C)),
            ),
          ),
          ...members.map(
            (u) => ListTile(
              leading: Container(
                width: 38,
                height: 38,
                decoration: BoxDecoration(color: _seedColor(u), shape: BoxShape.circle),
                alignment: Alignment.center,
                child: Text(
                  u.isEmpty ? '?' : u[0].toUpperCase(),
                  style: const TextStyle(color: Colors.white, fontSize: 14, fontWeight: FontWeight.w600),
                ),
              ),
              title: Text(u, style: const TextStyle(color: Color(0xFFE6E8EC), fontSize: 14)),
              trailing: (client.onlineUsers[u] ?? false)
                  ? const Text('在线', style: TextStyle(fontSize: 12, color: Color(0xFF07C160)))
                  : null,
            ),
          ),
          const SizedBox(height: 24),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: OutlinedButton.icon(
              onPressed: _invite,
              icon: const Icon(Icons.person_add_alt, size: 18),
              label: const Text('邀请成员', style: TextStyle(fontSize: 15)),
              style: OutlinedButton.styleFrom(
                foregroundColor: const Color(0xFF2B6BFF),
                side: const BorderSide(color: Color(0xFF2B6BFF)),
                padding: const EdgeInsets.symmetric(vertical: 12),
              ),
            ),
          ),
          const SizedBox(height: 12),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: OutlinedButton(
              onPressed: _leave,
              style: OutlinedButton.styleFrom(
                foregroundColor: const Color(0xFFE86452),
                side: const BorderSide(color: Color(0xFFE86452)),
                padding: const EdgeInsets.symmetric(vertical: 12),
              ),
              child: const Text('退出群聊', style: TextStyle(fontSize: 15)),
            ),
          ),
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
    if (u.isEmpty) return colors[0];
    return colors[u.codeUnitAt(0) % colors.length];
  }
}


/// 邀请成员选择弹层（复用建群成员多选交互）。
class _InviteSheet extends StatefulWidget {
  final HubClient client;
  final List<String> candidates;

  const _InviteSheet({required this.client, required this.candidates});

  @override
  State<_InviteSheet> createState() => _InviteSheetState();
}

class _InviteSheetState extends State<_InviteSheet> {
  final Set<String> _selected = {};

  @override
  Widget build(BuildContext context) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Padding(
          padding: const EdgeInsets.all(16),
          child: Text(
            '邀请成员（已选 ${_selected.length}）',
            style: const TextStyle(fontSize: 17, fontWeight: FontWeight.w700, color: Color(0xFFE6E8EC)),
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
                title: Text(u, style: const TextStyle(color: Color(0xFFE6E8EC), fontSize: 14)),
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
            onPressed: () => Navigator.of(context).pop(_selected.toList()),
            style: FilledButton.styleFrom(
              backgroundColor: const Color(0xFF2B6BFF),
              padding: const EdgeInsets.symmetric(vertical: 14),
            ),
            child: const Text('邀请', style: TextStyle(fontSize: 15, fontWeight: FontWeight.w600)),
          ),
        ),
      ],
    );
  }
}
