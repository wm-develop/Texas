# Android 发布签名配置指南

> 目标：让**所有维护者机器**用同一份发布密钥构建 Android 包，使不同电脑产出的 APK 可以在同一台设备上互相覆盖安装。
>
> 适用范围：`apps/poker_client` 的 Android Release 构建。HarmonyOS 使用 DevEco 本机签名，Web/Windows 不涉及签名，均不受本文影响。

## 1. 为什么需要这件事

Android 用**签名**而不是包名来判断两个 APK 是否"同一个应用"。签名不一致时系统拒绝覆盖安装，报"签名不一致 / `INSTALL_FAILED_UPDATE_INCOMPATIBLE`"。

本项目此前 Release 构建复用调试签名（`signingConfigs.getByName("debug")`），而调试密钥库 `~/.android/debug.keystore` 是 **Android SDK 在每台机器首次构建时随机生成的**，因此：

- A 电脑构建的 APK 和 B 电脑构建的 APK 签名不同，无法互相覆盖；
- 调试密钥库口令是公开的 `android`，任何人都能用它签出"看起来是同一个应用"的包；
- 重装系统或清空 `~/.android` 后密钥丢失，此前分发的所有安装包都无法再升级。

配置一份自己保管的发布密钥库即可一次性解决这三个问题。

## 2. 开始之前

- 需要 JDK 的 `keytool` 命令。本项目要求 JDK 17，通常位于 `C:\Program Files\Java\jdk-17\bin\keytool.exe`。
- 准备一个**仓库之外**的目录存放密钥库，例如 `D:\Keys\`。**绝对不要放进仓库目录**。
- 准备一个口令并记录到你的密码管理器。丢失后无法找回，也无法为已发布的应用重新签名。

检查 `keytool` 可用：

```powershell
& 'C:\Program Files\Java\jdk-17\bin\keytool.exe' -help
```

若 JDK 已在 `PATH` 中，直接用 `keytool` 即可。

## 3. 第一步：生成发布密钥库（只在一台机器上做一次）

在你选定的目录执行（把 `D:\Keys` 换成你的实际路径，**不要放在仓库里**）：

```powershell
& 'C:\Program Files\Java\jdk-17\bin\keytool.exe' -genkeypair -v `
  -keystore D:\Keys\poker-release.jks `
  -storetype JKS `
  -keyalg RSA -keysize 2048 -validity 10000 `
  -alias poker
```

参数含义：

| 参数 | 说明 |
|---|---|
| `-keystore` | 密钥库文件输出路径 |
| `-storetype JKS` | 密钥库格式，Gradle 与 `keytool` 均支持 |
| `-keyalg RSA -keysize 2048` | 密钥算法与长度，Android 通用配置 |
| `-validity 10000` | 有效期（天），约 27 年；**必须覆盖应用的整个生命周期** |
| `-alias poker` | 密钥别名，后面配置要用到，可自定义 |

执行后会依次提示：

1. **输入密钥库口令**：自己设定，至少 6 位，输入时不回显。随后要求再输入一次确认。
2. **姓名、组织单位、组织、城市、省份、国家代码**：私人项目可全部直接回车留空，或随意填写。这些信息只写入证书，不影响功能。
3. **最后确认**：输入 `y` 或 `是` 回车。
4. **密钥口令**：提示"输入 `<poker>` 的密钥口令（如果和密钥库口令相同，按回车）"——**直接回车**，让两个口令保持一致，配置最简单。

完成后确认文件已生成：

```powershell
Get-Item D:\Keys\poker-release.jks | Select-Object FullName, Length, LastWriteTime
```

查看指纹（后续核对两台机器是否用了同一份密钥时会用到）：

```powershell
& 'C:\Program Files\Java\jdk-17\bin\keytool.exe' -list -v `
  -keystore D:\Keys\poker-release.jks -alias poker
```

记下输出里的 `SHA1` 与 `SHA256` 两行。

## 4. 第二步：创建 `key.properties`（每台机器都要做）

在 **`apps/poker_client/android/`** 目录下新建 `key.properties`，内容如下（把路径和口令换成你自己的）：

```properties
storeFile=D:/Keys/poker-release.jks
storePassword=你的密钥库口令
keyAlias=poker
keyPassword=你的密钥口令
```

注意事项：

- **路径用正斜杠 `/`**，或者用双反斜杠 `\\`。写单个 `\` 会被当作转义符导致读取失败。
- `keyPassword` 填第 3 步里那个"直接回车"的口令，也就是与 `storePassword` 相同。
- 文件末尾不要留多余空格，口令**不要**加引号。
- 该文件已被 `android/.gitignore` 忽略（`key.properties`、`**/*.jks`、`**/*.keystore` 三条规则），**不会也不允许提交**。

## 5. 第三步：验证构建已使用发布签名

构建 Release 包（把服务地址换成你的实际生产地址）：

```powershell
flutter build apk --release `
  --dart-define=GAME_SERVER_URL=wss://api.example.com/ws `
  --dart-define=GAME_HTTP_SERVER_URL=https://api.example.com
```

构建脚本的行为：

- **有** `key.properties` → 使用你的发布密钥库签名；
- **无** `key.properties` → 回退到调试签名，并在日志中打印警告：
  `android/key.properties not found; the Release APK is signed with the local debug keystore and must not be distributed.`

因此**看到这条警告就说明签名没生效**，请回到第 4 步检查文件位置和内容。

构建成功后，核对 APK 的实际签名。**必须使用 Android SDK 的 `apksigner`**（位于 `<Android SDK>\build-tools\<版本>\apksigner.bat`）：

```powershell
& "$env:LOCALAPPDATA\Android\Sdk\build-tools\37.0.0\apksigner.bat" verify `
  --print-certs -v build\app\outputs\flutter-apk\app-release.apk
```

关注两处输出：

- `Verified using v2 scheme (APK Signature Scheme v2): true`——签名有效。本项目 `minSdkVersion` 为 24（Android 7.0），而 v2 签名自 Android 7.0 起支持，因此 `v1 scheme: false` 属于正常现象，不必修正。
- `V2 Signer: certificate SHA-1 digest: <指纹>`——把它与第 3 步记录的 `SHA1` 对比（`apksigner` 输出为无冒号小写十六进制，`keytool` 输出为大写带冒号，比较时忽略格式差异）。

若指纹与本机调试密钥相同，说明仍是调试签名。调试密钥指纹可用以下命令查看：

```powershell
& 'C:\Program Files\Java\jdk-17\bin\keytool.exe' -list -v `
  -keystore "$env:USERPROFILE\.android\debug.keystore" `
  -storepass android -alias androiddebugkey
```

> **不要用 `keytool -printcert -jarfile` 检查 APK。** 该命令只识别 v1（JAR）签名，而现代 APK 通常只有 v2/v3 整包签名，此时它会误报“不是已签名的 jar 文件”，与签名是否正确无关。

## 6. 第四步：把密钥同步到第二台电脑

1. 通过**安全渠道**把 `poker-release.jks` 复制到第二台电脑（加密 U 盘、密码管理器的文件附件、端到端加密的私人网盘）。**不要**走聊天软件、公共网盘、邮件附件或任何会留存副本的渠道。
2. 在第二台电脑上放到它自己的目录（路径可以与第一台不同）。
3. 在第二台电脑的 `apps/poker_client/android/` 下同样创建 `key.properties`，`storeFile` 填**这台机器上的实际路径**，其余三项与第一台完全相同。
4. 构建后用第 5 步的 `-printcert` 核对 SHA1，两台机器应输出同一指纹。

至此两台电脑产出的 APK 即可互相覆盖安装。

## 7. 首次切换签名时：设备上的旧包必须先卸载

从调试签名切换到发布签名，签名同样发生了变化，因此**所有已经装了旧版本的设备都要先卸载一次**：

```powershell
adb uninstall com.texas.game.poker_client
```

或在设备上手动卸载。之后安装新签名的 APK，以后再升级就不需要卸载了。

> 提醒测试的朋友：这一次需要卸载重装，本机数据（仅本地设置与屏蔽列表）会清空；账号、筹码和牌局历史都在服务端，不受影响。

## 8. 密钥保管要求

- **备份**：把 `poker-release.jks` 至少备份到两处独立位置（例如加密网盘 + 离线加密 U 盘）。密钥丢失意味着已分发的应用永远无法再升级，只能改包名重新分发。
- **口令**：存进密码管理器，不要写进任何文本文件、笔记、聊天记录或本仓库。
- **不提交**：密钥库和 `key.properties` 都不得进入版本库。提交前可以确认一次：

  ```powershell
  git status --short
  git check-ignore -v apps/poker_client/android/key.properties
  ```

  第二条命令应输出匹配的忽略规则；`git status` 中不应出现 `key.properties` 或任何 `.jks` 文件。
- **不外发**：GitHub Release 只发布源码，构建者使用各自的服务地址、TRTC 配置与签名。不要把密钥库随源码或安装包一起分发。

## 9. 常见问题

**Q：构建报 `Keystore file not found`**
`key.properties` 里的 `storeFile` 路径写错，或使用了单个反斜杠。改用正斜杠并确认文件确实存在。

**Q：构建报 `Keystore was tampered with, or password was incorrect`**
`storePassword` 不正确。用第 3 步的 `keytool -list` 命令验证口令是否能打开密钥库。

**Q：构建报 `Cannot recover key`**
`keyPassword` 不正确。如果生成时按了回车让两个口令相同，那么这一项应与 `storePassword` 一致。

**Q：安装仍报签名不一致**
设备上还留着旧签名的包，按第 7 步先卸载。若两台电脑之间仍不一致，用第 5 步的 `apksigner verify --print-certs` 分别核对两个 APK 的证书 SHA-1。

**Q：`keytool -printcert -jarfile` 报"不是已签名的 jar 文件"**
工具用错了，与签名是否正确无关。该命令只认 v1（JAR）签名，而本项目的 APK 是 v2 整包签名。改用第 5 步的 `apksigner verify`。

**Q：`Verified using v1 scheme: false` 要紧吗**
不要紧。v1 是 Android 7.0 之前的旧签名机制，本项目 `minSdkVersion` 为 24（Android 7.0），运行的设备全部支持 v2，无需 v1。

**Q：能不能直接共用 `debug.keystore`**
可以让两台机器互相覆盖安装，但它口令公开、随时可能被 SDK 重新生成、且重装系统即丢失，不适合分发给他人。本文的方案才是可长期使用的做法。

**Q：以后要不要改成 AAB / 上架应用商店**
本项目定位为熟人私人牌局，不上架商店、只在维护者之间分发 APK。若将来需要，同一份密钥库同样适用于 AAB 构建。

## 10. 与其他文档的关系

- 四端构建命令与产物路径见[生产环境更新手册](PRODUCTION_UPDATE_GUIDE.md)。
- 当前签名状态与其他待办见[项目现状](PROJECT_STATUS.md)。
- 本机工具链版本见[开发环境与工具链状态](TOOLCHAIN_STATUS.md)。
