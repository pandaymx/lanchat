import 'package:flutter_test/flutter_test.dart';

import 'package:lanchat_mobile/hub_client.dart';
import 'package:lanchat_mobile/protocol.dart';

/// 构造一条消息（测试辅助）。
StoredMessage msg({
  required String conv,
  required String suid,
  required int seq,
  String body = '',
  int at = 0,
}) {
  return StoredMessage(
    id: 'id-$seq',
    clientNonce: 'n-$seq',
    conversationId: conv,
    senderUserId: suid,
    senderDeviceId: 'd1',
    body: body,
    serverSeq: seq,
    createdAt: at == 0 ? 1700000000000 + seq : at,
  );
}

void main() {
  test('Frame round-trip 编解码', () {
    final frame = Frame(kind: kHello, ack: 7, payload: {'v': 1, 'd': 'dev-1', 'u': 'user-a'});
    final decoded = Frame.decode(frame.encode());
    expect(decoded.kind, kHello);
    expect(decoded.ack, 7);
    expect(decoded.payload!['u'], 'user-a');
  });

  test('空 payload 帧', () {
    final decoded = Frame.decode(Frame(kind: kPing).encode());
    expect(decoded.kind, kPing);
    expect(decoded.payload, isNull);
  });

  test('StoredMessage JSON 往返（含文件与引用）', () {
    final m = StoredMessage(
      id: 'id-1',
      clientNonce: 'nonce-1',
      conversationId: '',
      senderUserId: 'alice',
      senderDeviceId: 'dev-1',
      body: 'hello',
      serverSeq: 42,
      createdAt: 1700000000000,
      file: FileRef(fileId: 'abc', name: 'x.png', size: 1024, mime: 'image/png'),
      reply: ReplyRef(id: 'r1', senderUserId: 'bob', body: 'prev'),
    );
    final back = StoredMessage.fromJson(m.toJson());
    expect(back.serverSeq, 42);
    expect(back.file!.fileId, 'abc');
    expect(back.reply!.senderUserId, 'bob');
  });

  test('HistoryResponse 解析', () {
    final resp = HistoryResponse.fromJson({
      'm': [
        {
          'id': 'm1', 'nonce': 'n1', 'conv': '', 'suid': 'alice',
          'sdid': 'd1', 'body': 'hi', 'seq': 5, 'at': 1700000000000,
        }
      ],
      'more': false,
    });
    expect(resp.messages.length, 1);
    expect(resp.hasMore, false);
  });

  test('未读数：只算他人消息且 seq 大于已读游标', () {
    final c = HubClient(host: 'h', port: 9000, userId: 'me', deviceId: 'd');
    c.messages.addAll([
      msg(conv: '', suid: 'me', seq: 1),
      msg(conv: '', suid: 'bob', seq: 2),
      msg(conv: '', suid: 'bob', seq: 3),
      msg(conv: 'g1', suid: 'bob', seq: 4),
    ]);
    expect(c.unreadCount(''), 2); // seq 2,3（自己 seq1 不算）
    c.markRead('');
    expect(c.unreadCount(''), 0);
    expect(c.unreadCount('g1'), 1);
  });

  test('会话列表：大厅始终存在，按最后消息时间降序', () {
    final c = HubClient(host: 'h', port: 9000, userId: 'me', deviceId: 'd');
    c.conversations['g1'] = const ConversationSnapshot(id: 'g1', kind: 'group', title: '群A', members: ['a', 'b']);
    c.messages.addAll([
      msg(conv: 'g1', suid: 'bob', seq: 1, at: 1700000001000),
      msg(conv: '', suid: 'bob', seq: 2, at: 1700000002000),
    ]);
    final list = c.sortedConversations;
    expect(list.length, 2);
    // 大厅消息更新 → 大厅排第一
    expect(list.first.id, '');
    expect(list.last.title, '群A');
  });

  test('messagesOf 按 conv 过滤且升序', () {
    final c = HubClient(host: 'h', port: 9000, userId: 'me', deviceId: 'd');
    c.messages.addAll([
      msg(conv: 'g1', suid: 'a', seq: 3),
      msg(conv: '', suid: 'b', seq: 2),
      msg(conv: 'g1', suid: 'c', seq: 1),
    ]);
    final g = c.messagesOf('g1');
    expect(g.map((m) => m.serverSeq).toList(), [1, 3]);
    expect(c.messagesOf('').length, 1);
  });
}
