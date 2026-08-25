import 'package:flutter/widgets.dart';
import 'package:flutter/services.dart';
import 'package:poker_client/app/poker_app.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  await SystemChrome.setPreferredOrientations(const [
    DeviceOrientation.landscapeLeft,
    DeviceOrientation.landscapeRight,
  ]);
  runApp(const PokerApp());
}
