import 'dart:async';

import 'package:flutter/material.dart';
import 'package:poker_client/core/auth/auth_session.dart';
import 'package:poker_client/core/network/game_api_client.dart';
import 'package:poker_client/features/admin/domain/managed_user.dart';

class AdminPage extends StatefulWidget {
  const AdminPage({required this.session, super.key});

  final AuthSession session;

  @override
  State<AdminPage> createState() => _AdminPageState();
}

class _AdminPageState extends State<AdminPage> {
  late final GameApiClient _api;
  final _search = TextEditingController();
  final Set<String> _selected = {};
  Timer? _refreshTimer;
  List<ManagedUser> _users = const [];
  bool _registrationEnabled = true;
  bool _loading = true;
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _api = GameApiClient();
    _load();
    _refreshTimer = Timer.periodic(const Duration(seconds: 15), (_) {
      if (!_busy) unawaited(_load(silent: true));
    });
  }

  @override
  void dispose() {
    _refreshTimer?.cancel();
    _api.close();
    _search.dispose();
    super.dispose();
  }

  List<ManagedUser> get _filteredUsers {
    final query = _search.text.trim().toLowerCase();
    if (query.isEmpty) return _users;
    return _users
        .where(
          (user) =>
              user.username.toLowerCase().contains(query) ||
              user.displayName.toLowerCase().contains(query) ||
              user.roomCode.contains(query),
        )
        .toList(growable: false);
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('服务器管理'),
        actions: [
          IconButton(
            onPressed: _busy ? null : _load,
            icon: const Icon(Icons.refresh),
            tooltip: '刷新',
          ),
          const SizedBox(width: 8),
        ],
      ),
      floatingActionButton: FloatingActionButton.extended(
        onPressed: _busy ? null : _showCreateUsersDialog,
        icon: const Icon(Icons.person_add_alt_1),
        label: const Text('新增账号'),
      ),
      body: DecoratedBox(
        decoration: const BoxDecoration(
          gradient: RadialGradient(
            colors: [Color(0xFF16473B), Color(0xFF061814)],
            radius: 1.2,
          ),
        ),
        child: SafeArea(
          child: Column(
            children: [
              _buildToolbar(),
              if (_error != null)
                Padding(
                  padding: const EdgeInsets.fromLTRB(16, 0, 16, 8),
                  child: Text(
                    _error!,
                    style: const TextStyle(color: Colors.redAccent),
                  ),
                ),
              Expanded(
                child: _loading
                    ? const Center(child: CircularProgressIndicator())
                    : _buildUsers(),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Widget _buildToolbar() {
    return Padding(
      padding: const EdgeInsets.all(16),
      child: Wrap(
        spacing: 12,
        runSpacing: 10,
        crossAxisAlignment: WrapCrossAlignment.center,
        children: [
          SizedBox(
            width: 260,
            child: TextField(
              controller: _search,
              onChanged: (_) => setState(() {}),
              decoration: const InputDecoration(
                isDense: true,
                prefixIcon: Icon(Icons.search),
                labelText: '搜索用户名或昵称',
                border: OutlineInputBorder(),
              ),
            ),
          ),
          FilterChip(
            selected: _registrationEnabled,
            avatar: Icon(
              _registrationEnabled ? Icons.how_to_reg : Icons.person_off,
            ),
            label: Text(_registrationEnabled ? '允许新用户注册' : '已关闭新用户注册'),
            onSelected: _busy ? null : _setRegistrationEnabled,
          ),
          Text(
            '共 ${_users.length} 个账号 · 在线 ${_users.where((user) => user.online).length} 个'
            ' · 已选择 ${_selected.length} 个',
          ),
          if (_selected.isNotEmpty) ...[
            OutlinedButton.icon(
              onPressed: _busy ? null : () => _changeStatus('active'),
              icon: const Icon(Icons.check_circle_outline),
              label: const Text('批量恢复'),
            ),
            OutlinedButton.icon(
              onPressed: _busy ? null : () => _changeStatus('suspended'),
              icon: const Icon(Icons.pause_circle_outline),
              label: const Text('批量停用'),
            ),
            FilledButton.tonalIcon(
              onPressed: _busy ? null : _confirmDeleteSelected,
              icon: const Icon(Icons.delete_outline),
              label: const Text('批量删除'),
            ),
          ],
        ],
      ),
    );
  }

  Widget _buildUsers() {
    final users = _filteredUsers;
    if (users.isEmpty) {
      return const Center(child: Text('没有符合条件的账号'));
    }
    return LayoutBuilder(
      builder: (context, constraints) {
        final columns = constraints.maxWidth >= 1100
            ? 3
            : constraints.maxWidth >= 700
            ? 2
            : 1;
        final width =
            (constraints.maxWidth - 32 - (columns - 1) * 12) / columns;
        return SingleChildScrollView(
          padding: const EdgeInsets.fromLTRB(16, 0, 16, 96),
          child: Wrap(
            spacing: 12,
            runSpacing: 12,
            children: [
              for (final user in users)
                SizedBox(width: width, child: _buildUserCard(user)),
            ],
          ),
        );
      },
    );
  }

  Widget _buildUserCard(ManagedUser user) {
    final selectable = !user.isAdmin;
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(14),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Checkbox(
              value: _selected.contains(user.userId),
              onChanged: !selectable || _busy
                  ? null
                  : (value) => setState(() {
                      if (value ?? false) {
                        _selected.add(user.userId);
                      } else {
                        _selected.remove(user.userId);
                      }
                    }),
            ),
            CircleAvatar(
              child: Icon(
                user.isAdmin ? Icons.admin_panel_settings : Icons.person,
              ),
            ),
            const SizedBox(width: 12),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Expanded(
                        child: Text(
                          user.username,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: const TextStyle(fontWeight: FontWeight.w700),
                        ),
                      ),
                      const SizedBox(width: 6),
                      _onlineChip(user.online),
                      const SizedBox(width: 6),
                      _statusChip(user),
                    ],
                  ),
                  Text(
                    user.displayName,
                    style: const TextStyle(color: Colors.white70),
                  ),
                  const SizedBox(height: 8),
                  Wrap(
                    spacing: 12,
                    runSpacing: 4,
                    children: [
                      _metric(
                        Icons.toll,
                        '总筹码 ${user.walletChips + user.tableChips}',
                      ),
                      _metric(
                        Icons.account_balance_wallet_outlined,
                        '钱包 ${user.walletChips}',
                      ),
                      if (user.isInRoom)
                        _metric(Icons.casino_outlined, '牌桌 ${user.tableChips}'),
                      if (user.isInRoom)
                        _metric(
                          Icons.meeting_room_outlined,
                          '房间 ${user.roomCode}',
                        ),
                      if (user.chatMuted)
                        _metric(Icons.comments_disabled_outlined, '文字已禁言'),
                    ],
                  ),
                  if (user.isInRoom)
                    const Padding(
                      padding: EdgeInsets.only(top: 4),
                      child: Text(
                        '当前正在牌桌中，离桌前不可修改筹码、停用或删除',
                        style: TextStyle(
                          color: Colors.orangeAccent,
                          fontSize: 12,
                        ),
                      ),
                    ),
                ],
              ),
            ),
            PopupMenuButton<String>(
              enabled: !_busy,
              tooltip: '账号操作',
              onSelected: (value) => switch (value) {
                'edit' => _editUser(user),
                'password' => _resetPassword(user),
                'chat-mute' => _changeChatMute(user),
                'leave' => _removeFromRoom(user),
                _ => null,
              },
              itemBuilder: (context) => [
                const PopupMenuItem(
                  value: 'edit',
                  child: ListTile(
                    leading: Icon(Icons.edit_outlined),
                    title: Text('修改用户名和筹码'),
                  ),
                ),
                if (selectable)
                  const PopupMenuItem(
                    value: 'password',
                    child: ListTile(
                      leading: Icon(Icons.password),
                      title: Text('重置密码'),
                    ),
                  ),
                if (selectable)
                  PopupMenuItem(
                    value: 'chat-mute',
                    child: ListTile(
                      leading: Icon(
                        user.chatMuted
                            ? Icons.mark_chat_read_outlined
                            : Icons.comments_disabled_outlined,
                      ),
                      title: Text(user.chatMuted ? '解除文字禁言' : '禁止发送文字消息'),
                    ),
                  ),
                if (user.isInRoom)
                  const PopupMenuItem(
                    value: 'leave',
                    child: ListTile(
                      leading: Icon(Icons.person_remove_outlined),
                      title: Text('请出当前房间'),
                    ),
                  ),
              ],
            ),
          ],
        ),
      ),
    );
  }

  Widget _statusChip(ManagedUser user) {
    final (label, color) = switch (user.status) {
      'active' => ('正常', Colors.greenAccent),
      'suspended' => ('已停用', Colors.orangeAccent),
      _ => ('已删除', Colors.redAccent),
    };
    return Text(label, style: TextStyle(color: color, fontSize: 12));
  }

  Widget _onlineChip(bool online) => Row(
    mainAxisSize: MainAxisSize.min,
    children: [
      Icon(
        Icons.circle,
        size: 9,
        color: online ? Colors.greenAccent : Colors.white38,
      ),
      const SizedBox(width: 3),
      Text(
        online ? '在线' : '离线',
        style: TextStyle(
          color: online ? Colors.greenAccent : Colors.white54,
          fontSize: 12,
        ),
      ),
    ],
  );

  Widget _metric(IconData icon, String label) => Row(
    mainAxisSize: MainAxisSize.min,
    children: [Icon(icon, size: 16), const SizedBox(width: 4), Text(label)],
  );

  Future<void> _load({bool silent = false}) async {
    if (!silent) {
      setState(() {
        _loading = true;
        _error = null;
      });
    }
    try {
      final results = await Future.wait([
        _api.adminUsers(widget.session.accessToken),
        _api.adminRegistrationEnabled(widget.session.accessToken),
      ]);
      if (!mounted) return;
      setState(() {
        _users = results[0] as List<ManagedUser>;
        _registrationEnabled = results[1] as bool;
        _selected.removeWhere(
          (id) => !_users.any((user) => user.userId == id && !user.isAdmin),
        );
      });
    } on Object catch (error) {
      if (!silent && mounted) setState(() => _error = _message(error));
    } finally {
      if (!silent && mounted) setState(() => _loading = false);
    }
  }

  Future<void> _setRegistrationEnabled(bool enabled) async {
    await _run(() async {
      final value = await _api.adminSetRegistrationEnabled(
        accessToken: widget.session.accessToken,
        enabled: enabled,
      );
      if (mounted) setState(() => _registrationEnabled = value);
    });
  }

  Future<void> _changeStatus(String status) async {
    final ids = _selected.toList(growable: false);
    await _run(() async {
      await _api.adminSetUserStatus(
        accessToken: widget.session.accessToken,
        userIds: ids,
        status: status,
      );
      _selected.clear();
      await _load();
    });
  }

  Future<void> _confirmDeleteSelected() async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('删除所选账号？'),
        content: const Text('账号将无法登录，但历史牌局、筹码流水和审计记录会保留。'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('删除'),
          ),
        ],
      ),
    );
    if (confirmed ?? false) await _changeStatus('deleted');
  }

  Future<void> _resetPassword(ManagedUser user) async {
    var value = '';
    final password = await showDialog<String>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text('重置 ${user.username} 的密码'),
        content: TextFormField(
          onChanged: (next) => value = next,
          obscureText: true,
          autofocus: true,
          decoration: const InputDecoration(
            labelText: '新密码',
            helperText: '8～128 位；重置后该账号的现有会话会失效',
            border: OutlineInputBorder(),
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, value),
            child: const Text('确认重置'),
          ),
        ],
      ),
    );
    if (password == null) return;
    await _run(() async {
      await _api.adminResetPassword(
        accessToken: widget.session.accessToken,
        userId: user.userId,
        password: password,
      );
      _showMessage('密码已重置，该账号需要重新登录');
    });
  }

  Future<void> _changeChatMute(ManagedUser user) async {
    final nextMuted = !user.chatMuted;
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(
          nextMuted ? '禁言 ${user.username}？' : '解除 ${user.username} 的禁言？',
        ),
        content: Text(
          nextMuted
              ? '禁言后，该账号仍可正常进行牌局和使用语音，但不能发送牌桌文字、快捷语或表情。'
              : '解除后，该账号可以立即恢复发送牌桌文字、快捷语和表情。',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: Text(nextMuted ? '确认禁言' : '确认解除'),
          ),
        ],
      ),
    );
    if (!(confirmed ?? false)) return;
    await _run(() async {
      await _api.adminSetChatMuted(
        accessToken: widget.session.accessToken,
        userId: user.userId,
        muted: nextMuted,
      );
      await _load();
      _showMessage(nextMuted ? '该账号已被禁止发送牌桌文字消息' : '该账号已解除文字禁言');
    });
  }

  Future<void> _editUser(ManagedUser user) async {
    var username = user.username;
    var chips = user.walletChips.toString();
    final values = await showDialog<List<String>>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text('修改 ${user.username}'),
        content: SizedBox(
          width: 440,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              TextFormField(
                initialValue: username,
                onChanged: (value) => username = value,
                enabled: !user.isAdmin,
                autofocus: true,
                decoration: InputDecoration(
                  labelText: '登录用户名',
                  helperText: user.isAdmin
                      ? '当前管理员请从大厅右上角的个人信息中修改用户名'
                      : '3～24 位，只能使用英文字母、数字和下划线',
                  border: const OutlineInputBorder(),
                ),
              ),
              const SizedBox(height: 14),
              TextFormField(
                initialValue: chips,
                onChanged: (value) => chips = value,
                enabled: !user.isInRoom,
                keyboardType: TextInputType.number,
                decoration: InputDecoration(
                  labelText: '钱包筹码量',
                  helperText: user.isInRoom
                      ? '玩家仍在房间 ${user.roomCode}，请先将其请出牌桌'
                      : '填写调整后的准确数量，可以增加或减少',
                  border: const OutlineInputBorder(),
                ),
              ),
            ],
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () =>
                Navigator.pop(context, [username.trim(), chips.trim()]),
            child: const Text('保存'),
          ),
        ],
      ),
    );
    if (values == null) return;
    final walletChips = int.tryParse(values[1]);
    if (!user.isInRoom && (walletChips == null || walletChips < 0)) {
      setState(() => _error = '筹码量必须是大于或等于 0 的整数');
      return;
    }
    await _run(() async {
      if (!user.isAdmin && values[0] != user.username) {
        await _api.adminUpdateUsername(
          accessToken: widget.session.accessToken,
          userId: user.userId,
          username: values[0],
        );
      }
      if (!user.isInRoom && walletChips != user.walletChips) {
        await _api.adminSetWallet(
          accessToken: widget.session.accessToken,
          userId: user.userId,
          chips: walletChips!,
          requestId: 'admin-wallet-${DateTime.now().microsecondsSinceEpoch}',
        );
      }
      await _load();
      _showMessage('账号资料已更新');
    });
  }

  Future<void> _removeFromRoom(ManagedUser user) async {
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text('将 ${user.username} 请出房间 ${user.roomCode}？'),
        content: const Text(
          '玩家的牌桌筹码会返还钱包。如果该玩家是房主，房主身份会顺延给最早加入的其他玩家。'
          '正在进行的一手牌必须先正常结算。',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, true),
            child: const Text('确认请出'),
          ),
        ],
      ),
    );
    if (!(confirmed ?? false)) return;
    await _run(() async {
      final closed = await _api.adminLeaveRoom(
        accessToken: widget.session.accessToken,
        userId: user.userId,
      );
      await _load();
      _showMessage(closed ? '玩家已被请出，房间已无成员并关闭' : '玩家已被请出房间');
    });
  }

  Future<void> _showCreateUsersDialog() async {
    var value = '';
    final lines = await showDialog<List<String>>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('新增账号（支持批量）'),
        content: SizedBox(
          width: 520,
          child: TextFormField(
            onChanged: (next) => value = next,
            minLines: 4,
            maxLines: 10,
            autofocus: true,
            decoration: const InputDecoration(
              labelText: '每行一个账号',
              hintText: '用户名,牌桌昵称,初始密码\nplayer02,好友二,password-123',
              helperText: '使用英文逗号或 Tab 分隔；密码至少 8 位',
              border: OutlineInputBorder(),
            ),
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(
              context,
              value
                  .split(RegExp(r'[\r\n]+'))
                  .where((line) => line.trim().isNotEmpty)
                  .toList(),
            ),
            child: const Text('创建'),
          ),
        ],
      ),
    );
    if (lines == null || lines.isEmpty) return;
    await _run(() async {
      var created = 0;
      for (final line in lines) {
        final fields = line.split(RegExp(r'[,\t]'));
        if (fields.length != 3) {
          throw const FormatException('每行必须包含用户名、昵称和密码三项');
        }
        await _api.adminCreateUser(
          accessToken: widget.session.accessToken,
          username: fields[0].trim(),
          displayName: fields[1].trim(),
          password: fields[2].trim(),
        );
        created++;
      }
      await _load();
      _showMessage('已创建 $created 个账号');
    });
  }

  Future<void> _run(Future<void> Function() operation) async {
    if (_busy) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await operation();
    } on Object catch (error) {
      if (mounted) setState(() => _error = _message(error));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  void _showMessage(String message) {
    if (!mounted) return;
    ScaffoldMessenger.of(context)
      ..hideCurrentSnackBar()
      ..showSnackBar(SnackBar(content: Text(message)));
  }

  String _message(Object error) {
    if (error is FormatException) return error.message;
    if (error is GameApiTimeoutException) return '服务器响应超时，请刷新后确认操作结果';
    if (error is GameApiException) {
      return switch (error.code) {
        'username_taken' => '用户名已经存在',
        'invalid_profile' => '用户名或昵称格式不正确',
        'invalid_password' => '密码必须为 8～128 位',
        'protected_account' => '不能修改管理员账号或当前登录账号',
        'user_in_room' => '所选账号仍在牌桌中，请先让该玩家离桌',
        'hand_in_progress' => '本手牌正在进行，请在结算后再次执行请出操作',
        'room_not_found' => '玩家当前不在任何房间中，请刷新列表',
        'invalid_chip_amount' => '筹码量不正确',
        'admin_required' => '当前账号没有管理员权限',
        _ => '管理操作失败（${error.code}）',
      };
    }
    return '无法连接游戏服务';
  }
}
