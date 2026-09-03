import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:tencent_rtc_sdk/trtc_cloud.dart';
import 'package:tencent_rtc_sdk/trtc_cloud_def.dart';
import 'package:tencent_rtc_sdk/trtc_cloud_listener.dart';
import 'package:tencent_rtc_sdk/tx_device_manager.dart';

import 'microphone_permission.dart';
import 'voice_chat_service.dart';

class TrtcVoiceChatService implements VoiceChatService {
  TrtcVoiceChatService({MicrophonePermission? microphonePermission})
    : _microphonePermission =
          microphonePermission ?? SystemMicrophonePermission();

  static const _roomOperationTimeout = Duration(seconds: 15);
  static const _speakingVolumeThreshold = 10;

  final MicrophonePermission _microphonePermission;
  final _connectionController =
      StreamController<VoiceConnectionState>.broadcast(sync: true);
  final _speakingController = StreamController<Set<String>>.broadcast(
    sync: true,
  );

  TRTCCloud? _cloud;
  TRTCCloudListener? _listener;
  Completer<void>? _joinCompleter;
  Completer<void>? _leaveCompleter;
  Completer<void>? _roleCompleter;
  VoiceConnectionState _state = VoiceConnectionState.disconnected;
  String _localUserId = '';
  bool _joined = false;
  bool _microphoneEnabled = false;
  bool _disposed = false;

  @override
  Stream<VoiceConnectionState> get connectionState =>
      _connectionController.stream;

  @override
  Stream<Set<String>> get speakingUserIds => _speakingController.stream;

  @override
  Future<void> joinTableChannel({
    required int sdkAppId,
    required String tableId,
    required String userId,
    required String userSig,
  }) async {
    _ensureUsable();
    if (_state != VoiceConnectionState.disconnected) {
      throw StateError('Voice service is already joining or joined.');
    }
    if (sdkAppId <= 0 ||
        tableId.trim().isEmpty ||
        userId.trim().isEmpty ||
        userSig.trim().isEmpty) {
      throw ArgumentError('TRTC room credentials are incomplete.');
    }

    _localUserId = userId;
    _setState(VoiceConnectionState.connecting);
    final cloud = _cloud ??= await TRTCCloud.sharedInstance();
    _listener ??= _createListener();
    cloud.registerListener(_listener!);
    cloud.enableAudioVolumeEvaluation(
      true,
      TRTCAudioVolumeEvaluateParams(interval: 300, enableVadDetection: true),
    );

    final completer = Completer<void>();
    _joinCompleter = completer;
    cloud.enterRoom(
      TRTCParams(
        sdkAppId: sdkAppId,
        userId: userId,
        userSig: userSig,
        strRoomId: tableId,
        role: _usesCallScene ? TRTCRoleType.anchor : TRTCRoleType.audience,
      ),
      _usesCallScene ? TRTCAppScene.audioCall : TRTCAppScene.voiceChatRoom,
    );

    try {
      await completer.future.timeout(_roomOperationTimeout);
    } on Object {
      cloud.exitRoom();
      _joined = false;
      _setState(VoiceConnectionState.disconnected);
      rethrow;
    } finally {
      if (identical(_joinCompleter, completer)) _joinCompleter = null;
    }
  }

  @override
  Future<void> leaveTableChannel() async {
    if (_disposed || _state == VoiceConnectionState.disconnected) return;
    _microphoneEnabled = false;
    _cloud?.stopLocalAudio();

    final completer = Completer<void>();
    _leaveCompleter = completer;
    _cloud?.exitRoom();
    try {
      await completer.future.timeout(_roomOperationTimeout);
    } on TimeoutException {
      _finishLeaving();
    } finally {
      if (identical(_leaveCompleter, completer)) _leaveCompleter = null;
    }
  }

  @override
  Future<void> setMicrophoneEnabled(bool enabled) async {
    _ensureUsable();
    if (!_joined) throw StateError('Join the voice room first.');
    if (_microphoneEnabled == enabled) return;

    if (enabled) {
      final granted = await _microphonePermission.request();
      if (!granted) {
        throw const MicrophonePermissionDeniedException();
      }
      if (!_usesCallScene) await _switchRole(TRTCRoleType.anchor);
      _cloud?.startLocalAudio(TRTCAudioQuality.defaultMode);
    } else {
      _cloud?.stopLocalAudio();
      if (!_usesCallScene) await _switchRole(TRTCRoleType.audience);
    }
    _pinSpeakerAudioRoute();
    _microphoneEnabled = enabled;
  }

  /// HarmonyOS 使用「音频通话」场景而不是「语音聊天室」场景，并且不切换角色。
  ///
  /// 依据腾讯云 TRTC 文档：默认音量类型为 Auto，「麦上用户（视频通话场景中的
  /// 所有用户，低延时直播场景下的主播和连麦观众）使用通话音量；麦下用户
  /// （低延时直播场景下的普通观众）使用媒体音量」。语音聊天室属于直播场景，
  /// 观众→主播的角色切换会让系统音量类型在媒体音量与通话音量之间来回切换。
  /// 真机上的表现正是：开麦后音量条先显示话筒（通话音量）并能听到远端，
  /// 几秒后变为喇叭（媒体音量）且远端静音。
  ///
  /// Flutter SDK 13.4.3 没有暴露 setSystemVolumeType，无法直接固定音量类型；
  /// 改用音频通话场景后所有人都是「麦上用户」，全程通话音量，不再有切换。
  /// Android 与 Windows 保持已验证的语音聊天室方案不变。
  bool get _usesCallScene => defaultTargetPlatform == TargetPlatform.ohos;

  /// HarmonyOS switches the playback route to the low-volume earpiece shortly
  /// after the local microphone starts (audience → anchor), which sounds like
  /// remote audio dying entirely. Re-pin the loudspeaker route after every
  /// role change there. Android and Windows keep their validated default
  /// behaviour untouched.
  void _pinSpeakerAudioRoute() {
    if (defaultTargetPlatform != TargetPlatform.ohos) return;
    try {
      _cloud?.getDeviceManager().setAudioRoute(TXAudioRoute.speakerPhone);
    } on Object {
      // Best effort: the audio route is a playback convenience and must never
      // break joining or microphone toggling.
    }
  }

  @override
  Future<void> setRemoteUserMuted(String userId, bool muted) async {
    _ensureUsable();
    if (!_joined) throw StateError('Join the voice room first.');
    if (userId.trim().isEmpty) throw ArgumentError.value(userId, 'userId');
    _cloud?.muteRemoteAudio(userId, muted);
  }

  @override
  Future<void> setPlaybackVolume(double volume) async {
    _ensureUsable();
    _cloud?.setAudioPlayoutVolume((volume.clamp(0, 1) * 100).round());
  }

  @override
  Future<void> dispose() async {
    if (_disposed) return;
    await leaveTableChannel();
    final listener = _listener;
    if (listener != null) _cloud?.unRegisterListener(listener);
    _disposed = true;
    await _connectionController.close();
    await _speakingController.close();
  }

  TRTCCloudListener _createListener() {
    return TRTCCloudListener(
      onEnterRoom: (result) {
        final completer = _joinCompleter;
        if (result >= 0) {
          _joined = true;
          _setState(VoiceConnectionState.connected);
          _pinSpeakerAudioRoute();
          if (completer != null && !completer.isCompleted) completer.complete();
          return;
        }
        _joined = false;
        _setState(VoiceConnectionState.disconnected);
        if (completer != null && !completer.isCompleted) {
          completer.completeError(VoiceRoomException(result));
        }
      },
      onExitRoom: (_) {
        _finishLeaving();
        final completer = _leaveCompleter;
        if (completer != null && !completer.isCompleted) completer.complete();
      },
      onSwitchRole: (code, message) {
        final completer = _roleCompleter;
        if (completer == null || completer.isCompleted) return;
        if (code == 0) {
          completer.complete();
        } else {
          completer.completeError(VoiceRoomException(code, message));
        }
      },
      onError: (code, message) {
        final completer = _joinCompleter;
        if (completer != null && !completer.isCompleted) {
          completer.completeError(VoiceRoomException(code, message));
        }
        final roleCompleter = _roleCompleter;
        if (roleCompleter != null && !roleCompleter.isCompleted) {
          roleCompleter.completeError(VoiceRoomException(code, message));
        }
      },
      onConnectionLost: () => _setState(VoiceConnectionState.reconnecting),
      onTryToReconnect: () => _setState(VoiceConnectionState.reconnecting),
      onConnectionRecovery: () => _setState(VoiceConnectionState.connected),
      onUserVoiceVolume: (volumes, _) {
        final speaking = <String>{};
        for (final info in volumes) {
          if (info.vad != 1 && info.volume < _speakingVolumeThreshold) continue;
          final userId = info.userId.isEmpty ? _localUserId : info.userId;
          if (userId.isNotEmpty) speaking.add(userId);
        }
        if (!_speakingController.isClosed) {
          _speakingController.add(Set.unmodifiable(speaking));
        }
      },
    );
  }

  void _finishLeaving() {
    _joined = false;
    _microphoneEnabled = false;
    _localUserId = '';
    if (!_speakingController.isClosed) {
      _speakingController.add(const <String>{});
    }
    _setState(VoiceConnectionState.disconnected);
  }

  Future<void> _switchRole(TRTCRoleType role) async {
    final cloud = _cloud;
    if (cloud == null) throw StateError('TRTC is not initialized.');
    final completer = Completer<void>();
    _roleCompleter = completer;
    cloud.switchRole(role);
    try {
      await completer.future.timeout(_roomOperationTimeout);
    } finally {
      if (identical(_roleCompleter, completer)) _roleCompleter = null;
    }
  }

  void _setState(VoiceConnectionState state) {
    if (_state == state) return;
    _state = state;
    if (!_connectionController.isClosed) _connectionController.add(state);
  }

  void _ensureUsable() {
    if (_disposed) throw StateError('Voice service has been disposed.');
  }
}
