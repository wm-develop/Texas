import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:poker_client/core/auth/auth_session.dart';
import 'package:poker_client/core/network/game_api_client.dart';
import 'package:poker_client/core/settings/app_settings.dart';
import 'package:poker_client/core/settings/settings_dialog.dart';
import 'package:poker_client/features/history/domain/recent_hand.dart';
import 'package:poker_client/features/history/presentation/recent_hands_page.dart';
import 'package:poker_client/features/lobby/domain/friend_room.dart';

class LobbyPage extends StatefulWidget {
  const LobbyPage({
    required this.session,
    required this.onCreateRoom,
    required this.onJoinRoom,
    required this.onLoadRecentHands,
    required this.settings,
    required this.onLogout,
    super.key,
  });

  final AuthSession session;
  final Future<FriendRoom> Function(
    String preset,
    int maxPlayers,
    String password,
  )
  onCreateRoom;
  final Future<FriendRoom> Function(String code, String password) onJoinRoom;
  final Future<List<RecentHand>> Function() onLoadRecentHands;
  final AppSettingsController settings;
  final VoidCallback onLogout;

  @override
  State<LobbyPage> createState() => _LobbyPageState();
}

class _LobbyPageState extends State<LobbyPage> {
  final _roomCode = TextEditingController();
  final _joinPassword = TextEditingController();
  final _createPassword = TextEditingController();
  String _preset = 'standard';
  double _maxPlayers = 6;
  bool _busy = false;
  String? _error;

  @override
  void dispose() {
    _roomCode.dispose();
    _joinPassword.dispose();
    _createPassword.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('好友房大厅'),
        actions: [
          IconButton(
            onPressed: () => showAppSettingsDialog(context, widget.settings),
            icon: const Icon(Icons.settings_outlined),
            tooltip: '声音与语音设置',
          ),
          IconButton(
            onPressed: _busy ? null : _openRecentHands,
            icon: const Icon(Icons.history),
            tooltip: '最近牌局',
          ),
          Center(child: Text(widget.session.user.displayName)),
          const SizedBox(width: 8),
          IconButton(
            onPressed: _busy ? null : widget.onLogout,
            icon: const Icon(Icons.logout),
            tooltip: '退出登录',
          ),
          const SizedBox(width: 8),
        ],
      ),
      body: DecoratedBox(
        decoration: const BoxDecoration(
          gradient: RadialGradient(
            colors: [Color(0xFF16473B), Color(0xFF061814)],
            radius: 1.2,
          ),
        ),
        child: Center(
          child: SingleChildScrollView(
            padding: const EdgeInsets.all(24),
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 1000),
              child: Column(
                children: [
                  if (_error != null)
                    Padding(
                      padding: const EdgeInsets.only(bottom: 12),
                      child: Text(
                        _error!,
                        style: const TextStyle(color: Colors.redAccent),
                      ),
                    ),
                  LayoutBuilder(
                    builder: (context, constraints) {
                      final cards = [_buildJoinCard(), _buildCreateCard()];
                      if (constraints.maxWidth >= 760) {
                        return Row(
                          crossAxisAlignment: CrossAxisAlignment.start,
                          children: [
                            Expanded(child: cards[0]),
                            const SizedBox(width: 20),
                            Expanded(child: cards[1]),
                          ],
                        );
                      }
                      return Column(
                        children: [
                          cards[0],
                          const SizedBox(height: 20),
                          cards[1],
                        ],
                      );
                    },
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }

  Widget _buildJoinCard() => Card(
    child: Padding(
      padding: const EdgeInsets.all(24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          const Icon(Icons.group_add, size: 42, color: Color(0xFFD9B85F)),
          const SizedBox(height: 12),
          Text('加入朋友的牌桌', style: Theme.of(context).textTheme.titleLarge),
          const SizedBox(height: 16),
          TextField(
            controller: _roomCode,
            keyboardType: TextInputType.number,
            inputFormatters: [FilteringTextInputFormatter.digitsOnly],
            maxLength: 6,
            decoration: const InputDecoration(
              labelText: '6 位房间码',
              counterText: '',
              border: OutlineInputBorder(),
            ),
          ),
          const SizedBox(height: 12),
          TextField(
            controller: _joinPassword,
            obscureText: true,
            decoration: const InputDecoration(
              labelText: '房间密码（没有可留空）',
              border: OutlineInputBorder(),
            ),
          ),
          const SizedBox(height: 18),
          FilledButton.icon(
            onPressed: _busy ? null : _join,
            icon: const Icon(Icons.login),
            label: const Text('加入牌桌'),
          ),
        ],
      ),
    ),
  );

  Widget _buildCreateCard() => Card(
    child: Padding(
      padding: const EdgeInsets.all(24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          const Icon(
            Icons.add_circle_outline,
            size: 42,
            color: Color(0xFFD9B85F),
          ),
          const SizedBox(height: 12),
          Text('创建好友牌桌', style: Theme.of(context).textTheme.titleLarge),
          const SizedBox(height: 16),
          DropdownButtonFormField<String>(
            initialValue: _preset,
            decoration: const InputDecoration(
              labelText: '牌局预设',
              border: OutlineInputBorder(),
            ),
            items: const [
              DropdownMenuItem(
                value: 'casual',
                child: Text('休闲 · 1000 筹码 · 5/10'),
              ),
              DropdownMenuItem(
                value: 'standard',
                child: Text('标准 · 2000 筹码 · 10/20'),
              ),
              DropdownMenuItem(
                value: 'deep',
                child: Text('深筹 · 5000 筹码 · 10/20'),
              ),
            ],
            onChanged: _busy
                ? null
                : (value) => setState(() => _preset = value!),
          ),
          const SizedBox(height: 10),
          Text('人数上限：${_maxPlayers.round()} 人'),
          Slider(
            value: _maxPlayers,
            min: 2,
            max: 10,
            divisions: 8,
            label: '${_maxPlayers.round()}',
            onChanged: _busy
                ? null
                : (value) => setState(() => _maxPlayers = value),
          ),
          TextField(
            controller: _createPassword,
            obscureText: true,
            decoration: const InputDecoration(
              labelText: '房间密码（可选，至少 4 位）',
              border: OutlineInputBorder(),
            ),
          ),
          const SizedBox(height: 18),
          FilledButton.icon(
            onPressed: _busy ? null : _create,
            icon: const Icon(Icons.add),
            label: const Text('创建牌桌'),
          ),
        ],
      ),
    ),
  );

  Future<void> _join() async {
    if (_roomCode.text.trim().length != 6) {
      setState(() => _error = '请输入完整的 6 位房间码');
      return;
    }
    await _run(
      () => widget.onJoinRoom(_roomCode.text.trim(), _joinPassword.text),
    );
  }

  void _openRecentHands() {
    Navigator.of(context).push(
      MaterialPageRoute<void>(
        builder: (context) => RecentHandsPage(
          userId: widget.session.user.userId,
          loadHands: widget.onLoadRecentHands,
        ),
      ),
    );
  }

  Future<void> _create() async {
    final password = _createPassword.text;
    if (password.isNotEmpty && password.length < 4) {
      setState(() => _error = '房间密码至少需要 4 位');
      return;
    }
    await _run(
      () => widget.onCreateRoom(_preset, _maxPlayers.round(), password),
    );
  }

  Future<void> _run(Future<FriendRoom> Function() operation) async {
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await operation();
    } on GameApiException catch (error) {
      if (mounted) setState(() => _error = _roomError(error.code));
    } on Object {
      if (mounted) setState(() => _error = '无法连接游戏服务，请确认服务端已启动');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }
}

String _roomError(String code) => switch (code) {
  'room_not_found' => '没有找到这个房间',
  'invalid_room_password' => '房间密码不正确',
  'room_full' => '这个房间已经满员',
  'already_in_room' => '你已经在另一个房间中',
  _ => '操作失败（$code）',
};
