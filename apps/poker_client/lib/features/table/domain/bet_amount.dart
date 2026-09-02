import 'dart:math' as math;

import 'package:poker_client/features/table/domain/table_snapshot.dart';

/// 下注滑块的取值模型：把服务端下发的合法区间换算成一条可滑动的刻度，
/// 并决定某个金额最终应该提交为 `bet`、`raise` 还是 `all_in`。
///
/// 需要单独建模的原因是服务端的 `maxRaiseTo` 是**向下取整到小盲倍数**的
/// （`roundDownToUnit(streetBet + stack, smallBlind)`），当剩余筹码不是小盲的
/// 整数倍时，它比真正的全下额度小一点。若把滑块右端定在 `maxRaiseTo`，玩家
/// 就永远滑不出全下；若直接把 `allInTo` 当作 `raise` 提交，服务端会以
/// `invalid_amount` 拒绝。因此滑块右端是 `allInTo`，且只有该点提交 `all_in`。
class BetAmountModel {
  const BetAmountModel({
    required this.minRaiseTo,
    required this.maxRaiseTo,
    required this.allInTo,
    required this.unit,
    required this.streetBet,
    required this.canBet,
    required this.canRaise,
    required this.canAllIn,
  });

  factory BetAmountModel.fromOptions({
    required TableActionOptions options,
    required TableSeatSnapshot? ownSeat,
    required int smallBlind,
  }) {
    final streetBet = ownSeat?.streetBet ?? 0;
    final stack = ownSeat?.stack ?? 0;
    return BetAmountModel(
      minRaiseTo: options.minRaiseTo,
      maxRaiseTo: options.maxRaiseTo,
      // 与服务端 allInRaiseTo 的定义一致：本轮已投入 + 手上剩余筹码
      allInTo: streetBet + stack,
      unit: math.max(1, smallBlind),
      streetBet: streetBet,
      canBet: options.canBet,
      canRaise: options.canRaise,
      canAllIn: options.canAllIn,
    );
  }

  final int minRaiseTo;
  final int maxRaiseTo;
  final int allInTo;
  final int unit;
  final int streetBet;
  final bool canBet;
  final bool canRaise;
  final bool canAllIn;

  /// 是否存在任何主动进攻的选项（下注、加注或全下）。
  bool get hasAggressiveAction => canBet || canRaise || canAllIn;

  /// 能否按常规额度下注或加注。为 false 而 [canAllIn] 为 true 时，
  /// 玩家只剩全下一种进攻方式（例如筹码不足一个最小加注增量）。
  bool get canSizeBet => canBet || canRaise;

  int get sliderMin => canSizeBet ? minRaiseTo : allInTo;

  int get sliderMax => canAllIn ? allInTo : maxRaiseTo;

  /// 滑块是否有可滑动的区间。区间退化为一个点时不能渲染 Slider，
  /// 否则 Flutter 会因 min == max 抛错。
  bool get hasRange => sliderMax > sliderMin;

  int get defaultAmount => clampAmount(canSizeBet ? minRaiseTo : allInTo);

  /// 某个金额是否落在全下点上。滑块右端与直接输入全部筹码都会命中这里。
  bool isAllIn(int amount) => canAllIn && amount >= allInTo;

  /// 把任意输入吸附到合法刻度：全下点单独保留，其余对齐到小盲整数倍。
  ///
  /// 吸附到最近的整数倍后必须再夹到 `[minRaiseTo, maxRaiseTo]`，因为四舍五入
  /// 可能把靠近边界的值推到区间之外。
  int clampAmount(int amount) {
    if (canAllIn && amount >= allInTo) return allInTo;
    if (!canSizeBet) return canAllIn ? allInTo : amount;
    final snapped = ((amount + unit ~/ 2) ~/ unit) * unit;
    final clamped = snapped.clamp(minRaiseTo, maxRaiseTo);
    // 全下额度不是小盲整数倍时，吸附结果可能仍超过 maxRaiseTo 之上的全下点，
    // 这里只在确实达到全下时才返回全下额度。
    if (canAllIn && amount >= allInTo) return allInTo;
    return clamped;
  }

  /// 按一个小盲步进，越过上界时落到全下点。
  int step(int amount, int direction) {
    if (direction > 0 && canAllIn && amount + unit > maxRaiseTo) {
      return allInTo;
    }
    if (direction < 0 && isAllIn(amount) && canSizeBet) {
      return clampAmount(maxRaiseTo);
    }
    return clampAmount(amount + unit * direction);
  }

  /// 该金额应提交的动作名。全下必须发 `all_in`，它在服务端不受小盲整数倍限制。
  String actionFor(int amount) {
    if (isAllIn(amount)) return 'all_in';
    return canBet ? 'bet' : 'raise';
  }

  /// 该金额随动作一起提交的 `raiseTo`；全下不带金额，由服务端按剩余筹码结算。
  int? raiseToFor(int amount) => isAllIn(amount) ? null : amount;

  /// 本次还需从筹码里投入多少。
  int commitmentFor(int amount) => math.max(0, amount - streetBet);

  /// 主动动作按钮上的文案。
  String actionLabelFor(int amount) {
    if (isAllIn(amount)) return 'All in $allInTo';
    return '${canBet ? '下注' : '加注'} $amount';
  }
}
