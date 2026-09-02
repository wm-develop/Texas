import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/features/table/domain/bet_amount.dart';

BetAmountModel model({
  int minRaiseTo = 40,
  int maxRaiseTo = 260,
  int allInTo = 265,
  int unit = 10,
  int streetBet = 0,
  bool canBet = false,
  bool canRaise = true,
  bool canAllIn = true,
}) => BetAmountModel(
  minRaiseTo: minRaiseTo,
  maxRaiseTo: maxRaiseTo,
  allInTo: allInTo,
  unit: unit,
  streetBet: streetBet,
  canBet: canBet,
  canRaise: canRaise,
  canAllIn: canAllIn,
);

void main() {
  group('滑块区间', () {
    test('右端是真正的全下额度，而不是向下取整的 maxRaiseTo', () {
      // 服务端 maxRaiseTo = roundDownToUnit(265, 10) = 260，但全下是 265。
      // 若右端取 260，玩家永远滑不到全下。
      final bet = model();
      expect(bet.sliderMin, 40);
      expect(bet.sliderMax, 265);
      expect(bet.hasRange, isTrue);
    });

    test('不能全下时右端退回 maxRaiseTo', () {
      final bet = model(canAllIn: false);
      expect(bet.sliderMax, 260);
    });

    test('只能全下时区间退化为一个点，不渲染滑块', () {
      // 筹码不足一个最小加注增量：canRaise/canBet 均为 false
      final bet = model(canRaise: false, canBet: false);
      expect(bet.canSizeBet, isFalse);
      expect(bet.hasAggressiveAction, isTrue);
      expect(bet.sliderMin, 265);
      expect(bet.sliderMax, 265);
      expect(bet.hasRange, isFalse);
      expect(bet.defaultAmount, 265);
    });

    test('毫无进攻选项时不显示第三个按钮', () {
      final bet = model(canRaise: false, canBet: false, canAllIn: false);
      expect(bet.hasAggressiveAction, isFalse);
    });
  });

  group('金额吸附', () {
    test('对齐到小盲整数倍并夹在合法区间内', () {
      final bet = model();
      expect(bet.clampAmount(43), 40);
      expect(bet.clampAmount(46), 50);
      expect(bet.clampAmount(5), 40, reason: '低于最小加注要抬到下界');
      expect(bet.clampAmount(1000), 265, reason: '超过上界落到全下');
    });

    test('四舍五入不会把值推出上界', () {
      // 258 就近对齐是 260，正好等于 maxRaiseTo，不能变成 270
      final bet = model(canAllIn: false);
      expect(bet.clampAmount(258), 260);
      expect(bet.clampAmount(264), 260);
    });

    test('达到或超过全下额度时精确返回全下额度', () {
      final bet = model();
      expect(bet.clampAmount(265), 265);
      expect(bet.clampAmount(300), 265);
      expect(bet.isAllIn(265), isTrue);
      expect(bet.isAllIn(260), isFalse);
    });
  });

  group('步进', () {
    test('向上越过上界时落到全下点，而不是停在 maxRaiseTo', () {
      final bet = model();
      expect(bet.step(250, 1), 260);
      expect(bet.step(260, 1), 265, reason: '再加一档就是全下');
      expect(bet.step(265, 1), 265, reason: '已全下不再增加');
    });

    test('从全下点向下回到最大常规额度', () {
      final bet = model();
      expect(bet.step(265, -1), 260);
      expect(bet.step(50, -1), 40);
      expect(bet.step(40, -1), 40, reason: '不低于最小加注');
    });
  });

  group('提交的动作', () {
    test('全下提交 all_in 且不带 raiseTo', () {
      // 服务端对 all_in 不校验小盲整数倍；若按 raise 提交 265 会被拒。
      final bet = model();
      expect(bet.actionFor(265), 'all_in');
      expect(bet.raiseToFor(265), isNull);
      expect(bet.actionLabelFor(265), 'All in 265');
    });

    test('常规额度按 bet 或 raise 提交并带 raiseTo', () {
      final raise = model();
      expect(raise.actionFor(120), 'raise');
      expect(raise.raiseToFor(120), 120);
      expect(raise.actionLabelFor(120), '加注 120');

      final open = model(canBet: true, canRaise: false);
      expect(open.actionFor(120), 'bet');
      expect(open.actionLabelFor(120), '下注 120');
    });

    test('本次还需投入扣除本轮已投入部分', () {
      final bet = model(streetBet: 40);
      expect(bet.commitmentFor(120), 80);
      expect(bet.commitmentFor(20), 0, reason: '不出现负数');
    });
  });

  test('全下额度等于本轮已投入加剩余筹码', () {
    final bet = BetAmountModel(
      minRaiseTo: 40,
      maxRaiseTo: 200,
      allInTo: 40 + 165,
      unit: 10,
      streetBet: 40,
      canBet: false,
      canRaise: true,
      canAllIn: true,
    );
    expect(bet.allInTo, 205);
    expect(bet.commitmentFor(bet.allInTo), 165, reason: '全下投入等于剩余筹码');
  });
}
