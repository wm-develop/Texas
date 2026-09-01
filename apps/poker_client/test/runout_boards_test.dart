import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/features/table/domain/runout_boards.dart';

void main() {
  test('翻后全下发两次：共享前三张翻牌', () {
    expect(
      sharedRunoutPrefixLength([
        ['As', 'Kd', 'Qh', '2c', '3d'],
        ['As', 'Kd', 'Qh', '9s', 'Jc'],
      ]),
      3,
    );
  });

  test('转牌后全下发两次：只有河牌不同', () {
    expect(
      sharedRunoutPrefixLength([
        ['As', 'Kd', 'Qh', '2c', '3d'],
        ['As', 'Kd', 'Qh', '2c', 'Jc'],
      ]),
      4,
    );
  });

  test('翻前全下发两次：没有共享公共牌', () {
    expect(
      sharedRunoutPrefixLength([
        ['As', 'Kd', 'Qh', '2c', '3d'],
        ['7s', '8d', '9h', 'Tc', 'Jc'],
      ]),
      0,
    );
  });

  test('单块牌面不参与切换', () {
    expect(
      sharedRunoutPrefixLength([
        ['As', 'Kd', 'Qh', '2c', '3d'],
      ]),
      0,
    );
  });
}
