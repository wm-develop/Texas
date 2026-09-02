import 'dart:math' as math;

/// 一次发牌演出的时序模型。
///
/// 发牌一律「逐张背面落桌 → 停顿 → 一起翻开」。把时序单独建模是为了能直接
/// 单测，也为了让公共牌与底牌共用同一套节奏，两处不会各调各的。
///
/// 演出期间客户端会阻塞行动输入，而服务端的行动倒计时并不会因此暂停，
/// 所以总时长必须克制：本类把发牌阶段压在 1.4 秒以内，人数越多每张越快。
class DealAnimation {
  const DealAnimation({
    required this.cardCount,
    required this.dealInterval,
    required this.settlePause,
    required this.flipDuration,
  });

  /// 公共牌：翻牌 3 张，转牌与河牌各 1 张。
  factory DealAnimation.board({required int newCards}) => DealAnimation(
    cardCount: math.max(1, newCards),
    dealInterval: const Duration(milliseconds: 180),
    settlePause: const Duration(milliseconds: 220),
    flipDuration: const Duration(milliseconds: 420),
  );

  /// 底牌：每人两张，从小盲开始一张一张轮流发。
  ///
  /// 每张牌的间隔随人数收缩，使 10 人桌不会拖成三秒多；
  /// 但保留下限，两人桌也不会快到看不清。
  factory DealAnimation.holeCards({required int playerCount}) {
    final cards = math.max(1, playerCount) * 2;
    final interval = (1400 / cards).clamp(70, 160).round();
    return DealAnimation(
      cardCount: cards,
      dealInterval: Duration(milliseconds: interval),
      settlePause: const Duration(milliseconds: 200),
      flipDuration: const Duration(milliseconds: 420),
    );
  }

  final int cardCount;

  /// 相邻两张牌落桌的间隔。
  final Duration dealInterval;

  /// 最后一张落桌后、开始翻牌前的停顿。
  final Duration settlePause;

  /// 翻牌动画本身的时长。
  final Duration flipDuration;

  /// 全部牌落桌完成的时刻。
  Duration get dealEnd => dealInterval * cardCount;

  Duration get flipStart => dealEnd + settlePause;

  Duration get total => flipStart + flipDuration;

  /// 到 [elapsed] 时已经落桌的张数。第一张在 0 时刻即落桌。
  int placedCards(Duration elapsed) {
    if (elapsed < Duration.zero) return 0;
    final placed = elapsed.inMicroseconds ~/ dealInterval.inMicroseconds + 1;
    return placed.clamp(0, cardCount);
  }

  /// 翻牌进度，0 为仍是背面，1 为完全翻开。
  double flipProgress(Duration elapsed) {
    final since = elapsed - flipStart;
    if (since <= Duration.zero) return 0;
    if (since >= flipDuration) return 1;
    return since.inMicroseconds / flipDuration.inMicroseconds;
  }

  bool isComplete(Duration elapsed) => elapsed >= total;
}
