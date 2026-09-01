import 'dart:async';

import 'package:flutter/material.dart';
import 'package:poker_client/features/table/domain/runout_boards.dart';
import 'package:poker_client/features/table/domain/table_snapshot.dart';
import 'package:poker_client/features/table/presentation/table_labels.dart';
import 'package:poker_client/features/table/presentation/table_card_widgets.dart';

/// 牌桌中央的公共牌区域，含发两次时的分阶段叠牌展示。

class TableBoardCenter extends StatefulWidget {
  const TableBoardCenter({
    required this.snapshot,
    required this.actionRemaining,
    super.key,
  });

  final TableSnapshot? snapshot;
  final Duration actionRemaining;

  @override
  State<TableBoardCenter> createState() => TableBoardCenterState();
}

enum TableRunoutStage { firstBoard, stackedBoards }

class TableBoardCenterState extends State<TableBoardCenter> {
  static const _firstBoardHold = Duration(seconds: 5);

  Timer? _stageTimer;
  String _animatedHandId = '';
  TableRunoutStage _stage = TableRunoutStage.firstBoard;

  @override
  void didUpdateWidget(covariant TableBoardCenter oldWidget) {
    super.didUpdateWidget(oldWidget);
    _syncRunoutStage();
  }

  @override
  void initState() {
    super.initState();
    _syncRunoutStage();
  }

  @override
  void dispose() {
    _stageTimer?.cancel();
    super.dispose();
  }

  void _syncRunoutStage() {
    final settlement = widget.snapshot?.settlement;
    if (settlement == null || settlement.runoutBoards.length != 2) {
      _stageTimer?.cancel();
      _stageTimer = null;
      _animatedHandId = '';
      _stage = TableRunoutStage.firstBoard;
      return;
    }
    if (settlement.handId == _animatedHandId) return;
    _animatedHandId = settlement.handId;
    _stage = TableRunoutStage.firstBoard;
    _stageTimer?.cancel();
    _stageTimer = Timer(_firstBoardHold, () {
      if (!mounted) return;
      setState(() => _stage = TableRunoutStage.stackedBoards);
    });
  }

  /// 发两次的展示：先完整展示第一块牌面 5 秒；之后本次发出的牌（公共
  /// 前缀之外的部分）收起为顶部点数条，第二次的牌叠放在其下覆盖主体，
  /// 两次结果同时可读。已在场的公共牌全程保持原样。
  List<Widget> _runoutBoardRow(List<List<String>> runoutBoards) {
    final sharedPrefix = sharedRunoutPrefixLength(runoutBoards);
    final stacked = _stage == TableRunoutStage.stackedBoards;
    return [
      Padding(
        padding: const EdgeInsets.only(bottom: 2),
        child: Text(
          stacked ? '第1次(上) / 第2次(下)' : '第1次',
          style: const TextStyle(
            color: Color(0xFFF6D986),
            fontSize: 12,
            fontWeight: FontWeight.w700,
          ),
        ),
      ),
      Row(
        mainAxisAlignment: MainAxisAlignment.center,
        crossAxisAlignment: CrossAxisAlignment.end,
        children: List.generate(5, (index) {
          String? cardAt(List<String> board) =>
              index < board.length ? board[index] : null;
          final firstCard = cardAt(runoutBoards[0]);
          if (index < sharedPrefix || !stacked) {
            return TablePlayingCard(
              rank: firstCard == null ? '?' : cardRank(firstCard),
              suit: firstCard == null ? '' : cardSuit(firstCard),
              red:
                  firstCard != null &&
                  (firstCard.endsWith('h') || firstCard.endsWith('d')),
            );
          }
          final secondCard = cardAt(runoutBoards[1]);
          return Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              if (firstCard != null) TableFirstRunoutTab(card: firstCard),
              TablePlayingCard(
                rank: secondCard == null ? '?' : cardRank(secondCard),
                suit: secondCard == null ? '' : cardSuit(secondCard),
                red:
                    secondCard != null &&
                    (secondCard.endsWith('h') || secondCard.endsWith('d')),
              ),
            ],
          );
        }),
      ),
    ];
  }

  @override
  Widget build(BuildContext context) {
    final snapshot = widget.snapshot;
    final actionRemaining = widget.actionRemaining;
    final board = snapshot?.board ?? const <String>[];
    final runoutBoards =
        snapshot?.settlement?.runoutBoards ?? const <List<String>>[];
    final actor = snapshot?.currentAction;
    final actorName = snapshot?.seats
        .where((seat) => seat.userId == actor?.userId)
        .map((seat) => seat.displayName)
        .firstOrNull;
    return LayoutBuilder(
      builder: (context, constraints) => Center(
        child: FittedBox(
          fit: BoxFit.scaleDown,
          child: Container(
            width: 430,
            padding: const EdgeInsets.symmetric(horizontal: 18, vertical: 12),
            decoration: BoxDecoration(
              color: const Color(0xF2126344),
              borderRadius: BorderRadius.circular(24),
              border: Border.all(color: const Color(0x449B7838)),
              boxShadow: const [
                BoxShadow(
                  color: Color(0xCC126344),
                  blurRadius: 22,
                  spreadRadius: 14,
                ),
              ],
            ),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              mainAxisAlignment: MainAxisAlignment.center,
              children: [
                AnimatedSwitcher(
                  duration: const Duration(milliseconds: 220),
                  transitionBuilder: (child, animation) => ScaleTransition(
                    scale: Tween<double>(
                      begin: 0.88,
                      end: 1,
                    ).animate(animation),
                    child: FadeTransition(opacity: animation, child: child),
                  ),
                  child: Text(
                    '底池  ${snapshot?.totalPot ?? 0}',
                    key: ValueKey(snapshot?.totalPot ?? 0),
                    style: const TextStyle(
                      color: Color(0xFFF6D986),
                      fontSize: 18,
                    ),
                  ),
                ),
                if (snapshot != null && snapshot.settlement != null) ...[
                  const SizedBox(height: 6),
                  for (final award in snapshot.settlement!.potAwards)
                    Text(
                      potAwardLabel(award, snapshot.seats),
                      style: const TextStyle(
                        color: Color(0xFFF6D986),
                        fontSize: 12,
                        fontWeight: FontWeight.w700,
                      ),
                    ),
                  const SizedBox(height: 5),
                  Text(
                    phaseLabel(snapshot.phase),
                    style: const TextStyle(color: Colors.white70),
                  ),
                ],
                SizedBox(height: snapshot?.settlement == null ? 16 : 10),
                if (runoutBoards.length == 2)
                  ..._runoutBoardRow(runoutBoards)
                else
                  Row(
                    mainAxisAlignment: MainAxisAlignment.center,
                    children: List.generate(5, (index) {
                      final card = index < board.length ? board[index] : null;
                      return TablePlayingCard(
                        rank: card == null ? '?' : cardRank(card),
                        suit: card == null ? '' : cardSuit(card),
                        red:
                            card != null &&
                            (card.endsWith('h') || card.endsWith('d')),
                      );
                    }),
                  ),
                if (snapshot?.settlement == null) ...[
                  const SizedBox(height: 18),
                  Text(
                    actorName == null
                        ? phaseLabel(snapshot?.phase)
                        : '轮到 $actorName 行动 · ${remainingSeconds(actionRemaining)} 秒',
                    style: const TextStyle(color: Colors.white70),
                  ),
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }
}

class TableFirstRunoutTab extends StatelessWidget {
  const TableFirstRunoutTab({required this.card, super.key});

  final String card;

  @override
  Widget build(BuildContext context) {
    final red = card.endsWith('h') || card.endsWith('d');
    return TweenAnimationBuilder<double>(
      tween: Tween(begin: 0, end: 1),
      duration: const Duration(milliseconds: 400),
      curve: Curves.easeOut,
      builder: (context, value, child) => Opacity(opacity: value, child: child),
      child: Container(
        width: 58,
        height: 24,
        margin: const EdgeInsets.symmetric(horizontal: 4),
        alignment: Alignment.center,
        decoration: BoxDecoration(
          color: const Color(0xFFE8E2D3),
          borderRadius: const BorderRadius.vertical(top: Radius.circular(8)),
          border: Border.all(color: Colors.white30),
        ),
        child: Text(
          '${cardRank(card)} ${cardSuit(card)}',
          style: TextStyle(
            height: 1,
            color: red ? const Color(0xFFC63D45) : Colors.black87,
            fontSize: 13,
            fontWeight: FontWeight.w700,
          ),
        ),
      ),
    );
  }
}
