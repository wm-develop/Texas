import 'package:flutter/material.dart';
import 'package:poker_client/core/auth/auth_session.dart';
import 'package:poker_client/core/network/game_api_client.dart';
import 'package:poker_client/core/settings/app_settings.dart';
import 'package:poker_client/core/settings/settings_dialog.dart';
import 'package:poker_client/core/widgets/platform_number_field.dart';
import 'package:poker_client/features/admin/presentation/admin_page.dart';
import 'package:poker_client/features/bankroll/domain/bankroll_entry.dart';
import 'package:poker_client/features/bankroll/domain/bankroll_snapshot.dart';
import 'package:poker_client/features/history/domain/recent_hand.dart';
import 'package:poker_client/features/history/presentation/recent_hands_page.dart';
import 'package:poker_client/features/lobby/domain/friend_room.dart';
import 'package:poker_client/features/profile/presentation/profile_page.dart';

class LobbyPage extends StatefulWidget {
  const LobbyPage({
    required this.session,
    required this.bankroll,
    required this.onCreateRoom,
    required this.onJoinRoom,
    required this.onLoadRecentHands,
    required this.onTopUp,
    required this.onLoadBankrollEntries,
    required this.onPreviewRoom,
    required this.onUpdateUsername,
    required this.onUpdateDisplayName,
    required this.onChangePassword,
    required this.accessTokenProvider,
    required this.settings,
    required this.onLogout,
    super.key,
  });

  final AuthSession session;
  final BankrollSnapshot bankroll;
  final Future<FriendRoom> Function(CreateRoomInput input) onCreateRoom;
  final Future<FriendRoom> Function(String code, String password, int buyIn)
  onJoinRoom;
  final Future<List<RecentHand>> Function() onLoadRecentHands;
  final Future<BankrollSnapshot> Function(int amount) onTopUp;
  final Future<List<BankrollEntry>> Function() onLoadBankrollEntries;
  final Future<RoomPreview> Function(String code) onPreviewRoom;
  final Future<AppUser> Function(String username) onUpdateUsername;
  final Future<AppUser> Function(String displayName) onUpdateDisplayName;
  final Future<AuthSession> Function(String currentPassword, String newPassword)
  onChangePassword;
  final Future<String> Function({bool forceRefresh}) accessTokenProvider;
  final AppSettingsController settings;
  final VoidCallback onLogout;

  @override
  State<LobbyPage> createState() => _LobbyPageState();
}

class _LobbyPageState extends State<LobbyPage> {
  final _roomCode = TextEditingController();
  final _joinPassword = TextEditingController();
  final _createPassword = TextEditingController();
  final _smallBlind = TextEditingController(text: '10');
  final _bigBlind = TextEditingController(text: '20');
  final _maxBuyIn = TextEditingController(text: '2000');
  final _createBuyIn = TextEditingController(text: '2000');
  String _preset = 'standard';
  bool _busy = false;
  String? _error;

  @override
  void dispose() {
    _roomCode.dispose();
    _joinPassword.dispose();
    _createPassword.dispose();
    _smallBlind.dispose();
    _bigBlind.dispose();
    _maxBuyIn.dispose();
    _createBuyIn.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('好友房大厅'),
        actions: [
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 8),
            child: ActionChip(
              avatar: const Icon(Icons.toll, size: 18),
              label: Text('总筹码 ${widget.bankroll.walletChips}'),
              onPressed: _busy ? null : _showTopUpDialog,
            ),
          ),
          IconButton(
            onPressed: _busy ? null : _showBankrollEntries,
            icon: const Icon(Icons.receipt_long_outlined),
            tooltip: '筹码流水',
          ),
          const SizedBox(width: 8),
          IconButton(
            onPressed: () => showAppSettingsDialog(
              context,
              widget.settings,
              onOpenAdmin: widget.session.user.isAdmin ? _openAdmin : null,
            ),
            icon: const Icon(Icons.settings_outlined),
            tooltip: '声音与语音设置',
          ),
          IconButton(
            onPressed: _busy ? null : _openRecentHands,
            icon: const Icon(Icons.history),
            tooltip: '最近牌局',
          ),
          TextButton.icon(
            onPressed: _busy ? null : _openProfile,
            icon: const Icon(Icons.account_circle_outlined),
            label: Text(widget.session.user.username),
          ),
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
        child: Builder(
          builder: (context) {
            // 与登录页相同：布局档位用不随键盘变化的窗口尺寸判定，避免
            // 键盘压缩 body 高度导致布局翻转、输入框失焦、键盘收回。
            final windowSize = MediaQuery.sizeOf(context);
            final compactLandscape =
                windowSize.height <= 600 &&
                windowSize.width >= windowSize.height * 1.35;
            return Center(
              child: SingleChildScrollView(
                padding: compactLandscape
                    ? const EdgeInsets.fromLTRB(12, 8, 12, 10)
                    : const EdgeInsets.all(24),
                child: ConstrainedBox(
                  constraints: BoxConstraints(
                    maxWidth: compactLandscape ? 1200 : 1000,
                  ),
                  child: Column(
                    children: [
                      if (_error != null)
                        Padding(
                          padding: EdgeInsets.only(
                            bottom: compactLandscape ? 6 : 12,
                          ),
                          child: Text(
                            _error!,
                            style: const TextStyle(color: Colors.redAccent),
                          ),
                        ),
                      LayoutBuilder(
                        builder: (context, constraints) {
                          final cards = [
                            _buildJoinCard(compact: compactLandscape),
                            _buildCreateCard(compact: compactLandscape),
                          ];
                          if (compactLandscape || constraints.maxWidth >= 760) {
                            return Row(
                              crossAxisAlignment: CrossAxisAlignment.start,
                              children: [
                                Expanded(child: cards[0]),
                                SizedBox(width: compactLandscape ? 12 : 20),
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
            );
          },
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

  Future<void> _openProfile() => Navigator.of(context).push(
    MaterialPageRoute<void>(
      builder: (_) => ProfilePage(
        session: widget.session,
        onUpdateUsername: widget.onUpdateUsername,
        onUpdateDisplayName: widget.onUpdateDisplayName,
        onChangePassword: widget.onChangePassword,
      ),
    ),
  );

  Widget _buildJoinCard({required bool compact}) => Card(
    child: Padding(
      padding: EdgeInsets.all(compact ? 12 : 24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          _cardTitle(icon: Icons.group_add, title: '加入朋友的牌桌', compact: compact),
          SizedBox(height: compact ? 6 : 16),
          PlatformNumberField(
            controller: _roomCode,
            maxLength: 6,
            decoration: _fieldDecoration(
              '6 位房间码',
              compact: compact,
              counterText: '',
            ),
          ),
          SizedBox(height: compact ? 6 : 12),
          TextField(
            controller: _joinPassword,
            obscureText: true,
            decoration: _fieldDecoration('房间密码（没有可留空）', compact: compact),
          ),
          SizedBox(height: compact ? 6 : 18),
          FilledButton.icon(
            onPressed: _busy ? null : _join,
            icon: const Icon(Icons.login),
            label: const Text('加入牌桌'),
          ),
        ],
      ),
    ),
  );

  Widget _buildCreateCard({required bool compact}) => Card(
    child: Padding(
      padding: EdgeInsets.all(compact ? 12 : 24),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          _cardTitle(
            icon: Icons.add_circle_outline,
            title: '创建好友牌桌',
            compact: compact,
          ),
          SizedBox(height: compact ? 6 : 16),
          DropdownButtonFormField<String>(
            initialValue: _preset,
            isExpanded: true,
            decoration: _fieldDecoration('牌局预设', compact: compact),
            items: const [
              DropdownMenuItem(
                value: 'casual',
                child: Text('休闲 · 1000 筹码 · 10/20'),
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
            onChanged: _busy ? null : (value) => _applyPreset(value!),
          ),
          SizedBox(height: compact ? 6 : 12),
          Row(
            children: [
              Expanded(child: _chipField(_smallBlind, '小盲', compact)),
              const SizedBox(width: 10),
              Expanded(child: _chipField(_bigBlind, '大盲', compact)),
            ],
          ),
          if (!compact) ...[
            const SizedBox(height: 6),
            const Text(
              '最低盲注 10/20，大盲必须是小盲的整数倍',
              style: TextStyle(color: Colors.white60, fontSize: 12),
            ),
          ],
          SizedBox(height: compact ? 6 : 12),
          Row(
            children: [
              Expanded(child: _chipField(_maxBuyIn, '最大带入', compact)),
              const SizedBox(width: 10),
              Expanded(child: _chipField(_createBuyIn, '我的带入', compact)),
            ],
          ),
          SizedBox(height: compact ? 4 : 10),
          const Row(
            children: [
              Icon(Icons.groups_2_outlined, size: 18, color: Color(0xFFD9B85F)),
              SizedBox(width: 7),
              Expanded(child: Text('无需预设人数，牌桌随朋友加入动态扩展，最多 10 人')),
            ],
          ),
          if (compact)
            Row(
              children: [
                Expanded(
                  child: TextField(
                    controller: _createPassword,
                    obscureText: true,
                    decoration: _fieldDecoration('房间密码（可选）', compact: true),
                  ),
                ),
                const SizedBox(width: 10),
                FilledButton.icon(
                  onPressed: _busy ? null : _create,
                  icon: const Icon(Icons.add),
                  label: const Text('创建'),
                ),
              ],
            )
          else ...[
            TextField(
              controller: _createPassword,
              obscureText: true,
              decoration: _fieldDecoration('房间密码（可选，至少 4 位）', compact: false),
            ),
            const SizedBox(height: 18),
            FilledButton.icon(
              onPressed: _busy ? null : _create,
              icon: const Icon(Icons.add),
              label: const Text('创建牌桌'),
            ),
          ],
        ],
      ),
    ),
  );

  Widget _cardTitle({
    required IconData icon,
    required String title,
    required bool compact,
  }) {
    final iconWidget = Icon(
      icon,
      size: compact ? 28 : 42,
      color: const Color(0xFFD9B85F),
    );
    final titleWidget = Text(
      title,
      style: Theme.of(context).textTheme.titleLarge,
    );
    if (compact) {
      return Row(
        children: [
          iconWidget,
          const SizedBox(width: 10),
          Expanded(child: titleWidget),
        ],
      );
    }
    return Column(
      children: [
        iconWidget,
        const SizedBox(height: 12),
        Align(alignment: Alignment.centerLeft, child: titleWidget),
      ],
    );
  }

  InputDecoration _fieldDecoration(
    String label, {
    required bool compact,
    String? counterText,
  }) => InputDecoration(
    labelText: label,
    counterText: counterText,
    isDense: compact,
    contentPadding: compact
        ? const EdgeInsets.symmetric(horizontal: 12, vertical: 8)
        : null,
    border: const OutlineInputBorder(),
  );

  Future<void> _join() async {
    if (_roomCode.text.trim().length != 6) {
      setState(() => _error = '请输入完整的 6 位房间码');
      return;
    }
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      final preview = await widget.onPreviewRoom(_roomCode.text.trim());
      if (!mounted) return;
      final buyIn = await _showBuyInDialog(preview);
      if (buyIn == null) return;
      await widget.onJoinRoom(_roomCode.text.trim(), _joinPassword.text, buyIn);
    } on GameApiException catch (error) {
      if (mounted) setState(() => _error = _roomError(error.code));
    } on Object {
      if (mounted) setState(() => _error = '无法连接游戏服务，请确认服务端已启动');
    } finally {
      if (mounted) setState(() => _busy = false);
    }
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
    final smallBlind = int.tryParse(_smallBlind.text);
    final bigBlind = int.tryParse(_bigBlind.text);
    final maximum = int.tryParse(_maxBuyIn.text);
    final buyIn = int.tryParse(_createBuyIn.text);
    if (smallBlind == null ||
        bigBlind == null ||
        smallBlind < 10 ||
        bigBlind < 20 ||
        bigBlind <= smallBlind ||
        bigBlind % smallBlind != 0) {
      setState(() => _error = '最低盲注为 10/20，且大盲必须是小盲的整数倍');
      return;
    }
    if (maximum == null ||
        buyIn == null ||
        maximum < bigBlind ||
        buyIn <= 0 ||
        buyIn > maximum) {
      setState(() => _error = '请检查盲注、最大带入和我的带入额度');
      return;
    }
    if (buyIn > widget.bankroll.walletChips) {
      setState(() => _error = '账户筹码不足，请先点击顶部总筹码进行虚拟充值');
      return;
    }
    await _run(
      () => widget.onCreateRoom(
        CreateRoomInput(
          preset: _preset,
          password: password,
          smallBlind: smallBlind,
          bigBlind: bigBlind,
          maxBuyIn: maximum,
          buyIn: buyIn,
        ),
      ),
    );
  }

  Widget _chipField(
    TextEditingController controller,
    String label,
    bool compact,
  ) => PlatformNumberField(
    controller: controller,
    decoration: _fieldDecoration(label, compact: compact),
  );

  void _applyPreset(String value) {
    setState(() {
      _preset = value;
      switch (value) {
        case 'casual':
          _smallBlind.text = '10';
          _bigBlind.text = '20';
          _maxBuyIn.text = '1000';
          _createBuyIn.text = '1000';
        case 'deep':
          _smallBlind.text = '10';
          _bigBlind.text = '20';
          _maxBuyIn.text = '5000';
          _createBuyIn.text = '5000';
        default:
          _smallBlind.text = '10';
          _bigBlind.text = '20';
          _maxBuyIn.text = '2000';
          _createBuyIn.text = '2000';
      }
    });
  }

  Future<void> _showTopUpDialog() async {
    final amount = await showDialog<int>(
      context: context,
      builder: (context) => const _ChipAmountDialog(
        title: '虚拟充值娱乐筹码',
        description: '不接入支付平台，不产生订单或现实货币交易。',
        fieldLabel: '充值筹码数量',
        confirmLabel: '确认充值',
      ),
    );
    if (amount == null) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await widget.onTopUp(amount);
      if (mounted) {
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('充值成功，增加 $amount 筹码')));
      }
    } on GameApiException catch (error) {
      if (mounted) setState(() => _error = _roomError(error.code));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  void _showBankrollEntries() {
    showDialog<void>(
      context: context,
      builder: (context) =>
          _BankrollEntriesDialog(loadEntries: widget.onLoadBankrollEntries),
    );
  }

  Future<int?> _showBuyInDialog(RoomPreview preview) async {
    final suggested = preview.rules.maxBuyIn < widget.bankroll.walletChips
        ? preview.rules.maxBuyIn
        : widget.bankroll.walletChips;
    return showDialog<int>(
      context: context,
      builder: (context) => _ChipAmountDialog(
        title: '选择带入量',
        description:
            '盲注 ${preview.rules.smallBlind}/${preview.rules.bigBlind} · 最大带入 ${preview.rules.maxBuyIn}\n'
            '账户可用 ${widget.bankroll.walletChips} · 房间 ${preview.currentPlayers}/${preview.maxPlayers} 人',
        fieldLabel: '本次带入',
        confirmLabel: '带入并加入',
        initialAmount: suggested,
        maximum: suggested,
      ),
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

class _ChipAmountDialog extends StatefulWidget {
  const _ChipAmountDialog({
    required this.title,
    required this.description,
    required this.fieldLabel,
    required this.confirmLabel,
    this.initialAmount,
    this.maximum,
  });

  final String title;
  final String description;
  final String fieldLabel;
  final String confirmLabel;
  final int? initialAmount;
  final int? maximum;

  @override
  State<_ChipAmountDialog> createState() => _ChipAmountDialogState();
}

class _ChipAmountDialogState extends State<_ChipAmountDialog> {
  late final TextEditingController _controller;
  int _amount = 0;

  bool get _valid =>
      _amount > 0 && (widget.maximum == null || _amount <= widget.maximum!);

  @override
  void initState() {
    super.initState();
    _amount = widget.initialAmount ?? 0;
    _controller = TextEditingController(
      text: widget.initialAmount == null ? '' : '$_amount',
    );
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
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
      title: Text(widget.title),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(widget.description),
          SizedBox(height: keyboardVisible ? 8 : 12),
          PlatformNumberField(
            controller: _controller,
            autofocus: false,
            scrollPadding: const EdgeInsets.only(bottom: 100),
            decoration: InputDecoration(
              labelText: widget.fieldLabel,
              helperText: widget.maximum == null
                  ? null
                  : '本次最多可输入 ${widget.maximum}',
              border: const OutlineInputBorder(),
            ),
            onChanged: (value) =>
                setState(() => _amount = int.tryParse(value) ?? 0),
            onSubmitted: (_) {
              if (_valid) Navigator.of(context).pop(_amount);
            },
          ),
        ],
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('取消'),
        ),
        FilledButton(
          onPressed: _valid ? () => Navigator.of(context).pop(_amount) : null,
          child: Text(widget.confirmLabel),
        ),
      ],
    );
  }
}

class _BankrollEntriesDialog extends StatefulWidget {
  const _BankrollEntriesDialog({required this.loadEntries});

  final Future<List<BankrollEntry>> Function() loadEntries;

  @override
  State<_BankrollEntriesDialog> createState() => _BankrollEntriesDialogState();
}

class _BankrollEntriesDialogState extends State<_BankrollEntriesDialog> {
  late Future<List<BankrollEntry>> _entries;

  @override
  void initState() {
    super.initState();
    _entries = widget.loadEntries();
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Row(
        children: [
          Icon(Icons.receipt_long_outlined),
          SizedBox(width: 8),
          Text('娱乐筹码流水'),
        ],
      ),
      content: SizedBox(
        width: 560,
        height: 420,
        child: FutureBuilder<List<BankrollEntry>>(
          future: _entries,
          builder: (context, snapshot) {
            if (snapshot.connectionState != ConnectionState.done) {
              return const Center(child: CircularProgressIndicator());
            }
            if (snapshot.hasError) {
              return Center(
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    const Text('筹码流水加载失败'),
                    const SizedBox(height: 10),
                    OutlinedButton.icon(
                      onPressed: () =>
                          setState(() => _entries = widget.loadEntries()),
                      icon: const Icon(Icons.refresh),
                      label: const Text('重试'),
                    ),
                  ],
                ),
              );
            }
            final entries = snapshot.data ?? const [];
            if (entries.isEmpty) {
              return const Center(
                child: Text('还没有筹码流水', style: TextStyle(color: Colors.white54)),
              );
            }
            return ListView.separated(
              itemCount: entries.length,
              separatorBuilder: (_, _) => const Divider(height: 1),
              itemBuilder: (context, index) =>
                  _BankrollEntryTile(entry: entries[index]),
            );
          },
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('关闭'),
        ),
      ],
    );
  }
}

class _BankrollEntryTile extends StatelessWidget {
  const _BankrollEntryTile({required this.entry});

  final BankrollEntry entry;

  @override
  Widget build(BuildContext context) {
    return ListTile(
      contentPadding: const EdgeInsets.symmetric(horizontal: 4, vertical: 3),
      leading: CircleAvatar(
        backgroundColor: const Color(0xFF315F51),
        child: Icon(_bankrollReasonIcon(entry.reason), size: 20),
      ),
      title: Text(_bankrollReasonLabel(entry.reason)),
      subtitle: Text(
        '${_formatLocalTime(entry.createdAt)}\n'
        '余额：钱包 ${entry.walletBalanceAfter} · 牌桌 ${entry.tableBalanceAfter}',
      ),
      isThreeLine: true,
      trailing: Column(
        mainAxisAlignment: MainAxisAlignment.center,
        crossAxisAlignment: CrossAxisAlignment.end,
        children: [
          if (entry.walletDelta != 0)
            _DeltaText(label: '钱包', value: entry.walletDelta),
          if (entry.tableDelta != 0)
            _DeltaText(label: '牌桌', value: entry.tableDelta),
          if (entry.walletDelta == 0 && entry.tableDelta == 0)
            const Text('无变化', style: TextStyle(color: Colors.white38)),
        ],
      ),
    );
  }
}

class _DeltaText extends StatelessWidget {
  const _DeltaText({required this.label, required this.value});

  final String label;
  final int value;

  @override
  Widget build(BuildContext context) => Text(
    '$label ${value > 0 ? '+' : ''}$value',
    style: TextStyle(
      color: value > 0 ? const Color(0xFF6DE0A4) : Colors.orangeAccent,
      fontWeight: FontWeight.w700,
      fontSize: 12,
    ),
  );
}

String _bankrollReasonLabel(String reason) => switch (reason) {
  'virtual_top_up' => '虚拟充值',
  'buy_in' => '带入牌桌',
  'rebuy' => '牌桌补码',
  'hand_settlement' => '牌局结算',
  'cash_out' => '离桌返还',
  _ => reason,
};

IconData _bankrollReasonIcon(String reason) => switch (reason) {
  'virtual_top_up' => Icons.add_card,
  'buy_in' => Icons.login,
  'rebuy' => Icons.add_circle_outline,
  'hand_settlement' => Icons.style_outlined,
  'cash_out' => Icons.logout,
  _ => Icons.toll,
};

String _formatLocalTime(DateTime value) {
  final local = value.toLocal();
  String two(int number) => number.toString().padLeft(2, '0');
  return '${local.year}-${two(local.month)}-${two(local.day)} '
      '${two(local.hour)}:${two(local.minute)}';
}

String _roomError(String code) => switch (code) {
  'room_not_found' => '没有找到这个房间',
  'invalid_room_password' => '房间密码不正确',
  'room_full' => '这个房间已经满员',
  'already_in_room' => '你已经在另一个房间中',
  'insufficient_wallet_chips' => '账户筹码不足，请先充值或减少带入',
  'maximum_buy_in_exceeded' => '带入量超过房间上限',
  'invalid_room_rules' => '最低盲注为 10/20，大盲必须是小盲的整数倍，并请检查带入设置',
  'invalid_buy_in' => '带入量必须为正整数且不超过房间上限',
  'invalid_chip_amount' => '请输入有效的正整数筹码数量',
  _ => '操作失败（$code）',
};
