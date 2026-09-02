import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/features/table/domain/deal_animation.dart';

void main() {
  group('公共牌发牌时序', () {
    final flop = DealAnimation.board(newCards: 3);

    test('逐张落桌，落完停顿后才翻开', () {
      expect(flop.cardCount, 3);
      expect(flop.placedCards(Duration.zero), 1);
      expect(flop.placedCards(const Duration(milliseconds: 180)), 2);
      expect(flop.placedCards(const Duration(milliseconds: 360)), 3);
      expect(
        flop.placedCards(const Duration(milliseconds: 900)),
        3,
        reason: '张数不会超过本次发出的牌数',
      );

      // 落桌阶段结束前不能开始翻牌
      expect(flop.flipProgress(const Duration(milliseconds: 500)), 0);
      expect(flop.flipProgress(flop.flipStart), 0);
      expect(flop.flipProgress(flop.total), 1);
      expect(flop.isComplete(flop.total), isTrue);
    });

    test('翻牌进度在停顿之后线性推进', () {
      final middle = flop.flipStart + const Duration(milliseconds: 210);
      expect(flop.flipProgress(middle), closeTo(0.5, 0.01));
    });

    test('转牌与河牌只发一张', () {
      final turn = DealAnimation.board(newCards: 1);
      expect(turn.cardCount, 1);
      expect(turn.placedCards(Duration.zero), 1);
      expect(turn.total.inMilliseconds, 180 + 220 + 420);
    });
  });

  group('底牌发牌时序', () {
    test('每人两张，从小盲开始逐张发', () {
      final headsUp = DealAnimation.holeCards(playerCount: 2);
      expect(headsUp.cardCount, 4);
      expect(headsUp.placedCards(Duration.zero), 1);
      expect(headsUp.placedCards(headsUp.dealEnd), 4);
    });

    test('人数越多每张越快，总时长不至于失控', () {
      final headsUp = DealAnimation.holeCards(playerCount: 2);
      final full = DealAnimation.holeCards(playerCount: 10);

      expect(full.cardCount, 20);
      expect(
        full.dealInterval.inMilliseconds,
        lessThan(headsUp.dealInterval.inMilliseconds),
      );
      // 演出期间服务端倒计时并不暂停，10 人桌也必须控制在两秒出头
      expect(full.total.inMilliseconds, lessThan(2200));
      // 但两人桌不能快到看不清
      expect(headsUp.dealInterval.inMilliseconds, greaterThanOrEqualTo(140));
    });

    test('间隔有上下界，不随人数无限变化', () {
      final single = DealAnimation.holeCards(playerCount: 1);
      expect(single.dealInterval.inMilliseconds, 160);
      final huge = DealAnimation.holeCards(playerCount: 50);
      expect(huge.dealInterval.inMilliseconds, 70);
    });
  });

  test('时间倒流或为负时不会算出负张数', () {
    final flop = DealAnimation.board(newCards: 3);
    expect(flop.placedCards(const Duration(milliseconds: -50)), 0);
    expect(flop.flipProgress(const Duration(milliseconds: -50)), 0);
  });
}
