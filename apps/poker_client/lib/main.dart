import 'package:flutter/widgets.dart';
import 'package:flutter/services.dart';
import 'package:poker_client/app/poker_app.dart';
import 'package:poker_client/core/platform/system_ui_policy.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  if (shouldUseCurrentPlatformDartSystemUi) {
    await SystemChrome.setPreferredOrientations(const [
      DeviceOrientation.landscapeLeft,
      DeviceOrientation.landscapeRight,
    ]);
    await _enableImmersiveMode();
  }
  runApp(const PokerApp());
}

Future<void> _enableImmersiveMode() async {
  try {
    await SystemChrome.setEnabledSystemUIMode(SystemUiMode.immersiveSticky);
  } on Object {
    // Android can reject this briefly while its window is being recreated.
  }
}
