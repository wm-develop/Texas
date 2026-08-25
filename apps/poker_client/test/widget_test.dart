import 'dart:ui' show Size;

import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/app/poker_app.dart';

void main() {
  testWidgets('starts at the friends-only sign in screen', (tester) async {
    tester.view.physicalSize = const Size(1280, 720);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.resetPhysicalSize);
    addTearDown(tester.view.resetDevicePixelRatio);

    await tester.pumpWidget(const PokerApp());

    expect(find.text('好友德州'), findsOneWidget);
    expect(find.text('只和认识的朋友，快速组织一桌牌局'), findsOneWidget);
    expect(find.text('登录'), findsWidgets);
    expect(find.text('加入语音'), findsNothing);
  });
}
