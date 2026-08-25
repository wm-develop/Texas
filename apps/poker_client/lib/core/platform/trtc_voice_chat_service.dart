import 'dart:async';

import 'package:tencent_rtc_sdk/trtc_cloud.dart';
import 'package:tencent_rtc_sdk/trtc_cloud_def.dart';
import 'package:tencent_rtc_sdk/trtc_cloud_listener.dart';

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
        role: TRTCRoleType.audience,
      ),
      TRTCAppScene.voiceChatRoom,
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
      await _switchRole(TRTCRoleType.anchor);
      _cloud?.startLocalAudio(TRTCAudioQuality.defaultMode);
    } else {
      _cloud?.stopLocalAudio();
      await _switchRole(TRTCRoleType.audience);
    }
    _microphoneEnabled = enabled;
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
