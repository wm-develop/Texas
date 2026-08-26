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
  }

  @override
  void dispose() {
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
              user.displayName.toLowerCase().contains(query),
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
          Text('共 ${_users.length} 个账号 · 已选择 ${_selected.length} 个'),
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
                      _metric(Icons.toll, '总筹码 ${user.walletChips}'),
                      if (user.isInRoom)
                        _metric(Icons.casino_outlined, '牌桌 ${user.tableChips}'),
                    ],
                  ),
                  if (user.isInRoom)
                    const Padding(
                      padding: EdgeInsets.only(top: 4),
                      child: Text(
                        '当前正在牌桌中，离桌前不可停用或删除',
                        style: TextStyle(
                          color: Colors.orangeAccent,
                          fontSize: 12,
                        ),
                      ),
                    ),
                ],
              ),
            ),
            IconButton(
              onPressed: !selectable || _busy
                  ? null
                  : () => _resetPassword(user),
              icon: const Icon(Icons.password),
              tooltip: '重置密码',
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

  Widget _metric(IconData icon, String label) => Row(
    mainAxisSize: MainAxisSize.min,
    children: [Icon(icon, size: 16), const SizedBox(width: 4), Text(label)],
  );

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
    });
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
      if (mounted) setState(() => _error = _message(error));
    } finally {
      if (mounted) setState(() => _loading = false);
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
    final controller = TextEditingController();
    final password = await showDialog<String>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text('重置 ${user.username} 的密码'),
        content: TextField(
          controller: controller,
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
            onPressed: () => Navigator.pop(context, controller.text),
            child: const Text('确认重置'),
          ),
        ],
      ),
    );
    controller.dispose();
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

  Future<void> _showCreateUsersDialog() async {
    final controller = TextEditingController();
    final lines = await showDialog<List<String>>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('新增账号（支持批量）'),
        content: SizedBox(
          width: 520,
          child: TextField(
            controller: controller,
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
              controller.text
                  .split(RegExp(r'[\r\n]+'))
                  .where((line) => line.trim().isNotEmpty)
                  .toList(),
            ),
            child: const Text('创建'),
          ),
        ],
      ),
    );
    controller.dispose();
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
        'admin_required' => '当前账号没有管理员权限',
        _ => '管理操作失败（${error.code}）',
      };
    }
    return '无法连接游戏服务';
  }
}
