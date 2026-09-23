import 'package:flutter/material.dart';
import 'package:poker_client/core/network/game_api_client.dart';
import 'package:poker_client/core/widgets/platform_number_field.dart';
import 'package:poker_client/features/admin/domain/managed_user.dart';
import 'package:poker_client/features/history/domain/hand_review.dart';

/// 管理员的 AI 复盘管理：总开关、额度、用量，以及按账号开通。
///
/// 复盘只对名单里的账号开放，玩家只能复盘自己打过的手。大模型的地址、型号与
/// 密钥在服务端环境变量里配置，这里只显示有没有配置好。
class AdminReviewPage extends StatefulWidget {
  const AdminReviewPage({
    required this.loadOverview,
    required this.loadUsers,
    required this.saveSettings,
    required this.setAccess,
    super.key,
  });

  final Future<ReviewOverview> Function() loadOverview;
  final Future<List<ManagedUser>> Function() loadUsers;
  final Future<ReviewSettings> Function(ReviewSettings settings) saveSettings;
  final Future<void> Function(String userId, bool granted) setAccess;

  @override
  State<AdminReviewPage> createState() => _AdminReviewPageState();
}

class _AdminReviewPageState extends State<AdminReviewPage> {
  final _dailyLimit = TextEditingController();
  final _tokenBudget = TextEditingController();
  final _search = TextEditingController();
  // null 表示还没成功取到过，不能把「没取到」显示成「没有」。
  ReviewOverview? _overview;
  List<ManagedUser>? _users;
  bool _enabled = true;
  bool _loading = true;
  bool _busy = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _dailyLimit.dispose();
    _tokenBudget.dispose();
    _search.dispose();
    super.dispose();
  }

  Future<void> _load({bool resetForm = true}) async {
    setState(() {
      _loading = true;
      _error = null;
    });
    String? error;
    try {
      final overview = await widget.loadOverview();
      if (mounted) {
        setState(() {
          _overview = overview;
          if (resetForm) _fillForm(overview.settings);
        });
      }
    } on Object catch (failure) {
      error = '读取复盘设置失败：${_message(failure)}';
      // 开通或收回之后刷新失败时，旧名单已经不对了，不能继续按它显示开关
      if (mounted) setState(() => _overview = null);
    }
    try {
      final users = await widget.loadUsers();
      if (mounted) setState(() => _users = users);
    } on Object catch (failure) {
      final text = '读取账号列表失败：${_message(failure)}';
      error = error == null ? text : '$error\n$text';
    }
    if (!mounted) return;
    setState(() {
      _error = error;
      _loading = false;
    });
  }

  void _fillForm(ReviewSettings settings) {
    _enabled = settings.enabled;
    _dailyLimit.text = '${settings.dailyLimitPerUser}';
    _tokenBudget.text = '${settings.monthlyTokenBudget}';
  }

  Future<void> _saveSettings() async {
    final daily = int.tryParse(_dailyLimit.text.trim());
    final budget = int.tryParse(_tokenBudget.text.trim());
    if (daily == null || budget == null || daily < 0 || budget < 0) {
      setState(() => _error = '额度要填不小于 0 的整数，0 表示不限');
      return;
    }
    await _run(() async {
      await widget.saveSettings(
        ReviewSettings(
          enabled: _enabled,
          dailyLimitPerUser: daily,
          monthlyTokenBudget: budget,
        ),
      );
      _showMessage('复盘设置已保存');
      await _load();
    });
  }

  Future<void> _toggleAccess(ManagedUser user, bool granted) async {
    await _run(() async {
      await widget.setAccess(user.userId, granted);
      _showMessage(
        granted
            ? '已为 ${user.displayName} 开通 AI 复盘'
            : '已收回 ${user.displayName} 的 AI 复盘',
      );
      // 只刷新名单与用量，不覆盖管理员正在改、还没保存的设置
      await _load(resetForm: false);
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

  void _showMessage(String text) {
    if (!mounted) return;
    ScaffoldMessenger.of(context)
      ..hideCurrentSnackBar()
      ..showSnackBar(SnackBar(content: Text(text)));
  }

  String _message(Object error) {
    if (error is GameApiTimeoutException) return '服务器响应超时，请刷新后确认操作结果';
    if (error is GameApiException) {
      return switch (error.code) {
        'invalid_review_settings' => reviewErrorLabel(error.code),
        'user_not_found' => '找不到这个账号',
        'admin_required' || 'forbidden' => '需要管理员权限',
        'service_unavailable' => '服务端未启用这项功能',
        'internal_error' => '服务端出错，请稍后重试',
        'rate_limited' => '操作太频繁，请稍后再试',
        _ => '操作失败（${error.code}）',
      };
    }
    return '无法连接游戏服务';
  }

  List<ManagedUser> get _players {
    final query = _search.text.trim().toLowerCase();
    return (_users ?? const <ManagedUser>[])
        .where((user) => !user.isAdmin)
        .where(
          (user) =>
              query.isEmpty ||
              user.username.toLowerCase().contains(query) ||
              user.displayName.toLowerCase().contains(query),
        )
        .toList(growable: false);
  }

  @override
  Widget build(BuildContext context) {
    final overview = _overview;
    final users = _users;
    final players = _players;
    return Scaffold(
      appBar: AppBar(
        title: const Text('AI 复盘管理'),
        actions: [
          IconButton(
            onPressed: _loading || _busy ? null : _load,
            icon: const Icon(Icons.refresh),
            tooltip: '刷新',
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
        child: SafeArea(
          child: _loading && overview == null && users == null
              ? const Center(child: CircularProgressIndicator())
              : Center(
                  child: ConstrainedBox(
                    constraints: const BoxConstraints(maxWidth: 720),
                    child: ListView(
                      padding: const EdgeInsets.all(16),
                      children: [
                        if (_error != null)
                          Padding(
                            padding: const EdgeInsets.only(bottom: 12),
                            child: Text(
                              _error!,
                              style: const TextStyle(color: Colors.redAccent),
                            ),
                          ),
                        if (overview != null) ...[
                          _modelStatus(overview),
                          const SizedBox(height: 16),
                          _sectionTitle('设置'),
                          SwitchListTile(
                            key: const ValueKey('review-enabled'),
                            contentPadding: EdgeInsets.zero,
                            title: const Text('开放 AI 复盘'),
                            subtitle: const Text(
                              '关掉后所有人都不能发起或查看复盘；已有结果会保留，重新开放后仍可查看',
                            ),
                            value: _enabled,
                            onChanged: _busy
                                ? null
                                : (value) => setState(() => _enabled = value),
                          ),
                          const SizedBox(height: 8),
                          Row(
                            children: [
                              Expanded(
                                child: PlatformNumberField(
                                  key: const ValueKey('review-daily-limit'),
                                  controller: _dailyLimit,
                                  decoration: const InputDecoration(
                                    labelText: '每人 24 小时内次数',
                                    helperText: '0 表示不限',
                                    border: OutlineInputBorder(),
                                    isDense: true,
                                  ),
                                ),
                              ),
                              const SizedBox(width: 12),
                              Expanded(
                                child: PlatformNumberField(
                                  key: const ValueKey('review-token-budget'),
                                  controller: _tokenBudget,
                                  decoration: const InputDecoration(
                                    labelText: '最近 30 天 token 总额',
                                    helperText: '0 表示不限',
                                    border: OutlineInputBorder(),
                                    isDense: true,
                                  ),
                                ),
                              ),
                            ],
                          ),
                          const SizedBox(height: 12),
                          Align(
                            alignment: Alignment.centerRight,
                            child: FilledButton(
                              key: const ValueKey('review-save-settings'),
                              onPressed: _busy ? null : _saveSettings,
                              child: const Text('保存设置'),
                            ),
                          ),
                          const SizedBox(height: 16),
                          _sectionTitle('用量'),
                          Text(
                            '最近 24 小时 ${overview.requests24h} 次 · '
                            '最近 30 天 ${overview.requests30d} 次 · '
                            '${_formatTokens(overview.tokens30d)} token',
                            key: const ValueKey('review-usage'),
                          ),
                          const SizedBox(height: 20),
                        ],
                        _sectionTitle('开通名单'),
                        const Text(
                          '开通后，玩家可以在牌局回放里为自己打过的手发起 AI 复盘。',
                          style: TextStyle(color: Colors.white60, fontSize: 12),
                        ),
                        const SizedBox(height: 8),
                        TextField(
                          controller: _search,
                          onChanged: (_) => setState(() {}),
                          decoration: const InputDecoration(
                            prefixIcon: Icon(Icons.search),
                            hintText: '搜索账号或昵称',
                            border: OutlineInputBorder(),
                            isDense: true,
                          ),
                        ),
                        const SizedBox(height: 8),
                        // 名单取不到时不列开关：列出来就只能显示成「关」，
                        // 那是在把「没取到」说成「没开通」
                        if (overview == null)
                          const Padding(
                            key: ValueKey('review-access-unavailable'),
                            padding: EdgeInsets.symmetric(vertical: 16),
                            child: Text(
                              '开通名单没有读取成功，请点右上角刷新',
                              style: TextStyle(color: Colors.white54),
                            ),
                          )
                        else if (users != null && players.isEmpty)
                          const Padding(
                            padding: EdgeInsets.symmetric(vertical: 16),
                            child: Text(
                              '没有符合条件的玩家账号',
                              style: TextStyle(color: Colors.white54),
                            ),
                          ),
                        if (overview != null)
                          for (final user in players)
                            Card(
                              child: SwitchListTile(
                                key: ValueKey('review-access-${user.userId}'),
                                title: Text(user.displayName),
                                subtitle: Text(
                                  '@${user.username}'
                                  '${user.isActive ? '' : ' · 已停用'}',
                                ),
                                value: overview.accessUserIds.contains(
                                  user.userId,
                                ),
                                onChanged: _busy
                                    ? null
                                    : (value) => _toggleAccess(user, value),
                              ),
                            ),
                      ],
                    ),
                  ),
                ),
        ),
      ),
    );
  }

  Widget _modelStatus(ReviewOverview overview) {
    final configured = overview.modelConfigured;
    return Card(
      key: const ValueKey('review-model-status'),
      color: configured ? const Color(0x3326A269) : const Color(0x33E07A72),
      child: ListTile(
        leading: Icon(
          configured ? Icons.check_circle_outline : Icons.error_outline,
          color: configured ? const Color(0xFF6DE0A4) : const Color(0xFFE07A72),
        ),
        title: Text(configured ? '大模型：${overview.model}' : '服务端没有配置大模型'),
        subtitle: Text(
          configured
              ? '地址、型号与密钥在服务端环境变量里配置'
              : '在服务端设置 REVIEW_API_KEY 等环境变量并重启后才能使用',
        ),
      ),
    );
  }

  Widget _sectionTitle(String text) => Padding(
    padding: const EdgeInsets.only(bottom: 6),
    child: Text(
      text,
      style: const TextStyle(fontSize: 16, fontWeight: FontWeight.w800),
    ),
  );
}

String _formatTokens(int tokens) {
  if (tokens >= 1000000) return '${(tokens / 1000000).toStringAsFixed(2)} 百万';
  if (tokens >= 10000) return '${(tokens / 10000).toStringAsFixed(1)} 万';
  return '$tokens';
}
