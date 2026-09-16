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
  DateTime? _autoReadySubmittedAt;

  /// 提交准备后等待服务端确认的时长；超过仍未就绪则重试。
  /// 服务端曾因把在线玩家误判为断线而取消准备，客户端只提交一次就会
  /// 停在「倒计时走完却不准备」。
  static const Duration autoReadyRetryAfter = Duration(seconds: 3);
  String? _rebuyOfferedHandId;
  final Set<String> _handledRequestIds = {};

  TableSeatSnapshot? _ownSeat(TableSnapshot snapshot) =>
      snapshot.seats.where((seat) => seat.userId == currentUserId).firstOrNull;

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
        !socketJoined) {
      return false;
    }
    final submittedAt = _autoReadySubmittedAt;
    if (_autoReadySubmittedHandId == snapshot.handId &&
        submittedAt != null &&
        serverNow.difference(submittedAt) < autoReadyRetryAfter) {
      return false;
    }
    _autoReadySubmittedHandId = snapshot.handId;
    _autoReadySubmittedAt = serverNow;
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

  /// 被申请者选了「不再接受此人 / 任何人」后，服务端会顺手撤掉同一目标下其余
  /// 排队的申请。本地快照要等广播才更新，而答复的回执先到并触发一次刷新，
  /// 不在这里把那些申请标成已处理，就会弹出一条服务端已经不存在的申请。
  void withdrawAfterDecline({
    required TableSnapshot? snapshot,
    required PendingTableRequest declined,
    required bool holeCards,
    required String scope,
  }) {
    if (snapshot == null || scope == 'once') return;
    final candidates = holeCards
        ? snapshot.holeCardViewRequests
        : snapshot.seatSwapRequests;
    // 答复的那条已经不在快照里（弹窗跨过了开局或结算），服务端会回「申请已失效」
    // 且不会撤回任何申请；这时快照里剩下的可能是下一手的新申请，不能替它们作答。
    if (!candidates.any((c) => c.requestId == declined.requestId)) return;
    for (final candidate in candidates) {
      if (scope == 'everyone' ||
          candidate.requesterUserId == declined.requesterUserId) {
        _handledRequestIds.add(candidate.requestId);
      }
    }
  }

  /// 答复没能发出去（断线重连中）时把这条申请放回去，下一份快照会再弹一次。
  void forgetRequest(String requestId) => _handledRequestIds.remove(requestId);

  /// 取下一条待本人答复的牌桌请求，取出即视为已处理。
  ///
  /// 看手牌申请优先于换位申请：前者只在本手有效，晚一步答复就失去意义。
  ///
  /// [socketJoined] 为 false（断线重连中）时不弹：答复发不出去，弹了只会让玩家
  /// 对着一个怎么点都关不掉的弹窗；重连后的快照会再来触发。
  TableRequestPrompt? takeNextRequest(
    TableSnapshot? snapshot, {
    bool socketJoined = true,
  }) {
    if (snapshot == null || requestDialogOpen || !socketJoined) return null;
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
