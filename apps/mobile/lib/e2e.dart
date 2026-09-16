/// 消息端到端加密（E2E，Dart 侧，与 Go `pkg/e2e` 字节级对齐）。
///
/// 轻量 ECIES：每节点独立 E2E X25519 身份（与 mesh 传输身份分层），
/// 每条消息随机 DEK（AES-256-GCM 正文）+ eph 公钥封装 DEK，群聊
/// DEK 共享逐成员封装。信封 JSON → base64 放入消息 `enc` 字段；
/// hub/中继只见信封与密文，看不到明文。
///
/// 派生链（与 Go 完全一致）：ephPriv × recvPub → ECDH 共享秘密 →
/// HKDF-SHA256(salt=双方公钥排序拼接, info=`lanchat-e2e-v1`) →
/// 32B 封装密钥 → AES-GCM 封装 DEK；DEK 直接 AES-GCM 加密正文。
library;

import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:crypto/crypto.dart' show sha256;
import 'package:cryptography/cryptography.dart';

/// HKDF info 常量（与 Go pkg/e2e 的 keyInfo 一致）。
const String kE2EHkdfInfo = 'lanchat-e2e-v1';

/// 信封版本（与 Go EnvelopeVersion 一致）。
const int kE2EEnvelopeVersion = 1;

/// 生成 E2E PeerID：sha256(pub)[:16] hex（与 Go e2e.PeerID 一致，32 字符）。
String e2ePeerId(Uint8List pub) {
  final h = sha256.convert(pub).bytes.sublist(0, 16);
  return h.map((b) => b.toRadixString(16).padLeft(2, '0')).join();
}

/// E2E 身份：X25519 密钥对，seed 32B 持久化（与 Go e2e_identity.bin 同格式）。
class E2EIdentity {
  final Uint8List seed; // X25519 私钥 seed（32B）
  final Uint8List pub; // 公钥（32B raw）

  E2EIdentity._(this.seed, this.pub);

  /// 从 32B seed 重建身份。
  static Future<E2EIdentity> fromSeed(Uint8List seed) async {
    if (seed.length != 32) {
      throw ArgumentError('E2E seed must be 32 bytes');
    }
    final x = X25519();
    final pair = await x.newKeyPairFromSeed(seed);
    final pub = await pair.extractPublicKey();
    return E2EIdentity._(Uint8List.fromList(seed), Uint8List.fromList(pub.bytes));
  }

  /// 生成新身份。
  static Future<E2EIdentity> generate() async {
    final x = X25519();
    final pair = await x.newKeyPair();
    final seed = await pair.extractPrivateKeyBytes();
    final pub = await pair.extractPublicKey();
    return E2EIdentity._(Uint8List.fromList(seed), Uint8List.fromList(pub.bytes));
  }

  String get peerId => e2ePeerId(pub);

  /// 加载或创建身份文件（不存在则生成并 0600 持久化）。
  static Future<E2EIdentity> loadOrCreate(String path) async {
    final f = File(path);
    if (await f.exists()) {
      final raw = await f.readAsBytes();
      if (raw.length == 32) {
        return E2EIdentity.fromSeed(Uint8List.fromList(raw));
      }
      throw StateError('E2E identity file invalid (len=${raw.length})');
    }
    final id = await E2EIdentity.generate();
    await f.parent.create(recursive: true);
    await f.writeAsBytes(id.seed, flush: true);
    if (Platform.isLinux || Platform.isMacOS) {
      await Process.run('chmod', ['600', path]);
    }
    return id;
  }
}

/// 单个接收者的 DEK 封装（与 Go e2e.Recipient JSON 对齐）。
class E2ERecipient {
  final String keyId; // 接收者 E2E 公钥指纹
  final Uint8List encDEK; // AES-GCM(封装密钥, DEK)
  final Uint8List dekNonce;

  E2ERecipient({required this.keyId, required this.encDEK, required this.dekNonce});

  Map<String, dynamic> toJson() => {
        'key': keyId,
        'dek': base64Encode(encDEK),
        'dn': base64Encode(dekNonce),
      };

  factory E2ERecipient.fromJson(Map<String, dynamic> j) => E2ERecipient(
        keyId: (j['key'] as String?) ?? '',
        encDEK: base64Decode((j['dek'] as String?) ?? ''),
        dekNonce: base64Decode((j['dn'] as String?) ?? ''),
      );
}

/// E2E 消息信封（与 Go e2e.Envelope JSON 对齐）。
class E2EEnvelope {
  final int version;
  final Uint8List ephemeral; // eph 临时 X25519 公钥（raw 32B）
  final List<E2ERecipient> recipients;
  final Uint8List nonce; // 正文 GCM nonce
  final Uint8List ciphertext;

  E2EEnvelope({
    this.version = kE2EEnvelopeVersion,
    required this.ephemeral,
    required this.recipients,
    required this.nonce,
    required this.ciphertext,
  });

  /// 用一组接收者公钥加密明文（群聊：DEK 共享，逐成员封装，重复去重）。
  static Future<String> encryptMulti(List<Uint8List> recvPubs, Uint8List plain) async {
    if (recvPubs.isEmpty) {
      throw ArgumentError('e2e: no recipients');
    }
    // 1) 随机数据密钥 DEK。
    final dek = Uint8List.fromList(
        Uint8List.fromList(await (await SecretKeyData.random(length: 32)).extractBytes()));
    // 2) 临时 eph 密钥对。
    final x = X25519();
    final eph = await x.newKeyPair();
    final ephPub = await eph.extractPublicKey();
    final ephPubBytes = Uint8List.fromList(ephPub.bytes);
    // 3) 正文 AES-256-GCM。
    final aesGcm = AesGcm.with256bits();
    final nonce = Uint8List.fromList(aesGcm.newNonce());
    final box = await aesGcm.encrypt(plain, secretKey: SecretKey(dek), nonce: nonce);
    final combined = Uint8List.fromList([...box.cipherText, ...box.mac.bytes]);

    final recipients = <E2ERecipient>[];
    final seen = <String>{};
    for (final pub in recvPubs) {
      final keyId = e2ePeerId(pub);
      if (seen.contains(keyId)) continue;
      seen.add(keyId);
      // 4) 每个接收者封装 DEK。
      final shared = await x.sharedSecretKey(
        keyPair: eph,
        remotePublicKey: SimplePublicKey(pub, type: KeyPairType.x25519),
      );
      final sharedBytes = Uint8List.fromList(await shared.extractBytes());
      final wrapKey = await _deriveKey(sharedBytes, ephPubBytes, pub);
      final wrapNonce = Uint8List.fromList(aesGcm.newNonce());
      final wrapBox = await aesGcm.encrypt(
        dek,
        secretKey: wrapKey,
        nonce: wrapNonce,
      );
      recipients.add(E2ERecipient(
        keyId: keyId,
        encDEK: Uint8List.fromList([...wrapBox.cipherText, ...wrapBox.mac.bytes]),
        dekNonce: wrapNonce,
      ));
    }

    final env = E2EEnvelope(
      ephemeral: ephPubBytes,
      recipients: recipients,
      nonce: nonce,
      ciphertext: combined,
    );
    return env.marshal();
  }

  /// 用本端 E2E 私钥 seed 解出明文。
  ///
  /// 按 KeyID 匹配本节点封装；非接收者返回 null，认证失败返回 null
  ///（调用方显示占位「[加密消息：无法解密]」）。
  Future<Uint8List?> decrypt(Uint8List privSeed) async {
    if (version != kE2EEnvelopeVersion) return null;
    final id = await E2EIdentity.fromSeed(privSeed);
    final myID = id.peerId;
    final x = X25519();
    final ephPub = SimplePublicKey(ephemeral, type: KeyPairType.x25519);
    final ephPriv = await x.newKeyPairFromSeed(privSeed);
    final aesGcm = AesGcm.with256bits();
    for (final rcpt in recipients) {
      if (rcpt.keyId != myID) continue;
      try {
        final shared = await x.sharedSecretKey(
          keyPair: ephPriv,
          remotePublicKey: ephPub,
        );
        final sharedBytes = Uint8List.fromList(await shared.extractBytes());
        final wrapKey = await _deriveKey(sharedBytes, ephemeral, id.pub);
        final dek = await _openGCM(wrapKey, rcpt.dekNonce, rcpt.encDEK);
        if (dek == null) return null;
        final plain = await _openGCM(SecretKey(dek), nonce, ciphertext);
        return plain;
      } catch (_) {
        return null;
      }
    }
    return null;
  }

  /// 序列化：JSON → base64（与 Go Marshal 一致）。
  String marshal() => base64Encode(utf8.encode(jsonEncode(toJson())));

  Map<String, dynamic> toJson() => {
        'v': version,
        'eph': base64Encode(ephemeral),
        'rcpts': recipients.map((r) => r.toJson()).toList(),
        'n': base64Encode(nonce),
        'ct': base64Encode(ciphertext),
      };

  /// 反序列化（与 Go Unmarshal 一致；版本不符抛错）。
  factory E2EEnvelope.unmarshal(String s) {
    final decoded = jsonDecode(utf8.decode(base64Decode(s)));
    if (decoded is! Map<String, dynamic>) {
      throw const FormatException('e2e: envelope');
    }
    final v = (decoded['v'] as num?)?.toInt() ?? 0;
    if (v != kE2EEnvelopeVersion) {
      throw FormatException('e2e: unsupported envelope version $v');
    }
    return E2EEnvelope(
      version: v,
      ephemeral: base64Decode(decoded['eph'] as String),
      recipients: ((decoded['rcpts'] as List?) ?? const [])
          .map((r) => E2ERecipient.fromJson(r as Map<String, dynamic>))
          .toList(),
      nonce: base64Decode(decoded['n'] as String),
      ciphertext: base64Decode(decoded['ct'] as String),
    );
  }
}

/// HKDF 派生封装密钥：salt = 双方公钥排序拼接，info = kE2EHkdfInfo。
Future<SecretKey> _deriveKey(Uint8List shared, Uint8List pubA, Uint8List pubB) async {
  final Uint8List salt;
  if (_le(pubA, pubB)) {
    salt = Uint8List.fromList([...pubA, ...pubB]);
  } else {
    salt = Uint8List.fromList([...pubB, ...pubA]);
  }
  final hkdf = Hkdf(hmac: Hmac.sha256(), outputLength: 32);
  return hkdf.deriveKey(
    secretKey: SecretKey(shared),
    nonce: salt,
    info: utf8.encode(kE2EHkdfInfo),
  );
}

bool _le(Uint8List a, Uint8List b) {
  for (var i = 0; i < a.length && i < b.length; i++) {
    if (a[i] != b[i]) return a[i] < b[i];
  }
  return a.length <= b.length;
}

/// AES-GCM 解封公共路径（密文为 ct||tag 拼接，认证失败返回 null）。
Future<Uint8List?> _openGCM(SecretKey key, Uint8List nonce, Uint8List combined) async {
  if (combined.length < kGcmTagLength) return null;
  final ct = combined.sublist(0, combined.length - kGcmTagLength);
  final mac = Mac(combined.sublist(combined.length - kGcmTagLength));
  final aesGcm = AesGcm.with256bits();
  try {
    final plain = await aesGcm.decrypt(
      SecretBox(ct, nonce: nonce, mac: mac),
      secretKey: key,
    );
    return Uint8List.fromList(plain);
  } catch (_) {
    return null;
  }
}

/// AES-GCM tag 长度（16B，与 Go 一致）。
const int kGcmTagLength = 16;
