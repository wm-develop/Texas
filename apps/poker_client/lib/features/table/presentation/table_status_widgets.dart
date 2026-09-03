import 'dart:async';

import 'package:flutter/material.dart';
import 'package:poker_client/core/network/game_socket_client.dart';
import 'package:poker_client/core/platform/voice_chat_service.dart';
import 'package:poker_client/features/lobby/domain/friend_room.dart';

/// 牌桌周边状态组件：房间信息、连接状态与语音控制。
/// 手机布局把这些放在侧边栏，桌面布局放在顶部。

class TableRoomHeader extends StatelessWidget {
  const TableRoomHeader({
    required this.room,
    required this.currentPlayers,
    required this.onLeave,
    required this.onSettings,
    required this.onShowResult,
    required this.onToggleChat,
    this.unreadChatCount = 0,
    this.compact = false,
    super.key,
  });

  final FriendRoom room;
  final int currentPlayers;
  final Future<void> Function() onLeave;
  final VoidCallback onSettings;

  /// 打开本房间战绩窗口。
  final VoidCallback onShowResult;

  /// 打开/关闭文字聊天。做成图标而不是整条按钮，是为了把右栏的竖向空间
  /// 让给下注区——平板横屏时那点高度很紧张。
  final VoidCallback onToggleChat;
  final int unreadChatCount;
  final bool compact;

  @override
  Widget build(BuildContext context) {
    if (compact) {
      return Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Text(
            '好友牌桌',
            style: TextStyle(fontSize: 16, fontWeight: FontWeight.w800),
          ),
          Text(
            '$currentPlayers/10 人',
            style: const TextStyle(
              color: Color(0xFFF6D986),
              fontWeight: FontWeight.w700,
            ),
          ),
          const SizedBox(height: 5),
          Text(
            '房间 ${room.code}',
            style: const TextStyle(color: Colors.white70, fontSize: 12),
          ),
          Text(
            '盲注 ${room.rules.smallBlind}/${room.rules.bigBlind}',
            style: const TextStyle(color: Colors.white60, fontSize: 12),
          ),
          Row(
            children: [
              IconButton(
                onPressed: onLeave,
                visualDensity: VisualDensity.compact,
                constraints: const BoxConstraints.tightFor(
                  width: 34,
                  height: 34,
                ),
                icon: const Icon(Icons.exit_to_app, size: 19),
                tooltip: '离开房间',
              ),
              IconButton(
                key: const ValueKey('chat-toggle-button'),
                onPressed: onToggleChat,
                visualDensity: VisualDensity.compact,
                constraints: const BoxConstraints.tightFor(
                  width: 34,
                  height: 34,
                ),
                icon: Badge(
                  isLabelVisible: unreadChatCount > 0,
                  label: Text(unreadChatCount > 99 ? '99+' : '$unreadChatCount'),
                  child: const Icon(Icons.chat_bubble_outline, size: 19),
                ),
                tooltip: '文字聊天',
              ),
              IconButton(
                key: const ValueKey('room-result-button'),
                onPressed: onShowResult,
                visualDensity: VisualDensity.compact,
                constraints: const BoxConstraints.tightFor(
                  width: 34,
                  height: 34,
                ),
                icon: const Icon(Icons.leaderboard_outlined, size: 19),
                tooltip: '本房间战绩',
              ),
              IconButton(
                onPressed: onSettings,
                visualDensity: VisualDensity.compact,
                constraints: const BoxConstraints.tightFor(
                  width: 34,
                  height: 34,
                ),
                icon: const Icon(Icons.settings_outlined, size: 19),
                tooltip: '声音与语音设置',
              ),
            ],
          ),
        ],
      );
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Text(
              '好友牌桌 · $currentPlayers/10 人',
              style: const TextStyle(fontSize: 22, fontWeight: FontWeight.w700),
            ),
            const SizedBox(width: 8),
            IconButton(
              onPressed: onLeave,
              icon: const Icon(Icons.exit_to_app, size: 20),
              tooltip: '离开房间',
            ),
            IconButton(
              key: const ValueKey('chat-toggle-button'),
              onPressed: onToggleChat,
              icon: Badge(
                isLabelVisible: unreadChatCount > 0,
                label: Text(unreadChatCount > 99 ? '99+' : '$unreadChatCount'),
                child: const Icon(Icons.chat_bubble_outline, size: 20),
              ),
              tooltip: '文字聊天',
            ),
            IconButton(
              key: const ValueKey('room-result-button'),
              onPressed: onShowResult,
              icon: const Icon(Icons.leaderboard_outlined, size: 20),
              tooltip: '本房间战绩',
            ),
            IconButton(
              onPressed: onSettings,
              icon: const Icon(Icons.settings_outlined, size: 20),
              tooltip: '声音与语音设置',
            ),
          ],
        ),
        const SizedBox(height: 2),
        Text(
          '房间码 ${room.code}  ·  盲注 ${room.rules.smallBlind}/${room.rules.bigBlind}',
          style: const TextStyle(color: Colors.white60),
        ),
      ],
    );
  }
}

class TableConnectionStatusBar extends StatelessWidget {
  const TableConnectionStatusBar({
    required this.client,
    this.compact = false,
    super.key,
  });

  final GameSocketClient client;
  final bool compact;

  @override
  Widget build(BuildContext context) {
    final draining = client.snapshot?.draining == true;
    final (label, color) = switch (client.status) {
      // 优雅停机期间连接仍然正常，但要让玩家知道为什么不开新局
      GameSocketStatus.joined when draining => (
        '服务器即将更新，本手结束后暂停',
        Colors.orangeAccent,
      ),
      GameSocketStatus.disconnected => ('服务端未连接', Colors.white54),
      GameSocketStatus.connecting => ('正在连接', Colors.orangeAccent),
      GameSocketStatus.connected => ('服务端已连接', const Color(0xFF6DE0A4)),
      GameSocketStatus.authenticated => ('身份已验证', const Color(0xFF6DE0A4)),
      GameSocketStatus.joined => ('牌桌已同步', const Color(0xFF6DE0A4)),
      GameSocketStatus.reconnecting => ('正在恢复牌桌', Colors.orangeAccent),
      GameSocketStatus.failed => ('连接失败', Colors.redAccent),
    };

    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Container(
          width: 8,
          height: 8,
          decoration: BoxDecoration(color: color, shape: BoxShape.circle),
        ),
        const SizedBox(width: 7),
        if (compact)
          Expanded(
            child: Text(
              label,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(fontSize: 12),
            ),
          )
        else
          Text(label),
        SizedBox(width: compact ? 2 : 8),
        if (client.status == GameSocketStatus.connected ||
            client.status == GameSocketStatus.authenticated ||
            client.status == GameSocketStatus.joined)
          if (compact)
            IconButton(
              onPressed: client.sendPing,
              tooltip: client.lastMessageType ?? '测试连接',
              visualDensity: VisualDensity.compact,
              constraints: const BoxConstraints.tightFor(width: 30, height: 30),
              icon: const Icon(Icons.sync, size: 17),
            )
          else
            TextButton(
              onPressed: client.sendPing,
              child: Text(client.lastMessageType ?? '测试连接'),
            )
        else if (compact)
          IconButton(
            onPressed: client.connect,
            tooltip: '连接',
            visualDensity: VisualDensity.compact,
            constraints: const BoxConstraints.tightFor(width: 30, height: 30),
            icon: const Icon(Icons.refresh, size: 17),
          )
        else
          TextButton(onPressed: client.connect, child: const Text('连接')),
      ],
    );
  }
}

class TableVoiceControls extends StatelessWidget {
  const TableVoiceControls({
    required this.voiceJoined,
    required this.connectionState,
    required this.microphoneEnabled,
    required this.operationInProgress,
    required this.speakingCount,
    required this.members,
    required this.speakingUserIds,
    required this.mutedUserIds,
    required this.currentUserId,
    required this.displayName,
    required this.onJoinChanged,
    required this.onMicrophoneChanged,
    required this.onUserMuted,
    this.compact = false,
    super.key,
  });

  final bool voiceJoined;
  final VoiceConnectionState connectionState;
  final bool microphoneEnabled;
  final bool operationInProgress;
  final int speakingCount;
  final List<TableVoiceMember> members;
  final Set<String> speakingUserIds;
  final Set<String> mutedUserIds;
  final String currentUserId;
  final String displayName;
  final ValueChanged<bool> onJoinChanged;
  final ValueChanged<bool> onMicrophoneChanged;
  final void Function(String userId, bool muted) onUserMuted;
  final bool compact;

  @override
  Widget build(BuildContext context) {
    final joinChip = FilterChip(
      selected: voiceJoined,
      onSelected: operationInProgress ? null : onJoinChanged,
      avatar: Icon(voiceJoined ? Icons.headset : Icons.headset_off, size: 18),
      label: Text(
        compact
            ? switch (connectionState) {
                VoiceConnectionState.disconnected => '语音',
                VoiceConnectionState.connecting => '加入中…',
                VoiceConnectionState.connected => '已入语音',
                VoiceConnectionState.reconnecting => '重连中…',
              }
            : switch (connectionState) {
                VoiceConnectionState.disconnected => '加入语音',
                VoiceConnectionState.connecting => '正在加入…',
                VoiceConnectionState.connected => '已加入语音',
                VoiceConnectionState.reconnecting => '语音重连中…',
              },
      ),
    );
    final microphoneChip = FilterChip(
      selected: microphoneEnabled,
      onSelected: voiceJoined && !operationInProgress
          ? onMicrophoneChanged
          : null,
      avatar: Icon(microphoneEnabled ? Icons.mic : Icons.mic_off, size: 18),
      label: Text(
        compact
            ? (microphoneEnabled ? '已开麦' : '麦克风')
            : (microphoneEnabled ? '自由麦已开启' : '麦克风关闭'),
      ),
    );
    final memberButton = PopupMenuButton<String>(
      tooltip: '语音成员',
      onSelected: (userId) =>
          onUserMuted(userId, !mutedUserIds.contains(userId)),
      icon: Badge(
        label: Text('${members.length}'),
        child: const Icon(Icons.groups_2_outlined, size: 20),
      ),
      itemBuilder: (context) => members.isEmpty
          ? const [
              PopupMenuItem<String>(enabled: false, child: Text('还没有人加入语音')),
            ]
          : [
              for (final member in members)
                PopupMenuItem<String>(
                  value: member.userId,
                  enabled: voiceJoined && member.userId != currentUserId,
                  child: Row(
                    children: [
                      Icon(
                        member.microphoneEnabled ? Icons.mic : Icons.mic_off,
                        size: 17,
                        color:
                            speakingUserIds.contains(member.userId) &&
                                !mutedUserIds.contains(member.userId)
                            ? const Color(0xFF6DE0A4)
                            : Colors.white54,
                      ),
                      const SizedBox(width: 8),
                      Expanded(child: Text(member.displayName)),
                      if (member.userId == currentUserId)
                        const Text('自己', style: TextStyle(fontSize: 11))
                      else if (mutedUserIds.contains(member.userId))
                        const Row(
                          children: [
                            Icon(Icons.volume_off, size: 15),
                            SizedBox(width: 3),
                            Text('已屏蔽', style: TextStyle(fontSize: 11)),
                          ],
                        )
                      else if (speakingUserIds.contains(member.userId))
                        const Text(
                          '说话中',
                          style: TextStyle(
                            color: Color(0xFF6DE0A4),
                            fontSize: 11,
                          ),
                        )
                      else
                        const Text('点击屏蔽', style: TextStyle(fontSize: 11)),
                    ],
                  ),
                ),
            ],
    );
    if (compact) {
      return Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Wrap(spacing: 4, runSpacing: 4, children: [joinChip, microphoneChip]),
          Row(
            children: [
              Expanded(
                child: Text(
                  '$speakingCount 人说话',
                  style: const TextStyle(color: Colors.white60, fontSize: 11),
                ),
              ),
              memberButton,
            ],
          ),
        ],
      );
    }
    return Row(
      children: [
        joinChip,
        const SizedBox(width: 8),
        microphoneChip,
        const SizedBox(width: 8),
        Text(
          '$displayName · $speakingCount 人说话',
          style: const TextStyle(color: Colors.white60, fontSize: 12),
        ),
        const SizedBox(width: 4),
        memberButton,
      ],
    );
  }
}
