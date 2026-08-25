import 'voice_chat_service.dart';

VoiceChatService createVoiceChatService() => UnsupportedVoiceChatService();

class UnsupportedVoiceChatService implements VoiceChatService {
  static const _message =
      'Voice chat is not implemented for this platform yet.';

  @override
  Stream<VoiceConnectionState> get connectionState => const Stream.empty();

  @override
  Stream<Set<String>> get speakingUserIds => const Stream.empty();

  @override
  Future<void> joinTableChannel({
    required int sdkAppId,
    required String tableId,
    required String userId,
    required String userSig,
  }) => Future.error(UnsupportedError(_message));

  @override
  Future<void> leaveTableChannel() async {}

  @override
  Future<void> setMicrophoneEnabled(bool enabled) =>
      Future.error(UnsupportedError(_message));

  @override
  Future<void> setRemoteUserMuted(String userId, bool muted) =>
      Future.error(UnsupportedError(_message));

  @override
  Future<void> setPlaybackVolume(double volume) async {}

  @override
  Future<void> dispose() async {}
}
