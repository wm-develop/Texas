import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/core/auth/auth_session.dart';
import 'package:poker_client/core/settings/app_settings.dart';
import 'package:poker_client/features/bankroll/domain/bankroll_entry.dart';
import 'package:poker_client/features/bankroll/domain/bankroll_snapshot.dart';
import 'package:poker_client/features/lobby/domain/friend_room.dart';
import 'package:poker_client/features/lobby/presentation/lobby_page.dart';
import 'package:poker_client/features/table/domain/rake_settings.dart';

void main() {
  testWidgets('uses a compact two-column lobby on a landscape phone', (
    tester,
  ) async {
    tester.view.devicePixelRatio = 1;
    tester.view.physicalSize = const Size(900, 430);
    addTearDown(() {
      tester.view.resetDevicePixelRatio();
      tester.view.resetPhysicalSize();
    });

    await tester.pumpWidget(const MaterialApp(home: _LobbyHarness()));
    await tester.pumpAndSettle();

    final joinTitle = tester.getTopLeft(find.text('加入朋友的牌桌'));
    final createTitle = tester.getTopLeft(find.text('创建好友牌桌'));
    expect((joinTitle.dy - createTitle.dy).abs(), lessThan(4));
    expect(createTitle.dx, greaterThan(joinTitle.dx));
    expect(tester.getBottomRight(find.text('创建')).dy, lessThan(430));
    expect(tester.takeException(), isNull);
  });

  testWidgets('top-up closes its dialog safely and refreshes the balance', (
    tester,
  ) async {
    await tester.pumpWidget(const MaterialApp(home: _LobbyHarness()));

    await tester.tap(find.text('总筹码 0'));
    await tester.pumpAndSettle();
    final dialogField = find.descendant(
      of: find.byType(AlertDialog),
      matching: find.byType(TextField),
    );
    await tester.enterText(dialogField, '1000');
    await tester.pump();
    await tester.tap(find.text('确认充值'));
    await tester.pumpAndSettle();

    expect(tester.takeException(), isNull);
    expect(find.text('总筹码 1000'), findsOneWidget);
    expect(find.text('充值成功，增加 1000 筹码'), findsOneWidget);
  });

  testWidgets('chip amount dialog keeps its field and action above keyboard', (
    tester,
  ) async {
    tester.view.devicePixelRatio = 1;
    tester.view.physicalSize = const Size(900, 430);
    addTearDown(() {
      tester.view.resetDevicePixelRatio();
      tester.view.resetPhysicalSize();
      tester.view.resetViewInsets();
    });

    await tester.pumpWidget(const MaterialApp(home: _LobbyHarness()));
    await tester.tap(find.text('总筹码 0'));
    await tester.pumpAndSettle();

    final field = find.descendant(
      of: find.byType(AlertDialog),
      matching: find.byType(TextField),
    );
    await tester.tap(field);
    tester.view.viewInsets = const FakeViewPadding(bottom: 220);
    await tester.pumpAndSettle();

    final visibleBottom =
        tester.view.physicalSize.height - tester.view.viewInsets.bottom;
    expect(field.hitTestable(), findsOneWidget);
    expect(find.text('确认充值').hitTestable(), findsOneWidget);
    expect(
      tester.getBottomRight(find.text('确认充值')).dy,
      lessThanOrEqualTo(visibleBottom),
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets('shows auditable entertainment chip entries', (tester) async {
    await tester.pumpWidget(
      MaterialApp(
        home: _LobbyHarness(
          entries: [
            BankrollEntry(
              entryId: 'bank_1',
              reason: 'virtual_top_up',
              walletDelta: 2000,
              tableDelta: 0,
              walletBalanceAfter: 2000,
              tableBalanceAfter: 0,
              createdAt: DateTime.utc(2026, 8, 25, 10),
            ),
          ],
        ),
      ),
    );

    await tester.tap(find.byTooltip('筹码流水'));
    await tester.pumpAndSettle();

    expect(find.text('娱乐筹码流水'), findsOneWidget);
    expect(find.text('虚拟充值'), findsOneWidget);
    expect(find.text('钱包 +2000'), findsOneWidget);
  });

  testWidgets('带入窗口写明房间的抽水；横屏手机弹出键盘后输入框与按钮仍然够得着', (tester) async {
    tester.view.devicePixelRatio = 1;
    tester.view.physicalSize = const Size(900, 430);
    addTearDown(() {
      tester.view.resetDevicePixelRatio();
      tester.view.resetPhysicalSize();
      tester.view.resetViewInsets();
    });
    await tester.pumpWidget(
      const MaterialApp(
        home: _LobbyHarness(
          preview: RoomPreview(
            code: '123456',
            rules: RoomRules(
              startingChips: 1000,
              maxBuyIn: 2000,
              smallBlind: 10,
              bigBlind: 20,
              actionSeconds: 30,
            ),
            maxPlayers: 10,
            currentPlayers: 3,
            passwordRequired: false,
            rake: RakeSettings(enabled: true, basisPoints: 500, cap: 50),
          ),
        ),
      ),
    );
    await tester.pumpAndSettle();
    await tester.enterText(find.byType(TextField).first, '123456');
    await tester.tap(find.text('加入牌桌'));
    await tester.pumpAndSettle();
    expect(find.textContaining('本房间抽水：5%，最多 50'), findsOneWidget);

    final field = find.descendant(
      of: find.byType(AlertDialog),
      matching: find.byType(TextField),
    );
    await tester.tap(field);
    tester.view.viewInsets = const FakeViewPadding(bottom: 220);
    await tester.pumpAndSettle();
    final visibleBottom =
        tester.view.physicalSize.height - tester.view.viewInsets.bottom;
    expect(field.hitTestable(), findsOneWidget);
    expect(find.text('带入并加入').hitTestable(), findsOneWidget);
    expect(
      tester.getBottomRight(field).dy,
      lessThanOrEqualTo(visibleBottom),
    );
    expect(tester.takeException(), isNull);
  });

  testWidgets('管理员不能创建或加入牌桌，并被告知原因', (tester) async {
    await tester.pumpWidget(
      const MaterialApp(home: _LobbyHarness(role: 'admin')),
    );
    await tester.pumpAndSettle();

    // 不会走到 onCreateRoom / onPreviewRoom：它们在这个夹具里一调用就抛错
    await tester.ensureVisible(find.text('创建牌桌'));
    await tester.tap(find.text('创建牌桌'));
    await tester.pumpAndSettle();
    expect(find.textContaining('不能创建或加入牌桌'), findsOneWidget);
    expect(tester.takeException(), isNull);

    await tester.ensureVisible(find.text('加入牌桌'));
    await tester.tap(find.text('加入牌桌'));
    await tester.pumpAndSettle();
    expect(find.textContaining('不能创建或加入牌桌'), findsOneWidget);
    expect(tester.takeException(), isNull);
  });
}

class _LobbyHarness extends StatefulWidget {
  const _LobbyHarness({
    this.entries = const [],
    this.role = 'player',
    this.preview,
  });

  final List<BankrollEntry> entries;
  final String role;
  final RoomPreview? preview;

  @override
  State<_LobbyHarness> createState() => _LobbyHarnessState();
}

class _LobbyHarnessState extends State<_LobbyHarness> {
  final _settings = AppSettingsController();
  var _bankroll = const BankrollSnapshot(
    userId: 'user_1',
    walletChips: 0,
    tableChips: 0,
    revision: 0,
  );

  @override
  void dispose() {
    _settings.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return LobbyPage(
      session: AuthSession(
        user: AppUser(
          userId: 'user_1',
          username: 'friend_1',
          displayName: '好友一',
          role: widget.role,
        ),
        accessToken: 'access',
        refreshToken: 'refresh',
        accessExpiresAt: DateTime(2030),
        refreshExpiresAt: DateTime(2031),
      ),
      bankroll: _bankroll,
      onCreateRoom: (_) => Future.error(UnimplementedError()),
      onJoinRoom: (_, _, _) => Future.error(UnimplementedError()),
      onLoadRecentHands: () async => const [],
      onTopUp: (amount) async {
        final next = BankrollSnapshot(
          userId: 'user_1',
          walletChips: _bankroll.walletChips + amount,
          tableChips: 0,
          revision: _bankroll.revision + 1,
        );
        setState(() => _bankroll = next);
        return next;
      },
      onLoadBankrollEntries: () async => widget.entries,
      onPreviewRoom: (_) => widget.preview == null
          ? Future.error(UnimplementedError())
          : Future.value(widget.preview),
      onUpdateUsername: (username) async =>
          AppUser(userId: 'user_1', username: username, displayName: '好友一'),
      onUpdateDisplayName: (displayName) async => AppUser(
        userId: 'user_1',
        username: 'friend_1',
        displayName: displayName,
      ),
      onChangePassword: (_, _) => Future.error(UnimplementedError()),
      accessTokenProvider: ({bool forceRefresh = false}) async => 'access',
      settings: _settings,
      onLogout: () {},
    );
  }
}
