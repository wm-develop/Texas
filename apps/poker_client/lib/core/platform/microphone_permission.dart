import 'package:flutter/foundation.dart';
import 'package:permission_handler/permission_handler.dart';

abstract interface class MicrophonePermission {
  Future<bool> request();
}

class SystemMicrophonePermission implements MicrophonePermission {
  @override
  Future<bool> request() async {
    if (kIsWeb || defaultTargetPlatform == TargetPlatform.windows) {
      return true;
    }
    if (defaultTargetPlatform != TargetPlatform.android &&
        defaultTargetPlatform != TargetPlatform.iOS &&
        defaultTargetPlatform != TargetPlatform.ohos) {
      return false;
    }
    final status = await Permission.microphone.request();
    return status.isGranted;
  }
}
