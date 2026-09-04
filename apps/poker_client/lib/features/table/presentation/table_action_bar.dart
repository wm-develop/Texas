import 'package:flutter/material.dart';
import 'package:poker_client/core/network/game_socket_client.dart';
import 'package:poker_client/features/table/domain/table_snapshot.dart';
import 'package:poker_client/features/table/presentation/table_bet_panel.dart';
import 'package:poker_client/features/table/presentation/table_labels.dart';

/// 牌桌动作区。合法动作与快捷金额均来自服务端快照，本文件只负责展示与提交意图。
///
/// 手机上以 [vertical] 竖排放在牌桌右栏，把牌桌下方的空间让给玩家框与公共牌；
/// 平板与桌面横排放在底部右侧。两种形态共用同一套交互，避免两端肌肉记忆不一致。
class TableActionBar extends StatelessWidget {
  const TableActionBar({
    required this.client,
    required this.userId,
    required this.smallBlind,
    required this.onRebuy,
    this.vertical = false,
    this.blocked = false,
    super.key,
  });

  final GameSocketClient client;
  final String userId;
  final int smallBlind;
  final VoidCallback onRebuy;
  final bool vertical;

  /// 发牌演出进行中：牌还没翻开，不接受行动输入。
  final bool blocked;

  @override
  Widget build(BuildContext context) {
    final snapshot = client.snapshot;
    final ownSeat = snapshot?.seats
        .where((seat) => seat.userId == userId)
        .firstOrNull;
    final current = snapshot?.currentAction;
    final ownTurn = current?.userId == userId;
    final waiting =
        snapshot == null ||
        snapshot.phase == 'WAITING' ||
        snapshot.phase == 'WAITING_NEXT_HAND';
    final autoReadyRemaining = snapshot?.autoReadyDeadline?.difference(
      client.serverNow,
    );
    final autoReadyPending = autoReadyRemaining != null;
    final runoutChoice = snapshot?.runoutChoice;

    final Widget content;
    if (snapshot?.spectating ?? false) {
      // 观战者没有准备态，也不会轮到行动；面板只放「上桌」与「补码」。
      content = _SpectatorActions(
        client: client,
        userId: userId,
        snapshot: snapshot!,
        onRebuy: onRebuy,
        vertical: vertical,
      );
    } else if (waiting) {
      content = _WaitingActions(
        client: client,
        snapshot: snapshot,
        ownSeat: ownSeat,
        autoReadyPending: autoReadyPending,
        autoReadyRemaining: autoReadyRemaining,
        onRebuy: onRebuy,
        vertical: vertical,
      );
    } else if (runoutChoice != null) {
      content = _RunoutChoiceActions(
        client: client,
        userId: userId,
        choice: runoutChoice,
        remaining: runoutChoice.deadline?.difference(client.serverNow),
        vertical: vertical,
      );
    } else if (!ownTurn) {
      content = const Padding(
        padding: EdgeInsets.symmetric(vertical: 12),
        child: Text(
          '等待其他玩家行动',
          textAlign: TextAlign.center,
          style: TextStyle(color: Colors.white60),
        ),
      );
    } else {
      content = TableBetPanel(
        client: client,
        userId: userId,
        smallBlind: smallBlind,
        options: current!.options,
        suggestions: current.suggestions,
        ownSeat: ownSeat,
        vertical: vertical,
        blocked: blocked,
      );
    }

    return DecoratedBox(
      decoration: BoxDecoration(
        color: const Color(0xED0A1C18),
        borderRadius: BorderRadius.circular(16),
        border: Border.all(color: Colors.white12),
      ),
      child: Padding(
        padding: const EdgeInsets.fromLTRB(10, 8, 10, 9),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            if (client.actionPending)
              const Padding(
                padding: EdgeInsets.only(bottom: 6),
                child: SizedBox(
                  height: 2,
                  child: LinearProgressIndicator(minHeight: 2),
                ),
              ),
            content,
          ],
        ),
      ),
    );
  }
}

class _WaitingActions extends StatelessWidget {
  const _WaitingActions({
    required this.client,
    required this.snapshot,
    required this.ownSeat,
    required this.autoReadyPending,
    required this.autoReadyRemaining,
    required this.onRebuy,
    required this.vertical,
  });

  final GameSocketClient client;
  final TableSnapshot? snapshot;
  final TableSeatSnapshot? ownSeat;
  final bool autoReadyPending;
  final Duration? autoReadyRemaining;
  final VoidCallback onRebuy;
  final bool vertical;

  @override
  Widget build(BuildContext context) {
    final autoReadyActive =
        autoReadyPending && !(snapshot?.autoReadyCancelled ?? false);
    final ready = ownSeat?.ready == true;
    final children = <Widget>[
      FilledButton.icon(
        onPressed:
            client.status == GameSocketStatus.joined &&
                (ownSeat?.stack ?? 0) > 0 &&
                // 服务端排空期间准备会被拒绝，直接禁用并由状态条解释
                snapshot?.draining != true
            ? () => client.setReady(!(ready || autoReadyActive))
            : null,
        icon: Icon(
          ready || autoReadyActive ? Icons.pause_circle : Icons.play_circle,
        ),
        label: Text(
          ready
              ? '取消准备'
              : autoReadyActive
              ? (autoReadyRemaining! > Duration.zero
                    ? '取消自动准备（${remainingSeconds(autoReadyRemaining!)}秒）'
                    : '正在自动准备…')
              : '准备开始',
        ),
      ),
      OutlinedButton.icon(
        onPressed:
            client.status == GameSocketStatus.joined &&
                (ownSeat?.stack ?? 0) < (snapshot?.maxBuyIn ?? 0)
            ? onRebuy
            : null,
        icon: const Icon(Icons.add_circle_outline),
        label: const Text('补码'),
      ),
      // 进入观战：不占座位、不参与牌局，付看牌费后能看到所有人的手牌。
      // 观战位最多 10 人。
      OutlinedButton.icon(
        key: const ValueKey('enter-spectate-button'),
        onPressed:
            client.status == GameSocketStatus.joined &&
                ownSeat != null &&
                (snapshot?.spectators.length ?? 0) < 10
            ? client.enterSpectate
            : null,
        icon: const Icon(Icons.visibility_outlined, size: 17),
        label: const Text('进入观战'),
      ),
      if (snapshot?.canShowHoleCards ?? false)
        FilledButton.tonalIcon(
          onPressed: client.showHoleCards,
          icon: const Icon(Icons.visibility_outlined, size: 17),
          label: const Text('展示手牌'),
        ),
    ];
    return _stack(children, vertical);
  }
}

class _RunoutChoiceActions extends StatelessWidget {
  const _RunoutChoiceActions({
    required this.client,
    required this.userId,
    required this.choice,
    required this.remaining,
    required this.vertical,
  });

  final GameSocketClient client;
  final String userId;
  final RunoutChoiceSnapshot choice;
  final Duration? remaining;
  final bool vertical;

  @override
  Widget build(BuildContext context) {
    final ownChoice = choice.choices[userId];
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text(
          '全下发牌选择 · ${remainingSeconds(remaining ?? Duration.zero)} 秒',
          textAlign: TextAlign.center,
          style: const TextStyle(
            color: Color(0xFFF6D986),
            fontWeight: FontWeight.w700,
          ),
        ),
        const SizedBox(height: 8),
        if (!choice.eligiblePlayerIds.contains(userId))
          const Text('等待两名玩家选择', textAlign: TextAlign.center)
        else if (ownChoice != null)
          Text(
            '已选择发 $ownChoice 次，等待对方',
            textAlign: TextAlign.center,
            style: const TextStyle(color: Colors.white70),
          )
        else
          _stack([
            OutlinedButton(
              onPressed: () => client.chooseRunoutCount(1),
              child: const Text('发一次'),
            ),
            FilledButton(
              onPressed: () => client.chooseRunoutCount(2),
              child: const Text('发两次'),
            ),
          ], vertical),
      ],
    );
  }
}

/// 竖排时逐个铺满宽度，横排时并列居中。
Widget _stack(List<Widget> children, bool vertical) {
  if (children.isEmpty) return const SizedBox.shrink();
  if (vertical) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        for (var index = 0; index < children.length; index++) ...[
          if (index > 0) const SizedBox(height: 6),
          children[index],
        ],
      ],
    );
  }
  return Row(
    mainAxisAlignment: MainAxisAlignment.center,
    mainAxisSize: MainAxisSize.min,
    children: [
      for (var index = 0; index < children.length; index++) ...[
        if (index > 0) const SizedBox(width: 10),
        children[index],
      ],
    ],
  );
}

/// 观战者的操作面板：上桌、补码，以及本手能否看牌的状态。
///
/// 看牌费不够时按房主决定（D4）明确提示并突出补码按钮，而不是让人纳闷
/// 为什么看不到牌。
class _SpectatorActions extends StatelessWidget {
  const _SpectatorActions({
    required this.client,
    required this.userId,
    required this.snapshot,
    required this.onRebuy,
    required this.vertical,
  });

  final GameSocketClient client;
  final String userId;
  final TableSnapshot snapshot;
  final VoidCallback onRebuy;
  final bool vertical;

  @override
  Widget build(BuildContext context) {
    final me = snapshot.spectators
        .where((spectator) => spectator.userId == userId)
        .firstOrNull;
    final feeBigBlinds = snapshot.spectatorSettings.feeBigBlinds;
    final joined = client.status == GameSocketStatus.joined;
    final canSee = me?.canSeeHoleCards ?? false;
    final fee = snapshot.spectatorFee;
    final inHand =
        snapshot.phase != 'WAITING' && snapshot.phase != 'WAITING_NEXT_HAND';
    // 看牌权是开局收费时才发放的：手间、以及牌局中途才进来的观战者都还
    // 没付过费，此时 canSee 为 false 并不表示筹码不够。只有筹码真的低于
    // 看牌费才提示补码，否则告诉他什么时候能看到。
    final needsChips = fee > 0 && (me?.stack ?? 0) < fee;
    final status = needsChips
        ? '筹码不足以支付看牌费（$feeBigBlinds 个大盲/手，共 $fee），补码后可查看所有玩家手牌'
        : canSee
        ? '观战中 · 本手可查看所有玩家手牌'
        : fee == 0
        ? '观战中 · 免费观战，下一手起可查看所有玩家手牌'
        : inHand
        ? '观战中 · 本手中途进入，下一手起支付看牌费后可查看所有玩家手牌'
        : '观战中 · 下一手开始时支付看牌费（$feeBigBlinds 个大盲）后可查看所有玩家手牌';
    final rebuy = needsChips
        // 突出补码：这是恢复看牌的唯一途径
        ? FilledButton.icon(
            key: const ValueKey('spectator-rebuy-button'),
            onPressed: joined && (me?.stack ?? 0) < snapshot.maxBuyIn
                ? onRebuy
                : null,
            icon: const Icon(Icons.add_circle_outline),
            label: const Text('补码'),
          )
        : OutlinedButton.icon(
            key: const ValueKey('spectator-rebuy-button'),
            onPressed: joined && (me?.stack ?? 0) < snapshot.maxBuyIn
                ? onRebuy
                : null,
            icon: const Icon(Icons.add_circle_outline),
            label: const Text('补码'),
          );
    final children = <Widget>[
      Padding(
        padding: const EdgeInsets.symmetric(vertical: 4),
        child: Text(
          status,
          key: const ValueKey('spectator-status'),
          textAlign: TextAlign.center,
          style: TextStyle(
            color: needsChips ? Colors.orangeAccent : Colors.white70,
            fontSize: 12,
          ),
        ),
      ),
      FilledButton.icon(
        key: const ValueKey('take-seat-button'),
        // 手间立即入座；牌局进行中只记录意向。座位满时服务端拒绝并提示。
        onPressed: joined && me?.pendingSeat != true ? client.takeSeat : null,
        icon: const Icon(Icons.chair_alt_outlined),
        label: Text(me?.pendingSeat == true ? '本手结束后上桌' : '上桌'),
      ),
      rebuy,
    ];
    return _stack(children, vertical);
  }
}
