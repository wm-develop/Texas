import 'package:flutter/material.dart';

/// 牌面与筹码的基础展示组件：公共牌大牌、玩家框内小牌、本轮下注筹码。

class TablePlayingCard extends StatelessWidget {
  const TablePlayingCard({
    required this.rank,
    required this.suit,
    this.red = false,
    super.key,
  });

  final String rank;
  final String suit;
  final bool red;

  @override
  Widget build(BuildContext context) {
    return AnimatedSwitcher(
      duration: const Duration(milliseconds: 260),
      switchInCurve: Curves.easeOutBack,
      transitionBuilder: (child, animation) => ScaleTransition(
        scale: Tween<double>(begin: 0.72, end: 1).animate(animation),
        child: FadeTransition(opacity: animation, child: child),
      ),
      child: Container(
        key: ValueKey('$rank$suit'),
        width: 58,
        height: 78,
        margin: const EdgeInsets.symmetric(horizontal: 4),
        padding: const EdgeInsets.all(7),
        decoration: BoxDecoration(
          color: rank == '?'
              ? const Color(0xFF234E43)
              : const Color(0xFFF4F0E7),
          borderRadius: BorderRadius.circular(8),
          border: Border.all(color: Colors.white30),
        ),
        child: Text(
          '$rank\n$suit',
          style: TextStyle(
            height: 1,
            color: rank == '?'
                ? Colors.white54
                : (red ? const Color(0xFFC63D45) : Colors.black87),
            fontSize: 18,
            fontWeight: FontWeight.w700,
          ),
        ),
      ),
    );
  }
}

class TableMiniCard extends StatelessWidget {
  const TableMiniCard({required this.label, this.compact = false, super.key});

  final String label;
  final bool compact;

  @override
  Widget build(BuildContext context) {
    final redSuit = label.contains('♥') || label.contains('♦');
    return Container(
      width: compact ? 27 : 38,
      height: compact ? 34 : 48,
      alignment: Alignment.center,
      decoration: BoxDecoration(
        color: const Color(0xFFF4F0E7),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Text(
        label,
        style: TextStyle(
          color: redSuit ? const Color(0xFFC63D45) : Colors.black87,
          fontWeight: FontWeight.w800,
          fontSize: compact ? 10 : null,
        ),
      ),
    );
  }
}

class TableBetChip extends StatelessWidget {
  const TableBetChip({required this.amount, super.key});

  final int amount;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 9, vertical: 4),
      decoration: BoxDecoration(
        color: const Color(0xFF2F223F),
        borderRadius: BorderRadius.circular(12),
        border: Border.all(color: const Color(0xFFE0B85B)),
        boxShadow: const [BoxShadow(color: Colors.black45, blurRadius: 5)],
      ),
      child: Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Icon(Icons.paid, size: 13, color: Color(0xFFF6D986)),
          const SizedBox(width: 3),
          Text('$amount', style: const TextStyle(fontWeight: FontWeight.w700)),
        ],
      ),
    );
  }
}
