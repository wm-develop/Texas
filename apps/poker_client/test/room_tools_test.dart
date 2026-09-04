import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/core/settings/app_settings.dart';
import 'package:poker_client/core/settings/settings_dialog.dart';
import 'package:poker_client/features/table/domain/room_result.dart';
import 'package:poker_client/features/table/domain/table_snapshot.dart';
import 'package:poker_client/features/table/presentation/room_management_dialog.dart';
import 'package:poker_client/features/table/presentation/room_result_dialog.dart';

TableSnapshot _snapshot({
  String phase = 'WAITING_NEXT_HAND',
  bool withSpectator = false,
}) =>
    TableSnapshot.fromJson({
      'roomId': 'room_1',
      'roomCode': '123456',
      'ownerUserId': 'me',
      'tableRevision': 1,
      'phase': phase,
      'handId': phase == 'FLOP' ? 'hand_1' : '',
      'seats': [
        for (final entry in const [
          ['me', '房主', 1],
          ['other', '好友', 2],
        ])
          {
            'userId': entry[0],
            'displayName': entry[1],
            'seat': entry[2],
            'stack': 1500,
            'ready': false,
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
          },
      ],
      if (withSpectator)
        'spectators': [
          {
            'userId': 'watcher',
            'displayName': '观众',
            'connected': true,
            'stack': 800,
            'canSeeHoleCards': false,
            'pendingSeat': false,
          },
        ],
    });

void main() {
  group('本房间战绩', () {
    Future<void> pump(WidgetTester tester, RoomResult result) =>
        tester.pumpWidget(
          MaterialApp(
            home: Scaffold(
              body: RoomResultDialog(loadResult: () async => result),
            ),
          ),
        );

    testWidgets('显示净胜负并按默认比例换算', (tester) async {
      // 带入 2000、桌上 3200：净胜 1200，默认 10 元 = 2000 筹码 → 6 元
      await pump(
        tester,
        const RoomResult(
          boughtIn: 2000,
          returnedToWallet: 0,
          tableChips: 3200,
          net: 1200,
        ),
      );
      await tester.pumpAndSettle();

      expect(find.text('+1200'), findsOneWidget);
      expect(find.text('折合 +6.00 元'), findsOneWidget);
    });

    testWidgets('改换算比例后金额同步变化，净负显示负号', (tester) async {
      await pump(
        tester,
        const RoomResult(
          boughtIn: 2000,
          returnedToWallet: 0,
          tableChips: 1000,
          net: -1000,
        ),
      );
      await tester.pumpAndSettle();
      expect(find.text('-1000'), findsOneWidget);
      expect(find.text('折合 -5.00 元'), findsOneWidget);

      // 改成 20 元 = 2000 筹码
      await tester.enterText(find.byType(TextField).first, '20');
      await tester.pump();
      expect(find.text('折合 -10.00 元'), findsOneWidget);
    });

    testWidgets('比例非法时给出提示而不是显示错误数字', (tester) async {
      await pump(
        tester,
        const RoomResult(
          boughtIn: 2000,
          returnedToWallet: 0,
          tableChips: 3200,
          net: 1200,
        ),
      );
      await tester.pumpAndSettle();
      await tester.enterText(find.byType(TextField).last, '0');
      await tester.pump();
      expect(find.text('请输入大于 0 的金额与筹码'), findsOneWidget);
      expect(find.textContaining('折合'), findsNothing);
    });
  });

  group('房间管理', () {
    Future<void> pump(
      WidgetTester tester, {
      required String phase,
      bool withSpectator = false,
      bool joinLocked = false,
      Future<bool> Function(bool)? onSetJoinLocked,
      Future<void> Function(String)? onRemoveMember,
    }) => tester.pumpWidget(
      MaterialApp(
        home: Scaffold(
          body: RoomManagementDialog(
            snapshot: _snapshot(phase: phase, withSpectator: withSpectator),
            currentUserId: 'me',
            joinLocked: joinLocked,
            onSetJoinLocked: onSetJoinLocked ?? (locked) async => locked,
            onRemoveMember: onRemoveMember ?? (_) async {},
            spectatorSettings: const SpectatorSettings(),
            onUpdateSpectatorSettings: (_) => true,
          ),
        ),
      ),
    );

    testWidgets('手间可以移出玩家，需要二次确认', (tester) async {
      String? removed;
      await pump(
        tester,
        phase: 'WAITING_NEXT_HAND',
        onRemoveMember: (userId) async => removed = userId,
      );
      await tester.pumpAndSettle();

      // 只列出别人，不列自己
      expect(find.text('好友'), findsOneWidget);
      expect(find.text('房主'), findsNothing);

      // 弹窗加了观战位设置后变长，移出按钮可能在可视区之外
      await tester.ensureVisible(find.text('移出'));
      await tester.tap(find.text('移出'));
      await tester.pumpAndSettle();
      expect(find.textContaining('退回他自己的钱包'), findsOneWidget);
      await tester.tap(find.widgetWithText(FilledButton, '移出'));
      await tester.pumpAndSettle();
      expect(removed, 'other');
    });

    testWidgets('观战者也在移出列表里，能被房主移出', (tester) async {
      // 观战者不占座位，只列座位的话房主根本找不到请他出去的入口
      String? removed;
      await pump(
        tester,
        phase: 'WAITING_NEXT_HAND',
        withSpectator: true,
        onRemoveMember: (userId) async => removed = userId,
      );
      await tester.pumpAndSettle();

      expect(find.text('观众'), findsOneWidget);
      expect(find.textContaining('观战 · 筹码 800'), findsOneWidget);
      await tester.ensureVisible(
        find.byKey(const ValueKey('remove-member-watcher')),
      );
      await tester.tap(
        find.descendant(
          of: find.byKey(const ValueKey('remove-member-watcher')),
          matching: find.text('移出'),
        ),
      );
      await tester.pumpAndSettle();
      expect(find.textContaining('确定把「观众」移出房间吗'), findsOneWidget);
      await tester.tap(find.widgetWithText(FilledButton, '移出'));
      await tester.pumpAndSettle();
      expect(removed, 'watcher');
    });

    testWidgets('牌局进行中不能移出玩家', (tester) async {
      // 服务端只在手间允许踢人；界面同步禁用，让不可用状态在点之前就看得见
      await pump(tester, phase: 'FLOP');
      await tester.pumpAndSettle();

      expect(find.text('本手牌进行中，结算后才能移出'), findsOneWidget);
      final button = tester.widget<TextButton>(
        find.widgetWithText(TextButton, '移出'),
      );
      expect(button.onPressed, isNull);
    });

    testWidgets('开关房间入口', (tester) async {
      bool? requested;
      await pump(
        tester,
        phase: 'WAITING',
        onSetJoinLocked: (locked) async {
          requested = locked;
          return locked;
        },
      );
      await tester.pumpAndSettle();

      expect(find.text('其他人可以用房间码加入'), findsOneWidget);
      // 第一个开关是房间入口；后面三个是观战位权限
      await tester.tap(find.byType(Switch).first);
      await tester.pumpAndSettle();
      expect(requested, isTrue);
      expect(find.text('房间入口已关闭，房内玩家不受影响'), findsOneWidget);
    });
  });

  group('设置面板', () {
    testWidgets('条目放不下时可以滚动，且房间管理只对房主显示', (tester) async {
      // 手机横屏的可用高度
      tester.view.physicalSize = const Size(900, 420);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.resetPhysicalSize);
      addTearDown(tester.view.resetDevicePixelRatio);

      final settings = AppSettingsController();
      await tester.pumpWidget(
        MaterialApp(
          home: Builder(
            builder: (context) => Scaffold(
              body: Center(
                child: FilledButton(
                  onPressed: () => showAppSettingsDialog(
                    context,
                    settings,
                    onOpenAdmin: () {},
                    onOpenRoomManagement: () {},
                  ),
                  child: const Text('打开'),
                ),
              ),
            ),
          ),
        ),
      );
      await tester.tap(find.text('打开'));
      await tester.pumpAndSettle();

      expect(tester.takeException(), isNull);
      expect(find.byType(SingleChildScrollView), findsWidgets);

      // 滚到底能看到最后一项
      await tester.drag(
        find.byType(SingleChildScrollView).first,
        const Offset(0, -260),
      );
      await tester.pumpAndSettle();
      expect(find.text('打开服务器管理'), findsOneWidget);
      expect(tester.takeException(), isNull);
    });

    testWidgets('非房主看不到房间管理入口', (tester) async {
      final settings = AppSettingsController();
      await tester.pumpWidget(
        MaterialApp(
          home: Builder(
            builder: (context) => Scaffold(
              body: Center(
                child: FilledButton(
                  onPressed: () =>
                      showAppSettingsDialog(context, settings),
                  child: const Text('打开'),
                ),
              ),
            ),
          ),
        ),
      );
      await tester.tap(find.text('打开'));
      await tester.pumpAndSettle();
      expect(find.byKey(const ValueKey('settings-room-management')), findsNothing);
    });
  });
}
