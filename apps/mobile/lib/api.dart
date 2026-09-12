/// hub HTTP 端点（文件通道，与 Go pkg/client/file.go 对齐）。
///
/// FileID 由 hub 生成（crypto/rand 32 hex）；Name/Mime 仅用于展示与渲染
/// 决策。上传 = multipart POST /api/files → FileRef；下载 = GET /api/files/{fid}。
library;

import 'dart:convert';
import 'dart:io';

import 'package:http/http.dart' as http;

import 'protocol.dart';

class HubApi {
  final String host;
  final int port;

  HubApi({required this.host, required this.port});

  String get httpBase => 'http://$host:$port';

  /// 文件下载 URL（图片/附件）。
  String fileUrl(String fileId) => '$httpBase/api/files/$fileId';

  /// 上传文件（multipart POST /api/files），返回 hub 分配的 FileRef。
  Future<FileRef> uploadFile(File file, {String? mime}) async {
    final uri = Uri.parse('$httpBase/api/files');
    final req = http.MultipartRequest('POST', uri)
      ..files.add(await http.MultipartFile.fromPath(
        'file',
        file.path,
        filename: file.uri.pathSegments.last,
        contentType: mime != null ? _mediaType(mime) : null,
      ));
    final streamed = await req.send();
    final resp = await http.Response.fromStream(streamed);
    if (resp.statusCode != 200) {
      throw Exception('upload failed: HTTP ${resp.statusCode} ${resp.body}');
    }
    final json = jsonDecode(utf8.decode(resp.bodyBytes)) as Map<String, dynamic>;
    return FileRef.fromJson(json);
  }

  http.MediaType? _mediaType(String mime) {
    final parts = mime.split('/');
    if (parts.length != 2) return null;
    return http.MediaType(parts[0], parts[1]);
  }
}
