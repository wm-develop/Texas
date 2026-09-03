
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

  // 除了挖孔，还要让开手势导航条：平板横屏时它压在屏幕底部，落在那片区域的
  // 按钮即使画出来了也点不动（触摸归系统）。两块区域都用系统的避让区接口取，
  // 不猜固定像素。
  private static readonly AVOID_AREA_TYPES: window.AvoidAreaType[] = [
    window.AvoidAreaType.TYPE_CUTOUT,
    window.AvoidAreaType.TYPE_NAVIGATION_INDICATOR
  ];

  private async readDisplayCutoutInsets(): Promise<DisplayCutoutInsets> {
    const mainWindow = await window.getLastWindow(this.context);
    const density = display.getDefaultDisplaySync().densityPixels;
    const divisor = density > 0 ? density : 1;
    const insets: DisplayCutoutInsets = { left: 0, top: 0, right: 0, bottom: 0 };
    for (const areaType of EntryAbility.AVOID_AREA_TYPES) {
      const area = mainWindow.getWindowAvoidArea(areaType);
      insets.left = Math.max(insets.left, area.leftRect.width / divisor);
      insets.top = Math.max(insets.top, area.topRect.height / divisor);
      insets.right = Math.max(insets.right, area.rightRect.width / divisor);
      insets.bottom = Math.max(insets.bottom, area.bottomRect.height / divisor);
    }
    return insets;
  }
}
