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

  /// 在语音会话内部本地播放一小段音频。
  ///
  /// 鸿蒙的音频焦点是应用级的：走普通音频插件播提示音会把整个应用的音频
  /// 会话切成媒体场景并独占输出，压制正在进行的通话流。交给 RTC 引擎播放
  /// 后，声音与语音走同一条通道，不会产生这种冲突。
  ///
  /// [id] 区分不同音效以便并发播放；[publish] 恒为假，只有本人听得到。
  /// 平台不支持时应安静地忽略，绝不能回退到普通音频插件。
  Future<void> playLocalEffect({
    required int id,
    required String filePath,
    required double volume,
  });

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
