import java.util.Properties

plugins {
    id("com.android.application")
    id("kotlin-android")
    // The Flutter Gradle Plugin must be applied after the Android and Kotlin Gradle plugins.
    id("dev.flutter.flutter-gradle-plugin")
}

// Release signing is configured through android/key.properties, which is git
// ignored and never committed. Every maintainer machine must use the SAME
// keystore, otherwise the produced APKs cannot upgrade each other on a device.
// When the file is absent the build falls back to the debug keystore so that
// local development still works; such an APK is NOT suitable for distribution.
val keystoreProperties = Properties()
val keystorePropertiesFile = rootProject.file("key.properties")
val hasReleaseKeystore = keystorePropertiesFile.exists()
if (hasReleaseKeystore) {
    keystorePropertiesFile.inputStream().use { keystoreProperties.load(it) }
}

android {
    namespace = "com.texas.game.poker_client"
    compileSdk = flutter.compileSdkVersion
    ndkVersion = flutter.ndkVersion

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    kotlinOptions {
        jvmTarget = JavaVersion.VERSION_17.toString()
    }

    defaultConfig {
        // TODO: Specify your own unique Application ID (https://developer.android.com/studio/build/application-id.html).
        applicationId = "com.texas.game.poker_client"
        // You can update the following values to match your application needs.
        // For more information, see: https://flutter.dev/to/review-gradle-config.
        minSdk = flutter.minSdkVersion
        targetSdk = flutter.targetSdkVersion
        versionCode = flutter.versionCode
        versionName = flutter.versionName
    }

    signingConfigs {
        if (hasReleaseKeystore) {
            create("release") {
                storeFile = file(keystoreProperties.getProperty("storeFile"))
                storePassword = keystoreProperties.getProperty("storePassword")
                keyAlias = keystoreProperties.getProperty("keyAlias")
                keyPassword = keystoreProperties.getProperty("keyPassword")
            }
        }
    }

    buildTypes {
        release {
            signingConfig = if (hasReleaseKeystore) {
                signingConfigs.getByName("release")
            } else {
                // No key.properties on this machine: keep debug signing so that
                // `flutter run --release` works. Debug keystores are generated
                // per machine, so these APKs cannot be installed over an APK
                // built elsewhere.
                logger.warn(
                    "android/key.properties not found; the Release APK is signed " +
                        "with the local debug keystore and must not be distributed."
                )
                signingConfigs.getByName("debug")
            }

            // Several native Flutter plugins used by the Android client (most
            // notably the TRTC SDK) register classes dynamically. R8 cannot see
            // those references and may remove them, which makes the product APK
            // terminate during startup even though debug/profile builds work.
            // Keep the Release build AOT-compiled, but disable Java/Kotlin code
            // and resource shrinking until every plugin supplies verified keep
            // rules for this Flutter OH toolchain.
            isMinifyEnabled = false
            isShrinkResources = false
        }
    }
}

flutter {
    source = "../.."
}
