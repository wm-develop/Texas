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
import 'package:poker_client/core/platform/voice_chat_service.dart';
import 'package:poker_client/core/settings/app_settings.dart';
import 'package:poker_client/features/admin/presentation/admin_page.dart';
import 'package:poker_client/features/bankroll/domain/bankroll_snapshot.dart';
import 'package:poker_client/features/lobby/domain/friend_room.dart';
import 'package:poker_client/features/table/audio/table_action_sound_tracker.dart';
import 'package:poker_client/features/table/audio/table_sound_effects.dart';
import 'package:poker_client/features/table/domain/table_seat.dart';
import 'package:poker_client/features/table/presentation/table_labels.dart';
import 'package:poker_client/features/table/presentation/table_action_bar.dart';
import 'package:poker_client/features/table/presentation/table_canvas.dart';
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
            child: TableChatPanel(
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

/// 发两次的展示阶段：先完整展示第一块牌面 5 秒，然后仅让第一次发出的
/// 公共牌（两块牌面的公共前缀之外的部分）渐隐，再淡入第二块牌面。

/// 发两次进入叠放展示后，第一次发出的牌收起成这条顶部点数条，
/// 第二次的牌叠在它下方；玩家仍能同时读到两次的点数与花色。
