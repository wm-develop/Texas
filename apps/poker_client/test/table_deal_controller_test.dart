import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/features/table/domain/table_snapshot.dart';
import 'package:poker_client/features/table/presentation/table_deal_controller.dart';

TableSnapshot _snapshot({
  String handId = 'hand_1',
  List<String> board = const [],
  int players = 2,
  int smallBlindSeat = 1,
  List<String> spectators = const [],
}) => TableSnapshot.fromJson({
  'roomId': 'room_1',
  'roomCode': '123456',
  'tableRevision': 1,
  'phase': board.isEmpty ? 'PREFLOP' : 'FLOP',
  'handId': handId,
  'board': board,
  'smallBlindSeat': smallBlindSeat,
  'seats': [
    for (var index = 0; index < players; index++)
      {
        'userId': 'user_$index',
        'displayName': '玩家$index',
        'seat': index + 1,
        'stack': 1000,
        'ready': true,
        'connected': true,
        'participating': true,
        'folded': false,
        'allIn': false,
        'streetBet': 0,
        'totalBet': 0,
        'position': '',
        'lastAction': '',
        'lastCommitted': 0,
        'lastActionTo': 0,
        'timeExtensions': 2,
      },
    // 牌局进行中加入的玩家：待入座观战，不参与本手
    for (var index = 0; index < spectators.length; index++)
      {
        'userId': spectators[index],
        'displayName': spectators[index],
        'seat': 90 + index,
        'stack': 1000,
        'ready': false,
        'connected': true,
        'participating': false,
        'folded': false,
        'allIn': false,
        'streetBet': 0,
        'totalBet': 0,
        'position': '',
        'lastAction': '',
        'lastCommitted': 0,
        'lastActionTo': 0,
        'timeExtensions': 0,
      },
  ],
});

/// 可控时钟，避免测试依赖真实定时器。
class _Clock {
  DateTime value = DateTime.utc(2026, 9, 2, 12);
  DateTime now() => value;
  void advance(Duration duration) => value = value.add(duration);
}

void main() {
  test('第一份快照不播放动画，只记录状态', () {
    final controller = TableDealController();
    // 断线重连后拿到的第一份快照里牌已经在桌上，补一段发牌既不真实
    // 又会平白阻塞操作
    expect(
      controller.observe(_snapshot(board: const ['As', 'Kd', 'Qh'])),
      isFalse,
    );
    expect(controller.isAnimating, isFalse);
    controller.dispose();
  });

  test('新的一手触发底牌演出，逐张发给每位玩家', () {
    final clock = _Clock();
    final controller = TableDealController(now: clock.now);
    controller.observe(_snapshot(handId: 'hand_1'));

    expect(controller.observe(_snapshot(handId: 'hand_2')), isTrue);
    expect(controller.isAnimating, isTrue);
    expect(controller.kind, DealKind.holeCards);

    // 两人桌共 4 张，每张 160 毫秒；先每人一张，再发第二轮
    expect(controller.cardsDealtToPlayer(0), 1);
    expect(controller.cardsDealtToPlayer(1), 0);
    clock.advance(const Duration(milliseconds: 170));
    expect(controller.cardsDealtToPlayer(1), 1);
    clock.advance(const Duration(milliseconds: 340));
    expect(controller.cardsDealtToPlayer(0), 2);
    expect(controller.cardsDealtToPlayer(1), 2);

    // 落桌后停顿再翻开本人底牌
    expect(controller.holeFlipProgress, 0);
    clock.advance(const Duration(milliseconds: 900));
    expect(controller.holeFlipProgress, 1);
    controller.debugSettle();
    expect(controller.isAnimating, isFalse);
    controller.dispose();
  });

  test('翻牌逐张落桌，之前的公共牌保持正面', () {
    final clock = _Clock();
    final controller = TableDealController(now: clock.now);
    controller.observe(_snapshot(handId: 'hand_1'));

    expect(
      controller.observe(
        _snapshot(handId: 'hand_1', board: const ['As', 'Kd', 'Qh']),
      ),
      isTrue,
    );
    expect(controller.kind, DealKind.board);
    expect(controller.boardFaceUpCards, 0);
    expect(controller.boardPlacedCards, 1);
    clock.advance(const Duration(milliseconds: 380));
    expect(controller.boardPlacedCards, 3);
    expect(controller.boardFlipProgress, 0, reason: '落桌阶段不能提前翻面');
    // 翻牌总时长 3*180 + 220 + 420 = 1180 毫秒
    clock.advance(const Duration(milliseconds: 800));
    expect(controller.boardFlipProgress, 1);
    controller.debugSettle();

    // 转牌只发一张，前三张保持正面
    expect(
      controller.observe(
        _snapshot(handId: 'hand_1', board: const ['As', 'Kd', 'Qh', 'Jc']),
      ),
      isTrue,
    );
    expect(controller.boardFaceUpCards, 3);
    expect(controller.boardPlacedCards, 4);
    controller.dispose();
  });

  test('全下后一次性发完的公共牌同样逐张演出', () {
    final clock = _Clock();
    final controller = TableDealController(now: clock.now);
    controller.observe(_snapshot(handId: 'hand_1'));
    // 服务端在全下后一次性发完剩余公共牌，客户端仍按一张一张发来呈现
    expect(
      controller.observe(
        _snapshot(
          handId: 'hand_1',
          board: const ['As', 'Kd', 'Qh', 'Jc', 'Ts'],
        ),
      ),
      isTrue,
    );
    expect(controller.isAnimating, isTrue);
    expect(controller.boardPlacedCards, 1, reason: '第一张先落桌');
    clock.advance(const Duration(milliseconds: 740));
    expect(controller.boardPlacedCards, 5);
    controller.dispose();
  });

  test('十人桌底牌演出总时长控制在两秒出头', () {
    final clock = _Clock();
    final controller = TableDealController(now: clock.now);
    controller.observe(_snapshot(handId: 'hand_1', players: 10));
    controller.observe(_snapshot(handId: 'hand_2', players: 10));

    expect(controller.isAnimating, isTrue);
    clock.advance(const Duration(milliseconds: 2200));
    controller.debugSettle();
    expect(controller.isAnimating, isFalse);
    controller.dispose();
  });
  test('发牌顺序从小盲开始，中途加入的观战者不发牌', () {
    final clock = _Clock();
    final controller = TableDealController(now: clock.now);
    controller.observe(_snapshot(handId: 'hand_1', players: 3));
    controller.observe(
      _snapshot(handId: 'hand_2', players: 3, smallBlindSeat: 2),
    );

    // 座位 2 是小盲，发牌从它开始
    expect(controller.cardsDealtToUser('user_1'), 1);
    expect(controller.cardsDealtToUser('user_2'), 0);
    expect(controller.cardsDealtToUser('user_0'), 0);
    clock.advance(const Duration(milliseconds: 250));
    expect(controller.cardsDealtToUser('user_2'), 1);
  });

  test('发牌途中加入的玩家不会闪出牌背', () {
    final clock = _Clock();
    final controller = TableDealController(now: clock.now);
    controller.observe(_snapshot(handId: 'hand_1'));
    controller.observe(_snapshot(handId: 'hand_2'));
    expect(controller.cardsDealtToUser('user_0'), 1);

    // 演出进行到一半时有人加入，成为待入座观战者
    controller.observe(
      _snapshot(handId: 'hand_2', spectators: const ['latecomer']),
    );
    expect(
      controller.cardsDealtToUser('latecomer'),
      0,
      reason: '不参与本手的人不该出现发牌动画',
    );
    // 原有玩家的发牌顺序不受影响
    clock.advance(const Duration(milliseconds: 700));
    expect(controller.cardsDealtToUser('user_0'), 2);
    expect(controller.cardsDealtToUser('user_1'), 2);
    controller.dispose();
  });
}
