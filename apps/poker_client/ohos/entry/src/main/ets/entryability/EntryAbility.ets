
import { FlutterAbility, FlutterEngine } from '@ohos/flutter_ohos';
import { GeneratedPluginRegistrant } from '../plugins/GeneratedPluginRegistrant';
import MethodChannel, {
  MethodResult
} from '@ohos/flutter_ohos/src/main/ets/plugin/common/MethodChannel';
import MethodCall from '@ohos/flutter_ohos/src/main/ets/plugin/common/MethodCall';
import window from '@ohos.window';
import display from '@ohos.display';
import { TableSoundPool } from './TableSoundPool';

const DISPLAY_CUTOUT_CHANNEL = 'com.texas.game.poker_client/display_cutout';
const TABLE_SOUND_CHANNEL = 'com.texas.game.poker_client/table_sound';

interface DisplayCutoutInsets {
  left: number;
  top: number;
  right: number;
  bottom: number;
}

export default class EntryAbility extends FlutterAbility {
  private displayCutoutChannel: MethodChannel | null = null;
  private tableSoundChannel: MethodChannel | null = null;
  private tableSoundPool: TableSoundPool = new TableSoundPool();

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
    // 提示音必须绕开 audioplayers：它会切换应用级音频会话，压制 TRTC 的
    // 通话流。SoundPool 是系统给短音效的低时延通道，不经过 AudioSession。
    this.tableSoundChannel = new MethodChannel(
      flutterEngine.getDartExecutor(),
      TABLE_SOUND_CHANNEL
    );
    this.tableSoundChannel.setMethodCallHandler({
      onMethodCall: (call: MethodCall, result: MethodResult): void => {
        if (call.method !== 'play') {
          result.notImplemented();
          return;
        }
        const path = call.argument('path') as string;
        const volume = call.argument('volume') as number;
        if (path === null || path === undefined || path.length === 0) {
          result.error('INVALID_PATH', 'path is required', null);
          return;
        }
        this.tableSoundPool
          .play(path, volume === null || volume === undefined ? 1 : volume)
          .then((): void => result.success(null))
          .catch((error: Error): void => {
            result.error('SOUND_UNAVAILABLE', error.message, null);
          });
      }
    });
  }

  // 鸿蒙的 FlutterAbility 不会自动全屏：平板上会一直顶着系统状态栏，
  // 牌桌被压掉一条。这里显式做沉浸式布局并隐藏状态栏。
  //
  // 只隐藏状态栏，保留手势导航指示器：把它一起隐藏后，系统仍然占着屏幕
  // 底部那条手势区（触摸归系统），但避让区接口会返回 0，界面就不会再让开，
  // 落在那里的按钮又会变得点不动——那正是上一版刚修掉的问题。
  onWindowStageCreate(windowStage: window.WindowStage): void {
    super.onWindowStageCreate(windowStage);
    windowStage
      .getMainWindow()
      .then((mainWindow: window.Window): void => {
        mainWindow.setWindowLayoutFullScreen(true).catch((): void => {});
        mainWindow
          .setSpecificSystemBarEnabled('status', false)
          .catch((): void => {});
      })
      .catch((): void => {
        // 全屏是显示偏好，取不到窗口时不影响应用启动。
      });
  }

  onDestroy(): void {
    this.tableSoundPool.release().catch((): void => {});
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
