import 'package:flutter/material.dart';
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

class _PokerAppState extends State<PokerApp> {
  late final GameApiClient _api;
  late final AppSettingsController _settings;
  AuthSession? _session;
  FriendRoom? _room;
  BankrollSnapshot? _bankroll;

  @override
  void initState() {
    super.initState();
    _api = GameApiClient();
    _settings = AppSettingsController()..load();
  }

  @override
  void dispose() {
    _api.close();
    _settings.dispose();
    super.dispose();
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
        settings: _settings,
        onLogout: () => setState(() {
          _session = null;
          _bankroll = null;
        }),
      );
    }
    return TablePrototypePage(
      session: session,
      room: room,
      settings: _settings,
      onLeave: _leaveRoom,
      loadBankroll: () => _api.bankroll(_session!.accessToken),
    );
  }

  Future<AuthSession> _login(String username, String password) async {
    final session = await _api.login(username: username, password: password);
    final chips = await _api.bankroll(session.accessToken);
    if (mounted) {
      setState(() {
        _session = session;
        _bankroll = chips;
      });
    }
    return session;
  }

  Future<AuthSession> _register(
    String username,
    String displayName,
    String password,
  ) async {
    final session = await _api.register(
      username: username,
      displayName: displayName,
      password: password,
    );
    final chips = await _api.bankroll(session.accessToken);
    if (mounted) {
      setState(() {
        _session = session;
        _bankroll = chips;
      });
    }
    return session;
  }

  Future<FriendRoom> _createRoom(CreateRoomInput input) async {
    final room = await _api.createRoom(
      accessToken: _session!.accessToken,
      preset: input.preset,
      maxPlayers: input.maxPlayers,
      password: input.password,
      smallBlind: input.smallBlind,
      bigBlind: input.bigBlind,
      maxBuyIn: input.maxBuyIn,
      buyIn: input.buyIn,
      requestId: _requestId('create'),
    );
    final chips = await _api.bankroll(_session!.accessToken);
    if (mounted) {
      setState(() {
        _room = room;
        _bankroll = chips;
      });
    }
    return room;
  }

  Future<FriendRoom> _joinRoom(String code, String password, int buyIn) async {
    final room = await _api.joinRoom(
      accessToken: _session!.accessToken,
      code: code,
      password: password,
      buyIn: buyIn,
      requestId: _requestId('join'),
    );
    final chips = await _api.bankroll(_session!.accessToken);
    if (mounted) {
      setState(() {
        _room = room;
        _bankroll = chips;
      });
    }
    return room;
  }

  Future<void> _leaveRoom() async {
    await _api.leaveRoom(_session!.accessToken);
    final chips = await _api.bankroll(_session!.accessToken);
    if (mounted) {
      setState(() {
        _room = null;
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
    final chips = await _api.topUp(
      accessToken: _session!.accessToken,
      requestId: _requestId('topup'),
      amount: amount,
    );
    if (mounted) setState(() => _bankroll = chips);
    return chips;
  }

  String _requestId(String prefix) =>
      '$prefix-${DateTime.now().microsecondsSinceEpoch}';
}
