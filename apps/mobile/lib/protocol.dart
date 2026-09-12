/// lanchat wire 协议 v1 的 Dart 复刻。
///
/// 线缆格式：4 字节大端长度前缀 + JSON Frame。
/// 与 Go 侧 pkg/protocol/wire.go 保持一致；帧枚举值不可变（线缆协议一部分）。
library;

import 'dart:convert';
import 'dart:typed_data';

// Frame kinds（与 wire.go FrameKind 对齐）。
const int kHello = 1;
const int kMessage = 2;
const int kDeliver = 3;
const int kHistoryReq = 4;
const int kHistoryResp = 5;
const int kAck = 6;
const int kRead = 7;
const int kPresence = 8;
const int kTyping = 9;
const int kConvList = 10;
const int kConvCreate = 11;
const int kConvInvite = 12;
const int kConvLeave = 13;
const int kConvEvent = 14;
const int kPing = 15;
const int kPong = 16;
const int kError = 17;
const int kSearchReq = 18;
const int kSearchResp = 19;

/// 线缆协议当前版本（Go: pkg/protocol/doc.go ProtocolVersion = 1）。
const int protocolVersion = 1;

/// 大厅会话 ID（Go: pkg/tui/session.go DefaultConversationID = ""）。
const String lobbyConversationId = '';

/// 一条帧：k = kind，a = ack（可选），p = 类型化负载的 JSON 字符串。
class Frame {
  final int kind;
  final int ack;
  final Map<String, dynamic>? payload;

  Frame({required this.kind, this.ack = 0, this.payload});

  Uint8List encode() {
    final map = <String, dynamic>{'k': kind};
    if (ack != 0) map['a'] = ack;
    if (payload != null) map['p'] = jsonEncode(payload);
    final body = utf8.encode(jsonEncode(map));
    final out = BytesBuilder();
    final len = ByteData(4)..setUint32(0, body.length);
    out.add(len.buffer.asUint8List());
    out.add(body);
    return out.toBytes();
  }

  static Frame decode(Uint8List bytes) {
    // 前 4 字节是大端长度前缀（不含自身），之后才是 JSON body。
    if (bytes.length < 4) {
      throw const FormatException('frame too short');
    }
    final body = Uint8List.sublistView(bytes, 4);
    final map = jsonDecode(utf8.decode(body)) as Map<String, dynamic>;
    final p = map['p'];
    return Frame(
      kind: (map['k'] as num).toInt(),
      ack: ((map['a'] as num?) ?? 0).toInt(),
      payload: p is String ? jsonDecode(p) as Map<String, dynamic> : null,
    );
  }
}

/// 文件附件引用（Go: FileRef，json: fid/n/sz/m）。
class FileRef {
  final String fileId;
  final String name;
  final int size;
  final String mime;

  FileRef({
    required this.fileId,
    required this.name,
    this.size = 0,
    this.mime = '',
  });

  factory FileRef.fromJson(Map<String, dynamic> j) => FileRef(
        fileId: (j['fid'] as String?) ?? '',
        name: (j['n'] as String?) ?? '',
        size: ((j['sz'] as num?) ?? 0).toInt(),
        mime: (j['m'] as String?) ?? '',
      );

  Map<String, dynamic> toJson() => {'fid': fileId, 'n': name, 'sz': size, 'm': mime};
}

/// 引用回复快照（Go: ReplyRef，json: id/suid/body）。
class ReplyRef {
  final String id;
  final String senderUserId;
  final String body;

  ReplyRef({required this.id, required this.senderUserId, this.body = ''});

  factory ReplyRef.fromJson(Map<String, dynamic> j) => ReplyRef(
        id: (j['id'] as String?) ?? '',
        senderUserId: (j['suid'] as String?) ?? '',
        body: (j['body'] as String?) ?? '',
      );
}

/// 一条已落库消息（Go: StoredMessage）。
class StoredMessage {
  final String id;
  final String clientNonce;
  final String conversationId;
  final String senderUserId;
  final String senderDeviceId;
  final String body;
  final int serverSeq;
  final int createdAt; // Unix 毫秒
  final FileRef? file;
  final ReplyRef? reply;

  StoredMessage({
    required this.id,
    required this.clientNonce,
    required this.conversationId,
    required this.senderUserId,
    required this.senderDeviceId,
    required this.body,
    this.serverSeq = 0,
    this.createdAt = 0,
    this.file,
    this.reply,
  });

  factory StoredMessage.fromJson(Map<String, dynamic> j) => StoredMessage(
        id: (j['id'] as String?) ?? '',
        clientNonce: (j['nonce'] as String?) ?? '',
        conversationId: (j['conv'] as String?) ?? '',
        senderUserId: (j['suid'] as String?) ?? '',
        senderDeviceId: (j['sdid'] as String?) ?? '',
        body: (j['body'] as String?) ?? '',
        serverSeq: ((j['seq'] as num?) ?? 0).toInt(),
        createdAt: ((j['at'] as num?) ?? 0).toInt(),
        file: j['f'] != null ? FileRef.fromJson(j['f'] as Map<String, dynamic>) : null,
        reply: j['r'] != null ? ReplyRef.fromJson(j['r'] as Map<String, dynamic>) : null,
      );

  Map<String, dynamic> toJson() => {
        'id': id,
        'nonce': clientNonce,
        'conv': conversationId,
        'suid': senderUserId,
        'sdid': senderDeviceId,
        'body': body,
        'seq': serverSeq,
        if (createdAt != 0) 'at': createdAt,
        if (file != null) 'f': file!.toJson(),
        if (reply != null)
          'r': {'id': reply!.id, 'suid': reply!.senderUserId, 'body': reply!.body},
      };
}

/// 历史请求（Go: HistoryRequest，json: c/a/b/l）。
class HistoryRequest {
  final List<String> conversationIds;
  final int after;
  final int before;
  final int limit;

  HistoryRequest({
    this.conversationIds = const [],
    this.after = 0,
    this.before = 0,
    this.limit = 0,
  });

  Map<String, dynamic> toJson() => {
        if (conversationIds.isNotEmpty) 'c': conversationIds,
        if (after != 0) 'a': after,
        if (before != 0) 'b': before,
        if (limit != 0) 'l': limit,
      };
}

/// 历史响应（Go: HistoryResponse，json: m/more）。
class HistoryResponse {
  final List<StoredMessage> messages;
  final bool hasMore;

  HistoryResponse({required this.messages, this.hasMore = false});

  factory HistoryResponse.fromJson(Map<String, dynamic> j) => HistoryResponse(
        messages: ((j['m'] as List?) ?? [])
            .map((e) => StoredMessage.fromJson(e as Map<String, dynamic>))
            .toList(),
        hasMore: (j['more'] as bool?) ?? false,
      );
}

/// 已读标记（Go: Read，json: c/s）。
class ReadMark {
  final String conversationId;
  final int serverSeq;

  ReadMark({required this.conversationId, required this.serverSeq});

  Map<String, dynamic> toJson() => {'c': conversationId, 's': serverSeq};
}

/// 上下线状态（Go: Presence，json: u/d/on）。
class Presence {
  final String userId;
  final String deviceId;
  final bool online;

  Presence({required this.userId, this.deviceId = '', required this.online});

  factory Presence.fromJson(Map<String, dynamic> j) => Presence(
        userId: (j['u'] as String?) ?? '',
        deviceId: (j['d'] as String?) ?? '',
        online: (j['on'] as bool?) ?? false,
      );
}

/// 会话快照（Go: ConversationSnapshot，json: c/m）。
class ConversationSnapshot {
  final String id;
  final String kind;
  final String title;
  final List<String> members;

  const ConversationSnapshot({
    required this.id,
    required this.kind,
    this.title = '',
    this.members = const [],
  });

  factory ConversationSnapshot.fromJson(Map<String, dynamic> j) {
    final c = (j['c'] as Map<String, dynamic>?) ?? const {};
    return ConversationSnapshot(
      id: (c['id'] as String?) ?? '',
      kind: (c['kind'] as String?) ?? '',
      title: (c['title'] as String?) ?? '',
      members: ((j['m'] as List?) ?? []).map((e) => e.toString()).toList(),
    );
  }
}

/// 创建群请求（Go: ConversationRequest，json: t/m）。
class ConversationRequest {
  final String title;
  final List<String> memberIds;

  ConversationRequest({required this.title, this.memberIds = const []});

  Map<String, dynamic> toJson() => {
        't': title,
        if (memberIds.isNotEmpty) 'm': memberIds,
      };
}

/// 会话变更广播（Go: ConversationEvent，json: c/e/u/m）。
class ConversationEvent {
  final ConversationSnapshot snapshot;
  final String event; // created / joined / left
  final String byUserId;
  final List<String> members;

  ConversationEvent({
    required this.snapshot,
    required this.event,
    this.byUserId = '',
    this.members = const [],
  });

  factory ConversationEvent.fromJson(Map<String, dynamic> j) {
    final c = (j['c'] as Map<String, dynamic>?) ?? const {};
    return ConversationEvent(
      snapshot: ConversationSnapshot(
        id: (c['id'] as String?) ?? '',
        kind: (c['kind'] as String?) ?? '',
        title: (c['title'] as String?) ?? '',
      ),
      event: (j['e'] as String?) ?? '',
      byUserId: (j['u'] as String?) ?? '',
      members: ((j['m'] as List?) ?? []).map((e) => e.toString()).toList(),
    );
  }
}
