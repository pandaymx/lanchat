/// hub WS 客户端：连接、心跳、重连、事件分发。
///
/// 与 Go pkg/client 对齐的 MVP 子集：
/// - 连接后发 Hello（含 ResumeFrom）；
/// - 收 ConvList / HistoryResp / Deliver / Presence / Error / Pong；
/// - 30s 心跳 Ping；断线 3s 指数退避重连（0-15s）；
/// - 本地游标（max serverSeq）持久化，重连时续传。
library;

import 'dart:async';
import 'dart:typed_data';

import 'package:flutter/foundation.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

import 'protocol.dart';

class HubClient extends ChangeNotifier {
  final String host;
  final int port;
  final String userId;
  final String deviceId;

  WebSocketChannel? _channel;
  StreamSubscription? _sub;
  Timer? _pingTimer;
  Timer? _reconnectTimer;
  bool _closed = false;
  int _attempts = 0;

  final List<StoredMessage> messages = [];
  final Map<String, bool> onlineUsers = {}; // userID -> online
  final Map<String, ConversationSnapshot> conversations = {};
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
      _sub = channel.stream.listen(
        _onData,
        onDone: _onDisconnected,
        onError: (Object e) {
          lastError = e.toString();
          _onDisconnected();
        },
        cancelOnError: true,
      );
      // 等待底层连接建立后发 Hello。
      await channel.ready;
      _attempts = 0;
      _sendHello();
      _startPing();
      connected = true;
      connectionStatus = '已连接';
      notifyListeners();
    } catch (e) {
      lastError = e.toString();
      _onDisconnected();
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
    try {
      _channel?.sink.add(frame.encode());
    } catch (_) {
      // 通道已关：由 onDone/onError 负责重连。
    }
  }

  void _onData(dynamic raw) {
    try {
      final bytes = raw is List<int> ? raw : (raw as String).codeUnits;
      final frame = Frame.decode(bytes is Uint8List ? bytes : Uint8List.fromList(bytes));
      _handleFrame(frame);
    } catch (e) {
      debugPrint('frame decode error: $e');
    }
  }

  void _handleFrame(Frame frame) {
    final p = frame.payload;
    switch (frame.kind) {
      case kConvList:
        final list = ((p?['s'] as List?) ?? (p?['convs'] as List?) ?? []);
        for (final e in list) {
          final snap = ConversationSnapshot.fromJson(e as Map<String, dynamic>);
          conversations[snap.id] = snap;
        }
        _requestHistory();
        notifyListeners();
      case kHistoryResp:
        final resp = HistoryResponse.fromJson(p ?? {});
        _merge(resp.messages);
        if (resp.hasMore && resp.messages.isNotEmpty) {
          _requestHistory(after: resp.messages.last.serverSeq);
        }
        notifyListeners();
      case kDeliver:
        final msg = StoredMessage.fromJson(p ?? {});
        _merge([msg]);
        _sendRead(msg.conversationId, _cursor);
        notifyListeners();
      case kPresence:
        final presence = Presence.fromJson(p ?? {});
        if (presence.deviceId.isEmpty || presence.deviceId == deviceId) {
          onlineUsers[presence.userId] = presence.online;
        }
        notifyListeners();
      case kError:
        lastError = (p?['msg'] as String?) ?? (p?['code']?.toString() ?? 'hub error');
        connectionStatus = '错误: $lastError';
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

  void _sendRead(String conversationId, int serverSeq) {
    if (serverSeq <= 0) return;
    _send(Frame(kind: kRead, payload: ReadMark(
      conversationId: conversationId,
      serverSeq: serverSeq,
    ).toJson()));
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

  void sendTyping(String conversationId) {
    _send(Frame(kind: kTyping, payload: {'c': conversationId}));
  }

  void _onDisconnected() {
    final wasConnected = connected;
    connected = false;
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
      final delay = const [3, 6, 12, 15, 15]
          [(_attempts - 1).clamp(0, 4)];
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
