/// hub HTTP 端点（文件通道，与 Go pkg/client/file.go 对齐）。
///
/// FileID 由 hub 生成（crypto/rand 32 hex）；Name/Mime 仅用于展示与渲染
/// 决策，下载一律 GET /api/files/{fileID}。
library;

class HubApi {
  final String host;
  final int port;

  HubApi({required this.host, required this.port});

  String get httpBase => 'http://$host:$port';

  /// 文件下载 URL（图片/附件）。
  String fileUrl(String fileId) => '$httpBase/api/files/$fileId';

  /// 上传文件（multipart POST /api/files），返回 hub 分配的 FileRef。
  /// MVP 阶段仅文本与图片展示；上传留待下一迭代。
  // Not implemented in MVP.
}
