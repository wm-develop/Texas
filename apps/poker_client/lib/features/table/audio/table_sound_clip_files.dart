import 'dart:io';

import 'package:path_provider/path_provider.dart';
import 'package:poker_client/features/table/audio/table_action_sound_tracker.dart';
import 'package:poker_client/features/table/audio/table_sound_effects.dart';

/// 把内存里生成的提示音落成 WAV 文件。
///
/// RTC 引擎按文件路径播放，而提示音是运行时算出来的字节，所以需要先写盘。
/// 只在 HarmonyOS 语音进行中用到：那条路径把提示音交给 TRTC 播放，避免普通
/// 音频插件把整个应用的音频会话切成媒体场景、压制通话流。
///
/// 写盘失败一律返回 null，调用方安静地跳过这次提示音——绝不能因此回退到普通
/// 音频插件，那正是会掐掉远端语音的路径。
class TableSoundClipFiles {
  TableSoundClipFiles({Future<Directory> Function()? directory})
    : _directory = directory ?? getTemporaryDirectory;

  final Future<Directory> Function() _directory;
  final Map<TableSoundEffect, String> _paths = {};
  final Map<TableSoundEffect, Future<String?>> _pending = {};

  /// 该音效对应的文件路径，首次调用时写盘。同一音效并发调用只写一次。
  Future<String?> pathFor(TableSoundEffect effect) {
    final ready = _paths[effect];
    if (ready != null) return Future.value(ready);
    return _pending[effect] ??= _materialise(effect).whenComplete(() {
      _pending.remove(effect);
    });
  }

  Future<String?> _materialise(TableSoundEffect effect) async {
    try {
      final directory = await _directory();
      final file = File('${directory.path}/table_sound_${effect.name}.wav');
      if (!file.existsSync()) {
        await file.writeAsBytes(TableSoundEffects.clipBytes(effect));
      }
      final path = file.path;
      _paths[effect] = path;
      return path;
    } on Object {
      return null;
    }
  }
}
