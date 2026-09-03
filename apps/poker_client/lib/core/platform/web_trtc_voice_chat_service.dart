import 'dart:async';
import 'dart:convert';
import 'dart:js_interop';

import 'voice_chat_service.dart';

@JS('TexasTrtcBridge.setEventSink')
external void _setEventSink(JSFunction callback);

@JS('TexasTrtcBridge.clearEventSink')
external void _clearEventSink();

@JS('TexasTrtcBridge.join')
external JSPromise<JSAny?> _join(
  JSNumber sdkAppId,
  JSString tableId,
  JSString userId,
  JSString userSig,
);

@JS('TexasTrtcBridge.leave')
external JSPromise<JSAny?> _leave();

@JS('TexasTrtcBridge.setMicrophoneEnabled')
external JSPromise<JSAny?> _setMicrophoneEnabled(JSBoolean enabled);

@JS('TexasTrtcBridge.setRemoteUserMuted')
external JSPromise<JSAny?> _setRemoteUserMuted(
  JSString userId,
  JSBoolean muted,
);

@JS('TexasTrtcBridge.setPlaybackVolume')
external JSPromise<JSAny?> _setPlaybackVolume(JSNumber volume);

class WebTrtcVoiceChatService implements VoiceChatService {
  WebTrtcVoiceChatService() {
    _eventSink = _handleBridgeEvent.toJS;
    _setEventSink(_eventSink);
  }

  static const _operationTimeout = Duration(seconds: 15);

  final _connectionController =
      StreamController<VoiceConnectionState>.broadcast(sync: true);
  final _speakingController = StreamController<Set<String>>.broadcast(
    sync: true,
  );

  late final JSFunction _eventSink;
  VoiceConnectionState _state = VoiceConnectionState.disconnected;
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

    _setState(VoiceConnectionState.connecting);
    try {
      await _join(
        sdkAppId.toJS,
        tableId.toJS,
        userId.toJS,
        userSig.toJS,
      ).toDart.timeout(_operationTimeout);
      _joined = true;
      _setState(VoiceConnectionState.connected);
    } on TimeoutException {
      _setState(VoiceConnectionState.disconnected);
      rethrow;
    } on Object catch (error) {
      _setState(VoiceConnectionState.disconnected);
      throw _mapBridgeError(error);
    }
  }

  @override
  Future<void> leaveTableChannel() async {
    if (_disposed || _state == VoiceConnectionState.disconnected) return;
    try {
      await _leave().toDart.timeout(_operationTimeout);
    } on TimeoutException {
      // The browser may be closing; local state still needs to be released.
    } on Object catch (error) {
      throw _mapBridgeError(error);
    } finally {
      _joined = false;
      _microphoneEnabled = false;
      _emitSpeaking(const <String>{});
      _setState(VoiceConnectionState.disconnected);
    }
  }

  @override
  Future<void> setMicrophoneEnabled(bool enabled) async {
    _ensureUsable();
    if (!_joined) throw StateError('Join the voice room first.');
    if (_microphoneEnabled == enabled) return;

    try {
      await _setMicrophoneEnabled(
        enabled.toJS,
      ).toDart.timeout(_operationTimeout);
      _microphoneEnabled = enabled;
    } on TimeoutException {
      rethrow;
    } on Object catch (error) {
      throw _mapBridgeError(error, microphoneOperation: enabled);
    }
  }

  @override
  Future<void> setRemoteUserMuted(String userId, bool muted) async {
    _ensureUsable();
    if (!_joined) throw StateError('Join the voice room first.');
    if (userId.trim().isEmpty) throw ArgumentError.value(userId, 'userId');

    try {
      await _setRemoteUserMuted(
        userId.toJS,
        muted.toJS,
      ).toDart.timeout(_operationTimeout);
    } on TimeoutException {
      rethrow;
    } on Object catch (error) {
      throw _mapBridgeError(error);
    }
  }

  @override
  Future<void> playLocalEffect({
    required int id,
    required String filePath,
    required double volume,
  }) async {
    // 该冲突是 HarmonyOS 独有的，本平台无需在语音通道内播提示音。
  }

  @override
  Future<void> setPlaybackVolume(double volume) async {
    _ensureUsable();
    try {
      await _setPlaybackVolume(
        volume.clamp(0, 1).toDouble().toJS,
      ).toDart.timeout(_operationTimeout);
    } on Object catch (error) {
      throw _mapBridgeError(error);
    }
  }

  @override
  Future<void> dispose() async {
    if (_disposed) return;
    await leaveTableChannel();
    _clearEventSink();
    _disposed = true;
    await _connectionController.close();
    await _speakingController.close();
  }

  void _handleBridgeEvent(JSString encodedEvent) {
    if (_disposed) return;
    final Object? decoded;
    try {
      decoded = jsonDecode(encodedEvent.toDart);
    } on FormatException {
      return;
    }
    if (decoded is! Map<String, Object?>) return;

    switch (decoded['type']) {
      case 'connecting':
        _setState(VoiceConnectionState.connecting);
      case 'connected':
        _joined = true;
        _setState(VoiceConnectionState.connected);
      case 'reconnecting':
        _setState(VoiceConnectionState.reconnecting);
      case 'disconnected':
        _joined = false;
        _microphoneEnabled = false;
        _emitSpeaking(const <String>{});
        _setState(VoiceConnectionState.disconnected);
      case 'speaking':
        final values = decoded['userIds'];
        if (values is List<Object?>) {
          _emitSpeaking(
            values
                .whereType<String>()
                .where((value) => value.isNotEmpty)
                .toSet(),
          );
        }
    }
  }

  Object _mapBridgeError(Object error, {bool microphoneOperation = false}) {
    final message = error.toString();
    if (microphoneOperation &&
        (message.contains('NotAllowedError') ||
            message.contains('PermissionDeniedError') ||
            message.toLowerCase().contains('permission denied'))) {
      return const MicrophonePermissionDeniedException();
    }

    final codeMatch = RegExp(r'code\D+(-?\d+)').firstMatch(message);
    return VoiceRoomException(
      codeMatch == null ? -1 : int.tryParse(codeMatch.group(1)!) ?? -1,
      message,
    );
  }

  void _emitSpeaking(Set<String> userIds) {
    if (!_speakingController.isClosed) {
      _speakingController.add(Set.unmodifiable(userIds));
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
