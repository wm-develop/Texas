import 'package:audioplayers/audioplayers.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/features/table/audio/table_action_sound_tracker.dart';
import 'package:poker_client/features/table/audio/table_sound_effects.dart';

/// 记录每一次真正到达播放出口的提示音。测试不构造真实播放器：它会注册
/// 持续的帧回调，测试环境里跑不完。
class _Recorder {
  final played = <TableSoundEffect>[];
  final contexts = <AudioContext>[];

  TableSoundEffects effects({
    required TargetPlatform platform,
    required bool voiceActive,
  }) => TableSoundEffects(
    platform: platform,
    voiceSessionActive: () => voiceActive,
    configureGlobalAudio: (context) async => contexts.add(context),
    playClip: (effect, _) async => played.add(effect),
  );
}

void main() {
  group('HarmonyOS 语音进行中让位提示音', () {
    test('鸿蒙 + 语音进行中：完全不走播放路径', () async {
      // 鸿蒙音频焦点是应用级的：audioplayers 播放前会把会话场景设成 MEDIA
      // 并以 CONCURRENCY_DEFAULT 激活音频会话，该模式独占物理输出通道并
      // 压制应用内其他音频流，于是每有玩家动作播一声提示音，系统音量类型
      // 就被改成媒体音量（音量条话筒变喇叭）、TRTC 通话流被压制。
      final recorder = _Recorder();
      final effects = recorder.effects(
        platform: TargetPlatform.ohos,
        voiceActive: true,
      );

      await effects.play(TableSoundEffect.chips);
      await effects.play(TableSoundEffect.allIn);

      expect(recorder.played, isEmpty, reason: '语音进行中不能触发任何音频播放');
      expect(
        recorder.contexts,
        isEmpty,
        reason: '让位时连全局音频上下文都不该去碰',
      );
    });

    test('鸿蒙 + 未加入语音：照常播放', () async {
      final recorder = _Recorder();
      final effects = recorder.effects(
        platform: TargetPlatform.ohos,
        voiceActive: false,
      );

      await effects.play(TableSoundEffect.chips);

      expect(recorder.played, [TableSoundEffect.chips]);
    });

    test('其他平台即使在语音里也照常播放', () async {
      // Android 与 Windows 已验证可用，不能因为这个鸿蒙专属问题被削弱
      for (final platform in const [
        TargetPlatform.android,
        TargetPlatform.windows,
      ]) {
        final recorder = _Recorder();
        final effects = recorder.effects(
          platform: platform,
          voiceActive: true,
        );

        await effects.play(TableSoundEffect.allIn);

        expect(
          recorder.played,
          [TableSoundEffect.allIn],
          reason: '$platform 的提示音不应被改动',
        );
      }
    });

    test('已释放后不再出声', () async {
      final recorder = _Recorder();
      final effects = recorder.effects(
        platform: TargetPlatform.android,
        voiceActive: false,
      );
      await effects.dispose();

      await effects.play(TableSoundEffect.chips);

      expect(recorder.played, isEmpty);
    });
  });

  group('全局音频并发策略', () {
    test('鸿蒙上把焦点策略设为混音，且只设置一次', () async {
      final recorder = _Recorder();
      final effects = recorder.effects(
        platform: TargetPlatform.ohos,
        voiceActive: false,
      );

      await effects.play(TableSoundEffect.chips);
      await effects.play(TableSoundEffect.fold);

      expect(recorder.contexts, hasLength(1));
      expect(
        recorder.contexts.single.ohos.audioFocus,
        AudioContextConfigFocus.mixWithOthers,
        reason: '默认的独占模式会压制应用内的 TRTC 通话流',
      );
    });

    test('其他平台不改动全局音频上下文', () async {
      final recorder = _Recorder();
      final effects = recorder.effects(
        platform: TargetPlatform.android,
        voiceActive: false,
      );

      await effects.play(TableSoundEffect.chips);

      expect(recorder.contexts, isEmpty);
    });
  });
}
