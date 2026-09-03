import 'package:flutter/material.dart';
import 'package:poker_client/core/settings/app_settings.dart';

Future<void> showAppSettingsDialog(
  BuildContext context,
  AppSettingsController settings, {
  VoidCallback? onOpenAdmin,
  VoidCallback? onOpenRoomManagement,
}) => showDialog<void>(
  context: context,
  builder: (context) {
    // 手机横屏可用高度很小，条目多了一屏放不下；限高并滚动，
    // 不能让内容溢出或被裁掉。
    final maxHeight = MediaQuery.sizeOf(context).height * 0.7;
    return AlertDialog(
      title: const Text('声音与语音设置'),
      content: SizedBox(
        width: 420,
        child: ConstrainedBox(
          constraints: BoxConstraints(maxHeight: maxHeight),
          child: AnimatedBuilder(
            animation: settings,
            builder: (context, _) => SingleChildScrollView(
              child: Column(
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
                  if (onOpenRoomManagement != null) ...[
                    const Divider(),
                    ListTile(
                      key: const ValueKey('settings-room-management'),
                      contentPadding: EdgeInsets.zero,
                      leading: const Icon(Icons.meeting_room_outlined),
                      title: const Text('房间管理'),
                      subtitle: const Text('开关房间入口、把玩家移出房间'),
                      trailing: const Icon(Icons.chevron_right),
                      onTap: () {
                        Navigator.of(context).pop();
                        onOpenRoomManagement();
                      },
                    ),
                  ],
                  if (onOpenAdmin != null) ...[
                    const Divider(),
                    ListTile(
                      contentPadding: EdgeInsets.zero,
                      leading: const Icon(Icons.admin_panel_settings_outlined),
                      title: const Text('打开服务器管理'),
                      subtitle: const Text('管理账号、密码和新用户注册开关'),
                      trailing: const Icon(Icons.chevron_right),
                      onTap: () {
                        Navigator.of(context).pop();
                        onOpenAdmin();
                      },
                    ),
                  ],
                ],
              ),
            ),
          ),
        ),
      ),
      actions: [
        FilledButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('完成'),
        ),
      ],
    );
  },
);
