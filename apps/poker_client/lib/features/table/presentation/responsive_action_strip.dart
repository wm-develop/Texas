import 'package:flutter/material.dart';

/// Keeps the primary poker actions and the custom amount action reachable on
/// narrow tables, while allowing the preset amounts in the middle to scroll.
class ResponsiveActionStrip extends StatefulWidget {
  const ResponsiveActionStrip({
    required this.leadingActions,
    required this.presetActions,
    this.trailingAction,
    this.singleRowBreakpoint = 1320,
    this.spacing = 7,
    super.key,
  });

  final List<Widget> leadingActions;
  final List<Widget> presetActions;
  final Widget? trailingAction;
  final double singleRowBreakpoint;
  final double spacing;

  @override
  State<ResponsiveActionStrip> createState() => _ResponsiveActionStripState();
}

class _ResponsiveActionStripState extends State<ResponsiveActionStrip> {
  final ScrollController _controller = ScrollController();
  bool _canScrollBackward = false;
  bool _canScrollForward = false;

  @override
  void initState() {
    super.initState();
    _controller.addListener(_updateScrollButtons);
  }

  @override
  void didUpdateWidget(covariant ResponsiveActionStrip oldWidget) {
    super.didUpdateWidget(oldWidget);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted || !_controller.hasClients) return;
      if (_controller.offset > _controller.position.maxScrollExtent) {
        _controller.jumpTo(_controller.position.maxScrollExtent);
      }
      _updateScrollButtons();
    });
  }

  @override
  void dispose() {
    _controller
      ..removeListener(_updateScrollButtons)
      ..dispose();
    super.dispose();
  }

  void _updateScrollButtons() {
    if (!_controller.hasClients || !mounted) return;
    final position = _controller.position;
    final backward = position.pixels > position.minScrollExtent + 0.5;
    final forward = position.pixels < position.maxScrollExtent - 0.5;
    if (backward == _canScrollBackward && forward == _canScrollForward) {
      return;
    }
    setState(() {
      _canScrollBackward = backward;
      _canScrollForward = forward;
    });
  }

  void _scrollBy(double direction) {
    if (!_controller.hasClients) return;
    final position = _controller.position;
    final distance = position.viewportDimension * 0.72 * direction;
    final target = (position.pixels + distance).clamp(
      position.minScrollExtent,
      position.maxScrollExtent,
    );
    _controller.animateTo(
      target,
      duration: const Duration(milliseconds: 180),
      curve: Curves.easeOut,
    );
  }

  @override
  Widget build(BuildContext context) => LayoutBuilder(
    builder: (context, constraints) {
      if (constraints.maxWidth >= widget.singleRowBreakpoint) {
        return Row(
          mainAxisAlignment: MainAxisAlignment.center,
          children: _withSpacing([
            ...widget.leadingActions,
            ...widget.presetActions,
            if (widget.trailingAction != null) widget.trailingAction!,
          ]),
        );
      }

      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) _updateScrollButtons();
      });
      return Row(
        children: [
          ..._withSpacing(widget.leadingActions),
          if (widget.leadingActions.isNotEmpty &&
              widget.presetActions.isNotEmpty)
            SizedBox(width: widget.spacing),
          if (widget.presetActions.isNotEmpty) ...[
            _ScrollArrow(
              key: const ValueKey('bet-presets-scroll-left'),
              icon: Icons.chevron_left,
              onPressed: _canScrollBackward ? () => _scrollBy(-1) : null,
              tooltip: '向左查看更多下注额度',
            ),
            Expanded(
              child: SingleChildScrollView(
                controller: _controller,
                scrollDirection: Axis.horizontal,
                child: Row(children: _withSpacing(widget.presetActions)),
              ),
            ),
            _ScrollArrow(
              key: const ValueKey('bet-presets-scroll-right'),
              icon: Icons.chevron_right,
              onPressed: _canScrollForward ? () => _scrollBy(1) : null,
              tooltip: '向右查看更多下注额度',
            ),
          ] else
            const Spacer(),
          if (widget.trailingAction != null) ...[
            SizedBox(width: widget.spacing),
            widget.trailingAction!,
          ],
        ],
      );
    },
  );

  List<Widget> _withSpacing(List<Widget> children) {
    if (children.length < 2) return children;
    return [
      for (var index = 0; index < children.length; index++) ...[
        if (index > 0) SizedBox(width: widget.spacing),
        children[index],
      ],
    ];
  }
}

class _ScrollArrow extends StatelessWidget {
  const _ScrollArrow({
    required this.icon,
    required this.onPressed,
    required this.tooltip,
    super.key,
  });

  final IconData icon;
  final VoidCallback? onPressed;
  final String tooltip;

  @override
  Widget build(BuildContext context) => IconButton(
    onPressed: onPressed,
    tooltip: tooltip,
    icon: Icon(icon, size: 20),
    padding: EdgeInsets.zero,
    visualDensity: VisualDensity.compact,
    constraints: const BoxConstraints.tightFor(width: 28, height: 44),
  );
}
