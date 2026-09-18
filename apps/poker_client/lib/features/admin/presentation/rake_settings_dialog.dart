import 'package:flutter/material.dart';
import 'package:poker_client/core/widgets/platform_number_field.dart';
import 'package:poker_client/features/admin/domain/rake.dart';

/// 编辑一个房间的抽水规则。确认后返回新规则，取消返回 null。
///
/// 比例用滑块而不是输入框：HarmonyOS 的数字面板只有整数键，2.5% 这样的比例输不进去。
class RakeSettingsDialog extends StatefulWidget {
  const RakeSettingsDialog({required this.room, super.key});

  final AdminRoom room;

  static Future<RakeSettings?> show(BuildContext context, AdminRoom room) =>
      showDialog<RakeSettings>(
        context: context,
        builder: (_) => RakeSettingsDialog(room: room),
      );

  @override
  State<RakeSettingsDialog> createState() => _RakeSettingsDialogState();
}

class _RakeSettingsDialogState extends State<RakeSettingsDialog> {
  late bool _enabled = widget.room.rake.enabled;
  late int _basisPoints = widget.room.rake.basisPoints;
  late bool _postflopEnabled = widget.room.rake.postflopEnabled;
  late final _cap = TextEditingController(
    text: widget.room.rake.cap > 0 ? '${widget.room.rake.cap}' : '',
  );
  late final _postflopAmount = TextEditingController(
    text: widget.room.rake.postflopAmount > 0
        ? '${widget.room.rake.postflopAmount}'
        : '',
  );
  String? _error;

  @override
  void dispose() {
    _cap.dispose();
    _postflopAmount.dispose();
    super.dispose();
  }

  void _submit() {
    if (!_enabled) {
      // 关掉时其余字段不在界面上，保留原值：下次再开不用重填，也不会把看不见的输入提交上去
      final current = widget.room.rake;
      Navigator.of(context).pop(
        RakeSettings(
          basisPoints: current.basisPoints,
          cap: current.cap,
          postflopEnabled: current.postflopEnabled,
          postflopAmount: current.postflopAmount,
        ),
      );
      return;
    }
    final capText = _cap.text.trim();
    final postflopText = _postflopAmount.text.trim();
    final cap = capText.isEmpty ? 0 : int.tryParse(capText);
    // 加抽开关关着时输入框不可见：保留原来的数额（服务端此时不用它），下次打开不用重填
    final postflop = !_postflopEnabled
        ? widget.room.rake.postflopAmount
        : postflopText.isEmpty
        ? 0
        : int.tryParse(postflopText);
    if (cap == null || cap < 0) {
      setState(() => _error = '封顶请填非负整数，填 0 或留空表示不封顶');
      return;
    }
    if (postflop == null || postflop < 0) {
      setState(() => _error = '翻后加抽请填非负整数');
      return;
    }
    if (_postflopEnabled && postflop > widget.room.bigBlind) {
      setState(() => _error = '翻后加抽不能超过一个大盲（${widget.room.bigBlind}）');
      return;
    }
    if (_postflopEnabled && postflop == 0) {
      setState(() => _error = '已打开翻后加抽，请填写加抽的筹码数');
      return;
    }
    if (_basisPoints == 0 && !_postflopEnabled) {
      setState(() => _error = '已开启抽水，但比例为 0 且没有翻后加抽，实际不会抽');
      return;
    }
    Navigator.of(context).pop(
      RakeSettings(
        enabled: true,
        basisPoints: _basisPoints,
        cap: cap,
        postflopEnabled: _postflopEnabled,
        postflopAmount: postflop,
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final room = widget.room;
    return AlertDialog(
      title: Text('房间 ${room.roomCode} 的抽水'),
      // 手机横屏高度有限，内容一律可滚动
      scrollable: true,
      content: SizedBox(
        width: 380,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text(
              '盲注 ${room.smallBlind}/${room.bigBlind}。'
              '修改从下一手起生效，并会在房间文字聊天里公告具体规则。'
              '抽水按各池大小分摊；底池不足两个大盲时不加抽。',
              style: const TextStyle(color: Colors.white70, fontSize: 12),
            ),
            SwitchListTile(
              key: const ValueKey('rake-enabled'),
              contentPadding: EdgeInsets.zero,
              title: const Text('开启抽水'),
              value: _enabled,
              onChanged: (value) => setState(() {
                _enabled = value;
                _error = null;
              }),
            ),
            if (_enabled) ...[
              Text(
                '每手按底池的 ${formatBasisPoints(_basisPoints)} 抽水（向下取整，'
                '没人跟注而退回的筹码不计入底池）',
              ),
              Slider(
                key: const ValueKey('rake-basis-points'),
                value: _basisPoints
                    .clamp(0, RakeSettings.maximumBasisPoints)
                    .toDouble(),
                max: RakeSettings.maximumBasisPoints.toDouble(),
                divisions:
                    RakeSettings.maximumBasisPoints ~/
                    RakeSettings.basisPointStep,
                label: formatBasisPoints(_basisPoints),
                onChanged: (value) => setState(() {
                  _basisPoints = value.round();
                  _error = null;
                }),
              ),
              PlatformNumberField(
                key: const ValueKey('rake-cap'),
                controller: _cap,
                maxLength: 9,
                onChanged: (_) => setState(() => _error = null),
                decoration: const InputDecoration(
                  labelText: '按比例抽水的封顶筹码',
                  hintText: '填 0 或留空表示不封顶',
                  counterText: '',
                  border: OutlineInputBorder(),
                  isDense: true,
                ),
              ),
              SwitchListTile(
                key: const ValueKey('rake-postflop-enabled'),
                contentPadding: EdgeInsets.zero,
                title: const Text('翻后加抽'),
                subtitle: const Text('发出翻牌的手额外再抽一笔，不受封顶限制'),
                value: _postflopEnabled,
                onChanged: (value) => setState(() {
                  _postflopEnabled = value;
                  _error = null;
                }),
              ),
              if (_postflopEnabled)
                PlatformNumberField(
                  key: const ValueKey('rake-postflop-amount'),
                  controller: _postflopAmount,
                  maxLength: 9,
                  onChanged: (_) => setState(() => _error = null),
                  decoration: InputDecoration(
                    labelText: '翻后加抽筹码（不超过 ${room.bigBlind}）',
                    counterText: '',
                    border: const OutlineInputBorder(),
                    isDense: true,
                  ),
                ),
            ],
            if (_error != null)
              Padding(
                padding: const EdgeInsets.only(top: 10),
                child: Text(
                  _error!,
                  style: const TextStyle(color: Colors.redAccent),
                ),
              ),
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('取消'),
        ),
        FilledButton(
          key: const ValueKey('rake-save'),
          onPressed: _submit,
          child: const Text('保存'),
        ),
      ],
    );
  }
}
