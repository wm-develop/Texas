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
    required this.accessTokenProvider,
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
  final Future<String> Function({bool forceRefresh}) accessTokenProvider;
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
  final List<TablePlayerInteraction> _playerInteractions = [];
  int _chatEventRevision = 0;
  TableChatMessage? _latestChatEvent;
  List<TableVoiceMember> _voiceMembers = const [];
  Timer? _reconnectTimer;
  Timer? _heartbeatTimer;
  int _reconnectAttempts = 0;
  bool _disposed = false;
  Duration _serverClockOffset = Duration.zero;
  bool _actionPending = false;

  /// 动作提交后等待回执的看门狗。回执只会以 table.action.accepted /
  /// table.action.rejected / system.error 三种形态之一到达；若都没等到，
  /// 说明这条请求已经随连接一起丢失，必须把按钮放开，否则玩家会一直
  /// 处于「怎么点都没反应」的状态。requestId 保证重发不会重复执行。
  Timer? _actionWatchdog;
  static const Duration actionAckTimeout = Duration(seconds: 5);

  /// 本连接已被同一账号的新连接取代（服务端关闭码 4001）。
  /// 此时不能再自动重连去抢回连接，否则两个客户端会互相踢来踢去。
  bool _superseded = false;
  int? _pendingRevision;
  final TableSequenceTracker _sequences = TableSequenceTracker();
  bool _recoveringSequenceGap = false;
  bool _forceRefreshOnNextConnect = false;
  int _requestCounter = 0;
  String? _serverInstanceId;
  String? _voidedHandId;
  DateTime _lastServerMessageAt = DateTime.now();

  GameSocketStatus get status => _status;
  String? get lastMessageType => _lastMessageType;
  String? get errorMessage => _errorMessage;
  TableSnapshot? get snapshot => _snapshot;

  /// 服务端进程标识，来自 session.authenticated；重连后变化即服务端重启过。
  String? get serverInstanceId => _serverInstanceId;

  /// 取出并清除「上一手因服务端重启作废」的手牌号；没有则返回 null。
  /// 进行中的手只存在于旧进程内存里，新进程无法恢复它，玩家需要一个解释。
  String? takeVoidedHandId() {
    final value = _voidedHandId;
    _voidedHandId = null;
    return value;
  }

  void _observeServerInstance(String? instanceId) {
    if (instanceId == null || instanceId.isEmpty) return;
    final previous = _serverInstanceId;
    _serverInstanceId = instanceId;
    final snapshot = _snapshot;
    if (previous != null &&
        previous != instanceId &&
        snapshot != null &&
        snapshot.handId.isNotEmpty &&
        snapshot.settlement == null) {
      _voidedHandId = snapshot.handId;
    }
  }

  /// 仅供测试：把一条服务端消息喂给解析逻辑，绕过网络。
  @visibleForTesting
  void debugHandleMessage(String rawMessage) => _handleMessage(rawMessage);
  List<TableChatMessage> get chatMessages => List.unmodifiable(_chatMessages);
  List<TablePlayerInteraction> get playerInteractions =>
      List.unmodifiable(_playerInteractions);
  int get chatEventRevision => _chatEventRevision;
  TableChatMessage? get latestChatEvent => _latestChatEvent;
  List<TableVoiceMember> get voiceMembers => _voiceMembers;
  bool get actionPending => _actionPending || _recoveringSequenceGap;
  int get lastSequence => _sequences.lastSequence;
  DateTime get serverNow => DateTime.now().add(_serverClockOffset);

  Future<void> connect() async {
    _superseded = false;
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
      final accessToken = await accessTokenProvider(
        forceRefresh: _forceRefreshOnNextConnect,
      );
      _forceRefreshOnNextConnect = false;
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

  void showHoleCards() {
    if (_recoveringSequenceGap || !(_snapshot?.canShowHoleCards ?? false)) {
      return;
    }
    _send('table.hole_cards.reveal', payload: const {});
  }

  void requestHoleCardsView(String targetUserId) {
    if (targetUserId.isEmpty || _recoveringSequenceGap) return;
    _send(
      'table.hole_cards.view.request',
      payload: {'targetUserId': targetUserId},
    );
  }

  void respondHoleCardsView(String pendingRequestId, bool accept) {
    _send(
      'table.hole_cards.view.respond',
      payload: {'pendingRequestId': pendingRequestId, 'accept': accept},
    );
  }

  void requestSeatChange(int targetSeat) {
    if (targetSeat <= 0 || _recoveringSequenceGap) return;
    _send('table.seat.change.request', payload: {'targetSeat': targetSeat});
  }

  void respondSeatSwap(String pendingRequestId, bool accept) {
    _send(
      'table.seat.swap.respond',
      payload: {'pendingRequestId': pendingRequestId, 'accept': accept},
    );
  }

  void chooseRunoutCount(int count) {
    if ((count != 1 && count != 2) || _recoveringSequenceGap) return;
    _send('table.runout.choose', payload: {'count': count});
  }

  void submitAction(String action, {int? raiseTo}) {
    final snapshot = _snapshot;
    if (snapshot == null || snapshot.handId.isEmpty || actionPending) return;
    _actionPending = true;
    _pendingRevision = snapshot.tableRevision;
    _armActionWatchdog();
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

  void interactWithPlayer(String targetUserId, String kind) {
    if (targetUserId.isEmpty || (kind != 'praise' && kind != 'taunt')) return;
    _send(
      'table.player.interact',
      payload: {'targetUserId': targetUserId, 'kind': kind},
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
          if (payload is Map<String, dynamic>) {
            _observeServerInstance(payload['serverInstanceId'] as String?);
          }
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
              _clearPendingAction();
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
          _clearPendingAction();
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
              _latestChatEvent = chat;
              _chatEventRevision++;
            }
          }
        case 'table.player.interaction':
          if (payload is Map<String, dynamic>) {
            final interaction = TablePlayerInteraction.fromJson(payload);
            if (!_playerInteractions.any(
              (item) => item.interactionId == interaction.interactionId,
            )) {
              _playerInteractions.add(interaction);
              if (_playerInteractions.length > 20) {
                _playerInteractions.removeAt(0);
              }
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
            if (type == 'system.error' &&
                _errorMessage == 'authentication_required') {
              _forceRefreshOnNextConnect = true;
              unawaited(_channel?.sink.close());
            }
            // system.error 同样意味着这条动作没有生效（例如 request_id_required
            // 或分发前置校验失败）；只在 action.rejected 时放开会让按钮卡死。
            if (type == 'table.action.rejected' || type == 'system.error') {
              _clearPendingAction();
              // A rejection can arrive after the server has already advanced
              // its in-memory state but failed a later persistence step. Always
              // resync so the action bar cannot remain on an obsolete revision.
              requestSnapshot(reason: 'action_rejected');
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
    if (_channel?.closeCode == supersededCloseCode) {
      _superseded = true;
      _heartbeatTimer?.cancel();
      _clearPendingAction();
      _errorMessage = 'superseded';
      _setStatus(GameSocketStatus.disconnected);
      return;
    }
    _scheduleReconnect();
  }

  /// 服务端用这个关闭码通知旧连接：同一账号已从别处连上同一牌桌。
  static const int supersededCloseCode = 4001;

  void _clearPendingAction() {
    _actionWatchdog?.cancel();
    _actionWatchdog = null;
    _actionPending = false;
    _pendingRevision = null;
  }

  void _armActionWatchdog() {
    _actionWatchdog?.cancel();
    _actionWatchdog = Timer(actionAckTimeout, () {
      if (!_actionPending) return;
      _clearPendingAction();
      _errorMessage = 'action_timeout';
      // 回执迟迟不到，多半是状态已经不一致，顺手重新同步一次
      requestSnapshot(reason: 'action_timeout');
      notifyListeners();
    });
  }

  void _scheduleReconnect() {
    if (_disposed || _superseded || _reconnectTimer?.isActive == true) return;
    _heartbeatTimer?.cancel();
    _setStatus(GameSocketStatus.reconnecting);
    _clearPendingAction();
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
    _actionWatchdog?.cancel();
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

class TablePlayerInteraction {
  const TablePlayerInteraction({
    required this.interactionId,
    required this.fromUserId,
    required this.fromDisplayName,
    required this.targetUserId,
    required this.targetDisplayName,
    required this.kind,
    required this.sentAt,
  });

  factory TablePlayerInteraction.fromJson(Map<String, dynamic> json) =>
      TablePlayerInteraction(
        interactionId: json['interactionId'] as String,
        fromUserId: json['fromUserId'] as String,
        fromDisplayName: json['fromDisplayName'] as String,
        targetUserId: json['targetUserId'] as String,
        targetDisplayName: json['targetDisplayName'] as String,
        kind: json['kind'] as String,
        sentAt: DateTime.fromMillisecondsSinceEpoch(json['sentAt'] as int),
      );

  final String interactionId;
  final String fromUserId;
  final String fromDisplayName;
  final String targetUserId;
  final String targetDisplayName;
  final String kind;
  final DateTime sentAt;
}

Map<String, dynamic> _historyToWire(Map<String, dynamic> value) => {
  'messageId': value['MessageID'],
  'userId': value['UserID'],
  'displayName': value['DisplayName'],
  'kind': value['Kind'],
  'content': value['Content'],
  'sentAt': DateTime.parse(value['SentAt'] as String).millisecondsSinceEpoch,
};
