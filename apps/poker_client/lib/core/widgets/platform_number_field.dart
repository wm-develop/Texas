import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

/// Avoids the unstable OHOS numeric system-keyboard bridge in landscape
/// dialogs by using an in-app keypad on HarmonyOS.
class PlatformNumberField extends StatelessWidget {
  const PlatformNumberField({
    required this.controller,
    required this.decoration,
    this.maxLength,
    this.onChanged,
    this.onSubmitted,
    this.autofocus = false,
    this.scrollPadding = const EdgeInsets.all(20),
    super.key,
  });

  final TextEditingController controller;
  final InputDecoration decoration;
  final int? maxLength;
  final ValueChanged<String>? onChanged;
  final ValueChanged<String>? onSubmitted;
  final bool autofocus;
  final EdgeInsets scrollPadding;

  bool get _usesInAppPad =>
      !kIsWeb && defaultTargetPlatform.name.toLowerCase() == 'ohos';

  @override
  Widget build(BuildContext context) {
    if (!_usesInAppPad) {
      return TextField(
        controller: controller,
        autofocus: autofocus,
        keyboardType: TextInputType.number,
        textInputAction: TextInputAction.done,
        maxLength: maxLength,
        scrollPadding: scrollPadding,
        inputFormatters: [FilteringTextInputFormatter.digitsOnly],
        decoration: decoration,
        onChanged: onChanged,
        onSubmitted: onSubmitted,
      );
    }
    return ValueListenableBuilder<TextEditingValue>(
      valueListenable: controller,
      builder: (context, value, _) {
        final effectiveDecoration = decoration.copyWith(
          // OHOS renders the label and our custom child at the same baseline
          // when an InputDecorator is empty. Keep the label floated and let
          // the decorator own the hint so the two texts cannot overlap.
          floatingLabelBehavior: FloatingLabelBehavior.always,
          hintText: decoration.hintText ?? '点击输入',
        );
        return Semantics(
          button: true,
          textField: true,
          label: decoration.labelText,
          value: value.text,
          child: InkWell(
            borderRadius: BorderRadius.circular(4),
            onTap: () async {
              final selected = await showDialog<String>(
                context: context,
                builder: (context) => _InAppNumberPadDialog(
                  title: decoration.labelText ?? '输入数字',
                  initialValue: value.text,
                  maxLength: maxLength,
                ),
              );
              if (selected == null) return;
              controller.value = TextEditingValue(
                text: selected,
                selection: TextSelection.collapsed(offset: selected.length),
              );
              onChanged?.call(selected);
              onSubmitted?.call(selected);
            },
            child: InputDecorator(
              decoration: effectiveDecoration,
              isEmpty: value.text.isEmpty,
              child: value.text.isEmpty
                  ? const SizedBox.shrink()
                  : Text(value.text),
            ),
          ),
        );
      },
    );
  }
}

class _InAppNumberPadDialog extends StatefulWidget {
  const _InAppNumberPadDialog({
    required this.title,
    required this.initialValue,
    this.maxLength,
  });

  final String title;
  final String initialValue;
  final int? maxLength;

  @override
  State<_InAppNumberPadDialog> createState() => _InAppNumberPadDialogState();
}

class _InAppNumberPadDialogState extends State<_InAppNumberPadDialog> {
  late String _value = widget.initialValue;

  void _append(String digit) {
    if (widget.maxLength != null && _value.length >= widget.maxLength!) return;
    setState(() => _value = _value == '0' ? digit : '$_value$digit');
  }

  @override
  Widget build(BuildContext context) {
    const keys = <String>['1', '2', '3', '4', '5', '6', '7', '8', '9'];
    final mediaSize = MediaQuery.sizeOf(context);
    final compactLandscape =
        mediaSize.width > mediaSize.height && mediaSize.height < 520;
    final keypad = GridView.count(
      key: const ValueKey('ohos-number-keypad'),
      shrinkWrap: true,
      physics: const NeverScrollableScrollPhysics(),
      crossAxisCount: 3,
      mainAxisSpacing: 7,
      crossAxisSpacing: 7,
      childAspectRatio: compactLandscape ? 2.35 : 2.2,
      children: [
        for (final key in keys)
          FilledButton.tonal(
            key: ValueKey('ohos-number-key-$key'),
            onPressed: () => _append(key),
            child: Text(key),
          ),
        OutlinedButton(
          key: const ValueKey('ohos-number-clear'),
          onPressed: () => setState(() => _value = ''),
          child: const Text('清空'),
        ),
        FilledButton.tonal(
          key: const ValueKey('ohos-number-key-0'),
          onPressed: () => _append('0'),
          child: const Text('0'),
        ),
        OutlinedButton.icon(
          key: const ValueKey('ohos-number-backspace'),
          onPressed: _value.isEmpty
              ? null
              : () => setState(
                  () => _value = _value.substring(0, _value.length - 1),
                ),
          icon: const Icon(Icons.backspace_outlined, size: 17),
          label: const Text('退格'),
        ),
      ],
    );
    final valueAndActions = Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text(widget.title, style: Theme.of(context).textTheme.headlineSmall),
        const SizedBox(height: 12),
        Container(
          width: double.infinity,
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
          decoration: BoxDecoration(
            border: Border.all(color: const Color(0xFFD9B85F)),
            borderRadius: BorderRadius.circular(8),
          ),
          child: Text(
            _value.isEmpty ? '0' : _value,
            key: const ValueKey('ohos-number-value'),
            textAlign: TextAlign.right,
            style: Theme.of(context).textTheme.headlineSmall,
          ),
        ),
        if (compactLandscape) const Spacer() else const SizedBox(height: 18),
        Row(
          mainAxisAlignment: MainAxisAlignment.end,
          children: [
            TextButton(
              onPressed: () => Navigator.of(context).pop(),
              child: const Text('取消'),
            ),
            const SizedBox(width: 8),
            FilledButton(
              key: const ValueKey('ohos-number-confirm'),
              onPressed: _value.isEmpty
                  ? null
                  : () => Navigator.of(context).pop(_value),
              child: const Text('确定'),
            ),
          ],
        ),
      ],
    );
    return Dialog(
      insetPadding: const EdgeInsets.all(12),
      child: ConstrainedBox(
        constraints: BoxConstraints(
          maxWidth: compactLandscape ? 660 : 390,
          maxHeight: mediaSize.height - 24,
        ),
        child: Padding(
          padding: const EdgeInsets.all(18),
          child: compactLandscape
              ? SizedBox(
                  height: (mediaSize.height - 60)
                      .clamp(236.0, 310.0)
                      .toDouble(),
                  child: Row(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      SizedBox(width: 230, child: valueAndActions),
                      const SizedBox(width: 18),
                      Expanded(child: Center(child: keypad)),
                    ],
                  ),
                )
              : SingleChildScrollView(
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      valueAndActions,
                      const SizedBox(height: 12),
                      keypad,
                    ],
                  ),
                ),
        ),
      ),
    );
  }
}
