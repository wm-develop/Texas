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
        child: Center(
          child: SingleChildScrollView(
            padding: const EdgeInsets.all(24),
            child: ConstrainedBox(
              constraints: const BoxConstraints(maxWidth: 430),
              child: Card(
                child: Padding(
                  padding: const EdgeInsets.all(28),
                  child: AutofillGroup(
                    child: Form(
                      key: _formKey,
                      child: Column(
                        crossAxisAlignment: CrossAxisAlignment.stretch,
                        children: [
                          const Icon(
                            Icons.style,
                            size: 44,
                            color: Color(0xFFD9B85F),
                          ),
                          const SizedBox(height: 12),
                          Text(
                            '好友德州',
                            textAlign: TextAlign.center,
                            style: Theme.of(context).textTheme.headlineMedium,
                          ),
                          const SizedBox(height: 4),
                          const Text(
                            '只和认识的朋友，快速组织一桌牌局',
                            textAlign: TextAlign.center,
                            style: TextStyle(color: Colors.white60),
                          ),
                          const SizedBox(height: 24),
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
                                  }),
                          ),
                          const SizedBox(height: 20),
                          TextFormField(
                            controller: _username,
                            autofillHints: const [AutofillHints.username],
                            decoration: const InputDecoration(
                              labelText: '账号',
                              hintText: '3～24 位字母、数字或下划线',
                              border: OutlineInputBorder(),
                            ),
                            validator: (value) =>
                                value == null || value.trim().isEmpty
                                ? '请输入账号'
                                : null,
                          ),
                          if (_registering) ...[
                            const SizedBox(height: 14),
                            TextFormField(
                              controller: _displayName,
                              decoration: const InputDecoration(
                                labelText: '牌桌昵称',
                                border: OutlineInputBorder(),
                              ),
                              validator: (value) =>
                                  value == null || value.trim().isEmpty
                                  ? '请输入牌桌昵称'
                                  : null,
                            ),
                          ],
                          const SizedBox(height: 14),
                          TextFormField(
                            controller: _password,
                            obscureText: true,
                            autofillHints: const [AutofillHints.password],
                            decoration: const InputDecoration(
                              labelText: '密码',
                              border: OutlineInputBorder(),
                            ),
                            validator: (value) =>
                                value == null || value.isEmpty ? '请输入密码' : null,
                            onFieldSubmitted: (_) => _submit(),
                          ),
                          if (_error != null) ...[
                            const SizedBox(height: 12),
                            Text(
                              _error!,
                              style: const TextStyle(color: Colors.redAccent),
                            ),
                          ],
                          const SizedBox(height: 20),
                          FilledButton(
                            onPressed: _submitting ? null : _submit,
                            child: Padding(
                              padding: const EdgeInsets.symmetric(vertical: 11),
                              child: Text(
                                _submitting
                                    ? '请稍候…'
                                    : (_registering ? '注册并进入' : '登录'),
                              ),
                            ),
                          ),
                        ],
                      ),
                    ),
                  ),
                ),
              ),
            ),
          ),
        ),
      ),
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
        );
      } else {
        await widget.onLogin(_username.text.trim(), _password.text);
      }
    } on GameApiTimeoutException {
      if (mounted) {
        setState(() => _error = '网络响应较慢，请稍后重试；注册结果会自动核验');
      }
    } on GameApiException catch (error) {
      if (mounted) setState(() => _error = _messageFor(error.code));
    } on Object {
      if (mounted) setState(() => _error = '无法连接游戏服务，请确认服务端已启动');
    } finally {
      if (mounted) setState(() => _submitting = false);
    }
  }
}

String _messageFor(String code) => switch (code) {
  'invalid_credentials' => '账号或密码不正确',
  'username_taken' => '这个账号已被使用',
  'invalid_profile' => '账号或昵称格式不符合要求',
  'invalid_password' => '密码不符合要求',
  _ => '操作失败（$code）',
};
