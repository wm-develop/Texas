import 'package:flutter/material.dart';
import 'package:poker_client/core/network/game_socket_client.dart';
import 'package:poker_client/core/widgets/platform_number_field.dart';
import 'package:poker_client/features/table/domain/bet_amount.dart';
import 'package:poker_client/features/table/domain/table_snapshot.dart';
import 'package:poker_client/features/table/presentation/table_labels.dart';

/// 轮到自己行动时的下注区：最多三个大按钮，加注额度由预设档、滑块和直接输入
/// 三种方式共同决定，改额度与提交动作分成两步。
///
/// 只有三个按钮的原因：弃牌任何时候都合法；能过牌时不存在"跟注"，第三个键是
/// 下注；不能过牌时第三个键是加注。全下不单独占一个按钮，把额度推到最右即可，
/// 具体换算见 [BetAmountModel]。
class TableBetPanel extends StatefulWidget {
  const TableBetPanel({
    required this.client,
    required this.userId,
    required this.smallBlind,
    required this.options,
    required this.suggestions,
    required this.ownSeat,
    this.vertical = false,
    this.blocked = false,
    super.key,
  });

  final GameSocketClient client;
  final String userId;
  final int smallBlind;
  final TableActionOptions options;
  final List<BetSuggestion> suggestions;
  final TableSeatSnapshot? ownSeat;

  /// 竖排用于手机右栏，横排用于平板与桌面底部右侧。
  final bool vertical;

  /// 发牌演出进行中，暂不接受输入。
  final bool blocked;

  @override
  State<TableBetPanel> createState() => _TableBetPanelState();
}

class _TableBetPanelState extends State<TableBetPanel> {
  int? _amount;
  String _signature = '';

  /// 由 State 持有：对话框关闭后还有一帧退场动画会读它，
  /// 在 showDialog 返回处立即 dispose 会触发「controller used after disposed」。
  final TextEditingController _amountController = TextEditingController();

  @override
  void dispose() {
    _amountController.dispose();
    super.dispose();
  }

  BetAmountModel get _model => BetAmountModel.fromOptions(
    options: widget.options,
    ownSeat: widget.ownSeat,
    smallBlind: widget.smallBlind,
  );

  /// 合法区间随每一轮下注变化，变化时必须把额度重置回最小加注，
  /// 否则上一轮残留的数字会被带进新一轮并被服务端拒绝。
  String get _optionsSignature {
    final model = _model;
    return '${widget.client.snapshot?.handId}|${widget.client.snapshot?.phase}'
        '|${model.minRaiseTo}|${model.maxRaiseTo}|${model.allInTo}';
  }

  int get _currentAmount {
    final model = _model;
    final value = _amount ?? model.defaultAmount;
    return model.clampAmount(value);
  }

  void _syncSignature() {
    final signature = _optionsSignature;
    if (signature == _signature) return;
    _signature = signature;
    _amount = null;
  }

  void _setAmount(int value) {
    setState(() => _amount = _model.clampAmount(value));
  }

  void _submitAggressive() {
    final model = _model;
    final amount = _currentAmount;
    widget.client.submitAction(
      model.actionFor(amount),
      raiseTo: model.raiseToFor(amount),
    );
  }

  Future<void> _editAmount() async {
    final model = _model;
    _amountController.text = '$_currentAmount';
    final entered = await showDialog<int>(
      context: context,
      builder: (context) => AlertDialog(
        title: const Text('输入额度'),
        content: SizedBox(
          width: 320,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              Text(
                model.canSizeBet
                    ? '允许范围 ${model.minRaiseTo} ～ ${model.maxRaiseTo}，'
                          '需为小盲 ${model.unit} 的整数倍；'
                          '输入 ${model.allInTo} 或更多即为全下。'
                    : '当前只能全下 ${model.allInTo}。',
                style: const TextStyle(color: Colors.white70, fontSize: 13),
              ),
              const SizedBox(height: 12),
              PlatformNumberField(
                controller: _amountController,
                autofocus: true,
                decoration: const InputDecoration(
                  labelText: '额度',
                  border: OutlineInputBorder(),
                ),
                onSubmitted: (value) =>
                    Navigator.of(context).pop(int.tryParse(value)),
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
            onPressed: () =>
                Navigator.of(context).pop(int.tryParse(_amountController.text)),
            child: const Text('确定'),
          ),
        ],
      ),
    );
    if (entered != null && mounted) _setAmount(entered);
  }

  /// 能过牌时弃牌几乎总是误触——白白放弃一手本可以免费看下去的牌。此时
  /// 先弹窗确认；不能过牌（要跟注）时弃牌是正常决策，直接提交。
  Future<void> _fold(BuildContext context, {required bool confirm}) async {
    if (!confirm) {
      widget.client.submitAction('fold');
      return;
    }
    final confirmed = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        key: const ValueKey('fold-confirm-dialog'),
        title: const Text('确定要弃牌吗？'),
        content: const Text('现在可以直接过牌，不用投入任何筹码就能看到下一张牌。'),
        actions: [
          TextButton(
            key: const ValueKey('fold-confirm-cancel'),
            onPressed: () => Navigator.pop(context, false),
            child: const Text('先不弃'),
          ),
          FilledButton(
            key: const ValueKey('fold-confirm-accept'),
            style: FilledButton.styleFrom(backgroundColor: Colors.redAccent),
            onPressed: () => Navigator.pop(context, true),
            child: const Text('弃牌'),
          ),
        ],
      ),
    );
    if (confirmed != true || !mounted) return;
    // 对话框开着的时候可能已经超时轮到别人，别把弃牌打到别人的回合上
    if (widget.client.snapshot?.currentAction?.userId != widget.userId) return;
    widget.client.submitAction('fold');
  }

  @override
  Widget build(BuildContext context) {
    _syncSignature();
    final model = _model;
    final options = widget.options;
    final busy = widget.client.actionPending || widget.blocked;
    final amount = _currentAmount;

    // 手机上按钮竖排、靠拇指操作，做得更高一些，试玩反馈原来的高度容易误触
    final buttonHeight = widget.vertical ? 56.0 : 44.0;
    final fold = _ActionButton(
      key: const ValueKey('bet-fold-action'),
      label: '弃牌',
      height: buttonHeight,
      tone: _ButtonTone.danger,
      onPressed: options.canFold && !busy
          ? () => _fold(context, confirm: options.canCheck)
          : null,
    );
    final passive = options.canCheck || options.canCall
        ? _ActionButton(
            key: const ValueKey('bet-check-call-action'),
            label: options.canCheck ? '过牌' : '跟注 ${options.toCall}',
            height: buttonHeight,
            tone: _ButtonTone.neutral,
            onPressed: busy
                ? null
                : options.canCheck
                ? () => widget.client.submitAction('check')
                : () => widget.client.submitAction('call'),
          )
        : null;
    final aggressive = model.hasAggressiveAction
        ? _ActionButton(
            key: const ValueKey('bet-aggressive-action'),
            label: model.actionLabelFor(amount),
            height: buttonHeight,
            tone: _ButtonTone.primary,
            onPressed: busy ? null : _submitAggressive,
          )
        : null;

    final sizing = model.hasAggressiveAction
        ? _SizingControls(
            model: model,
            amount: amount,
            suggestions: widget.suggestions,
            vertical: widget.vertical,
            enabled: !busy,
            onChanged: _setAmount,
            onEdit: _editAmount,
          )
        : null;

    if (widget.vertical) {
      return Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (sizing != null) ...[sizing, const SizedBox(height: 8)],
          // 弃牌放在离拇指最远的一端，降低误触代价
          fold,
          if (passive != null) ...[const SizedBox(height: 6), passive],
          if (aggressive != null) ...[const SizedBox(height: 6), aggressive],
        ],
      );
    }

    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (sizing != null) ...[sizing, const SizedBox(height: 8)],
        Row(
          children: [
            Expanded(child: fold),
            if (passive != null) ...[
              const SizedBox(width: 8),
              Expanded(child: passive),
            ],
            if (aggressive != null) ...[
              const SizedBox(width: 8),
              Expanded(flex: 2, child: aggressive),
            ],
          ],
        ),
      ],
    );
  }
}

/// 预设档、滑块与步进输入。三者共同修改同一个额度，不直接提交动作。
class _SizingControls extends StatelessWidget {
  const _SizingControls({
    required this.model,
    required this.amount,
    required this.suggestions,
    required this.vertical,
    required this.enabled,
    required this.onChanged,
    required this.onEdit,
  });

  final BetAmountModel model;
  final int amount;
  final List<BetSuggestion> suggestions;
  final bool vertical;
  final bool enabled;
  final ValueChanged<int> onChanged;
  final VoidCallback onEdit;

  @override
  Widget build(BuildContext context) {
    final presets = [
      // 全下不做成注码尺度按钮：它不是「几分之几底池」那一类的尺度，混在
      // 里面容易误触。要全下就把滑块推到最右或直接输入额度，与三个大按钮
      // 的设计一致（全下同样不单独占一个大按钮）。
      for (final suggestion in suggestions.where(
        (suggestion) => suggestion.action != 'all_in',
      ))
        _PresetChip(
          key: ValueKey('bet-preset-${suggestion.label}-${suggestion.raiseTo}'),
          label: suggestionLabel(suggestion.label),
          selected: amount == model.clampAmount(suggestion.raiseTo),
          onPressed: enabled ? () => onChanged(suggestion.raiseTo) : null,
        ),
    ];

    final stepper = Row(
      children: [
        _StepButton(
          icon: Icons.remove,
          onPressed: enabled && amount > model.sliderMin
              ? () => onChanged(model.step(amount, -1))
              : null,
        ),
        Expanded(
          child: InkWell(
            key: const ValueKey('bet-amount-value'),
            onTap: enabled ? onEdit : null,
            borderRadius: BorderRadius.circular(8),
            child: Padding(
              padding: const EdgeInsets.symmetric(vertical: 5),
              child: Text(
                model.isAllIn(amount) ? '全下 $amount' : '$amount',
                textAlign: TextAlign.center,
                style: const TextStyle(
                  fontSize: 16,
                  fontWeight: FontWeight.w800,
                  color: Color(0xFFF6D986),
                ),
              ),
            ),
          ),
        ),
        _StepButton(
          icon: Icons.add,
          onPressed: enabled && amount < model.sliderMax
              ? () => onChanged(model.step(amount, 1))
              : null,
        ),
      ],
    );

    final slider = model.hasRange
        ? SliderTheme(
            data: SliderThemeData(
              trackHeight: 3,
              overlayShape: const RoundSliderOverlayShape(overlayRadius: 14),
            ),
            child: Slider(
              key: const ValueKey('bet-amount-slider'),
              value: amount
                  .clamp(model.sliderMin, model.sliderMax)
                  .toDouble(),
              min: model.sliderMin.toDouble(),
              max: model.sliderMax.toDouble(),
              onChanged: enabled ? (value) => onChanged(value.round()) : null,
            ),
          )
        : null;

    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (presets.isNotEmpty)
          Wrap(
            spacing: 5,
            runSpacing: 5,
            alignment: WrapAlignment.center,
            children: presets,
          ),
        if (slider != null) slider else const SizedBox(height: 4),
        stepper,
        Text(
          '本次投入 ${model.commitmentFor(amount)}',
          textAlign: TextAlign.center,
          style: const TextStyle(color: Colors.white54, fontSize: 11),
        ),
      ],
    );
  }
}

enum _ButtonTone { danger, neutral, primary }

/// 动作按钮配色。
///
/// 三个可用态的颜色都必须明显亮于面板底色，否则玩家会把「弃牌」「跟注」当成
/// 不可点击的灰键——这正是上一版低饱和深色方案的问题。禁用态则单独用一个低
/// 彩度的暗色，与任何可用态都拉开差距，让"不能点"一眼可辨。
class TableActionPalette {
  const TableActionPalette._();

  /// 动作区面板底色，用作对比基准。
  static const Color panelBackground = Color(0xFF0A1C18);

  static const Color fold = Color(0xFFC4453C);
  static const Color passive = Color(0xFF2E9E7B);
  static const Color aggressive = Color(0xFFE0A83A);

  static const Color disabledBackground = Color(0xFF243530);
  static const Color disabledForeground = Color(0x66FFFFFF);

  static const List<Color> enabledTones = [fold, passive, aggressive];
}

class _ActionButton extends StatelessWidget {
  const _ActionButton({
    required this.label,
    required this.tone,
    required this.onPressed,
    this.height = 44,
    super.key,
  });

  final String label;
  final _ButtonTone tone;
  final VoidCallback? onPressed;
  final double height;

  @override
  Widget build(BuildContext context) {
    final style = FilledButton.styleFrom(
      minimumSize: Size(0, height),
      padding: const EdgeInsets.symmetric(horizontal: 10),
      backgroundColor: switch (tone) {
        _ButtonTone.danger => TableActionPalette.fold,
        _ButtonTone.neutral => TableActionPalette.passive,
        _ButtonTone.primary => TableActionPalette.aggressive,
      },
      foregroundColor: Colors.white,
      disabledBackgroundColor: TableActionPalette.disabledBackground,
      disabledForegroundColor: TableActionPalette.disabledForeground,
      textStyle: const TextStyle(fontSize: 14, fontWeight: FontWeight.w800),
    );
    return FilledButton(
      onPressed: onPressed,
      style: style,
      child: Text(label, maxLines: 1, overflow: TextOverflow.ellipsis),
    );
  }
}

class _PresetChip extends StatelessWidget {
  const _PresetChip({
    required this.label,
    required this.selected,
    required this.onPressed,
    super.key,
  });

  final String label;
  final bool selected;
  final VoidCallback? onPressed;

  @override
  Widget build(BuildContext context) => SizedBox(
    height: 26,
    child: OutlinedButton(
      onPressed: onPressed,
      style: OutlinedButton.styleFrom(
        padding: const EdgeInsets.symmetric(horizontal: 8),
        minimumSize: Size.zero,
        tapTargetSize: MaterialTapTargetSize.shrinkWrap,
        backgroundColor: selected ? const Color(0x33F6D986) : null,
        side: BorderSide(
          color: selected ? const Color(0xFFF6D986) : Colors.white24,
        ),
        textStyle: const TextStyle(fontSize: 11, fontWeight: FontWeight.w700),
      ),
      child: Text(label),
    ),
  );
}

class _StepButton extends StatelessWidget {
  const _StepButton({required this.icon, required this.onPressed});

  final IconData icon;
  final VoidCallback? onPressed;

  @override
  Widget build(BuildContext context) => IconButton(
    onPressed: onPressed,
    icon: Icon(icon, size: 18),
    visualDensity: VisualDensity.compact,
    padding: EdgeInsets.zero,
    constraints: const BoxConstraints.tightFor(width: 30, height: 30),
  );
}
