import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/foundation.dart' show defaultTargetPlatform;
import 'package:flutter/material.dart';
import 'package:poker_client/core/settings/settings_dialog.dart';
import 'package:poker_client/core/platform/voice_chat_service_factory.dart';
import 'package:poker_client/core/auth/auth_session.dart';
import 'package:poker_client/core/network/game_socket_client.dart';
import 'package:poker_client/core/network/trtc_credential_client.dart';
import 'package:poker_client/core/platform/native_display_cutout.dart';
import 'package:poker_client/core/settings/app_settings.dart';
import 'package:poker_client/features/admin/presentation/admin_page.dart';
import 'package:poker_client/features/bankroll/domain/bankroll_snapshot.dart';
import 'package:poker_client/features/lobby/domain/friend_room.dart';
import 'package:poker_client/features/table/audio/table_action_sound_tracker.dart';
import 'package:poker_client/features/table/audio/table_sound_effects.dart';
import 'package:poker_client/features/table/domain/table_seat.dart';
import 'package:poker_client/features/table/presentation/table_labels.dart';
import 'package:poker_client/features/table/presentation/table_action_bar.dart';
import 'package:poker_client/features/table/presentation/table_automation_coordinator.dart';
import 'package:poker_client/features/table/presentation/table_canvas.dart';
import 'package:poker_client/features/table/presentation/table_voice_controller.dart';
import 'package:poker_client/features/table/presentation/table_chat_panel.dart';
import 'package:poker_client/features/table/presentation/table_rebuy_dialog.dart';
import 'package:poker_client/features/table/presentation/table_status_widgets.dart';
import 'package:poker_client/features/table/domain/table_snapshot.dart';
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

  bool _chatVisible = true;
  bool _compactChatOpen = false;
  int _observedChatRevision = 0;
  int _unreadChatCount = 0;
  late final GameSocketClient _gameSocket;
  late final TrtcCredentialClient _trtcCredentials;
  late final TableVoiceController _voice;
  Timer? _tableClock;
  String? _lastShownGameError;
  GameSocketStatus? _lastGameSocketStatus;
  final TableActionSoundTracker _actionSoundTracker = TableActionSoundTracker();
  final TableSoundEffects _tableSoundEffects = TableSoundEffects();
  late final TableAutomationCoordinator _automation;
  bool _autoJoinAttempted = false;
  bool _removedFromRoomHandled = false;
  final Set<String> _observedInteractionIds = {};
  final List<TablePlayerInteraction> _activeInteractions = [];
  final Map<String, Timer> _interactionTimers = {};
  EdgeInsets _nativeDisplayCutout = EdgeInsets.zero;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _automation = TableAutomationCoordinator(
      currentUserId: widget.session.user.userId,
    );
    _gameSocket = GameSocketClient(
      accessTokenProvider: widget.accessTokenProvider,
      roomId: widget.room.roomId,
      userId: widget.session.user.userId,
    )..addListener(_refresh);
    unawaited(_gameSocket.connect());
    _trtcCredentials = TrtcCredentialClient(
      serverBaseUri: Uri.parse(_gameHttpServerUrl),
    );
    _voice = TableVoiceController(
      voiceChat: createVoiceChatService(),
      issueCredentials: () async => _trtcCredentials.issue(
        userId: widget.session.user.userId,
        roomId: widget.room.roomId,
        accessToken: await widget.accessTokenProvider(),
      ),
      currentUserId: widget.session.user.userId,
      onStateChanged: ({required joined, required microphoneEnabled}) =>
          _gameSocket.setVoiceState(
            joined: joined,
            microphoneEnabled: microphoneEnabled,
          ),
      onError: _showVoiceError,
      playbackVolume: () => widget.settings.voiceVolume,
    )..addListener(_voiceChanged);
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
    _voice
      ..removeListener(_voiceChanged)
      ..dispose();
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

  /// True on touch platforms where a soft keyboard can shrink the viewport.
  /// There the table layout family is fixed by the device class instead of
  /// the momentary window height, so opening the keyboard can never rebuild
  /// the input field's subtree and immediately dismiss the keyboard again.
  static bool get _isMobilePlatform =>
      defaultTargetPlatform == TargetPlatform.android ||
      defaultTargetPlatform == TargetPlatform.iOS ||
      defaultTargetPlatform == TargetPlatform.ohos;

  /// Viewport size that ignores the soft keyboard: on mobile it derives from
  /// the full window size (which stays constant while the keyboard only adds
  /// viewInsets) minus the safe-area and cutout paddings the body applies.
  Size _stableViewportSize(BuildContext context, EdgeInsets remainingCutout) {
    final mediaSize = MediaQuery.sizeOf(context);
    final mediaPadding = MediaQuery.paddingOf(context);
    return Size(
      math.max(
        1,
        mediaSize.width - mediaPadding.horizontal - remainingCutout.horizontal,
      ),
      math.max(
        1,
        mediaSize.height - mediaPadding.vertical - remainingCutout.vertical,
      ),
    );
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
                final stableSize = _isMobilePlatform
                    ? _stableViewportSize(context, remainingCutout)
                    : constraints.biggest;
                final viewport = TableViewportLayout.fromSize(
                  stableSize,
                  chatVisible: _chatVisible,
                  // Phones stay on the compact layout, tablets on the regular
                  // one, no matter how the window momentarily resizes.
                  compactOverride: _isMobilePlatform
                      ? MediaQuery.sizeOf(context).shortestSide < 600
                      : null,
                );
                final showSideChat = _chatVisible && viewport.supportsSideChat;
                final roomHeader = TableRoomHeader(
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
                final connectionStatus = TableConnectionStatusBar(
                  client: _gameSocket,
                  compact: viewport.isCompactLandscape,
                );
                final voiceControls = TableVoiceControls(
                  voiceJoined: _voice.joined,
                  connectionState: _voice.connectionState,
                  microphoneEnabled: _voice.microphoneEnabled,
                  operationInProgress: _voice.operationInProgress,
                  speakingCount: _voice.audibleSpeakingUserIds.length,
                  members: _gameSocket.voiceMembers,
                  speakingUserIds: _voice.speakingUserIds,
                  mutedUserIds: _voice.mutedUserIds,
                  currentUserId: widget.session.user.userId,
                  displayName: widget.session.user.displayName,
                  compact: viewport.isCompactLandscape,
                  onJoinChanged: _voice.setJoined,
                  onMicrophoneChanged: _voice.setMicrophoneEnabled,
                  onUserMuted: _voice.setUserMuted,
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
                            child: TableCanvas(
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
                              child: TableChatPanel(
                                client: _gameSocket,
                                currentUserId: widget.session.user.userId,
                                blockedUserIds: _voice.blockedUserIds,
                                onBlockChanged: _voice.setUserBlocked,
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
                            child: TableActionBar(
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
            child: TableChatPanel(
              client: _gameSocket,
              currentUserId: widget.session.user.userId,
              blockedUserIds: _voice.blockedUserIds,
              onBlockChanged: _voice.setUserBlocked,
              onClose: () => Navigator.of(dialogContext).pop(),
            ),
          ),
        ),
      );
    } finally {
      if (mounted) setState(() => _compactChatOpen = false);
    }
  }

  void _voiceChanged() {
    if (mounted) setState(() {});
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
      if (_gameSocket.status == GameSocketStatus.joined && _voice.joined) {
        _gameSocket.setVoiceState(
          joined: true,
          microphoneEnabled: _voice.microphoneEnabled,
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
          ..showSnackBar(SnackBar(content: Text(gameErrorLabel(error))));
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
        TableViewportLayout.fromSize(
          size,
          chatVisible: true,
          compactOverride: _isMobilePlatform ? size.shortestSide < 600 : null,
        ).supportsSideChat;
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
    if (_automation.shouldSubmitAutoReady(
      snapshot: snapshot,
      serverNow: _gameSocket.serverNow,
      socketJoined: _gameSocket.status == GameSocketStatus.joined,
    )) {
      _gameSocket.setReady(true);
    }
  }

  void _offerPendingTableRequest() {
    final prompt = _automation.takeNextRequest(_gameSocket.snapshot);
    if (prompt == null) return;
    WidgetsBinding.instance.addPostFrameCallback((_) async {
      if (!mounted) return;
      final accepted = await showDialog<bool>(
        context: context,
        barrierDismissible: false,
        builder: (context) => AlertDialog(
          title: Text(prompt.title),
          content: Text(prompt.description),
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
      final requestId = prompt.request.requestId;
      if (prompt.holeCards) {
        _gameSocket.respondHoleCardsView(requestId, accepted == true);
      } else {
        _gameSocket.respondSeatSwap(requestId, accepted == true);
      }
      _automation.requestDialogOpen = false;
      if (mounted) _offerPendingTableRequest();
    });
  }

  Future<void> _handleSeatTap(TableSeat seat) async {
    final snapshot = _gameSocket.snapshot;
    if (snapshot == null) return;
    final waiting =
        snapshot.phase == 'WAITING' || snapshot.phase == 'WAITING_NEXT_HAND';
    if (waiting) {
      // 座位数量始终等于玩家数量，不存在可选的空位；换位只有一种方式：
      // 点击目标玩家发起双方确认的换位申请。
      if (seat.isCurrentUser) return;
      final confirmed = await _confirmTableAction(
        '申请换位',
        '向 ${seat.displayName} 发出交换座位申请？',
      );
      if (confirmed) _gameSocket.requestSeatChange(seat.number);
      return;
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
      '向 ${seat.displayName} 申请私下查看对方的手牌？只有对方同意后你才能看到。',
    );
    if (confirmed) _gameSocket.requestHoleCardsView(seat.userId);
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

  void _settingsChanged() {
    if (!mounted) return;
    unawaited(_voice.applyPlaybackVolume());
    if (widget.settings.ready &&
        widget.settings.autoJoinVoice &&
        !_autoJoinAttempted &&
        !_voice.joined) {
      _autoJoinAttempted = true;
      unawaited(_voice.setJoined(true));
    }
  }

  void _playTableSounds() {
    final actionEffect = _actionSoundTracker.observe(_gameSocket.snapshot);
    if (widget.settings.soundEnabled && actionEffect != null) {
      unawaited(_tableSoundEffects.play(actionEffect));
    }
  }

  void _offerAutomaticRebuy() {
    if (!_automation.shouldOfferRebuy(_gameSocket.snapshot)) return;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) unawaited(_showRebuyDialog(automatic: true));
    });
  }

  Future<void> _showRebuyDialog({bool automatic = false}) async {
    if (_automation.rebuyDialogOpen) return;
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
    _automation.rebuyDialogOpen = true;
    try {
      final bankroll = await widget.loadBankroll();
      if (!mounted) return;
      final available = math.min(room, bankroll.walletChips);
      final selected = await showDialog<int>(
        context: context,
        barrierDismissible: !automatic,
        builder: (context) => TableRebuyAmountDialog(
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
      _automation.rebuyDialogOpen = false;
    }
  }

  Future<void> _leaveTable() async {
    try {
      await widget.onLeave();
    } on Object {
      if (!mounted) return;
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('弃牌后或本手牌结束后才能离开并返还筹码')));
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
            isSpeaking: _voice.isSpeaking(value.userId),
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
    ScaffoldMessenger.of(context)
      ..hideCurrentSnackBar()
      ..showSnackBar(SnackBar(content: Text(voiceErrorMessage(error))));
  }
}

/// 发两次的展示阶段：先完整展示第一块牌面 5 秒，然后仅让第一次发出的
/// 公共牌（两块牌面的公共前缀之外的部分）渐隐，再淡入第二块牌面。

/// 发两次进入叠放展示后，第一次发出的牌收起成这条顶部点数条，
/// 第二次的牌叠在它下方；玩家仍能同时读到两次的点数与花色。
