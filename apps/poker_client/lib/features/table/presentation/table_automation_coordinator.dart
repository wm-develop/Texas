import 'package:poker_client/features/table/domain/table_snapshot.dart';

/// 牌桌上「由快照触发、每手只做一次」的自动行为的判定与去重。
///
/// 从牌桌页抽出的原因是这些规则本身容易出错：自动准备重复提交会打断玩家取消
/// 准备的意图，自动补码重复弹窗会打断牌局，牌桌请求漏标记会反复弹同一条。判定
/// 与记账放在这里可以直接测试；真正的弹窗仍由页面执行，本类不接触 UI。
class TableAutomationCoordinator {
  TableAutomationCoordinator({required this.currentUserId});

  final String currentUserId;

  /// 弹窗期间由页面置位，避免同一时刻叠加多个对话框。
  bool rebuyDialogOpen = false;
  bool requestDialogOpen = false;

  String? _autoReadySubmittedHandId;
  String? _rebuyOfferedHandId;
  final Set<String> _handledRequestIds = {};

  TableSeatSnapshot? _ownSeat(TableSnapshot snapshot) => snapshot.seats
      .where((seat) => seat.userId == currentUserId)
      .firstOrNull;

  /// 自动准备倒计时结束后是否应替本人提交准备。
  ///
  /// 返回 true 时已记录本手，重复调用不会再次返回 true——牌桌时钟每 200 毫秒
  /// 就会调用一次，没有这层记账会持续重发。
  bool shouldSubmitAutoReady({
    required TableSnapshot? snapshot,
    required DateTime serverNow,
    required bool socketJoined,
  }) {
    if (snapshot == null) return false;
    final deadline = snapshot.autoReadyDeadline;
    final ownSeat = _ownSeat(snapshot);
    if (deadline == null ||
        deadline.isAfter(serverNow) ||
        snapshot.autoReadyCancelled ||
        ownSeat == null ||
        ownSeat.ready ||
        // 筹码为 0 的玩家要先补码，替他准备只会立刻被服务端拒绝
        ownSeat.stack <= 0 ||
        !socketJoined ||
        _autoReadySubmittedHandId == snapshot.handId) {
      return false;
    }
    _autoReadySubmittedHandId = snapshot.handId;
    return true;
  }

  /// 本手结算后筹码归零时是否应主动弹出补码。每手最多提示一次。
  bool shouldOfferRebuy(TableSnapshot? snapshot) {
    if (snapshot == null || rebuyDialogOpen) return false;
    final settlementHandId = snapshot.settlement?.handId;
    if (settlementHandId == null || settlementHandId == _rebuyOfferedHandId) {
      return false;
    }
    final ownSeat = _ownSeat(snapshot);
    if (ownSeat == null || ownSeat.stack > 0) return false;
    _rebuyOfferedHandId = settlementHandId;
    return true;
  }

  /// 取下一条待本人答复的牌桌请求，取出即视为已处理。
  ///
  /// 看手牌申请优先于换位申请：前者只在本手有效，晚一步答复就失去意义。
  TableRequestPrompt? takeNextRequest(TableSnapshot? snapshot) {
    if (snapshot == null || requestDialogOpen) return null;
    for (final holeCards in [true, false]) {
      final candidates = holeCards
          ? snapshot.holeCardViewRequests
          : snapshot.seatSwapRequests;
      for (final candidate in candidates) {
        if (_handledRequestIds.add(candidate.requestId)) {
          requestDialogOpen = true;
          return TableRequestPrompt(
            request: candidate,
            holeCards: holeCards,
            requesterName: snapshot.seats
                .where((seat) => seat.userId == candidate.requesterUserId)
                .map((seat) => seat.displayName)
                .firstOrNull,
          );
        }
      }
    }
    return null;
  }
}

/// 一条等待本人答复的牌桌请求及其展示所需信息。
class TableRequestPrompt {
  const TableRequestPrompt({
    required this.request,
    required this.holeCards,
    required this.requesterName,
  });

  final PendingTableRequest request;

  /// true 为查看手牌申请，false 为换位申请。
  final bool holeCards;

  /// 发起者昵称；对方已离桌时为 null，由页面回退为「一名玩家」。
  final String? requesterName;

  String get title => holeCards ? '查看手牌申请' : '换位申请';

  String get description => holeCards
      ? '${requesterName ?? '一名玩家'}已弃牌，申请提前查看你的手牌。是否同意？'
      : '${requesterName ?? '一名玩家'}申请与你交换座位。是否同意？';
}
