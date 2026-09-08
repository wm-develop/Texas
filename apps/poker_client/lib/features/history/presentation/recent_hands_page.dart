import 'package:flutter/material.dart';
import 'package:poker_client/features/history/domain/recent_hand.dart';
import 'package:poker_client/features/table/presentation/table_card_widgets.dart';
import 'package:poker_client/features/table/presentation/table_labels.dart';

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
    final boards = hand.runoutBoards.isEmpty
        ? <List<String>>[hand.board]
        : hand.runoutBoards;
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
                for (var index = 0; index < boards.length; index++)
                  Padding(
                    padding: EdgeInsets.only(top: index == 0 ? 0 : 8),
                    child: Wrap(
                      crossAxisAlignment: WrapCrossAlignment.center,
                      spacing: 6,
                      children: [
                        Text(boards.length == 1 ? '公共牌' : '第${index + 1}次公共牌'),
                        if (boards[index].isEmpty)
                          const Text(
                            '未发出',
                            style: TextStyle(color: Colors.white54),
                          ),
                        for (final card in boards[index]) _PlayingCard(card),
                      ],
                    ),
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
                        // 服务端只下发本人的牌与摊牌时亮过的牌，别人盖着结束
                        // 的牌永远不会出现在这里；写清楚，免得看着像加载失败。
                        if (player.holeCards.isEmpty)
                          const Text(
                            '未亮牌',
                            style: TextStyle(
                              color: Colors.white38,
                              fontSize: 12,
                            ),
                          )
                        else
                          for (final card in player.holeCards)
                            _PlayingCard(card),
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
                if (hand.actions.isNotEmpty) ...[
                  const Divider(height: 20),
                  const Text(
                    '过程',
                    style: TextStyle(fontWeight: FontWeight.w700),
                  ),
                  const SizedBox(height: 4),
                  for (final street in _streetsOf(hand))
                    Padding(
                      key: ValueKey('hand-street-$street'),
                      padding: const EdgeInsets.only(top: 4),
                      child: Text(
                        '${_streetLabel(street)}：'
                        '${_actionsOfStreet(hand, street).join('，')}',
                        style: const TextStyle(
                          color: Colors.white70,
                          fontSize: 12,
                        ),
                      ),
                    ),
                ],
              ],
            ),
          ),
        ],
      ),
    );
  }
}

/// 本手出现过的街，按牌局顺序；没有动作的街不显示。
List<String> _streetsOf(RecentHand hand) {
  const order = ['preflop', 'flop', 'turn', 'river'];
  final present = hand.actions.map((action) => action.street).toSet();
  return [for (final street in order) if (present.contains(street)) street];
}

String _streetLabel(String street) => switch (street) {
  'preflop' => '翻牌前',
  'flop' => '翻牌',
  'turn' => '转牌',
  'river' => '河牌',
  _ => street,
};

/// 把一条街上的动作写成「昵称 动作」，昵称取自本手的玩家名单。
List<String> _actionsOfStreet(RecentHand hand, String street) {
  final names = {
    for (final player in hand.players) player.userId: player.displayName,
  };
  return [
    for (final action in hand.actions)
      if (action.street == street)
        '${names[action.userId] ?? '玩家'} ${_actionLabel(action)}',
  ];
}

String _actionLabel(RecentHandAction action) => switch (action.type) {
  'fold' => '弃牌',
  'check' => '过牌',
  'call' => '跟注 ${action.committed}',
  'bet' => '下注 ${action.raiseTo}',
  'raise' => '加注到 ${action.raiseTo}',
  'all_in' => '全下 ${action.raiseTo}',
  'post_small_blind' => '小盲 ${action.committed}',
  'post_big_blind' => '大盲 ${action.committed}',
  _ => action.type,
};

class _PlayingCard extends StatelessWidget {
  const _PlayingCard(this.card);

  final String card;

  @override
  Widget build(BuildContext context) {
    // 与牌桌共用同一套四色和自绘花色：花色符号在 Android / HarmonyOS 上会被
    // 彩色 emoji 字体接管，用文字画出来颜色不受控，两处还会不一致。
    final symbol = cardSuit(card);
    final color = suitColor(symbol);
    return Container(
      width: 34,
      height: 42,
      alignment: Alignment.center,
      decoration: BoxDecoration(
        color: const Color(0xFFF4F0E8),
        borderRadius: BorderRadius.circular(6),
      ),
      // 「10」比别的点数宽，缩放而不是溢出
      child: FittedBox(
        fit: BoxFit.scaleDown,
        child: card.isEmpty
            ? const Text('?', style: TextStyle(fontWeight: FontWeight.w700))
            : Row(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(
                    cardRank(card),
                    style: TextStyle(color: color, fontWeight: FontWeight.w800),
                  ),
                  const SizedBox(width: 1),
                  SuitGlyph(suit: symbol, size: 11, color: color),
                ],
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
