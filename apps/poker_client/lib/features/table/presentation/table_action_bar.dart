import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:poker_client/core/network/game_socket_client.dart';
import 'package:poker_client/core/widgets/platform_number_field.dart';
import 'package:poker_client/features/table/domain/table_snapshot.dart';
import 'package:poker_client/features/table/presentation/table_labels.dart';
import 'package:poker_client/features/table/presentation/responsive_action_strip.dart';

/// 底部动作栏与自定义下注对话框。
/// 合法动作与快捷金额均来自服务端快照，本文件只负责展示与提交意图。

class TableActionBar extends StatelessWidget {
  const TableActionBar({
    required this.client,
    required this.userId,
    required this.smallBlind,
    required this.onRebuy,
    super.key,
  });

  final GameSocketClient client;
  final String userId;
  final int smallBlind;
  final VoidCallback onRebuy;

  @override
  Widget build(BuildContext context) {
    final snapshot = client.snapshot;
    final ownSeat = snapshot?.seats
        .where((seat) => seat.userId == userId)
        .firstOrNull;
    final current = snapshot?.currentAction;
    final ownTurn = current?.userId == userId;
    final options = ownTurn ? current!.options : null;
    final waiting =
        snapshot == null ||
        snapshot.phase == 'WAITING' ||
        snapshot.phase == 'WAITING_NEXT_HAND';
    final autoReadyRemaining = snapshot?.autoReadyDeadline?.difference(
      client.serverNow,
    );
    final autoReadyPending = autoReadyRemaining != null;
    final runoutChoice = snapshot?.runoutChoice;
    final runoutRemaining = runoutChoice?.deadline?.difference(
      client.serverNow,
    );
    final ownRunoutChoice = runoutChoice?.choices[userId];
    return Container(
      height: 70,
      padding: const EdgeInsets.symmetric(horizontal: 14),
      decoration: BoxDecoration(
        color: const Color(0xED0A1C18),
        borderRadius: BorderRadius.circular(18),
        border: Border.all(color: Colors.white12),
      ),
      child: Row(
        children: [
          if (client.actionPending) ...[
            const SizedBox(
              width: 18,
              height: 18,
              child: CircularProgressIndicator(strokeWidth: 2),
            ),
            const SizedBox(width: 10),
          ],
          if (waiting)
            Expanded(
              child: Row(
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  FilledButton.icon(
                    onPressed:
                        client.status == GameSocketStatus.joined &&
                            (ownSeat?.stack ?? 0) > 0 &&
                            // 服务端排空期间准备会被拒绝，直接禁用并由状态条解释
                            snapshot?.draining != true
                        ? () {
                            if (ownSeat?.ready == true) {
                              client.setReady(false);
                            } else if (autoReadyPending &&
                                !(snapshot?.autoReadyCancelled ?? false)) {
                              client.setReady(false);
                            } else {
                              client.setReady(true);
                            }
                          }
                        : null,
                    icon: Icon(
                      ownSeat?.ready == true ||
                              (autoReadyPending &&
                                  !(snapshot?.autoReadyCancelled ?? false))
                          ? Icons.pause_circle
                          : Icons.play_circle,
                    ),
                    label: Text(
                      ownSeat?.ready == true
                          ? '取消准备'
                          : autoReadyPending &&
                                !(snapshot?.autoReadyCancelled ?? false)
                          ? autoReadyRemaining > Duration.zero
                                ? '取消自动准备（${remainingSeconds(autoReadyRemaining)}秒）'
                                : '正在自动准备…'
                          : '准备开始',
                    ),
                  ),
                  const SizedBox(width: 12),
                  OutlinedButton.icon(
                    onPressed:
                        client.status == GameSocketStatus.joined &&
                            (ownSeat?.stack ?? 0) < (snapshot?.maxBuyIn ?? 0)
                        ? onRebuy
                        : null,
                    icon: const Icon(Icons.add_circle_outline),
                    label: const Text('补码'),
                  ),
                  if (snapshot?.canShowHoleCards ?? false) ...[
                    const SizedBox(width: 12),
                    FilledButton.tonalIcon(
                      onPressed: client.showHoleCards,
                      icon: const Icon(Icons.visibility_outlined, size: 17),
                      label: const Text('展示手牌'),
                    ),
                  ],
                ],
              ),
            )
          else if (runoutChoice != null)
            Expanded(
              child: Row(
                mainAxisAlignment: MainAxisAlignment.center,
                children: [
                  Text(
                    '全下发牌选择 · ${remainingSeconds(runoutRemaining ?? Duration.zero)} 秒',
                    style: const TextStyle(
                      color: Color(0xFFF6D986),
                      fontWeight: FontWeight.w700,
                    ),
                  ),
                  const SizedBox(width: 16),
                  if (!runoutChoice.eligiblePlayerIds.contains(userId))
                    const Text('等待两名玩家选择')
                  else if (ownRunoutChoice != null)
                    Text(
                      '已选择发 $ownRunoutChoice 次，等待对方',
                      style: const TextStyle(color: Colors.white70),
                    )
                  else ...[
                    OutlinedButton(
                      onPressed: () => client.chooseRunoutCount(1),
                      child: const Text('发一次'),
                    ),
                    const SizedBox(width: 10),
                    FilledButton(
                      onPressed: () => client.chooseRunoutCount(2),
                      child: const Text('发两次'),
                    ),
                  ],
                ],
              ),
            )
          else if (!ownTurn)
            const Expanded(
              child: Center(
                child: Text(
                  '等待其他玩家行动',
                  style: TextStyle(color: Colors.white60),
                ),
              ),
            )
          else
            Expanded(
              child: ResponsiveActionStrip(
                leadingActions: [
                  OutlinedButton(
                    key: const ValueKey('bet-fold-action'),
                    onPressed: options!.canFold && !client.actionPending
                        ? () => client.submitAction('fold')
                        : null,
                    child: const Text('弃牌'),
                  ),
                  if (options.canCheck || options.canCall)
                    FilledButton.tonal(
                      key: const ValueKey('bet-check-call-action'),
                      onPressed: client.actionPending
                          ? null
                          : options.canCheck
                          ? () => client.submitAction('check')
                          : () => client.submitAction('call'),
                      child: Text(
                        options.canCheck ? '过牌' : '跟注 ${options.toCall}',
                      ),
                    ),
                ],
                presetActions: [
                  for (final suggestion in current!.suggestions)
                    FilledButton.tonal(
                      key: ValueKey(
                        'bet-suggestion-${suggestion.label}-${suggestion.raiseTo}',
                      ),
                      onPressed: client.actionPending
                          ? null
                          : () => client.submitAction(
                              suggestion.action,
                              raiseTo: suggestion.action == 'all_in'
                                  ? null
                                  : suggestion.raiseTo,
                            ),
                      child: Text(
                        suggestionButtonLabel(
                          suggestion,
                          ownSeat?.streetBet ?? 0,
                        ),
                        textAlign: TextAlign.center,
                        style: const TextStyle(fontSize: 11, height: 1.05),
                      ),
                    ),
                ],
                trailingAction: options.canBet || options.canRaise
                    ? OutlinedButton.icon(
                        key: const ValueKey('bet-custom-action'),
                        onPressed: client.actionPending
                            ? null
                            : () => showDialog<void>(
                                context: context,
                                builder: (context) => TableBetAmountDialog(
                                  client: client,
                                  options: options,
                                  streetBet: ownSeat?.streetBet ?? 0,
                                  smallBlind: smallBlind,
                                  onSubmit: (action, raiseTo) => client
                                      .submitAction(action, raiseTo: raiseTo),
                                ),
                              ),
                        icon: const Icon(Icons.tune, size: 18),
                        label: const Text('自定义'),
                      )
                    : null,
              ),
            ),
        ],
      ),
    );
  }
}

class TableBetAmountDialog extends StatefulWidget {
  const TableBetAmountDialog({
    required this.client,
    required this.options,
    required this.streetBet,
    required this.smallBlind,
    required this.onSubmit,
    super.key,
  });

  final GameSocketClient client;
  final TableActionOptions options;
  final int streetBet;
  final int smallBlind;
  final void Function(String action, int raiseTo) onSubmit;

  @override
  State<TableBetAmountDialog> createState() => TableBetAmountDialogState();
}

class TableBetAmountDialogState extends State<TableBetAmountDialog> {
  late int _amount;
  late final TextEditingController _controller;
  String? _error;
  Timer? _clock;

  int get _unit => math.max(1, widget.smallBlind);

  @override
  void initState() {
    super.initState();
    _amount = widget.options.minRaiseTo;
    _controller = TextEditingController(text: '$_amount');
    widget.client.addListener(_refresh);
    _clock = Timer.periodic(
      const Duration(milliseconds: 200),
      (_) => _refresh(),
    );
  }

  @override
  void dispose() {
    _clock?.cancel();
    widget.client.removeListener(_refresh);
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final options = widget.options;
    final action = options.canBet ? 'bet' : 'raise';
    final actionLabel = options.canBet ? '下注' : '加注';
    final current = widget.client.snapshot?.currentAction;
    final deadline = current?.deadline;
    final remaining = deadline == null
        ? Duration.zero
        : deadline.difference(widget.client.serverNow);
    final ownSeat = widget.client.snapshot?.seats
        .where((seat) => seat.userId == widget.client.userId)
        .firstOrNull;
    final secondsLeft = remainingSeconds(remaining);
    final keyboardVisible = MediaQuery.viewInsetsOf(context).bottom > 0;
    return AlertDialog(
      scrollable: true,
      insetPadding: EdgeInsets.symmetric(
        horizontal: 16,
        vertical: keyboardVisible ? 8 : 20,
      ),
      titlePadding: EdgeInsets.fromLTRB(
        24,
        keyboardVisible ? 10 : 18,
        16,
        keyboardVisible ? 2 : 8,
      ),
      contentPadding: EdgeInsets.fromLTRB(
        24,
        keyboardVisible ? 4 : 8,
        24,
        keyboardVisible ? 6 : 12,
      ),
      actionsPadding: const EdgeInsets.fromLTRB(16, 0, 16, 10),
      title: Row(
        children: [
          Text('$actionLabel额度'),
          const Spacer(),
          Icon(
            Icons.timer_outlined,
            size: 19,
            color: secondsLeft <= 5 ? Colors.redAccent : Colors.white70,
          ),
          const SizedBox(width: 5),
          Text(
            '剩余 $secondsLeft 秒',
            style: TextStyle(
              fontSize: 14,
              fontWeight: FontWeight.w700,
              color: secondsLeft <= 5 ? Colors.redAccent : Colors.white70,
            ),
          ),
          const SizedBox(width: 8),
          OutlinedButton(
            onPressed:
                (ownSeat?.timeExtensions ?? 0) > 0 &&
                    current?.userId == widget.client.userId
                ? widget.client.useTimeExtension
                : null,
            child: Text('加时 +30秒 ×${ownSeat?.timeExtensions ?? 0}'),
          ),
        ],
      ),
      content: SizedBox(
        width: 520,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text(
              '允许范围：${options.minRaiseTo} ～ ${options.maxRaiseTo} · '
              '最小单位：小盲 $_unit',
              style: const TextStyle(color: Colors.white60),
            ),
            const SizedBox(height: 10),
            if (options.maxRaiseTo > options.minRaiseTo)
              Slider(
                value: _amount
                    .clamp(options.minRaiseTo, options.maxRaiseTo)
                    .toDouble(),
                min: options.minRaiseTo.toDouble(),
                max: options.maxRaiseTo.toDouble(),
                label: '$_amount',
                onChanged: (value) => _setAmount(value.round()),
              ),
            PlatformNumberField(
              controller: _controller,
              scrollPadding: const EdgeInsets.only(bottom: 100),
              decoration: InputDecoration(
                labelText: '$actionLabel至',
                border: const OutlineInputBorder(),
                errorText: _error,
              ),
              onChanged: (value) {
                final parsed = int.tryParse(value);
                if (parsed != null) {
                  setState(() {
                    _amount = parsed;
                    _error = parsed % _unit == 0 ? null : '额度必须是小盲 $_unit 的整数倍';
                  });
                }
              },
            ),
            const SizedBox(height: 10),
            Text(
              '$actionLabel至 $_amount · 本次还需投入 ${math.max(0, _amount - widget.streetBet)}',
              style: const TextStyle(
                color: Color(0xFFF6D986),
                fontWeight: FontWeight.w700,
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
          onPressed: () {
            final entered = int.tryParse(_controller.text);
            if (entered == null ||
                entered < options.minRaiseTo ||
                entered > options.maxRaiseTo ||
                entered % _unit != 0) {
              if (entered != null && entered % _unit != 0) {
                setState(() => _error = '额度必须是小盲 $_unit 的整数倍');
                return;
              }
              setState(() => _error = '请输入允许范围内的额度');
              return;
            }
            Navigator.of(context).pop();
            widget.onSubmit(action, entered);
          },
          child: Text('$actionLabel至 $_amount'),
        ),
      ],
    );
  }

  void _setAmount(int value) {
    final snapped = ((value + _unit ~/ 2) ~/ _unit) * _unit;
    final clamped = snapped.clamp(
      widget.options.minRaiseTo,
      widget.options.maxRaiseTo,
    );
    setState(() {
      _amount = clamped;
      _controller.text = '$clamped';
      _error = null;
    });
  }

  void _refresh() {
    if (!mounted) return;
    if (widget.client.snapshot?.currentAction?.userId != widget.client.userId) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) Navigator.of(context).maybePop();
      });
      return;
    }
    setState(() {});
  }
}
