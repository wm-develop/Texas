import 'package:flutter/material.dart';
import 'package:poker_client/core/settings/app_settings.dart';

Future<void> showAppSettingsDialog(
  BuildContext context,
  AppSettingsController settings,
) => showDialog<void>(
  context: context,
  builder: (context) => AlertDialog(
    title: const Text('声音与语音设置'),
    content: SizedBox(
      width: 420,
      child: AnimatedBuilder(
        animation: settings,
        builder: (context, _) => Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            SwitchListTile(
              contentPadding: EdgeInsets.zero,
              title: const Text('牌局提示音'),
              subtitle: const Text('轮到你行动和牌局结算时播放提示'),
              value: settings.soundEnabled,
              onChanged: settings.setSoundEnabled,
            ),
            ListTile(
              contentPadding: EdgeInsets.zero,
              title: const Text('语音播放音量'),
              subtitle: Slider(
                value: settings.voiceVolume,
                divisions: 10,
                label: '${(settings.voiceVolume * 100).round()}%',
                onChanged: settings.setVoiceVolume,
              ),
              trailing: SizedBox(
                width: 44,
                child: Text('${(settings.voiceVolume * 100).round()}%'),
              ),
            ),
            SwitchListTile(
              contentPadding: EdgeInsets.zero,
              title: const Text('进入牌桌后自动加入语音'),
              subtitle: const Text('只加入收听，不会自动开启麦克风'),
              value: settings.autoJoinVoice,
              onChanged: settings.setAutoJoinVoice,
            ),
          ],
        ),
      ),
    ),
    actions: [
      FilledButton(
        onPressed: () => Navigator.of(context).pop(),
        child: const Text('完成'),
      ),
    ],
  ),
);
