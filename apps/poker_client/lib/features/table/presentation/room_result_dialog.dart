import 'package:flutter/material.dart';
import 'package:poker_client/core/network/game_api_client.dart';
import 'package:poker_client/core/widgets/platform_number_field.dart';
import 'package:poker_client/features/table/domain/room_result.dart';

/// 本房间战绩窗口：显示净胜负筹码，并按玩家自定的比例做一次本地换算。
///
/// 换算只发生在客户端：服务端不存储也不传输任何金额，产品不接入支付，
/// 这里只是熟人之间自行结算时的参考。
class RoomResultDialog extends StatefulWidget {
  const RoomResultDialog({required this.loadResult, super.key});

  final Future<RoomResult> Function() loadResult;

  @override
  State<RoomResultDialog> createState() => _RoomResultDialogState();
}

class _RoomResultDialogState extends State<RoomResultDialog> {
  final _money = TextEditingController(
    text: ChipExchangeRate.defaultRate.money.toStringAsFixed(0),
  );
  final _chips = TextEditingController(
    text: '${ChipExchangeRate.defaultRate.chips}',
  );
  RoomResult? _result;
  String? _error;
  bool _loading = true;

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

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final result = await widget.loadResult();
      if (mounted) setState(() => _result = result);
    } on Object catch (error) {
      if (mounted) setState(() => _error = _message(error));
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  ChipExchangeRate get _rate => ChipExchangeRate(
    money: double.tryParse(_money.text.trim()) ?? 0,
    chips: int.tryParse(_chips.text.trim()) ?? 0,
  );

  String _message(Object error) {
    if (error is GameApiTimeoutException) return '服务器响应超时，请稍后重试';
    if (error is GameApiException) {
      return switch (error.code) {
        'room_not_found' => '你已经不在任何房间中',
        _ => '读取战绩失败（${error.code}）',
      };
    }
    return '无法连接游戏服务';
  }

  @override
  Widget build(BuildContext context) {
    final result = _result;
    final rate = _rate;
    final money = result == null ? null : rate.convert(result.net);
    return AlertDialog(
      title: const Text('本房间战绩'),
      content: SizedBox(
        width: 360,
        // 手机横屏高度有限，内容一律可滚动
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              if (_loading)
                const Padding(
                  padding: EdgeInsets.symmetric(vertical: 24),
                  child: Center(child: CircularProgressIndicator()),
                )
              else if (_error != null)
                Text(_error!, style: const TextStyle(color: Colors.redAccent))
              else if (result != null) ...[
                Center(
                  child: Text(
                    '${result.net >= 0 ? '+' : ''}${result.net}',
                    style: TextStyle(
                      fontSize: 40,
                      fontWeight: FontWeight.w800,
                      color: result.net >= 0
                          ? const Color(0xFF6DE0A4)
                          : const Color(0xFFE07A72),
                    ),
                  ),
                ),
                const Center(
                  child: Text(
                    '净胜负筹码',
                    style: TextStyle(color: Colors.white60, fontSize: 12),
                  ),
                ),
                const SizedBox(height: 14),
                _line('累计带入', '${result.boughtIn}'),
                _line('离桌返还', '${result.returnedToWallet}'),
                _line('桌上筹码', '${result.tableChips}'),
                const Divider(height: 22),
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
                const SizedBox(height: 14),
                Center(
                  child: Text(
                    money == null
                        ? '请输入大于 0 的金额与筹码'
                        : '折合 ${money >= 0 ? '+' : ''}${money.toStringAsFixed(2)} 元',
                    style: TextStyle(
                      fontSize: 18,
                      fontWeight: FontWeight.w700,
                      color: money == null
                          ? Colors.white54
                          : const Color(0xFFF6D986),
                    ),
                  ),
                ),
              ],
            ],
          ),
        ),
      ),
      actions: [
        if (!_loading)
          TextButton(onPressed: _load, child: const Text('刷新')),
        FilledButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('关闭'),
        ),
      ],
    );
  }

  Widget _line(String label, String value) => Padding(
    padding: const EdgeInsets.symmetric(vertical: 3),
    child: Row(
      mainAxisAlignment: MainAxisAlignment.spaceBetween,
      children: [
        Text(label, style: const TextStyle(color: Colors.white70)),
        Text(value, style: const TextStyle(fontWeight: FontWeight.w700)),
      ],
    ),
  );
}
