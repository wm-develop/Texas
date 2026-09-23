import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:poker_client/core/network/game_api_client.dart';
import 'package:poker_client/core/platform/native_display_cutout.dart';
import 'package:poker_client/features/history/domain/hand_replay.dart';
import 'package:poker_client/features/table/domain/table_seat.dart';
import 'package:poker_client/features/table/domain/table_snapshot.dart';
import 'package:poker_client/features/table/presentation/table_canvas.dart';
import 'package:poker_client/features/table/presentation/table_labels.dart';
import 'package:poker_client/features/table/presentation/table_viewport_layout.dart';

/// 牌局回放：像录像一样逐步播放一手牌，每名玩家的每次行动是一步。
///
/// 牌桌画面直接复用对局时的 [TableCanvas] 与同一套画布几何，每一帧把服务端
/// 给的桌面状态换成一份 [TableSnapshot]，外观与真实牌桌一致。
///
/// 控件不跟着牌桌一起缩放：手机横屏时牌桌会缩到一半左右，按钮若一起缩就
/// 小得点不中。它们放在牌桌两侧对局时信息栏与下注区所在的位置。
class HandReplayPage extends StatefulWidget {
  const HandReplayPage({
    required this.userId,
    required this.loadReplay,
    super.key,
  });

  final String userId;
  final Future<HandReplay> Function() loadReplay;

  @override
  State<HandReplayPage> createState() => _HandReplayPageState();
}

class _HandReplayPageState extends State<HandReplayPage>
    with WidgetsBindingObserver {
  static const _speeds = [1.0, 2.0, 0.5];

  HandReplay? _replay;
  String? _error;
  bool _loading = true;
  int _index = 0;
  Timer? _timer;
  int _speedIndex = 0;
  NativeScreenInsets _nativeScreenInsets = NativeScreenInsets.zero;

  bool get _playing => _timer != null;

  static bool get _isMobilePlatform =>
      defaultTargetPlatform == TargetPlatform.android ||
      defaultTargetPlatform == TargetPlatform.iOS ||
      defaultTargetPlatform == TargetPlatform.ohos;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    unawaited(_refreshDisplayCutout());
    _load();
  }

  @override
  void didChangeMetrics() {
    // 旋转、分屏后挖孔所在的边会变
    unawaited(_refreshDisplayCutout());
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    _timer?.cancel();
    super.dispose();
  }

  Future<void> _refreshDisplayCutout() async {
    final next = await NativeDisplayCutout.read();
    if (mounted && next != _nativeScreenInsets) {
      setState(() => _nativeScreenInsets = next);
    }
  }

  Future<void> _load() async {
    setState(() {
      _loading = true;
      _error = null;
    });
    try {
      final replay = await widget.loadReplay();
      if (!mounted) return;
      setState(() {
        _replay = replay;
        _index = 0;
      });
    } on Object catch (error) {
      if (mounted) setState(() => _error = replayErrorMessage(error));
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  void _goTo(int index) {
    final replay = _replay;
    if (replay == null || replay.steps.isEmpty) return;
    setState(() => _index = index.clamp(0, replay.steps.length - 1));
    if (_index == replay.steps.length - 1) _pause();
  }

  void _togglePlay() {
    if (_playing) {
      _pause();
      return;
    }
    final replay = _replay;
    if (replay == null) return;
    // 已经在最后一步时从头放
    if (_index >= replay.steps.length - 1) _goTo(0);
    _startTimer();
  }

  void _startTimer() {
    _timer?.cancel();
    final interval = Duration(
      milliseconds: (1200 / _speeds[_speedIndex]).round(),
    );
    _timer = Timer.periodic(interval, (_) => _goTo(_index + 1));
    setState(() {});
  }

  void _pause() {
    _timer?.cancel();
    _timer = null;
    if (mounted) setState(() {});
  }

  void _cycleSpeed() {
    setState(() => _speedIndex = (_speedIndex + 1) % _speeds.length);
    if (_playing) _startTimer();
  }

  void _step(int delta) {
    _pause();
    _goTo(_index + delta);
  }

  @override
  Widget build(BuildContext context) {
    final replay = _replay;
    final mediaSize = MediaQuery.sizeOf(context);
    // 手机横屏高度只有三百多，标题栏要让给牌桌；返回按钮挪进左栏
    final shortScreen = mediaSize.height < 500;
    final remainingCutout = NativeDisplayCutout.remainingSystemInsets(
      nativeCutout: _nativeScreenInsets.cutout,
      mediaPadding: MediaQuery.paddingOf(context),
      viewPadding: MediaQuery.viewPaddingOf(context),
      systemGestureInsets: MediaQuery.systemGestureInsetsOf(context),
    );
    final title = replay == null ? '牌局回放' : '牌局回放 · 房间 ${replay.roomCode}';
    return Scaffold(
      appBar: shortScreen && replay != null ? null : AppBar(title: Text(title)),
      body: DecoratedBox(
        decoration: const BoxDecoration(
          gradient: RadialGradient(
            colors: [Color(0xFF16473B), Color(0xFF061814)],
            radius: 1.2,
          ),
        ),
        child: SafeArea(
          child: Padding(
            padding: remainingCutout,
            child: _loading
                ? const Center(child: CircularProgressIndicator())
                : replay == null || replay.steps.isEmpty
                ? _ReplayError(
                    message: _error ?? '这手牌没有可以回放的内容',
                    onRetry: _load,
                  )
                : _buildReplay(context, replay, shortScreen ? title : null),
          ),
        ),
      ),
    );
  }

  Widget _buildReplay(BuildContext context, HandReplay replay, String? title) {
    final step = replay.steps[_index];
    return LayoutBuilder(
      builder: (context, constraints) {
        final available = constraints.biggest;
        final viewport = TableViewportLayout.fromSize(
          available,
          chatVisible: false,
          compactOverride: _isMobilePlatform
              ? MediaQuery.sizeOf(context).shortestSide < 600
              : null,
        );
        final compact = viewport.isCompactLandscape;
        final canvas = viewport.canvasSize;
        final scale = math.min(
          available.width / canvas.width,
          available.height / canvas.height,
        );
        final left = (available.width - canvas.width * scale) / 2;
        final top = (available.height - canvas.height * scale) / 2;
        // 两侧栏在屏幕上的实际宽度；牌桌居中后两边若有空白也一并用上。面板
        // 宽度只取栏宽，不设下限：栏再窄也不能伸进牌桌压住玩家框
        final rightRail =
            (compact
                ? TableViewportLayout.compactRightRailWidth
                : TableViewportLayout.betRailWidth) *
            scale;
        final leftRail = compact
            ? TableViewportLayout.compactLeftRailWidth * scale
            : 0.0;
        final seats = replaySeats(replay, _index, widget.userId);
        final anchor = seats.indexWhere((seat) => seat.isCurrentUser);
        final info = _ReplayInfo(
          title: title,
          stepText: replayStepLabel(replay, step),
          position: _index,
          count: replay.steps.length,
        );
        final controls = _ReplayControls(
          position: _index,
          count: replay.steps.length,
          playing: _playing,
          speed: _speeds[_speedIndex],
          onSeek: (index) {
            _pause();
            _goTo(index);
          },
          onTogglePlay: _togglePlay,
          onCycleSpeed: _cycleSpeed,
          onPrevious: _index > 0 ? () => _step(-1) : null,
          onNext: _index < replay.steps.length - 1 ? () => _step(1) : null,
        );
        return Stack(
          children: [
            Positioned(
              left: left,
              top: top,
              width: canvas.width * scale,
              height: canvas.height * scale,
              child: FittedBox(
                child: SizedBox.fromSize(
                  size: canvas,
                  child: Stack(
                    children: [
                      Positioned.fromRect(
                        rect: viewport.tableRect,
                        child: TableCanvas(
                          key: const ValueKey('replay-table'),
                          seats: seats,
                          alignments: List.generate(seats.length, (index) {
                            final relative =
                                (index - (anchor < 0 ? 0 : anchor)) %
                                seats.length;
                            return viewport.seatAlignment(
                              relative,
                              seats.length,
                            );
                          }),
                          boardRect: viewport.boardRect.shift(
                            -viewport.tableRect.topLeft,
                          ),
                          snapshot: replaySnapshot(replay, _index),
                          actionRemaining: Duration.zero,
                          onSeatTap: (_) {},
                          onAvatarTap: null,
                          onUseTimeExtension: null,
                          interactions: const [],
                        ),
                      ),
                    ],
                  ),
                ),
              ),
            ),
            if (compact) ...[
              // 手机：说明在左栏，操作在右栏
              Positioned(
                left: 6,
                top: 6,
                bottom: 6,
                width: left + leftRail - 10,
                child: info,
              ),
              Positioned(
                right: 6,
                bottom: 6,
                width: left + rightRail - 10,
                child: controls,
              ),
            ] else
              Positioned(
                right: 12,
                bottom: 12,
                width: left + rightRail - 24,
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [info, const SizedBox(height: 8), controls],
                ),
              ),
          ],
        );
      },
    );
  }
}

const _panelDecoration = BoxDecoration(
  color: Color(0xCC102620),
  borderRadius: BorderRadius.all(Radius.circular(12)),
  border: Border.fromBorderSide(BorderSide(color: Colors.white12)),
);

class _ReplayInfo extends StatelessWidget {
  const _ReplayInfo({
    required this.title,
    required this.stepText,
    required this.position,
    required this.count,
  });

  /// 没有标题栏时（手机横屏）才有：连同返回按钮放在这里。
  final String? title;
  final String stepText;
  final int position;
  final int count;

  @override
  Widget build(BuildContext context) {
    return Align(
      alignment: Alignment.topLeft,
      child: Container(
        key: const ValueKey('replay-info-panel'),
        padding: const EdgeInsets.all(8),
        decoration: _panelDecoration,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              if (title != null) ...[
                Row(
                  children: [
                    IconButton(
                      key: const ValueKey('replay-back'),
                      onPressed: () => Navigator.of(context).maybePop(),
                      icon: const Icon(Icons.arrow_back),
                      tooltip: '返回',
                      visualDensity: VisualDensity.compact,
                    ),
                    Expanded(
                      child: Text(
                        title!,
                        maxLines: 2,
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(fontSize: 12),
                      ),
                    ),
                  ],
                ),
                const SizedBox(height: 4),
              ],
              Text(
                '第 ${position + 1} / $count 步',
                key: const ValueKey('replay-position'),
                style: const TextStyle(color: Colors.white60, fontSize: 12),
              ),
              const SizedBox(height: 4),
              Text(
                stepText,
                key: const ValueKey('replay-step-text'),
                style: const TextStyle(
                  fontSize: 14,
                  fontWeight: FontWeight.w700,
                  color: Color(0xFFF6D986),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _ReplayControls extends StatelessWidget {
  const _ReplayControls({
    required this.position,
    required this.count,
    required this.playing,
    required this.speed,
    required this.onSeek,
    required this.onTogglePlay,
    required this.onCycleSpeed,
    required this.onPrevious,
    required this.onNext,
  });

  final int position;
  final int count;
  final bool playing;
  final double speed;
  final ValueChanged<int> onSeek;
  final VoidCallback onTogglePlay;
  final VoidCallback onCycleSpeed;
  final VoidCallback? onPrevious;
  final VoidCallback? onNext;

  @override
  Widget build(BuildContext context) {
    return Container(
      key: const ValueKey('replay-controls-panel'),
      padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 6),
      decoration: _panelDecoration,
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Slider(
            key: const ValueKey('replay-slider'),
            value: position.toDouble(),
            max: math.max(1, count - 1).toDouble(),
            divisions: count > 1 ? count - 1 : null,
            onChanged: count > 1 ? (value) => onSeek(value.round()) : null,
          ),
          LayoutBuilder(
            builder: (context, constraints) {
              final previous = IconButton(
                key: const ValueKey('replay-previous'),
                onPressed: onPrevious,
                icon: const Icon(Icons.skip_previous),
                tooltip: '上一步',
              );
              final play = IconButton.filled(
                key: const ValueKey('replay-play'),
                onPressed: onTogglePlay,
                icon: Icon(playing ? Icons.pause : Icons.play_arrow),
                tooltip: playing ? '暂停' : '播放',
              );
              final next = IconButton(
                key: const ValueKey('replay-next'),
                onPressed: onNext,
                icon: const Icon(Icons.skip_next),
                tooltip: '下一步',
              );
              // 手机右栏只有一百来像素宽，三个按钮排不下一行：播放单独一行
              if (constraints.maxWidth < 160) {
                // 竖着的窄窗口里栏宽可能连两个按钮都放不下，那就整体缩一点
                return FittedBox(
                  fit: BoxFit.scaleDown,
                  child: Column(
                    children: [
                      play,
                      Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [previous, next],
                      ),
                    ],
                  ),
                );
              }
              return Row(
                mainAxisAlignment: MainAxisAlignment.spaceEvenly,
                children: [previous, play, next],
              );
            },
          ),
          TextButton(
            key: const ValueKey('replay-speed'),
            onPressed: onCycleSpeed,
            child: Text(
              '速度 ${speed == speed.roundToDouble() ? speed.toInt() : speed}×',
            ),
          ),
        ],
      ),
    );
  }
}

class _ReplayError extends StatelessWidget {
  const _ReplayError({required this.message, required this.onRetry});

  final String message;
  final VoidCallback onRetry;

  @override
  Widget build(BuildContext context) => Center(
    child: Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        const Icon(
          Icons.movie_filter_outlined,
          size: 52,
          color: Colors.white38,
        ),
        const SizedBox(height: 12),
        Text(message, style: const TextStyle(color: Colors.white60)),
        const SizedBox(height: 12),
        OutlinedButton(onPressed: onRetry, child: const Text('重试')),
      ],
    ),
  );
}

String replayErrorMessage(Object error) {
  if (error is GameApiTimeoutException) return '服务器响应超时，请稍后重试';
  if (error is GameApiException) {
    return switch (error.code) {
      'hand_not_found' => '找不到这手牌',
      'replay_unavailable' => '这手牌的记录不完整，无法回放',
      'authentication_required' => '登录已过期，请重新登录',
      _ => '读取回放失败，请稍后重试',
    };
  }
  // 解析失败说明服务端给的数据与客户端对不上，不是网络问题
  if (error is FormatException || error is TypeError) {
    return '回放数据无法识别，请更新客户端后重试';
  }
  return '无法连接游戏服务';
}

bool _atTheEnd(ReplayStep step) =>
    step.kind == 'showdown' || step.kind == 'settle';

/// 把时间轴上的一帧换成牌桌组件能直接显示的快照。
TableSnapshot replaySnapshot(HandReplay replay, int index) {
  final step = replay.steps[index];
  final viewer = replay.players.where((player) => player.isViewer).firstOrNull;
  final twoBoards = step.runoutBoards.length > 1;
  final settled = step.kind == 'settle';
  return TableSnapshot(
    roomId: '',
    roomCode: replay.roomCode,
    ownerUserId: '',
    tableRevision: index,
    phase: settled
        ? 'SETTLEMENT'
        : switch (step.street) {
            'flop' => 'FLOP',
            'turn' => 'TURN',
            'river' => 'RIVER',
            'showdown' => 'SHOWDOWN',
            _ => 'PREFLOP',
          },
    handId: replay.handId,
    dealerSeat: replay.dealerSeat,
    smallBlindSeat: replay.smallBlindSeat,
    board: step.board,
    holeCards: viewer == null
        ? const []
        : step.seat(viewer.userId)?.holeCards ?? const [],
    seats: [
      for (final player in replay.players)
        if (step.seat(player.userId) case final seat?)
          TableSeatSnapshot(
            userId: player.userId,
            displayName: player.displayName,
            seat: player.seat,
            stack: seat.stack,
            ready: false,
            connected: true,
            participating: false,
            folded: seat.folded,
            allIn: seat.allIn,
            streetBet: seat.streetBet,
            totalBet: seat.totalBet,
            position: player.position,
            lastAction: _visibleAction(seat),
            lastCommitted: 0,
            lastActionTo: seat.streetBet,
            timeExtensions: 0,
          ),
    ],
    currentAction: null,
    lastAction: null,
    // 与对局一致：结算时底池显示的是分出去的总额，而不是清空后的 0
    totalPot: settled
        ? step.awards.fold(0, (sum, award) => sum + award.amount)
        : step.pot,
    maxBuyIn: 0,
    // 发两次的两块牌面与分池结果都由结算区显示；分池只在最后一帧出现
    settlement: _atTheEnd(step) || twoBoards
        ? TableSettlement(
            handId: replay.handId,
            showdown: replay.showdown,
            revealedHands: _atTheEnd(step) ? replay.revealedHands : const [],
            potAwards: settled ? step.awards : const [],
            runoutBoards: step.runoutBoards,
            rake: settled ? replay.rake : 0,
          )
        : null,
    voluntaryReveals: const [],
    privateReveals: const [],
    holeCardViewRequests: const [],
    seatSwapRequests: const [],
    runoutChoice: null,
    canShowHoleCards: false,
    autoReadyDeadline: null,
    autoReadyCancelled: false,
  );
}

/// 座位上显示的上一个动作。带金额的动作（跟注、下注、加注、全下、盲注）只在
/// 本街还有投入时才显示：换街之后金额归零，写成「跟注至 0」只会让人看糊涂。
String _visibleAction(ReplaySeat seat) {
  const withoutAmount = {'fold', 'check'};
  if (withoutAmount.contains(seat.lastAction) || seat.streetBet > 0) {
    return seat.lastAction;
  }
  return '';
}

/// 这一帧的座位，按座号排序。本人的牌在玩家框里；摊牌亮出的牌（包括本人的）
/// 只在摊牌与结算两帧按亮牌样式显示，之前的帧不能提前透露最终牌型。
List<TableSeat> replaySeats(HandReplay replay, int index, String userId) {
  final step = replay.steps[index];
  final atTheEnd = _atTheEnd(step);
  final categories = {
    for (final shown in replay.revealedHands) shown.userId: shown.category,
  };
  final players = [...replay.players]
    ..sort((left, right) => left.seat.compareTo(right.seat));
  return [
    for (final player in players)
      if (step.seat(player.userId) case final seat?)
        TableSeat(
          number: player.seat,
          userId: player.userId,
          displayName: player.displayName,
          chips: seat.stack,
          isCurrentUser: player.userId == userId,
          isDealer: player.seat == replay.dealerSeat,
          isFolded: seat.folded,
          isAllIn: seat.allIn,
          position: player.position,
          streetBet: seat.streetBet,
          totalBet: seat.totalBet,
          lastAction: _visibleAction(seat),
          lastActionTo: seat.streetBet,
          holeCards: player.userId == userId ? seat.holeCards : const [],
          revealedCards:
              atTheEnd &&
                  categories.containsKey(player.userId) &&
                  seat.holeCards.isNotEmpty
              ? seat.holeCards
              : const [],
          handCategory: atTheEnd ? categories[player.userId] ?? '' : '',
        ),
  ];
}

/// 一帧的文字说明，显示在控制栏里。
String replayStepLabel(HandReplay replay, ReplayStep step) {
  String name(String userId) {
    final player = replay.player(userId);
    if (player == null) return '玩家';
    final position = player.position.isEmpty ? '' : '（${player.position}）';
    return '${player.isViewer ? '我' : player.displayName}$position';
  }

  switch (step.kind) {
    case 'blinds':
      final small = replay.players
          .where((player) => player.seat == replay.smallBlindSeat)
          .firstOrNull;
      final big = replay.players
          .where((player) => player.seat == replay.bigBlindSeat)
          .firstOrNull;
      return '发牌。${small == null ? '' : '${name(small.userId)} 小盲，'}'
          '${big == null ? '' : '${name(big.userId)} 大盲'}'
          '（${replay.smallBlind}/${replay.bigBlind}）';
    case 'action':
      final seat = step.seat(step.actorId);
      final label = step.action == 'fold' || step.action == 'check'
          ? actionLabel(step.action, 0)
          : actionLabel(step.action, seat?.streetBet ?? step.amount);
      return '${name(step.actorId)} $label'
          '${step.timedOut ? '（超时自动）' : ''}';
    case 'street':
      return switch (step.street) {
        'flop' => '发出翻牌',
        'turn' => '发出转牌',
        'river' => '发出河牌',
        _ => '发牌',
      };
    case 'runout':
      return '全下后发两次公共牌';
    case 'refund':
      return '无人跟注，退回 ${name(step.actorId)} ${step.amount}';
    case 'showdown':
      return '摊牌';
    case 'settle':
      final viewer = replay.players
          .where((player) => player.isViewer)
          .firstOrNull;
      final result = viewer == null
          ? ''
          : '，我 ${viewer.delta >= 0 ? '+' : ''}${viewer.delta}';
      return '结算$result${replay.rake > 0 ? '（本手抽水 ${replay.rake}）' : ''}';
  }
  return step.kind;
}
