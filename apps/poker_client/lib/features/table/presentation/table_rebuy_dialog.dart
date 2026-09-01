import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:poker_client/core/widgets/platform_number_field.dart';

/// 两手之间的补码对话框。

class TableRebuyAmountDialog extends StatefulWidget {
  const TableRebuyAmountDialog({
    required this.automatic,
    required this.walletChips,
    required this.currentStack,
    required this.maximum,
    required this.available,
    super.key,
  });

  final bool automatic;
  final int walletChips;
  final int currentStack;
  final int maximum;
  final int available;

  @override
  State<TableRebuyAmountDialog> createState() => TableRebuyAmountDialogState();
}

class TableRebuyAmountDialogState extends State<TableRebuyAmountDialog> {
  late final TextEditingController _controller;
  late int _amount;

  bool get _valid => _amount > 0 && _amount <= widget.available;

  @override
  void initState() {
    super.initState();
    _amount = widget.available;
    _controller = TextEditingController(text: '$_amount');
  }

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final sliderMaximum = widget.available > 0 ? widget.available : 1;
    final sliderValue = _amount.clamp(0, sliderMaximum).toDouble();
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
      title: Text(widget.automatic ? '筹码已用完，请补码' : '补码'),
      content: SizedBox(
        width: 440,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text(
              '钱包 ${widget.walletChips} · 当前 ${widget.currentStack} · 最大带入 ${widget.maximum}',
            ),
            const SizedBox(height: 12),
            Slider(
              value: sliderValue,
              min: 0,
              max: sliderMaximum.toDouble(),
              divisions: widget.available > 0
                  ? math.min(widget.available, 100)
                  : 1,
              label: '$_amount',
              onChanged: widget.available <= 0
                  ? null
                  : (value) {
                      setState(() => _amount = value.round());
                      _controller.text = '$_amount';
                    },
            ),
            PlatformNumberField(
              controller: _controller,
              scrollPadding: const EdgeInsets.only(bottom: 100),
              decoration: InputDecoration(
                labelText: '补码数量',
                helperText: widget.available > 0
                    ? '本次最多可补 ${widget.available}'
                    : '钱包余额不足，请返回大厅充值',
                errorText: _amount > widget.available ? '补码数量超过本次上限' : null,
              ),
              onChanged: (value) =>
                  setState(() => _amount = int.tryParse(value) ?? 0),
              onSubmitted: (_) {
                if (_valid) Navigator.of(context).pop(_amount);
              },
            ),
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: Text(widget.automatic ? '暂不补码' : '取消'),
        ),
        FilledButton(
          onPressed: _valid ? () => Navigator.of(context).pop(_amount) : null,
          child: const Text('确认补码'),
        ),
      ],
    );
  }
}
