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

import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:http/http.dart' as http;
import 'package:path_provider/path_provider.dart';
import 'package:shared_preferences/shared_preferences.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

import 'dart:io';

import 'e2e.dart';
import 'package:crypto/crypto.dart' show sha256;
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
  final Map<String, Presence> onlineDevices = {}; // deviceID -> presence（E2E 目标选择）
  E2EIdentity? _e2e; // E2E 身份；null = 未启用（明文渐进）
  final Map<String, String> _pins = {}; // deviceId -> 首次见的公钥 b64（TOFU）
  /// 换钥告警回调（UI 层挂 SnackBar）；null = 未挂。
  void Function(E2EKeyChange change)? onKeyChange;
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

  String get _httpBase => 'http://$host:$port';

  /// 启用 E2E：加载/创建身份（docs/e2e_identity.bin）并注册公钥到 keyring。
  /// 失败不抛错——明文渐进，自我声明会逐步补全 keyring。
  Future<void> enableE2E() async {
    try {
      final dir = await getApplicationDocumentsDirectory();
      final id = await E2EIdentity.loadOrCreate(
          '${dir.path}/e2e_identity.bin');
      _e2e = id;
      await _loadPins();
      await _registerKey();
      debugPrint('e2e enabled, peer=${id.peerId}');
    } catch (e) {
      debugPrint('e2e enable failed, plaintext mode: $e');
    }
  }

  /// _checkPin：TOFU pinning。首次见即记录；一致无事；变了 → onKeyChange。
  void _checkPin(String deviceId, String pubB64) {
    final old = _pins[deviceId];
    if (old == null) {
      _pins[deviceId] = pubB64;
      _savePins();
      return;
    }
    if (old == pubB64) return;
    onKeyChange?.call(E2EKeyChange(
      deviceId: deviceId,
      oldFingerprint: e2eFingerprint(old),
      newFingerprint: e2eFingerprint(pubB64),
      newPubB64: pubB64,
    ));
  }

  /// trustE2EKey：用户核对完换钥告警后，把 deviceId 的 pin 覆盖为新公钥。
  /// 幂等；覆盖后下一次 checkPin 一致，不再告警。
  Future<void> trustE2EKey(String deviceId, String pubB64) async {
    _pins[deviceId] = pubB64;
    await _savePins();
  }

  Future<void> _loadPins() async {
    try {
      final dir = await getApplicationDocumentsDirectory();
      final f = File('${dir.path}/e2e_pins.json');
      if (!await f.exists()) return;
      final j = jsonDecode(await f.readAsString()) as Map<String, dynamic>;
      _pins
        ..clear()
        ..addAll(j.map((k, v) => MapEntry(k, v as String)));
    } catch (e) {
      debugPrint('e2e load pins failed: $e');
    }
  }

  Future<void> _savePins() async {
    try {
      final dir = await getApplicationDocumentsDirectory();
      final f = File('${dir.path}/e2e_pins.json');
      await f.writeAsString(jsonEncode(_pins));
    } catch (_) {}
  }

  /// 注册本设备公钥到 hub keyring（自我声明；失败静默）。
  Future<void> _registerKey() async {
    try {
      await http.post(
        Uri.parse('$_httpBase/api/v1/e2e/keys'),
        headers: {'content-type': 'application/json'},
        body: jsonEncode({
          'device_id': deviceId,
          'pubkey': base64Encode(_e2e!.pub),
        }),
      );
    } catch (_) {}
  }

  /// 批量查 keyring 公钥：deviceID → pubkey(base64)。
  Future<Map<String, String>> _lookupKeys(List<String> deviceIds) async {
    if (deviceIds.isEmpty) return {};
    try {
      final uri = Uri.parse(
          '$_httpBase/api/v1/e2e/keys?device_id=${deviceIds.join(',')}');
      final resp = await http.get(uri);
      if (resp.statusCode != 200) return {};
      final j = jsonDecode(resp.body) as Map<String, dynamic>;
      final keys = j['keys'];
      return keys is Map
          ? Map<String, String>.from(keys.map((k, v) => MapEntry(k as String, v as String)))
          : {};
    } catch (_) {
      return {};
    }
  }

  /// 为会话加密正文。目标 = 会话成员在线设备 + 自己设备；
  /// 全部有公钥才加密，否则返回明文（渐进）。返回 (enc, finalBody)。
  Future<(String, String)> _encryptFor(String convId, String body) async {
    final members = conversations[convId]?.members ?? const <String>[];
    final targets = <String>{};
    for (final p in onlineDevices.values) {
      if (p.online && members.contains(p.userId)) targets.add(p.deviceId);
    }
    if (deviceId.isNotEmpty) targets.add(deviceId);
    if (targets.isEmpty) return ('', body);

    final keys = await _lookupKeys(targets.toList());
    if (keys.length != targets.length) return ('', body); // 有设备缺公钥 → 明文

    keys.forEach(_checkPin);
    final pubs = keys.values.map(base64Decode).toList();
    final enc = await E2EEnvelope.encryptMulti(pubs, utf8.encode(body));
    return (enc, '');
  }

  /// 就地解密 deliver/history 帧里的密文消息（失败显示占位）。
  Future<void> _decryptFramePayload(Frame frame) async {
    final p = frame.payload;
    if (p is! Map<String, dynamic>) return;
    if (frame.kind == kDeliver) {
      if (p['enc'] is String && (p['enc'] as String).isNotEmpty) {
        final plain = await _tryDecrypt(p['enc'] as String);
        p['body'] = plain ?? '[加密消息：无法解密]';
        // 入站自我声明的公钥也走 pinning（中间人换钥后会带新 ek）。
        final ek = p['ek'];
        final sdid = p['sdid'];
        if (ek is String && ek.isNotEmpty && sdid is String && sdid.isNotEmpty) {
          _checkPin(sdid, ek);
        }
        p.remove('enc');
        p.remove('ek');
      }
      return;
    }
    final msgs = p['m'];
    if (msgs is List) {
      for (final m in msgs) {
        if (m is Map<String, dynamic> && m['enc'] is String &&
            (m['enc'] as String).isNotEmpty) {
          final plain = await _tryDecrypt(m['enc'] as String);
          m['body'] = plain ?? '[加密消息：无法解密]';
          final ek = m['ek'];
          final sdid = m['sdid'];
          if (ek is String && ek.isNotEmpty && sdid is String && sdid.isNotEmpty) {
            _checkPin(sdid, ek);
          }
          m.remove('enc');
          m.remove('ek');
        }
      }
    }
  }

  /// 尝试解 E2E 密文信封；失败（非接收者/认证失败）返回 null。
  Future<String?> _tryDecrypt(String encB64) async {
    try {
      final env = E2EEnvelope.unmarshal(encB64);
      final plain = await env.decrypt(_e2e!.seed);
      return plain == null ? null : utf8.decode(plain);
    } catch (_) {
      return null;
    }
  }

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
      _session!.openFrame(bytes).then((plain) async {
        final frame = Frame.decode(plain);
        // E2E：密文消息在进入事件分发前解密（deliver / history 统一路径）。
        if (_e2e != null && (frame.kind == kDeliver || frame.kind == kHistoryResp)) {
          await _decryptFramePayload(frame);
        }
        _handleFrame(frame);
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
        // E2E：记录他端设备级在线（发送时据此选加密目标）。
        if (presence.deviceId.isNotEmpty && presence.deviceId != deviceId) {
          onlineDevices[presence.deviceId] = presence;
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
  ///
  /// E2E 启用时：会话成员在线设备（含自己）全部有公钥才加密正文，
  /// 否则明文渐进；无论如何都自我声明本设备 E2E 公钥（keyring 收集）。
  Future<String> sendMessage(String conversationId, String body, {ReplyRef? replyTo}) async {
    final nonce = _newNonce();
    final now = DateTime.now().millisecondsSinceEpoch;
    var finalBody = body;
    var enc = '';
    var ek = '';
    if (_e2e != null) {
      try {
        final r = await _encryptFor(conversationId, body);
        enc = r.$1;
        finalBody = r.$2;
        ek = base64Encode(_e2e!.pub);
      } catch (e) {
        debugPrint('e2e encrypt error, plaintext fallback: $e');
      }
    }
    final msg = StoredMessage(
      id: 'local-$nonce',
      clientNonce: nonce,
      conversationId: conversationId,
      senderUserId: userId,
      senderDeviceId: deviceId,
      body: finalBody,
      encrypted: enc,
      e2eKey: ek,
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

/// E2E 公钥变更告警（TOFU pinning）。
class E2EKeyChange {
  final String deviceId;
  final String oldFingerprint;
  final String newFingerprint;
  final String newPubB64;
  E2EKeyChange({
    required this.deviceId,
    required this.oldFingerprint,
    required this.newFingerprint,
    required this.newPubB64,
  });
}

/// e2eFingerprint：sha256(pub) 前 8 字节 hex（16 字符，人眼可辨）。
String e2eFingerprint(String pubB64) {
  final sum = sha256.convert(base64Decode(pubB64)).bytes;
  return sum.take(8).map((b) => b.toRadixString(16).padLeft(2, '0')).join();
}
