import 'package:flutter_test/flutter_test.dart';

import 'package:lanchat_mobile/protocol.dart';

void main() {
  test('Frame round-trip 编解码', () {
    final frame = Frame(
      kind: kHello,
      ack: 7,
      payload: {'v': 1, 'd': 'dev-1', 'u': 'user-a'},
    );
    final bytes = frame.encode();
    final decoded = Frame.decode(bytes);
    expect(decoded.kind, kHello);
    expect(decoded.ack, 7);
    expect(decoded.payload!['u'], 'user-a');
    expect(decoded.payload!['v'], 1);
  });

  test('空 payload 帧', () {
    final frame = Frame(kind: kPing);
    final decoded = Frame.decode(frame.encode());
    expect(decoded.kind, kPing);
    expect(decoded.payload, isNull);
  });

  test('StoredMessage JSON 往返（含文件与引用）', () {
    final msg = StoredMessage(
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
    final json = msg.toJson();
    final back = StoredMessage.fromJson(json);
    expect(back.serverSeq, 42);
    expect(back.file!.fileId, 'abc');
    expect(back.reply!.senderUserId, 'bob');
  });

  test('HistoryResponse 解析', () {
    final resp = HistoryResponse.fromJson({
      'm': [
        {
          'id': 'm1',
          'nonce': 'n1',
          'conv': '',
          'suid': 'alice',
          'sdid': 'd1',
          'body': 'hi',
          'seq': 5,
          'at': 1700000000000,
        }
      ],
      'more': false,
    });
    expect(resp.messages.length, 1);
    expect(resp.messages.first.body, 'hi');
    expect(resp.hasMore, false);
  });
}
