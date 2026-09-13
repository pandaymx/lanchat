/// wire v2 传输加密（Dart 侧，与 Go `pkg/secure` + `pkg/transport/ws/handshake.go` 字节级对齐）。
///
/// 线缆格式（加密后）：
/// - 4 字节大端长度前缀（**加密后** body 长度，始终明文）+ body；
/// - body 是 JSON Envelope：`{"n": base64(nonce), "c": base64(密文含 16B tag)}`；
/// - 解密后得到明文 body：`{"k":..,"a":..,"p":..}` 标准 Frame JSON。
///
/// 密钥派生（与 Go 一致）：
/// 客户端一次性 X25519 私钥 × hub 持久公钥 → ECDH → HKDF-SHA256，
/// salt = 双方公钥排序拼接（AAD 同值），info = `lanchat-ws-v1`，输出 32B AES-256-GCM 密钥。
library;

import 'dart:convert';
import 'dart:typed_data';

import 'package:cryptography/cryptography.dart';

/// 握手挑战明文：hub 用会话密钥加密后发给客户端，客户端解密比对。
const String kChallenge = 'lanchat-ws-challenge-v1';

/// HKDF info（与 Go pkg/secure/session.go 一致，区分 mesh 的 `lanchat-mesh-v1`）。
const String kHkdfInfo = 'lanchat-ws-v1';

/// AES-GCM 认证 tag 长度（字节）。
const int kGcmTagLength = 16;

/// 一条已建立的 client↔hub 会话密钥（AES-256-GCM + AAD）。
class WsSecureSession {
  final Uint8List _key;
  final Uint8List _aad;

  WsSecureSession._(this._key, this._aad);

  Uint8List get aad => _aad;

  /// 由客户端临时私钥/公钥 + hub 公钥派生会话密钥。
  /// [clientPriv]/[clientPub] 是 32B 原始字节；[hubPub] 是 hub 持久公钥 32B。
  static Future<WsSecureSession> derive({
    required Uint8List clientPriv,
    required Uint8List clientPub,
    required Uint8List hubPub,
  }) async {
    if (clientPub.length != 32 || hubPub.length != 32) {
      throw ArgumentError('X25519 public keys must be 32 bytes');
    }
    final x25519 = X25519();
    // X25519 私钥即 32B seed：由 seed 重建密钥对参与 ECDH。
    final keyPair = await x25519.newKeyPairFromSeed(clientPriv);
    final sharedKey = await x25519.sharedSecretKey(
      keyPair: keyPair,
      remotePublicKey: SimplePublicKey(hubPub, type: KeyPairType.x25519),
    );
    final shared = await sharedKey.extractBytes();
    final aad = _aadFor(clientPub, hubPub);
    // cryptography 的 Hkdf 用 nonce 参数充当 HKDF salt（与 Go 侧 salt 对齐）。
    final hkdf = Hkdf(hmac: Hmac.sha256(), outputLength: 32);
    final derived = await hkdf.deriveKey(
      secretKey: SecretKey(shared),
      nonce: aad,
      info: utf8.encode(kHkdfInfo),
    );
    final keyBytes = await derived.extractBytes();
    return WsSecureSession._(Uint8List.fromList(keyBytes), aad);
  }

  /// 生成客户端一次性 X25519 密钥对，返回 (私钥, 公钥)。
  static Future<(Uint8List, Uint8List)> newClientKeyPair() async {
    final x25519 = X25519();
    final pair = await x25519.newKeyPair();
    final priv = await pair.extractPrivateKeyBytes();
    final pub = await pair.extractPublicKey();
    return (Uint8List.fromList(priv), Uint8List.fromList(pub.bytes));
  }

  /// 加密一段明文 body → wire Envelope 字节（JSON `{"n","c"}`）。
  Future<Uint8List> seal(Uint8List plain) async {
    final aesGcm = AesGcm.with256bits();
    final nonce = aesGcm.newNonce();
    final box = await aesGcm.encrypt(
      plain,
      secretKey: SecretKey(_key),
      nonce: nonce,
      aad: _aad,
    );
    final combined = Uint8List.fromList([...box.cipherText, ...box.mac.bytes]);
    final env = jsonEncode({
      'n': base64Encode(nonce),
      'c': base64Encode(combined),
    });
    return Uint8List.fromList(utf8.encode(env));
  }

  /// 解密 wire Envelope body → 明文 body。
  Future<Uint8List> open(Uint8List envelope) async {
    final decoded = jsonDecode(utf8.decode(envelope));
    if (decoded is! Map<String, dynamic>) {
      throw const FormatException('secure: envelope');
    }
    final nonce = base64Decode(decoded['n'] as String);
    final combined = base64Decode(decoded['c'] as String);
    if (combined.length < kGcmTagLength) {
      throw const FormatException('secure: cipher too short');
    }
    final cipherText = combined.sublist(0, combined.length - kGcmTagLength);
    final mac = Mac(combined.sublist(combined.length - kGcmTagLength));
    final aesGcm = AesGcm.with256bits();
    final plain = await aesGcm.decrypt(
      SecretBox(cipherText, nonce: nonce, mac: mac),
      secretKey: SecretKey(_key),
      aad: _aad,
    );
    return Uint8List.fromList(plain);
  }

  /// 加密一帧（4 字节前缀 + body）→ 线上格式（新前缀 + Envelope body）。
  Future<Uint8List> sealFrame(Uint8List wireFrame) async {
    if (wireFrame.length < 4) {
      throw const FormatException('frame too short');
    }
    final body = wireFrame.sublist(4);
    final sealed = await seal(body);
    return _join(_prefix(sealed.length), sealed);
  }

  /// 解密线上格式（前缀 + Envelope）→ 完整帧（新前缀 + 明文 body）。
  Future<Uint8List> openFrame(Uint8List wireMsg) async {
    if (wireMsg.length < 4) {
      throw const FormatException('frame too short');
    }
    final plain = await open(wireMsg.sublist(4));
    return _join(_prefix(plain.length), plain);
  }

  /// 校验 hub 挑战密文（证明 hub 持有持久私钥）。
  Future<void> verifyChallenge(Uint8List nonce, Uint8List ciphertext) async {
    final combined = ciphertext;
    if (combined.length < kGcmTagLength) {
      throw const FormatException('challenge cipher too short');
    }
    final cipher = combined.sublist(0, combined.length - kGcmTagLength);
    final mac = Mac(combined.sublist(combined.length - kGcmTagLength));
    final aesGcm = AesGcm.with256bits();
    final plain = await aesGcm.decrypt(
      SecretBox(cipher, nonce: nonce, mac: mac),
      secretKey: SecretKey(_key),
      aad: _aad,
    );
    if (utf8.decode(plain) != kChallenge) {
      throw StateError('secure: challenge mismatch');
    }
  }

  static Uint8List _aadFor(Uint8List pubA, Uint8List pubB) {
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

  static bool _lt(Uint8List a, Uint8List b) {
    for (var i = 0; i < a.length && i < b.length; i++) {
      if (a[i] != b[i]) return a[i] < b[i];
    }
    return a.length < b.length;
  }

  static Uint8List _prefix(int len) {
    return Uint8List(4)..buffer.asByteData().setUint32(0, len);
  }

  static Uint8List _join(Uint8List a, Uint8List b) {
    final out = Uint8List(a.length + b.length);
    out.setRange(0, a.length, a);
    out.setRange(a.length, out.length, b);
    return out;
  }
}
