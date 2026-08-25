import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:shared_preferences/shared_preferences.dart';

class AppSettingsController extends ChangeNotifier {
  static const _soundKey = 'texas.sound_enabled';
  static const _voiceVolumeKey = 'texas.voice_volume';
  static const _autoJoinVoiceKey = 'texas.auto_join_voice';

  SharedPreferences? _preferences;

  bool _ready = false;
  bool _soundEnabled = true;
  double _voiceVolume = 0.8;
  bool _autoJoinVoice = false;

  bool get ready => _ready;
  bool get soundEnabled => _soundEnabled;
  double get voiceVolume => _voiceVolume;
  bool get autoJoinVoice => _autoJoinVoice;

  Future<void> load() async {
    try {
      final preferences = await SharedPreferences.getInstance();
      _preferences = preferences;
      _soundEnabled = preferences.getBool(_soundKey) ?? true;
      _voiceVolume = (preferences.getDouble(_voiceVolumeKey) ?? 0.8)
          .clamp(0, 1)
          .toDouble();
      _autoJoinVoice = preferences.getBool(_autoJoinVoiceKey) ?? false;
    } on Object {
      // A platform without a preferences plugin still gets session settings.
    }
    _ready = true;
    notifyListeners();
  }

  void setSoundEnabled(bool value) {
    if (_soundEnabled == value) return;
    _soundEnabled = value;
    notifyListeners();
    unawaited(_save(() => _preferences?.setBool(_soundKey, value)));
  }

  void setVoiceVolume(double value) {
    final normalized = value.clamp(0, 1).toDouble();
    if (_voiceVolume == normalized) return;
    _voiceVolume = normalized;
    notifyListeners();
    unawaited(
      _save(() => _preferences?.setDouble(_voiceVolumeKey, normalized)),
    );
  }

  void setAutoJoinVoice(bool value) {
    if (_autoJoinVoice == value) return;
    _autoJoinVoice = value;
    notifyListeners();
    unawaited(_save(() => _preferences?.setBool(_autoJoinVoiceKey, value)));
  }

  Future<void> _save(Future<bool>? Function() operation) async {
    try {
      await operation();
    } on Object {
      // Keep the in-memory value when persistence is unavailable.
    }
  }
}
