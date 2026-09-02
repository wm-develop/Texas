import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:poker_client/core/network/game_socket_client.dart';
import 'package:poker_client/features/table/domain/hand_category_label.dart';
import 'package:poker_client/features/table/domain/table_seat.dart';
import 'package:poker_client/features/table/presentation/table_labels.dart';
import 'package:poker_client/features/table/presentation/table_card_widgets.dart';
import 'package:poker_client/features/table/presentation/table_deal_controller.dart';

/// 玩家框及其内部区域：本人手牌、摊牌结果与互动气泡。
/// 图层约束见项目交接文档：玩家框高于公共牌，互动气泡位于最上层。

class TableSeatCard extends StatelessWidget {
  const TableSeatCard({
    required this.seat,
    required this.actionRemaining,
    required this.showReadyStatus,
    required this.winnerAmount,
    required this.onAvatarTap,
    required this.onUseTimeExtension,
    this.deal = const SeatDealState.settled(),
    super.key,
  });

  final TableSeat seat;
  final Duration actionRemaining;
  final bool showReadyStatus;
  final int winnerAmount;
  final VoidCallback onAvatarTap;
  final VoidCallback onUseTimeExtension;

  /// 本座位的发牌演出状态。
  final SeatDealState deal;

  @override
  Widget build(BuildContext context) {
    final borderColor = winnerAmount > 0
        ? const Color(0xFFFFD54F)
        : seat.isCurrentActor
        ? const Color(0xFFFFA94D)
        : seat.isCurrentUser
        ? const Color(0xFFF4D477)
        : seat.isSpeaking
        ? const Color(0xFF6DE0A4)
        : Colors.white24;

    return AnimatedContainer(
      duration: const Duration(milliseconds: 240),
      curve: Curves.easeOut,
      width: 216,
      height: 116,
      padding: const EdgeInsets.symmetric(horizontal: 11, vertical: 8),
      decoration: BoxDecoration(
        color: seat.isEmpty
            ? const Color(0x80112622)
            : seat.isFolded
            ? const Color(0xFA070B0A)
            : winnerAmount > 0
            ? const Color(0xFF263518)
            : const Color(0xEE0D211D),
        borderRadius: BorderRadius.circular(13),
        border: Border.all(
          color: borderColor,
          width: winnerAmount > 0 || seat.isCurrentActor || seat.isCurrentUser
              ? 2.5
              : 1,
        ),
        boxShadow: [
          BoxShadow(
            color: winnerAmount > 0
                ? const Color(0x99FFD54F)
                : seat.isCurrentActor
                ? const Color(0x66FFA94D)
                : Colors.black38,
            blurRadius: winnerAmount > 0 || seat.isCurrentActor ? 18 : 8,
            spreadRadius: winnerAmount > 0 || seat.isCurrentActor ? 3 : 0,
          ),
        ],
      ),
      child: Row(
        children: [
          GestureDetector(
            behavior: HitTestBehavior.opaque,
            onTap: seat.isEmpty || seat.isCurrentUser ? null : onAvatarTap,
            child: Tooltip(
              message: seat.isEmpty || seat.isCurrentUser
                  ? ''
                  : '点击赞赏或嘲讽 ${seat.displayName}',
              child: CircleAvatar(
                radius: 25,
                backgroundColor: seat.isEmpty
                    ? Colors.white10
                    : const Color(0xFF315F51),
                child: seat.isEmpty
                    ? const Icon(Icons.add, color: Colors.white54, size: 24)
                    : Text(
                        seat.position.isEmpty
                            ? '${seat.number}'
                            : seat.position,
                        textAlign: TextAlign.center,
                        style: const TextStyle(
                          fontWeight: FontWeight.w700,
                          fontSize: 11,
                        ),
                      ),
              ),
            ),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              mainAxisAlignment: MainAxisAlignment.center,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                if (seat.revealedCards.isNotEmpty)
                  TableSeatShowdownSummary(
                    seat: seat,
                    winnerAmount: winnerAmount,
                  )
                else if (seat.isCurrentUser)
                  TableCurrentSeatSummary(
                    seat: seat,
                    showReadyStatus: showReadyStatus,
                    onUseTimeExtension: onUseTimeExtension,
                    deal: deal,
                  )
                else ...[
                  // 别人的底牌只在发牌演出期间以牌背出现，随后淡出；
                  // 玩家框还要承载筹码、位置等信息，不能被牌常驻占用。
                  if (deal.isDealing && deal.dealtCards > 0)
                    Padding(
                      padding: const EdgeInsets.only(bottom: 3),
                      child: Opacity(
                        opacity: (1 - deal.flipProgress).clamp(0.0, 1.0),
                        child: Row(
                          children: [
                            for (
                              var index = 0;
                              index < deal.dealtCards;
                              index++
                            ) ...[
                              if (index > 0) const SizedBox(width: 3),
                              const TableMiniCardBack(),
                            ],
                          ],
                        ),
                      ),
                    ),
                  Row(
                    children: [
                      Expanded(
                        child: FittedBox(
                          fit: BoxFit.scaleDown,
                          alignment: Alignment.centerLeft,
                          child: Text(
                            seat.displayName,
                            maxLines: 1,
                            style: const TextStyle(
                              fontWeight: FontWeight.w700,
                              fontSize: 16,
                            ),
                          ),
                        ),
                      ),
                      if (seat.isDealer)
                        const Padding(
                          padding: EdgeInsets.only(left: 3),
                          child: Text(
                            'D',
                            style: TextStyle(color: Color(0xFFF4D477)),
                          ),
                        ),
                      if (seat.isOwner)
                        const Padding(
                          padding: EdgeInsets.only(left: 4),
                          child: Tooltip(
                            message: '房主',
                            child: Icon(
                              Icons.workspace_premium,
                              color: Color(0xFFF6D986),
                              size: 17,
                            ),
                          ),
                        ),
                    ],
                  ),
                  const SizedBox(height: 2),
                  Row(
                    children: [
                      if (seat.isSpeaking)
                        const Icon(
                          Icons.graphic_eq,
                          color: Color(0xFF6DE0A4),
                          size: 17,
                        )
                      else if (seat.isMicrophoneEnabled)
                        const Icon(
                          Icons.mic,
                          color: Color(0xFF6DE0A4),
                          size: 17,
                        ),
                      if (showReadyStatus && seat.isReady) ...[
                        const SizedBox(width: 4),
                        const Icon(
                          Icons.check_circle,
                          color: Color(0xFF6DE0A4),
                          size: 16,
                        ),
                        const SizedBox(width: 3),
                        const Text(
                          '已准备',
                          style: TextStyle(
                            color: Color(0xFF6DE0A4),
                            fontSize: 11,
                          ),
                        ),
                      ],
                      if (seat.isParticipating && !showReadyStatus) ...[
                        const Spacer(),
                        Text(
                          '加时卡 ×${seat.timeExtensions}',
                          style: const TextStyle(
                            color: Colors.white54,
                            fontSize: 11,
                          ),
                        ),
                      ],
                    ],
                  ),
                  Row(
                    children: [
                      const Icon(
                        Icons.paid,
                        size: 14,
                        color: Color(0xFFF6D986),
                      ),
                      const SizedBox(width: 4),
                      Expanded(
                        child: Text(
                          '${seat.chips}${seat.isAllIn
                              ? ' · 全下'
                              : !seat.isConnected
                              ? ' · 已断线'
                              : ''}',
                          overflow: TextOverflow.ellipsis,
                          style: const TextStyle(
                            color: Colors.white70,
                            fontSize: 13,
                            fontWeight: FontWeight.w600,
                          ),
                        ),
                      ),
                    ],
                  ),
                ],
                if (seat.revealedCards.isEmpty)
                  if (!seat.isEmpty && winnerAmount > 0)
                    Text(
                      '🏆 赢家  +$winnerAmount',
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(
                        color: Color(0xFFFFD54F),
                        fontSize: 12,
                        fontWeight: FontWeight.w800,
                      ),
                    )
                  else if (!seat.isEmpty && seat.isFolded)
                    const Row(
                      children: [
                        Icon(Icons.block, color: Colors.redAccent, size: 15),
                        SizedBox(width: 4),
                        Text(
                          '本手已弃牌',
                          style: TextStyle(
                            color: Colors.redAccent,
                            fontSize: 12,
                            fontWeight: FontWeight.w800,
                          ),
                        ),
                      ],
                    )
                  else if (!seat.isEmpty && seat.isCurrentActor)
                    Text(
                      '剩余 ${remainingSeconds(actionRemaining)} 秒',
                      style: TextStyle(
                        color: remainingSeconds(actionRemaining) <= 5
                            ? Colors.redAccent
                            : const Color(0xFFFFA94D),
                        fontSize: 12,
                        fontWeight: FontWeight.w700,
                      ),
                    )
                  else if (!seat.isEmpty && seat.lastAction.isNotEmpty)
                    Text(
                      actionLabel(seat.lastAction, seat.lastActionTo),
                      overflow: TextOverflow.ellipsis,
                      style: const TextStyle(
                        color: Color(0xFFF6D986),
                        fontSize: 12,
                      ),
                    ),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class TableCurrentSeatSummary extends StatelessWidget {
  const TableCurrentSeatSummary({
    required this.seat,
    required this.showReadyStatus,
    required this.onUseTimeExtension,
    this.deal = const SeatDealState.settled(),
    super.key,
  });

  final TableSeat seat;
  final bool showReadyStatus;
  final VoidCallback onUseTimeExtension;

  /// 本座位的发牌演出状态。
  final SeatDealState deal;

  @override
  Widget build(BuildContext context) {
    final canExtend =
        seat.isCurrentActor && seat.timeExtensions > 0 && !showReadyStatus;
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            if (deal.isDealing) ...[
              // 发牌演出：先落牌背，本人的牌随后翻开，别人的牌淡出，
              // 不常驻在玩家框里——那里还要承载筹码、位置等信息。
              for (var index = 0; index < deal.dealtCards; index++) ...[
                if (index > 0) const SizedBox(width: 3),
                if (seat.isCurrentUser && index < seat.holeCards.length)
                  TableMiniFlipCard(
                    progress: deal.flipProgress,
                    label:
                        '${cardRank(seat.holeCards[index])}'
                        '${cardSuit(seat.holeCards[index])}',
                  )
                else
                  Opacity(
                    opacity: (1 - deal.flipProgress).clamp(0.0, 1.0),
                    child: const TableMiniCardBack(),
                  ),
              ],
              const Spacer(),
            ] else if (seat.holeCards.isEmpty)
              const Expanded(
                child: Text(
                  '等待发牌',
                  style: TextStyle(color: Colors.white38, fontSize: 11),
                ),
              )
            else ...[
              for (var index = 0; index < seat.holeCards.length; index++) ...[
                if (index > 0) const SizedBox(width: 3),
                TableMiniCard(
                  label:
                      '${cardRank(seat.holeCards[index])}${cardSuit(seat.holeCards[index])}',
                  compact: true,
                ),
              ],
              const Spacer(),
            ],
            IconButton(
              key: const ValueKey('seat-time-extension'),
              onPressed: canExtend ? onUseTimeExtension : null,
              tooltip: '加时 +30秒（剩余 ${seat.timeExtensions} 张）',
              padding: EdgeInsets.zero,
              visualDensity: VisualDensity.compact,
              constraints: const BoxConstraints.tightFor(width: 30, height: 30),
              icon: Badge(
                label: Text('${seat.timeExtensions}'),
                child: const Icon(Icons.timer_outlined, size: 17),
              ),
            ),
          ],
        ),
        const SizedBox(height: 2),
        Row(
          children: [
            const Icon(Icons.paid, size: 14, color: Color(0xFFF6D986)),
            const SizedBox(width: 4),
            Text(
              '${seat.chips}',
              style: const TextStyle(
                color: Colors.white70,
                fontSize: 13,
                fontWeight: FontWeight.w700,
              ),
            ),
            if (seat.isDealer)
              const Padding(
                padding: EdgeInsets.only(left: 5),
                child: Text('D', style: TextStyle(color: Color(0xFFF4D477))),
              ),
            if (seat.isOwner)
              const Padding(
                padding: EdgeInsets.only(left: 4),
                child: Tooltip(
                  message: '房主',
                  child: Icon(
                    Icons.workspace_premium,
                    color: Color(0xFFF6D986),
                    size: 16,
                  ),
                ),
              ),
            if (showReadyStatus && seat.isReady) ...[
              const Spacer(),
              const Icon(
                Icons.check_circle,
                color: Color(0xFF6DE0A4),
                size: 15,
              ),
            ],
          ],
        ),
      ],
    );
  }
}

class TableSeatShowdownSummary extends StatelessWidget {
  const TableSeatShowdownSummary({
    required this.seat,
    required this.winnerAmount,
    super.key,
  });

  final TableSeat seat;
  final int winnerAmount;

  @override
  Widget build(BuildContext context) {
    return KeyedSubtree(
      key: ValueKey('seat-showdown-${seat.userId}'),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            seat.displayName,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: const TextStyle(fontSize: 13, fontWeight: FontWeight.w800),
          ),
          const SizedBox(height: 2),
          Row(
            children: [
              for (
                var index = 0;
                index < seat.revealedCards.length;
                index++
              ) ...[
                if (index > 0) const SizedBox(width: 3),
                TableMiniCard(
                  label:
                      '${cardRank(seat.revealedCards[index])}${cardSuit(seat.revealedCards[index])}',
                  compact: true,
                ),
              ],
              if (winnerAmount > 0) ...[
                const Spacer(),
                const Icon(
                  Icons.emoji_events,
                  color: Color(0xFFFFD54F),
                  size: 19,
                ),
              ],
            ],
          ),
          const SizedBox(height: 2),
          Text(
            handCategoryLabel(seat.handCategory),
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: const TextStyle(
              fontSize: 10,
              color: Color(0xFFF6D986),
              fontWeight: FontWeight.w700,
            ),
          ),
          if (winnerAmount > 0)
            Text(
              '赢家 +$winnerAmount',
              style: const TextStyle(
                color: Color(0xFFFFD54F),
                fontSize: 10,
                fontWeight: FontWeight.w800,
              ),
            ),
        ],
      ),
    );
  }
}

class TablePlayerInteractionBurst extends StatelessWidget {
  const TablePlayerInteractionBurst({required this.interaction, super.key});

  final TablePlayerInteraction interaction;

  @override
  Widget build(BuildContext context) {
    final praise = interaction.kind == 'praise';
    return TweenAnimationBuilder<double>(
      key: ValueKey(interaction.interactionId),
      tween: Tween(begin: 0, end: 1),
      duration: const Duration(milliseconds: 1700),
      curve: Curves.easeOut,
      builder: (context, progress, child) {
        final opacity = math.sin(math.pi * progress).clamp(0.0, 1.0);
        final shake = praise ? 0.0 : math.sin(progress * math.pi * 10) * 8;
        return Opacity(
          opacity: opacity,
          child: Transform.translate(
            offset: Offset(shake, -48 - progress * 48),
            child: Transform.scale(
              scale: 0.72 + math.min(progress * 1.4, 0.38),
              child: child,
            ),
          ),
        );
      },
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 7),
        decoration: BoxDecoration(
          color: praise ? const Color(0xEE6A541C) : const Color(0xEE6A2A32),
          borderRadius: BorderRadius.circular(22),
          border: Border.all(
            color: praise ? const Color(0xFFFFD54F) : Colors.redAccent,
            width: 2,
          ),
          boxShadow: [
            BoxShadow(
              color: praise ? const Color(0x99FFD54F) : const Color(0x99FF5252),
              blurRadius: 18,
              spreadRadius: 3,
            ),
          ],
        ),
        child: Text(
          praise
              ? '👍  ${interaction.fromDisplayName} 赞赏'
              : '😜  ${interaction.fromDisplayName} 嘲讽',
          style: const TextStyle(fontWeight: FontWeight.w800, fontSize: 13),
        ),
      ),
    );
  }
}
