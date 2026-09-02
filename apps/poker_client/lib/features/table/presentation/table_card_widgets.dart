import 'dart:math' as math;

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

/// 会翻面的公共牌。[progress] 为 0 时是背面，1 时完全翻开。
///
/// 用绕 Y 轴旋转实现：前半程转到侧立（看不见牌面），越过中点后换成正面并把
/// 变换镜像回来，视觉上就是一张牌被翻过来，而不是两张牌淡入淡出。
class TableFlipCard extends StatelessWidget {
  const TableFlipCard({
    required this.progress,
    required this.rank,
    required this.suit,
    this.red = false,
    super.key,
  });

  final double progress;
  final String rank;
  final String suit;
  final bool red;

  @override
  Widget build(BuildContext context) {
    final clamped = progress.clamp(0.0, 1.0);
    final showFace = clamped >= 0.5;
    final angle = clamped * math.pi;
    return Transform(
      alignment: Alignment.center,
      transform: Matrix4.identity()
        ..setEntry(3, 2, 0.0012)
        ..rotateY(showFace ? angle - math.pi : angle),
      child: showFace
          ? TablePlayingCard(rank: rank, suit: suit, red: red)
          : const TableCardBack(),
    );
  }
}

/// 玩家框内的迷你牌背，发底牌时先落下的就是它。
class TableMiniCardBack extends StatelessWidget {
  const TableMiniCardBack({this.compact = true, super.key});

  final bool compact;

  @override
  Widget build(BuildContext context) => Container(
    width: compact ? 27 : 38,
    height: compact ? 34 : 48,
    decoration: BoxDecoration(
      gradient: const LinearGradient(
        begin: Alignment.topLeft,
        end: Alignment.bottomRight,
        colors: [Color(0xFF7A2F35), Color(0xFF4A1B20)],
      ),
      borderRadius: BorderRadius.circular(6),
      border: Border.all(color: const Color(0x66E0B85B)),
    ),
  );
}

/// 玩家框内会翻面的迷你牌。
class TableMiniFlipCard extends StatelessWidget {
  const TableMiniFlipCard({
    required this.progress,
    required this.label,
    this.compact = true,
    super.key,
  });

  final double progress;
  final String label;
  final bool compact;

  @override
  Widget build(BuildContext context) {
    final clamped = progress.clamp(0.0, 1.0);
    final showFace = clamped >= 0.5;
    final angle = clamped * math.pi;
    return Transform(
      alignment: Alignment.center,
      transform: Matrix4.identity()
        ..setEntry(3, 2, 0.0015)
        ..rotateY(showFace ? angle - math.pi : angle),
      child: showFace
          ? TableMiniCard(label: label, compact: compact)
          : TableMiniCardBack(compact: compact),
    );
  }
}

/// 牌背。发牌过程中先落桌的就是它。
class TableCardBack extends StatelessWidget {
  const TableCardBack({this.width = 58, this.height = 78, super.key});

  final double width;
  final double height;

  @override
  Widget build(BuildContext context) => Container(
    width: width,
    height: height,
    margin: const EdgeInsets.symmetric(horizontal: 4),
    decoration: BoxDecoration(
      gradient: const LinearGradient(
        begin: Alignment.topLeft,
        end: Alignment.bottomRight,
        colors: [Color(0xFF7A2F35), Color(0xFF4A1B20)],
      ),
      borderRadius: BorderRadius.circular(8),
      border: Border.all(color: const Color(0x66E0B85B)),
      boxShadow: const [BoxShadow(color: Colors.black45, blurRadius: 6)],
    ),
    child: Center(
      child: Container(
        width: width * 0.5,
        height: height * 0.62,
        decoration: BoxDecoration(
          border: Border.all(color: const Color(0x55E0B85B)),
          borderRadius: BorderRadius.circular(4),
        ),
      ),
    ),
  );
}
