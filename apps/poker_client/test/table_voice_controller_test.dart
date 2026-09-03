import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/core/network/trtc_credential_client.dart';
import 'package:poker_client/core/platform/voice_chat_service.dart';
import 'package:poker_client/features/table/presentation/table_voice_controller.dart';

class FakeVoiceChatService implements VoiceChatService {
  final _connection = StreamController<VoiceConnectionState>.broadcast();
  final _speaking = StreamController<Set<String>>.broadcast();
  final List<String> calls = [];
  final Map<String, bool> remoteMuted = {};
  double? volume;
  bool disposed = false;
  Object? joinError;

  @override
  Stream<VoiceConnectionState> get connectionState => _connection.stream;

  @override
  Stream<Set<String>> get speakingUserIds => _speaking.stream;

  void emitConnection(VoiceConnectionState state) => _connection.add(state);

  void emitSpeaking(Set<String> userIds) => _speaking.add(userIds);

  @override
  Future<void> joinTableChannel({
    required int sdkAppId,
    required String tableId,
    required String userId,
    required String userSig,
  }) async {
    if (joinError != null) throw joinError!;
    calls.add('join:$tableId:$userId');
    emitConnection(VoiceConnectionState.connected);
  }

  @override
  Future<void> leaveTableChannel() async {
    calls.add('leave');
    emitConnection(VoiceConnectionState.disconnected);
  }

  @override
  Future<void> setMicrophoneEnabled(bool enabled) async =>
      calls.add('mic:$enabled');

  @override
  Future<void> setRemoteUserMuted(String userId, bool muted) async {
    calls.add('remote:$userId:$muted');
    remoteMuted[userId] = muted;
  }

  final localEffects = <(int, String, double)>[];

  @override
  Future<void> playLocalEffect({
    required int id,
    required String filePath,
    required double volume,
  }) async => localEffects.add((id, filePath, volume));

  @override
  Future<void> setPlaybackVolume(double value) async => volume = value;

  @override
  Future<void> dispose() async => disposed = true;
}

class _Harness {
  _Harness({double volume = 0.8}) {
    controller = TableVoiceController(
      voiceChat: service,
      issueCredentials: () async {
        credentialRequests++;
        return const TrtcCredentials(
          sdkAppId: 1400000,
          userId: 'me',
          roomId: 'room_1',
          userSig: 'sig',
          expireIn: 3600,
        );
      },
      currentUserId: 'me',
      onStateChanged: ({required joined, required microphoneEnabled}) =>
          pushed.add('$joined/$microphoneEnabled'),
      onError: errors.add,
      playbackVolume: () => volume,
    );
  }

  final service = FakeVoiceChatService();
  final List<String> pushed = [];
  final List<Object> errors = [];
  int credentialRequests = 0;
  late final TableVoiceController controller;

  /// 等待状态流的事件送达监听器。
  Future<void> settle() => Future<void>.delayed(Duration.zero);
}

void main() {
  test('joining issues credentials, applies volume and reports state', () async {
    final harness = _Harness();
    await harness.controller.setJoined(true);
    await harness.settle();

    expect(harness.credentialRequests, 1);
    expect(harness.service.calls, contains('join:room_1:me'));
    expect(harness.service.volume, 0.8);
    expect(harness.controller.joined, isTrue);
    expect(harness.pushed, ['true/false']);
    expect(harness.controller.operationInProgress, isFalse);
  });

  test('users muted before joining are re-muted on join', () async {
    final harness = _Harness();
    await harness.controller.setUserMuted('noisy', true);
    await harness.controller.setUserBlocked('rude', true);
    // 未加入语音时不应调用 RTC
    expect(harness.service.calls, isEmpty);

    await harness.controller.setJoined(true);
    await harness.settle();
    expect(harness.service.remoteMuted['noisy'], isTrue);
    expect(harness.service.remoteMuted['rude'], isTrue);
  });

  test('blocking and voice muting are independent sources', () async {
    final harness = _Harness();
    await harness.controller.setJoined(true);
    await harness.settle();

    await harness.controller.setUserBlocked('both', true);
    await harness.controller.setUserMuted('both', true);
    expect(harness.service.remoteMuted['both'], isTrue);

    // 解除屏蔽后仍然处于语音静音，不能恢复出声
    await harness.controller.setUserBlocked('both', false);
    expect(harness.service.remoteMuted['both'], isTrue);
    expect(harness.controller.blockedUserIds, isNot(contains('both')));
    expect(harness.controller.mutedUserIds, contains('both'));

    // 两个来源都解除后才恢复
    await harness.controller.setUserMuted('both', false);
    expect(harness.service.remoteMuted['both'], isFalse);
  });

  test('muted users are not shown or counted as speaking', () async {
    final harness = _Harness();
    await harness.controller.setJoined(true);
    await harness.controller.setUserMuted('quiet', true);
    harness.service.emitSpeaking({'loud', 'quiet'});
    await harness.settle();

    expect(harness.controller.isSpeaking('loud'), isTrue);
    expect(harness.controller.isSpeaking('quiet'), isFalse);
    expect(harness.controller.audibleSpeakingUserIds, {'loud'});
    expect(harness.controller.speakingUserIds, {'loud', 'quiet'});
  });

  test('disconnecting clears the microphone and speaking state', () async {
    final harness = _Harness();
    await harness.controller.setJoined(true);
    await harness.settle();
    await harness.controller.setMicrophoneEnabled(true);
    harness.service.emitSpeaking({'loud'});
    await harness.settle();
    expect(harness.controller.microphoneEnabled, isTrue);

    harness.service.emitConnection(VoiceConnectionState.disconnected);
    await harness.settle();
    expect(harness.controller.joined, isFalse);
    expect(harness.controller.microphoneEnabled, isFalse);
    expect(harness.controller.speakingUserIds, isEmpty);
  });

  test('reconnecting still counts as joined', () async {
    final harness = _Harness();
    harness.service.emitConnection(VoiceConnectionState.reconnecting);
    await harness.settle();
    expect(harness.controller.joined, isTrue);
  });

  test('the microphone cannot be toggled before joining', () async {
    final harness = _Harness();
    await harness.controller.setMicrophoneEnabled(true);
    expect(harness.controller.microphoneEnabled, isFalse);
    expect(harness.service.calls, isEmpty);
  });

  test('a user cannot mute or block themselves', () async {
    final harness = _Harness();
    await harness.controller.setJoined(true);
    await harness.settle();
    await harness.controller.setUserMuted('me', true);
    await harness.controller.setUserBlocked('me', true);
    expect(harness.controller.mutedUserIds, isEmpty);
    expect(harness.controller.blockedUserIds, isEmpty);
    expect(harness.service.remoteMuted, isNot(contains('me')));
  });

  test('a join failure surfaces the error and releases the busy flag', () async {
    final harness = _Harness();
    harness.service.joinError = const MicrophonePermissionDeniedException();
    await harness.controller.setJoined(true);

    expect(harness.errors, hasLength(1));
    expect(harness.controller.operationInProgress, isFalse);
    expect(harness.controller.joined, isFalse);
    expect(harness.pushed, isEmpty);
  });

  test('leaving reports the state and stops the microphone', () async {
    final harness = _Harness();
    await harness.controller.setJoined(true);
    await harness.settle();
    await harness.controller.setMicrophoneEnabled(true);
    await harness.controller.setJoined(false);
    await harness.settle();

    expect(harness.service.calls, contains('leave'));
    expect(harness.pushed.last, 'false/false');
    expect(harness.controller.microphoneEnabled, isFalse);
  });

  test('the playback volume only reaches RTC while joined', () async {
    final harness = _Harness();
    await harness.controller.applyPlaybackVolume();
    expect(harness.service.volume, isNull);

    await harness.controller.setJoined(true);
    await harness.settle();
    await harness.controller.applyPlaybackVolume();
    expect(harness.service.volume, 0.8);
  });

  test('disposing releases the underlying voice service', () async {
    final harness = _Harness();
    harness.controller.dispose();
    await harness.settle();
    expect(harness.service.disposed, isTrue);
  });

  test('voice errors are translated to Chinese messages', () {
    expect(
      voiceErrorMessage(const MicrophonePermissionDeniedException()),
      '需要麦克风权限才能开启自由麦',
    );
    expect(
      voiceErrorMessage(const TrtcCredentialException('x', statusCode: 401)),
      '语音测试凭证鉴权失败',
    );
    expect(voiceErrorMessage(const VoiceRoomException(-1308)), '加入语音房失败（错误码 -1308）');
    expect(voiceErrorMessage(TimeoutException('x')), '语音服务连接超时');
    expect(voiceErrorMessage(UnsupportedError('x')), '当前平台的语音适配尚未完成');
    expect(voiceErrorMessage(Exception('x')), '语音操作失败');
  });
}
