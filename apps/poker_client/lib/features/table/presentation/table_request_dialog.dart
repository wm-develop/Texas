import 'package:flutter/material.dart';

/// 被申请者对一条换座/看牌申请的处理结果。
enum RequestDecision {
  accept,
  declineOnce,
  declineRequester,
  declineEveryone;

  bool get accepted => this == RequestDecision.accept;

  /// 发给服务端的拒绝范围；同意时无意义，发 'once' 保持载荷稳定。
  String get scope => switch (this) {
    RequestDecision.declineRequester => 'requester',
    RequestDecision.declineEveryone => 'everyone',
    _ => 'once',
  };
}

/// 换座 / 看牌申请的答复弹窗。除「拒绝」「同意」外，换座多一项「不再接受此人」
/// （本房间内有效），看牌多两项「本手不再接受此人 / 任何人」（随下一手清空）。
class TableRequestDialog extends StatelessWidget {
  const TableRequestDialog({
    super.key,
    required this.title,
    required this.description,
    required this.holeCards,
  });

  final String title;
  final String description;

  /// true 为看牌申请，false 为换座申请。
  final bool holeCards;

  @override
  Widget build(BuildContext context) {
    final navigator = Navigator.of(context);
    return AlertDialog(
      title: Text(title),
      // 横屏手机只有 360 逻辑像素高，再开大字号时正文和按钮放不下。
      // scrollable 让标题和正文滚动而不是被压成 0 高度（上游 AlertDialog 的
      // 行为；OpenHarmony 分支默认就是 scrollable，这里显式统一四端）。
      scrollable: true,
      content: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text(description),
          const SizedBox(height: 16),
          // 范围选项按列放在正文里、整行可点：四个按钮挤在动作栏里会在窄屏
          // 上折成参差的两行，看不出哪个是「拒绝」哪个是「同意」。
          OutlinedButton(
            key: const ValueKey('request-decline-requester'),
            onPressed: () => navigator.pop(RequestDecision.declineRequester),
            child: Text(holeCards ? '本手不再接受此人的看牌申请' : '不再接受此人的换座申请'),
          ),
          if (holeCards) ...[
            const SizedBox(height: 8),
            OutlinedButton(
              key: const ValueKey('request-decline-everyone'),
              onPressed: () => navigator.pop(RequestDecision.declineEveryone),
              child: const Text('本手不再接受任何人的看牌申请'),
            ),
          ],
        ],
      ),
      actions: [
        TextButton(
          key: const ValueKey('request-decline-once'),
          onPressed: () => navigator.pop(RequestDecision.declineOnce),
          child: const Text('拒绝'),
        ),
        FilledButton(
          key: const ValueKey('request-accept'),
          onPressed: () => navigator.pop(RequestDecision.accept),
          child: const Text('同意'),
        ),
      ],
    );
  }
}
