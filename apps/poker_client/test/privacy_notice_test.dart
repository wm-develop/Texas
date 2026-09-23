import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:poker_client/core/legal/privacy_notice.dart';

void main() {
  // 两处要求同步修改，靠人记容易漏：逐条比较文档与对话框里的条目
  test('客户端隐私说明与 docs/PRIVACY_NOTICE.md 的条目一致', () {
    final document = File('../../docs/PRIVACY_NOTICE.md').readAsStringSync();
    final body = document.split('## 实现说明').first;
    final documentItems = [
      for (final line in body.split(RegExp(r'\r?\n')))
        if (line.startsWith('- ')) line.substring(2).trim(),
    ];
    final dialogItems = [
      for (final line in privacyNoticeText.split('\n'))
        if (line.startsWith('• ')) line.substring(2).trim(),
    ];
    expect(documentItems, isNotEmpty);
    expect(dialogItems, documentItems);
  });
}
