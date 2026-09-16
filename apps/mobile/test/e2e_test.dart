import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:test/test.dart';

import 'package:lanchat_mobile/e2e.dart';

/// 互操作 fixture（Go pkg/e2e 生成）：A 加密 → 信封；验证 Dart 能解。
const String kGoEnvelope =
    'eyJ2IjoxLCJlcGgiOiJSSzJxQVdnUnU4WEROV3p3WWR5Y0prZkJEblI5bGdIU1NmSURBdTltdUJRPSIsInJjcHRzIjpbeyJrZXkiOiI5NzQ0MTQ3MGUyZDRkNDRlY2E3ZmE4MjNmMjI0NzJiMyIsImRlayI6IkUzYVZRRmhFbXVsR0Q3UG5zeVpCRE10TE82WlpiM2czRXBoRTdKekE1UlhtS1NRMmtjczEwd2ZPNHphVkNPUlUiLCJkbiI6IjBxZTgrOURNUk1aV1RoOFYifV0sIm4iOiJOd1ZtdXlyYU9mOW4yTHNpIiwiY3QiOiJqV3JtcXNxYk9kZFczRXBkeWpWN3RIR0U5TlF2cWN2TStLbzdKbjRHeWVrPSJ9';
const String kGoSeedB =
    '93358d6cef083a019b5b20d0114e760a89afea3e290a6a3d932d55cb402a6a4f';
const String kGoPeerB = '97441470e2d4d44eca7fa823f22472b3';
const String kGoPlain = 'hello e2e 你好';

Uint8List hex(String s) {
  final out = Uint8List(s.length ~/ 2);
  for (var i = 0; i < out.length; i++) {
    out[i] = int.parse(s.substring(i * 2, i * 2 + 2), radix: 16);
  }
  return out;
}

void main() {
  test('Go 生成的密文信封能被 Dart 解密（跨语言互操作）', () async {
    final id = await E2EIdentity.fromSeed(hex(kGoSeedB));
    expect(id.peerId, kGoPeerB);

    final env = E2EEnvelope.unmarshal(kGoEnvelope);
    final plain = await env.decrypt(id.seed);
    expect(plain, isNotNull);
    expect(utf8.decode(plain!), kGoPlain);
  });

  test('Dart 加密 → Dart 解密（自洽闭环）', () async {
    final a = await E2EIdentity.generate();
    final b = await E2EIdentity.generate();
    final enc = await E2EEnvelope.encryptMulti([b.pub], utf8.encode('秘密消息'));
    final env = E2EEnvelope.unmarshal(enc);
    final plain = await env.decrypt(b.seed);
    expect(utf8.decode(plain!), '秘密消息');
    // 非接收者（第三方）解不开。
    final c = await E2EIdentity.generate();
    expect(await env.decrypt(c.seed), isNull);
  });

  test('群聊多接收者 DEK 共享，逐成员可解', () async {
    final a = await E2EIdentity.generate();
    final b = await E2EIdentity.generate();
    final c = await E2EIdentity.generate();
    final enc = await E2EEnvelope.encryptMulti(
        [a.pub, b.pub, c.pub, b.pub], utf8.encode('群聊密文'));
    final env = E2EEnvelope.unmarshal(enc);
    // 重复接收者去重：只应有 3 个封装。
    expect(env.recipients.length, 3);
    for (final id in [a, b, c]) {
      expect(utf8.decode((await env.decrypt(id.seed))!), '群聊密文');
    }
  });

  test('篡改密文认证失败返回 null', () async {
    final a = await E2EIdentity.generate();
    final b = await E2EIdentity.generate();
    final enc = await E2EEnvelope.encryptMulti([b.pub], utf8.encode('机密'));
    final env = E2EEnvelope.unmarshal(enc);
    // 翻转密文最后一字节。
    final ct = Uint8List.fromList(env.ciphertext);
    ct[ct.length - 1] ^= 0x01;
    final tampered = E2EEnvelope(
      ephemeral: env.ephemeral,
      recipients: env.recipients,
      nonce: env.nonce,
      ciphertext: ct,
    );
    expect(await tampered.decrypt(b.seed), isNull);
  });

  test('E2EIdentity 持久化与重载一致', () async {
    final dir = await Directory.systemTemp.createTemp('lanchat-e2e-test');
    final path = '${dir.path}/id.bin';
    final id = await E2EIdentity.generate();
    await File(path).writeAsBytes(id.seed);
    final loaded = await E2EIdentity.loadOrCreate(path);
    expect(loaded.peerId, id.peerId);
    expect(loaded.seed, id.seed);
  });
}
