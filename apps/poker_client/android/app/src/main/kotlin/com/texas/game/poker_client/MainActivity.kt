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

    // 除了挖孔，还要让开手势导航条：平板横屏时它压在屏幕底部，落在那片区域的
    // 按钮即使画出来了也点不动（触摸归系统）。两块区域都问系统要，不猜像素。
    private fun readDisplayCutoutInsets(): Map<String, Double> {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.P) {
            return zeroCutoutInsets()
        }
        val rootInsets = window.decorView.rootWindowInsets ?: return zeroCutoutInsets()
        val cutout = rootInsets.displayCutout
        val waterfall = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            cutout?.waterfallInsets
        } else {
            null
        }
        val navigation = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            rootInsets.getInsets(WindowInsets.Type.navigationBars())
        } else {
            null
        }
        val density = resources.displayMetrics.density.toDouble()
            .takeIf { it > 0 } ?: 1.0
        fun inset(safe: Int?, waterfallEdge: Int?, navigationEdge: Int?): Double =
            max(max(safe ?: 0, waterfallEdge ?: 0), navigationEdge ?: 0) / density
        return mapOf(
            "left" to inset(cutout?.safeInsetLeft, waterfall?.left, navigation?.left),
            "top" to inset(cutout?.safeInsetTop, waterfall?.top, navigation?.top),
            "right" to inset(cutout?.safeInsetRight, waterfall?.right, navigation?.right),
            "bottom" to inset(cutout?.safeInsetBottom, waterfall?.bottom, navigation?.bottom),
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
