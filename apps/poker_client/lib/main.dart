import 'package:flutter/widgets.dart';
import 'package:flutter/services.dart';
import 'package:poker_client/app/poker_app.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  await SystemChrome.setPreferredOrientations(const [
    DeviceOrientation.landscapeLeft,
    DeviceOrientation.landscapeRight,
  ]);
  await _enableImmersiveMode();
  runApp(const PokerApp());
}

Future<void> _enableImmersiveMode() async {
  try {
    await SystemChrome.setEnabledSystemUIMode(SystemUiMode.immersiveSticky);
  } on Object {
    // Desktop, Web, and some OpenHarmony embeddings can ignore this request.
  }
}
