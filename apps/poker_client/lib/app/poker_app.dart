import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:poker_client/core/auth/auth_session.dart';
import 'package:poker_client/core/auth/session_store.dart';
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
  const PokerApp({super.key, this.apiClient, this.sessionStore});

  /// 仅供测试注入：传入用 MockClient 构造的客户端，就能在不联网的情况下
  /// 走通登录、恢复会话这些整条链路。生产不传，自己建一个。
  @visibleForTesting
  final GameApiClient? apiClient;

  /// 仅供测试注入：默认实现在 Web 上不持久化，测试要覆盖这个行为。
  @visibleForTesting
  final SessionStore? sessionStore;

  @override
  State<PokerApp> createState() => _PokerAppState();
}

class _PokerAppState extends State<PokerApp> with WidgetsBindingObserver {
  final _navigatorKey = GlobalKey<NavigatorState>();
  late final GameApiClient _api;
  late final AppSettingsController _settings;
  AuthSession? _session;
  late final SessionStore _sessions;
  /// 启动时正在用存下来的刷新令牌换会话。此时既不该显示登录页（会闪一下
  /// 又跳走），也不该显示大厅（还不知道是谁）。
  /// Web 端不持久化，没有可恢复的东西，第一帧直接显示登录页。
  bool _restoringSession = !kIsWeb;
  /// 上次恢复因为网络之类的临时原因失败，令牌还留着，值得再试一次。
  bool _restoreRetryable = false;
  bool _restoreInFlight = false;
  /// 每次恢复的代次。超时只是放弃等待——`Future.timeout` 不会取消底层的
  /// 网络请求，那个请求可能十几秒后才成功返回。代次让迟到的结果知道自己
  /// 已经作废，不去动界面。
  int _restoreGeneration = 0;
  /// 恢复的总超时。三个串行请求各自能等 30 秒，不设上限就会把启动卡住。
  static const _restoreTimeout = Duration(seconds: 8);
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
    _api = widget.apiClient ?? GameApiClient();
    _sessions = widget.sessionStore ?? SessionStore();
    _settings = AppSettingsController()..load();
    // 必须排在 _api 之后：版本检查与会话恢复都要用它。
    unawaited(_recheckClientVersion());
    unawaited(_restoreSession());
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state != AppLifecycleState.resumed) return;
    if (shouldUseCurrentPlatformDartSystemUi) {
      unawaited(_enableImmersiveMode());
    }
    // 上次恢复只是因为没网之类的原因失败，令牌还在。回到前台时再试一次：
    // 否则「不清除令牌」这个设计要等到下一次冷启动才兑现，而玩家此刻正
    // 对着登录页，手里明明有一个能用的令牌。
    if (_session == null && _restoreRetryable && !_restoreInFlight) {
      _restoreRetryable = false;
      // 静默重试：不切回等待界面。切回去会把 AuthPage 整个卸载，玩家已经
      // 输入的用户名、密码连同输入框一起没了——而触发这条路径的前提正是
      // 「上次因为网络失败」，也就是玩家很可能正在手动登录。恢复成功时
      // _activateSession 自己会切到大厅。
      unawaited(_restoreSession(showWaiting: false));
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
      // 恢复登录态期间先显示等待，否则会闪一下登录页再跳进大厅
      if (_restoringSession) {
        return const Scaffold(
          key: ValueKey('session-restoring'),
          body: Center(child: CircularProgressIndicator()),
        );
      }
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

  /// 用设备上存着的刷新令牌换回登录态。
  ///
  /// 失败一律安静地回到登录页：令牌可能已过期、被服务端吊销，或者这台设备
  /// 根本没存过。这是启动路径，不该因此弹错误。
  ///
  /// 整个过程有总超时。它串了三个请求（换会话、查钱包、查房间），每个都能
  /// 等满 30 秒；服务端挂起时，玩家会对着一个没有文字也没有出口的转圈等上
  /// 一分半，而在这个功能之前登录页是立刻出现的。宁可放弃这次恢复。
  Future<void> _restoreSession({bool showWaiting = true}) async {
    if (_restoreInFlight) return;
    _restoreInFlight = true;
    final generation = ++_restoreGeneration;
    try {
      await _attemptRestore(generation).timeout(_restoreTimeout);
    } on TimeoutException {
      // 超时只是放弃等待，请求还在跑。递增代次让那个迟到的结果不再落地：
      // 玩家此刻已经在登录页上，十几秒后界面突然自己跳进大厅、甚至覆盖掉
      // 他手动登录的账号，比让他多等一次严重得多。轮换出来的新令牌仍会被
      // 存下来，切后台回来重试时就能用上。
      _restoreGeneration++;
      _restoreRetryable = true;
    } on Object {
      // 分类处理都在 _attemptRestore 内部完成
    } finally {
      // 不在这里释放 _restoreInFlight：超时的时候 _attemptRestore 还在跑，
      // 提前释放会让「回到前台重试」启动第二次恢复，两次拿着同一个令牌去
      // 换会话。服务端刷新时会删掉旧会话，于是后一次必然 401，还会把存储
      // 清掉——玩家的登录态就这么没了。改由 _attemptRestore 自己释放。
      if (showWaiting && mounted) setState(() => _restoringSession = false);
    }
  }

  Future<void> _attemptRestore(int generation) async {
    try {
      final token = await _sessions.loadRefreshToken();
      if (token == null) {
        _restoreRetryable = false;
        return;
      }
      // 只有换会话这一步的失败才说明令牌本身有问题，所以 try 只包它；
      // 后面几步的 401 不该连累一个刚刚成功轮换过的令牌。
      final AuthSession session;
      try {
        session = await _api.refresh(token);
      } on GameApiException catch (error) {
        if (error.code == 'invalid_refresh_token' || error.statusCode == 401) {
          await _sessions.clear();
          _restoreRetryable = false;
        }
        rethrow;
      }
      // 服务端刷新时会删掉旧会话，所以此刻旧令牌已经作废，新的必须存下来，
      // 否则它就丢了，下次启动照样登不回来。唯一不该存的情况是玩家已经手动
      // 登录了（可能是另一个账号）——那时存储归他。
      if (_session != null) return;
      await _sessions.save(session);
      // 这次恢复可能已经因为超时而作废，或者玩家已经手动登录。两种情况都
      // 不能再去改会话状态。
      if (generation != _restoreGeneration || _session != null) return;
      await _activateSession(
        session,
        stale: () => generation != _restoreGeneration || _session != null,
      );
      _restoreRetryable = false;
    } on ClientTooOldException catch (error) {
      // 换会话的接口豁免版本门禁，后面的接口不豁免，于是 426 会落到这里。
      // 不处理的话它会被当成普通失败吞掉，玩家看到的是登录页而不是阻断页。
      _handleClientTooOld(error);
      _restoreRetryable = false;
      rethrow;
    } on Object {
      // 没网、超时、服务端 5xx 都会走到这里。这些都不代表令牌失效，清掉它
      // 只会让玩家在信号不好的地方启动一次就得重新登录；留着并允许重试。
      _restoreRetryable = true;
      rethrow;
    } finally {
      // 真正跑完才释放，见 _restoreSession 里对并发的说明
      _restoreInFlight = false;
    }
  }

  /// [stale] 由恢复路径传入：它内部还有几次网络等待，等待期间这次恢复
  /// 可能已经作废（超时）或被玩家的手动登录取代，那就不能再改会话状态。
  /// 登录与注册路径不传，行为不变。
  Future<void> _activateSession(
    AuthSession session, {
    bool Function()? stale,
  }) async {
    final chips = await _api.bankroll(session.accessToken);
    if (stale?.call() ?? false) return;
    FriendRoom? room;
    if (chips.tableId.isNotEmpty) {
      // 这里不做「拿不到就进大厅」的降级。服务端说玩家还在一桌牌局里，
      // 把他放进大厅会造成一个自相矛盾的状态：钱包里少了一笔带入，界面上
      // 却没有座位，而大厅并不显示「你还在某桌」，唯一的回去方式是点创建
      // 房间撞上 already_in_room——没人会想到这么做。宁可让这次失败，
      // 玩家重试一次即可。
      room = await _api.currentRoom(session.accessToken);
    }
    // 每次拿到会话都存一遍刷新令牌：服务端会轮换它，存着的旧令牌下次启动
    // 时已经作废。
    await _sessions.save(session);
    if (!mounted || (stale?.call() ?? false)) return;
    // 已经有会话了，回到前台时不必再去恢复
    _restoreRetryable = false;
    setState(() {
      _session = session;
      _bankroll = chips;
      _room = room;
      _restoringSession = false;
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
    await _sessions.clear();
    if (!mounted) return;
    setState(() {
      _session = null;
      _bankroll = null;
      _room = null;
      _restoringSession = false;
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
    // 改密码会换一份新会话。服务端目前不吊销旧会话，所以存着的旧令牌仍然
    // 能用；但让存储与当前会话保持一致，将来若改成吊销旧会话也不会出问题。
    await _sessions.save(updated);
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

  Future<void> _logout() async {
    final token = _session?.accessToken;
    _presenceTimer?.cancel();
    // 主动登出必须清掉存储，否则下次启动又自动登回来。这里等它写完再继续：
    // 「发射后不管」的话，登出后立刻杀掉应用重启，理论上还能读到没清完的
    // 令牌，表现为又自动登了回来——这对账号安全感的伤害远大于多等几毫秒。
    await _sessions.clear();
    if (!mounted) return;
    setState(() {
      _session = null;
      _bankroll = null;
      _room = null;
      _restoringSession = false;
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
        // 等它写完再返回。服务端刷新时会删掉旧会话，此刻存储里的旧令牌已经
        // 作废；如果进程在写入落盘之前被系统回收（这正是本功能要应对的场景），
        // 下次启动拿到的就是一个必然被拒的令牌。多等几毫秒换掉这个窗口。
        //
        // 保存必须和上面的判断在一起：刷新期间玩家可能已经登出并清空了存储，
        // 那时不能把令牌写回去，否则下次启动又自动登了回来。
        await _sessions.save(updated);
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
    // 令牌已被服务端拒绝，存着也换不回会话
    unawaited(_sessions.clear());
    if (!mounted || _session == null) return;
    setState(() {
      _session = null;
      _bankroll = null;
      _room = null;
      _restoringSession = false;
    });
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _navigatorKey.currentState?.popUntil((route) => route.isFirst);
    });
  }
}
