import 'package:flutter/material.dart';

import '../hub_client.dart';
import '../protocol.dart';
import 'audio_ctl.dart';

/// 消息气泡：文本 / 图片 / 引用 / 时间戳。
/// 深色仿 QQ：自己蓝色（#2B6BFF）、他人深灰。
class MessageBubble extends StatelessWidget {
  final StoredMessage message;
  final HubClient client;
  final String selfUserId;
  final VoidCallback? onTap;
  final VoidCallback? onLongPress;

  const MessageBubble({
    super.key,
    required this.message,
    required this.client,
    required this.selfUserId,
    this.onTap,
    this.onLongPress,
  });

  bool get isMine => message.senderUserId == selfUserId;

  /// @提及高亮：`@名字` 用品牌蓝渲染，其余文本保持基础色。
  TextSpan _bodySpan(String body, Color base) {
    final mention = RegExp(r'@([^\s@，。！？,.!?]+)');
    final out = <TextSpan>[];
    var start = 0;
    for (final m in mention.allMatches(body)) {
      if (m.start > start) {
        out.add(TextSpan(text: body.substring(start, m.start)));
      }
      out.add(TextSpan(
        text: m.group(0),
        style: const TextStyle(color: Color(0xFF2B6BFF), fontWeight: FontWeight.w600),
      ));
      start = m.end;
    }
    if (start < body.length) {
      out.add(TextSpan(text: body.substring(start)));
    }
    return TextSpan(children: out, style: TextStyle(color: base, height: 1.4));
  }

  String _formatTime(int atMs) {
    if (atMs <= 0) return '';
    final t = DateTime.fromMillisecondsSinceEpoch(atMs);
    final now = DateTime.now();
    final sameDay = t.year == now.year && t.month == now.month && t.day == now.day;
    String two(int v) => v.toString().padLeft(2, '0');
    final hm = '${two(t.hour)}:${two(t.minute)}';
    if (sameDay) return hm;
    final yesterday = now.subtract(const Duration(days: 1));
    final isYesterday = t.year == yesterday.year &&
        t.month == yesterday.month &&
        t.day == yesterday.day;
    if (isYesterday) return '昨天 $hm';
    if (t.year == now.year) return '${t.month}/${t.day} $hm';
    return '${t.year}/${t.month}/${t.day} $hm';
  }

  bool get _isImage {
    final f = message.file;
    if (f == null) return false;
    if (f.mime.startsWith('image/')) return true;
    final lower = f.name.toLowerCase();
    return lower.endsWith('.png') ||
        lower.endsWith('.jpg') ||
        lower.endsWith('.jpeg') ||
        lower.endsWith('.gif') ||
        lower.endsWith('.webp');
  }

  bool get _isAudio {
    final f = message.file;
    if (f == null) return false;
    if (f.mime.startsWith('audio/')) return true;
    final lower = f.name.toLowerCase();
    return lower.endsWith('.m4a') ||
        lower.endsWith('.aac') ||
        lower.endsWith('.mp3') ||
        lower.endsWith('.wav') ||
        lower.endsWith('.ogg');
  }

  @override
  Widget build(BuildContext context) {
    final bubbleColor = isMine ? const Color(0xFF2B6BFF) : const Color(0xFF2B2D33);
    final textColor = isMine ? Colors.white : const Color(0xFFE6E8EC);
    final radius = BorderRadius.only(
      topLeft: const Radius.circular(14),
      topRight: const Radius.circular(14),
      bottomLeft: Radius.circular(isMine ? 14 : 4),
      bottomRight: Radius.circular(isMine ? 4 : 14),
    );

    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
      child: Row(
        mainAxisAlignment:
            isMine ? MainAxisAlignment.end : MainAxisAlignment.start,
        crossAxisAlignment: CrossAxisAlignment.end,
        children: [
          if (!isMine) _avatar(),
          const SizedBox(width: 8),
          Flexible(
            child: Column(
              crossAxisAlignment:
                  isMine ? CrossAxisAlignment.end : CrossAxisAlignment.start,
              children: [
                if (!isMine)
                  Padding(
                    padding: const EdgeInsets.only(left: 4, bottom: 3),
                    child: Text(
                      message.senderUserId,
                      style: const TextStyle(fontSize: 11, color: Color(0xFF8B919C)),
                    ),
                  ),
                if (message.reply != null) _replyBlock(textColor),
                if (_isImage)
                  _imageBubble(textColor, radius)
                else if (_isAudio)
                  _audioBubble(textColor, radius)
                else
                  GestureDetector(
                    onLongPress: onLongPress,
                    child: Container(
                      constraints: const BoxConstraints(maxWidth: 260),
                      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 9),
                      decoration: BoxDecoration(color: bubbleColor, borderRadius: radius),
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.end,
                        children: [
                          if (message.body.isNotEmpty)
                            SelectableText.rich(
                              _bodySpan(message.body, textColor),
                              style: TextStyle(fontSize: 15, color: textColor, height: 1.4),
                            ),
                          if (message.file != null)
                            Text(
                              '📎 ${message.file!.name}',
                              style: TextStyle(fontSize: 13, color: textColor),
                            ),
                          const SizedBox(height: 2),
                          Text(
                            _formatTime(message.createdAt),
                            style: TextStyle(fontSize: 10, color: textColor.withValues(alpha: 0.6)),
                          ),
                        ],
                      ),
                    ),
                  ),
              ],
            ),
          ),
          if (isMine) const SizedBox(width: 8),
          if (isMine) _avatar(),
        ],
      ),
    );
  }

  Widget _avatar() {
    return Container(
      width: 34,
      height: 34,
      decoration: BoxDecoration(color: _avatarColor(message.senderUserId), shape: BoxShape.circle),
      alignment: Alignment.center,
      child: Text(
        message.senderUserId.isEmpty ? '?' : message.senderUserId[0].toUpperCase(),
        style: const TextStyle(color: Colors.white, fontSize: 15, fontWeight: FontWeight.w600),
      ),
    );
  }

  Color _avatarColor(String id) {
    const colors = [
      Color(0xFF5B8FF9),
      Color(0xFF61C766),
      Color(0xFFF6BD16),
      Color(0xFFE86452),
      Color(0xFF9A60B4),
      Color(0xFF2F9E9B),
    ];
    if (id.isEmpty) return colors[0];
    return colors[id.codeUnitAt(0) % colors.length];
  }

  Widget _replyBlock(Color textColor) {
    return Container(
      margin: const EdgeInsets.only(bottom: 4),
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 6),
      decoration: BoxDecoration(
        color: textColor.withValues(alpha: 0.08),
        borderRadius: BorderRadius.circular(8),
        border: Border(left: BorderSide(color: textColor.withValues(alpha: 0.4), width: 3)),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            message.reply!.senderUserId,
            style: TextStyle(fontSize: 11, fontWeight: FontWeight.w600, color: textColor.withValues(alpha: 0.8)),
          ),
          Text(
            message.reply!.body,
            maxLines: 2,
            overflow: TextOverflow.ellipsis,
            style: TextStyle(fontSize: 12, color: textColor.withValues(alpha: 0.6)),
          ),
        ],
      ),
    );
  }

  Widget _audioBubble(Color textColor, BorderRadius radius) {
    final file = message.file!;
    final url = 'http://${client.host}:${client.port}/api/files/${file.fileId}';
    return GestureDetector(
      onLongPress: onLongPress,
      child: Container(
        constraints: const BoxConstraints(maxWidth: 220, minWidth: 120),
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 8),
        decoration: BoxDecoration(
          color: isMine ? const Color(0xFF2B6BFF) : const Color(0xFF2B2D33),
          borderRadius: radius,
        ),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            ValueListenableBuilder<bool>(
              valueListenable: AudioCtl.playing,
              builder: (context, playing, _) {
                final isThis = playing && AudioCtl.currentUrl == url;
                return IconButton(
                  visualDensity: VisualDensity.compact,
                  onPressed: () => AudioCtl.toggle(url),
                  icon: Icon(
                    isThis ? Icons.pause_circle_filled : Icons.play_circle_filled,
                    color: textColor,
                    size: 30,
                  ),
                );
              },
            ),
            const SizedBox(width: 4),
            Flexible(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Text(
                    '语音 ${_formatTime(message.createdAt)}',
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(fontSize: 12, color: textColor.withValues(alpha: 0.9)),
                  ),
                  Text(
                    file.name,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: TextStyle(fontSize: 10, color: textColor.withValues(alpha: 0.6)),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _imageBubble(Color textColor, BorderRadius radius) {
    final file = message.file!;
    final url = 'http://${client.host}:${client.port}/api/files/${file.fileId}';
    return GestureDetector(
      onTap: onTap,
      onLongPress: onLongPress,
      child: Container(
        decoration: BoxDecoration(
          color: isMine ? const Color(0xFF2B6BFF) : const Color(0xFF2B2D33),
          borderRadius: radius,
        ),
        padding: const EdgeInsets.all(4),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.end,
          children: [
            ClipRRect(
              borderRadius: BorderRadius.circular(10),
              child: Image.network(
                url,
                width: 180,
                fit: BoxFit.cover,
                loadingBuilder: (context, child, progress) {
                  if (progress == null) return child;
                  return Container(
                    width: 180,
                    height: 120,
                    color: const Color(0xFF1B1D22),
                    child: const Center(
                      child: SizedBox(
                        width: 20,
                        height: 20,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      ),
                    ),
                  );
                },
                errorBuilder: (context, error, stack) => Container(
                  width: 180,
                  height: 90,
                  color: const Color(0xFF1B1D22),
                  alignment: Alignment.center,
                  child: Text(
                    '图片加载失败\n${file.name}',
                    style: const TextStyle(fontSize: 12, color: Color(0xFF8B919C)),
                  ),
                ),
              ),
            ),
            Padding(
              padding: const EdgeInsets.only(left: 8, right: 2, top: 2),
              child: Text(
                _formatTime(message.createdAt),
                style: TextStyle(fontSize: 10, color: Colors.white.withValues(alpha: 0.6)),
              ),
            ),
          ],
        ),
      ),
    );
  }
}
