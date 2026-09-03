import 'package:flutter/material.dart';
import 'package:poker_client/core/app_version.dart';

/// 调整最低客户端版本的弹窗。
///
/// 门槛以整数存储与比较（`major*1000000 + minor*1000 + patch`），但手填一串
/// 数字很容易写错一位，因此边输入边把它还原成 `0.2.1` 这种形式，填错一眼
/// 就能看出来。
class MinimumClientVersionDialog extends StatefulWidget {
  const MinimumClientVersionDialog({required this.current, super.key});

  /// 当前生效的门槛，0 表示未限制。
  final int current;

  /// 返回用户确认后的新门槛；取消时返回 null。
  static Future<int?> show(BuildContext context, {required int current}) =>
      showDialog<int>(
        context: context,
        builder: (context) => MinimumClientVersionDialog(current: current),
      );

  /// 输入框下方的即时预览文案。
  ///
  /// 分成三种：不限制、合法版本、以及非法输入。非法输入不能悄悄当成 0，
  /// 那会把门禁关掉而使用者以为设上了。
  static String previewFor(String input) {
    final parsed = parseVersionCode(input);
    if (parsed == null) return '只能填数字，请检查输入';
    if (parsed == 0) return '不限制客户端版本';
    return '= $parsed（${describeVersionCode(parsed)}）';
  }

  static bool isValid(String input) => parseVersionCode(input) != null;

  @override
  State<MinimumClientVersionDialog> createState() =>
      _MinimumClientVersionDialogState();
}

class _MinimumClientVersionDialogState
    extends State<MinimumClientVersionDialog> {
  late final TextEditingController _controller = TextEditingController(
    text: widget.current > 0 ? '${widget.current}' : '',
  );

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final input = _controller.text;
    final valid = MinimumClientVersionDialog.isValid(input);
    return AlertDialog(
      title: const Text('最低客户端版本'),
      content: SizedBox(
        width: 360,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              const Text(
                '低于该版本的客户端会被拒绝连接，并看到更新提示。'
                '填客户端 pubspec.yaml 里「+」后面那个数（0.2.1 → 2001）；'
                '留空或填 0 表示不限制。',
                style: TextStyle(color: Colors.white70, fontSize: 12),
              ),
              const SizedBox(height: 12),
              TextField(
                key: const ValueKey('minimum-client-version-field'),
                controller: _controller,
                autofocus: true,
                keyboardType: TextInputType.number,
                onChanged: (_) => setState(() {}),
                decoration: const InputDecoration(
                  labelText: '版本号',
                  border: OutlineInputBorder(),
                  isDense: true,
                ),
              ),
              const SizedBox(height: 8),
              Text(
                MinimumClientVersionDialog.previewFor(input),
                style: TextStyle(
                  fontWeight: FontWeight.w700,
                  color: valid ? const Color(0xFFF6D986) : Colors.redAccent,
                ),
              ),
              const SizedBox(height: 10),
              const Text(
                '调高后还没更新的朋友会立刻被挡在门外，请在新客户端发出去之后再改。',
                style: TextStyle(color: Colors.orangeAccent, fontSize: 12),
              ),
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('取消'),
        ),
        FilledButton(
          // 非法输入不放行：否则会被当成 0 悄悄关掉门禁
          onPressed: valid
              ? () => Navigator.pop(context, parseVersionCode(input))
              : null,
          child: const Text('保存'),
        ),
      ],
    );
  }
}
