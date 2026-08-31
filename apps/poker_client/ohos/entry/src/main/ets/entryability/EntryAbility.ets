
import { FlutterAbility, FlutterEngine } from '@ohos/flutter_ohos';
import { GeneratedPluginRegistrant } from '../plugins/GeneratedPluginRegistrant';
import MethodChannel, {
  MethodResult
} from '@ohos/flutter_ohos/src/main/ets/plugin/common/MethodChannel';
import MethodCall from '@ohos/flutter_ohos/src/main/ets/plugin/common/MethodCall';
import window from '@ohos.window';
import display from '@ohos.display';

const DISPLAY_CUTOUT_CHANNEL = 'com.texas.game.poker_client/display_cutout';

interface DisplayCutoutInsets {
  left: number;
  top: number;
  right: number;
  bottom: number;
}

export default class EntryAbility extends FlutterAbility {
  private displayCutoutChannel: MethodChannel | null = null;

  configureFlutterEngine(flutterEngine: FlutterEngine) {
    super.configureFlutterEngine(flutterEngine);
    GeneratedPluginRegistrant.registerWith(flutterEngine);
    this.displayCutoutChannel = new MethodChannel(
      flutterEngine.getDartExecutor(),
      DISPLAY_CUTOUT_CHANNEL
    );
    this.displayCutoutChannel.setMethodCallHandler({
      onMethodCall: (call: MethodCall, result: MethodResult): void => {
        if (call.method !== 'getInsets') {
          result.notImplemented();
          return;
        }
        this.readDisplayCutoutInsets()
          .then((insets: DisplayCutoutInsets): void => result.success(insets))
          .catch((error: Error): void => {
            result.error('CUTOUT_UNAVAILABLE', error.message, null);
          });
      }
    });
  }

  private async readDisplayCutoutInsets(): Promise<DisplayCutoutInsets> {
    const mainWindow = await window.getLastWindow(this.context);
    const avoidArea = mainWindow.getWindowAvoidArea(
      window.AvoidAreaType.TYPE_CUTOUT
    );
    const density = display.getDefaultDisplaySync().densityPixels;
    const divisor = density > 0 ? density : 1;
    return {
      left: avoidArea.leftRect.width / divisor,
      top: avoidArea.topRect.height / divisor,
      right: avoidArea.rightRect.width / divisor,
      bottom: avoidArea.bottomRect.height / divisor
    };
  }
}
