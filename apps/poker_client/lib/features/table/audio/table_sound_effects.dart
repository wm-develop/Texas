import 'dart:math' as math;
import 'dart:typed_data';

import 'package:audioplayers/audioplayers.dart';
import 'package:flutter/services.dart';
import 'package:poker_client/features/table/audio/table_action_sound_tracker.dart';

class TableSoundEffects {
  TableSoundEffects({AudioPlayer? player}) : _player = player ?? AudioPlayer();

  static final Map<TableSoundEffect, Uint8List> _clips = {
    for (final effect in TableSoundEffect.values) effect: _buildClip(effect),
  };

  final AudioPlayer _player;
  bool _disposed = false;

  Future<void> play(TableSoundEffect effect) async {
    if (_disposed) return;
    try {
      await _player.stop();
      await _player.play(
        BytesSource(_clips[effect]!, mimeType: 'audio/wav'),
        volume: effect == TableSoundEffect.allIn ? 0.9 : 0.75,
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
    await _player.dispose();
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
