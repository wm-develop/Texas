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
  final contexts = <AudioContext>[];

  TableSoundEffects effects({
    required TargetPlatform platform,
    required bool voiceActive,
    bool voiceOutputAvailable = true,
    Future<String?> Function(TableSoundEffect effect)? clipFilePath,
  }) => TableSoundEffects(
    platform: platform,
    voiceSessionActive: () => voiceActive,
    configureGlobalAudio: (context) async => contexts.add(context),
    playClip: (effect, _) async => viaPlugin.add(effect),
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
  group('鸿蒙语音进行中改走 RTC 音频通道', () {
    test('语音进行中：提示音交给 RTC 播放，不碰普通音频插件', () async {
      // 鸿蒙音频焦点是应用级的：audioplayers 播放前会把会话场景设成 MEDIA
      // 并以 CONCURRENCY_DEFAULT 激活音频会话，独占输出并压制应用内其他
      // 音频流，于是提示音一响，TRTC 通话流就被压制、音量条从话筒变喇叭。
      // 交给 RTC 引擎播放后声音走通话流，语音与提示音得以并存。
      final recorder = _Recorder();
      final effects = recorder.effects(
        platform: TargetPlatform.ohos,
        voiceActive: true,
      );

      await effects.play(TableSoundEffect.chips);

      expect(recorder.viaPlugin, isEmpty, reason: '语音中绝不能走普通音频插件');
      expect(
        recorder.contexts,
        isEmpty,
        reason: '走 RTC 通道时不该去动全局音频上下文',
      );
      expect(recorder.viaVoiceSession, hasLength(1));
      final (id, path, volume) = recorder.viaVoiceSession.single;
      expect(id, TableSoundEffects.musicId(TableSoundEffect.chips));
      expect(path, '/tmp/chips.wav');
      expect(volume, 0.75);
    });

    test('每种提示音有各自的音效 ID，连续触发不会互相打断', () async {
      final recorder = _Recorder();
      final effects = recorder.effects(
        platform: TargetPlatform.ohos,
        voiceActive: true,
      );

      await effects.play(TableSoundEffect.chips);
      await effects.play(TableSoundEffect.allIn);

      final ids = recorder.viaVoiceSession.map((call) => call.$1).toSet();
      expect(ids, hasLength(2));
      // All in 的音量更高，与普通插件路径保持一致
      expect(recorder.viaVoiceSession.last.$3, 0.9);
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
      }
    });

    test('RTC 通道不可用时安静跳过，绝不回退到普通音频插件', () async {
      // 回退才是会掐掉远端语音的路径，宁可这一声不响
      final recorder = _Recorder();
      final effects = recorder.effects(
        platform: TargetPlatform.ohos,
        voiceActive: true,
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
