enum VoiceConnectionState { disconnected, connecting, connected, reconnecting }

abstract interface class VoiceChatService {
  Stream<VoiceConnectionState> get connectionState;

  Stream<Set<String>> get speakingUserIds;

  Future<void> joinTableChannel({
    required int sdkAppId,
    required String tableId,
    required String userId,
    required String userSig,
  });

  Future<void> leaveTableChannel();

  Future<void> setMicrophoneEnabled(bool enabled);

  Future<void> setRemoteUserMuted(String userId, bool muted);

  Future<void> setPlaybackVolume(double volume);

  Future<void> dispose();
}

class VoiceRoomException implements Exception {
  const VoiceRoomException(this.code, [this.message = '']);

  final int code;
  final String message;

  @override
  String toString() => 'VoiceRoomException(code: $code, message: $message)';
}

class MicrophonePermissionDeniedException implements Exception {
  const MicrophonePermissionDeniedException();

  @override
  String toString() => 'Microphone permission was denied.';
}
