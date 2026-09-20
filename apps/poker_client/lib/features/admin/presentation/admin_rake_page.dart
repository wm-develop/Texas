import 'package:flutter/material.dart';
import 'package:poker_client/core/network/game_api_client.dart';
import 'package:poker_client/core/widgets/platform_number_field.dart';
import 'package:poker_client/features/admin/domain/rake.dart';
import 'package:poker_client/features/admin/presentation/rake_settings_dialog.dart';
import 'package:poker_client/features/table/domain/room_result.dart';

/// 管理员的抽水管理：给开着的房间设置抽水规则，按房间查看累计抽水并做本地换算。
///
/// 换算与「本房间战绩」一样只发生在客户端：服务端不存储也不传输任何金额。
class AdminRakePage extends StatefulWidget {
  const AdminRakePage({
    required this.loadRooms,
    required this.loadSummary,
    required this.saveRake,
    super.key,
  });

  final Future<List<AdminRoom>> Function() loadRooms;
  final Future<RakeSummary> Function() loadSummary;
  final Future<RakeSettings> Function(String roomId, RakeSettings settings)
  saveRake;

  @override
  State<AdminRakePage> createState() => _AdminRakePageState();
}

class _AdminRakePageState extends State<AdminRakePage> {
  final _money = TextEditingController(
    text: ChipExchangeRate.defaultRate.money.toStringAsFixed(0),
  );
  final _chips = TextEditingController(
    text: '${ChipExchangeRate.defaultRate.chips}',
  );
  // null 表示还没成功取到过：此时不能显示「没有房间」「没有记录」这类空状态，
  // 那是在把「没取到」说成「没有」。
  List<AdminRoom>? _rooms;
  RakeSummary? _summary;
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
    _money.dispose();
    _chips.dispose();
    super.dispose();
  }

  /// 两个请求各取各的：累计取不到不该让管理员连规则都设不了，反之亦然。
  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    String? error;
    try {
      final rooms = await widget.loadRooms();
      if (mounted) setState(() => _rooms = rooms);
    } on Object catch (failure) {
      error = '读取房间列表失败：${_message(failure)}';
    }
    try {
      final summary = await widget.loadSummary();
      if (mounted) setState(() => _summary = summary);
    } on Object catch (failure) {
      final text = '读取累计抽水失败：${_message(failure)}';
      error = error == null ? text : '$error\n$text';
    }
    if (!mounted) return;
    setState(() {
      _error = error;
      _loading = false;
    });
  }

  Future<void> _edit(AdminRoom room) async {
    final settings = await RakeSettingsDialog.show(context, room);
    if (settings == null || !mounted) return;
    setState(() {
      _busy = true;
      _error = null;
    });
    try {
      await widget.saveRake(room.roomId, settings);
      if (!mounted) return;
      ScaffoldMessenger.of(context)
        ..hideCurrentSnackBar()
        ..showSnackBar(
          SnackBar(content: Text('房间 ${room.roomCode} 的抽水已更新，下一手起生效')),
        );
      await _load();
    } on Object catch (error) {
      if (mounted) setState(() => _error = _message(error));
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  ChipExchangeRate get _rate => ChipExchangeRate(
    money: double.tryParse(_money.text.trim()) ?? 0,
    chips: int.tryParse(_chips.text.trim()) ?? 0,
  );

  String _message(Object error) {
    if (error is GameApiTimeoutException) return '服务器响应超时，请刷新后确认操作结果';
    if (error is GameApiException) {
      return switch (error.code) {
        'room_not_found' => '这个房间已经关闭',
        'invalid_rake_settings' => '抽水规则不合法：比例不超过 10%，翻后加抽不超过一个大盲',
        'admin_required' || 'forbidden' => '需要管理员权限',
        'service_unavailable' => '服务端未启用这项功能',
        'internal_error' => '服务端出错，请稍后重试',
        'rate_limited' => '操作太频繁，请稍后再试',
        _ => '操作失败（${error.code}）',
      };
    }
    return '无法连接游戏服务';
  }

  String _money2(int chips, ChipExchangeRate rate) {
    final money = rate.convert(chips);
    return money == null ? '' : '≈ ${money.toStringAsFixed(2)} 元';
  }

  @override
  Widget build(BuildContext context) {
    final rate = _rate;
    final rooms = _rooms;
    final summary = _summary;
    return Scaffold(
      appBar: AppBar(
        title: const Text('抽水管理'),
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
          // 只有第一次加载才整页转圈；保存后的刷新保留列表与滚动位置
          child: _loading && rooms == null && summary == null
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
                        _sectionTitle('开着的房间'),
                        const Text(
                          '抽水只有管理员能设置，新房间默认不抽。抽走的筹码进入管理员账户。',
                          style: TextStyle(color: Colors.white60, fontSize: 12),
                        ),
                        const SizedBox(height: 8),
                        if (rooms != null && rooms.isEmpty)
                          const Padding(
                            padding: EdgeInsets.symmetric(vertical: 16),
                            child: Text(
                              '当前没有开着的房间',
                              style: TextStyle(color: Colors.white54),
                            ),
                          ),
                        for (final room in rooms ?? const <AdminRoom>[])
                          Card(
                            child: ListTile(
                              key: ValueKey('rake-room-${room.roomId}'),
                              title: Text(
                                '房间 ${room.roomCode} · '
                                '${room.smallBlind}/${room.bigBlind}',
                              ),
                              subtitle: Text(
                                '${room.ownerName.isEmpty ? '' : '房主 ${room.ownerName} · '}'
                                '上桌 ${room.seatedCount} 人'
                                '${room.spectatorCount > 0 ? ' · 观战 ${room.spectatorCount} 人' : ''}\n'
                                '${room.rake.summary} · 已抽 ${room.rakeTotal}',
                              ),
                              isThreeLine: true,
                              trailing: FilledButton.tonal(
                                onPressed: _busy ? null : () => _edit(room),
                                child: const Text('设置'),
                              ),
                            ),
                          ),
                        const SizedBox(height: 20),
                        _sectionTitle('累计抽水'),
                        const Text(
                          '换算比例（仅本机参考，服务端不记录任何金额）',
                          style: TextStyle(color: Colors.white60, fontSize: 12),
                        ),
                        const SizedBox(height: 8),
                        Row(
                          children: [
                            Expanded(
                              child: PlatformNumberField(
                                controller: _money,
                                onChanged: (_) => setState(() {}),
                                decoration: const InputDecoration(
                                  labelText: '金额（元）',
                                  border: OutlineInputBorder(),
                                  isDense: true,
                                ),
                              ),
                            ),
                            const Padding(
                              padding: EdgeInsets.symmetric(horizontal: 10),
                              child: Text('='),
                            ),
                            Expanded(
                              child: PlatformNumberField(
                                controller: _chips,
                                onChanged: (_) => setState(() {}),
                                decoration: const InputDecoration(
                                  labelText: '筹码',
                                  border: OutlineInputBorder(),
                                  isDense: true,
                                ),
                              ),
                            ),
                          ],
                        ),
                        const SizedBox(height: 12),
                        Card(
                          color: const Color(0x33F6D986),
                          child: ListTile(
                            key: const ValueKey('rake-grand-total'),
                            title: Text(
                              '合计 ${summary?.total ?? '—'}',
                              style: const TextStyle(
                                fontSize: 20,
                                fontWeight: FontWeight.w800,
                                color: Color(0xFFF6D986),
                              ),
                            ),
                            subtitle: Text(
                              '${summary?.rooms.length ?? '—'} 个房间 · ${summary?.hands ?? '—'} 手'
                              '${rate.isValid ? '' : ' · 请输入大于 0 的金额与筹码'}',
                            ),
                            trailing: _trailing(
                              summary == null
                                  ? ''
                                  : _money2(summary.total, rate),
                              fontSize: 16,
                            ),
                          ),
                        ),
                        if (summary != null && summary.rooms.isEmpty)
                          const Padding(
                            padding: EdgeInsets.symmetric(vertical: 16),
                            child: Text(
                              '还没有任何抽水记录',
                              style: TextStyle(color: Colors.white54),
                            ),
                          ),
                        for (final entry
                            in summary?.rooms ?? const <RoomRakeTotal>[])
                          ListTile(
                            key: ValueKey('rake-total-${entry.roomId}'),
                            dense: true,
                            title: Text(
                              '房间 ${entry.roomCode.isEmpty ? '（已关闭）' : entry.roomCode}'
                              '${entry.closed && entry.roomCode.isNotEmpty ? ' · 已关闭' : ''}',
                            ),
                            subtitle: Text('${entry.hands} 手'),
                            trailing: _trailing(
                              '${entry.total}'
                              '${rate.isValid ? '  ${_money2(entry.total, rate)}' : ''}',
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

  /// 行尾的数字。窄窗口里数字一长就会把整行宽度占满（ListTile 直接报错），
  /// 所以限宽并在放不下时缩小，而不是撑破。
  Widget _trailing(String text, {double? fontSize}) => ConstrainedBox(
    constraints: const BoxConstraints(maxWidth: 170),
    child: FittedBox(
      fit: BoxFit.scaleDown,
      alignment: Alignment.centerRight,
      child: Text(
        text,
        style: TextStyle(fontSize: fontSize, fontWeight: FontWeight.w700),
      ),
    ),
  );

  Widget _sectionTitle(String text) => Padding(
    padding: const EdgeInsets.only(bottom: 4),
    child: Text(
      text,
      style: const TextStyle(fontSize: 16, fontWeight: FontWeight.w700),
    ),
  );
}
