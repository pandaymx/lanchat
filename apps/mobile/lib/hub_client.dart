/// hub WS 客户端：连接、心跳、重连、事件分发、多会话与未读。
///
/// 与 Go pkg/client 对齐的 MVP 子集：
/// - 连接后发 Hello（含 ResumeFrom）；
/// - 收 ConvList / HistoryResp / Deliver / Presence / Error / Pong；
/// - 30s 心跳 Ping；断线指数退避重连（0-15s）；
/// - 本地游标（max serverSeq）持久化，重连时续传；
/// - 多会话：messages 全局收流，按 conv 过滤渲染；每会话已读游标。
library;

import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

import 'protocol.dart';
import 'secure_client.dart';

class HubClient extends ChangeNotifier {
  final String host;
  final int port;
  final String userId;
  final String deviceId;

  WebSocketChannel? _channel;
  StreamSubscription? _sub;
  final List<Frame> _pending = [];
  Timer? _pingTimer;
  Timer? _reconnectTimer;
  bool _closed = false;
  int _attempts = 0;

  /// 已建立的会话加密（wire v2）。null = 握手未完成（明文期）。
  WsSecureSession? _session;
  Uint8List? _ephPriv;
  Uint8List? _ephPub;

  final List<StoredMessage> messages = [];
  final Map<String, bool> onlineUsers = {}; // userID -> online
  final Map<String, ConversationSnapshot> conversations = {};
  final Map<String, List<ConvEventMsg>> convEvents = {}; // convID -> 成员变动系统消息
  final Map<String, int> _readCursors = {}; // convID -> 已读 ServerSeq
  final Map<String, String> typingUsers = {}; // convID -> 正在输入的 userID
  final Map<String, Timer> _typingTimers = {};
  String connectionStatus = '未连接';
  bool connected = false;
  String? lastError;

  int _cursor = 0;
  int get cursor => _cursor;

  HubClient({
    required this.host,
    required this.port,
    required this.userId,
    required this.deviceId,
  });

  String get wsUrl => 'ws://$host:$port/ws';

  /// 会话列表（大厅 + 群聊），按最后消息时间降序。
  List<ConversationSnapshot> get sortedConversations {
    final all = <ConversationSnapshot>[
      ...conversations.values,
      const ConversationSnapshot(id: lobbyConversationId, kind: 'lobby', title: '大厅'),
    ];
    final seen = <String>{};
    final unique = <ConversationSnapshot>[];
    for (final c in all) {
      if (seen.add(c.id)) unique.add(c);
    }
    unique.sort((a, b) {
      final la = lastMessageOf(a.id)?.createdAt ?? 0;
      final lb = lastMessageOf(b.id)?.createdAt ?? 0;
      return lb.compareTo(la);
    });
    return unique;
  }

  /// 某会话的消息（按 ServerSeq 升序）。
  List<StoredMessage> messagesOf(String convId) {
    final out = messages.where((m) => m.conversationId == convId).toList()
      ..sort((a, b) => a.serverSeq.compareTo(b.serverSeq));
    return out;
  }

  /// 某会话最后一条消息。
  StoredMessage? lastMessageOf(String convId) {
    final list = messagesOf(convId);
    return list.isEmpty ? null : list.last;
  }

  /// 某会话已读游标（自己或他人设备推进，广播同步）。
  int readSeqOf(String convId) => _readCursors[convId] ?? 0;

  /// 某会话未读数：他人发、seq 大于本地已读游标的消息数。
  int unreadCount(String convId) {
    final readSeq = _readCursors[convId] ?? 0;
    return messagesOf(convId)
        .where((m) => m.senderUserId != userId && m.serverSeq > readSeq)
        .length;
  }

  /// 进入会话：上报已读并本地记录游标。
  void markRead(String convId) {
    final last = lastMessageOf(convId);
    if (last == null || last.serverSeq <= (_readCursors[convId] ?? 0)) return;
    final seq = last.serverSeq;
    _readCursors[convId] = seq;
    _send(Frame(kind: kRead, payload: ReadMark(conversationId: convId, serverSeq: seq).toJson()));
    notifyListeners();
  }

  /// 开始连接。startCursor 来自本地持久化（0 = 首次）。
  Future<void> start(int startCursor) async {
    _closed = false;
    _cursor = startCursor;
    await _open();
  }

  Future<void> _open() async {
    if (_closed) return;
    connectionStatus = '连接中…';
    notifyListeners();
    try {
      final channel = WebSocketChannel.connect(Uri.parse(wsUrl));
      _channel = channel;
      _session = null;
      final handshakeDone = Completer<void>();
      _sub = channel.stream.listen(
        (raw) => _onRaw(raw, handshakeDone),
        onDone: _onDisconnected,
        onError: (Object e) {
          lastError = e.toString();
          _onDisconnected();
        },
        cancelOnError: true,
      );
      await channel.ready;
      // wire v2：连接建立后第一帧必须是明文 FKHandshake。
      await _sendHandshake(channel);
      await handshakeDone.future.timeout(
        const Duration(seconds: 10),
        onTimeout: () => throw TimeoutException('hub handshake timeout'),
      );
      _attempts = 0;
      _sendHello();
      _flushPending();
      _startPing();
      connected = true;
      connectionStatus = '已连接';
      notifyListeners();
    } catch (e) {
      lastError = e.toString();
      _onDisconnected();
    }
  }

  /// 发送明文握手帧：客户端临时 X25519 公钥（base64）。
  Future<void> _sendHandshake(WebSocketChannel channel) async {
    final (priv, pub) = await WsSecureSession.newClientKeyPair();
    _ephPriv = priv;
    _ephPub = pub;
    final payload = <String, dynamic>{
      'v': protocolVersion,
      'c': base64Encode(pub),
    };
    channel.sink.add(Frame(kind: kHandshake, payload: payload).encode());
  }

  /// 处理握手期收到的 FKHandshakeAck（明文）并启用加密。
  Future<void> _finalizeHandshake(Uint8List raw) async {
    final frame = Frame.decode(raw);
    if (frame.kind != kHandshakeAck) {
      throw FormatException('expected handshake ack, got kind ${frame.kind}');
    }
    final p = frame.payload ?? const {};
    final hubPub = base64Decode(p['h'] as String);
    final nonce = base64Decode(p['n'] as String);
    final cipher = base64Decode(p['c'] as String);
    if (hubPub.length != 32) {
      throw const FormatException('invalid hub public key');
    }
    final session = await WsSecureSession.derive(
      clientPriv: _ephPriv!,
      clientPub: _ephPub!,
      hubPub: hubPub,
    );
    await session.verifyChallenge(nonce, cipher);
    await _trustHub(hubPub);
    _session = session;
  }

  /// TOFU 记录 hub 公钥：首次信任并持久化，变化拒绝（防中间人）。
  Future<void> _trustHub(Uint8List hubPub) async {
    final key = 'known_hub_${host}_$port';
    final b64 = base64Encode(hubPub);
    final prefs = await SharedPreferences.getInstance();
    final saved = prefs.getString(key);
    if (saved != null && saved != b64) {
      throw StateError('hub public key changed (TOFU violation)');
    }
    if (saved == null) {
      await prefs.setString(key, b64);
    }
  }

  /// 统一收流入口：握手期明文，握手后解密。
  void _onRaw(dynamic raw, Completer<void> handshakeDone) {
    try {
      final bytes = raw is List<int>
          ? Uint8List.fromList(raw)
          : Uint8List.fromList((raw as String).codeUnits);
      if (_session == null) {
        if (handshakeDone.isCompleted) {
          // 握手完成后的明文帧：v2 下不应出现，容错丢弃。
          debugPrint('unexpected plaintext frame after handshake');
          return;
        }
        _finalizeHandshake(bytes).then((_) {
          if (!handshakeDone.isCompleted) handshakeDone.complete();
        }).catchError((Object e) {
          if (!handshakeDone.isCompleted) handshakeDone.completeError(e);
        });
        return;
      }
      _session!.openFrame(bytes).then((plain) {
        _handleFrame(Frame.decode(plain));
      }).catchError((Object e) {
        debugPrint('frame decrypt error: $e');
      });
    } catch (e) {
      debugPrint('frame decode error: $e');
    }
  }

  void _sendHello() {
    final hello = <String, dynamic>{
      'v': protocolVersion,
      'd': deviceId,
      'u': userId,
      if (_cursor > 0) 'r': _cursor,
    };
    _send(Frame(kind: kHello, payload: hello));
  }

  void _startPing() {
    _pingTimer?.cancel();
    _pingTimer = Timer.periodic(const Duration(seconds: 30), (_) {
      _send(Frame(kind: kPing));
    });
  }

  void _send(Frame frame) {
    // 断线时入队，重连成功后补发（消息不丢）。
    if (_channel == null || !connected) {
      if (frame.kind == kHello) return; // Hello 只由 _open 发送
      _pending.add(frame);
      return;
    }
    try {
      final wire = frame.encode();
      final s = _session;
      if (s == null) {
        _channel?.sink.add(wire);
      } else {
        s.sealFrame(wire).then((sealed) {
          _channel?.sink.add(sealed);
        }).catchError((_) {
          _pending.add(frame);
        });
      }
    } catch (_) {
      _pending.add(frame);
    }
  }

  void _flushPending() {
    if (_pending.isEmpty) return;
    final frames = List<Frame>.from(_pending);
    _pending.clear();
    for (final f in frames) {
      try {
        final wire = f.encode();
        final s = _session;
        if (s == null) {
          _channel?.sink.add(wire);
        } else {
          s.sealFrame(wire).then((sealed) {
            _channel?.sink.add(sealed);
          }).catchError((_) {
            _pending.add(f);
          });
        }
      } catch (_) {
        _pending.add(f);
      }
    }
  }

  void _handleFrame(Frame frame) {
    final p = frame.payload;
    switch (frame.kind) {
      case kConvList:
        // Go 侧载荷是裸数组（sendConvSnapshot json.Marshal(snaps)）：
        // 兼容数组与 {s:[...]}/{convs:[...]} 两种形态。
        final list = switch (p) {
          List l => l,
          Map m => (m['s'] ?? m['convs'] ?? const []),
          _ => const [],
        };
        for (final e in list) {
          final snap = ConversationSnapshot.fromJson(e as Map<String, dynamic>);
          conversations[snap.id] = snap;
        }
        _requestHistory();
        notifyListeners();
      case kHistoryResp:
        final resp = HistoryResponse.fromJson(p ?? {});
        _merge(resp.messages);
        loadingEarlier = false;
        if (resp.hasMore && resp.messages.isNotEmpty) {
          _requestHistory(after: resp.messages.last.serverSeq);
        }
        notifyListeners();
      case kDeliver:
        final msg = StoredMessage.fromJson(p ?? {});
        _merge([msg]);
        notifyListeners();
      case kConvEvent:
        final ev = ConversationEvent.fromJson(p ?? {});
        final existing = conversations[ev.snapshot.id];
        if (existing == null || ev.members.isNotEmpty) {
          conversations[ev.snapshot.id] = ConversationSnapshot(
            id: ev.snapshot.id,
            kind: ev.snapshot.kind,
            title: ev.snapshot.title,
            members: ev.members.isNotEmpty ? ev.members : (existing?.members ?? const []),
          );
        }
        // 群成员变动系统消息（实时，不持久化）。
        if (ev.event == 'joined' || ev.event == 'left') {
          final isSelf = ev.byUserId == userId;
          final who = isSelf ? '你' : ev.byUserId;
          final text = ev.event == 'joined'
              ? (isSelf ? '你加入了群聊' : '$who 加入了群聊')
              : (isSelf ? '你退出了群聊' : '$who 退出了群聊');
          convEvents[ev.snapshot.id] = [
            ...?convEvents[ev.snapshot.id],
            ConvEventMsg(
              convId: ev.snapshot.id,
              text: text,
              createdAt: DateTime.now().millisecondsSinceEpoch,
            ),
          ];
        }
        if (ev.event == 'left' && ev.snapshot.id != lobbyConversationId &&
            ev.snapshot.id.isNotEmpty) {
          // 自己退出：会话列表移除（保留大厅）。left 广播给剩余成员，
          // 对离开者自己此处保守处理：仅当 byUser 是自己时才移除。
          if (ev.byUserId == userId) {
            conversations.remove(ev.snapshot.id);
          }
        }
        notifyListeners();
      case kPresence:
        final presence = Presence.fromJson(p ?? {});
        if (presence.deviceId.isEmpty || presence.deviceId == deviceId) {
          onlineUsers[presence.userId] = presence.online;
        }
        notifyListeners();
      case kTyping:
        final convId = (p?['c'] as String?) ?? '';
        final typer = (p?['u'] as String?) ?? '';
        if (convId.isNotEmpty && typer.isNotEmpty && typer != userId) {
          typingUsers[convId] = typer;
          _typingTimers[convId]?.cancel();
          _typingTimers[convId] = Timer(const Duration(seconds: 3), () {
            if (typingUsers[convId] == typer) {
              typingUsers.remove(convId);
              notifyListeners();
            }
          });
          notifyListeners();
        }
      case kRead:
        // hub 盖戳广播：其他设备已读推进游标（自己发的消息据此显示已读）。
        final convId = (p?['c'] as String?) ?? '';
        final seq = (p?['s'] as num?)?.toInt() ?? 0;
        if (convId.isNotEmpty && seq > 0) {
          if (seq > (_readCursors[convId] ?? 0)) {
            _readCursors[convId] = seq;
          }
          notifyListeners();
        }
      case kSearchResp:
        searchResults
          ..clear()
          ..addAll(SearchResponse.fromJson(p ?? {}).hits);
        searching = false;
        notifyListeners();
      case kError:
        lastError = (p?['msg'] as String?) ?? (p?['code']?.toString() ?? 'hub error');
        connectionStatus = '错误: $lastError';
        if (searching) {
          searching = false;
          searchError = lastError;
        }
        notifyListeners();
      case kPong:
        break;
      default:
        debugPrint('unhandled frame kind ${frame.kind}');
    }
  }

  void _requestHistory({int after = 0, int limit = 200}) {
    final req = HistoryRequest(after: after, limit: limit).toJson();
    _send(Frame(kind: kHistoryReq, payload: req));
  }

  /// 向上加载更早历史（单会话，before=当前最早 seq）。
  bool loadingEarlier = false;

  void loadEarlier(String convId) {
    if (loadingEarlier) return;
    final earliest = messagesOf(convId).isEmpty ? null : messagesOf(convId).first.serverSeq;
    if (earliest == null) {
      _requestHistory();
      return;
    }
    loadingEarlier = true;
    notifyListeners();
    final req = HistoryRequest(conversationIds: [convId], before: earliest, limit: 200).toJson();
    _send(Frame(kind: kHistoryReq, payload: req));
  }

  void _merge(List<StoredMessage> incoming) {
    for (final m in incoming) {
      final idx = messages.indexWhere(
        (e) => e.serverSeq != 0 && e.serverSeq == m.serverSeq,
      );
      if (idx >= 0) {
        messages[idx] = m;
      } else {
        messages.add(m);
      }
      if (m.serverSeq > _cursor) _cursor = m.serverSeq;
    }
    messages.sort((a, b) => a.serverSeq.compareTo(b.serverSeq));
  }

  /// 发送一条文本消息。返回本地 nonce；Hub 回 FKDeliver 后合并。
  String sendMessage(String conversationId, String body, {ReplyRef? replyTo}) {
    final nonce = _newNonce();
    final now = DateTime.now().millisecondsSinceEpoch;
    final msg = StoredMessage(
      id: 'local-$nonce',
      clientNonce: nonce,
      conversationId: conversationId,
      senderUserId: userId,
      senderDeviceId: deviceId,
      body: body,
      createdAt: now,
      reply: replyTo,
    );
    _send(Frame(kind: kMessage, payload: msg.toJson()));
    return nonce;
  }

  /// 发送一条带文件附件的消息（FileRef 已由上传获得）。
  String sendFileMessage(String conversationId, FileRef file, {String body = ''}) {
    final nonce = _newNonce();
    final now = DateTime.now().millisecondsSinceEpoch;
    final msg = StoredMessage(
      id: 'local-$nonce',
      clientNonce: nonce,
      conversationId: conversationId,
      senderUserId: userId,
      senderDeviceId: deviceId,
      body: body,
      createdAt: now,
      file: file,
    );
    _send(Frame(kind: kMessage, payload: msg.toJson()));
    return nonce;
  }

  /// 创建群聊（FKConvCreate，payload {t, m}）。hub 广播 created 事件后
  /// 会话列表自动更新（创建者自动成为成员）。
  void createConversation(String title, List<String> memberIds) {
    final req = ConversationRequest(title: title, memberIds: memberIds);
    _send(Frame(kind: kConvCreate, payload: req.toJson()));
  }

  /// 退出群聊（FKConvLeave，payload {c}）。hub 广播 left 事件后
  /// 会话列表自动移除（仅剩大厅）。
  void leaveConversation(String conversationId) {
    _send(Frame(kind: kConvLeave, payload: {'c': conversationId}));
  }

  /// 邀请成员入群（FKConvInvite，payload {c, u}）。hub 广播 joined 事件。
  void inviteMembers(String conversationId, List<String> userIds) {
    if (userIds.isEmpty) return;
    _send(Frame(kind: kConvInvite, payload: {'c': conversationId, 'u': userIds}));
  }

  /// 历史搜索（v1.1）。结果异步回填 [searchResults] 并 notify。
  final List<StoredMessage> searchResults = [];
  bool searching = false;
  String? searchError;

  void search(String query, {String conversationId = ''}) {
    if (query.trim().isEmpty) return;
    searching = true;
    searchError = null;
    searchResults.clear();
    notifyListeners();
    final req = SearchRequest(query: query.trim(), conversationId: conversationId);
    _send(Frame(kind: kSearchReq, payload: req.toJson()));
  }

  void clearSearch() {
    searching = false;
    searchError = null;
    searchResults.clear();
    notifyListeners();
  }

  void sendTyping(String conversationId) {
    _send(Frame(kind: kTyping, payload: {'c': conversationId}));
  }

  void _onDisconnected() {
    final wasConnected = connected;
    connected = false;
    _session = null;
    _ephPriv = null;
    _ephPub = null;
    _pingTimer?.cancel();
    _sub?.cancel();
    try {
      _channel?.sink.close();
    } catch (_) {}
    if (wasConnected) {
      connectionStatus = '已断开，重连中…';
    } else {
      connectionStatus = '连接失败，重试中…';
    }
    notifyListeners();
    if (!_closed) {
      _attempts++;
      final delay = const [3, 6, 12, 15, 15][(_attempts - 1).clamp(0, 4)];
      _reconnectTimer?.cancel();
      _reconnectTimer = Timer(Duration(seconds: delay), () {
        if (!_closed) _open();
      });
    }
  }

  void close() {
    _closed = true;
    _pingTimer?.cancel();
    _reconnectTimer?.cancel();
    _sub?.cancel();
    try {
      _channel?.sink.close();
    } catch (_) {}
    connected = false;
    connectionStatus = '已断开';
    notifyListeners();
  }

  static String _newNonce() {
    final rnd = DateTime.now().microsecondsSinceEpoch;
    return '$rnd-${DateTime.now().millisecondsSinceEpoch}';
  }
}
