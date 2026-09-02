import 'package:flutter/material.dart';
import 'package:poker_client/core/network/game_socket_client.dart';
import 'package:poker_client/features/table/domain/table_seat.dart';
import 'package:poker_client/features/table/domain/table_snapshot.dart';
import 'package:poker_client/features/table/presentation/table_seat_widgets.dart';
import 'package:poker_client/features/table/presentation/table_card_widgets.dart';
import 'package:poker_client/features/table/presentation/table_board_center.dart';
import 'package:poker_client/features/table/presentation/table_deal_controller.dart';
import 'package:poker_client/features/table/presentation/table_viewport_layout.dart';

/// 牌桌画布：按响应式布局摆放座位，并把公共牌区域嵌入桌面中央。

class TableCanvas extends StatelessWidget {
  const TableCanvas({
    required this.seats,
    required this.alignments,
    required this.boardRect,
    required this.snapshot,
    required this.actionRemaining,
    required this.onSeatTap,
    required this.onAvatarTap,
    required this.onUseTimeExtension,
    required this.interactions,
    this.dealState = const BoardDealState.settled(),
    super.key,
  });

  final List<TableSeat> seats;
  final List<Alignment> alignments;
  final Rect boardRect;
  final TableSnapshot? snapshot;
  final Duration actionRemaining;
  final ValueChanged<TableSeat> onSeatTap;
  final ValueChanged<TableSeat> onAvatarTap;
  final VoidCallback onUseTimeExtension;
  final List<TablePlayerInteraction> interactions;

  /// 发牌演出的瞬时状态。
  final BoardDealState dealState;

  @override
  Widget build(BuildContext context) {
    final winnerAmounts = <String, int>{};
    for (final award in snapshot?.settlement?.potAwards ?? const <PotAward>[]) {
      for (final payout in award.payouts) {
        winnerAmounts.update(
          payout.userId,
          (amount) => amount + payout.amount,
          ifAbsent: () => payout.amount,
        );
      }
    }
    final showReadyStatus =
        snapshot?.phase == 'WAITING' || snapshot?.phase == 'WAITING_NEXT_HAND';
    return Stack(
      clipBehavior: Clip.none,
      children: [
        // 毛毡的内缩必须与 TableViewportLayout.feltInsets 一致：座位落位与
        // 公共牌区都以桌沿为基准，两处各写一份就会重演「玩家框整个落在桌外」。
        Positioned.fill(
          left: TableViewportLayout.feltInsets.left,
          right: TableViewportLayout.feltInsets.right,
          top: TableViewportLayout.feltInsets.top,
          bottom: TableViewportLayout.feltInsets.bottom,
          child: DecoratedBox(
            decoration: BoxDecoration(
              color: const Color(0xFF126344),
              borderRadius: BorderRadius.circular(240),
              border: Border.all(color: const Color(0xFF9B7838), width: 12),
              boxShadow: const [
                BoxShadow(
                  color: Colors.black54,
                  blurRadius: 30,
                  offset: Offset(0, 14),
                ),
                BoxShadow(color: Color(0xFF253C30), spreadRadius: 5),
              ],
            ),
          ),
        ),
        Positioned.fromRect(
          rect: boardRect,
          child: IgnorePointer(
            child: TableBoardCenter(
              snapshot: snapshot,
              actionRemaining: actionRemaining,
              dealState: dealState,
            ),
          ),
        ),
        for (var index = 0; index < seats.length; index++)
          Align(
            alignment: alignments[index],
            child: ClipRRect(
              borderRadius: BorderRadius.circular(13),
              child: GestureDetector(
                behavior: HitTestBehavior.opaque,
                onTap: () => onSeatTap(seats[index]),
                child: TableSeatCard(
                  seat: seats[index],
                  actionRemaining: actionRemaining,
                  showReadyStatus: showReadyStatus,
                  winnerAmount: winnerAmounts[seats[index].userId] ?? 0,
                  onAvatarTap: () => onAvatarTap(seats[index]),
                  onUseTimeExtension: onUseTimeExtension,
                ),
              ),
            ),
          ),
        for (var index = 0; index < seats.length; index++)
          if (seats[index].streetBet > 0)
            Align(
              alignment: Alignment(
                alignments[index].x * 0.54,
                alignments[index].y * 0.58,
              ),
              child: TableBetChip(amount: seats[index].streetBet),
            ),
        for (final interaction in interactions)
          if (seats.any((seat) => seat.userId == interaction.targetUserId))
            Align(
              alignment:
                  alignments[seats.indexWhere(
                    (seat) => seat.userId == interaction.targetUserId,
                  )],
              child: IgnorePointer(
                child: TablePlayerInteractionBurst(interaction: interaction),
              ),
            ),
      ],
    );
  }
}
