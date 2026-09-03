import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:poker_client/core/auth/auth_session.dart';
import 'package:poker_client/core/network/game_api_client.dart';
import 'package:poker_client/core/network/game_socket_client.dart';
import 'package:poker_client/core/platform/system_ui_policy.dart';
import 'package:poker_client/core/settings/app_settings.dart';
import 'package:poker_client/features/auth/presentation/auth_page.dart';
import 'package:poker_client/features/bankroll/domain/bankroll_entry.dart';
import 'package:poker_client/features/bankroll/domain/bankroll_snapshot.dart';
import 'package:poker_client/features/history/domain/recent_hand.dart';
import 'package:poker_client/features/lobby/domain/friend_room.dart';
import 'package:poker_client/features/lobby/presentation/lobby_page.dart';
import 'package:poker_client/features/table/presentation/table_prototype_page.dart';
import 'package:poker_client/features/update/presentation/update_required_page.dart';

class PokerApp extends StatefulWidget {
  const PokerApp({super.key});

  @override
  State<PokerApp> createState() => _PokerAppState();
}

class _PokerAppState extends State<PokerApp> with WidgetsBindingObserver {
  final _navigatorKey = GlobalKey<NavigatorState>();
  late final GameApiClient _api;
  late final AppSettingsController _settings;
  AuthSession? _session;
  /// 版本过旧时阻断整个应用，连登录都不放行。
  bool _clientTooOld = false;
  int _minimumClientVersion = 0;
  FriendRoom? _room;
  BankrollSnapshot? _bankroll;
  Timer? _presenceTimer;
  Future<AuthSession>? _sessionRefresh;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    if (shouldUseCurrentPlatformDartSystemUi) {
      unawaited(_enableImmersiveMode());
      unawaited(
        SystemChrome.setSystemUIChangeCallback((systemBarsVisible) async {
          if (!systemBarsVisible) return;
          // Android temporarily prevents UI changes after the keyboard closes.
          // Reapply after that guard interval so the whole app stays immersive.
          await Future<void>.delayed(const Duration(milliseconds: 1100));
          if (mounted) await _enableImmersiveMode();
        }),
      );
    }
    _api = GameApiClient();
    _settings = AppSettingsController()..load();
    // 必须排在 _api 之后：版本检查要用它。
    unawaited(_recheckClientVersion());
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed &&
        shouldUseCurrentPlatformDartSystemUi) {
      unawaited(_enableImmersiveMode());
    }
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    if (shouldUseCurrentPlatformDartSystemUi) {
      unawaited(SystemChrome.setSystemUIChangeCallback(null));
    }
    _presenceTimer?.cancel();
    _api.close();
    _settings.dispose();
    super.dispose();
  }

  Future<void> _enableImmersiveMode() async {
    try {
      await SystemChrome.setEnabledSystemUIMode(SystemUiMode.immersiveSticky);
    } on Object {
      // This is a mobile presentation preference; unsupported targets ignore it.
    }
  }

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      navigatorKey: _navigatorKey,
      title: '好友德州',
      debugShowCheckedModeBanner: false,
      theme: ThemeData(
        brightness: Brightness.dark,
        scaffoldBackgroundColor: const Color(0xFF071B18),
        colorScheme: ColorScheme.fromSeed(
          seedColor: const Color(0xFFD9B85F),
          brightness: Brightness.dark,
          surface: const Color(0xFF102A25),
        ),
        useMaterial3: true,
      ),
      home: _home(),
    );
  }

  Widget _home() {
    // 版本门禁在最前面：开发期服务端改动频繁，旧客户端连上新服务端会出
    // 各种难以定位的问题，因此完全阻断，连登录都不放行。
    if (_clientTooOld) {
      return UpdateRequiredPage(
        minimumVersionCode: _minimumClientVersion,
        onRetry: _recheckClientVersion,
      );
    }
    final session = _session;
    if (session == null) {
      return AuthPage(onLogin: _login, onRegister: _register);
    }
    final bankroll = _bankroll;
    if (bankroll == null) {
      return const Scaffold(body: Center(child: CircularProgressIndicator()));
    }
    final room = _room;
    if (room == null) {
      return LobbyPage(
        session: session,
        bankroll: bankroll,
        onCreateRoom: _createRoom,
        onJoinRoom: _joinRoom,
        onLoadRecentHands: _loadRecentHands,
        onTopUp: _topUp,
        onLoadBankrollEntries: _loadBankrollEntries,
        onPreviewRoom: _previewRoom,
        onUpdateUsername: _updateUsername,
        onUpdateDisplayName: _updateDisplayName,
        onChangePassword: _changePassword,
        onDeleteAccount: _deleteAccount,
        accessTokenProvider: _accessToken,
        settings: _settings,
        onLogout: _logout,
      );
    }
    return TablePrototypePage(
      session: session,
      room: room,
      settings: _settings,
      onLeave: _leaveRoom,
      onRemoved: _removedFromRoom,
      accessTokenProvider: _accessToken,
      loadBankroll: () => _authorized(_api.bankroll),
    );
  }

  Future<AuthSession> _login(String username, String password) async {
    final session = await _api.login(username: username, password: password);
    await _activateSession(session);
    return session;
  }

  Future<AuthSession> _register(
    String username,
    String displayName,
    String password,
    bool requestAdmin,
  ) async {
    late final AuthSession session;
    try {
      session = await _api.register(
        username: username,
        displayName: displayName,
        password: password,
        requestAdmin: requestAdmin,
      );
    } on GameApiTimeoutException catch (timeout) {
      session = await _recoverRegistration(username, password, timeout);
    } on GameApiException catch (error) {
      if (error.code != 'username_taken') rethrow;
      session = await _recoverRegistration(username, password, error);
    }
    await _activateSession(session);
    return session;
  }

  Future<FriendRoom> _createRoom(CreateRoomInput input) async {
    final requestId = _requestId('create');
    late final FriendRoom room;
    try {
      room = await _authorized(
        (token) => _api.createRoom(
          accessToken: token,
          preset: input.preset,
          password: input.password,
          smallBlind: input.smallBlind,
          bigBlind: input.bigBlind,
          maxBuyIn: input.maxBuyIn,
          buyIn: input.buyIn,
          requestId: requestId,
        ),
      );
    } on GameApiTimeoutException catch (error) {
      return _restoreCurrentRoom(error);
    } on GameApiException catch (error) {
      if (error.code != 'already_in_room') rethrow;
      return _restoreCurrentRoom(error);
    }
    await _enterRoom(room);
    return room;
  }

  Future<FriendRoom> _joinRoom(String code, String password, int buyIn) async {
    final requestId = _requestId('join');
    late final FriendRoom room;
    try {
      room = await _authorized(
        (token) => _api.joinRoom(
          accessToken: token,
          code: code,
          password: password,
          buyIn: buyIn,
          requestId: requestId,
        ),
      );
    } on GameApiTimeoutException catch (error) {
      return _restoreCurrentRoom(error);
    } on GameApiException catch (error) {
      if (error.code != 'already_in_room') rethrow;
      return _restoreCurrentRoom(error);
    }
    await _enterRoom(room);
    return room;
  }

  Future<void> _leaveRoom() async {
    try {
      await _authorized(_api.leaveRoom);
    } on GameApiTimeoutException {
      final current = await _authorized(_api.currentRoom);
      if (current != null) rethrow;
    }
    if (mounted) setState(() => _room = null);
    final chips = await _authorized(_api.bankroll);
    if (mounted) {
      setState(() {
        _bankroll = chips;
      });
    }
  }

  Future<List<RecentHand>> _loadRecentHands() =>
      _authorized((token) => _api.recentHands(accessToken: token));

  Future<List<BankrollEntry>> _loadBankrollEntries() =>
      _authorized((token) => _api.bankrollEntries(accessToken: token));

  Future<RoomPreview> _previewRoom(String code) =>
      _authorized((token) => _api.roomPreview(accessToken: token, code: code));

  Future<BankrollSnapshot> _topUp(int amount) async {
    final requestId = _requestId('topup');
    late final BankrollSnapshot chips;
    try {
      chips = await _authorized(
        (token) => _api.topUp(
          accessToken: token,
          requestId: requestId,
          amount: amount,
        ),
      );
    } on GameApiTimeoutException {
      chips = await _authorized(
        (token) => _api.topUp(
          accessToken: token,
          requestId: requestId,
          amount: amount,
        ),
      );
    }
    if (mounted) setState(() => _bankroll = chips);
    return chips;
  }

  Future<void> _activateSession(AuthSession session) async {
    final chips = await _api.bankroll(session.accessToken);
    FriendRoom? room;
    if (chips.tableId.isNotEmpty) {
      room = await _api.currentRoom(session.accessToken);
    }
    if (!mounted) return;
    setState(() {
      _session = session;
      _bankroll = chips;
      _room = room;
    });
    _startPresenceHeartbeat();
  }

  Future<AppUser> _updateUsername(String username) async {
    final user = await _authorized(
      (token) => _api.updateUsername(accessToken: token, username: username),
    );
    if (mounted) setState(() => _session = _session?.copyWith(user: user));
    return user;
  }

  Future<AppUser> _updateDisplayName(String displayName) async {
    final user = await _authorized(
      (token) =>
          _api.updateDisplayName(accessToken: token, displayName: displayName),
    );
    if (mounted) setState(() => _session = _session?.copyWith(user: user));
    return user;
  }

  Future<void> _deleteAccount(String password) async {
    await _authorized(
      (token) => _api.deleteAccount(accessToken: token, password: password),
    );
    // 服务端已撤销全部会话，本地直接清除登录态，不再调用远端登出
    _presenceTimer?.cancel();
    if (!mounted) return;
    setState(() {
      _session = null;
      _bankroll = null;
      _room = null;
    });
  }

  Future<AuthSession> _changePassword(
    String currentPassword,
    String newPassword,
  ) async {
    final updated = await _authorized(
      (token) => _api.changePassword(
        accessToken: token,
        currentPassword: currentPassword,
        newPassword: newPassword,
      ),
    );
    if (mounted) setState(() => _session = updated);
    _startPresenceHeartbeat();
    return updated;
  }

  /// 玩家已不在房间里：回到大厅并说明原因。
  ///
  /// [reason] 来自服务端的关闭原因（`removed_by_owner` 等）；拿不到时不弹窗，
  /// 因为那种情况多半是自己退的房或房间已关闭，突兀的提示反而误导。
  /// 向服务端确认本客户端是否还够新。
  ///
  /// 端点不存在（旧服务端）或网络不通时不阻断——那种情况该更新的是服务端，
  /// 或者只是暂时连不上，把客户端锁死只会更难排查。真正过旧的客户端在
  /// 随后的任何一次请求上都会被服务端以 426 拒绝，同样会走到阻断页。
  Future<void> _recheckClientVersion() async {
    final requirement = await _api.clientVersionRequirement();
    if (!mounted) return;
    if (requirement == null) {
      setState(() => _clientTooOld = false);
      return;
    }
    setState(() {
      _minimumClientVersion = requirement.minimum;
      _clientTooOld = requirement.blocksCurrentBuild;
    });
  }

  /// 任何一次请求被服务端以 426 拒绝时进入阻断态。
  void _handleClientTooOld(ClientTooOldException error) {
    if (!mounted) return;
    setState(() {
      _minimumClientVersion = error.minimumVersionCode;
      _clientTooOld = true;
    });
  }

  Future<void> _removedFromRoom(String reason) async {
    if (!mounted || _room == null) return;
    setState(() => _room = null);
    final message = _removalMessage(reason);
    if (message != null) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) unawaited(_showRemovalNotice(message));
      });
    }
    try {
      final chips = await _authorized(_api.bankroll);
      if (mounted) setState(() => _bankroll = chips);
    } on Object {
      // The lobby remains usable and refreshes the wallet on the next action.
    }
  }

  static String? _removalMessage(String reason) => switch (reason) {
    GameSocketClient.removedByOwner => '房主已将你请出房间。牌桌上的筹码已退回你的钱包。',
    GameSocketClient.removedByAdministrator =>
      '管理员已将你移出房间。牌桌上的筹码已退回你的钱包。',
    _ => null,
  };

  Future<void> _showRemovalNotice(String message) => showDialog<void>(
    context: context,
    builder: (context) => AlertDialog(
      title: const Text('已离开房间'),
      content: Text(message),
      actions: [
        FilledButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('知道了'),
        ),
      ],
    ),
  );

  void _startPresenceHeartbeat() {
    _presenceTimer?.cancel();
    if (_session == null) return;
    unawaited(_sendPresenceHeartbeat());
    _presenceTimer = Timer.periodic(const Duration(seconds: 30), (_) {
      if (_session != null) unawaited(_sendPresenceHeartbeat());
    });
  }

  Future<void> _sendPresenceHeartbeat() async {
    try {
      await _authorized(_api.heartbeat);
    } on Object {
      // Presence is best effort. A transient network failure must not interrupt
      // the lobby or surface as an unhandled asynchronous exception.
    }
  }

  void _logout() {
    final token = _session?.accessToken;
    _presenceTimer?.cancel();
    setState(() {
      _session = null;
      _bankroll = null;
      _room = null;
    });
    if (token != null) unawaited(_logoutRemote(token));
  }

  Future<AuthSession> _recoverRegistration(
    String username,
    String password,
    Object originalError,
  ) async {
    try {
      return await _api.login(username: username, password: password);
    } on Object {
      throw originalError;
    }
  }

  Future<void> _logoutRemote(String token) async {
    try {
      await _api.logout(token);
    } on Object {
      // Local logout must always succeed even when the network is unavailable.
    }
  }

  Future<FriendRoom> _restoreCurrentRoom(Object originalError) async {
    try {
      final room = await _authorized(_api.currentRoom);
      if (room == null) throw originalError;
      await _enterRoom(room);
      return room;
    } on GameApiException {
      throw originalError;
    } on GameApiTimeoutException {
      throw originalError;
    }
  }

  Future<void> _enterRoom(FriendRoom room) async {
    if (mounted) setState(() => _room = room);
    try {
      final chips = await _authorized(_api.bankroll);
      if (mounted) setState(() => _bankroll = chips);
    } on GameApiTimeoutException {
      // Joining has already succeeded. Keep the table open and refresh the
      // wallet after the connection recovers instead of reporting a false
      // join failure or encouraging another buy-in attempt.
    } on GameApiException {
      // The room is authoritative once the mutation/current-room lookup has
      // succeeded. A separate wallet refresh failure must not strand the user
      // in the lobby or trigger another buy-in attempt.
    }
  }

  String _requestId(String prefix) =>
      '$prefix-${DateTime.now().microsecondsSinceEpoch}';

  Future<String> _accessToken({bool forceRefresh = false}) async {
    final session = _session;
    if (session == null) {
      throw const GameApiException('authentication_required', statusCode: 401);
    }
    final now = DateTime.now();
    if (!forceRefresh &&
        session.accessExpiresAt.isAfter(now.add(const Duration(minutes: 2)))) {
      return session.accessToken;
    }
    if (!session.refreshExpiresAt.isAfter(now)) {
      _expireSession();
      throw const GameApiException('authentication_required', statusCode: 401);
    }
    final pending = _sessionRefresh;
    if (pending != null) return (await pending).accessToken;

    final refresh = _api.refresh(session.refreshToken);
    _sessionRefresh = refresh;
    try {
      final updated = await refresh;
      if (_session?.refreshToken == session.refreshToken && mounted) {
        setState(() => _session = updated);
      }
      return updated.accessToken;
    } on GameApiException catch (error) {
      if (error.code == 'invalid_refresh_token' || error.statusCode == 401) {
        _expireSession();
      }
      rethrow;
    } finally {
      if (identical(_sessionRefresh, refresh)) _sessionRefresh = null;
    }
  }

  Future<T> _authorized<T>(Future<T> Function(String token) operation) async {
    var token = await _accessToken();
    try {
      return await operation(token);
    } on ClientTooOldException catch (error) {
      // 服务端在任何接口上拒绝这个版本：整个应用进入阻断态，而不是把它
      // 当成一次失败的操作反复重试。
      _handleClientTooOld(error);
      rethrow;
    } on GameApiException catch (error) {
      if (error.statusCode != 401) rethrow;
      token = await _accessToken(forceRefresh: true);
      try {
        return await operation(token);
      } on GameApiException catch (retryError) {
        if (retryError.statusCode == 401) _expireSession();
        rethrow;
      }
    }
  }

  void _expireSession() {
    _presenceTimer?.cancel();
    if (!mounted || _session == null) return;
    setState(() {
      _session = null;
      _bankroll = null;
      _room = null;
    });
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _navigatorKey.currentState?.popUntil((route) => route.isFirst);
    });
  }
}
