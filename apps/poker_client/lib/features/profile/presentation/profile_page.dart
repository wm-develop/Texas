import 'package:flutter/material.dart';
import 'package:poker_client/core/auth/auth_session.dart';
import 'package:poker_client/core/legal/privacy_notice.dart';
import 'package:poker_client/core/network/game_api_client.dart';

class ProfilePage extends StatefulWidget {
  const ProfilePage({
    required this.session,
    required this.onUpdateUsername,
    required this.onUpdateDisplayName,
    required this.onChangePassword,
    this.onDeleteAccount,
    super.key,
  });

  final AuthSession session;
  final Future<AppUser> Function(String username) onUpdateUsername;
  final Future<AppUser> Function(String displayName) onUpdateDisplayName;
  final Future<AuthSession> Function(String currentPassword, String newPassword)
  onChangePassword;

  /// 自行注销。为 null 时不显示注销入口。成功后由调用方清除登录态。
  final Future<void> Function(String password)? onDeleteAccount;

  @override
  State<ProfilePage> createState() => _ProfilePageState();
}

class _ProfilePageState extends State<ProfilePage> {
  late AppUser _user = widget.session.user;
  bool _busy = false;
  String? _error;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: const Text('个人信息')),
      body: DecoratedBox(
        decoration: const BoxDecoration(
          gradient: RadialGradient(
            colors: [Color(0xFF16473B), Color(0xFF061814)],
            radius: 1.2,
          ),
        ),
        child: Center(
          child: SingleChildScrollView(
            padding: const EdgeInsets.all(16),
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 520),
              child: Card(
                child: Padding(
                  padding: const EdgeInsets.all(20),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      const CircleAvatar(
                        radius: 34,
                        child: Icon(Icons.person_outline, size: 36),
                      ),
                      const SizedBox(height: 18),
                      _line('登录用户名', _user.username),
                      _line('牌桌昵称', _user.displayName),
                      _line('账号角色', _user.isAdmin ? '管理员' : '玩家'),
                      if (_error != null) ...[
                        const SizedBox(height: 10),
                        Text(
                          _error!,
                          style: const TextStyle(color: Colors.redAccent),
                        ),
                      ],
                      const SizedBox(height: 18),
                      FilledButton.icon(
                        onPressed: _busy ? null : _editUsername,
                        icon: const Icon(Icons.edit_outlined),
                        label: const Text('修改登录用户名'),
                      ),
                      const SizedBox(height: 10),
                      OutlinedButton.icon(
                        onPressed: _busy ? null : _editDisplayName,
                        icon: const Icon(Icons.badge_outlined),
                        label: const Text('修改牌桌昵称'),
                      ),
                      const SizedBox(height: 10),
                      OutlinedButton.icon(
                        onPressed: _busy ? null : _changePassword,
                        icon: const Icon(Icons.password_outlined),
                        label: const Text('修改密码'),
                      ),
                      const SizedBox(height: 18),
                      TextButton.icon(
                        onPressed: () => showPrivacyNoticeDialog(context),
                        icon: const Icon(Icons.privacy_tip_outlined),
                        label: const Text('隐私说明'),
                      ),
                      if (widget.onDeleteAccount != null && !_user.isAdmin)
                        TextButton.icon(
                          onPressed: _busy ? null : _deleteAccount,
                          style: TextButton.styleFrom(
                            foregroundColor: Colors.redAccent,
                          ),
                          icon: const Icon(Icons.person_remove_outlined),
                          label: const Text('注销账号'),
                        ),
                    ],
                  ),
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }

  Future<void> _editDisplayName() async {
    var value = _user.displayName;
    final displayName = await showDialog<String>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('修改牌桌昵称'),
        content: TextFormField(
          initialValue: value,
          onChanged: (next) => value = next,
          autofocus: true,
          maxLength: 20,
          decoration: const InputDecoration(
            labelText: '新牌桌昵称',
            helperText: '1～20 个字符，不能包含控制字符',
            border: OutlineInputBorder(),
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, value.trim()),
            child: const Text('保存'),
          ),
        ],
      ),
    );
    if (displayName == null || displayName == _user.displayName) return;
    await _run(() async {
      final updated = await widget.onUpdateDisplayName(displayName);
      if (mounted) setState(() => _user = updated);
      _showMessage('牌桌昵称已修改');
    });
  }

  Widget _line(String label, String value) => Padding(
    padding: const EdgeInsets.symmetric(vertical: 7),
    child: Row(
      children: [
        SizedBox(
          width: 100,
          child: Text(label, style: const TextStyle(color: Colors.white60)),
        ),
        Expanded(
          child: SelectableText(
            value,
            style: const TextStyle(fontSize: 16, fontWeight: FontWeight.w600),
          ),
        ),
      ],
    ),
  );

  Future<void> _editUsername() async {
    var value = _user.username;
    final username = await showDialog<String>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('修改登录用户名'),
        content: TextFormField(
          initialValue: value,
          onChanged: (next) => value = next,
          autofocus: true,
          decoration: const InputDecoration(
            labelText: '新用户名',
            helperText: '3～24 位，只能使用英文字母、数字和下划线',
            border: OutlineInputBorder(),
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(context, value.trim()),
            child: const Text('保存'),
          ),
        ],
      ),
    );
    if (username == null || username == _user.username) return;
    await _run(() async {
      final updated = await widget.onUpdateUsername(username);
      if (mounted) setState(() => _user = updated);
      _showMessage('用户名已修改，下次登录请使用新用户名');
    });
  }

  Future<void> _changePassword() async {
    var current = '';
    var next = '';
    var confirmation = '';
    final values = await showDialog<List<String>>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('修改密码'),
        content: SizedBox(
          width: 420,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              TextFormField(
                onChanged: (value) => current = value,
                obscureText: true,
                autofocus: true,
                decoration: const InputDecoration(labelText: '当前密码'),
              ),
              TextFormField(
                onChanged: (value) => next = value,
                obscureText: true,
                decoration: const InputDecoration(
                  labelText: '新密码',
                  helperText: '8～128 位',
                ),
              ),
              TextFormField(
                onChanged: (value) => confirmation = value,
                obscureText: true,
                decoration: const InputDecoration(labelText: '再次输入新密码'),
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
                Navigator.pop(context, [current, next, confirmation]),
            child: const Text('确认修改'),
          ),
        ],
      ),
    );
    if (values == null) return;
    if (values[1] != values[2]) {
      setState(() => _error = '两次输入的新密码不一致');
      return;
    }
    await _run(() async {
      final session = await widget.onChangePassword(values[0], values[1]);
      if (mounted) setState(() => _user = session.user);
      _showMessage('密码已修改，其他已登录设备需要重新登录');
    });
  }

  Future<void> _deleteAccount() async {
    var password = '';
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('注销账号'),
        content: SizedBox(
          width: 420,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Text(
                '注销后：\n'
                '• 账号不能再登录，原用户名可被重新注册；\n'
                '• 钱包中剩余的娱乐筹码将转入服务器管理员账户，并记录来源；\n'
                '• 牌局历史和账本予以保留，但不再关联你的用户名；\n'
                '• 此操作不可撤销。\n\n'
                '请输入当前密码以确认。',
                style: TextStyle(height: 1.5),
              ),
              const SizedBox(height: 12),
              TextFormField(
                onChanged: (value) => password = value,
                obscureText: true,
                autofocus: true,
                decoration: const InputDecoration(
                  labelText: '当前密码',
                  border: OutlineInputBorder(),
                ),
              ),
            ],
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: Colors.redAccent),
            onPressed: () => Navigator.pop(context, true),
            child: const Text('确认注销'),
          ),
        ],
      ),
    );
    if (confirmed != true) return;
    await _run(() async {
      await widget.onDeleteAccount!(password);
      if (!mounted) return;
      // 登录态已由调用方清除，退回到根路由让应用切换到登录页
      Navigator.of(context).popUntil((route) => route.isFirst);
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
    if (error is GameApiException) {
      return switch (error.code) {
        'username_taken' => '用户名已经存在',
        'invalid_profile' => '用户名或牌桌昵称格式不正确',
        'invalid_current_password' => '当前密码不正确',
        'invalid_password' => '新密码必须为 8～128 位',
        'authentication_required' => '登录状态已失效，请重新登录',
        'user_in_room' => '请先离开房间再注销账号',
        'protected_account' => '管理员账号不能自行注销',
        'admin_unavailable' => '服务器没有可接收筹码的管理员账号，暂时无法注销',
        'rate_limited' => '密码错误次数过多，请稍后再试',
        _ => '修改失败（${error.code}）',
      };
    }
    if (error is GameApiTimeoutException) return '服务器响应超时，请稍后重试';
    return '无法连接游戏服务';
  }
}
