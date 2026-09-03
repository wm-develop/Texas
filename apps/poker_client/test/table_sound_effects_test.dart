import 'dart:io';

import 'package:audioplayers/audioplayers.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/features/table/audio/table_action_sound_tracker.dart';
import 'package:poker_client/features/table/audio/table_sound_clip_files.dart';
import 'package:poker_client/features/table/audio/table_sound_effects.dart';

/// 记录提示音走了哪条出声路径。测试不构造真实播放器：它会注册持续的帧
/// 回调，测试环境里跑不完。
class _Recorder {
  final viaPlugin = <TableSoundEffect>[];
  final viaVoiceSession = <(int, String, double)>[];
  final viaNativeSoundPool = <(String, double)>[];
  final contexts = <AudioContext>[];

  TableSoundEffects effects({
    required TargetPlatform platform,
    required bool voiceActive,
    bool voiceOutputAvailable = true,
    HarmonyVoiceSoundStrategy strategy =
        HarmonyVoiceSoundStrategy.nativeSoundPool,
    Future<String?> Function(TableSoundEffect effect)? clipFilePath,
  }) => TableSoundEffects(
    platform: platform,
    voiceSessionActive: () => voiceActive,
    harmonyVoiceStrategy: strategy,
    configureAudio: (context) async => contexts.add(context),
    playClip: (effect, _) async => viaPlugin.add(effect),
    playNative: (filePath, volume) async =>
        viaNativeSoundPool.add((filePath, volume)),
    clipFilePath:
        clipFilePath ??
        (voiceOutputAvailable
            ? (effect) async => '/tmp/${effect.name}.wav'
            : null),
    playInVoiceSession: voiceOutputAvailable
        ? (id, filePath, volume) async =>
              viaVoiceSession.add((id, filePath, volume))
        : null,
  );
}

void main() {
  group('鸿蒙语音进行中的提示音', () {
    test('默认策略：语音中交给原生 SoundPool，完全不碰普通插件', () async {
      // audioplayers 在鸿蒙上播放前会切换应用级音频会话，压制 TRTC 的通话流。
      // 只把并发模式改成混音真机实测无效，致命的是会话切换本身。SoundPool
      // 不经过 AudioSessionManager，且是系统给短音效的低时延通道。
      final recorder = _Recorder();
      final effects = recorder.effects(
        platform: TargetPlatform.ohos,
        voiceActive: true,
      );

      await effects.play(TableSoundEffect.chips);
      await effects.play(TableSoundEffect.allIn);

      expect(recorder.viaPlugin, isEmpty, reason: '语音中绝不能走普通音频插件');
      expect(recorder.viaVoiceSession, isEmpty);
      expect(recorder.contexts, isEmpty, reason: '不走插件就不必动它的音频上下文');
      expect(recorder.viaNativeSoundPool, [
        ('/tmp/chips.wav', 0.75),
        ('/tmp/allIn.wav', 0.9),
      ]);
    });

    test('原生通道拿不到文件时安静跳过，绝不回退到普通音频插件', () async {
      final recorder = _Recorder();
      final effects = recorder.effects(
        platform: TargetPlatform.ohos,
        voiceActive: true,
        clipFilePath: (_) async => null,
      );

      await effects.play(TableSoundEffect.chips);

      expect(recorder.viaPlugin, isEmpty);
      expect(recorder.viaNativeSoundPool, isEmpty);
    });

    test('改用 RTC 通道策略时走通话流，带正确的音效 ID 与音量', () async {
      final recorder = _Recorder();
      final effects = recorder.effects(
        platform: TargetPlatform.ohos,
        voiceActive: true,
        strategy: HarmonyVoiceSoundStrategy.throughVoiceEngine,
      );

      await effects.play(TableSoundEffect.chips);
      await effects.play(TableSoundEffect.allIn);

      expect(recorder.viaPlugin, isEmpty);
      final ids = recorder.viaVoiceSession.map((call) => call.$1).toSet();
      expect(ids, hasLength(2), reason: '每种音效一个固定 ID，避免互相打断');
      expect(
        recorder.viaVoiceSession.first.$2,
        '/tmp/chips.wav',
      );
      expect(recorder.viaVoiceSession.last.$3, 0.9);
    });

    test('静音策略下语音中完全不出声', () async {
      final recorder = _Recorder();
      final effects = recorder.effects(
        platform: TargetPlatform.ohos,
        voiceActive: true,
        strategy: HarmonyVoiceSoundStrategy.silent,
      );

      await effects.play(TableSoundEffect.chips);

      expect(recorder.viaPlugin, isEmpty);
      expect(recorder.viaVoiceSession, isEmpty);
      expect(recorder.viaNativeSoundPool, isEmpty);
      expect(recorder.contexts, isEmpty);
    });

    test('混音策略仍可切换：走普通插件并先解除独占（真机实测不足以救活语音）', () async {
      final recorder = _Recorder();
      final effects = recorder.effects(
        platform: TargetPlatform.ohos,
        voiceActive: true,
        strategy: HarmonyVoiceSoundStrategy.mixWithVoice,
      );

      await effects.play(TableSoundEffect.chips);

      expect(recorder.viaPlugin, [TableSoundEffect.chips]);
      expect(
        recorder.contexts.single.ohos.audioFocus,
        AudioContextConfigFocus.mixWithOthers,
      );
    });

    test('鸿蒙未加入语音：照常走普通音频插件', () async {
      final recorder = _Recorder();
      final effects = recorder.effects(
        platform: TargetPlatform.ohos,
        voiceActive: false,
      );

      await effects.play(TableSoundEffect.chips);

      expect(recorder.viaPlugin, [TableSoundEffect.chips]);
      expect(recorder.viaVoiceSession, isEmpty);
      expect(recorder.viaNativeSoundPool, isEmpty);
    });

    test('其他平台即使在语音里也照常走普通音频插件', () async {
      // Android 与 Windows 已验证可以一边语音一边提示音，不能被改动
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

        expect(recorder.viaPlugin, [TableSoundEffect.allIn], reason: '$platform');
        expect(recorder.viaVoiceSession, isEmpty, reason: '$platform');
        expect(recorder.viaNativeSoundPool, isEmpty, reason: '$platform');
      }
    });

    test('RTC 通道策略下通道不可用时安静跳过，绝不回退到普通音频插件', () async {
      // 该策略的前提就是不碰普通插件，宁可这一声不响
      final recorder = _Recorder();
      final effects = recorder.effects(
        platform: TargetPlatform.ohos,
        voiceActive: true,
        strategy: HarmonyVoiceSoundStrategy.throughVoiceEngine,
        voiceOutputAvailable: false,
      );

      await effects.play(TableSoundEffect.chips);

      expect(recorder.viaPlugin, isEmpty);
      expect(recorder.viaVoiceSession, isEmpty);
    });

    test('落盘失败或抛错时同样安静跳过', () async {
      for (final clipFilePath
          in <Future<String?> Function(TableSoundEffect)>[
            (_) async => null,
            (_) async => throw const FileSystemException('no space'),
          ]) {
        final recorder = _Recorder();
        final effects = recorder.effects(
          platform: TargetPlatform.ohos,
          voiceActive: true,
          strategy: HarmonyVoiceSoundStrategy.throughVoiceEngine,
          clipFilePath: clipFilePath,
        );

        await effects.play(TableSoundEffect.chips);

        expect(recorder.viaPlugin, isEmpty);
        expect(recorder.viaVoiceSession, isEmpty);
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

      expect(recorder.viaPlugin, isEmpty);
    });
  });

  group('全局音频并发策略', () {
    test('鸿蒙上把焦点策略设为混音，且只设置一次', () async {
      // 必须在出声之前设好，而且只设一次
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

  group('提示音落盘', () {
    late Directory directory;

    setUp(() async {
      directory = await Directory.systemTemp.createTemp('table_sound_test');
    });
    tearDown(() async {
      if (directory.existsSync()) await directory.delete(recursive: true);
    });

    test('写出可播放的 WAV，并且同一音效只写一次', () async {
      var calls = 0;
      final files = TableSoundClipFiles(
        directory: () async {
          calls++;
          return directory;
        },
      );

      final first = await files.pathFor(TableSoundEffect.chips);
      final second = await files.pathFor(TableSoundEffect.chips);

      expect(first, isNotNull);
      expect(second, first);
      expect(calls, 1, reason: '路径应被缓存，不必反复解析目录');
      final bytes = await File(first!).readAsBytes();
      expect(String.fromCharCodes(bytes.sublist(0, 4)), 'RIFF');
      expect(String.fromCharCodes(bytes.sublist(8, 12)), 'WAVE');
      expect(bytes, TableSoundEffects.clipBytes(TableSoundEffect.chips));
    });

    test('不同音效写到不同文件', () async {
      final files = TableSoundClipFiles(directory: () async => directory);

      final chips = await files.pathFor(TableSoundEffect.chips);
      final fold = await files.pathFor(TableSoundEffect.fold);

      expect(chips, isNot(fold));
    });

    test('目录不可用时返回 null 而不是抛错', () async {
      final files = TableSoundClipFiles(
        directory: () async => throw const FileSystemException('unavailable'),
      );

      expect(await files.pathFor(TableSoundEffect.chips), isNull);
    });
  });
}
