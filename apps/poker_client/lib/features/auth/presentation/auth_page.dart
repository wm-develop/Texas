import 'package:flutter/material.dart';
import 'package:poker_client/core/auth/auth_session.dart';
import 'package:poker_client/core/network/game_api_client.dart';

class AuthPage extends StatefulWidget {
  const AuthPage({required this.onLogin, required this.onRegister, super.key});

  final Future<AuthSession> Function(String username, String password) onLogin;
  final Future<AuthSession> Function(
    String username,
    String displayName,
    String password,
    bool requestAdmin,
  )
  onRegister;

  @override
  State<AuthPage> createState() => _AuthPageState();
}

class _AuthPageState extends State<AuthPage> {
  final _formKey = GlobalKey<FormState>();
  final _username = TextEditingController();
  final _displayName = TextEditingController();
  final _password = TextEditingController();
  bool _registering = false;
  bool _submitting = false;
  String? _error;
  int _adminTapCount = 0;
  DateTime? _lastAdminTap;
  bool _requestAdmin = false;

  @override
  void dispose() {
    _username.dispose();
    _displayName.dispose();
    _password.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: DecoratedBox(
        decoration: const BoxDecoration(
          gradient: RadialGradient(
            colors: [Color(0xFF16473B), Color(0xFF061814)],
            radius: 1.2,
          ),
        ),
        child: LayoutBuilder(
          builder: (context, constraints) {
            final compact = constraints.maxHeight < 560;
            final horizontal = compact && constraints.maxWidth >= 640;
            return Center(
              child: SingleChildScrollView(
                padding: EdgeInsets.all(compact ? 12 : 24),
                child: ConstrainedBox(
                  constraints: BoxConstraints(maxWidth: horizontal ? 760 : 430),
                  child: Card(
                    child: Padding(
                      padding: EdgeInsets.all(compact ? 16 : 28),
                      child: AutofillGroup(
                        child: Form(
                          key: _formKey,
                          child: horizontal
                              ? Row(
                                  crossAxisAlignment: CrossAxisAlignment.center,
                                  children: [
                                    SizedBox(
                                      width: 230,
                                      child: _AuthBranding(
                                        compact: true,
                                        onIconTap: _handleAdminTap,
                                      ),
                                    ),
                                    const SizedBox(
                                      height: 240,
                                      child: VerticalDivider(),
                                    ),
                                    const SizedBox(width: 20),
                                    Expanded(child: _buildForm(compact: true)),
                                  ],
                                )
                              : Column(
                                  crossAxisAlignment:
                                      CrossAxisAlignment.stretch,
                                  children: [
                                    _AuthBranding(
                                      compact: compact,
                                      onIconTap: _handleAdminTap,
                                    ),
                                    SizedBox(height: compact ? 12 : 24),
                                    _buildForm(compact: compact),
                                  ],
                                ),
                        ),
                      ),
                    ),
                  ),
                ),
              ),
            );
          },
        ),
      ),
    );
  }

  Widget _buildForm({required bool compact}) {
    final fieldGap = compact ? 8.0 : 14.0;
    InputDecoration decoration(String label, {String? hint}) => InputDecoration(
      labelText: label,
      hintText: hint,
      border: const OutlineInputBorder(),
      isDense: compact,
      contentPadding: EdgeInsets.symmetric(
        horizontal: 12,
        vertical: compact ? 11 : 16,
      ),
    );

    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      mainAxisSize: MainAxisSize.min,
      children: [
        SegmentedButton<bool>(
          segments: const [
            ButtonSegment(value: false, label: Text('登录')),
            ButtonSegment(value: true, label: Text('注册')),
          ],
          selected: {_registering},
          onSelectionChanged: _submitting
              ? null
              : (value) => setState(() {
                  _registering = value.first;
                  _error = null;
                  if (!_registering) {
                    _requestAdmin = false;
                    _adminTapCount = 0;
                  }
                }),
        ),
        SizedBox(height: compact ? 10 : 20),
        TextFormField(
          controller: _username,
          autofillHints: const [AutofillHints.username],
          decoration: decoration(
            '账号',
            hint: compact ? null : '3～24 位字母、数字或下划线',
          ),
          validator: (value) =>
              value == null || value.trim().isEmpty ? '请输入账号' : null,
        ),
        if (_registering) ...[
          SizedBox(height: fieldGap),
          TextFormField(
            controller: _displayName,
            decoration: decoration('牌桌昵称'),
            validator: (value) =>
                value == null || value.trim().isEmpty ? '请输入牌桌昵称' : null,
          ),
        ],
        SizedBox(height: fieldGap),
        TextFormField(
          controller: _password,
          obscureText: true,
          autofillHints: const [AutofillHints.password],
          decoration: decoration('密码'),
          validator: (value) => value == null || value.isEmpty ? '请输入密码' : null,
          onFieldSubmitted: (_) => _submit(),
        ),
        if (_error != null) ...[
          SizedBox(height: compact ? 8 : 12),
          Text(_error!, style: const TextStyle(color: Colors.redAccent)),
        ],
        SizedBox(height: compact ? 10 : 20),
        FilledButton(
          onPressed: _submitting ? null : _submit,
          child: Padding(
            padding: EdgeInsets.symmetric(vertical: compact ? 7 : 11),
            child: Text(_submitting ? '请稍候…' : (_registering ? '注册并进入' : '登录')),
          ),
        ),
      ],
    );
  }

  Future<void> _submit() async {
    if (_submitting || !(_formKey.currentState?.validate() ?? false)) return;
    setState(() {
      _submitting = true;
      _error = null;
    });
    try {
      if (_registering) {
        await widget.onRegister(
          _username.text.trim(),
          _displayName.text.trim(),
          _password.text,
          _requestAdmin,
        );
      } else {
        await widget.onLogin(_username.text.trim(), _password.text);
      }
    } on GameApiTimeoutException {
      if (mounted) {
        setState(() => _error = '网络响应较慢，请稍后重试；注册结果会自动核验');
      }
    } on GameApiException catch (error) {
      if (mounted) {
        final message = _messageFor(error.code);
        setState(() => _error = message);
        if (error.code == 'registration_disabled') {
          ScaffoldMessenger.of(context)
            ..hideCurrentSnackBar()
            ..showSnackBar(SnackBar(content: Text(message)));
        }
      }
    } on Object {
      if (mounted) setState(() => _error = '无法连接游戏服务，请确认服务端已启动');
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }

  void _handleAdminTap() {
    if (!_registering || _requestAdmin) return;
    final now = DateTime.now();
    if (_lastAdminTap == null || now.difference(_lastAdminTap!).inSeconds > 2) {
      _adminTapCount = 0;
    }
    _lastAdminTap = now;
    _adminTapCount++;
    if (_adminTapCount < 10) return;
    setState(() {
      _requestAdmin = true;
      _adminTapCount = 0;
    });
    ScaffoldMessenger.of(context)
      ..hideCurrentSnackBar()
      ..showSnackBar(const SnackBar(content: Text('管理员注册模式已开启，仅服务器首位管理员有效')));
  }
}

class _AuthBranding extends StatelessWidget {
  const _AuthBranding({required this.compact, required this.onIconTap});

  final bool compact;
  final VoidCallback onIconTap;

  @override
  Widget build(BuildContext context) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        GestureDetector(
          behavior: HitTestBehavior.opaque,
          onTap: onIconTap,
          child: Padding(
            padding: const EdgeInsets.all(4),
            child: Icon(
              Icons.style,
              size: compact ? 36 : 44,
              color: const Color(0xFFD9B85F),
            ),
          ),
        ),
        SizedBox(height: compact ? 6 : 12),
        Text(
          '好友德州',
          textAlign: TextAlign.center,
          style: compact
              ? Theme.of(context).textTheme.headlineSmall
              : Theme.of(context).textTheme.headlineMedium,
        ),
        const SizedBox(height: 4),
        const Text(
          '只和认识的朋友，快速组织一桌牌局',
          textAlign: TextAlign.center,
          style: TextStyle(color: Colors.white60),
        ),
      ],
    );
  }
}

String _messageFor(String code) => switch (code) {
  'invalid_credentials' => '账号或密码不正确',
  'username_taken' => '这个账号已被使用',
  'invalid_profile' => '账号或昵称格式不符合要求',
  'invalid_password' => '密码不符合要求',
  'registration_disabled' => '服务器当前已关闭新用户注册',
  'admin_already_initialized' => '服务器已经创建管理员，请使用普通注册或联系管理员',
  _ => '操作失败（$code）',
};
