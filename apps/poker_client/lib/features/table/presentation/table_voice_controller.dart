import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:poker_client/core/network/trtc_credential_client.dart';
import 'package:poker_client/core/platform/voice_chat_service.dart';

/// 牌桌语音状态与命令的控制器。
///
/// 从牌桌页抽出的目的是把「屏蔽与静音两个来源如何合成远端静音」「加入语音后
/// 如何补齐既有静音」这类容易写错的规则放到可以单独测试的位置。控制器只负责
/// 状态与 RTC 调用，不接触 BuildContext：错误通过 [onError] 交回页面展示。
class TableVoiceController extends ChangeNotifier {
  TableVoiceController({
    required VoiceChatService voiceChat,
    required Future<TrtcCredentials> Function() issueCredentials,
    required String currentUserId,
    required void Function({
      required bool joined,
      required bool microphoneEnabled,
    })
    onStateChanged,
    required void Function(Object error) onError,
    required double Function() playbackVolume,
  }) : _voiceChat = voiceChat,
       _issueCredentials = issueCredentials,
       _currentUserId = currentUserId,
       _onStateChanged = onStateChanged,
       _onError = onError,
       _playbackVolume = playbackVolume {
    _connectionSubscription = _voiceChat.connectionState.listen((state) {
      _connectionState = state;
      if (state == VoiceConnectionState.disconnected) {
        _microphoneEnabled = false;
        _speakingUserIds = const {};
      }
      _notify();
    });
    _speakingSubscription = _voiceChat.speakingUserIds.listen((userIds) {
      _speakingUserIds = userIds;
      _notify();
    });
  }

  final VoiceChatService _voiceChat;
  final Future<TrtcCredentials> Function() _issueCredentials;
  final String _currentUserId;
  final void Function({required bool joined, required bool microphoneEnabled})
  _onStateChanged;
  final void Function(Object error) _onError;
  final double Function() _playbackVolume;

  late final StreamSubscription<VoiceConnectionState> _connectionSubscription;
  late final StreamSubscription<Set<String>> _speakingSubscription;

  VoiceConnectionState _connectionState = VoiceConnectionState.disconnected;
  bool _microphoneEnabled = false;
  bool _operationInProgress = false;
  Set<String> _speakingUserIds = const {};
  final Set<String> _blockedUserIds = {};
  final Set<String> _mutedUserIds = {};
  bool _disposed = false;

  VoiceConnectionState get connectionState => _connectionState;

  /// 重连期间仍视为已加入，避免界面在短暂抖动时反复切换。
  bool get joined =>
      _connectionState == VoiceConnectionState.connected ||
      _connectionState == VoiceConnectionState.reconnecting;

  bool get microphoneEnabled => _microphoneEnabled;

  bool get operationInProgress => _operationInProgress;

  /// RTC 上报的正在说话者，未扣除本地静音。
  Set<String> get speakingUserIds => _speakingUserIds;

  Set<String> get blockedUserIds => _blockedUserIds;

  Set<String> get mutedUserIds => _mutedUserIds;

  /// 本地实际能听见的说话者：被静音的人不应显示说话动效，也不计入人数。
  Set<String> get audibleSpeakingUserIds =>
      _speakingUserIds.difference(_mutedUserIds);

  bool isSpeaking(String userId) =>
      _speakingUserIds.contains(userId) && !_mutedUserIds.contains(userId);

  /// 远端是否应当被静音：屏蔽和语音静音是两个独立来源，任一成立即静音，
  /// 因此取消其中一个时不能把另一个也解除。
  bool _shouldMute(String userId) =>
      _blockedUserIds.contains(userId) || _mutedUserIds.contains(userId);

  Future<void> setJoined(bool value) async {
    if (_operationInProgress) return;
    _operationInProgress = true;
    _notify();
    try {
      if (value) {
        final credentials = await _issueCredentials();
        await _voiceChat.joinTableChannel(
          sdkAppId: credentials.sdkAppId,
          tableId: credentials.roomId,
          userId: credentials.userId,
          userSig: credentials.userSig,
        );
        await _voiceChat.setPlaybackVolume(_playbackVolume());
        // 加入后补齐进入语音前累积的屏蔽与静音，否则这些人会突然可听见。
        for (final userId in {..._blockedUserIds, ..._mutedUserIds}) {
          await _voiceChat.setRemoteUserMuted(userId, true);
        }
        _onStateChanged(joined: true, microphoneEnabled: _microphoneEnabled);
      } else {
        await _voiceChat.leaveTableChannel();
        _onStateChanged(joined: false, microphoneEnabled: false);
      }
    } on Object catch (error) {
      _onError(error);
    } finally {
      _operationInProgress = false;
      _notify();
    }
  }

  Future<void> setMicrophoneEnabled(bool value) async {
    if (!joined || _operationInProgress) return;
    _operationInProgress = true;
    _notify();
    try {
      await _voiceChat.setMicrophoneEnabled(value);
      _microphoneEnabled = value;
      _onStateChanged(joined: true, microphoneEnabled: value);
    } on Object catch (error) {
      _onError(error);
    } finally {
      _operationInProgress = false;
      _notify();
    }
  }

  /// 屏蔽同时影响聊天与语音；自己不能屏蔽自己。
  Future<void> setUserBlocked(String userId, bool blocked) =>
      _applyRemoteMute(userId, blocked, _blockedUserIds);

  /// 只静音语音，不影响聊天可见性。
  Future<void> setUserMuted(String userId, bool muted) =>
      _applyRemoteMute(userId, muted, _mutedUserIds);

  Future<void> _applyRemoteMute(
    String userId,
    bool enabled,
    Set<String> target,
  ) async {
    if (userId == _currentUserId) return;
    if (enabled) {
      target.add(userId);
    } else {
      target.remove(userId);
    }
    _notify();
    if (!joined) return;
    try {
      await _voiceChat.setRemoteUserMuted(userId, _shouldMute(userId));
    } on Object catch (error) {
      _onError(error);
    }
  }

  /// 把当前播放音量同步给 RTC；未加入语音时无事发生。
  Future<void> applyPlaybackVolume() async {
    if (!joined) return;
    try {
      await _voiceChat.setPlaybackVolume(_playbackVolume());
    } on Object catch (error) {
      _onError(error);
    }
  }

  void _notify() {
    if (_disposed) return;
    notifyListeners();
  }

  @override
  void dispose() {
    _disposed = true;
    unawaited(_connectionSubscription.cancel());
    unawaited(_speakingSubscription.cancel());
    unawaited(_voiceChat.dispose());
    super.dispose();
  }
}

/// 把语音相关异常翻译为可直接展示的中文提示。
String voiceErrorMessage(Object error) => switch (error) {
  MicrophonePermissionDeniedException() => '需要麦克风权限才能开启自由麦',
  TrtcCredentialException(statusCode: 401) => '语音测试凭证鉴权失败',
  TrtcCredentialException() => '获取语音凭证失败，请检查本地服务',
  VoiceRoomException(:final code) => '加入语音房失败（错误码 $code）',
  TimeoutException() => '语音服务连接超时',
  UnsupportedError() => '当前平台的语音适配尚未完成',
  _ => '语音操作失败',
};
