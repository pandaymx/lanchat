import 'dart:ffi';

import 'package:ffi/ffi.dart';
import 'package:path_provider/path_provider.dart';

/// 进程内嵌入式 Go hub 的 Dart 桥（FFI 直调 liblanchub.so）。
///
/// 去中心化第 4 步：Go hub 以 c-shared 编进安卓（mobile/cmd/androidhub），
/// so 打包在 jniLibs。App 启动/连接时经 lanchub_start 起本机回环嵌入式
/// hub（127.0.0.1 随机端口 + mesh + mDNS），拿回本地 ws 地址直接连——
/// 不再依赖「外部先跑 hub 进程」。so 缺失（桌面/未打包的构建）返回 null，
/// 调用方回退手动地址。
class EmbeddedHub {
  static DynamicLibrary? _lib;

  /// liblanchub.so 句柄；加载失败（平台无此 so）返回 null。
  static DynamicLibrary? get _handle {
    if (_lib != null) return _lib;
    try {
      _lib = DynamicLibrary.open('liblanchub.so');
    } catch (_) {
      _lib = null;
    }
    return _lib;
  }

  static String? _take(Pointer<Utf8>? p) {
    if (p == null || p == nullptr) return null;
    final s = p.toDartString();
    final lib = _handle;
    if (lib != null) {
      lib.lookupFunction<FreeNative, FreeDart>('lanchub_free')(p);
    }
    return s;
  }

  /// 启动嵌入式 hub，返回本地 ws 地址。
  /// [lanVisible]=true 时监听 0.0.0.0（局域网其他设备可发现并连上本机），
  /// false 只绑 127.0.0.1（本机自用）。
  /// 已启动时幂等返回现有地址；so 缺失或启动失败返回 null。
  static Future<String?> start(String nodeId, {bool lanVisible = false}) async {
    final lib = _handle;
    if (lib == null) return null;
    try {
      final start = lib.lookupFunction<StartNativeLan, StartDartLan>('lanchub_start_lan');
      String dataDir;
      try {
        dataDir = (await getApplicationDocumentsDirectory()).path;
      } catch (_) {
        dataDir = '';
      }
      final dirP = dataDir.toNativeUtf8();
      final nodeP = nodeId.toNativeUtf8();
      final verP = 'lanchat'.toNativeUtf8();
      try {
        final lanP = (lanVisible ? "1" : "0").toNativeUtf8();
        try {
          return _take(start(dirP, nodeP, verP, lanP));
        } finally {
          calloc.free(lanP);
        }
      } finally {
        calloc.free(dirP);
        calloc.free(nodeP);
        calloc.free(verP);
      }
    } catch (_) {
      return null;
    }
  }

  /// 停掉嵌入式 hub（幂等）。so 缺失时静默。
  static Future<void> stop() async {
    final lib = _handle;
    if (lib == null) return;
    try {
      lib.lookupFunction<StopNative, StopDart>('lanchub_stop')();
    } catch (_) {
      // 平台未实现时无需清理。
    }
  }
}

typedef StartNativeLan = Pointer<Utf8> Function(
    Pointer<Utf8>, Pointer<Utf8>, Pointer<Utf8>, Pointer<Utf8>);
typedef StartDartLan = Pointer<Utf8> Function(
    Pointer<Utf8>, Pointer<Utf8>, Pointer<Utf8>, Pointer<Utf8>);

/// C 侧返回 void 的导出：Native 泛型用 ffi 的 Void，Dart 侧用 void。
typedef StopNative = Void Function();
typedef StopDart = void Function();
typedef FreeNative = Void Function(Pointer<Utf8>);
typedef FreeDart = void Function(Pointer<Utf8>);
