import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:poker_client/core/auth/auth_session.dart';
import 'package:poker_client/core/network/game_socket_client.dart';
import 'package:poker_client/core/network/trtc_credential_client.dart';
import 'package:poker_client/core/platform/native_display_cutout.dart';
import 'package:poker_client/core/platform/voice_chat_service.dart';
import 'package:poker_client/core/platform/voice_chat_service_factory.dart';
import 'package:poker_client/core/settings/app_settings.dart';
import 'package:poker_client/core/settings/settings_dialog.dart';
import 'package:poker_client/core/widgets/platform_number_field.dart';
import 'package:poker_client/features/admin/presentation/admin_page.dart';
import 'package:poker_client/features/bankroll/domain/bankroll_snapshot.dart';
import 'package:poker_client/features/lobby/domain/friend_room.dart';
import 'package:poker_client/features/table/audio/table_action_sound_tracker.dart';
import 'package:poker_client/features/table/audio/table_sound_effects.dart';
import 'package:poker_client/features/table/domain/hand_category_label.dart';
import 'package:poker_client/features/table/domain/table_seat.dart';
import 'package:poker_client/features/table/domain/table_snapshot.dart';
import 'package:poker_client/features/table/presentation/responsive_action_strip.dart';
import 'package:poker_client/features/table/presentation/table_viewport_layout.dart';

class TablePrototypePage extends StatefulWidget {
  const TablePrototypePage({
    required this.session,
    required this.room,
    required this.settings,
    required this.onLeave,
    required this.onRemoved,
    required this.accessTokenProvider,
    required this.loadBankroll,
    super.key,
  });

  final AuthSession session;
  final FriendRoom room;
  final AppSettingsController settings;
  final Future<void> Function() onLeave;
  final Future<void> Function() onRemoved;
  final Future<String> Function({bool forceRefresh}) accessTokenProvider;
  final Future<BankrollSnapshot> Function() loadBankroll;

  @override
  State<TablePrototypePage> createState() => _TablePrototypePageState();
}

class _TablePrototypePageState extends State<TablePrototypePage>
    with WidgetsBindingObserver {
  static const _gameHttpServerUrl = String.fromEnvironment(
    'GAME_HTTP_SERVER_URL',
    defaultValue: 'http://127.0.0.1:8080',
  );

  VoiceConnectionState _voiceState = VoiceConnectionState.disconnected;
  bool _microphoneEnabled = false;
  bool _voiceOperationInProgress = false;
  Set<String> _speakingUserIds = const {};
  final Set<String> _blockedUserIds = {};
  final Set<String> _mutedVoiceUserIds = {};
  bool _chatVisible = true;
  bool _compactChatOpen = false;
  int _observedChatRevision = 0;
  int _unreadChatCount = 0;
  late final GameSocketClient _gameSocket;
  late final TrtcCredentialClient _trtcCredentials;
  late final VoiceChatService _voiceChat;
  late final StreamSubscription<VoiceConnectionState> _voiceStateSubscription;
  late final StreamSubscription<Set<String>> _speakingSubscription;
  Timer? _tableClock;
  String? _lastShownGameError;
  GameSocketStatus? _lastGameSocketStatus;
  final TableActionSoundTracker _actionSoundTracker = TableActionSoundTracker();
  final TableSoundEffects _tableSoundEffects = TableSoundEffects();
  bool _autoJoinAttempted = false;
  bool _rebuyDialogOpen = false;
  String? _autoRebuyHandId;
  bool _removedFromRoomHandled = false;
  String? _autoReadySubmittedHandId;
  final Set<String> _handledTableRequestIds = {};
  bool _tableRequestDialogOpen = false;
  final Set<String> _observedInteractionIds = {};
  final List<TablePlayerInteraction> _activeInteractions = [];
  final Map<String, Timer> _interactionTimers = {};
  EdgeInsets _nativeDisplayCutout = EdgeInsets.zero;

  bool get _voiceJoined =>
      _voiceState == VoiceConnectionState.connected ||
      _voiceState == VoiceConnectionState.reconnecting;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _gameSocket = GameSocketClient(
      accessTokenProvider: widget.accessTokenProvider,
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
      final snapshot = _gameSocket.snapshot;
      _submitExpiredAutoReady(snapshot);
      if (mounted &&
          (snapshot?.currentAction?.deadline != null ||
              snapshot?.autoReadyDeadline != null ||
              snapshot?.runoutChoice?.deadline != null)) {
        setState(() {});
      }
    });
    widget.settings.addListener(_settingsChanged);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _settingsChanged();
      unawaited(_refreshDisplayCutout());
    });
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _gameSocket
      ..removeListener(_refresh)
      ..dispose();
    unawaited(_voiceStateSubscription.cancel());
    unawaited(_speakingSubscription.cancel());
    unawaited(_voiceChat.dispose());
    unawaited(_tableSoundEffects.dispose());
    _tableClock?.cancel();
    for (final timer in _interactionTimers.values) {
      timer.cancel();
    }
    _trtcCredentials.close();
    widget.settings.removeListener(_settingsChanged);
    super.dispose();
  }

  @override
  void didChangeMetrics() {
    WidgetsBinding.instance.addPostFrameCallback((_) {
      unawaited(_refreshDisplayCutout());
    });
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      unawaited(_refreshDisplayCutout());
    }
  }

  Future<void> _refreshDisplayCutout() async {
    final next = await NativeDisplayCutout.read();
    if (mounted && next != _nativeDisplayCutout) {
      setState(() => _nativeDisplayCutout = next);
    }
  }

  @override
  Widget build(BuildContext context) {
    final mediaPadding = MediaQuery.paddingOf(context);
    final remainingCutout = NativeDisplayCutout.remainingAfter(
      _nativeDisplayCutout,
      mediaPadding,
    );
    return Scaffold(
      body: DecoratedBox(
        decoration: const BoxDecoration(
          gradient: RadialGradient(
            colors: [Color(0xFF16473B), Color(0xFF061814)],
            radius: 1.1,
          ),
        ),
        child: SafeArea(
          child: Padding(
            padding: remainingCutout,
            child: LayoutBuilder(
              builder: (context, constraints) {
                final viewport = TableViewportLayout.fromSize(
                  constraints.biggest,
                  chatVisible: _chatVisible,
                );
                final showSideChat = _chatVisible && viewport.supportsSideChat;
                final roomHeader = _RoomHeader(
                  room: widget.room,
                  currentPlayers:
                      _gameSocket.snapshot?.seats.length ??
                      widget.room.members.length,
                  compact: viewport.isCompactLandscape,
                  onLeave: _leaveTable,
                  onSettings: () => showAppSettingsDialog(
                    context,
                    widget.settings,
                    onOpenAdmin: widget.session.user.isAdmin
                        ? _openAdmin
                        : null,
                  ),
                );
                final connectionStatus = _ConnectionStatusBar(
                  client: _gameSocket,
                  compact: viewport.isCompactLandscape,
                );
                final voiceControls = _VoiceControls(
                  voiceJoined: _voiceJoined,
                  connectionState: _voiceState,
                  microphoneEnabled: _microphoneEnabled,
                  operationInProgress: _voiceOperationInProgress,
                  speakingCount: _speakingUserIds
                      .difference(_mutedVoiceUserIds)
                      .length,
                  members: _gameSocket.voiceMembers,
                  speakingUserIds: _speakingUserIds,
                  mutedUserIds: _mutedVoiceUserIds,
                  currentUserId: widget.session.user.userId,
                  displayName: widget.session.user.displayName,
                  compact: viewport.isCompactLandscape,
                  onJoinChanged: _setVoiceJoined,
                  onMicrophoneChanged: _setMicrophoneEnabled,
                  onUserMuted: _setVoiceUserMuted,
                );
                return Align(
                  alignment: viewport.isCompactLandscape
                      ? Alignment.bottomCenter
                      : Alignment.center,
                  child: FittedBox(
                    fit: BoxFit.contain,
                    child: SizedBox.fromSize(
                      size: viewport.canvasSize,
                      child: Stack(
                        children: [
                          if (viewport.isCompactLandscape)
                            Positioned(
                              left: 8,
                              top: 8,
                              width:
                                  TableViewportLayout.compactLeftRailWidth - 16,
                              child: DecoratedBox(
                                decoration: BoxDecoration(
                                  color: const Color(0x99102620),
                                  borderRadius: BorderRadius.circular(14),
                                  border: Border.all(color: Colors.white12),
                                ),
                                child: Padding(
                                  padding: const EdgeInsets.all(9),
                                  child: Column(
                                    crossAxisAlignment:
                                        CrossAxisAlignment.stretch,
                                    children: [
                                      roomHeader,
                                      const Divider(height: 14),
                                      connectionStatus,
                                      const Divider(height: 14),
                                      voiceControls,
                                    ],
                                  ),
                                ),
                              ),
                            )
                          else ...[
                            Positioned(left: 24, top: 18, child: roomHeader),
                            Positioned(
                              left: 0,
                              right: 0,
                              top: 16,
                              child: Center(child: connectionStatus),
                            ),
                            Positioned(
                              right: 24,
                              top: 16,
                              child: voiceControls,
                            ),
                          ],
                          Positioned.fromRect(
                            rect: viewport.tableRect,
                            child: _PokerTable(
                              seats: _tableSeats,
                              alignments: _seatAlignments(viewport),
                              boardRect: viewport.boardRect.shift(
                                -viewport.tableRect.topLeft,
                              ),
                              snapshot: _gameSocket.snapshot,
                              actionRemaining: _actionRemaining,
                              onSeatTap: _handleSeatTap,
                              onAvatarTap: _handleAvatarTap,
                              onUseTimeExtension: _gameSocket.useTimeExtension,
                              interactions: _activeInteractions,
                            ),
                          ),
                          if (showSideChat)
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
                              bottom: viewport.isCompactLandscape ? 86 : 118,
                              child: FilledButton.tonalIcon(
                                onPressed: viewport.supportsSideChat
                                    ? _toggleChat
                                    : _showCompactChat,
                                icon: Badge(
                                  isLabelVisible: _unreadChatCount > 0,
                                  label: Text(
                                    _unreadChatCount > 99
                                        ? '99+'
                                        : '$_unreadChatCount',
                                  ),
                                  child: const Icon(Icons.chat_bubble_outline),
                                ),
                                label: Text(
                                  _unreadChatCount > 0
                                      ? '文字聊天 · $_unreadChatCount 条新消息'
                                      : '文字聊天',
                                ),
                              ),
                            ),
                          Positioned(
                            left: viewport.isCompactLandscape ? 12 : 24,
                            right: viewport.isCompactLandscape ? 12 : 24,
                            bottom: viewport.isCompactLandscape ? 2 : 18,
                            child: _ActionBar(
                              client: _gameSocket,
                              userId: widget.session.user.userId,
                              smallBlind: widget.room.rules.smallBlind,
                              onRebuy: _showRebuyDialog,
                            ),
                          ),
                        ],
                      ),
                    ),
                  ),
                );
              },
            ),
          ),
        ),
      ),
    );
  }

  Future<void> _openAdmin() => Navigator.of(context).push(
    MaterialPageRoute<void>(
      builder: (_) =>
          AdminPage(accessTokenProvider: widget.accessTokenProvider),
    ),
  );

  Future<void> _setVoiceJoined(bool value) async {
    if (_voiceOperationInProgress) return;
    setState(() => _voiceOperationInProgress = true);
    try {
      if (value) {
        final accessToken = await widget.accessTokenProvider();
        final credentials = await _trtcCredentials.issue(
          userId: widget.session.user.userId,
          roomId: widget.room.roomId,
          accessToken: accessToken,
        );
        await _voiceChat.joinTableChannel(
          sdkAppId: credentials.sdkAppId,
          tableId: credentials.roomId,
          userId: credentials.userId,
          userSig: credentials.userSig,
        );
        await _voiceChat.setPlaybackVolume(widget.settings.voiceVolume);
        for (final userId in {..._blockedUserIds, ..._mutedVoiceUserIds}) {
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

  void _toggleChat() => setState(() {
    _chatVisible = !_chatVisible;
    if (_chatVisible) _unreadChatCount = 0;
  });

  Future<void> _showCompactChat() async {
    final mediaSize = MediaQuery.sizeOf(context);
    setState(() {
      _compactChatOpen = true;
      _unreadChatCount = 0;
    });
    try {
      await showDialog<void>(
        context: context,
        builder: (dialogContext) => Dialog(
          insetPadding: const EdgeInsets.all(12),
          backgroundColor: Colors.transparent,
          child: SizedBox(
            width: math.min(520, mediaSize.width * 0.72),
            height: math.min(520, mediaSize.height * 0.88),
            child: _ChatPanel(
              client: _gameSocket,
              currentUserId: widget.session.user.userId,
              blockedUserIds: _blockedUserIds,
              onBlockChanged: _setUserBlocked,
              onClose: () => Navigator.of(dialogContext).pop(),
            ),
          ),
        ),
      );
    } finally {
      if (mounted) setState(() => _compactChatOpen = false);
    }
  }

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
      await _voiceChat.setRemoteUserMuted(
        userId,
        blocked || _mutedVoiceUserIds.contains(userId),
      );
    } on Object catch (error) {
      _showVoiceError(error);
    }
  }

  Future<void> _setVoiceUserMuted(String userId, bool muted) async {
    if (userId == widget.session.user.userId) return;
    setState(() {
      if (muted) {
        _mutedVoiceUserIds.add(userId);
      } else {
        _mutedVoiceUserIds.remove(userId);
      }
    });
    if (!_voiceJoined) return;
    try {
      await _voiceChat.setRemoteUserMuted(
        userId,
        muted || _blockedUserIds.contains(userId),
      );
    } on Object catch (error) {
      _showVoiceError(error);
    }
  }

  void _refresh() {
    if (!mounted) return;
    _updateChatNotification();
    _updatePlayerInteractions();
    setState(() {});
    final error = _gameSocket.errorMessage;
    if (!_removedFromRoomHandled &&
        (error == 'permission_denied' || error == 'room_not_found')) {
      _removedFromRoomHandled = true;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) unawaited(widget.onRemoved());
      });
    }
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
    _offerAutomaticRebuy();
    _offerPendingTableRequest();
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

  void _updateChatNotification() {
    final revision = _gameSocket.chatEventRevision;
    if (revision == _observedChatRevision) return;
    _observedChatRevision = revision;
    final message = _gameSocket.latestChatEvent;
    if (message == null || message.userId == widget.session.user.userId) return;
    final size = MediaQuery.sizeOf(context);
    final sideChatVisible =
        _chatVisible &&
        TableViewportLayout.fromSize(size, chatVisible: true).supportsSideChat;
    if (!_compactChatOpen && !sideChatVisible) _unreadChatCount++;
  }

  void _updatePlayerInteractions() {
    for (final interaction in _gameSocket.playerInteractions) {
      if (!_observedInteractionIds.add(interaction.interactionId)) continue;
      _activeInteractions.add(interaction);
      if (widget.settings.soundEnabled) {
        unawaited(
          _tableSoundEffects.play(
            interaction.kind == 'praise'
                ? TableSoundEffect.praise
                : TableSoundEffect.taunt,
          ),
        );
      }
      _interactionTimers[interaction.interactionId] = Timer(
        const Duration(milliseconds: 1900),
        () {
          _interactionTimers.remove(interaction.interactionId);
          _activeInteractions.removeWhere(
            (value) => value.interactionId == interaction.interactionId,
          );
          if (mounted) setState(() {});
        },
      );
    }
  }

  void _submitExpiredAutoReady(TableSnapshot? snapshot) {
    final deadline = snapshot?.autoReadyDeadline;
    if (snapshot == null || deadline == null) {
      return;
    }
    final ownSeat = snapshot.seats
        .where((seat) => seat.userId == widget.session.user.userId)
        .firstOrNull;
    if (deadline.isAfter(_gameSocket.serverNow) ||
        snapshot.autoReadyCancelled ||
        ownSeat == null ||
        ownSeat.ready ||
        ownSeat.stack <= 0 ||
        _gameSocket.status != GameSocketStatus.joined ||
        _autoReadySubmittedHandId == snapshot.handId) {
      return;
    }
    _autoReadySubmittedHandId = snapshot.handId;
    _gameSocket.setReady(true);
  }

  void _offerPendingTableRequest() {
    if (_tableRequestDialogOpen) return;
    final snapshot = _gameSocket.snapshot;
    if (snapshot == null) return;
    PendingTableRequest? request;
    var holeCards = false;
    for (final candidate in snapshot.holeCardViewRequests) {
      if (!_handledTableRequestIds.contains(candidate.requestId)) {
        request = candidate;
        holeCards = true;
        break;
      }
    }
    if (request == null) {
      for (final candidate in snapshot.seatSwapRequests) {
        if (!_handledTableRequestIds.contains(candidate.requestId)) {
          request = candidate;
          break;
        }
      }
    }
    if (request == null) return;
    _handledTableRequestIds.add(request.requestId);
    _tableRequestDialogOpen = true;
    final pending = request;
    final requesterName = snapshot.seats
        .where((seat) => seat.userId == pending.requesterUserId)
        .map((seat) => seat.displayName)
        .firstOrNull;
    WidgetsBinding.instance.addPostFrameCallback((_) async {
      if (!mounted) return;
      final accepted = await showDialog<bool>(
        context: context,
        barrierDismissible: false,
        builder: (context) => AlertDialog(
          title: Text(holeCards ? '查看手牌申请' : '换位申请'),
          content: Text(
            holeCards
                ? '${requesterName ?? '一名玩家'}已弃牌，申请提前查看你的手牌。是否同意？'
                : '${requesterName ?? '一名玩家'}申请与你交换座位。是否同意？',
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(context).pop(false),
              child: const Text('拒绝'),
            ),
            FilledButton(
              onPressed: () => Navigator.of(context).pop(true),
              child: const Text('同意'),
            ),
          ],
        ),
      );
      if (holeCards) {
        _gameSocket.respondHoleCardsView(pending.requestId, accepted == true);
      } else {
        _gameSocket.respondSeatSwap(pending.requestId, accepted == true);
      }
      _tableRequestDialogOpen = false;
      if (mounted) _offerPendingTableRequest();
    });
  }

  Future<void> _handleSeatTap(TableSeat seat) async {
    final snapshot = _gameSocket.snapshot;
    if (snapshot == null) return;
    final waiting =
        snapshot.phase == 'WAITING' || snapshot.phase == 'WAITING_NEXT_HAND';
    if (waiting) {
      final full = snapshot.seats.length >= 10;
      if (seat.isCurrentUser && !full) {
        await _showEmptySeatPicker(snapshot, seat.number);
        return;
      }
      if (seat.isCurrentUser) return;
      if (!full) {
        _showTableHint('牌桌未坐满时，请点击自己的玩家框选择空座位');
        return;
      }
      if (full) {
        final confirmed = await _confirmTableAction(
          '申请换位',
          '向 ${seat.displayName} 发出交换座位申请？',
        );
        if (confirmed) _gameSocket.requestSeatChange(seat.number);
        return;
      }
    }
    if (seat.isCurrentUser) return;
    final ownSeat = snapshot.seats
        .where((value) => value.userId == widget.session.user.userId)
        .firstOrNull;
    final target = snapshot.seats
        .where((value) => value.userId == seat.userId)
        .firstOrNull;
    if (ownSeat?.folded != true || target?.participating != true) return;
    final confirmed = await _confirmTableAction(
      '申请查看手牌',
      '向 ${seat.displayName} 申请提前查看他的手牌？只有对方同意后你才能看到。',
    );
    if (confirmed) _gameSocket.requestHoleCardsView(seat.userId);
  }

  Future<void> _showEmptySeatPicker(
    TableSnapshot snapshot,
    int currentSeat,
  ) async {
    final occupied = snapshot.seats.map((seat) => seat.seat).toSet();
    final available = [
      for (var number = 1; number <= 10; number++)
        if (!occupied.contains(number)) number,
    ];
    if (available.isEmpty) return;
    final selected = await showDialog<int>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('选择空座位'),
        content: Wrap(
          spacing: 8,
          runSpacing: 8,
          children: [
            for (final number in available)
              OutlinedButton(
                onPressed: () => Navigator.of(context).pop(number),
                child: Text('$number 号位'),
              ),
          ],
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(),
            child: const Text('取消'),
          ),
        ],
      ),
    );
    if (selected == null || selected == currentSeat) return;
    _gameSocket.requestSeatChange(selected);
  }

  Future<void> _handleAvatarTap(TableSeat seat) async {
    if (seat.isEmpty || seat.isCurrentUser || seat.userId.isEmpty) return;
    final kind = await showDialog<String>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text('与 ${seat.displayName} 互动'),
        content: const Text('牌桌上的所有玩家都会看到动画并听到互动音效。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(context).pop(),
            child: const Text('取消'),
          ),
          FilledButton.tonalIcon(
            onPressed: () => Navigator.of(context).pop('taunt'),
            icon: const Text('😜'),
            label: const Text('嘲讽'),
          ),
          FilledButton.icon(
            onPressed: () => Navigator.of(context).pop('praise'),
            icon: const Icon(Icons.thumb_up_alt),
            label: const Text('赞赏'),
          ),
        ],
      ),
    );
    if (kind != null) _gameSocket.interactWithPlayer(seat.userId, kind);
  }

  Future<bool> _confirmTableAction(String title, String content) async =>
      await showDialog<bool>(
        context: context,
        builder: (context) => AlertDialog(
          title: Text(title),
          content: Text(content),
          actions: [
            TextButton(
              onPressed: () => Navigator.of(context).pop(false),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () => Navigator.of(context).pop(true),
              child: const Text('确定'),
            ),
          ],
        ),
      ) ??
      false;

  void _showTableHint(String message) {
    ScaffoldMessenger.of(context)
      ..hideCurrentSnackBar()
      ..showSnackBar(SnackBar(content: Text(message)));
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
    final actionEffect = _actionSoundTracker.observe(_gameSocket.snapshot);
    if (widget.settings.soundEnabled && actionEffect != null) {
      unawaited(_tableSoundEffects.play(actionEffect));
    }
  }

  void _offerAutomaticRebuy() {
    final snapshot = _gameSocket.snapshot;
    final settlementHandId = snapshot?.settlement?.handId;
    if (settlementHandId == null ||
        settlementHandId == _autoRebuyHandId ||
        _rebuyDialogOpen) {
      return;
    }
    final ownSeat = snapshot?.seats
        .where((seat) => seat.userId == widget.session.user.userId)
        .firstOrNull;
    if (ownSeat == null || ownSeat.stack > 0) return;
    _autoRebuyHandId = settlementHandId;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) unawaited(_showRebuyDialog(automatic: true));
    });
  }

  Future<void> _showRebuyDialog({bool automatic = false}) async {
    if (_rebuyDialogOpen) return;
    final snapshot = _gameSocket.snapshot;
    final ownSeat = snapshot?.seats
        .where((seat) => seat.userId == widget.session.user.userId)
        .firstOrNull;
    if (snapshot == null || ownSeat == null) return;
    final maximum = snapshot.maxBuyIn > 0
        ? snapshot.maxBuyIn
        : widget.room.rules.maxBuyIn;
    final room = maximum - ownSeat.stack;
    if (room <= 0) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('当前牌桌筹码已达到最大带入')));
      return;
    }
    _rebuyDialogOpen = true;
    try {
      final bankroll = await widget.loadBankroll();
      if (!mounted) return;
      final available = math.min(room, bankroll.walletChips);
      final selected = await showDialog<int>(
        context: context,
        barrierDismissible: !automatic,
        builder: (context) => _RebuyAmountDialog(
          automatic: automatic,
          walletChips: bankroll.walletChips,
          currentStack: ownSeat.stack,
          maximum: maximum,
          available: available,
        ),
      );
      if (selected != null && selected > 0) {
        _gameSocket.rebuy(selected);
      }
    } on Object {
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(const SnackBar(content: Text('读取钱包失败，请稍后重试')));
      }
    } finally {
      _rebuyDialogOpen = false;
    }
  }

  Future<void> _leaveTable() async {
    try {
      await widget.onLeave();
    } on Object {
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('本手牌结束后才能离开并返还筹码')));
    }
  }

  List<TableSeat> get _tableSeats {
    final snapshot = _gameSocket.snapshot;
    final occupiedSeats = [...?snapshot?.seats]
      ..sort((left, right) => left.seat.compareTo(right.seat));
    return occupiedSeats
        .map((value) {
          final seatNumber = value.seat;
          final revealed = [
            ...?snapshot?.settlement?.revealedHands,
            ...?snapshot?.voluntaryReveals,
            ...?snapshot?.privateReveals,
          ].where((hand) => hand.userId == value.userId).firstOrNull;
          return TableSeat(
            number: seatNumber,
            userId: value.userId,
            displayName: value.displayName,
            chips: value.stack,
            isCurrentUser: value.userId == widget.session.user.userId,
            isDealer: snapshot?.dealerSeat == seatNumber,
            isOwner: snapshot?.ownerUserId == value.userId,
            isSpeaking:
                _speakingUserIds.contains(value.userId) &&
                !_mutedVoiceUserIds.contains(value.userId),
            isMicrophoneEnabled: _gameSocket.voiceMembers.any(
              (member) =>
                  member.userId == value.userId && member.microphoneEnabled,
            ),
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
            holeCards: value.userId == widget.session.user.userId
                ? snapshot?.holeCards ?? const []
                : const [],
            handCategory: revealed?.category ?? '',
            timeExtensions: value.timeExtensions,
            isParticipating: value.participating,
          );
        })
        .toList(growable: false);
  }

  List<Alignment> _seatAlignments(TableViewportLayout viewport) {
    final seats = _tableSeats;
    final currentIndex = seats.indexWhere((seat) => seat.isCurrentUser);
    final anchor = currentIndex < 0 ? 0 : currentIndex;
    return List.generate(seats.length, (index) {
      final relativeIndex = (index - anchor) % seats.length;
      return viewport.seatAlignment(relativeIndex, seats.length);
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
    required this.currentPlayers,
    required this.onLeave,
    required this.onSettings,
    this.compact = false,
  });

  final FriendRoom room;
  final int currentPlayers;
  final Future<void> Function() onLeave;
  final VoidCallback onSettings;
  final bool compact;

  @override
  Widget build(BuildContext context) {
    if (compact) {
      return Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Text(
            '好友牌桌',
            style: TextStyle(fontSize: 16, fontWeight: FontWeight.w800),
          ),
          Text(
            '$currentPlayers/10 人',
            style: const TextStyle(
              color: Color(0xFFF6D986),
              fontWeight: FontWeight.w700,
            ),
          ),
          const SizedBox(height: 5),
          Text(
            '房间 ${room.code}',
            style: const TextStyle(color: Colors.white70, fontSize: 12),
          ),
          Text(
            '盲注 ${room.rules.smallBlind}/${room.rules.bigBlind}',
            style: const TextStyle(color: Colors.white60, fontSize: 12),
          ),
          Row(
            children: [
              IconButton(
                onPressed: onLeave,
                visualDensity: VisualDensity.compact,
                constraints: const BoxConstraints.tightFor(
                  width: 34,
                  height: 34,
                ),
                icon: const Icon(Icons.exit_to_app, size: 19),
                tooltip: '离开房间',
              ),
              IconButton(
                onPressed: onSettings,
                visualDensity: VisualDensity.compact,
                constraints: const BoxConstraints.tightFor(
                  width: 34,
                  height: 34,
                ),
                icon: const Icon(Icons.settings_outlined, size: 19),
                tooltip: '声音与语音设置',
              ),
            ],
          ),
        ],
      );
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            Text(
              '好友牌桌 · $currentPlayers/10 人',
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
  const _ConnectionStatusBar({required this.client, this.compact = false});

  final GameSocketClient client;
  final bool compact;

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
      mainAxisSize: MainAxisSize.min,
      children: [
        Container(
          width: 8,
          height: 8,
          decoration: BoxDecoration(color: color, shape: BoxShape.circle),
        ),
        const SizedBox(width: 7),
        if (compact)
          Expanded(
            child: Text(
              label,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(fontSize: 12),
            ),
          )
        else
          Text(label),
        SizedBox(width: compact ? 2 : 8),
        if (client.status == GameSocketStatus.connected ||
            client.status == GameSocketStatus.authenticated ||
            client.status == GameSocketStatus.joined)
          if (compact)
            IconButton(
              onPressed: client.sendPing,
              tooltip: client.lastMessageType ?? '测试连接',
              visualDensity: VisualDensity.compact,
              constraints: const BoxConstraints.tightFor(width: 30, height: 30),
              icon: const Icon(Icons.sync, size: 17),
            )
          else
            TextButton(
              onPressed: client.sendPing,
              child: Text(client.lastMessageType ?? '测试连接'),
            )
        else if (compact)
          IconButton(
            onPressed: client.connect,
            tooltip: '连接',
            visualDensity: VisualDensity.compact,
            constraints: const BoxConstraints.tightFor(width: 30, height: 30),
            icon: const Icon(Icons.refresh, size: 17),
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
    required this.mutedUserIds,
    required this.currentUserId,
    required this.displayName,
    required this.onJoinChanged,
    required this.onMicrophoneChanged,
    required this.onUserMuted,
    this.compact = false,
  });

  final bool voiceJoined;
  final VoiceConnectionState connectionState;
  final bool microphoneEnabled;
  final bool operationInProgress;
  final int speakingCount;
  final List<TableVoiceMember> members;
  final Set<String> speakingUserIds;
  final Set<String> mutedUserIds;
  final String currentUserId;
  final String displayName;
  final ValueChanged<bool> onJoinChanged;
  final ValueChanged<bool> onMicrophoneChanged;
  final void Function(String userId, bool muted) onUserMuted;
  final bool compact;

  @override
  Widget build(BuildContext context) {
    final joinChip = FilterChip(
      selected: voiceJoined,
      onSelected: operationInProgress ? null : onJoinChanged,
      avatar: Icon(voiceJoined ? Icons.headset : Icons.headset_off, size: 18),
      label: Text(
        compact
            ? switch (connectionState) {
                VoiceConnectionState.disconnected => '语音',
                VoiceConnectionState.connecting => '加入中…',
                VoiceConnectionState.connected => '已入语音',
                VoiceConnectionState.reconnecting => '重连中…',
              }
            : switch (connectionState) {
                VoiceConnectionState.disconnected => '加入语音',
                VoiceConnectionState.connecting => '正在加入…',
                VoiceConnectionState.connected => '已加入语音',
                VoiceConnectionState.reconnecting => '语音重连中…',
              },
      ),
    );
    final microphoneChip = FilterChip(
      selected: microphoneEnabled,
      onSelected: voiceJoined && !operationInProgress
          ? onMicrophoneChanged
          : null,
      avatar: Icon(microphoneEnabled ? Icons.mic : Icons.mic_off, size: 18),
      label: Text(
        compact
            ? (microphoneEnabled ? '已开麦' : '麦克风')
            : (microphoneEnabled ? '自由麦已开启' : '麦克风关闭'),
      ),
    );
    final memberButton = PopupMenuButton<String>(
      tooltip: '语音成员',
      onSelected: (userId) =>
          onUserMuted(userId, !mutedUserIds.contains(userId)),
      icon: Badge(
        label: Text('${members.length}'),
        child: const Icon(Icons.groups_2_outlined, size: 20),
      ),
      itemBuilder: (context) => members.isEmpty
          ? const [
              PopupMenuItem<String>(enabled: false, child: Text('还没有人加入语音')),
            ]
          : [
              for (final member in members)
                PopupMenuItem<String>(
                  value: member.userId,
                  enabled: voiceJoined && member.userId != currentUserId,
                  child: Row(
                    children: [
                      Icon(
                        member.microphoneEnabled ? Icons.mic : Icons.mic_off,
                        size: 17,
                        color:
                            speakingUserIds.contains(member.userId) &&
                                !mutedUserIds.contains(member.userId)
                            ? const Color(0xFF6DE0A4)
                            : Colors.white54,
                      ),
                      const SizedBox(width: 8),
                      Expanded(child: Text(member.displayName)),
                      if (member.userId == currentUserId)
                        const Text('自己', style: TextStyle(fontSize: 11))
                      else if (mutedUserIds.contains(member.userId))
                        const Row(
                          children: [
                            Icon(Icons.volume_off, size: 15),
                            SizedBox(width: 3),
                            Text('已屏蔽', style: TextStyle(fontSize: 11)),
                          ],
                        )
                      else if (speakingUserIds.contains(member.userId))
                        const Text(
                          '说话中',
                          style: TextStyle(
                            color: Color(0xFF6DE0A4),
                            fontSize: 11,
                          ),
                        )
                      else
                        const Text('点击屏蔽', style: TextStyle(fontSize: 11)),
                    ],
                  ),
                ),
            ],
    );
    if (compact) {
      return Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Wrap(spacing: 4, runSpacing: 4, children: [joinChip, microphoneChip]),
          Row(
            children: [
              Expanded(
                child: Text(
                  '$speakingCount 人说话',
                  style: const TextStyle(color: Colors.white60, fontSize: 11),
                ),
              ),
              memberButton,
            ],
          ),
        ],
      );
    }
    return Row(
      children: [
        joinChip,
        const SizedBox(width: 8),
        microphoneChip,
        const SizedBox(width: 8),
        Text(
          '$displayName · $speakingCount 人说话',
          style: const TextStyle(color: Colors.white60, fontSize: 12),
        ),
        const SizedBox(width: 4),
        memberButton,
      ],
    );
  }
}

class _PokerTable extends StatelessWidget {
  const _PokerTable({
    required this.seats,
    required this.alignments,
    required this.boardRect,
    required this.snapshot,
    required this.actionRemaining,
    required this.onSeatTap,
    required this.onAvatarTap,
    required this.onUseTimeExtension,
    required this.interactions,
  });

  final List<TableSeat> seats;
  final List<Alignment> alignments;
  final Rect boardRect;
  final TableSnapshot? snapshot;
  final Duration actionRemaining;
  final ValueChanged<TableSeat> onSeatTap;
  final ValueChanged<TableSeat> onAvatarTap;
  final VoidCallback onUseTimeExtension;
  final List<TablePlayerInteraction> interactions;

  @override
  Widget build(BuildContext context) {
    final winnerAmounts = <String, int>{};
    for (final award in snapshot?.settlement?.potAwards ?? const <PotAward>[]) {
      for (final payout in award.payouts) {
        winnerAmounts.update(
          payout.userId,
          (amount) => amount + payout.amount,
          ifAbsent: () => payout.amount,
        );
      }
    }
    final showReadyStatus =
        snapshot?.phase == 'WAITING' || snapshot?.phase == 'WAITING_NEXT_HAND';
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
          ),
        ),
        Positioned.fromRect(
          rect: boardRect,
          child: IgnorePointer(
            child: _BoardCenter(
              snapshot: snapshot,
              actionRemaining: actionRemaining,
            ),
          ),
        ),
        for (var index = 0; index < seats.length; index++)
          Align(
            alignment: alignments[index],
            child: ClipRRect(
              borderRadius: BorderRadius.circular(13),
              child: GestureDetector(
                behavior: HitTestBehavior.opaque,
                onTap: () => onSeatTap(seats[index]),
                child: _SeatCard(
                  seat: seats[index],
                  actionRemaining: actionRemaining,
                  showReadyStatus: showReadyStatus,
                  winnerAmount: winnerAmounts[seats[index].userId] ?? 0,
                  onAvatarTap: () => onAvatarTap(seats[index]),
                  onUseTimeExtension: onUseTimeExtension,
                ),
              ),
            ),
          ),
        for (var index = 0; index < seats.length; index++)
          if (seats[index].streetBet > 0)
            Align(
              alignment: Alignment(
                alignments[index].x * 0.54,
                alignments[index].y * 0.58,
              ),
              child: _BetChip(amount: seats[index].streetBet),
            ),
        for (final interaction in interactions)
          if (seats.any((seat) => seat.userId == interaction.targetUserId))
            Align(
              alignment:
                  alignments[seats.indexWhere(
                    (seat) => seat.userId == interaction.targetUserId,
                  )],
              child: IgnorePointer(
                child: _PlayerInteractionBurst(interaction: interaction),
              ),
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
    final runoutBoards =
        snapshot?.settlement?.runoutBoards ?? const <List<String>>[];
    final actor = snapshot?.currentAction;
    final actorName = snapshot?.seats
        .where((seat) => seat.userId == actor?.userId)
        .map((seat) => seat.displayName)
        .firstOrNull;
    return LayoutBuilder(
      builder: (context, constraints) => Center(
        child: FittedBox(
          fit: BoxFit.scaleDown,
          child: Container(
            width: runoutBoards.length == 2 ? 460 : 430,
            padding: const EdgeInsets.symmetric(horizontal: 18, vertical: 12),
            decoration: BoxDecoration(
              color: const Color(0xF2126344),
              borderRadius: BorderRadius.circular(24),
              border: Border.all(color: const Color(0x449B7838)),
              boxShadow: const [
                BoxShadow(
                  color: Color(0xCC126344),
                  blurRadius: 22,
                  spreadRadius: 14,
                ),
              ],
            ),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                AnimatedSwitcher(
                  duration: const Duration(milliseconds: 220),
                  transitionBuilder: (child, animation) => ScaleTransition(
                    scale: Tween<double>(
                      begin: 0.88,
                      end: 1,
                    ).animate(animation),
                    child: FadeTransition(opacity: animation, child: child),
                  ),
                  child: Text(
                    '底池  ${snapshot?.totalPot ?? 0}',
                    key: ValueKey(snapshot?.totalPot ?? 0),
                    style: const TextStyle(
                      color: Color(0xFFF6D986),
                      fontSize: 18,
                    ),
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
                  const SizedBox(height: 5),
                  Text(
                    _phaseLabel(snapshot!.phase),
                    style: const TextStyle(color: Colors.white70),
                  ),
                ],
                SizedBox(height: snapshot?.settlement == null ? 16 : 10),
                if (runoutBoards.length == 2)
                  for (
                    var runoutIndex = 0;
                    runoutIndex < runoutBoards.length;
                    runoutIndex++
                  )
                    Padding(
                      padding: const EdgeInsets.symmetric(vertical: 2),
                      child: Row(
                        mainAxisAlignment: MainAxisAlignment.center,
                        children: [
                          SizedBox(
                            width: 48,
                            child: Text(
                              '第${runoutIndex + 1}次',
                              style: const TextStyle(
                                color: Color(0xFFF6D986),
                                fontSize: 11,
                              ),
                            ),
                          ),
                          for (final card in runoutBoards[runoutIndex])
                            _PlayingCard(
                              rank: _cardRank(card),
                              suit: _cardSuit(card),
                              red: card.endsWith('h') || card.endsWith('d'),
                              compact: true,
                            ),
                        ],
                      ),
                    )
                else
                  Row(
                    mainAxisAlignment: MainAxisAlignment.center,
                    children: List.generate(5, (index) {
                      final card = index < board.length ? board[index] : null;
                      return _PlayingCard(
                        rank: card == null ? '?' : _cardRank(card),
                        suit: card == null ? '' : _cardSuit(card),
                        red:
                            card != null &&
                            (card.endsWith('h') || card.endsWith('d')),
                      );
                    }),
                  ),
                if (snapshot?.settlement == null) ...[
                  const SizedBox(height: 18),
                  Text(
                    actorName == null
                        ? _phaseLabel(snapshot?.phase)
                        : '轮到 $actorName 行动 · ${_remainingSeconds(actionRemaining)} 秒',
                    style: const TextStyle(color: Colors.white70),
                  ),
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class _PlayingCard extends StatelessWidget {
  const _PlayingCard({
    required this.rank,
    required this.suit,
    this.red = false,
    this.compact = false,
  });

  final String rank;
  final String suit;
  final bool red;
  final bool compact;

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
        width: compact ? 42 : 58,
        height: compact ? 54 : 78,
        margin: EdgeInsets.symmetric(horizontal: compact ? 2 : 4),
        padding: EdgeInsets.all(compact ? 4 : 7),
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
            fontSize: compact ? 13 : 18,
            fontWeight: FontWeight.w700,
          ),
        ),
      ),
    );
  }
}

class _SeatCard extends StatelessWidget {
  const _SeatCard({
    required this.seat,
    required this.actionRemaining,
    required this.showReadyStatus,
    required this.winnerAmount,
    required this.onAvatarTap,
    required this.onUseTimeExtension,
  });

  final TableSeat seat;
  final Duration actionRemaining;
  final bool showReadyStatus;
  final int winnerAmount;
  final VoidCallback onAvatarTap;
  final VoidCallback onUseTimeExtension;

  @override
  Widget build(BuildContext context) {
    final borderColor = winnerAmount > 0
        ? const Color(0xFFFFD54F)
        : seat.isCurrentActor
        ? const Color(0xFFFFA94D)
        : seat.isCurrentUser
        ? const Color(0xFFF4D477)
        : seat.isSpeaking
        ? const Color(0xFF6DE0A4)
        : Colors.white24;

    return AnimatedContainer(
      duration: const Duration(milliseconds: 240),
      curve: Curves.easeOut,
      width: 216,
      height: 116,
      padding: const EdgeInsets.symmetric(horizontal: 11, vertical: 8),
      decoration: BoxDecoration(
        color: seat.isEmpty
            ? const Color(0x80112622)
            : seat.isFolded
            ? const Color(0xFA070B0A)
            : winnerAmount > 0
            ? const Color(0xFF263518)
            : const Color(0xEE0D211D),
        borderRadius: BorderRadius.circular(13),
        border: Border.all(
          color: borderColor,
          width: winnerAmount > 0 || seat.isCurrentActor || seat.isCurrentUser
              ? 2.5
              : 1,
        ),
        boxShadow: [
          BoxShadow(
            color: winnerAmount > 0
                ? const Color(0x99FFD54F)
                : seat.isCurrentActor
                ? const Color(0x66FFA94D)
                : Colors.black38,
            blurRadius: winnerAmount > 0 || seat.isCurrentActor ? 18 : 8,
            spreadRadius: winnerAmount > 0 || seat.isCurrentActor ? 3 : 0,
          ),
        ],
      ),
      child: Row(
        children: [
          GestureDetector(
            behavior: HitTestBehavior.opaque,
            onTap: seat.isEmpty || seat.isCurrentUser ? null : onAvatarTap,
            child: Tooltip(
              message: seat.isEmpty || seat.isCurrentUser
                  ? ''
                  : '点击赞赏或嘲讽 ${seat.displayName}',
              child: CircleAvatar(
                radius: 25,
                backgroundColor: seat.isEmpty
                    ? Colors.white10
                    : const Color(0xFF315F51),
                child: seat.isEmpty
                    ? const Icon(Icons.add, color: Colors.white54, size: 24)
                    : Text(
                        seat.position.isEmpty
                            ? '${seat.number}'
                            : seat.position,
                        textAlign: TextAlign.center,
                        style: const TextStyle(
                          fontWeight: FontWeight.w700,
                          fontSize: 11,
                        ),
                      ),
              ),
            ),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              mainAxisAlignment: MainAxisAlignment.center,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                if (seat.revealedCards.isNotEmpty)
                  _SeatShowdownSummary(seat: seat, winnerAmount: winnerAmount)
                else if (seat.isCurrentUser)
                  _CurrentSeatSummary(
                    seat: seat,
                    showReadyStatus: showReadyStatus,
                    onUseTimeExtension: onUseTimeExtension,
                  )
                else ...[
                  Row(
                    children: [
                      Expanded(
                        child: FittedBox(
                          fit: BoxFit.scaleDown,
                          alignment: Alignment.centerLeft,
                          child: Text(
                            seat.displayName,
                            maxLines: 1,
                            style: const TextStyle(
                              fontWeight: FontWeight.w700,
                              fontSize: 16,
                            ),
                          ),
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
                      if (seat.isOwner)
                        const Padding(
                          padding: EdgeInsets.only(left: 4),
                          child: Tooltip(
                            message: '房主',
                            child: Icon(
                              Icons.workspace_premium,
                              color: Color(0xFFF6D986),
                              size: 17,
                            ),
                          ),
                        ),
                    ],
                  ),
                  const SizedBox(height: 2),
                  Row(
                    children: [
                      if (seat.isSpeaking)
                        const Icon(
                          Icons.graphic_eq,
                          color: Color(0xFF6DE0A4),
                          size: 17,
                        )
                      else if (seat.isMicrophoneEnabled)
                        const Icon(
                          Icons.mic,
                          color: Color(0xFF6DE0A4),
                          size: 17,
                        ),
                      if (showReadyStatus && seat.isReady) ...[
                        const SizedBox(width: 4),
                        const Icon(
                          Icons.check_circle,
                          color: Color(0xFF6DE0A4),
                          size: 16,
                        ),
                        const SizedBox(width: 3),
                        const Text(
                          '已准备',
                          style: TextStyle(
                            color: Color(0xFF6DE0A4),
                            fontSize: 11,
                          ),
                        ),
                      ],
                      if (seat.isParticipating && !showReadyStatus) ...[
                        const Spacer(),
                        Text(
                          '加时卡 ×${seat.timeExtensions}',
                          style: const TextStyle(
                            color: Colors.white54,
                            fontSize: 11,
                          ),
                        ),
                      ],
                    ],
                  ),
                  Row(
                    children: [
                      const Icon(
                        Icons.paid,
                        size: 14,
                        color: Color(0xFFF6D986),
                      ),
                      const SizedBox(width: 4),
                      Expanded(
                        child: Text(
                          '${seat.chips}${seat.isAllIn
                              ? ' · 全下'
                              : !seat.isConnected
                              ? ' · 已断线'
                              : ''}',
                          overflow: TextOverflow.ellipsis,
                          style: const TextStyle(
                            color: Colors.white70,
                            fontSize: 13,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                      ),
                    ],
                  ),
                ],
                if (seat.revealedCards.isEmpty)
                  if (!seat.isEmpty && winnerAmount > 0)
                    Text(
                      '🏆 赢家  +$winnerAmount',
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(
                        color: Color(0xFFFFD54F),
                        fontSize: 12,
                        fontWeight: FontWeight.w800,
                      ),
                    )
                  else if (!seat.isEmpty && seat.isFolded)
                    const Row(
                      children: [
                        Icon(Icons.block, color: Colors.redAccent, size: 15),
                        SizedBox(width: 4),
                        Text(
                          '本手已弃牌',
                          style: TextStyle(
                            color: Colors.redAccent,
                            fontSize: 12,
                            fontWeight: FontWeight.w800,
                          ),
                        ),
                      ],
                    )
                  else if (!seat.isEmpty && seat.isCurrentActor)
                    Text(
                      '剩余 ${_remainingSeconds(actionRemaining)} 秒',
                      style: TextStyle(
                        color: _remainingSeconds(actionRemaining) <= 5
                            ? Colors.redAccent
                            : const Color(0xFFFFA94D),
                        fontSize: 12,
                        fontWeight: FontWeight.w700,
                      ),
                    )
                  else if (!seat.isEmpty && seat.lastAction.isNotEmpty)
                    Text(
                      _actionLabel(seat.lastAction, seat.lastActionTo),
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(
                        color: Color(0xFFF6D986),
                        fontSize: 12,
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

class _CurrentSeatSummary extends StatelessWidget {
  const _CurrentSeatSummary({
    required this.seat,
    required this.showReadyStatus,
    required this.onUseTimeExtension,
  });

  final TableSeat seat;
  final bool showReadyStatus;
  final VoidCallback onUseTimeExtension;

  @override
  Widget build(BuildContext context) {
    final canExtend =
        seat.isCurrentActor && seat.timeExtensions > 0 && !showReadyStatus;
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            if (seat.holeCards.isEmpty)
              const Expanded(
                child: Text(
                  '等待发牌',
                  style: TextStyle(color: Colors.white38, fontSize: 11),
                ),
              )
            else ...[
              for (var index = 0; index < seat.holeCards.length; index++) ...[
                if (index > 0) const SizedBox(width: 3),
                _MiniCard(
                  label:
                      '${_cardRank(seat.holeCards[index])}${_cardSuit(seat.holeCards[index])}',
                  compact: true,
                ),
              ],
              const Spacer(),
            ],
            IconButton(
              key: const ValueKey('seat-time-extension'),
              onPressed: canExtend ? onUseTimeExtension : null,
              tooltip: '加时 +30秒（剩余 ${seat.timeExtensions} 张）',
              padding: EdgeInsets.zero,
              visualDensity: VisualDensity.compact,
              constraints: const BoxConstraints.tightFor(width: 30, height: 30),
              icon: Badge(
                label: Text('${seat.timeExtensions}'),
                child: const Icon(Icons.timer_outlined, size: 17),
              ),
            ),
          ],
        ),
        const SizedBox(height: 2),
        Row(
          children: [
            const Icon(Icons.paid, size: 14, color: Color(0xFFF6D986)),
            const SizedBox(width: 4),
            Text(
              '${seat.chips}',
              style: const TextStyle(
                color: Colors.white70,
                fontSize: 13,
                fontWeight: FontWeight.w700,
              ),
            ),
            if (seat.isDealer)
              const Padding(
                padding: EdgeInsets.only(left: 5),
                child: Text('D', style: TextStyle(color: Color(0xFFF4D477))),
              ),
            if (seat.isOwner)
              const Padding(
                padding: EdgeInsets.only(left: 4),
                child: Tooltip(
                  message: '房主',
                  child: Icon(
                    Icons.workspace_premium,
                    color: Color(0xFFF6D986),
                    size: 16,
                  ),
                ),
              ),
            if (showReadyStatus && seat.isReady) ...[
              const Spacer(),
              const Icon(
                Icons.check_circle,
                color: Color(0xFF6DE0A4),
                size: 15,
              ),
            ],
          ],
        ),
      ],
    );
  }
}

class _SeatShowdownSummary extends StatelessWidget {
  const _SeatShowdownSummary({required this.seat, required this.winnerAmount});

  final TableSeat seat;
  final int winnerAmount;

  @override
  Widget build(BuildContext context) {
    return KeyedSubtree(
      key: ValueKey('seat-showdown-${seat.userId}'),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            seat.displayName,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w800),
          ),
          const SizedBox(height: 2),
          Row(
            children: [
              for (
                var index = 0;
                index < seat.revealedCards.length;
                index++
              ) ...[
                if (index > 0) const SizedBox(width: 3),
                _MiniCard(
                  label:
                      '${_cardRank(seat.revealedCards[index])}${_cardSuit(seat.revealedCards[index])}',
                  compact: true,
                ),
              ],
              if (winnerAmount > 0) ...[
                const Spacer(),
                const Icon(
                  Icons.emoji_events,
                  color: Color(0xFFFFD54F),
                  size: 19,
                ),
              ],
            ],
          ),
          const SizedBox(height: 2),
          Text(
            handCategoryLabel(seat.handCategory),
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: const TextStyle(
              fontSize: 10,
              color: Color(0xFFF6D986),
              fontWeight: FontWeight.w700,
            ),
          ),
          if (winnerAmount > 0)
            Text(
              '赢家 +$winnerAmount',
              style: const TextStyle(
                color: Color(0xFFFFD54F),
                fontSize: 10,
                fontWeight: FontWeight.w800,
              ),
            ),
        ],
      ),
    );
  }
}

class _PlayerInteractionBurst extends StatelessWidget {
  const _PlayerInteractionBurst({required this.interaction});

  final TablePlayerInteraction interaction;

  @override
  Widget build(BuildContext context) {
    final praise = interaction.kind == 'praise';
    return TweenAnimationBuilder<double>(
      key: ValueKey(interaction.interactionId),
      tween: Tween(begin: 0, end: 1),
      duration: const Duration(milliseconds: 1700),
      curve: Curves.easeOut,
      builder: (context, progress, child) {
        final opacity = math.sin(math.pi * progress).clamp(0.0, 1.0);
        final shake = praise ? 0.0 : math.sin(progress * math.pi * 10) * 8;
        return Opacity(
          opacity: opacity,
          child: Transform.translate(
            offset: Offset(shake, -48 - progress * 48),
            child: Transform.scale(
              scale: 0.72 + math.min(progress * 1.4, 0.38),
              child: child,
            ),
          ),
        );
      },
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 7),
        decoration: BoxDecoration(
          color: praise ? const Color(0xEE6A541C) : const Color(0xEE6A2A32),
          borderRadius: BorderRadius.circular(22),
          border: Border.all(
            color: praise ? const Color(0xFFFFD54F) : Colors.redAccent,
            width: 2,
          ),
          boxShadow: [
            BoxShadow(
              color: praise ? const Color(0x99FFD54F) : const Color(0x99FF5252),
              blurRadius: 18,
              spreadRadius: 3,
            ),
          ],
        ),
        child: Text(
          praise
              ? '👍  ${interaction.fromDisplayName} 赞赏'
              : '😜  ${interaction.fromDisplayName} 嘲讽',
          style: const TextStyle(fontWeight: FontWeight.w800, fontSize: 13),
        ),
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

class _RebuyAmountDialog extends StatefulWidget {
  const _RebuyAmountDialog({
    required this.automatic,
    required this.walletChips,
    required this.currentStack,
    required this.maximum,
    required this.available,
  });

  final bool automatic;
  final int walletChips;
  final int currentStack;
  final int maximum;
  final int available;

  @override
  State<_RebuyAmountDialog> createState() => _RebuyAmountDialogState();
}

class _RebuyAmountDialogState extends State<_RebuyAmountDialog> {
  late final TextEditingController _controller;
  late int _amount;

  bool get _valid => _amount > 0 && _amount <= widget.available;

  @override
  void initState() {
    super.initState();
    _amount = widget.available;
    _controller = TextEditingController(text: '$_amount');
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final sliderMaximum = widget.available > 0 ? widget.available : 1;
    final sliderValue = _amount.clamp(0, sliderMaximum).toDouble();
    final keyboardVisible = MediaQuery.viewInsetsOf(context).bottom > 0;
    return AlertDialog(
      scrollable: true,
      insetPadding: EdgeInsets.symmetric(
        horizontal: 18,
        vertical: keyboardVisible ? 8 : 24,
      ),
      titlePadding: EdgeInsets.fromLTRB(
        24,
        keyboardVisible ? 12 : 20,
        24,
        keyboardVisible ? 4 : 12,
      ),
      contentPadding: EdgeInsets.fromLTRB(
        24,
        keyboardVisible ? 4 : 8,
        24,
        keyboardVisible ? 8 : 16,
      ),
      actionsPadding: const EdgeInsets.fromLTRB(16, 0, 16, 10),
      title: Text(widget.automatic ? '筹码已用完，请补码' : '补码'),
      content: SizedBox(
        width: 440,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text(
              '钱包 ${widget.walletChips} · 当前 ${widget.currentStack} · 最大带入 ${widget.maximum}',
            ),
            const SizedBox(height: 12),
            Slider(
              value: sliderValue,
              min: 0,
              max: sliderMaximum.toDouble(),
              divisions: widget.available > 0
                  ? math.min(widget.available, 100)
                  : 1,
              label: '$_amount',
              onChanged: widget.available <= 0
                  ? null
                  : (value) {
                      setState(() => _amount = value.round());
                      _controller.text = '$_amount';
                    },
            ),
            PlatformNumberField(
              controller: _controller,
              scrollPadding: const EdgeInsets.only(bottom: 100),
              decoration: InputDecoration(
                labelText: '补码数量',
                helperText: widget.available > 0
                    ? '本次最多可补 ${widget.available}'
                    : '钱包余额不足，请返回大厅充值',
                errorText: _amount > widget.available ? '补码数量超过本次上限' : null,
              ),
              onChanged: (value) =>
                  setState(() => _amount = int.tryParse(value) ?? 0),
              onSubmitted: (_) {
                if (_valid) Navigator.of(context).pop(_amount);
              },
            ),
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: Text(widget.automatic ? '暂不补码' : '取消'),
        ),
        FilledButton(
          onPressed: _valid ? () => Navigator.of(context).pop(_amount) : null,
          child: const Text('确认补码'),
        ),
      ],
    );
  }
}

class _ActionBar extends StatelessWidget {
  const _ActionBar({
    required this.client,
    required this.userId,
    required this.smallBlind,
    required this.onRebuy,
  });

  final GameSocketClient client;
  final String userId;
  final int smallBlind;
  final VoidCallback onRebuy;

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
    final autoReadyRemaining = snapshot?.autoReadyDeadline?.difference(
      client.serverNow,
    );
    final autoReadyPending = autoReadyRemaining != null;
    final runoutChoice = snapshot?.runoutChoice;
    final runoutRemaining = runoutChoice?.deadline?.difference(
      client.serverNow,
    );
    final ownRunoutChoice = runoutChoice?.choices[userId];
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
              child: Row(
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  FilledButton.icon(
                    onPressed:
                        client.status == GameSocketStatus.joined &&
                            (ownSeat?.stack ?? 0) > 0
                        ? () {
                            if (ownSeat?.ready == true) {
                              client.setReady(false);
                            } else if (autoReadyPending &&
                                !(snapshot?.autoReadyCancelled ?? false)) {
                              client.setReady(false);
                            } else {
                              client.setReady(true);
                            }
                          }
                        : null,
                    icon: Icon(
                      ownSeat?.ready == true ||
                              (autoReadyPending &&
                                  !(snapshot?.autoReadyCancelled ?? false))
                          ? Icons.pause_circle
                          : Icons.play_circle,
                    ),
                    label: Text(
                      ownSeat?.ready == true
                          ? '取消准备'
                          : autoReadyPending &&
                                !(snapshot?.autoReadyCancelled ?? false)
                          ? autoReadyRemaining > Duration.zero
                                ? '取消自动准备（${_remainingSeconds(autoReadyRemaining)}秒）'
                                : '正在自动准备…'
                          : '准备开始',
                    ),
                  ),
                  const SizedBox(width: 12),
                  OutlinedButton.icon(
                    onPressed:
                        client.status == GameSocketStatus.joined &&
                            (ownSeat?.stack ?? 0) < (snapshot?.maxBuyIn ?? 0)
                        ? onRebuy
                        : null,
                    icon: const Icon(Icons.add_circle_outline),
                    label: const Text('补码'),
                  ),
                  if (snapshot?.canShowHoleCards ?? false) ...[
                    const SizedBox(width: 12),
                    FilledButton.tonalIcon(
                      onPressed: client.showHoleCards,
                      icon: const Icon(Icons.visibility_outlined, size: 17),
                      label: const Text('展示手牌'),
                    ),
                  ],
                ],
              ),
            )
          else if (runoutChoice != null)
            Expanded(
              child: Row(
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  Text(
                    '全下发牌选择 · ${_remainingSeconds(runoutRemaining ?? Duration.zero)} 秒',
                    style: const TextStyle(
                      color: Color(0xFFF6D986),
                      fontWeight: FontWeight.w700,
                    ),
                  ),
                  const SizedBox(width: 16),
                  if (!runoutChoice.eligiblePlayerIds.contains(userId))
                    const Text('等待两名玩家选择')
                  else if (ownRunoutChoice != null)
                    Text(
                      '已选择发 $ownRunoutChoice 次，等待对方',
                      style: const TextStyle(color: Colors.white70),
                    )
                  else ...[
                    OutlinedButton(
                      onPressed: () => client.chooseRunoutCount(1),
                      child: const Text('发一次'),
                    ),
                    const SizedBox(width: 10),
                    FilledButton(
                      onPressed: () => client.chooseRunoutCount(2),
                      child: const Text('发两次'),
                    ),
                  ],
                ],
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
              child: ResponsiveActionStrip(
                leadingActions: [
                  OutlinedButton(
                    key: const ValueKey('bet-fold-action'),
                    onPressed: options!.canFold && !client.actionPending
                        ? () => client.submitAction('fold')
                        : null,
                    child: const Text('弃牌'),
                  ),
                  if (options.canCheck || options.canCall)
                    FilledButton.tonal(
                      key: const ValueKey('bet-check-call-action'),
                      onPressed: client.actionPending
                          ? null
                          : options.canCheck
                          ? () => client.submitAction('check')
                          : () => client.submitAction('call'),
                      child: Text(
                        options.canCheck ? '过牌' : '跟注 ${options.toCall}',
                      ),
                    ),
                ],
                presetActions: [
                  for (final suggestion in current!.suggestions)
                    FilledButton.tonal(
                      key: ValueKey(
                        'bet-suggestion-${suggestion.label}-${suggestion.raiseTo}',
                      ),
                      onPressed: client.actionPending
                          ? null
                          : () => client.submitAction(
                              suggestion.action,
                              raiseTo: suggestion.action == 'all_in'
                                  ? null
                                  : suggestion.raiseTo,
                            ),
                      child: Text(
                        _suggestionButtonLabel(
                          suggestion,
                          ownSeat?.streetBet ?? 0,
                        ),
                        textAlign: TextAlign.center,
                        style: const TextStyle(fontSize: 11, height: 1.05),
                      ),
                    ),
                ],
                trailingAction: options.canBet || options.canRaise
                    ? OutlinedButton.icon(
                        key: const ValueKey('bet-custom-action'),
                        onPressed: client.actionPending
                            ? null
                            : () => showDialog<void>(
                                context: context,
                                builder: (context) => _BetAmountDialog(
                                  client: client,
                                  options: options,
                                  streetBet: ownSeat?.streetBet ?? 0,
                                  smallBlind: smallBlind,
                                  onSubmit: (action, raiseTo) => client
                                      .submitAction(action, raiseTo: raiseTo),
                                ),
                              ),
                        icon: const Icon(Icons.tune, size: 18),
                        label: const Text('自定义'),
                      )
                    : null,
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
    required this.streetBet,
    required this.smallBlind,
    required this.onSubmit,
  });

  final GameSocketClient client;
  final TableActionOptions options;
  final int streetBet;
  final int smallBlind;
  final void Function(String action, int raiseTo) onSubmit;

  @override
  State<_BetAmountDialog> createState() => _BetAmountDialogState();
}

class _BetAmountDialogState extends State<_BetAmountDialog> {
  late int _amount;
  late final TextEditingController _controller;
  String? _error;
  Timer? _clock;

  int get _unit => math.max(1, widget.smallBlind);

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
    final remainingSeconds = _remainingSeconds(remaining);
    final keyboardVisible = MediaQuery.viewInsetsOf(context).bottom > 0;
    return AlertDialog(
      scrollable: true,
      insetPadding: EdgeInsets.symmetric(
        horizontal: 16,
        vertical: keyboardVisible ? 8 : 20,
      ),
      titlePadding: EdgeInsets.fromLTRB(
        24,
        keyboardVisible ? 10 : 18,
        16,
        keyboardVisible ? 2 : 8,
      ),
      contentPadding: EdgeInsets.fromLTRB(
        24,
        keyboardVisible ? 4 : 8,
        24,
        keyboardVisible ? 6 : 12,
      ),
      actionsPadding: const EdgeInsets.fromLTRB(16, 0, 16, 10),
      title: Row(
        children: [
          Text('$actionLabel额度'),
          const Spacer(),
          Icon(
            Icons.timer_outlined,
            size: 19,
            color: remainingSeconds <= 5 ? Colors.redAccent : Colors.white70,
          ),
          const SizedBox(width: 5),
          Text(
            '剩余 $remainingSeconds 秒',
            style: TextStyle(
              fontSize: 14,
              fontWeight: FontWeight.w700,
              color: remainingSeconds <= 5 ? Colors.redAccent : Colors.white70,
            ),
          ),
          const SizedBox(width: 8),
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
      content: SizedBox(
        width: 520,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text(
              '允许范围：${options.minRaiseTo} ～ ${options.maxRaiseTo} · '
              '最小单位：小盲 $_unit',
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
            PlatformNumberField(
              controller: _controller,
              scrollPadding: const EdgeInsets.only(bottom: 100),
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
                    _error = parsed % _unit == 0 ? null : '额度必须是小盲 $_unit 的整数倍';
                  });
                }
              },
            ),
            const SizedBox(height: 10),
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
                entered > options.maxRaiseTo ||
                entered % _unit != 0) {
              if (entered != null && entered % _unit != 0) {
                setState(() => _error = '额度必须是小盲 $_unit 的整数倍');
                return;
              }
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
    final snapped = ((value + _unit ~/ 2) ~/ _unit) * _unit;
    final clamped = snapped.clamp(
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
  'min_raise' => '最小加注',
  'quarter_pot' => '1/4 池',
  'third_pot' => '1/3 池',
  'half_pot' => '1/2 池',
  'two_thirds_pot' => '2/3 池',
  'pot' => '满池',
  'overbet_120' => '1.2× 超池',
  'all_in' => '全下',
  _ => label,
};

String _suggestionButtonLabel(BetSuggestion suggestion, int streetBet) {
  final committed = math.max(0, suggestion.raiseTo - streetBet);
  if (suggestion.action == 'all_in') {
    return '全下\n投入 $committed';
  }
  return '${_suggestionLabel(suggestion.label)}\n'
      '投入 $committed · 至 ${suggestion.raiseTo}';
}

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
  final runout = award.runoutIndex > 0 ? '第${award.runoutIndex}次 · ' : '';
  return '$runout$potName ${award.amount}：$names';
}

String _gameErrorLabel(String code) => switch (code) {
  'connection_failed' => '牌桌网络暂时不可用，正在自动重新连接',
  'sequence_gap' => '检测到网络消息缺口，正在恢复牌桌状态',
  'stale_revision' || 'stale_hand' => '牌桌状态已经更新，正在为你恢复最新画面',
  'not_your_turn' => '现在还没有轮到你',
  'illegal_action' => '当前不能执行这个操作',
  'invalid_amount' => '下注额度不在允许范围内',
  'rate_limited' => '消息发送太快，请稍后再试',
  'chat_muted' => '你已被管理员禁言，暂时不能发送牌桌文字消息',
  'content_rejected' => '消息内容不符合要求',
  'authentication_required' => '登录状态已失效，请重新登录',
  'no_time_extensions' => '本手的两张加时卡已经用完',
  'time_extension_expired' => '本次行动已经超时，无法再主动加时',
  'insufficient_wallet_chips' => '账户筹码不足，请返回大厅充值',
  'maximum_buy_in_exceeded' => '本次补码会超过房间最大带入',
  'hand_in_progress' => '只能在两手牌之间补码',
  'rebuy_required' => '筹码已用完，请先补码再准备',
  'hole_card_view_not_available' => '只有本手已弃牌的玩家才能申请查看仍在本手中的玩家手牌',
  'hole_card_view_request_not_found' => '这条看牌申请已经失效',
  'choose_empty_seat' => '牌桌未满时请点击空座位换位',
  'seat_swap_request_not_found' => '这条换位申请已经失效',
  'runout_choice_not_available' => '当前不在发牌次数选择阶段',
  'invalid_player_interaction' => '请选择同桌的其他玩家进行互动',
  'player_not_at_table' => '该玩家已经离开牌桌',
  'player_interaction_too_frequent' => '互动发送太快，请稍后再试',
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
  'RUNOUT_CHOICE' => '全下，等待选择发牌次数',
  'SHOWDOWN' => '摊牌',
  _ => phase,
};

class _MiniCard extends StatelessWidget {
  const _MiniCard({required this.label, this.compact = false});

  final String label;
  final bool compact;

  @override
  Widget build(BuildContext context) {
    final redSuit = label.contains('♥') || label.contains('♦');
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
          color: redSuit ? const Color(0xFFC63D45) : Colors.black87,
          fontWeight: FontWeight.w800,
          fontSize: compact ? 10 : null,
        ),
      ),
    );
  }
}
