import 'package:flutter/services.dart';

/// HarmonyOS 的低时延短音效通道。
///
/// 起因：audioplayers 在鸿蒙上播放前会调用
/// `setAudioSessionScene(AUDIO_SESSION_SCENE_MEDIA)` 与 `activateAudioSession`，
/// 这是应用级的音频会话切换，会压制同一应用里 TRTC 的通话流——提示音一响就
/// 听不到其他玩家说话。把并发模式改成 `CONCURRENCY_MIX_WITH_OTHERS` 真机实测
/// 无效，说明致命的是会话切换本身，不是并发模式。
///
/// 原生侧改用 SoundPool：系统为「急促简短的音效」提供的低时延通道，完全不经过
/// AudioSessionManager，并且 usage 取 `STREAM_USAGE_MUSIC` 时为混音模式，
/// 按官方说明不会打断其他音频播放。
class NativeTableSound {
  const NativeTableSound._();

  static const _channel = MethodChannel(
    'com.texas.game.poker_client/table_sound',
  );

  /// 播放一段已落盘的短音效。
  ///
  /// 通道不可用（其他平台、旧版本宿主）时安静返回：提示音是附属功能，
  /// 任何失败都不能影响牌局，更不能回退到会掐断通话的播放路径。
  static Future<void> play({
    required String filePath,
    required double volume,
  }) async {
    try {
      await _channel.invokeMethod<void>('play', {
        'path': filePath,
        'volume': volume.clamp(0.0, 1.0),
      });
    } on Object {
      // 安静跳过这一声。
    }
  }
}
