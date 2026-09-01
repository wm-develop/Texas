import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/features/table/domain/hand_category_label.dart';

void main() {
  test('翻译单次和发两次的牌型', () {
    expect(handCategoryLabel('one_pair'), '一对');
    expect(handCategoryLabel('two_pair'), '两对');
    expect(handCategoryLabel('ONE_PAIR / straight_flush'), '一对 / 同花顺');
  });

  test('私下看牌与主动亮牌显示中文而非内部枚举', () {
    expect(handCategoryLabel('private_view'), '私下查看');
    expect(handCategoryLabel('voluntary'), '主动亮牌');
  });
}
