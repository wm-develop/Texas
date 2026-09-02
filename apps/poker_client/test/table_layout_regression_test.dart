import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/features/table/domain/table_seat.dart';
import 'package:poker_client/features/table/presentation/table_canvas.dart';
import 'package:poker_client/features/table/presentation/table_card_widgets.dart';
import 'package:poker_client/features/table/presentation/table_seat_widgets.dart';
import 'package:poker_client/features/table/presentation/table_viewport_layout.dart';

// 牌桌布局与图层的回归测试。
//
// 这些约束是多轮真机返工换来的结论（见项目交接文档「牌桌 UI 约束」）：
// 玩家框不能被公共牌遮挡、本轮下注筹码位于玩家框之上、互动气泡位于最上层、
// 本人手牌与摊牌都在玩家框内。此前它们只存在于文档里，只能靠人工多端冒烟
// 兜底；现在固化为可执行断言。

TableSeat buildSeat({
  required int number,
  String displayName = '玩家',
  bool isCurrentUser = false,
  bool isFolded = false,
  int streetBet = 0,
  List<String> holeCards = const [],
  List<String> revealedCards = const [],
  String handCategory = '',
  bool isParticipating = true,
}) {
  return TableSeat(
    number: number,
    userId: 'user_$number',
    displayName: displayName,
    chips: 1000,
    isCurrentUser: isCurrentUser,
    isFolded: isFolded,
    streetBet: streetBet,
    holeCards: holeCards,
    revealedCards: revealedCards,
    handCategory: handCategory,
    isParticipating: isParticipating,
    isConnected: true,
  );
}

Widget wrap(Widget child, {Size size = const Size(1280, 720)}) {
  return MaterialApp(
    home: Scaffold(
      body: Center(
        child: SizedBox(width: size.width, height: size.height, child: child),
      ),
    ),
  );
}

Widget buildCanvas(List<TableSeat> seats, TableViewportLayout layout) {
  final alignments = List.generate(
    seats.length,
    (index) => layout.seatAlignment(index, seats.length),
  );
  return wrap(
    TableCanvas(
      seats: seats,
      alignments: alignments,
      boardRect: layout.boardRect.shift(-layout.tableRect.topLeft),
      snapshot: null,
      actionRemaining: Duration.zero,
      onSeatTap: (_) {},
      onAvatarTap: (_) {},
      onUseTimeExtension: () {},
      interactions: const [],
    ),
    size: layout.tableRect.size,
  );
}

void main() {
  group('座位排布', () {
    // 2～10 人是产品支持的全部人数，每一档都必须能完成布局且不溢出。
    for (final seatCount in [2, 3, 4, 5, 6, 7, 8, 9, 10]) {
      testWidgets('$seatCount 人牌桌完成布局且无溢出', (tester) async {
        final layout = TableViewportLayout.fromSize(
          const Size(1280, 720),
          chatVisible: false,
        );
        final seats = List.generate(
          seatCount,
          (index) => buildSeat(number: index + 1, isCurrentUser: index == 0),
        );
        await tester.pumpWidget(buildCanvas(seats, layout));
        expect(tester.takeException(), isNull);
        expect(find.byType(TableSeatCard), findsNWidgets(seatCount));
      });
    }

    testWidgets('紧凑横屏手机布局同样支持 10 人', (tester) async {
      final layout = TableViewportLayout.fromSize(
        const Size(920, 420),
        chatVisible: false,
        compactOverride: true,
      );
      expect(layout.isCompactLandscape, isTrue);
      final seats = List.generate(10, (index) => buildSeat(number: index + 1));
      await tester.pumpWidget(buildCanvas(seats, layout));
      expect(tester.takeException(), isNull);
      expect(find.byType(TableSeatCard), findsNWidgets(10));
    });
  });

  group('玩家框内容', () {
    testWidgets('本人手牌显示在玩家框内，不在独立面板', (tester) async {
      await tester.pumpWidget(
        wrap(
          TableSeatCard(
            seat: buildSeat(
              number: 1,
              isCurrentUser: true,
              holeCards: const ['As', 'Kd'],
            ),
            actionRemaining: Duration.zero,
            showReadyStatus: false,
            winnerAmount: 0,
            onAvatarTap: () {},
            onUseTimeExtension: () {},
          ),
          size: const Size(260, 160),
        ),
      );
      expect(tester.takeException(), isNull);
      // 两张手牌必须作为玩家框的后代出现
      final cardsInSeat = find.descendant(
        of: find.byType(TableSeatCard),
        matching: find.byType(TableMiniCard),
      );
      expect(cardsInSeat, findsNWidgets(2));
    });

    testWidgets('摊牌结果与中文牌型显示在玩家框内', (tester) async {
      await tester.pumpWidget(
        wrap(
          TableSeatCard(
            seat: buildSeat(
              number: 2,
              revealedCards: const ['Qh', 'Qc'],
              handCategory: 'one_pair',
            ),
            actionRemaining: Duration.zero,
            showReadyStatus: false,
            winnerAmount: 300,
            onAvatarTap: () {},
            onUseTimeExtension: () {},
          ),
          size: const Size(260, 160),
        ),
      );
      expect(tester.takeException(), isNull);
      expect(
        find.descendant(
          of: find.byType(TableSeatCard),
          matching: find.byType(TableMiniCard),
        ),
        findsNWidgets(2),
      );
      // 牌型必须是中文，不能泄漏内部枚举
      expect(find.text('一对'), findsOneWidget);
      expect(find.textContaining('one_pair'), findsNothing);
    });

    testWidgets('未参局玩家不显示手牌', (tester) async {
      await tester.pumpWidget(
        wrap(
          TableSeatCard(
            seat: buildSeat(number: 3, isParticipating: false),
            actionRemaining: Duration.zero,
            showReadyStatus: true,
            winnerAmount: 0,
            onAvatarTap: () {},
            onUseTimeExtension: () {},
          ),
          size: const Size(260, 160),
        ),
      );
      expect(tester.takeException(), isNull);
      expect(
        find.descendant(
          of: find.byType(TableSeatCard),
          matching: find.byType(TableMiniCard),
        ),
        findsNothing,
      );
    });
  });

  group('图层顺序', () {
    // 约束：公共牌背景 < 玩家框 < 本轮下注筹码 < 互动气泡。
    // 用 Stack 中的子节点先后顺序表达——越靠后绘制越靠上。
    testWidgets('本轮下注筹码绘制在玩家框之后', (tester) async {
      final layout = TableViewportLayout.fromSize(
        const Size(1280, 720),
        chatVisible: false,
      );
      final seats = [
        buildSeat(number: 1, isCurrentUser: true, streetBet: 200),
        buildSeat(number: 2, streetBet: 400),
      ];
      await tester.pumpWidget(buildCanvas(seats, layout));
      expect(tester.takeException(), isNull);

      final allWidgets = tester.allWidgets.toList();
      final lastSeatIndex = allWidgets.lastIndexWhere(
        (widget) => widget is TableSeatCard,
      );
      final firstChipIndex = allWidgets.indexWhere(
        (widget) => widget is TableBetChip,
      );
      expect(
        firstChipIndex,
        greaterThan(lastSeatIndex),
        reason: '本轮下注筹码必须绘制在所有玩家框之后，否则会被玩家框遮挡',
      );
    });

    testWidgets('无下注时不渲染筹码', (tester) async {
      final layout = TableViewportLayout.fromSize(
        const Size(1280, 720),
        chatVisible: false,
      );
      await tester.pumpWidget(
        buildCanvas([
          buildSeat(number: 1, isCurrentUser: true),
          buildSeat(number: 2),
        ], layout),
      );
      expect(find.byType(TableBetChip), findsNothing);
    });
  });

  group('视口布局几何', () {
    test('公共牌区域始终位于牌桌内部并留有边距', () {
      for (final size in const [
        Size(1280, 720),
        Size(2400, 1080),
        Size(920, 420),
        Size(1024, 768),
      ]) {
        final layout = TableViewportLayout.fromSize(size, chatVisible: false);
        expect(
          layout.boardRect.left,
          greaterThan(layout.tableRect.left),
          reason: '$size 下公共牌区域越过了牌桌左边界',
        );
        expect(
          layout.boardRect.right,
          lessThan(layout.tableRect.right),
          reason: '$size 下公共牌区域越过了牌桌右边界',
        );
        expect(
          layout.boardRect.top,
          greaterThan(layout.tableRect.top),
          reason: '$size 下公共牌区域越过了牌桌上边界',
        );
        expect(
          layout.boardRect.bottom,
          lessThan(layout.tableRect.bottom),
          reason: '$size 下公共牌区域越过了牌桌下边界',
        );
      }
    });

    test('座位分布在牌桌周边而不聚集在中心', () {
      final layout = TableViewportLayout.fromSize(
        const Size(1280, 720),
        chatVisible: false,
      );
      for (final seatCount in [2, 6, 10]) {
        for (var index = 0; index < seatCount; index++) {
          final alignment = layout.seatAlignment(index, seatCount);
          final distance = alignment.x.abs() + alignment.y.abs();
          expect(
            distance,
            greaterThan(0.3),
            reason: '$seatCount 人时第 $index 个座位过于靠近牌桌中心，可能压住公共牌',
          );
          // alignment 现在会超过 1：玩家框有意伸出桌沿，只有 1/3 留在桌内。
          // 「不压住公共牌区」由 table_viewport_layout_test.dart 里按真实
          // seatRect 逐个校验，这里只做一个不至于离谱的上界。
          expect(alignment.x.abs(), lessThanOrEqualTo(2.0));
          expect(alignment.y.abs(), lessThanOrEqualTo(2.0));
        }
      }
    });
  });
}
