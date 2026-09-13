import 'package:flutter/material.dart';
import 'package:video_player/video_player.dart';

/// 视频全屏播放页（黑底 + 播放/暂停 + 进度）。
class VideoPlayerScreen extends StatefulWidget {
  final String url;
  final String name;

  const VideoPlayerScreen({super.key, required this.url, required this.name});

  @override
  State<VideoPlayerScreen> createState() => _VideoPlayerScreenState();
}

class _VideoPlayerScreenState extends State<VideoPlayerScreen> {
  late VideoPlayerController _controller;
  bool _ready = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _controller = VideoPlayerController.networkUrl(Uri.parse(widget.url));
    _controller.initialize().then((_) {
      if (!mounted) return;
      setState(() => _ready = true);
      _controller.play();
    }).catchError((Object e) {
      if (!mounted) return;
      setState(() => _error = e.toString());
    });
    _controller.addListener(_onTick);
  }

  void _onTick() {
    if (mounted) setState(() {});
  }

  @override
  void dispose() {
    _controller.removeListener(_onTick);
    _controller.dispose();
    super.dispose();
  }

  String _fmt(Duration d) {
    String two(int v) => v.toString().padLeft(2, '0');
    final m = d.inMinutes;
    final s = d.inSeconds % 60;
    return '${two(m)}:${two(s)}';
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      backgroundColor: Colors.black,
      appBar: AppBar(
        backgroundColor: Colors.black,
        foregroundColor: Colors.white,
        title: Text(widget.name, style: const TextStyle(fontSize: 14)),
      ),
      body: Center(
        child: _error != null
            ? Text('播放失败\n$_error',
                textAlign: TextAlign.center,
                style: const TextStyle(color: Colors.white54, fontSize: 13))
            : !_ready
                ? const CircularProgressIndicator(color: Colors.white54)
                : GestureDetector(
                    onTap: () => setState(() {
                      _controller.value.isPlaying ? _controller.pause() : _controller.play();
                    }),
                    child: AspectRatio(
                      aspectRatio: _controller.value.aspectRatio,
                      child: Stack(
                        alignment: Alignment.center,
                        children: [
                          VideoPlayer(_controller),
                          if (!_controller.value.isPlaying)
                            const Icon(Icons.play_circle_fill, color: Colors.white70, size: 64),
                          Positioned(
                            left: 0,
                            right: 0,
                            bottom: 0,
                            child: Container(
                              color: Colors.black54,
                              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 4),
                              child: Row(
                                children: [
                                  Text(
                                    '${_fmt(_controller.value.position)} / ${_fmt(_controller.value.duration)}',
                                    style: const TextStyle(color: Colors.white, fontSize: 11),
                                  ),
                                  const Spacer(),
                                  if (!_controller.value.isPlaying)
                                    GestureDetector(
                                      onTap: () => _controller.seekTo(Duration.zero),
                                      child: const Text('重播',
                                          style: TextStyle(color: Colors.white70, fontSize: 12)),
                                    ),
                                ],
                              ),
                            ),
                          ),
                        ],
                      ),
                    ),
                  ),
      ),
    );
  }
}
