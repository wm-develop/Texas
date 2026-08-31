package com.texas.game.poker_client

import android.os.Build
import android.os.Bundle
import android.view.View
import android.view.WindowInsets
import android.view.WindowInsetsController
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel
import kotlin.math.max

class MainActivity : FlutterActivity() {
    companion object {
        private const val DISPLAY_CUTOUT_CHANNEL =
            "com.texas.game.poker_client/display_cutout"
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        hideSystemBars()
    }

    override fun onWindowFocusChanged(hasFocus: Boolean) {
        super.onWindowFocusChanged(hasFocus)
        if (hasFocus) hideSystemBars()
    }

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        MethodChannel(
            flutterEngine.dartExecutor.binaryMessenger,
            DISPLAY_CUTOUT_CHANNEL,
        ).setMethodCallHandler { call, result ->
            if (call.method == "getInsets") {
                result.success(readDisplayCutoutInsets())
            } else {
                result.notImplemented()
            }
        }
    }

    private fun readDisplayCutoutInsets(): Map<String, Double> {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.P) {
            return zeroCutoutInsets()
        }
        val cutout = window.decorView.rootWindowInsets?.displayCutout
            ?: return zeroCutoutInsets()
        val waterfall = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            cutout.waterfallInsets
        } else {
            null
        }
        val density = resources.displayMetrics.density.toDouble()
            .takeIf { it > 0 } ?: 1.0
        return mapOf(
            "left" to max(cutout.safeInsetLeft, waterfall?.left ?: 0) / density,
            "top" to max(cutout.safeInsetTop, waterfall?.top ?: 0) / density,
            "right" to max(cutout.safeInsetRight, waterfall?.right ?: 0) / density,
            "bottom" to max(cutout.safeInsetBottom, waterfall?.bottom ?: 0) / density,
        )
    }

    private fun zeroCutoutInsets() = mapOf(
        "left" to 0.0,
        "top" to 0.0,
        "right" to 0.0,
        "bottom" to 0.0,
    )

    @Suppress("DEPRECATION")
    private fun hideSystemBars() {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            window.setDecorFitsSystemWindows(false)
            window.insetsController?.apply {
                hide(WindowInsets.Type.systemBars())
                systemBarsBehavior =
                    WindowInsetsController.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
            }
        } else {
            window.decorView.systemUiVisibility =
                View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY or
                View.SYSTEM_UI_FLAG_FULLSCREEN or
                View.SYSTEM_UI_FLAG_HIDE_NAVIGATION or
                View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN or
                View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION or
                View.SYSTEM_UI_FLAG_LAYOUT_STABLE
        }
    }
}
