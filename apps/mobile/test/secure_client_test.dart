/// wire v2 传输加密单测：会话派生、帧加解密、挑战校验（与 Go pkg/secure 对齐）。
library;

import 'dart:convert';
import 'dart:typed_data';

import 'package:test/test.dart';

import 'package:cryptography/cryptography.dart';
import 'package:lanchat_mobile/secure_client.dart';

void main() {
  group('WsSecureSession', () {
    test('derive 确定性：同一密钥对两次派生结果一致', () async {
      final (priv, pub) = await WsSecureSession.newClientKeyPair();
      final hubPub = await _fakeHubPub();
      final s1 = await WsSecureSession.derive(clientPriv: priv, clientPub: pub, hubPub: hubPub);
      final s2 = await WsSecureSession.derive(clientPriv: priv, clientPub: pub, hubPub: hubPub);
      // 密钥不出现在公开字段，用 seal 结果可重复性间接验证：
      // 同一明文两次 seal 的 ciphertext 不同（随机 nonce）但都能 open 回原文。
      final a = await s1.seal(utf8.encode('hello'));
      final b = await s1.seal(utf8.encode('hello'));
      expect(a, isNot(equals(b)));
      expect(utf8.decode(await s1.open(a)), 'hello');
      expect(utf8.decode(await s2.open(b)), 'hello');
    });

    test('seal/open 往返 + 篡改拒绝', () async {
      final (priv, pub) = await WsSecureSession.newClientKeyPair();
      final hubPub = await _fakeHubPub();
      final s = await WsSecureSession.derive(clientPriv: priv, clientPub: pub, hubPub: hubPub);
      final plain = utf8.encode('{"k":2,"p":"{\\"body\\":\\"hi\\"}"}');
      final env = await s.seal(plain);
      // envelope 是 JSON {"n","c"}
      final dec = jsonDecode(utf8.decode(env)) as Map<String, dynamic>;
      expect(dec['n'], isA<String>());
      expect(dec['c'], isA<String>());
      expect(utf8.decode(await s.open(env)), utf8.decode(plain));
      // 篡改任一字节 → 解密失败
      final tampered = Uint8List.fromList(env);
      tampered[tampered.length - 3] ^= 0x01;
      await expectLater(s.open(tampered), throwsA(anything));
    });

    test('sealFrame/openFrame：完整帧（前缀+body）往返', () async {
      final (priv, pub) = await WsSecureSession.newClientKeyPair();
      final hubPub = await _fakeHubPub();
      final s = await WsSecureSession.derive(clientPriv: priv, clientPub: pub, hubPub: hubPub);
      // 模拟 Frame.encode() 的输出：4B 大端长度前缀 + JSON body
      final body = utf8.encode('{"k":1,"a":0}');
      final wire = Uint8List(4 + body.length);
      wire.buffer.asByteData().setUint32(0, body.length);
      wire.setRange(4, wire.length, body);
      final sealed = await s.sealFrame(wire);
      // 前缀是加密后长度，与明文不同
      final sealedLen = sealed.buffer.asByteData().getUint32(0);
      expect(sealedLen, greaterThan(0));
      final opened = await s.openFrame(sealed);
      expect(opened, equals(wire));
    });

    test('verifyChallenge：正确挑战通过、篡改拒绝', () async {
      // 模拟 hub 侧：hub 用「hub 私钥 × 客户端临时公钥」派生同一密钥并加密挑战。
      final (clientPriv, clientPub) = await WsSecureSession.newClientKeyPair();
      final hub = await _fakeHubKeyPair();
      final hubPub = Uint8List.fromList((await hub.extractPublicKey()).bytes);
      // hub 侧派生（hubPriv × clientPub）
      final x25519 = X25519();
      final hubShared = await x25519.sharedSecretKey(
        keyPair: hub,
        remotePublicKey: SimplePublicKey(clientPub, type: KeyPairType.x25519),
      );
      final shared = await hubShared.extractBytes();
      final hkdf = Hkdf(hmac: Hmac.sha256(), outputLength: 32);
      final aad = _aadFor(clientPub, hubPub);
      final derived = await hkdf.deriveKey(
        secretKey: SecretKey(shared),
        nonce: aad,
        info: utf8.encode(kHkdfInfo),
      );
      final keyBytes = await derived.extractBytes();
      final aesGcm = AesGcm.with256bits();
      final nonce = Uint8List.fromList(aesGcm.newNonce());
      final box = await aesGcm.encrypt(
        utf8.encode(kChallenge),
        secretKey: SecretKey(keyBytes),
        nonce: nonce,
        aad: aad,
      );
      final ciphertext = Uint8List.fromList([...box.cipherText, ...box.mac.bytes]);

      // 客户端派生并校验
      final s = await WsSecureSession.derive(clientPriv: clientPriv, clientPub: clientPub, hubPub: hubPub);
      await s.verifyChallenge(nonce, ciphertext);

      // 篡改挑战密文 → 拒绝
      final bad = Uint8List.fromList(ciphertext);
      bad[0] ^= 0x01;
      await expectLater(s.verifyChallenge(nonce, bad), throwsA(anything));
    });

    test('newClientKeyPair：公钥 32B', () async {
      final (priv, pub) = await WsSecureSession.newClientKeyPair();
      expect(priv.length, 32);
      expect(pub.length, 32);
    });
  });
}

Future<Uint8List> _fakeHubPub() async {
  final x25519 = X25519();
  final pair = await x25519.newKeyPair();
  return Uint8List.fromList((await pair.extractPublicKey()).bytes);
}

Future<SimpleKeyPair> _fakeHubKeyPair() => X25519().newKeyPair();

Uint8List _aadFor(Uint8List pubA, Uint8List pubB) {
  final out = BytesBuilder(copy: false);
  if (_lt(pubA, pubB)) {
    out.add(pubA);
    out.add(pubB);
  } else {
    out.add(pubB);
    out.add(pubA);
  }
  return out.toBytes();
}

bool _lt(Uint8List a, Uint8List b) {
  for (var i = 0; i < a.length && i < b.length; i++) {
    if (a[i] != b[i]) return a[i] < b[i];
  }
  return a.length < b.length;
}
