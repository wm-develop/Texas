import 'package:flutter/material.dart';
import 'package:poker_client/features/table/domain/table_snapshot.dart';

/// 房间名单：分列上桌玩家与观战者。
///
/// 从右上角的「X/10（OB: Y）」点开。观战者不显示在牌桌画面上（桌面已经很挤），
/// 这里是唯一能看到他们的地方，因此把「能否看牌」「是否待上桌」也标出来。
class TableRosterDialog extends StatelessWidget {
  const TableRosterDialog({
    required this.snapshot,
    required this.currentUserId,
    this.maxPlayers = 10,
    super.key,
  });

  final TableSnapshot snapshot;
  final String currentUserId;
  final int maxPlayers;

  static Future<void> show(
    BuildContext context, {
    required TableSnapshot snapshot,
    required String currentUserId,
    int maxPlayers = 10,
  }) => showDialog<void>(
    context: context,
    builder: (_) => TableRosterDialog(
      snapshot: snapshot,
      currentUserId: currentUserId,
      maxPlayers: maxPlayers,
    ),
  );

  @override
  Widget build(BuildContext context) {
    final seats = [...snapshot.seats]
      ..sort((left, right) => left.seat.compareTo(right.seat));
    final spectators = snapshot.spectators;
    return AlertDialog(
      title: Text(
        '房间名单 · ${seats.length}/$maxPlayers（OB: ${spectators.length}）',
      ),
      content: SizedBox(
        width: 380,
        // 人多时列表很长，手机横屏更放不下，一律可滚动
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              _SectionTitle('上桌玩家（${seats.length}）'),
              if (seats.isEmpty)
                const _Hint('牌桌上还没有人')
              else
                for (final seat in seats)
                  ListTile(
                    key: ValueKey('roster-seat-${seat.userId}'),
                    dense: true,
                    contentPadding: EdgeInsets.zero,
                    leading: _SeatBadge(seat.seat),
                    title: Text(
                      seat.userId == currentUserId
                          ? '${seat.displayName}（我）'
                          : seat.displayName,
                    ),
                    subtitle: Text(
                      '筹码 ${seat.stack}'
                      '${seat.ready ? ' · 已准备' : ''}'
                      '${seat.connected ? '' : ' · 已断线'}'
                      '${seat.pendingSpectate ? ' · 本手结束后进入观战' : ''}',
                      style: const TextStyle(fontSize: 12),
                    ),
                  ),
              const Divider(height: 20),
              _SectionTitle('观战（${spectators.length}）'),
              if (spectators.isEmpty)
                const _Hint('暂时没有人观战')
              else
                for (final spectator in spectators)
                  ListTile(
                    key: ValueKey('roster-spectator-${spectator.userId}'),
                    dense: true,
                    contentPadding: EdgeInsets.zero,
                    leading: Icon(
                      spectator.canSeeHoleCards
                          ? Icons.visibility_outlined
                          : Icons.visibility_off_outlined,
                      size: 20,
                      color: spectator.canSeeHoleCards
                          ? const Color(0xFFF6D986)
                          : Colors.white38,
                    ),
                    title: Text(
                      spectator.userId == currentUserId
                          ? '${spectator.displayName}（我）'
                          : spectator.displayName,
                    ),
                    subtitle: Text(
                      '筹码 ${spectator.stack}'
                      '${spectator.canSeeHoleCards ? ' · 本手可看牌' : ' · 本手不可看牌'}'
                      '${spectator.connected ? '' : ' · 已断线'}'
                      '${spectator.pendingSeat ? ' · 本手结束后上桌' : ''}',
                      style: const TextStyle(fontSize: 12),
                    ),
                  ),
            ],
          ),
        ),
      ),
      actions: [
        FilledButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('关闭'),
        ),
      ],
    );
  }
}

class _SectionTitle extends StatelessWidget {
  const _SectionTitle(this.text);

  final String text;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.only(bottom: 4),
    child: Text(text, style: const TextStyle(fontWeight: FontWeight.w700)),
  );
}

class _Hint extends StatelessWidget {
  const _Hint(this.text);

  final String text;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.symmetric(vertical: 8),
    child: Text(text, style: const TextStyle(color: Colors.white54)),
  );
}

class _SeatBadge extends StatelessWidget {
  const _SeatBadge(this.seat);

  final int seat;

  @override
  Widget build(BuildContext context) => Container(
    width: 26,
    height: 26,
    alignment: Alignment.center,
    decoration: const BoxDecoration(
      color: Color(0xFF1F5F4B),
      shape: BoxShape.circle,
    ),
    child: Text(
      '$seat',
      style: const TextStyle(fontSize: 12, fontWeight: FontWeight.w700),
    ),
  );
}
