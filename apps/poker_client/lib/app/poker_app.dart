import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:poker_client/core/auth/auth_session.dart';
import 'package:poker_client/core/network/game_api_client.dart';
import 'package:poker_client/core/settings/app_settings.dart';
import 'package:poker_client/features/auth/presentation/auth_page.dart';
import 'package:poker_client/features/bankroll/domain/bankroll_entry.dart';
import 'package:poker_client/features/bankroll/domain/bankroll_snapshot.dart';
import 'package:poker_client/features/history/domain/recent_hand.dart';
import 'package:poker_client/features/lobby/domain/friend_room.dart';
import 'package:poker_client/features/lobby/presentation/lobby_page.dart';
import 'package:poker_client/features/table/presentation/table_prototype_page.dart';

class PokerApp extends StatefulWidget {
  const PokerApp({super.key});

  @override
  State<PokerApp> createState() => _PokerAppState();
}

class _PokerAppState extends State<PokerApp> with WidgetsBindingObserver {
  late final GameApiClient _api;
  late final AppSettingsController _settings;
  AuthSession? _session;
  FriendRoom? _room;
  BankrollSnapshot? _bankroll;
  Timer? _presenceTimer;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
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
    _api = GameApiClient();
    _settings = AppSettingsController()..load();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      unawaited(_enableImmersiveMode());
    }
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    unawaited(SystemChrome.setSystemUIChangeCallback(null));
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
        onChangePassword: _changePassword,
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
      loadBankroll: () => _api.bankroll(_session!.accessToken),
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
      room = await _api.createRoom(
        accessToken: _session!.accessToken,
        preset: input.preset,
        maxPlayers: input.maxPlayers,
        password: input.password,
        smallBlind: input.smallBlind,
        bigBlind: input.bigBlind,
        maxBuyIn: input.maxBuyIn,
        buyIn: input.buyIn,
        requestId: requestId,
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
      room = await _api.joinRoom(
        accessToken: _session!.accessToken,
        code: code,
        password: password,
        buyIn: buyIn,
        requestId: requestId,
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
    final accessToken = _session!.accessToken;
    try {
      await _api.leaveRoom(accessToken);
    } on GameApiTimeoutException {
      final current = await _api.currentRoom(accessToken);
      if (current != null) rethrow;
    }
    if (mounted) setState(() => _room = null);
    final chips = await _api.bankroll(accessToken);
    if (mounted) {
      setState(() {
        _bankroll = chips;
      });
    }
  }

  Future<List<RecentHand>> _loadRecentHands() =>
      _api.recentHands(accessToken: _session!.accessToken);

  Future<List<BankrollEntry>> _loadBankrollEntries() =>
      _api.bankrollEntries(accessToken: _session!.accessToken);

  Future<RoomPreview> _previewRoom(String code) =>
      _api.roomPreview(accessToken: _session!.accessToken, code: code);

  Future<BankrollSnapshot> _topUp(int amount) async {
    final requestId = _requestId('topup');
    late final BankrollSnapshot chips;
    try {
      chips = await _api.topUp(
        accessToken: _session!.accessToken,
        requestId: requestId,
        amount: amount,
      );
    } on GameApiTimeoutException {
      chips = await _api.topUp(
        accessToken: _session!.accessToken,
        requestId: requestId,
        amount: amount,
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
    final session = _session!;
    final user = await _api.updateUsername(
      accessToken: session.accessToken,
      username: username,
    );
    if (mounted) setState(() => _session = session.copyWith(user: user));
    return user;
  }

  Future<AuthSession> _changePassword(
    String currentPassword,
    String newPassword,
  ) async {
    final updated = await _api.changePassword(
      accessToken: _session!.accessToken,
      currentPassword: currentPassword,
      newPassword: newPassword,
    );
    if (mounted) setState(() => _session = updated);
    _startPresenceHeartbeat();
    return updated;
  }

  Future<void> _removedFromRoom() async {
    if (!mounted || _room == null) return;
    setState(() => _room = null);
    try {
      final chips = await _api.bankroll(_session!.accessToken);
      if (mounted) setState(() => _bankroll = chips);
    } on Object {
      // The lobby remains usable and refreshes the wallet on the next action.
    }
  }

  void _startPresenceHeartbeat() {
    _presenceTimer?.cancel();
    final token = _session?.accessToken;
    if (token == null) return;
    unawaited(_sendPresenceHeartbeat(token));
    _presenceTimer = Timer.periodic(const Duration(seconds: 30), (_) {
      final currentToken = _session?.accessToken;
      if (currentToken != null) {
        unawaited(_sendPresenceHeartbeat(currentToken));
      }
    });
  }

  Future<void> _sendPresenceHeartbeat(String token) async {
    try {
      await _api.heartbeat(token);
    } on Object {
      // Presence is best effort. A transient network failure must not interrupt
      // the lobby or surface as an unhandled asynchronous exception.
    }
  }

  void _logout() {
    _presenceTimer?.cancel();
    setState(() {
      _session = null;
      _bankroll = null;
      _room = null;
    });
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

  Future<FriendRoom> _restoreCurrentRoom(Object originalError) async {
    try {
      final room = await _api.currentRoom(_session!.accessToken);
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
      final chips = await _api.bankroll(_session!.accessToken);
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
}
