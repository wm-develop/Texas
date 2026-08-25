import 'package:flutter/material.dart';
import 'package:poker_client/features/history/domain/recent_hand.dart';

class RecentHandsPage extends StatefulWidget {
  const RecentHandsPage({
    required this.userId,
    required this.loadHands,
    super.key,
  });

  final String userId;
  final Future<List<RecentHand>> Function() loadHands;

  @override
  State<RecentHandsPage> createState() => _RecentHandsPageState();
}

class _RecentHandsPageState extends State<RecentHandsPage> {
  late Future<List<RecentHand>> _hands;

  @override
  void initState() {
    super.initState();
    _reload();
  }

  void _reload() => _hands = widget.loadHands();

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: const Text('最近牌局'),
        actions: [
          IconButton(
            onPressed: () => setState(_reload),
            tooltip: '刷新',
            icon: const Icon(Icons.refresh),
          ),
        ],
      ),
      body: DecoratedBox(
        decoration: const BoxDecoration(
          gradient: RadialGradient(
            colors: [Color(0xFF16473B), Color(0xFF061814)],
            radius: 1.2,
          ),
        ),
        child: FutureBuilder<List<RecentHand>>(
          future: _hands,
          builder: (context, snapshot) {
            if (snapshot.connectionState != ConnectionState.done) {
              return const Center(child: CircularProgressIndicator());
            }
            if (snapshot.hasError) {
              return _EmptyState(
                icon: Icons.cloud_off,
                message: '暂时无法读取最近牌局',
                onRetry: () => setState(_reload),
              );
            }
            final hands = snapshot.data ?? const [];
            if (hands.isEmpty) {
              return const _EmptyState(
                icon: Icons.style_outlined,
                message: '完成第一手牌后，这里会显示牌局记录',
              );
            }
            return ListView.separated(
              padding: const EdgeInsets.all(20),
              itemCount: hands.length,
              separatorBuilder: (_, _) => const SizedBox(height: 12),
              itemBuilder: (context, index) =>
                  _HandCard(hand: hands[index], userId: widget.userId),
            );
          },
        ),
      ),
    );
  }
}

class _HandCard extends StatelessWidget {
  const _HandCard({required this.hand, required this.userId});

  final RecentHand hand;
  final String userId;

  @override
  Widget build(BuildContext context) {
    final own = hand.players.where((value) => value.userId == userId).first;
    final won = own.delta > 0;
    final color = won
        ? const Color(0xFF6DE0A4)
        : own.delta < 0
        ? Colors.redAccent
        : Colors.white70;
    return Card(
      child: ExpansionTile(
        leading: CircleAvatar(
          backgroundColor: color.withValues(alpha: 0.16),
          child: Icon(
            won ? Icons.trending_up : Icons.horizontal_rule,
            color: color,
          ),
        ),
        title: Text(
          '${own.delta >= 0 ? '+' : ''}${own.delta} 筹码',
          style: TextStyle(color: color, fontWeight: FontWeight.w700),
        ),
        subtitle: Text(
          '房间 ${hand.roomCode} · ${_formatTime(hand.endedAt.toLocal())} · '
          '${hand.showdown ? '摊牌' : '弃牌结束'}',
        ),
        trailing: Wrap(
          spacing: 4,
          children: [for (final card in own.holeCards) _PlayingCard(card)],
        ),
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(20, 0, 20, 18),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                const Divider(),
                Wrap(
                  crossAxisAlignment: WrapCrossAlignment.center,
                  spacing: 6,
                  children: [
                    const Text('公共牌'),
                    if (hand.board.isEmpty)
                      const Text(
                        '未发出',
                        style: TextStyle(color: Colors.white54),
                      ),
                    for (final card in hand.board) _PlayingCard(card),
                  ],
                ),
                const SizedBox(height: 12),
                for (final player in hand.players)
                  ListTile(
                    dense: true,
                    contentPadding: EdgeInsets.zero,
                    title: Text('${player.displayName} · 座位 ${player.seat}'),
                    subtitle: Text(
                      '${player.startingStack} → ${player.endingStack}',
                    ),
                    trailing: Wrap(
                      spacing: 4,
                      crossAxisAlignment: WrapCrossAlignment.center,
                      children: [
                        for (final card in player.holeCards) _PlayingCard(card),
                        Text(
                          '${player.delta >= 0 ? '+' : ''}${player.delta}',
                          style: TextStyle(
                            color: player.delta >= 0
                                ? const Color(0xFF6DE0A4)
                                : Colors.redAccent,
                          ),
                        ),
                      ],
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

class _PlayingCard extends StatelessWidget {
  const _PlayingCard(this.card);

  final String card;

  @override
  Widget build(BuildContext context) {
    final suit = card.length == 2 ? card[1] : '';
    final red = suit == 'h' || suit == 'd';
    final symbol = switch (suit) {
      'c' => '♣',
      'd' => '♦',
      'h' => '♥',
      's' => '♠',
      _ => '',
    };
    return Container(
      width: 34,
      height: 42,
      alignment: Alignment.center,
      decoration: BoxDecoration(
        color: const Color(0xFFF4F0E8),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Text(
        card.isEmpty ? '?' : '${card[0]}$symbol',
        style: TextStyle(
          color: red ? const Color(0xFFC43B44) : const Color(0xFF17201E),
          fontWeight: FontWeight.w700,
        ),
      ),
    );
  }
}

class _EmptyState extends StatelessWidget {
  const _EmptyState({required this.icon, required this.message, this.onRetry});

  final IconData icon;
  final String message;
  final VoidCallback? onRetry;

  @override
  Widget build(BuildContext context) => Center(
    child: Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        Icon(icon, size: 52, color: Colors.white38),
        const SizedBox(height: 12),
        Text(message, style: const TextStyle(color: Colors.white60)),
        if (onRetry != null) ...[
          const SizedBox(height: 12),
          OutlinedButton(onPressed: onRetry, child: const Text('重试')),
        ],
      ],
    ),
  );
}

String _formatTime(DateTime value) =>
    '${value.month.toString().padLeft(2, '0')}-${value.day.toString().padLeft(2, '0')} '
    '${value.hour.toString().padLeft(2, '0')}:${value.minute.toString().padLeft(2, '0')}';
