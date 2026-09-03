package com.texas.game.poker_client

import android.os.Build
import android.os.Bundle
import android.view.View
import android.view.RoundedCorner
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
        ) + readCornerRadii(rootInsets, density)
    }

    // 屏幕圆角会把贴边的控件切掉一角。它不属于挖孔也不属于系统栏，两套
    // inset 都不包含它，只能单独问系统要（API 31 起才有这个接口）。
    private fun readCornerRadii(rootInsets: WindowInsets, density: Double): Map<String, Double> {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.S) {
            return zeroCornerRadii()
        }
        fun radius(position: Int): Double =
            (rootInsets.getRoundedCorner(position)?.radius ?: 0) / density
        return mapOf(
            "cornerTopLeft" to radius(RoundedCorner.POSITION_TOP_LEFT),
            "cornerTopRight" to radius(RoundedCorner.POSITION_TOP_RIGHT),
            "cornerBottomLeft" to radius(RoundedCorner.POSITION_BOTTOM_LEFT),
            "cornerBottomRight" to radius(RoundedCorner.POSITION_BOTTOM_RIGHT),
        )
    }

    private fun zeroCornerRadii() = mapOf(
        "cornerTopLeft" to 0.0,
        "cornerTopRight" to 0.0,
        "cornerBottomLeft" to 0.0,
        "cornerBottomRight" to 0.0,
    )

    private fun zeroCutoutInsets() = mapOf(
        "left" to 0.0,
        "top" to 0.0,
        "right" to 0.0,
        "bottom" to 0.0,
    ) + zeroCornerRadii()

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
