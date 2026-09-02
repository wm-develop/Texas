import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:poker_client/features/table/domain/deal_animation.dart';
import 'package:poker_client/features/table/domain/table_snapshot.dart';

/// 正在演出的发牌类型。
enum DealKind { holeCards, board }

/// 从快照变化中识别「刚发了牌」，并驱动发牌动画。
///
/// 只在**亲眼看到**这次变化时才播放：断线重连后拿到的第一份快照里牌已经在
/// 桌上了，此时补一段发牌动画既不真实，也会平白阻塞玩家操作。因此没有上一
/// 份快照、或牌数跳变不符合发牌规律（例如同一手里公共牌从 0 直接变成 5）时，
/// 一律直接呈现终态。
class TableDealController extends ChangeNotifier {
  TableDealController({DateTime Function()? now})
    : _now = now ?? DateTime.now;

  final DateTime Function() _now;

  Timer? _ticker;
  DealAnimation? _animation;
  DealKind? _kind;
  DateTime? _startedAt;

  /// 本次发牌之前已经翻开的公共牌张数，它们保持正面不变。
  int _boardFaceUp = 0;

  /// 本次发牌的顺序（小盲在前）。在演出开始时固定下来：牌局进行中加入的
  /// 玩家是「待入座」观战者，不参与本手，因此不在这个列表里，也就不会有
  /// 发牌动画；即使他在演出途中加入，顺序也不会被打乱。
  List<String> _dealOrder = const [];

  bool _seenSnapshot = false;
  String _lastHandId = '';
  int _lastBoardCount = 0;

  /// 演出进行中。此时应当禁用行动输入，避免玩家在牌还没翻开时就下决定。
  bool get isAnimating => _animation != null;

  DealKind? get kind => _kind;

  Duration get _elapsed {
    final startedAt = _startedAt;
    if (startedAt == null) return Duration.zero;
    return _now().difference(startedAt);
  }

  /// 已经落桌（含背面）的公共牌张数。
  int get boardPlacedCards {
    final animation = _animation;
    if (animation == null || _kind != DealKind.board) return _lastBoardCount;
    return _boardFaceUp + animation.placedCards(_elapsed);
  }

  /// 保持正面的公共牌张数：本次发牌之前就在桌上的那些。
  int get boardFaceUpCards =>
      _kind == DealKind.board && _animation != null
      ? _boardFaceUp
      : _lastBoardCount;

  /// 本次新发公共牌的翻面进度。
  double get boardFlipProgress {
    final animation = _animation;
    if (animation == null || _kind != DealKind.board) return 1;
    return animation.flipProgress(_elapsed);
  }

  /// 底牌演出中已经发出的张数。
  int get holeCardsPlaced {
    final animation = _animation;
    if (animation == null || _kind != DealKind.holeCards) return 0;
    return animation.placedCards(_elapsed);
  }

  /// 公共牌演出的瞬时状态。
  BoardDealState get boardState => BoardDealState(
    placedCards: boardPlacedCards,
    faceUpCards: boardFaceUpCards,
    flipProgress: boardFlipProgress,
  );

  /// 本人底牌的翻面进度；发别人的牌不翻面，只做淡出。
  double get holeFlipProgress {
    final animation = _animation;
    if (animation == null || _kind != DealKind.holeCards) return 1;
    return animation.flipProgress(_elapsed);
  }

  /// 发牌顺序中第 [orderIndex] 位玩家（0 为小盲）此刻已经拿到几张牌。
  ///
  /// 一张一张轮流发：第一轮每人一张，第二轮再一张。
  int cardsDealtToPlayer(int orderIndex) {
    final players = _dealOrder.length;
    if (_kind != DealKind.holeCards || players <= 0 || orderIndex < 0) {
      return 2;
    }
    final placed = holeCardsPlaced;
    var cards = 0;
    for (var round = 0; round < 2; round++) {
      if (placed > round * players + orderIndex) cards++;
    }
    return cards;
  }

  /// 某位玩家此刻已经拿到几张牌。
  ///
  /// 不在本手发牌顺序里的人（牌局进行中加入的观战者）恒为 0：不该给他发牌，
  /// 也不该在他的玩家框里闪出牌背。
  int cardsDealtToUser(String userId) {
    if (_kind != DealKind.holeCards) return 2;
    final index = _dealOrder.indexOf(userId);
    if (index < 0) return 0;
    return cardsDealtToPlayer(index);
  }

  /// 按小盲起始的发牌顺序排列本手的参与者。
  static List<String> dealOrderFor(TableSnapshot snapshot) {
    final players =
        snapshot.seats.where((seat) => seat.participating).toList()
          ..sort((left, right) => left.seat.compareTo(right.seat));
    if (players.isEmpty) return const [];
    var start = players.indexWhere(
      (seat) => seat.seat == snapshot.smallBlindSeat,
    );
    if (start < 0) start = 0;
    return [
      for (var offset = 0; offset < players.length; offset++)
        players[(start + offset) % players.length].userId,
    ];
  }

  /// 处理一份新快照。返回是否因此开始了一段新的演出。
  bool observe(TableSnapshot? snapshot) {
    if (snapshot == null) return false;
    final handId = snapshot.handId;
    final boardCount = snapshot.board.length;

    if (!_seenSnapshot) {
      _seenSnapshot = true;
      _lastHandId = handId;
      _lastBoardCount = boardCount;
      return false;
    }

    var started = false;
    if (handId.isNotEmpty && handId != _lastHandId) {
      // 新的一手：发底牌。此时公共牌一定是空的。
      final order = dealOrderFor(snapshot);
      _start(
        DealAnimation.holeCards(playerCount: order.length),
        DealKind.holeCards,
        faceUp: 0,
        dealOrder: order,
      );
      started = true;
    } else if (handId == _lastHandId && boardCount > _lastBoardCount) {
      final dealt = boardCount - _lastBoardCount;
      // 只有符合发牌规律的增量才演出：翻牌一次 3 张，转牌与河牌各 1 张。
      // 其余情况（重连补齐、全下一次性发完）直接呈现终态。
      final expected =
          (_lastBoardCount == 0 && dealt == 3) ||
          (_lastBoardCount >= 3 && dealt == 1);
      if (expected) {
        _start(
          DealAnimation.board(newCards: dealt),
          DealKind.board,
          faceUp: _lastBoardCount,
          dealOrder: const [],
        );
        started = true;
      } else {
        _stop();
      }
    }

    _lastHandId = handId;
    _lastBoardCount = boardCount;
    if (!started) notifyListeners();
    return started;
  }

  void _start(
    DealAnimation animation,
    DealKind kind, {
    required int faceUp,
    required List<String> dealOrder,
  }) {
    _ticker?.cancel();
    _animation = animation;
    _kind = kind;
    _boardFaceUp = faceUp;
    _dealOrder = dealOrder;
    _startedAt = _now();
    _ticker = Timer.periodic(const Duration(milliseconds: 16), (_) {
      if (animation.isComplete(_elapsed)) {
        _stop();
        return;
      }
      notifyListeners();
    });
    notifyListeners();
  }

  void _stop() {
    _ticker?.cancel();
    _ticker = null;
    _animation = null;
    _kind = null;
    _startedAt = null;
    notifyListeners();
  }

  /// 供测试推进时间后立即结算状态，无需等待真实定时器。
  @visibleForTesting
  void debugSettle() {
    final animation = _animation;
    if (animation != null && animation.isComplete(_elapsed)) _stop();
  }

  @override
  void dispose() {
    _ticker?.cancel();
    super.dispose();
  }
}

/// 公共牌演出的瞬时状态，交给展示层渲染。
class BoardDealState {
  const BoardDealState({
    required this.placedCards,
    required this.faceUpCards,
    required this.flipProgress,
  });

  /// 没有演出时的状态：快照里有多少张就照常显示多少张正面。
  const BoardDealState.settled()
    : placedCards = 5,
      faceUpCards = 5,
      flipProgress = 1;

  /// 已经落桌（含背面）的张数。
  final int placedCards;

  /// 保持正面的张数，即本次发牌之前就在桌上的那些。
  final int faceUpCards;

  /// 本次新发的牌的翻面进度。
  final double flipProgress;
}
