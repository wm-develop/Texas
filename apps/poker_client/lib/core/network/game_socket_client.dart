import 'dart:async';
import 'dart:convert';

import 'package:flutter/foundation.dart';
import 'package:poker_client/core/network/table_sequence_tracker.dart';
import 'package:poker_client/features/table/domain/table_snapshot.dart';
import 'package:web_socket_channel/web_socket_channel.dart';

enum GameSocketStatus {
  disconnected,
  connecting,
  connected,
  authenticated,
  joined,
  reconnecting,
  failed,
}

class GameSocketClient extends ChangeNotifier {
  GameSocketClient({
    required this.accessToken,
    required this.roomId,
    required this.userId,
    String? serverUrl,
    this.connectTimeout = const Duration(seconds: 20),
  }) : serverUrl =
           serverUrl ??
           const String.fromEnvironment(
             'GAME_SERVER_URL',
             defaultValue: 'ws://127.0.0.1:8080/ws',
           );

  final String serverUrl;
  final String accessToken;
  final String roomId;
  final String userId;
  final Duration connectTimeout;

  WebSocketChannel? _channel;
  StreamSubscription<dynamic>? _subscription;
  GameSocketStatus _status = GameSocketStatus.disconnected;
  String? _lastMessageType;
  String? _errorMessage;
  TableSnapshot? _snapshot;
  final List<TableChatMessage> _chatMessages = [];
  List<TableVoiceMember> _voiceMembers = const [];
  Timer? _reconnectTimer;
  Timer? _heartbeatTimer;
  int _reconnectAttempts = 0;
  bool _disposed = false;
  Duration _serverClockOffset = Duration.zero;
  bool _actionPending = false;
  int? _pendingRevision;
  final TableSequenceTracker _sequences = TableSequenceTracker();
  bool _recoveringSequenceGap = false;
  int _requestCounter = 0;
  DateTime _lastServerMessageAt = DateTime.now();

  GameSocketStatus get status => _status;
  String? get lastMessageType => _lastMessageType;
  String? get errorMessage => _errorMessage;
  TableSnapshot? get snapshot => _snapshot;
  List<TableChatMessage> get chatMessages => List.unmodifiable(_chatMessages);
  List<TableVoiceMember> get voiceMembers => _voiceMembers;
  bool get actionPending => _actionPending || _recoveringSequenceGap;
  int get lastSequence => _sequences.lastSequence;
  DateTime get serverNow => DateTime.now().add(_serverClockOffset);

  Future<void> connect() async {
    if (_status == GameSocketStatus.connecting ||
        _status == GameSocketStatus.connected ||
        _status == GameSocketStatus.authenticated ||
        _status == GameSocketStatus.joined) {
      return;
    }
    _reconnectTimer?.cancel();
    await _subscription?.cancel();
    await _channel?.sink.close();
    _setStatus(GameSocketStatus.connecting);
    _errorMessage = null;
    WebSocketChannel? pendingChannel;
    try {
      pendingChannel = WebSocketChannel.connect(Uri.parse(serverUrl));
      await pendingChannel.ready.timeout(connectTimeout);
      final channel = pendingChannel;
      _channel = channel;
      _subscription = channel.stream.listen(
        _handleMessage,
        onError: _handleError,
        onDone: _handleDone,
        cancelOnError: true,
      );
      _errorMessage = null;
      _lastServerMessageAt = DateTime.now();
      _startHeartbeat();
      _setStatus(GameSocketStatus.connected);
      _send(
        'session.authenticate',
        payload: {
          'accessToken': accessToken,
          'deviceId':
              '${defaultTargetPlatform.name}-${DateTime.now().millisecondsSinceEpoch}',
        },
      );
    } on Object {
      await pendingChannel?.sink.close();
      _errorMessage = 'connection_failed';
      _scheduleReconnect();
    }
  }

  void sendPing() => _send('system.ping');

  void setReady(bool ready) {
    _send('table.ready.set', payload: {'ready': ready});
  }

  void submitAction(String action, {int? raiseTo}) {
    final snapshot = _snapshot;
    if (snapshot == null || snapshot.handId.isEmpty || actionPending) return;
    _actionPending = true;
    _pendingRevision = snapshot.tableRevision;
    notifyListeners();
    final requestId = _requestId();
    _send(
      'table.action.submit',
      requestId: requestId,
      handId: snapshot.handId,
      tableRevision: snapshot.tableRevision,
      payload: {'actionId': requestId, 'action': action, 'raiseTo': ?raiseTo},
    );
  }

  void sendChat(String content, {String kind = 'text'}) {
    final trimmed = content.trim();
    if (trimmed.isEmpty) return;
    _send(
      'table.chat.send',
      payload: {
        'clientMessageId': _requestId(),
        'kind': kind,
        'content': trimmed,
      },
    );
  }

  void requestSnapshot({String reason = 'manual'}) => _send(
    'table.snapshot.request',
    payload: {'lastSequence': _sequences.lastSequence, 'reason': reason},
  );

  void useTimeExtension() {
    if (_recoveringSequenceGap) return;
    _send('table.time_extension.use', payload: const {});
  }

  void rebuy(int amount) {
    if (amount <= 0 || _recoveringSequenceGap) return;
    _send('table.rebuy', payload: {'amount': amount});
  }

  void setVoiceState({required bool joined, required bool microphoneEnabled}) =>
      _send(
        'table.voice.state.set',
        payload: {
          'joined': joined,
          'microphoneEnabled': joined && microphoneEnabled,
        },
      );

  void _send(
    String type, {
    String? requestId,
    Map<String, Object?>? payload,
    String? handId,
    int? tableRevision,
  }) {
    if (_channel == null ||
        _status == GameSocketStatus.disconnected ||
        _status == GameSocketStatus.connecting ||
        _status == GameSocketStatus.reconnecting ||
        _status == GameSocketStatus.failed) {
      return;
    }
    _channel!.sink.add(
      jsonEncode({
        'version': 1,
        'type': type,
        'requestId': requestId ?? _requestId(),
        'tableId': roomId,
        'handId': ?handId,
        'tableRevision': ?tableRevision,
        'payload': ?payload,
      }),
    );
  }

  void _handleMessage(dynamic rawMessage) {
    try {
      _lastServerMessageAt = DateTime.now();
      final message = jsonDecode(rawMessage as String) as Map<String, dynamic>;
      final type = message['type'] as String?;
      _lastMessageType = type;
      final serverTime = message['serverTime'];
      if (serverTime is int && serverTime > 0) {
        _serverClockOffset = Duration(
          milliseconds: serverTime - DateTime.now().millisecondsSinceEpoch,
        );
      }
      final payload = message['payload'];
      final sequence = message['sequence'];
      if (sequence is int && sequence > 0 && !_acceptSequence(type, sequence)) {
        notifyListeners();
        return;
      }
      switch (type) {
        case 'session.authenticated':
          _setStatus(GameSocketStatus.authenticated, notify: false);
          _send(
            'table.join',
            payload: {'lastSequence': _sequences.lastSequence},
          );
        case 'table.joined':
          _reconnectAttempts = 0;
          _setStatus(GameSocketStatus.joined, notify: false);
          if (payload is Map<String, dynamic>) {
            final history = payload['chatHistory'];
            if (history is List<dynamic>) {
              _chatMessages
                ..clear()
                ..addAll(
                  history.map(
                    (value) => TableChatMessage.fromJson(
                      _historyToWire(value as Map<String, dynamic>),
                    ),
                  ),
                );
            }
            _parseVoiceMembers(payload['voiceMembers']);
          }
        case 'table.snapshot':
          if (payload is Map<String, dynamic>) {
            _snapshot = TableSnapshot.fromJson(payload);
            _recoveringSequenceGap = false;
            if (_errorMessage == 'sequence_gap') _errorMessage = null;
            if (_pendingRevision != null &&
                _snapshot!.tableRevision != _pendingRevision) {
              _actionPending = false;
              _pendingRevision = null;
            }
          }
        case 'table.replay.completed':
          if (payload is Map<String, dynamic>) {
            final recoveredThrough = payload['lastSequence'];
            if (recoveredThrough is int &&
                _sequences.recoveredThrough(recoveredThrough)) {
              _recoveringSequenceGap = false;
              if (_errorMessage == 'sequence_gap') _errorMessage = null;
            }
          }
        case 'table.action.accepted':
          _actionPending = false;
          _pendingRevision = null;
        case 'table.rebuy.accepted':
          _errorMessage = null;
        case 'table.chat.message':
          if (payload is Map<String, dynamic>) {
            final chat = TableChatMessage.fromJson(payload);
            if (!_chatMessages.any(
              (item) => item.messageId == chat.messageId,
            )) {
              _chatMessages.add(chat);
              if (_chatMessages.length > 50) _chatMessages.removeAt(0);
            }
          }
        case 'table.voice.state':
          if (payload is Map<String, dynamic>) {
            _parseVoiceMembers(payload['members']);
          }
        case 'system.error':
        case 'table.action.rejected':
        case 'table.chat.rejected':
        case 'table.time_extension.rejected':
        case 'table.rebuy.rejected':
          if (payload is Map<String, dynamic>) {
            _errorMessage = payload['code'] as String? ?? type;
            if (type == 'table.action.rejected') {
              _actionPending = false;
              _pendingRevision = null;
            }
            if (type == 'table.action.rejected' &&
                (_errorMessage == 'stale_revision' ||
                    _errorMessage == 'stale_hand')) {
              requestSnapshot(reason: 'stale_action');
            }
          }
      }
      notifyListeners();
    } on Object catch (error) {
      _errorMessage = 'invalid_server_message: $error';
      notifyListeners();
    }
  }

  void _handleError(Object error, StackTrace stackTrace) {
    _errorMessage = 'connection_failed';
    _scheduleReconnect();
  }

  void _handleDone() {
    _scheduleReconnect();
  }

  void _scheduleReconnect() {
    if (_disposed || _reconnectTimer?.isActive == true) return;
    _heartbeatTimer?.cancel();
    _setStatus(GameSocketStatus.reconnecting);
    _actionPending = false;
    _pendingRevision = null;
    final seconds = 1 << _reconnectAttempts.clamp(0, 3);
    _reconnectAttempts++;
    _reconnectTimer = Timer(Duration(seconds: seconds), connect);
  }

  void _startHeartbeat() {
    _heartbeatTimer?.cancel();
    _heartbeatTimer = Timer.periodic(const Duration(seconds: 25), (_) {
      if (DateTime.now().difference(_lastServerMessageAt) >
          const Duration(seconds: 75)) {
        _errorMessage = 'connection_failed';
        unawaited(_channel?.sink.close());
        _scheduleReconnect();
        return;
      }
      sendPing();
    });
  }

  void _setStatus(GameSocketStatus value, {bool notify = true}) {
    _status = value;
    if (notify) notifyListeners();
  }

  void _parseVoiceMembers(Object? values) {
    if (values is! List<dynamic>) return;
    _voiceMembers = values
        .map(
          (value) => TableVoiceMember.fromJson(value as Map<String, dynamic>),
        )
        .toList(growable: false);
  }

  bool _acceptSequence(String? type, int sequence) {
    switch (_sequences.accept(type, sequence)) {
      case TableSequenceDisposition.accepted:
        return true;
      case TableSequenceDisposition.duplicate:
        return false;
      case TableSequenceDisposition.gap:
        _recoveringSequenceGap = true;
        _errorMessage = 'sequence_gap';
        requestSnapshot(reason: 'sequence_gap');
        return false;
    }
  }

  String _requestId() =>
      '${DateTime.now().microsecondsSinceEpoch}-${_requestCounter++}';

  @override
  void dispose() {
    _disposed = true;
    _reconnectTimer?.cancel();
    _heartbeatTimer?.cancel();
    unawaited(_subscription?.cancel());
    unawaited(_channel?.sink.close());
    super.dispose();
  }
}

class TableVoiceMember {
  const TableVoiceMember({
    required this.userId,
    required this.displayName,
    required this.joined,
    required this.microphoneEnabled,
  });

  factory TableVoiceMember.fromJson(Map<String, dynamic> json) =>
      TableVoiceMember(
        userId: json['userId'] as String,
        displayName: json['displayName'] as String,
        joined: json['joined'] as bool? ?? false,
        microphoneEnabled: json['microphoneEnabled'] as bool? ?? false,
      );

  final String userId;
  final String displayName;
  final bool joined;
  final bool microphoneEnabled;
}

Map<String, dynamic> _historyToWire(Map<String, dynamic> value) => {
  'messageId': value['MessageID'],
  'userId': value['UserID'],
  'displayName': value['DisplayName'],
  'kind': value['Kind'],
  'content': value['Content'],
  'sentAt': DateTime.parse(value['SentAt'] as String).millisecondsSinceEpoch,
};
