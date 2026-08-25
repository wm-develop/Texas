import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/core/settings/app_settings.dart';
import 'package:shared_preferences/shared_preferences.dart';

void main() {
  test('loads and updates local sound and voice settings', () async {
    SharedPreferences.setMockInitialValues({
      'texas.sound_enabled': false,
      'texas.voice_volume': 0.4,
      'texas.auto_join_voice': true,
    });
    final settings = AppSettingsController();

    await settings.load();

    expect(settings.soundEnabled, isFalse);
    expect(settings.voiceVolume, 0.4);
    expect(settings.autoJoinVoice, isTrue);
    settings
      ..setSoundEnabled(true)
      ..setVoiceVolume(0.7)
      ..setAutoJoinVoice(false);
    expect(settings.soundEnabled, isTrue);
    expect(settings.voiceVolume, 0.7);
    expect(settings.autoJoinVoice, isFalse);
  });
}
