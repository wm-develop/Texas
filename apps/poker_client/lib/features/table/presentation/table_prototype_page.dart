import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:poker_client/core/auth/auth_session.dart';
import 'package:poker_client/core/network/game_socket_client.dart';
import 'package:poker_client/core/network/trtc_credential_client.dart';
import 'package:poker_client/core/platform/voice_chat_service.dart';
import 'package:poker_client/core/platform/voice_chat_service_factory.dart';
import 'package:poker_client/core/settings/app_settings.dart';
import 'package:poker_client/core/settings/settings_dialog.dart';
import 'package:poker_client/features/table/domain/table_seat.dart';
import 'package:poker_client/features/table/domain/table_snapshot.dart';
import 'package:poker_client/features/lobby/domain/friend_room.dart';

class TablePrototypePage extends StatefulWidget {
  const TablePrototypePage({
    required this.session,
    required this.room,
    required this.settings,
    required this.onLeave,
    super.key,
  });

  final AuthSession session;
  final FriendRoom room;
  final AppSettingsController settings;
  final Future<void> Function() onLeave;

  @override
  State<TablePrototypePage> createState() => _TablePrototypePageState();
}

class _TablePrototypePageState extends State<TablePrototypePage> {
  static const _designSize = Size(1280, 720);
  static const _gameHttpServerUrl = String.fromEnvironment(
    'GAME_HTTP_SERVER_URL',
    defaultValue: 'http://127.0.0.1:8080',
  );

  VoiceConnectionState _voiceState = VoiceConnectionState.disconnected;
  bool _microphoneEnabled = false;
  bool _voiceOperationInProgress = false;
  Set<String> _speakingUserIds = const {};
  final Set<String> _blockedUserIds = {};
  bool _chatVisible = true;
  late final GameSocketClient _gameSocket;
  late final TrtcCredentialClient _trtcCredentials;
  late final VoiceChatService _voiceChat;
  late final StreamSubscription<VoiceConnectionState> _voiceStateSubscription;
  late final StreamSubscription<Set<String>> _speakingSubscription;
  Timer? _tableClock;
  String? _lastShownGameError;
  GameSocketStatus? _lastGameSocketStatus;
  String? _lastActionSoundUserId;
  String? _lastSettlementSoundHandId;
  bool _autoJoinAttempted = false;

  bool get _voiceJoined =>
      _voiceState == VoiceConnectionState.connected ||
      _voiceState == VoiceConnectionState.reconnecting;

  @override
  void initState() {
    super.initState();
    _gameSocket = GameSocketClient(
      accessToken: widget.session.accessToken,
      roomId: widget.room.roomId,
      userId: widget.session.user.userId,
    )..addListener(_refresh);
    unawaited(_gameSocket.connect());
    _trtcCredentials = TrtcCredentialClient(
      serverBaseUri: Uri.parse(_gameHttpServerUrl),
    );
    _voiceChat = createVoiceChatService();
    _voiceStateSubscription = _voiceChat.connectionState.listen((state) {
      if (!mounted) return;
      setState(() {
        _voiceState = state;
        if (state == VoiceConnectionState.disconnected) {
          _microphoneEnabled = false;
          _speakingUserIds = const {};
        }
      });
    });
    _speakingSubscription = _voiceChat.speakingUserIds.listen((userIds) {
      if (mounted) setState(() => _speakingUserIds = userIds);
    });
    _tableClock = Timer.periodic(const Duration(milliseconds: 200), (_) {
      if (mounted && _gameSocket.snapshot?.currentAction?.deadline != null) {
        setState(() {});
      }
    });
    widget.settings.addListener(_settingsChanged);
    WidgetsBinding.instance.addPostFrameCallback((_) => _settingsChanged());
  }

  @override
  void dispose() {
    _gameSocket
      ..removeListener(_refresh)
      ..dispose();
    unawaited(_voiceStateSubscription.cancel());
    unawaited(_speakingSubscription.cancel());
    unawaited(_voiceChat.dispose());
    _tableClock?.cancel();
    _trtcCredentials.close();
    widget.settings.removeListener(_settingsChanged);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: SafeArea(
        child: Center(
          child: FittedBox(
            fit: BoxFit.contain,
            child: SizedBox.fromSize(
              size: _designSize,
              child: DecoratedBox(
                decoration: const BoxDecoration(
                  gradient: RadialGradient(
                    colors: [Color(0xFF16473B), Color(0xFF061814)],
                    radius: 1.1,
                  ),
                ),
                child: Stack(
                  children: [
                    Positioned(
                      left: 24,
                      top: 18,
                      child: _RoomHeader(
                        room: widget.room,
                        onLeave: _leaveTable,
                        onSettings: () =>
                            showAppSettingsDialog(context, widget.settings),
                      ),
                    ),
                    Positioned(
                      left: 430,
                      top: 16,
                      child: _ConnectionStatusBar(client: _gameSocket),
                    ),
                    Positioned(
                      right: 24,
                      top: 16,
                      child: _VoiceControls(
                        voiceJoined: _voiceJoined,
                        connectionState: _voiceState,
                        microphoneEnabled: _microphoneEnabled,
                        operationInProgress: _voiceOperationInProgress,
                        speakingCount: _speakingUserIds.length,
                        members: _gameSocket.voiceMembers,
                        speakingUserIds: _speakingUserIds,
                        userId: widget.session.user.displayName,
                        onJoinChanged: _setVoiceJoined,
                        onMicrophoneChanged: _setMicrophoneEnabled,
                      ),
                    ),
                    Positioned.fill(
                      left: 104,
                      right: _chatVisible ? 264 : 104,
                      top: 62,
                      bottom: 132,
                      child: _PokerTable(
                        seats: _tableSeats,
                        alignments: _seatAlignments,
                        snapshot: _gameSocket.snapshot,
                        actionRemaining: _actionRemaining,
                      ),
                    ),
                    if (_chatVisible)
                      Positioned(
                        right: 18,
                        top: 86,
                        bottom: 104,
                        width: 230,
                        child: _ChatPanel(
                          client: _gameSocket,
                          currentUserId: widget.session.user.userId,
                          blockedUserIds: _blockedUserIds,
                          onBlockChanged: _setUserBlocked,
                          onClose: _toggleChat,
                        ),
                      )
                    else
                      Positioned(
                        right: 24,
                        bottom: 118,
                        child: FilledButton.tonalIcon(
                          onPressed: _toggleChat,
                          icon: const Icon(Icons.chat_bubble_outline),
                          label: const Text('文字聊天'),
                        ),
                      ),
                    Positioned(
                      left: 104,
                      right: _chatVisible ? 264 : 104,
                      bottom: 92,
                      height: 66,
                      child: Center(
                        child: SizedBox(
                          width: 680,
                          child: _HandCardsPanel(
                            client: _gameSocket,
                            userId: widget.session.user.userId,
                          ),
                        ),
                      ),
                    ),
                    Positioned(
                      left: 24,
                      right: 24,
                      bottom: 18,
                      child: _ActionBar(
                        client: _gameSocket,
                        userId: widget.session.user.userId,
                      ),
                    ),
                  ],
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }

  Future<void> _setVoiceJoined(bool value) async {
    if (_voiceOperationInProgress) return;
    setState(() => _voiceOperationInProgress = true);
    try {
      if (value) {
        final credentials = await _trtcCredentials.issue(
          userId: widget.session.user.userId,
          roomId: widget.room.roomId,
          accessToken: widget.session.accessToken,
        );
        await _voiceChat.joinTableChannel(
          sdkAppId: credentials.sdkAppId,
          tableId: credentials.roomId,
          userId: credentials.userId,
          userSig: credentials.userSig,
        );
        await _voiceChat.setPlaybackVolume(widget.settings.voiceVolume);
        for (final userId in _blockedUserIds) {
          await _voiceChat.setRemoteUserMuted(userId, true);
        }
        _gameSocket.setVoiceState(
          joined: true,
          microphoneEnabled: _microphoneEnabled,
        );
      } else {
        await _voiceChat.leaveTableChannel();
        _gameSocket.setVoiceState(joined: false, microphoneEnabled: false);
      }
    } on Object catch (error) {
      _showVoiceError(error);
    } finally {
      if (mounted) setState(() => _voiceOperationInProgress = false);
    }
  }

  Future<void> _setMicrophoneEnabled(bool value) async {
    if (!_voiceJoined || _voiceOperationInProgress) return;
    setState(() => _voiceOperationInProgress = true);
    try {
      await _voiceChat.setMicrophoneEnabled(value);
      if (mounted) {
        setState(() => _microphoneEnabled = value);
        _gameSocket.setVoiceState(joined: true, microphoneEnabled: value);
      }
    } on Object catch (error) {
      _showVoiceError(error);
    } finally {
      if (mounted) setState(() => _voiceOperationInProgress = false);
    }
  }

  void _toggleChat() => setState(() => _chatVisible = !_chatVisible);

  Future<void> _setUserBlocked(String userId, bool blocked) async {
    if (userId == widget.session.user.userId) return;
    setState(() {
      if (blocked) {
        _blockedUserIds.add(userId);
      } else {
        _blockedUserIds.remove(userId);
      }
    });
    if (!_voiceJoined) return;
    try {
      await _voiceChat.setRemoteUserMuted(userId, blocked);
    } on Object catch (error) {
      _showVoiceError(error);
    }
  }

  void _refresh() {
    if (!mounted) return;
    setState(() {});
    final error = _gameSocket.errorMessage;
    if (_lastGameSocketStatus != _gameSocket.status) {
      _lastGameSocketStatus = _gameSocket.status;
      if (_gameSocket.status == GameSocketStatus.joined && _voiceJoined) {
        _gameSocket.setVoiceState(
          joined: true,
          microphoneEnabled: _microphoneEnabled,
        );
      }
    }
    _playTableSounds();
    if (error == null) _lastShownGameError = null;
    if (error != null && error != _lastShownGameError) {
      _lastShownGameError = error;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (!mounted) return;
        ScaffoldMessenger.of(context)
          ..hideCurrentSnackBar()
          ..showSnackBar(SnackBar(content: Text(_gameErrorLabel(error))));
      });
    }
  }

  void _settingsChanged() {
    if (!mounted) return;
    if (_voiceJoined) {
      unawaited(_voiceChat.setPlaybackVolume(widget.settings.voiceVolume));
    }
    if (widget.settings.ready &&
        widget.settings.autoJoinVoice &&
        !_autoJoinAttempted &&
        !_voiceJoined) {
      _autoJoinAttempted = true;
      unawaited(_setVoiceJoined(true));
    }
  }

  void _playTableSounds() {
    final snapshot = _gameSocket.snapshot;
    final actor = snapshot?.currentAction?.userId;
    if (actor != _lastActionSoundUserId) {
      _lastActionSoundUserId = actor;
      if (widget.settings.soundEnabled && actor == widget.session.user.userId) {
        unawaited(SystemSound.play(SystemSoundType.alert));
      }
    }
    final settlementHand = snapshot?.settlement?.handId;
    if (settlementHand != null &&
        settlementHand != _lastSettlementSoundHandId) {
      _lastSettlementSoundHandId = settlementHand;
      if (widget.settings.soundEnabled) {
        unawaited(SystemSound.play(SystemSoundType.click));
      }
    }
  }

  Future<void> _leaveTable() async {
    if (_voiceJoined) {
      await _setVoiceJoined(false);
    }
    await widget.onLeave();
  }

  List<TableSeat> get _tableSeats {
    final snapshot = _gameSocket.snapshot;
    final bySeat = {
      for (final seat in snapshot?.seats ?? const <TableSeatSnapshot>[])
        seat.seat: seat,
    };
    return List.generate(widget.room.maxPlayers, (index) {
      final seatNumber = index + 1;
      final value = bySeat[seatNumber];
      if (value == null) {
        return TableSeat(
          number: seatNumber,
          displayName: '等待入座',
          chips: 0,
          isEmpty: true,
        );
      }
      final revealed = snapshot?.settlement?.revealedHands
          .where((hand) => hand.userId == value.userId)
          .firstOrNull;
      return TableSeat(
        number: seatNumber,
        displayName: value.displayName,
        chips: value.stack,
        isCurrentUser: value.userId == widget.session.user.userId,
        isDealer: snapshot?.dealerSeat == seatNumber,
        isSpeaking: _speakingUserIds.contains(value.userId),
        isReady: value.ready,
        isConnected: value.connected,
        isFolded: value.folded,
        isAllIn: value.allIn,
        position: value.position,
        streetBet: value.streetBet,
        totalBet: value.totalBet,
        lastAction: value.lastAction,
        lastActionTo: value.lastActionTo,
        isCurrentActor: snapshot?.currentAction?.userId == value.userId,
        revealedCards: revealed?.holeCards ?? const [],
        handCategory: revealed?.category ?? '',
        timeExtensions: value.timeExtensions,
        isParticipating: value.participating,
      );
    });
  }

  List<Alignment> get _seatAlignments {
    final seats = _tableSeats;
    final currentIndex = seats.indexWhere((seat) => seat.isCurrentUser);
    final anchor = currentIndex < 0 ? 0 : currentIndex;
    return List.generate(widget.room.maxPlayers, (index) {
      final relativeIndex = (index - anchor) % widget.room.maxPlayers;
      final angle =
          math.pi / 2 + (math.pi * 2 * relativeIndex / widget.room.maxPlayers);
      return Alignment(math.cos(angle) * 0.94, math.sin(angle) * 0.88);
    });
  }

  Duration get _actionRemaining {
    final deadline = _gameSocket.snapshot?.currentAction?.deadline;
    if (deadline == null) return Duration.zero;
    final remaining = deadline.difference(_gameSocket.serverNow);
    return remaining.isNegative ? Duration.zero : remaining;
  }

  void _showVoiceError(Object error) {
    if (!mounted) return;
    final message = switch (error) {
      MicrophonePermissionDeniedException() => '需要麦克风权限才能开启自由麦',
      TrtcCredentialException(statusCode: 401) => '语音测试凭证鉴权失败',
      TrtcCredentialException() => '获取语音凭证失败，请检查本地服务',
      VoiceRoomException(:final code) => '加入语音房失败（错误码 $code）',
      TimeoutException() => '语音服务连接超时',
      UnsupportedError() => '当前平台的语音适配尚未完成',
      _ => '语音操作失败',
    };
    ScaffoldMessenger.of(context)
      ..hideCurrentSnackBar()
      ..showSnackBar(SnackBar(content: Text(message)));
  }
}

class _RoomHeader extends StatelessWidget {
  const _RoomHeader({
    required this.room,
    required this.onLeave,
    required this.onSettings,
  });

  final FriendRoom room;
  final Future<void> Function() onLeave;
  final VoidCallback onSettings;

  @override
  Widget build(BuildContext context) {
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Text(
              '${room.maxPlayers} 人好友牌桌',
              style: const TextStyle(fontSize: 22, fontWeight: FontWeight.w700),
            ),
            const SizedBox(width: 8),
            IconButton(
              onPressed: onLeave,
              icon: const Icon(Icons.exit_to_app, size: 20),
              tooltip: '离开房间',
            ),
            IconButton(
              onPressed: onSettings,
              icon: const Icon(Icons.settings_outlined, size: 20),
              tooltip: '声音与语音设置',
            ),
          ],
        ),
        const SizedBox(height: 2),
        Text(
          '房间码 ${room.code}  ·  盲注 ${room.rules.smallBlind}/${room.rules.bigBlind}',
          style: const TextStyle(color: Colors.white60),
        ),
      ],
    );
  }
}

class _ConnectionStatusBar extends StatelessWidget {
  const _ConnectionStatusBar({required this.client});

  final GameSocketClient client;

  @override
  Widget build(BuildContext context) {
    final (label, color) = switch (client.status) {
      GameSocketStatus.disconnected => ('服务端未连接', Colors.white54),
      GameSocketStatus.connecting => ('正在连接', Colors.orangeAccent),
      GameSocketStatus.connected => ('服务端已连接', const Color(0xFF6DE0A4)),
      GameSocketStatus.authenticated => ('身份已验证', const Color(0xFF6DE0A4)),
      GameSocketStatus.joined => ('牌桌已同步', const Color(0xFF6DE0A4)),
      GameSocketStatus.reconnecting => ('正在恢复牌桌', Colors.orangeAccent),
      GameSocketStatus.failed => ('连接失败', Colors.redAccent),
    };

    return Row(
      children: [
        Container(
          width: 8,
          height: 8,
          decoration: BoxDecoration(color: color, shape: BoxShape.circle),
        ),
        const SizedBox(width: 7),
        Text(label),
        const SizedBox(width: 8),
        if (client.status == GameSocketStatus.connected ||
            client.status == GameSocketStatus.authenticated ||
            client.status == GameSocketStatus.joined)
          TextButton(
            onPressed: client.sendPing,
            child: Text(client.lastMessageType ?? '测试连接'),
          )
        else
          TextButton(onPressed: client.connect, child: const Text('连接')),
      ],
    );
  }
}

class _VoiceControls extends StatelessWidget {
  const _VoiceControls({
    required this.voiceJoined,
    required this.connectionState,
    required this.microphoneEnabled,
    required this.operationInProgress,
    required this.speakingCount,
    required this.members,
    required this.speakingUserIds,
    required this.userId,
    required this.onJoinChanged,
    required this.onMicrophoneChanged,
  });

  final bool voiceJoined;
  final VoiceConnectionState connectionState;
  final bool microphoneEnabled;
  final bool operationInProgress;
  final int speakingCount;
  final List<TableVoiceMember> members;
  final Set<String> speakingUserIds;
  final String userId;
  final ValueChanged<bool> onJoinChanged;
  final ValueChanged<bool> onMicrophoneChanged;

  @override
  Widget build(BuildContext context) {
    return Row(
      children: [
        FilterChip(
          selected: voiceJoined,
          onSelected: operationInProgress ? null : onJoinChanged,
          avatar: Icon(
            voiceJoined ? Icons.headset : Icons.headset_off,
            size: 18,
          ),
          label: Text(switch (connectionState) {
            VoiceConnectionState.disconnected => '加入语音',
            VoiceConnectionState.connecting => '正在加入…',
            VoiceConnectionState.connected => '已加入语音',
            VoiceConnectionState.reconnecting => '语音重连中…',
          }),
        ),
        const SizedBox(width: 8),
        FilterChip(
          selected: microphoneEnabled,
          onSelected: voiceJoined && !operationInProgress
              ? onMicrophoneChanged
              : null,
          avatar: Icon(microphoneEnabled ? Icons.mic : Icons.mic_off, size: 18),
          label: Text(microphoneEnabled ? '自由麦已开启' : '麦克风关闭'),
        ),
        const SizedBox(width: 8),
        Text(
          '$userId · $speakingCount 人说话',
          style: const TextStyle(color: Colors.white60, fontSize: 12),
        ),
        const SizedBox(width: 4),
        PopupMenuButton<String>(
          tooltip: '语音成员',
          icon: Badge(
            label: Text('${members.length}'),
            child: const Icon(Icons.groups_2_outlined, size: 20),
          ),
          itemBuilder: (context) => members.isEmpty
              ? const [
                  PopupMenuItem<String>(
                    enabled: false,
                    child: Text('还没有人加入语音'),
                  ),
                ]
              : [
                  for (final member in members)
                    PopupMenuItem<String>(
                      enabled: false,
                      child: Row(
                        children: [
                          Icon(
                            member.microphoneEnabled
                                ? Icons.mic
                                : Icons.mic_off,
                            size: 17,
                            color: speakingUserIds.contains(member.userId)
                                ? const Color(0xFF6DE0A4)
                                : Colors.white54,
                          ),
                          const SizedBox(width: 8),
                          Expanded(child: Text(member.displayName)),
                          if (speakingUserIds.contains(member.userId))
                            const Text(
                              '说话中',
                              style: TextStyle(
                                color: Color(0xFF6DE0A4),
                                fontSize: 11,
                              ),
                            ),
                        ],
                      ),
                    ),
                ],
        ),
      ],
    );
  }
}

class _PokerTable extends StatelessWidget {
  const _PokerTable({
    required this.seats,
    required this.alignments,
    required this.snapshot,
    required this.actionRemaining,
  });

  final List<TableSeat> seats;
  final List<Alignment> alignments;
  final TableSnapshot? snapshot;
  final Duration actionRemaining;

  @override
  Widget build(BuildContext context) {
    return Stack(
      clipBehavior: Clip.none,
      children: [
        Positioned.fill(
          left: 70,
          right: 70,
          top: 54,
          bottom: 54,
          child: DecoratedBox(
            decoration: BoxDecoration(
              color: const Color(0xFF126344),
              borderRadius: BorderRadius.circular(240),
              border: Border.all(color: const Color(0xFF9B7838), width: 12),
              boxShadow: const [
                BoxShadow(
                  color: Colors.black54,
                  blurRadius: 30,
                  offset: Offset(0, 14),
                ),
                BoxShadow(color: Color(0xFF253C30), spreadRadius: 5),
              ],
            ),
            child: _BoardCenter(
              snapshot: snapshot,
              actionRemaining: actionRemaining,
            ),
          ),
        ),
        for (var index = 0; index < seats.length; index++)
          Align(
            alignment: alignments[index],
            child: _SeatCard(
              seat: seats[index],
              actionRemaining: actionRemaining,
            ),
          ),
        for (var index = 0; index < seats.length; index++)
          if (seats[index].streetBet > 0)
            Align(
              alignment: Alignment(
                alignments[index].x * 0.64,
                alignments[index].y * 0.74,
              ),
              child: _BetChip(amount: seats[index].streetBet),
            ),
        for (var index = 0; index < seats.length; index++)
          if (seats[index].revealedCards.isNotEmpty)
            Align(
              alignment: Alignment(
                alignments[index].x * 0.72,
                alignments[index].y * 0.62,
              ),
              child: _RevealedCardsBadge(seat: seats[index]),
            ),
      ],
    );
  }
}

class _BoardCenter extends StatelessWidget {
  const _BoardCenter({required this.snapshot, required this.actionRemaining});

  final TableSnapshot? snapshot;
  final Duration actionRemaining;

  @override
  Widget build(BuildContext context) {
    final board = snapshot?.board ?? const <String>[];
    final actor = snapshot?.currentAction;
    final actorName = snapshot?.seats
        .where((seat) => seat.userId == actor?.userId)
        .map((seat) => seat.displayName)
        .firstOrNull;
    return Column(
      mainAxisAlignment: MainAxisAlignment.center,
      children: [
        AnimatedSwitcher(
          duration: const Duration(milliseconds: 220),
          transitionBuilder: (child, animation) => ScaleTransition(
            scale: Tween<double>(begin: 0.88, end: 1).animate(animation),
            child: FadeTransition(opacity: animation, child: child),
          ),
          child: Text(
            '底池  ${snapshot?.totalPot ?? 0}',
            key: ValueKey(snapshot?.totalPot ?? 0),
            style: const TextStyle(color: Color(0xFFF6D986), fontSize: 18),
          ),
        ),
        if (snapshot?.settlement != null) ...[
          const SizedBox(height: 6),
          for (final award in snapshot!.settlement!.potAwards)
            Text(
              _potAwardLabel(award, snapshot!.seats),
              style: const TextStyle(
                color: Color(0xFFF6D986),
                fontSize: 12,
                fontWeight: FontWeight.w700,
              ),
            ),
        ],
        const SizedBox(height: 16),
        Row(
          mainAxisAlignment: MainAxisAlignment.center,
          children: List.generate(5, (index) {
            final card = index < board.length ? board[index] : null;
            return _PlayingCard(
              rank: card == null ? '?' : _cardRank(card),
              suit: card == null ? '' : _cardSuit(card),
              red: card != null && (card.endsWith('h') || card.endsWith('d')),
            );
          }),
        ),
        const SizedBox(height: 18),
        Text(
          actorName == null
              ? _phaseLabel(snapshot?.phase)
              : '轮到 $actorName 行动 · ${_remainingSeconds(actionRemaining)} 秒',
          style: const TextStyle(color: Colors.white70),
        ),
      ],
    );
  }
}

class _PlayingCard extends StatelessWidget {
  const _PlayingCard({
    required this.rank,
    required this.suit,
    this.red = false,
  });

  final String rank;
  final String suit;
  final bool red;

  @override
  Widget build(BuildContext context) {
    return AnimatedSwitcher(
      duration: const Duration(milliseconds: 260),
      switchInCurve: Curves.easeOutBack,
      transitionBuilder: (child, animation) => ScaleTransition(
        scale: Tween<double>(begin: 0.72, end: 1).animate(animation),
        child: FadeTransition(opacity: animation, child: child),
      ),
      child: Container(
        key: ValueKey('$rank$suit'),
        width: 58,
        height: 78,
        margin: const EdgeInsets.symmetric(horizontal: 4),
        padding: const EdgeInsets.all(7),
        decoration: BoxDecoration(
          color: rank == '?'
              ? const Color(0xFF234E43)
              : const Color(0xFFF4F0E7),
          borderRadius: BorderRadius.circular(8),
          border: Border.all(color: Colors.white30),
        ),
        child: Text(
          '$rank\n$suit',
          style: TextStyle(
            height: 1,
            color: rank == '?'
                ? Colors.white54
                : (red ? const Color(0xFFC63D45) : Colors.black87),
            fontSize: 18,
            fontWeight: FontWeight.w700,
          ),
        ),
      ),
    );
  }
}

class _SeatCard extends StatelessWidget {
  const _SeatCard({required this.seat, required this.actionRemaining});

  final TableSeat seat;
  final Duration actionRemaining;

  @override
  Widget build(BuildContext context) {
    final borderColor = seat.isCurrentActor
        ? const Color(0xFFFFA94D)
        : seat.isCurrentUser
        ? const Color(0xFFF4D477)
        : seat.isSpeaking
        ? const Color(0xFF6DE0A4)
        : Colors.white24;

    return AnimatedContainer(
      duration: const Duration(milliseconds: 240),
      curve: Curves.easeOut,
      width: 118,
      height: 76,
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 7),
      decoration: BoxDecoration(
        color: seat.isEmpty
            ? const Color(0x80112622)
            : seat.isFolded
            ? const Color(0xEE252D2A)
            : const Color(0xEE0D211D),
        borderRadius: BorderRadius.circular(13),
        border: Border.all(
          color: borderColor,
          width: seat.isCurrentActor || seat.isCurrentUser ? 2 : 1,
        ),
        boxShadow: [
          BoxShadow(
            color: seat.isCurrentActor
                ? const Color(0x66FFA94D)
                : Colors.black38,
            blurRadius: seat.isCurrentActor ? 16 : 8,
            spreadRadius: seat.isCurrentActor ? 2 : 0,
          ),
        ],
      ),
      child: Row(
        children: [
          CircleAvatar(
            radius: 18,
            backgroundColor: seat.isEmpty
                ? Colors.white10
                : const Color(0xFF315F51),
            child: seat.isEmpty
                ? const Icon(Icons.add, color: Colors.white54, size: 20)
                : Text(
                    seat.position.isEmpty ? '${seat.number}' : seat.position,
                    textAlign: TextAlign.center,
                    style: const TextStyle(
                      fontWeight: FontWeight.w700,
                      fontSize: 10,
                    ),
                  ),
          ),
          const SizedBox(width: 8),
          Expanded(
            child: Column(
              mainAxisAlignment: MainAxisAlignment.center,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Flexible(
                      child: Text(
                        seat.displayName,
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(fontWeight: FontWeight.w600),
                      ),
                    ),
                    if (seat.isDealer)
                      const Padding(
                        padding: EdgeInsets.only(left: 3),
                        child: Text(
                          'D',
                          style: TextStyle(color: Color(0xFFF4D477)),
                        ),
                      ),
                    if (seat.isSpeaking)
                      const Padding(
                        padding: EdgeInsets.only(left: 3),
                        child: Icon(
                          Icons.graphic_eq,
                          color: Color(0xFF6DE0A4),
                          size: 15,
                        ),
                      ),
                    if (seat.isReady && !seat.isDealer)
                      const Padding(
                        padding: EdgeInsets.only(left: 3),
                        child: Icon(
                          Icons.check_circle,
                          color: Color(0xFF6DE0A4),
                          size: 13,
                        ),
                      ),
                    if (seat.isParticipating)
                      Padding(
                        padding: const EdgeInsets.only(left: 3),
                        child: Text(
                          '⏱${seat.timeExtensions}',
                          style: const TextStyle(
                            color: Colors.white54,
                            fontSize: 9,
                          ),
                        ),
                      ),
                  ],
                ),
                Text(
                  seat.isEmpty
                      ? '空座位'
                      : seat.isAllIn
                      ? '${seat.chips} · 全下'
                      : !seat.isConnected
                      ? '${seat.chips} · 已断线'
                      : '${seat.chips} 筹码',
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(color: Colors.white60, fontSize: 11),
                ),
                if (!seat.isEmpty && seat.isCurrentActor)
                  Text(
                    '剩余 ${_remainingSeconds(actionRemaining)} 秒',
                    style: TextStyle(
                      color: _remainingSeconds(actionRemaining) <= 5
                          ? Colors.redAccent
                          : const Color(0xFFFFA94D),
                      fontSize: 10,
                      fontWeight: FontWeight.w700,
                    ),
                  )
                else if (!seat.isEmpty && seat.lastAction.isNotEmpty)
                  Text(
                    _actionLabel(seat.lastAction, seat.lastActionTo),
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(
                      color: Color(0xFFF6D986),
                      fontSize: 10,
                    ),
                  ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _BetChip extends StatelessWidget {
  const _BetChip({required this.amount});

  final int amount;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 4),
      decoration: BoxDecoration(
        color: const Color(0xFF2F223F),
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: const Color(0xFFE0B85B)),
        boxShadow: const [BoxShadow(color: Colors.black45, blurRadius: 5)],
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Icon(Icons.paid, size: 13, color: Color(0xFFF6D986)),
          const SizedBox(width: 3),
          Text('$amount', style: const TextStyle(fontWeight: FontWeight.w700)),
        ],
      ),
    );
  }
}

class _RevealedCardsBadge extends StatelessWidget {
  const _RevealedCardsBadge({required this.seat});

  final TableSeat seat;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 4),
      decoration: BoxDecoration(
        color: const Color(0xE60A1C18),
        borderRadius: BorderRadius.circular(8),
        border: Border.all(color: const Color(0xFFF6D986)),
      ),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              for (final card in seat.revealedCards)
                Padding(
                  padding: const EdgeInsets.symmetric(horizontal: 2),
                  child: _MiniCard(
                    label: '${_cardRank(card)}${_cardSuit(card)}',
                    compact: true,
                  ),
                ),
            ],
          ),
          Text(
            _handCategoryLabel(seat.handCategory),
            style: const TextStyle(fontSize: 9, color: Color(0xFFF6D986)),
          ),
        ],
      ),
    );
  }
}

class _ChatPanel extends StatefulWidget {
  const _ChatPanel({
    required this.client,
    required this.currentUserId,
    required this.blockedUserIds,
    required this.onBlockChanged,
    required this.onClose,
  });

  final GameSocketClient client;
  final String currentUserId;
  final Set<String> blockedUserIds;
  final void Function(String userId, bool blocked) onBlockChanged;
  final VoidCallback onClose;

  @override
  State<_ChatPanel> createState() => _ChatPanelState();
}

class _ChatPanelState extends State<_ChatPanel> {
  final _controller = TextEditingController();

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final messages = widget.client.chatMessages
        .where((message) => !widget.blockedUserIds.contains(message.userId))
        .toList(growable: false);
    return Card(
      color: const Color(0xE6112621),
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Row(
              children: [
                const Expanded(
                  child: Text(
                    '牌桌聊天',
                    style: TextStyle(fontWeight: FontWeight.w700),
                  ),
                ),
                if (widget.blockedUserIds.isNotEmpty)
                  IconButton(
                    onPressed: _showBlockedUsers,
                    icon: const Icon(Icons.person_off_outlined),
                    tooltip: '已屏蔽玩家',
                  ),
                IconButton(
                  onPressed: widget.onClose,
                  icon: const Icon(Icons.close),
                  tooltip: '关闭聊天',
                ),
              ],
            ),
            const Divider(height: 12),
            Expanded(
              child: messages.isEmpty
                  ? const Center(
                      child: Text(
                        '还没有消息',
                        style: TextStyle(color: Colors.white38),
                      ),
                    )
                  : ListView.builder(
                      itemCount: messages.length,
                      itemBuilder: (context, index) => _ChatLine(
                        name: messages[index].displayName,
                        message: messages[index].content,
                        canBlock:
                            messages[index].userId != widget.currentUserId,
                        onBlock: () =>
                            widget.onBlockChanged(messages[index].userId, true),
                      ),
                    ),
            ),
            TextField(
              controller: _controller,
              maxLength: 200,
              onSubmitted: (_) => _send(),
              decoration: InputDecoration(
                counterText: '',
                hintText: '输入牌桌消息',
                border: const OutlineInputBorder(),
                isDense: true,
                suffixIcon: IconButton(
                  onPressed: _send,
                  icon: const Icon(Icons.send),
                ),
              ),
            ),
            const SizedBox(height: 8),
            Wrap(
              spacing: 6,
              children: [
                ActionChip(
                  label: const Text('好牌'),
                  onPressed: () =>
                      widget.client.sendChat('好牌', kind: 'quick_text'),
                ),
                ActionChip(
                  label: const Text('快一点'),
                  onPressed: () =>
                      widget.client.sendChat('快一点', kind: 'quick_text'),
                ),
                ActionChip(
                  label: const Text('👍'),
                  onPressed: () => widget.client.sendChat('👍', kind: 'emoji'),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  void _send() {
    widget.client.sendChat(_controller.text);
    _controller.clear();
  }

  void _showBlockedUsers() {
    final seats = widget.client.snapshot?.seats ?? const <TableSeatSnapshot>[];
    showDialog<void>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('已屏蔽玩家'),
        content: SizedBox(
          width: 320,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              for (final userId in widget.blockedUserIds)
                ListTile(
                  title: Text(
                    seats
                            .where((seat) => seat.userId == userId)
                            .map((seat) => seat.displayName)
                            .firstOrNull ??
                        userId,
                  ),
                  subtitle: const Text('文字和语音均已屏蔽'),
                  trailing: TextButton(
                    onPressed: () {
                      widget.onBlockChanged(userId, false);
                      Navigator.of(context).pop();
                    },
                    child: const Text('取消屏蔽'),
                  ),
                ),
            ],
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(),
            child: const Text('关闭'),
          ),
        ],
      ),
    );
  }
}

class _ChatLine extends StatelessWidget {
  const _ChatLine({
    required this.name,
    required this.message,
    required this.canBlock,
    required this.onBlock,
  });

  final String name;
  final String message;
  final bool canBlock;
  final VoidCallback onBlock;

  @override
  Widget build(BuildContext context) {
    return Row(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Expanded(
          child: Padding(
            padding: const EdgeInsets.only(bottom: 9),
            child: Text.rich(
              TextSpan(
                children: [
                  TextSpan(
                    text: '$name：',
                    style: const TextStyle(color: Color(0xFFF4D477)),
                  ),
                  TextSpan(text: message),
                ],
              ),
            ),
          ),
        ),
        if (canBlock)
          PopupMenuButton<String>(
            padding: EdgeInsets.zero,
            iconSize: 16,
            tooltip: '消息选项',
            onSelected: (_) => onBlock(),
            itemBuilder: (context) => const [
              PopupMenuItem(value: 'block', child: Text('屏蔽此玩家')),
            ],
          ),
      ],
    );
  }
}

class _HandCardsPanel extends StatelessWidget {
  const _HandCardsPanel({required this.client, required this.userId});

  final GameSocketClient client;
  final String userId;

  @override
  Widget build(BuildContext context) {
    final snapshot = client.snapshot;
    final ownSeat = snapshot?.seats
        .where((seat) => seat.userId == userId)
        .firstOrNull;
    final ownTurn = snapshot?.currentAction?.userId == userId;
    final cards = snapshot?.holeCards ?? const <String>[];
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 14, vertical: 7),
      decoration: BoxDecoration(
        color: const Color(0xF20B211D),
        borderRadius: BorderRadius.circular(14),
        border: Border.all(
          color: ownTurn ? const Color(0xFFFFA94D) : Colors.white24,
          width: ownTurn ? 2 : 1,
        ),
        boxShadow: const [BoxShadow(color: Colors.black38, blurRadius: 10)],
      ),
      child: Row(
        mainAxisAlignment: MainAxisAlignment.center,
        children: [
          const Text('你的手牌'),
          const SizedBox(width: 10),
          if (cards.isEmpty)
            const Text('等待发牌', style: TextStyle(color: Colors.white38))
          else
            for (final card in cards) ...[
              _MiniCard(label: '${_cardRank(card)}${_cardSuit(card)}'),
              const SizedBox(width: 5),
            ],
          const SizedBox(width: 14),
          OutlinedButton.icon(
            onPressed:
                ownTurn &&
                    (ownSeat?.timeExtensions ?? 0) > 0 &&
                    snapshot?.currentAction?.deadline != null
                ? client.useTimeExtension
                : null,
            icon: const Icon(Icons.timer_outlined, size: 17),
            label: Text('加时 +30秒 ×${ownSeat?.timeExtensions ?? 0}'),
          ),
        ],
      ),
    );
  }
}

class _ActionBar extends StatelessWidget {
  const _ActionBar({required this.client, required this.userId});

  final GameSocketClient client;
  final String userId;

  @override
  Widget build(BuildContext context) {
    final snapshot = client.snapshot;
    final ownSeat = snapshot?.seats
        .where((seat) => seat.userId == userId)
        .firstOrNull;
    final current = snapshot?.currentAction;
    final ownTurn = current?.userId == userId;
    final options = ownTurn ? current!.options : null;
    final waiting =
        snapshot == null ||
        snapshot.phase == 'WAITING' ||
        snapshot.phase == 'WAITING_NEXT_HAND';
    return Container(
      height: 70,
      padding: const EdgeInsets.symmetric(horizontal: 14),
      decoration: BoxDecoration(
        color: const Color(0xED0A1C18),
        borderRadius: BorderRadius.circular(18),
        border: Border.all(color: Colors.white12),
      ),
      child: Row(
        children: [
          if (client.actionPending) ...[
            const SizedBox(
              width: 18,
              height: 18,
              child: CircularProgressIndicator(strokeWidth: 2),
            ),
            const SizedBox(width: 10),
          ],
          if (waiting)
            Expanded(
              child: Center(
                child: FilledButton.icon(
                  onPressed: client.status == GameSocketStatus.joined
                      ? () => client.setReady(!(ownSeat?.ready ?? false))
                      : null,
                  icon: Icon(
                    ownSeat?.ready == true
                        ? Icons.pause_circle
                        : Icons.play_circle,
                  ),
                  label: Text(ownSeat?.ready == true ? '取消准备' : '准备开始'),
                ),
              ),
            )
          else if (!ownTurn)
            const Expanded(
              child: Center(
                child: Text(
                  '等待其他玩家行动',
                  style: TextStyle(color: Colors.white60),
                ),
              ),
            )
          else
            Expanded(
              child: LayoutBuilder(
                builder: (context, constraints) => SingleChildScrollView(
                  scrollDirection: Axis.horizontal,
                  child: ConstrainedBox(
                    constraints: BoxConstraints(minWidth: constraints.maxWidth),
                    child: Row(
                      mainAxisSize: MainAxisSize.min,
                      mainAxisAlignment: MainAxisAlignment.center,
                      children: [
                        OutlinedButton(
                          onPressed: options!.canFold && !client.actionPending
                              ? () => client.submitAction('fold')
                              : null,
                          child: const Text('弃牌'),
                        ),
                        const SizedBox(width: 7),
                        FilledButton.tonal(
                          onPressed: client.actionPending
                              ? null
                              : options.canCheck
                              ? () => client.submitAction('check')
                              : options.canCall
                              ? () => client.submitAction('call')
                              : null,
                          child: Text(
                            options.canCheck ? '过牌' : '跟注 ${options.toCall}',
                          ),
                        ),
                        const SizedBox(width: 7),
                        for (final suggestion in current!.suggestions) ...[
                          FilledButton.tonal(
                            onPressed: client.actionPending
                                ? null
                                : () => client.submitAction(
                                    suggestion.action,
                                    raiseTo: suggestion.action == 'all_in'
                                        ? null
                                        : suggestion.raiseTo,
                                  ),
                            child: Text(
                              suggestion.action == 'all_in'
                                  ? '全下\n${suggestion.raiseTo}'
                                  : '${_suggestionLabel(suggestion.label)}\n${suggestion.raiseTo}',
                              textAlign: TextAlign.center,
                            ),
                          ),
                          const SizedBox(width: 7),
                        ],
                        OutlinedButton.icon(
                          onPressed:
                              client.actionPending ||
                                  (!options.canBet && !options.canRaise)
                              ? null
                              : () => showDialog<void>(
                                  context: context,
                                  builder: (context) => _BetAmountDialog(
                                    client: client,
                                    options: options,
                                    suggestions: current.suggestions,
                                    streetBet: ownSeat?.streetBet ?? 0,
                                    onSubmit: (action, raiseTo) => client
                                        .submitAction(action, raiseTo: raiseTo),
                                  ),
                                ),
                          icon: const Icon(Icons.tune, size: 18),
                          label: const Text('自定义'),
                        ),
                      ],
                    ),
                  ),
                ),
              ),
            ),
        ],
      ),
    );
  }
}

class _BetAmountDialog extends StatefulWidget {
  const _BetAmountDialog({
    required this.client,
    required this.options,
    required this.suggestions,
    required this.streetBet,
    required this.onSubmit,
  });

  final GameSocketClient client;
  final TableActionOptions options;
  final List<BetSuggestion> suggestions;
  final int streetBet;
  final void Function(String action, int raiseTo) onSubmit;

  @override
  State<_BetAmountDialog> createState() => _BetAmountDialogState();
}

class _BetAmountDialogState extends State<_BetAmountDialog> {
  late int _amount;
  late final TextEditingController _controller;
  String? _error;
  Timer? _clock;

  @override
  void initState() {
    super.initState();
    _amount = widget.options.minRaiseTo;
    _controller = TextEditingController(text: '$_amount');
    widget.client.addListener(_refresh);
    _clock = Timer.periodic(
      const Duration(milliseconds: 200),
      (_) => _refresh(),
    );
  }

  @override
  void dispose() {
    _clock?.cancel();
    widget.client.removeListener(_refresh);
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final options = widget.options;
    final action = options.canBet ? 'bet' : 'raise';
    final actionLabel = options.canBet ? '下注' : '加注';
    final current = widget.client.snapshot?.currentAction;
    final deadline = current?.deadline;
    final remaining = deadline == null
        ? Duration.zero
        : deadline.difference(widget.client.serverNow);
    final ownSeat = widget.client.snapshot?.seats
        .where((seat) => seat.userId == widget.client.userId)
        .firstOrNull;
    return AlertDialog(
      title: Text('$actionLabel额度'),
      content: SizedBox(
        width: 520,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 9),
              decoration: BoxDecoration(
                color: const Color(0xFF172D28),
                borderRadius: BorderRadius.circular(10),
                border: Border.all(
                  color: _remainingSeconds(remaining) <= 5
                      ? Colors.redAccent
                      : const Color(0xFFFFA94D),
                ),
              ),
              child: Row(
                children: [
                  const Icon(Icons.timer_outlined, size: 20),
                  const SizedBox(width: 7),
                  Text(
                    '剩余 ${_remainingSeconds(remaining)} 秒',
                    style: const TextStyle(fontWeight: FontWeight.w700),
                  ),
                  const Spacer(),
                  OutlinedButton(
                    onPressed:
                        (ownSeat?.timeExtensions ?? 0) > 0 &&
                            current?.userId == widget.client.userId
                        ? widget.client.useTimeExtension
                        : null,
                    child: Text('加时 +30秒 ×${ownSeat?.timeExtensions ?? 0}'),
                  ),
                ],
              ),
            ),
            const SizedBox(height: 12),
            Text(
              '允许范围：${options.minRaiseTo} ～ ${options.maxRaiseTo}',
              style: const TextStyle(color: Colors.white60),
            ),
            const SizedBox(height: 10),
            if (options.maxRaiseTo > options.minRaiseTo)
              Slider(
                value: _amount
                    .clamp(options.minRaiseTo, options.maxRaiseTo)
                    .toDouble(),
                min: options.minRaiseTo.toDouble(),
                max: options.maxRaiseTo.toDouble(),
                label: '$_amount',
                onChanged: (value) => _setAmount(value.round()),
              ),
            TextField(
              controller: _controller,
              keyboardType: TextInputType.number,
              inputFormatters: [FilteringTextInputFormatter.digitsOnly],
              decoration: InputDecoration(
                labelText: '$actionLabel至',
                border: const OutlineInputBorder(),
                errorText: _error,
              ),
              onChanged: (value) {
                final parsed = int.tryParse(value);
                if (parsed != null) {
                  setState(() {
                    _amount = parsed;
                    _error = null;
                  });
                }
              },
            ),
            const SizedBox(height: 14),
            Wrap(
              spacing: 7,
              runSpacing: 7,
              children: [
                for (final suggestion in widget.suggestions)
                  ActionChip(
                    label: Text(_suggestionLabel(suggestion.label)),
                    onPressed: () {
                      if (suggestion.action == 'all_in') {
                        Navigator.of(context).pop();
                        widget.onSubmit('all_in', suggestion.raiseTo);
                      } else {
                        _setAmount(suggestion.raiseTo);
                      }
                    },
                  ),
              ],
            ),
            const SizedBox(height: 14),
            Text(
              '$actionLabel至 $_amount · 本次还需投入 ${math.max(0, _amount - widget.streetBet)}',
              style: const TextStyle(
                color: Color(0xFFF6D986),
                fontWeight: FontWeight.w700,
              ),
            ),
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('取消'),
        ),
        FilledButton(
          onPressed: () {
            final entered = int.tryParse(_controller.text);
            if (entered == null ||
                entered < options.minRaiseTo ||
                entered > options.maxRaiseTo) {
              setState(() => _error = '请输入允许范围内的额度');
              return;
            }
            Navigator.of(context).pop();
            widget.onSubmit(action, entered);
          },
          child: Text('$actionLabel至 $_amount'),
        ),
      ],
    );
  }

  void _setAmount(int value) {
    final clamped = value.clamp(
      widget.options.minRaiseTo,
      widget.options.maxRaiseTo,
    );
    setState(() {
      _amount = clamped;
      _controller.text = '$clamped';
      _error = null;
    });
  }

  void _refresh() {
    if (!mounted) return;
    if (widget.client.snapshot?.currentAction?.userId != widget.client.userId) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) Navigator.of(context).maybePop();
      });
      return;
    }
    setState(() {});
  }
}

String _cardRank(String card) {
  if (card.length != 2) return '?';
  return switch (card[0]) {
    'T' => '10',
    'J' => 'J',
    'Q' => 'Q',
    'K' => 'K',
    'A' => 'A',
    final value => value,
  };
}

int _remainingSeconds(Duration duration) {
  if (duration <= Duration.zero) return 0;
  return (duration.inMilliseconds / 1000).ceil();
}

String _actionLabel(String action, int actionTo) => switch (action) {
  'fold' => '弃牌',
  'check' => '过牌',
  'call' => '跟注至 $actionTo',
  'bet' => '下注至 $actionTo',
  'raise' => '加注至 $actionTo',
  'all_in' => '全下至 $actionTo',
  _ => action,
};

String _suggestionLabel(String label) => switch (label) {
  'quarter_pot' => '1/4 池',
  'third_pot' => '1/3 池',
  'half_pot' => '1/2 池',
  'two_thirds_pot' => '2/3 池',
  'pot' => '满池',
  'overbet_120' => '1.2× 超池',
  'all_in' => '全下',
  _ => label,
};

String _handCategoryLabel(String category) => switch (category) {
  'high_card' => '高牌',
  'one_pair' => '一对',
  'two_pair' => '两对',
  'three_of_a_kind' => '三条',
  'straight' => '顺子',
  'flush' => '同花',
  'full_house' => '葫芦',
  'four_of_a_kind' => '四条',
  'straight_flush' => '同花顺',
  _ => category,
};

String _potAwardLabel(PotAward award, List<TableSeatSnapshot> seats) {
  final names = award.payouts
      .map((payout) {
        final name = seats
            .where((seat) => seat.userId == payout.userId)
            .map((seat) => seat.displayName)
            .firstOrNull;
        return '${name ?? payout.userId} +${payout.amount}';
      })
      .join('、');
  final potName = award.potIndex == 0 ? '主池' : '边池 ${award.potIndex}';
  return '$potName ${award.amount}：$names';
}

String _gameErrorLabel(String code) => switch (code) {
  'sequence_gap' => '检测到网络消息缺口，正在恢复牌桌状态',
  'stale_revision' || 'stale_hand' => '牌桌状态已经更新，正在为你恢复最新画面',
  'not_your_turn' => '现在还没有轮到你',
  'illegal_action' => '当前不能执行这个操作',
  'invalid_amount' => '下注额度不在允许范围内',
  'rate_limited' => '消息发送太快，请稍后再试',
  'content_rejected' => '消息内容不符合要求',
  'authentication_required' => '登录状态已失效，请重新登录',
  'no_time_extensions' => '本手的两张加时卡已经用完',
  'time_extension_expired' => '本次行动已经超时，无法再主动加时',
  _ when code.startsWith('invalid_server_message') => '收到的牌桌数据无法解析',
  _ => '牌桌操作失败（$code）',
};

String _cardSuit(String card) {
  if (card.length != 2) return '';
  return switch (card[1]) {
    'c' => '♣',
    'd' => '♦',
    'h' => '♥',
    's' => '♠',
    _ => '',
  };
}

String _phaseLabel(String? phase) => switch (phase) {
  null => '正在同步牌桌…',
  'WAITING' => '等待玩家准备',
  'WAITING_NEXT_HAND' => '本手已结算，等待下一手',
  'PREFLOP' => '翻牌前',
  'FLOP' => '翻牌圈',
  'TURN' => '转牌圈',
  'RIVER' => '河牌圈',
  'SHOWDOWN' => '摊牌',
  _ => phase,
};

class _MiniCard extends StatelessWidget {
  const _MiniCard({required this.label, this.compact = false});

  final String label;
  final bool compact;

  @override
  Widget build(BuildContext context) {
    return Container(
      width: compact ? 27 : 38,
      height: compact ? 34 : 48,
      alignment: Alignment.center,
      decoration: BoxDecoration(
        color: const Color(0xFFF4F0E7),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Text(
        label,
        style: TextStyle(
          color: Colors.black87,
          fontWeight: FontWeight.w800,
          fontSize: compact ? 10 : null,
        ),
      ),
    );
  }
}
