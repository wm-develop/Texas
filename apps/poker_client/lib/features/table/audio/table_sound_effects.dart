import 'dart:math' as math;
import 'dart:typed_data';

import 'package:audioplayers/audioplayers.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:poker_client/features/table/audio/table_action_sound_tracker.dart';

class TableSoundEffects {
  TableSoundEffects({
    AudioPlayer? player,
    bool Function()? voiceSessionActive,
    TargetPlatform? platform,
    Future<void> Function(AudioContext context)? configureGlobalAudio,
    Future<void> Function(TableSoundEffect effect, double volume)? playClip,
  }) : _injectedPlayer = player,
       _voiceSessionActive = voiceSessionActive ?? _neverActive,
       _platform = platform ?? defaultTargetPlatform,
       _configureGlobalAudio =
           configureGlobalAudio ?? AudioPlayer.global.setAudioContext,
       _injectedPlayClip = playClip;

  static final Map<TableSoundEffect, Uint8List> _clips = {
    for (final effect in TableSoundEffect.values) effect: _buildClip(effect),
  };

  static bool _neverActive() => false;

  final AudioPlayer? _injectedPlayer;
  final bool Function() _voiceSessionActive;
  final TargetPlatform _platform;
  final Future<void> Function(AudioContext context) _configureGlobalAudio;
  final Future<void> Function(TableSoundEffect effect, double volume)?
  _injectedPlayClip;
  AudioPlayer? _createdPlayer;
  bool _disposed = false;
  bool _globalAudioConfigured = false;

  /// 真实播放器按需创建：它会注册持续的帧回调，测试注入播放出口后就不该
  /// 被构造出来。
  AudioPlayer get _player =>
      _injectedPlayer ?? (_createdPlayer ??= AudioPlayer());

  /// HarmonyOS 上语音进行中必须放弃提示音。
  ///
  /// 鸿蒙的音频焦点是应用级的：audioplayers 的 FocusManager 在每次播放前会
  /// 调用 setAudioSessionScene(AUDIO_SESSION_SCENE_MEDIA) 并以
  /// CONCURRENCY_DEFAULT 激活音频会话，而该模式会独占输出通道、压制应用内
  /// 其他音频流。TRTC 的通话流和提示音在同一个应用里共用这一套策略，于是
  /// 每次有人动作播一声提示音，系统音量类型就被改成媒体音量（音量条从话筒
  /// 变喇叭），远端语音随之被压制。插件没有把这两个调用开放给 Dart 侧配置，
  /// 唯一可靠的办法是语音进行中根本不走这条播放路径。
  ///
  /// 只影响 HarmonyOS 且只在语音进行中；不在语音里、以及其他平台照常播放。
  bool get _suppressedByVoiceSession =>
      _platform == TargetPlatform.ohos && _voiceSessionActive();

  /// 第二道保险：把鸿蒙的应用级并发策略从独占改成混音。
  ///
  /// 插件默认用 CONCURRENCY_DEFAULT 激活音频会话，该模式「独占物理输出通道
  /// 并压制其他音频」。改成 CONCURRENCY_MIX_WITH_OTHERS 后提示音与通话流
  /// 混音播放，即使某条路径绕过了上面的让位判断也不会掐掉远端语音。
  /// 只在 HarmonyOS 上设置，不改动其他平台已验证的行为。
  Future<void> _ensureGlobalAudioContext() async {
    if (_globalAudioConfigured || _platform != TargetPlatform.ohos) return;
    _globalAudioConfigured = true;
    try {
      await _configureGlobalAudio(
        AudioContextConfig(
          focus: AudioContextConfigFocus.mixWithOthers,
        ).build(),
      );
    } on Object {
      // 配置失败不能影响出声；让位判断本身已经覆盖了主要场景。
    }
  }

  Future<void> play(TableSoundEffect effect) async {
    if (_disposed || _suppressedByVoiceSession) return;
    await _ensureGlobalAudioContext();
    if (_disposed) return;
    final volume = effect == TableSoundEffect.allIn ? 0.9 : 0.75;
    final playClip = _injectedPlayClip;
    try {
      if (playClip != null) {
        await playClip(effect, volume);
        return;
      }
      await _player.stop();
      await _player.play(
        BytesSource(_clips[effect]!, mimeType: 'audio/wav'),
        volume: volume,
      );
    } on Object {
      // Keep action feedback available if a platform audio backend is
      // temporarily unavailable.
      await SystemSound.play(
        effect == TableSoundEffect.allIn
            ? SystemSoundType.alert
            : SystemSoundType.click,
      );
    }
  }

  Future<void> dispose() async {
    _disposed = true;
    await _injectedPlayer?.dispose();
    await _createdPlayer?.dispose();
  }
}

Uint8List _buildClip(TableSoundEffect effect) {
  const sampleRate = 22050;
  final duration = switch (effect) {
    TableSoundEffect.chips => 0.24,
    TableSoundEffect.allIn => 0.68,
    TableSoundEffect.check => 0.36,
    TableSoundEffect.fold => 0.3,
    TableSoundEffect.praise => 0.5,
    TableSoundEffect.taunt => 0.46,
  };
  final samples = Float64List((sampleRate * duration).ceil());
  var noiseState = 0x13579BDF;

  double noise() {
    noiseState = (1664525 * noiseState + 1013904223) & 0x7FFFFFFF;
    return noiseState / 0x3FFFFFFF - 1;
  }

  void mixBurst({
    required double start,
    required double length,
    required double Function(double time, double envelope) sample,
  }) {
    final first = (start * sampleRate).round();
    final count = (length * sampleRate).round();
    for (
      var index = 0;
      index < count && first + index < samples.length;
      index++
    ) {
      final time = index / sampleRate;
      final envelope = math.exp(-7.5 * time / length);
      samples[first + index] += sample(time, envelope);
    }
  }

  void mixChip(double start, {double pitch = 1}) {
    mixBurst(
      start: start,
      length: 0.07,
      sample: (time, envelope) =>
          envelope *
          (0.33 * math.sin(2 * math.pi * 2100 * pitch * time) +
              0.2 * math.sin(2 * math.pi * 3150 * pitch * time) +
              0.12 * noise()),
    );
  }

  void mixKnock(double start) {
    mixBurst(
      start: start,
      length: 0.105,
      sample: (time, envelope) =>
          envelope *
          (0.62 * math.sin(2 * math.pi * 145 * time) +
              0.24 * math.sin(2 * math.pi * 310 * time) +
              0.1 * noise()),
    );
  }

  void mixTone(double start, double length, double frequency) {
    mixBurst(
      start: start,
      length: length,
      sample: (time, envelope) =>
          envelope * 0.25 * math.sin(2 * math.pi * frequency * time),
    );
  }

  switch (effect) {
    case TableSoundEffect.chips:
      mixChip(0, pitch: 0.95);
      mixChip(0.052, pitch: 1.08);
      mixChip(0.108, pitch: 0.88);
    case TableSoundEffect.allIn:
      for (var index = 0; index < 7; index++) {
        mixChip(index * 0.038, pitch: 0.82 + index * 0.055);
      }
      mixTone(0.29, 0.25, 440);
      mixTone(0.39, 0.27, 660);
    case TableSoundEffect.check:
      mixKnock(0.015);
      mixKnock(0.18);
    case TableSoundEffect.fold:
      mixBurst(
        start: 0,
        length: 0.2,
        sample: (time, envelope) {
          final sweep = 1350 - 900 * (time / 0.2);
          return envelope *
              (0.18 * noise() + 0.12 * math.sin(2 * math.pi * sweep * time));
        },
      );
      mixBurst(
        start: 0.17,
        length: 0.08,
        sample: (time, envelope) =>
            envelope *
            (0.34 * math.sin(2 * math.pi * 220 * time) + 0.12 * noise()),
      );
    case TableSoundEffect.praise:
      mixTone(0, 0.2, 523.25);
      mixTone(0.1, 0.24, 659.25);
      mixTone(0.22, 0.26, 783.99);
      mixChip(0.3, pitch: 1.25);
      mixChip(0.36, pitch: 1.45);
    case TableSoundEffect.taunt:
      mixTone(0, 0.18, 330);
      mixTone(0.11, 0.2, 247);
      mixTone(0.23, 0.22, 196);
      mixKnock(0.31);
  }

  var peak = 0.0;
  for (final sample in samples) {
    peak = math.max(peak, sample.abs());
  }
  final scale = peak > 0.92 ? 0.92 / peak : 1.0;
  final pcm = Int16List(samples.length);
  for (var index = 0; index < samples.length; index++) {
    pcm[index] = (samples[index] * scale * 32767).round().clamp(-32768, 32767);
  }
  return _encodeWave(pcm, sampleRate);
}

Uint8List _encodeWave(Int16List samples, int sampleRate) {
  const headerSize = 44;
  final dataLength = samples.length * 2;
  final bytes = ByteData(headerSize + dataLength);

  void writeText(int offset, String value) {
    for (var index = 0; index < value.length; index++) {
      bytes.setUint8(offset + index, value.codeUnitAt(index));
    }
  }

  writeText(0, 'RIFF');
  bytes.setUint32(4, 36 + dataLength, Endian.little);
  writeText(8, 'WAVE');
  writeText(12, 'fmt ');
  bytes.setUint32(16, 16, Endian.little);
  bytes.setUint16(20, 1, Endian.little);
  bytes.setUint16(22, 1, Endian.little);
  bytes.setUint32(24, sampleRate, Endian.little);
  bytes.setUint32(28, sampleRate * 2, Endian.little);
  bytes.setUint16(32, 2, Endian.little);
  bytes.setUint16(34, 16, Endian.little);
  writeText(36, 'data');
  bytes.setUint32(40, dataLength, Endian.little);
  for (var index = 0; index < samples.length; index++) {
    bytes.setInt16(headerSize + index * 2, samples[index], Endian.little);
  }
  return bytes.buffer.asUint8List();
}
