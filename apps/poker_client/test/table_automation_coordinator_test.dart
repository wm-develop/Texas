import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/features/table/domain/table_snapshot.dart';
import 'package:poker_client/features/table/presentation/table_automation_coordinator.dart';

final _now = DateTime.utc(2026, 9, 2, 12);

Map<String, dynamic> _seat(
  String userId, {
  int stack = 1000,
  bool ready = false,
  String displayName = '玩家',
}) => {
  'userId': userId,
  'displayName': displayName,
  'seat': 1,
  'stack': stack,
  'ready': ready,
  'connected': true,
  'participating': true,
  'folded': false,
  'allIn': false,
  'streetBet': 0,
  'totalBet': 0,
  'position': '',
  'lastAction': '',
  'lastCommitted': 0,
  'lastActionTo': 0,
  'timeExtensions': 0,
};

TableSnapshot _snapshot({
  String handId = 'hand_1',
  List<Map<String, dynamic>>? seats,
  DateTime? autoReadyDeadline,
  bool autoReadyCancelled = false,
  String? settlementHandId,
  List<Map<String, String>> holeCardViewRequests = const [],
  List<Map<String, String>> seatSwapRequests = const [],
}) => TableSnapshot.fromJson({
  'roomId': 'room_1',
  'roomCode': '123456',
  'tableRevision': 1,
  'phase': 'SETTLED',
  'handId': handId,
  'seats': seats ?? [_seat('me')],
  'autoReadyDeadline': autoReadyDeadline?.millisecondsSinceEpoch ?? 0,
  'autoReadyCancelled': autoReadyCancelled,
  'holeCardViewRequests': holeCardViewRequests,
  'seatSwapRequests': seatSwapRequests,
  if (settlementHandId != null)
    'settlement': {
      'handId': settlementHandId,
      'showdown': false,
      'revealedHands': <dynamic>[],
      'potAwards': <dynamic>[],
    },
});

Map<String, String> _request(String requestId, {String from = 'other'}) => {
  'requestId': requestId,
  'requesterUserId': from,
  'targetUserId': 'me',
};

TableAutomationCoordinator _coordinator() =>
    TableAutomationCoordinator(currentUserId: 'me');

bool _autoReady(
  TableAutomationCoordinator coordinator,
  TableSnapshot? snapshot, {
  bool socketJoined = true,
}) => coordinator.shouldSubmitAutoReady(
  snapshot: snapshot,
  serverNow: _now,
  socketJoined: socketJoined,
);

void main() {
  group('自动准备', () {
    test('倒计时结束后提交一次，之后不再重复提交', () {
      final coordinator = _coordinator();
      final snapshot = _snapshot(
        autoReadyDeadline: _now.subtract(const Duration(seconds: 1)),
      );
      expect(_autoReady(coordinator, snapshot), isTrue);
      // 牌桌时钟每 200 毫秒调用一次，第二次起必须沉默
      expect(_autoReady(coordinator, snapshot), isFalse);
      expect(_autoReady(coordinator, snapshot), isFalse);
    });

    test('提交后长时间仍未就绪则重试，玩家主动取消时不重试', () {
      // 服务端曾把在线玩家误判为断线并取消准备；客户端只提交一次就会
      // 停在「倒计时走完却不准备」。
      final coordinator = _coordinator();
      final deadline = _now.subtract(const Duration(seconds: 1));
      expect(
        _autoReady(coordinator, _snapshot(autoReadyDeadline: deadline)),
        isTrue,
      );
      final later = _now.add(TableAutomationCoordinator.autoReadyRetryAfter);
      expect(
        coordinator.shouldSubmitAutoReady(
          snapshot: _snapshot(autoReadyDeadline: deadline),
          serverNow: later,
          socketJoined: true,
        ),
        isTrue,
        reason: '超过重试间隔仍未就绪，应再提交一次',
      );
      expect(
        coordinator.shouldSubmitAutoReady(
          snapshot: _snapshot(
            autoReadyDeadline: deadline,
            autoReadyCancelled: true,
          ),
          serverNow: later.add(TableAutomationCoordinator.autoReadyRetryAfter),
          socketJoined: true,
        ),
        isFalse,
        reason: '玩家主动取消的意图必须被尊重',
      );
    });

    test('下一手重新计算', () {
      final coordinator = _coordinator();
      final deadline = _now.subtract(const Duration(seconds: 1));
      expect(
        _autoReady(coordinator, _snapshot(autoReadyDeadline: deadline)),
        isTrue,
      );
      expect(
        _autoReady(
          coordinator,
          _snapshot(handId: 'hand_2', autoReadyDeadline: deadline),
        ),
        isTrue,
      );
    });

    test('倒计时未到、玩家已取消或已准备时都不提交', () {
      final future = _now.add(const Duration(seconds: 5));
      final past = _now.subtract(const Duration(seconds: 1));
      expect(
        _autoReady(_coordinator(), _snapshot(autoReadyDeadline: future)),
        isFalse,
      );
      expect(
        _autoReady(
          _coordinator(),
          _snapshot(autoReadyDeadline: past, autoReadyCancelled: true),
        ),
        isFalse,
      );
      expect(
        _autoReady(
          _coordinator(),
          _snapshot(
            autoReadyDeadline: past,
            seats: [_seat('me', ready: true)],
          ),
        ),
        isFalse,
      );
      expect(_autoReady(_coordinator(), _snapshot()), isFalse);
      expect(_autoReady(_coordinator(), null), isFalse);
    });

    test('筹码为 0 或连接未就绪时不提交', () {
      final past = _now.subtract(const Duration(seconds: 1));
      expect(
        _autoReady(
          _coordinator(),
          _snapshot(autoReadyDeadline: past, seats: [_seat('me', stack: 0)]),
        ),
        isFalse,
      );
      expect(
        _autoReady(
          _coordinator(),
          _snapshot(autoReadyDeadline: past),
          socketJoined: false,
        ),
        isFalse,
      );
    });

    test('自己不在座位上时不提交', () {
      expect(
        _autoReady(
          _coordinator(),
          _snapshot(
            autoReadyDeadline: _now.subtract(const Duration(seconds: 1)),
            seats: [_seat('someone_else')],
          ),
        ),
        isFalse,
      );
    });
  });

  group('自动补码', () {
    test('结算后筹码归零提示一次，同一手不再提示', () {
      final coordinator = _coordinator();
      final snapshot = _snapshot(
        settlementHandId: 'hand_1',
        seats: [_seat('me', stack: 0)],
      );
      expect(coordinator.shouldOfferRebuy(snapshot), isTrue);
      expect(coordinator.shouldOfferRebuy(snapshot), isFalse);

      // 下一手再次归零则重新提示
      expect(
        coordinator.shouldOfferRebuy(
          _snapshot(
            handId: 'hand_2',
            settlementHandId: 'hand_2',
            seats: [_seat('me', stack: 0)],
          ),
        ),
        isTrue,
      );
    });

    test('还有筹码、尚未结算或弹窗已打开时不提示', () {
      expect(
        _coordinator().shouldOfferRebuy(
          _snapshot(settlementHandId: 'hand_1', seats: [_seat('me')]),
        ),
        isFalse,
      );
      expect(
        _coordinator().shouldOfferRebuy(_snapshot(seats: [_seat('me', stack: 0)])),
        isFalse,
      );
      final busy = _coordinator()..rebuyDialogOpen = true;
      expect(
        busy.shouldOfferRebuy(
          _snapshot(settlementHandId: 'hand_1', seats: [_seat('me', stack: 0)]),
        ),
        isFalse,
      );
      expect(_coordinator().shouldOfferRebuy(null), isFalse);
    });
  });

  group('牌桌请求', () {
    test('看手牌申请优先于换位申请', () {
      final coordinator = _coordinator();
      final snapshot = _snapshot(
        holeCardViewRequests: [_request('view_1')],
        seatSwapRequests: [_request('swap_1')],
      );
      final first = coordinator.takeNextRequest(snapshot)!;
      expect(first.holeCards, isTrue);
      expect(first.request.requestId, 'view_1');

      // 弹窗未关闭前不取下一条
      expect(coordinator.takeNextRequest(snapshot), isNull);
      coordinator.requestDialogOpen = false;

      final second = coordinator.takeNextRequest(snapshot)!;
      expect(second.holeCards, isFalse);
      expect(second.request.requestId, 'swap_1');
      coordinator.requestDialogOpen = false;

      // 两条都已答复，快照仍带着它们也不再重复弹出
      expect(coordinator.takeNextRequest(snapshot), isNull);
    });

    test('提示文案带上发起者昵称，找不到时回退', () {
      final coordinator = _coordinator();
      final prompt = coordinator.takeNextRequest(
        _snapshot(
          seats: [_seat('me'), _seat('other', displayName: '老王')],
          holeCardViewRequests: [_request('view_1')],
        ),
      )!;
      expect(prompt.title, '查看手牌申请');
      expect(prompt.description, contains('老王'));
      coordinator.requestDialogOpen = false;

      final swap = _coordinator().takeNextRequest(
        _snapshot(seatSwapRequests: [_request('swap_1', from: 'ghost')]),
      )!;
      expect(swap.title, '换位申请');
      expect(swap.description, contains('一名玩家'));
    });

    test('没有待处理请求时返回 null 且不占用弹窗', () {
      final coordinator = _coordinator();
      expect(coordinator.takeNextRequest(_snapshot()), isNull);
      expect(coordinator.takeNextRequest(null), isNull);
      expect(coordinator.requestDialogOpen, isFalse);
    });
  });
}
