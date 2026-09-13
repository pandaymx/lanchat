import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:image_picker/image_picker.dart';
import 'package:record/record.dart';
import 'package:shared_preferences/shared_preferences.dart';

import '../api.dart';
import '../hub_client.dart';
import '../protocol.dart';
import '../widgets/message_bubble.dart';
import 'video_player_screen.dart';

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
  bool _emojiOpen = false;
  bool _showJumpDown = false;
  bool _multiSelect = false;
  final Set<String> _selectedIds = {};
  final Set<String> _playedVoices = {}; // 已播放的语音消息 id
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
    _restoreDraft();
    _loadPlayedVoices();
  }

  /// 恢复已播放语音标记（本地 prefs）。
  Future<void> _loadPlayedVoices() async {
    final prefs = await SharedPreferences.getInstance();
    final ids = prefs.getStringList('voice_played') ?? const [];
    if (!mounted) return;
    setState(() => _playedVoices.addAll(ids));
  }

  void _markVoicePlayed(String id) {
    if (_playedVoices.contains(id)) return;
    setState(() => _playedVoices.add(id));
    SharedPreferences.getInstance().then((prefs) {
      prefs.setStringList('voice_played', _playedVoices.toList());
    });
  }

  /// 恢复上次未发送的草稿（prefs per-conversation）。
  Future<void> _restoreDraft() async {
    final prefs = await SharedPreferences.getInstance();
    final draft = prefs.getString('draft_$convId') ?? '';
    if (draft.isNotEmpty && mounted) {
      _inputCtrl.text = draft;
      _inputCtrl.selection = TextSelection.collapsed(offset: draft.length);
    }
  }

  Future<void> _saveDraft() async {
    final prefs = await SharedPreferences.getInstance();
    final draft = _inputCtrl.text.trim();
    if (draft.isEmpty) {
      await prefs.remove('draft_$convId');
    } else {
      await prefs.setString('draft_$convId', draft);
    }
  }

  void _onScroll() {
    if (!_scrollCtrl.hasClients) return;
    final pos = _scrollCtrl.position;
    // 接近顶部 → 加载更早历史
    if (pos.pixels < 60 && !client.loadingEarlier) {
      client.loadEarlier(convId);
    }
    // 离开底部 → 显示「回到最新」按钮
    final away = pos.maxScrollExtent - pos.pixels > 300;
    if (away != _showJumpDown && mounted) {
      setState(() => _showJumpDown = away);
    }
  }

  @override
  void dispose() {
    _saveDraft();
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
    if (body.isEmpty) return;
    if (_replyTo != null) {
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
    _saveDraft();
    setState(() => _replyTo = null);
    _scrollToBottom();
  }

  /// 输入变化：防抖上发 typing（M7.3，hub 盖戳转发给同会话成员）；
  /// 输入以 @ 结尾时弹出群成员选择。
  void _onInputChanged(String text) {
    _typingTimer?.cancel();
    _typingTimer = Timer(const Duration(milliseconds: 500), () {
      if (_inputCtrl.text.trim().isNotEmpty) {
        client.sendTyping(convId);
      }
    });
    if (!_isLobby && text.endsWith('@')) {
      _openMentionPicker();
    }
  }

  /// @提及：弹层选择群成员，插入「@名字 」并继续输入。
  Future<void> _openMentionPicker() async {
    final conv = client.conversations[convId];
    final members = (conv?.members ?? const <String>[])
        .where((u) => u != client.userId)
        .toList();
    if (members.isEmpty) return;
    final picked = await showModalBottomSheet<String>(
      context: context,
      backgroundColor: const Color(0xFF20232A),
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
      ),
      builder: (ctx) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Padding(
              padding: EdgeInsets.all(16),
              child: Text('选择成员', style: TextStyle(fontSize: 16, fontWeight: FontWeight.w700, color: Color(0xFFE6E8EC))),
            ),
            ...members.map(
              (u) => ListTile(
                dense: true,
                leading: Icon(Icons.person, color: client.onlineUsers[u] == true ? const Color(0xFF07C160) : const Color(0xFF8B919C)),
                title: Text(u, style: const TextStyle(color: Color(0xFFE6E8EC), fontSize: 15)),
                onTap: () => Navigator.of(ctx).pop(u),
              ),
            ),
            const SizedBox(height: 8),
          ],
        ),
      ),
    );
    if (picked == null || !mounted) return;
    final ctrl = _inputCtrl;
    final sel = ctrl.selection;
    final text = ctrl.text;
    final atPos = text.lastIndexOf('@', sel.isValid ? sel.start : text.length);
    final base = atPos >= 0 ? text.substring(0, atPos) : text;
    final suffix = atPos >= 0 && sel.isValid ? text.substring(sel.start) : '';
    final next = '$base@$picked $suffix';
    ctrl.value = TextEditingValue(
      text: next,
      selection: TextSelection.collapsed(offset: next.length - suffix.length),
    );
    _inputFocus.requestFocus();
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
            ListTile(
              leading: const Icon(Icons.forward, color: Color(0xFFE6E8EC)),
              title: const Text('转发', style: TextStyle(color: Color(0xFFE6E8EC))),
              onTap: () {
                Navigator.of(sheetCtx).pop();
                _onForward(m);
              },
            ),
            ListTile(
              leading: const Icon(Icons.checklist, color: Color(0xFFE6E8EC)),
              title: const Text('多选', style: TextStyle(color: Color(0xFFE6E8EC))),
              onTap: () {
                Navigator.of(sheetCtx).pop();
                setState(() {
                  _multiSelect = true;
                  _selectedIds.add(m.id);
                });
              },
            ),
          ],
        ),
      ),
    );
  }

  /// 多选批量转发：选会话 → 逐条重发（文本原样；文件复用 fileId）。
  Future<void> _forwardSelected() async {
    final selected = client.messagesOf(convId)
        .where((m) => _selectedIds.contains(m.id))
        .toList();
    if (selected.isEmpty) {
      _toast('未选择消息');
      return;
    }
    final convs = client.sortedConversations
        .where((c) => c.id != convId)
        .toList();
    if (convs.isEmpty) {
      _toast('没有可转发到的会话');
      return;
    }
    final picked = await showModalBottomSheet<ConversationSnapshot>(
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
              child: Text('转发 ${selected.length} 条到…',
                  style: const TextStyle(fontSize: 16, fontWeight: FontWeight.w700, color: Color(0xFFE6E8EC))),
            ),
            ...convs.map((c) {
              final isLobby = c.id == lobbyConversationId;
              return ListTile(
                leading: Icon(isLobby ? Icons.forum : Icons.group, color: const Color(0xFF2B6BFF)),
                title: Text(
                  isLobby ? '大厅' : (c.title.isEmpty ? '群聊' : c.title),
                  style: const TextStyle(color: Color(0xFFE6E8EC), fontSize: 15),
                ),
                onTap: () => Navigator.of(ctx).pop(c),
              );
            }),
            const SizedBox(height: 8),
          ],
        ),
      ),
    );
    if (picked == null || !mounted) return;
    for (final m in selected) {
      if (m.file != null) {
        client.sendFileMessage(picked.id, m.file!, body: m.body);
      } else {
        client.sendMessage(picked.id, m.body);
      }
    }
    setState(() {
      _multiSelect = false;
      _selectedIds.clear();
    });
    _toast('已转发 ${selected.length} 条');
  }

  /// 转发：选会话 → 重发原消息（文本原样；文件复用 fileId，hub 已有存储）。
  Future<void> _onForward(StoredMessage m) async {
    final convs = client.sortedConversations
        .where((c) => c.id != convId)
        .toList();
    if (convs.isEmpty) {
      _toast('没有可转发到的会话');
      return;
    }
    final picked = await showModalBottomSheet<ConversationSnapshot>(
      context: context,
      backgroundColor: const Color(0xFF20232A),
      shape: const RoundedRectangleBorder(
        borderRadius: BorderRadius.vertical(top: Radius.circular(16)),
      ),
      builder: (ctx) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Padding(
              padding: EdgeInsets.all(16),
              child: Text('转发到…', style: TextStyle(fontSize: 16, fontWeight: FontWeight.w700, color: Color(0xFFE6E8EC))),
            ),
            ...convs.map((c) {
              final isLobby = c.id == lobbyConversationId;
              return ListTile(
                leading: Icon(isLobby ? Icons.forum : Icons.group, color: const Color(0xFF2B6BFF)),
                title: Text(
                  isLobby ? '大厅' : (c.title.isEmpty ? '群聊' : c.title),
                  style: const TextStyle(color: Color(0xFFE6E8EC), fontSize: 15),
                ),
                onTap: () => Navigator.of(ctx).pop(c),
              );
            }),
            const SizedBox(height: 8),
          ],
        ),
      ),
    );
    if (picked == null || !mounted) return;
    if (m.file != null) {
      client.sendFileMessage(picked.id, m.file!, body: m.body);
    } else {
      client.sendMessage(picked.id, m.body);
    }
    _toast('已转发');
  }

  Future<void> _pickAndSendImage() async {
    final picker = ImagePicker();
    final List<XFile> picked;
    try {
      picked = await picker.pickMultiImage(imageQuality: 85);
    } catch (e) {
      _toast('无法打开图库: $e');
      return;
    }
    if (picked.isEmpty) return;
    setState(() => _uploading = true);
    try {
      final api = HubApi(host: client.host, port: client.port);
      var sent = 0;
      for (final x in picked) {
        final file = File(x.path);
        final mime = _mimeOf(file);
        final ref = await api.uploadFile(file, mime: mime);
        if (!mounted) return;
        client.sendFileMessage(convId, ref);
        sent++;
      }
      if (sent > 0) _scrollToBottom();
    } catch (e) {
      _toast('上传失败: $e');
    } finally {
      if (mounted) setState(() => _uploading = false);
    }
  }

  String _mimeOf(File f) {
    final name = f.path.toLowerCase();
    if (name.endsWith('.png')) return 'image/png';
    if (name.endsWith('.gif')) return 'image/gif';
    if (name.endsWith('.webp')) return 'image/webp';
    if (name.endsWith('.heic')) return 'image/heic';
    return 'image/jpeg';
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
    // 合并系统消息（群成员变动）与普通消息，按时间升序。
    final all = <Object>[
      ...messages,
      ...?client.convEvents[convId],
    ]..sort((a, b) {
        final ta = a is StoredMessage ? a.createdAt : (a as ConvEventMsg).createdAt;
        final tb = b is StoredMessage ? b.createdAt : (b as ConvEventMsg).createdAt;
        return ta - tb;
      });
    final items = <Object>[];
    DateTime? prevDay;
    for (final m in all) {
      final t = DateTime.fromMillisecondsSinceEpoch(
          m is StoredMessage ? m.createdAt : (m as ConvEventMsg).createdAt);
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

  void _openFullVideo(StoredMessage m) {
    final url = 'http://${client.host}:${client.port}/api/files/${m.file!.fileId}';
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => VideoPlayerScreen(url: url, name: m.file!.name),
      ),
    );
  }

  /// 「+」更多菜单：相册 / 拍摄 / 视频。
  void _showMoreMenu() {
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
            const Padding(
              padding: EdgeInsets.all(16),
              child: Text('发送', style: TextStyle(fontSize: 16, fontWeight: FontWeight.w700, color: Color(0xFFE6E8EC))),
            ),
            Row(
              mainAxisAlignment: MainAxisAlignment.spaceEvenly,
              children: [
                _moreItem(ctx, Icons.photo_library_outlined, '相册', () {
                  Navigator.of(ctx).pop();
                  _pickAndSendImage();
                }),
                _moreItem(ctx, Icons.photo_camera_outlined, '拍摄', () {
                  Navigator.of(ctx).pop();
                  _pickAndSendCamera();
                }),
                _moreItem(ctx, Icons.videocam_outlined, '视频', () {
                  Navigator.of(ctx).pop();
                  _pickAndSendVideo();
                }),
              ],
            ),
            const SizedBox(height: 16),
          ],
        ),
      ),
    );
  }

  Widget _moreItem(BuildContext ctx, IconData icon, String label, VoidCallback onTap) {
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(12),
      child: Padding(
        padding: const EdgeInsets.all(8),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Container(
              width: 52,
              height: 52,
              decoration: BoxDecoration(
                color: const Color(0xFF2B2D33),
                borderRadius: BorderRadius.circular(14),
              ),
              child: Icon(icon, color: const Color(0xFFE6E8EC), size: 26),
            ),
            const SizedBox(height: 6),
            Text(label, style: const TextStyle(fontSize: 12, color: Color(0xFF8B919C))),
          ],
        ),
      ),
    );
  }

  Future<void> _pickAndSendCamera() async {
    final picker = ImagePicker();
    final XFile? picked;
    try {
      picked = await picker.pickImage(source: ImageSource.camera, imageQuality: 85);
    } catch (e) {
      _toast('无法打开相机: $e');
      return;
    }
    if (picked == null) return;
    await _uploadAndSendFile(File(picked.path), 'image/jpeg');
  }

  Future<void> _pickAndSendVideo() async {
    final picker = ImagePicker();
    final XFile? picked;
    try {
      picked = await picker.pickVideo(source: ImageSource.gallery);
    } catch (e) {
      _toast('无法打开视频: $e');
      return;
    }
    if (picked == null) return;
    await _uploadAndSendFile(File(picked.path), 'video/mp4');
  }

  Future<void> _uploadAndSendFile(File file, String mime) async {
    setState(() => _uploading = true);
    try {
      final api = HubApi(host: client.host, port: client.port);
      final ref = await api.uploadFile(file, mime: mime);
      if (!mounted) return;
      client.sendFileMessage(convId, ref);
      _scrollToBottom();
    } catch (e) {
      _toast('上传失败: $e');
    } finally {
      if (mounted) setState(() => _uploading = false);
    }
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
        title: _multiSelect
            ? Text('已选 ${_selectedIds.length} 条',
                style: const TextStyle(fontSize: 17, fontWeight: FontWeight.w600))
            : Column(
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
        actions: _multiSelect
            ? [
                IconButton(
                  tooltip: '取消多选',
                  icon: const Icon(Icons.close),
                  onPressed: () => setState(() {
                    _multiSelect = false;
                    _selectedIds.clear();
                  }),
                ),
              ]
            : [
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
                : Stack(
                    children: [
                      ListView.builder(
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
                          if (item is ConvEventMsg) return _systemMsg(item);
                          final m = item as StoredMessage;
                          final selected = _selectedIds.contains(m.id);
                          return Stack(
                            children: [
                              MessageBubble(
                                message: m,
                                client: client,
                                selfUserId: client.userId,
                                playedVoiceIds: _playedVoices,
                                onVoicePlayed: _markVoicePlayed,
                                onTap: _multiSelect
                                    ? () => _toggleSelect(m)
                                    : _isVideo(m)
                                        ? () => _openFullVideo(m)
                                        : (m.file != null && (m.file!.mime.startsWith('image/')))
                                            ? () => _openFullImage(m)
                                            : null,
                                onLongPress: () =>
                                    _multiSelect ? _toggleSelect(m) : _onMessageLongPress(m),
                              ),
                              if (_multiSelect)
                                Positioned(
                                  top: 2,
                                  left: m.senderUserId == client.userId ? 2 : null,
                                  right: m.senderUserId == client.userId ? null : 2,
                                  child: Container(
                                    width: 20,
                                    height: 20,
                                    decoration: BoxDecoration(
                                      shape: BoxShape.circle,
                                      color: selected ? const Color(0xFF2B6BFF) : const Color(0x66000000),
                                      border: selected
                                          ? null
                                          : Border.all(color: Colors.white70, width: 1.5),
                                    ),
                                    alignment: Alignment.center,
                                    child: selected
                                        ? const Icon(Icons.check, size: 14, color: Colors.white)
                                        : null,
                                  ),
                                ),
                            ],
                          );
                        },
                      ),
                      if (_showJumpDown)
                        Positioned(
                          right: 14,
                          bottom: 14,
                          child: Material(
                            color: const Color(0xFF2B6BFF),
                            shape: const CircleBorder(),
                            elevation: 3,
                            child: InkWell(
                              customBorder: const CircleBorder(),
                              onTap: _scrollToBottom,
                              child: const Padding(
                                padding: EdgeInsets.all(8),
                                child: Icon(Icons.arrow_downward, color: Colors.white, size: 20),
                              ),
                            ),
                          ),
                        ),
                    ],
                  ),
          ),
          if (_replyTo != null) _replyBar(),
          if (_recording) _recordingBar(),
          if (_multiSelect)
            Container(
              color: const Color(0xFF20232A),
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
              child: Row(
                mainAxisAlignment: MainAxisAlignment.spaceBetween,
                children: [
                  TextButton(
                    onPressed: () => setState(() {
                      _multiSelect = false;
                      _selectedIds.clear();
                    }),
                    child: const Text('取消', style: TextStyle(color: Color(0xFF8B919C))),
                  ),
                  FilledButton.icon(
                    onPressed: _selectedIds.isEmpty ? null : _forwardSelected,
                    icon: const Icon(Icons.forward, size: 16),
                    label: Text('转发 (${_selectedIds.length})'),
                    style: FilledButton.styleFrom(
                      backgroundColor: const Color(0xFF2B6BFF),
                      padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 10),
                    ),
                  ),
                ],
              ),
            )
          else
            _composer(),
        ],
      ),
    );
  }

  void _toggleSelect(StoredMessage m) {
    setState(() {
      if (!_selectedIds.remove(m.id)) _selectedIds.add(m.id);
    });
  }

  bool _isVideo(StoredMessage m) {
    final f = m.file;
    if (f == null) return false;
    if (f.mime.startsWith('video/')) return true;
    final lower = f.name.toLowerCase();
    return lower.endsWith('.mp4') ||
        lower.endsWith('.mov') ||
        lower.endsWith('.webm') ||
        lower.endsWith('.mkv');
  }

  Widget _systemMsg(ConvEventMsg e) {
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 8, horizontal: 40),
      child: Center(
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 4),
          decoration: BoxDecoration(
            color: const Color(0x143B3F47),
            borderRadius: BorderRadius.circular(8),
          ),
          child: Text(
            e.text,
            style: const TextStyle(fontSize: 11, color: Color(0xFF8B919C)),
          ),
        ),
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
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (_emojiOpen) _emojiPanel(),
          Row(
        children: [
          IconButton(
            onPressed: _uploading ? null : _showMoreMenu,
            icon: _uploading
                ? const SizedBox(
                    width: 20,
                    height: 20,
                    child: CircularProgressIndicator(strokeWidth: 2, color: Color(0xFF8B919C)),
                  )
                : const Icon(Icons.add_circle_outline, color: Color(0xFF8B919C)),
            tooltip: '发送图片/视频',
          ),
          IconButton(
            onPressed: _toggleRecord,
            icon: _recording
                ? const Icon(Icons.mic, color: Color(0xFFE86452))
                : const Icon(Icons.mic_none, color: Color(0xFF8B919C)),
            tooltip: _recording ? '停止录音并发送' : '录音',
          ),
          IconButton(
            onPressed: () => setState(() => _emojiOpen = !_emojiOpen),
            icon: Icon(
              _emojiOpen ? Icons.keyboard : Icons.emoji_emotions_outlined,
              color: _emojiOpen ? const Color(0xFF2B6BFF) : const Color(0xFF8B919C),
            ),
            tooltip: '表情',
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
        ],
      ),
    );
  }

  static const List<String> _emojiList = [
    '😀', '😄', '😁', '😂', '🤣', '😊', '😍', '😘', '😎', '🤔',
    '😅', '😭', '😡', '🥳', '😇', '🤗', '🫡', '🫶', '🤝', '👍',
    '👎', '👏', '🙏', '💪', '✌️', '🤞', '🎉', '🎂', '🎁', '❤️',
    '💔', '💯', '🔥', '✨', '🌟', '🚀', '🌙', '☀️', '⭐', '🌈',
    '🍎', '🍺', '☕', '🍜', '🍰', '⚽', '🏀', '🎮', '🎧', '🎵',
    '📱', '💻', '🕐', '❓', '❗', '✅', '❌', '⚠️', '🔒', '📌',
  ];

  Widget _emojiPanel() {
    return Container(
      height: 190,
      padding: const EdgeInsets.fromLTRB(10, 8, 10, 4),
      decoration: const BoxDecoration(
        color: Color(0xFF2B2D33),
        borderRadius: BorderRadius.vertical(top: Radius.circular(12)),
      ),
      child: GridView.count(
        crossAxisCount: 8,
        children: _emojiList.map((e) {
          return InkWell(
            onTap: () => _insertEmoji(e),
            borderRadius: BorderRadius.circular(8),
            child: Center(
              child: Text(e, style: const TextStyle(fontSize: 22)),
            ),
          );
        }).toList(),
      ),
    );
  }

  void _insertEmoji(String emoji) {
    final ctrl = _inputCtrl;
    final sel = ctrl.selection;
    final text = ctrl.text;
    final start = sel.isValid ? sel.start : text.length;
    final next = text.substring(0, start) + emoji + text.substring(sel.isValid ? sel.end : start);
    ctrl.value = TextEditingValue(
      text: next,
      selection: TextSelection.collapsed(offset: start + emoji.length),
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
