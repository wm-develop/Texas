import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:poker_client/core/network/game_api_client.dart';
import 'package:poker_client/core/platform/native_display_cutout.dart';
import 'package:poker_client/features/history/domain/hand_replay.dart';
import 'package:poker_client/features/history/domain/hand_review.dart';
import 'package:poker_client/features/table/domain/hand_category_label.dart';
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
/// 说明与控件放在牌桌两侧对局时信息栏与下注区所在的位置，和牌桌画在同一张
/// 画布里、一起缩放，字号和按钮尺寸与牌桌页的两栏一致；按钮按画布尺寸定得
/// 够大，手机横屏缩到一半左右时仍点得中。
class HandReplayPage extends StatefulWidget {
  const HandReplayPage({
    required this.userId,
    required this.loadReplay,
    this.loadReviewApi,
    this.onReviewDone,
    super.key,
  });

  final String userId;
  final Future<HandReplay> Function() loadReplay;

  /// 这一手有了复盘结果时调用（打开时已有、或在这里分析完成），牌局记录据此
  /// 加标识，不用回去再刷新。
  final VoidCallback? onReviewDone;

  /// 取 AI 复盘接口；返回 null（没开通或服务端没配置）时不显示复盘入口。
  final Future<HandReviewApi?> Function()? loadReviewApi;

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
  final _scaffoldKey = GlobalKey<ScaffoldState>();
  HandReviewApi? _reviewApi;
  HandReview? _review;
  bool _reviewBusy = false;
  String? _reviewError;
  Timer? _reviewPoll;

  /// 每次发起复盘加一：打开回放时那次查询若在发起之后才返回，结果已经过时，
  /// 不能把刚拿到的「排队中」盖回「还没分析」。
  int _reviewEpoch = 0;

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
    _reviewPoll?.cancel();
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
      unawaited(_loadReview(replay));
    } on Object catch (error) {
      if (mounted) setState(() => _error = replayErrorMessage(error));
    } finally {
      if (mounted) setState(() => _loading = false);
    }
  }

  /// 打开回放时看看有没有复盘入口、这一手之前有没有分析过。取不到就当没有：
  /// 复盘是附加功能，不能让它挡住回放本身。
  Future<void> _loadReview(HandReplay replay) async {
    final loadApi = widget.loadReviewApi;
    // 本人这一手一个决定都没做（例如大盲时所有人弃牌）就没有可点评的，不给入口
    final decided = replay.steps.any(
      (step) =>
          step.index > 0 &&
          step.kind == 'action' &&
          step.actorId == widget.userId,
    );
    if (loadApi == null || !decided) return;
    final handId = replay.handId;
    try {
      final api = await loadApi();
      if (!mounted || api == null) return;
      setState(() => _reviewApi = api);
      final epoch = _reviewEpoch;
      final existing = await api.load(handId);
      if (!mounted || epoch != _reviewEpoch) return;
      _setReview(existing);
      if (existing != null && existing.inProgress) _pollReview(handId);
    } on Object {
      // 忽略：入口不显示或显示为「AI 复盘」，点了再报具体原因
    }
  }

  /// 点「AI 复盘」：有结果（包括旧版结果）就只打开面板；正在分析就接着查；
  /// 都没有才发起。[reanalyse] 为真时是旧版结果上的「用新版重新分析」。
  Future<void> _requestReview({bool reanalyse = false}) async {
    final api = _reviewApi;
    final replay = _replay;
    if (api == null || replay == null) return;
    _scaffoldKey.currentState?.openEndDrawer();
    final current = _review;
    if (current != null && current.done && !reanalyse) return;
    if (current != null && current.inProgress) {
      // 已经在分析：只是之前的查询出过错，清掉提示接着查
      setState(() => _reviewError = null);
      _pollReview(replay.handId);
      return;
    }
    _reviewEpoch++;
    setState(() {
      _reviewBusy = true;
      _reviewError = null;
    });
    try {
      final review = await api.request(replay.handId);
      if (!mounted) return;
      _setReview(review);
      if (review.inProgress) _pollReview(replay.handId);
    } on GameApiException catch (error) {
      if (mounted) setState(() => _reviewError = reviewErrorLabel(error.code));
    } on Object {
      if (mounted) setState(() => _reviewError = '无法连接游戏服务');
    } finally {
      if (mounted) setState(() => _reviewBusy = false);
    }
  }

  /// 分析要几十秒到一两分钟，期间每 3 秒问一次，出结果或失败就停。上一次
  /// 问完才排下一次：网络慢时不会同时挂着好几个请求、结果乱序覆盖。
  void _pollReview(String handId) {
    final api = _reviewApi;
    if (api == null) return;
    _reviewPoll?.cancel();
    final epoch = _reviewEpoch;
    _reviewPoll = Timer(const Duration(seconds: 3), () async {
      var keepPolling = true;
      try {
        final latest = await api.load(handId);
        // 途中又发起了一次：这次的结果已经过时，交给新的轮询
        if (!mounted || epoch != _reviewEpoch) return;
        _setReview(latest);
        keepPolling = latest != null && latest.inProgress;
      } on GameApiException catch (error) {
        // 服务端明确拒绝（权限被收回、总开关关了）：再问也一样，停下并说明。
        // 5xx 多半是部署重启时反代的 502；反代返回的是 HTML 或空页，解析不了时
        // 状态码记为 0，这两种都下一轮再问
        final refused = error.statusCode >= 400 && error.statusCode < 500;
        if (refused || error.code == 'review_unavailable') {
          keepPolling = false;
          if (mounted) {
            setState(() => _reviewError = reviewErrorLabel(error.code));
          }
        }
      } on Object {
        // 网络抖动时下一轮再问
      }
      if (mounted && keepPolling && epoch == _reviewEpoch) {
        _pollReview(handId);
      }
    });
  }

  void _setReview(HandReview? value) {
    setState(() => _review = value);
    // 有结果（包括旧版结果、新版失败时带着的旧版）就告诉牌局记录加标识
    if (value != null && (value.done || value.previous != null)) {
      widget.onReviewDone?.call();
    }
  }

  void _jumpToStep(int step) {
    _scaffoldKey.currentState?.closeEndDrawer();
    _pause();
    _goTo(step);
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

  /// 回放停在本人操作过、且有点评的那一步时，打开面板就定位到这一步。
  int? _focusStep() {
    final replay = _replay;
    final review = _review;
    if (replay == null || review == null || replay.steps.isEmpty) return null;
    final step = replay.steps[_index.clamp(0, replay.steps.length - 1)];
    return review.decisionAt(step.index) == null ? null : step.index;
  }

  String _reviewButtonLabel() {
    final review = _review;
    if (review == null) return 'AI 复盘';
    if (review.done) return '查看 AI 复盘';
    if (review.inProgress) return 'AI 分析中…';
    return 'AI 复盘（重试）';
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
      key: _scaffoldKey,
      appBar: shortScreen && replay != null
          ? null
          // 有复盘抽屉时 AppBar 会自己加一个菜单键，入口已经在控制栏里了
          : AppBar(title: Text(title), automaticallyImplyActions: false),
      // 手机横屏时右侧是控制栏，从右边缘滑动很容易误开抽屉
      endDrawerEnableOpenDragGesture: false,
      // 复盘结果放在右侧抽屉里：手机横屏高度紧，底部面板放不下几段分析
      endDrawer: _reviewApi == null || replay == null
          ? null
          : Drawer(
              width: math.min(440, mediaSize.width * 0.9),
              child: SafeArea(
                child: _ReviewPanel(
                  replay: replay,
                  review: _review,
                  busy: _reviewBusy,
                  error: _reviewError,
                  onStart: _requestReview,
                  onReanalyse: () => _requestReview(reanalyse: true),
                  onJump: _jumpToStep,
                  focusStep: _focusStep(),
                ),
              ),
            ),
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
                : _buildReplay(context, replay, showHeader: shortScreen),
          ),
        ),
      ),
    );
  }

  Widget _buildReplay(
    BuildContext context,
    HandReplay replay, {
    required bool showHeader,
  }) {
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
        final seats = replaySeats(replay, _index, widget.userId);
        final anchor = seats.indexWhere((seat) => seat.isCurrentUser);
        final info = _ReplayInfo(
          title: showHeader ? '牌局回放' : null,
          subtitle: showHeader ? '房间 ${replay.roomCode}' : null,
          stepText: replayStepLabel(replay, step),
          position: _index,
          count: replay.steps.length,
          decision: _review?.decisionAt(step.index),
          onOpenReview: () => _scaffoldKey.currentState?.openEndDrawer(),
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
          reviewLabel: _reviewApi == null ? null : _reviewButtonLabel(),
          onReview: _requestReview,
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
                      // 两侧栏与牌桌画在同一张画布里、随牌桌一起缩放，与牌桌页
                      // 的两栏同一套尺寸：放在画布外面时字号和按钮不缩，手机上
                      // 会比玩家框里的字大出一截。手机：说明在左栏，操作在右栏；
                      // 大屏：都在右栏，说明在上、操作贴底。
                      if (compact) ...[
                        Positioned(
                          left: 8,
                          top: 8,
                          bottom: 8,
                          width: TableViewportLayout.compactLeftRailWidth - 16,
                          child: info,
                        ),
                        Positioned(
                          right: 8,
                          bottom: 8,
                          width: TableViewportLayout.compactRightRailWidth - 16,
                          child: controls,
                        ),
                      ] else
                        Positioned(
                          right: 16,
                          top: 16,
                          bottom: 18,
                          width: TableViewportLayout.betRailWidth - 32,
                          child: Column(
                            crossAxisAlignment: CrossAxisAlignment.stretch,
                            children: [
                              Expanded(child: info),
                              const SizedBox(height: 8),
                              controls,
                            ],
                          ),
                        ),
                    ],
                  ),
                ),
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
    required this.subtitle,
    required this.stepText,
    required this.position,
    required this.count,
    this.decision,
    this.onOpenReview,
  });

  /// 这一步的 AI 点评；没有时为空。
  final ReviewDecision? decision;
  final VoidCallback? onOpenReview;

  /// 没有标题栏时（手机横屏）才有：连同返回按钮放在这里。
  final String? title;
  final String? subtitle;
  final String stepText;
  final int position;
  final int count;

  @override
  Widget build(BuildContext context) {
    return Align(
      alignment: Alignment.topLeft,
      child: Container(
        key: const ValueKey('replay-info-panel'),
        padding: const EdgeInsets.all(9),
        decoration: _panelDecoration,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              // 字号与牌桌页左栏的房间信息一致
              if (title != null) ...[
                Row(
                  children: [
                    IconButton(
                      key: const ValueKey('replay-back'),
                      onPressed: () => Navigator.of(context).maybePop(),
                      icon: const Icon(Icons.arrow_back, size: 22),
                      tooltip: '返回',
                      visualDensity: VisualDensity.compact,
                      constraints: const BoxConstraints.tightFor(
                        width: 36,
                        height: 36,
                      ),
                      padding: EdgeInsets.zero,
                    ),
                    const SizedBox(width: 4),
                    Expanded(
                      child: Text(
                        title!,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(
                          fontSize: 16,
                          fontWeight: FontWeight.w800,
                        ),
                      ),
                    ),
                  ],
                ),
                if (subtitle case final subtitle?)
                  Text(
                    subtitle,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(color: Colors.white70, fontSize: 12),
                  ),
                const Divider(height: 14),
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
              if (decision case final decision?) ...[
                const SizedBox(height: 6),
                InkWell(
                  key: const ValueKey('replay-step-review'),
                  onTap: onOpenReview,
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      VerdictChip(verdict: decision.verdict),
                      const SizedBox(height: 2),
                      Text(
                        decision.reasoning,
                        maxLines: 3,
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(
                          fontSize: 12,
                          color: Colors.white70,
                        ),
                      ),
                    ],
                  ),
                ),
              ],
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
    this.reviewLabel,
    this.onReview,
  });

  /// 复盘入口的文字；为空时不显示入口。
  final String? reviewLabel;
  final VoidCallback? onReview;

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
      padding: const EdgeInsets.all(8),
      decoration: _panelDecoration,
      // 尺寸按牌桌页右栏的下注按钮（高 56、字号 14）来定，随牌桌一起缩放
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
                iconSize: 30,
                icon: const Icon(Icons.skip_previous),
                tooltip: '上一步',
              );
              final play = IconButton.filled(
                key: const ValueKey('replay-play'),
                onPressed: onTogglePlay,
                iconSize: 32,
                style: IconButton.styleFrom(minimumSize: const Size.square(60)),
                icon: Icon(playing ? Icons.pause : Icons.play_arrow),
                tooltip: playing ? '暂停' : '播放',
              );
              final next = IconButton(
                key: const ValueKey('replay-next'),
                onPressed: onNext,
                iconSize: 30,
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
          if (reviewLabel case final label?) ...[
            const SizedBox(height: 6),
            FilledButton.tonalIcon(
              key: const ValueKey('replay-review'),
              onPressed: onReview,
              style: FilledButton.styleFrom(
                minimumSize: const Size(0, 60),
                padding: const EdgeInsets.symmetric(horizontal: 10),
              ),
              icon: const Icon(Icons.psychology_alt_outlined, size: 20),
              label: FittedBox(fit: BoxFit.scaleDown, child: Text(label)),
            ),
          ],
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

/// 点评结论的色块：好、合理、有争议、失误。
class VerdictChip extends StatelessWidget {
  const VerdictChip({required this.verdict, super.key});

  final String verdict;

  @override
  Widget build(BuildContext context) {
    final color = switch (verdict) {
      '好' => const Color(0xFF6DE0A4),
      '合理' => const Color(0xFF8EC5FF),
      '失误' => const Color(0xFFE07A72),
      _ => const Color(0xFFF6C35B),
    };
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.18),
        border: Border.all(color: color),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Text(
        'AI：$verdict',
        style: TextStyle(
          color: color,
          fontSize: 11,
          fontWeight: FontWeight.w700,
        ),
      ),
    );
  }
}

/// 复盘抽屉：分析中、失败、结果三种状态。点一条逐步点评就关抽屉并跳到那一步。
class _ReviewPanel extends StatefulWidget {
  const _ReviewPanel({
    required this.replay,
    required this.review,
    required this.busy,
    required this.error,
    required this.onStart,
    required this.onReanalyse,
    required this.onJump,
    this.focusStep,
  });

  final HandReplay replay;
  final HandReview? review;
  final bool busy;
  final String? error;
  final VoidCallback onStart;

  /// 旧版结果上的「用新版重新分析」。
  final VoidCallback onReanalyse;
  final ValueChanged<int> onJump;

  /// 打开面板时回放停在本人操作过、且有点评的那一步：滚到这一步的点评并高亮。
  final int? focusStep;

  @override
  State<_ReviewPanel> createState() => _ReviewPanelState();
}

class _ReviewPanelState extends State<_ReviewPanel> {
  final _cardKeys = <int, GlobalKey>{};

  @override
  void initState() {
    super.initState();
    _scrollToFocus();
  }

  @override
  void didUpdateWidget(covariant _ReviewPanel oldWidget) {
    super.didUpdateWidget(oldWidget);
    // 面板开着时结果才出来：出来那一刻再滚一次
    if (oldWidget.review?.result == null && widget.review?.result != null) {
      _scrollToFocus();
    }
  }

  void _scrollToFocus() {
    final step = widget.focusStep;
    if (step == null) return;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      final target = _cardKeys[step]?.currentContext;
      if (!mounted || target == null) return;
      Scrollable.ensureVisible(
        target,
        alignment: 0.1,
        duration: const Duration(milliseconds: 250),
      );
    });
  }

  @override
  Widget build(BuildContext context) {
    final review = widget.review;
    final busy = widget.busy;
    final error = widget.error;
    // 用 SingleChildScrollView 而不是 ListView：ListView 只建出看得见的那几张，
    // 屏幕外的卡片拿不到位置，没法滚过去
    return SingleChildScrollView(
      key: const ValueKey('review-panel'),
      padding: const EdgeInsets.all(16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Row(
            children: [
              const Expanded(
                child: Text(
                  'AI 复盘',
                  style: TextStyle(fontSize: 18, fontWeight: FontWeight.w800),
                ),
              ),
              IconButton(
                onPressed: () => Scaffold.of(context).closeEndDrawer(),
                icon: const Icon(Icons.close),
                tooltip: '关闭',
              ),
            ],
          ),
          const SizedBox(height: 8),
          if (review != null && review.done) ...[
            // 已有结果（包括旧版）：出错时照样显示结果，错误写在上面
            if (error != null)
              _errorLine(
                error,
                review.outdated ? widget.onReanalyse : widget.onStart,
              ),
            if (review.outdated)
              Card(
                key: const ValueKey('review-outdated'),
                color: const Color(0x33F6D986),
                child: Padding(
                  padding: const EdgeInsets.all(10),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      const Text(
                        '这是旧版复盘的结果。复盘已经升级，可以用新版重新分析（会占用一次次数）。',
                        style: TextStyle(fontSize: 12),
                      ),
                      const SizedBox(height: 6),
                      OutlinedButton(
                        key: const ValueKey('review-reanalyse'),
                        onPressed: busy ? null : widget.onReanalyse,
                        child: const Text('用新版重新分析'),
                      ),
                    ],
                  ),
                ),
              ),
            ..._resultSections(review.result!, focusStep: widget.focusStep),
            const SizedBox(height: 16),
            Text(
              '由 ${review.model.isEmpty ? 'AI' : review.model} 生成，仅供参考。',
              style: const TextStyle(color: Colors.white38, fontSize: 11),
            ),
          ] else if (error != null) ...[
            _errorLine(error, widget.onStart),
            ..._previousSections(review),
          ] else if (busy || review == null) ...[
            const Text(
              '让 AI 以专业教练的视角点评你在这一手里的每个决策，并结合对手在你们同桌牌局里的打法倾向。',
              style: TextStyle(color: Colors.white70),
            ),
            const SizedBox(height: 12),
            FilledButton(
              key: const ValueKey('review-start'),
              onPressed: busy ? null : widget.onStart,
              child: Text(busy ? '正在提交…' : '开始分析'),
            ),
            // 失败后点「重新分析」、正在提交的这一下，旧版结果不要闪掉
            ..._previousSections(review),
          ] else if (review.inProgress) ...[
            const Center(child: CircularProgressIndicator()),
            const SizedBox(height: 12),
            Text(
              review.attempts > 0
                  ? '大模型这次没有完成分析（服务繁忙或超时），正在自动重试（第 ${review.attempts} 次）。'
                        '可以离开这里做别的，分析在服务器上继续，结果会保存下来。'
                  : '正在分析，通常需要一到两分钟。可以离开这里做别的，'
                        '分析在服务器上继续，结果会保存下来，下次打开这手回放就能看到。',
              key: const ValueKey('review-progress'),
              textAlign: TextAlign.center,
              style: const TextStyle(color: Colors.white70),
            ),
            ..._previousSections(review),
          ] else ...[
            Text(
              reviewErrorLabel(review.failure),
              style: const TextStyle(color: Colors.redAccent),
            ),
            const SizedBox(height: 8),
            OutlinedButton(
              key: const ValueKey('review-retry'),
              onPressed: widget.onStart,
              child: const Text('重新分析'),
            ),
            ..._previousSections(review),
          ],
        ],
      ),
    );
  }

  Widget _errorLine(String error, VoidCallback onRetry) => Padding(
    padding: const EdgeInsets.only(bottom: 8),
    child: Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          error,
          key: const ValueKey('review-error'),
          style: const TextStyle(color: Colors.redAccent),
        ),
        const SizedBox(height: 6),
        OutlinedButton(
          key: const ValueKey('review-error-retry'),
          onPressed: widget.busy ? null : onRetry,
          child: const Text('重试'),
        ),
      ],
    ),
  );

  /// 新版还没有结果（排队、分析中、失败）时，下面接着显示旧版的结果。
  List<Widget> _previousSections(HandReview? review) {
    final previous = review?.previous;
    if (previous == null) return const [];
    return [
      const SizedBox(height: 16),
      const Text(
        '以下是旧版复盘的结果',
        key: ValueKey('review-previous'),
        style: TextStyle(fontSize: 12, color: Colors.white60),
      ),
      ..._resultSections(previous),
    ];
  }

  List<Widget> _resultSections(ReviewResult result, {int? focusStep}) => [
    _section('总评'),
    Text(result.summary),
    if (result.decisions.isNotEmpty) ...[
      _section('逐步点评'),
      for (final decision in result.decisions)
        _decisionCard(decision, decision.step == focusStep),
    ],
    if (result.keyLessons.isNotEmpty) ...[
      _section('要点'),
      for (final lesson in result.keyLessons) Text('· $lesson'),
    ],
    if (result.opponentNotes.isNotEmpty) ...[
      _section('对手倾向'),
      for (final note in result.opponentNotes) Text('· $note'),
    ],
    if (result.hindsight.isNotEmpty) ...[
      _section('结果回顾'),
      Text(result.hindsight),
    ],
  ];

  Widget _decisionCard(ReviewDecision decision, bool focused) {
    final key = _cardKeys.putIfAbsent(decision.step, GlobalKey.new);
    return Card(
      key: key,
      margin: const EdgeInsets.only(bottom: 8),
      shape: focused
          ? RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(12),
              side: const BorderSide(color: Color(0xFFF6D986), width: 2),
            )
          : null,
      child: InkWell(
        key: ValueKey('review-decision-${decision.step}'),
        onTap: () => widget.onJump(decision.step),
        child: Padding(
          padding: const EdgeInsets.all(10),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(
                children: [
                  VerdictChip(verdict: decision.verdict),
                  const SizedBox(width: 6),
                  Expanded(
                    child: Text(
                      '第 ${decision.step + 1} 步 · ${_stepLabel(decision.step)}',
                      style: const TextStyle(
                        fontSize: 12,
                        color: Colors.white60,
                      ),
                    ),
                  ),
                  if (focused)
                    const Text(
                      '当前步',
                      key: ValueKey('review-focused'),
                      style: TextStyle(fontSize: 11, color: Color(0xFFF6D986)),
                    ),
                ],
              ),
              if (decision.facts case final facts?) ...[
                const SizedBox(height: 4),
                _DecisionNumbers(facts: facts),
              ],
              const SizedBox(height: 4),
              Text(decision.reasoning),
              if (decision.bestAction.isNotEmpty) ...[
                const SizedBox(height: 4),
                Text(
                  '最佳行动：${decision.bestAction}',
                  key: ValueKey('review-best-${decision.step}'),
                  style: const TextStyle(color: Color(0xFFF6D986)),
                ),
              ],
              if (_estimateLine(decision) case final line?) ...[
                const SizedBox(height: 4),
                Text(
                  line,
                  key: ValueKey('review-estimate-${decision.step}'),
                  style: const TextStyle(fontSize: 12, color: Colors.white70),
                ),
              ],
            ],
          ),
        ),
      ),
    );
  }

  String _stepLabel(int step) {
    final replay = widget.replay;
    if (step < 0 || step >= replay.steps.length) return '';
    return replayStepLabel(replay, replay.steps[step]);
  }

  /// 模型估算的胜率与期望收益，明确标成估算。
  String? _estimateLine(ReviewDecision decision) {
    final parts = <String>[];
    if (decision.equityVsRange case final equity?) {
      parts.add('对对手范围胜率约 ${equity.toStringAsFixed(0)}%');
    }
    final taken = decision.evTaken;
    final best = decision.evBest;
    if (taken != null && best != null) {
      parts.add('本次 EV ${reviewEvLabel(taken)}');
      if (best - taken >= 0.05) {
        parts.add(
          '最佳 EV ${reviewEvLabel(best)}（多 ${reviewEvLabel(best - taken)}）',
        );
      }
    }
    if (parts.isEmpty) return null;
    return 'AI 估算：${parts.join(' · ')}';
  }

  Widget _section(String title) => Padding(
    padding: const EdgeInsets.only(top: 14, bottom: 6),
    child: Text(
      title,
      style: const TextStyle(
        fontWeight: FontWeight.w800,
        color: Color(0xFFF6D986),
      ),
    ),
  );
}

/// 一个决策点的精确数字：底池、需跟注与所需胜率、SPR、牌力与对随机手牌胜率。
class _DecisionNumbers extends StatelessWidget {
  const _DecisionNumbers({required this.facts});

  final ReviewDecisionFacts facts;

  @override
  Widget build(BuildContext context) {
    final pot = facts.winnablePot > 0
        ? '底池 ${facts.potBefore}（你最多能赢 ${facts.winnablePot}）'
        : '底池 ${facts.potBefore}';
    final call = facts.toCall > 0
        ? '需跟注 ${facts.toCall}，至少要 ${facts.potOdds?.toStringAsFixed(1) ?? '—'}% 胜率'
        : '无需跟注';
    final lines = [
      '$pot · $call · SPR ${facts.stackToPotRatio.toStringAsFixed(1)}',
      if (facts.madeHand.isNotEmpty)
        '牌力：${handCategoryLabel(facts.madeHand)}（${facts.bestFive.map(reviewCardLabel).join(' ')}）'
            '${facts.boardPlays ? '，公共牌本身就是这个牌力' : '，用到底牌 ${facts.holeCardsUsed.map(reviewCardLabel).join(' ')}'}'
            ' · 对随机手牌胜率 ${facts.equityVsRandom.toStringAsFixed(0)}%'
      else
        '对随机手牌胜率 ${facts.equityVsRandom.toStringAsFixed(0)}%',
    ];
    return Text(
      lines.join('\n'),
      style: const TextStyle(fontSize: 12, color: Colors.white60, height: 1.4),
    );
  }
}
