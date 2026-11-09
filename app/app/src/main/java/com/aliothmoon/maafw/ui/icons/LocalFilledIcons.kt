package androidx.compose.material.icons.filled

import androidx.compose.material.icons.Icons
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.path
import androidx.compose.ui.unit.dp

private var _stop: ImageVector? = null

/** Keep this simple glyph local instead of shipping the extended icon library. */
public val Icons.Filled.Stop: ImageVector
    get() = _stop ?: ImageVector.Builder(
        name = "Filled.Stop", defaultWidth = 24.dp, defaultHeight = 24.dp,
        viewportWidth = 24f, viewportHeight = 24f,
    ).apply {
        path(fill = SolidColor(Color.Black)) {
            moveTo(6f, 6f)
            horizontalLineTo(18f)
            verticalLineTo(18f)
            horizontalLineTo(6f)
            close()
        }
    }.build().also { _stop = it }

private var _playCircle: ImageVector? = null

public val Icons.Filled.PlayCircle: ImageVector
    get() {
        if (_playCircle != null) return _playCircle!!
        val builder = ImageVector.Builder(
            name = "Filled.PlayCircle",
            defaultWidth = 24.dp,
            defaultHeight = 24.dp,
            viewportWidth = 24f,
            viewportHeight = 24f,
        )
        builder.path(fill = SolidColor(Color(0xFF000000))) {
            moveTo(12.0f, 2f)
            curveTo(6.48f, 2f, 2f, 6.48f, 2f, 12.0f)
            reflectiveCurveToRelative(4.48f, 10.0f, 10.0f, 10.0f)
            reflectiveCurveToRelative(10.0f, -4.48f, 10.0f, -10.0f)
            reflectiveCurveTo(17.52f, 2f, 12.0f, 2f)
            close()
            moveTo(9.5f, 16.5f)
            verticalLineToRelative(-9.0f)
            lineToRelative(7.0f, 4.5f)
            lineTo(9.5f, 16.5f)
            close()
        }
        _playCircle = builder.build()
        return _playCircle!!
    }

private var _public: ImageVector? = null

public val Icons.Filled.Public: ImageVector
    get() {
        if (_public != null) return _public!!
        val builder = ImageVector.Builder(
            name = "Filled.Public",
            defaultWidth = 24.dp,
            defaultHeight = 24.dp,
            viewportWidth = 24f,
            viewportHeight = 24f,
        )
        builder.path(fill = SolidColor(Color(0xFF000000))) {
            moveTo(12.0f, 2f)
            curveTo(6.48f, 2f, 2f, 6.48f, 2f, 12.0f)
            reflectiveCurveToRelative(4.48f, 10.0f, 10.0f, 10.0f)
            reflectiveCurveToRelative(10.0f, -4.48f, 10.0f, -10.0f)
            reflectiveCurveTo(17.52f, 2f, 12.0f, 2f)
            close()
            moveTo(11.0f, 19.93f)
            curveToRelative(-3.95f, -0.49f, -7.0f, -3.85f, -7.0f, -7.93f)
            curveToRelative(0f, -0.62f, 0.08f, -1.21f, 0.21f, -1.79f)
            lineTo(9.0f, 15.0f)
            verticalLineToRelative(1f)
            curveToRelative(0f, 1.1f, 0.9f, 2f, 2f, 2f)
            verticalLineToRelative(1.93f)
            close()
            moveTo(17.9f, 17.39f)
            curveToRelative(-0.26f, -0.81f, -1.0f, -1.39f, -1.9f, -1.39f)
            horizontalLineToRelative(-1.0f)
            verticalLineToRelative(-3.0f)
            curveToRelative(0f, -0.55f, -0.45f, -1.0f, -1.0f, -1.0f)
            lineTo(8.0f, 12.0f)
            verticalLineToRelative(-2.0f)
            horizontalLineToRelative(2f)
            curveToRelative(0.55f, 0f, 1f, -0.45f, 1f, -1.0f)
            lineTo(11.0f, 7.0f)
            horizontalLineToRelative(2f)
            curveToRelative(1.1f, 0f, 2f, -0.9f, 2f, -2.0f)
            verticalLineToRelative(-0.41f)
            curveToRelative(2.93f, 1.19f, 5.0f, 4.06f, 5.0f, 7.41f)
            curveToRelative(0f, 2.08f, -0.8f, 3.97f, -2.1f, 5.39f)
            close()
        }
        _public = builder.build()
        return _public!!
    }

private var _visibility: ImageVector? = null

public val Icons.Filled.Visibility: ImageVector
    get() {
        if (_visibility != null) return _visibility!!
        val builder = ImageVector.Builder(
            name = "Filled.Visibility",
            defaultWidth = 24.dp,
            defaultHeight = 24.dp,
            viewportWidth = 24f,
            viewportHeight = 24f,
        )
        builder.path(fill = SolidColor(Color(0xFF000000))) {
            moveTo(12.0f, 4.5f)
            curveTo(7.0f, 4.5f, 2.73f, 7.61f, 1f, 12.0f)
            curveToRelative(1.73f, 4.39f, 6.0f, 7.5f, 11.0f, 7.5f)
            reflectiveCurveToRelative(9.27f, -3.11f, 11.0f, -7.5f)
            curveToRelative(-1.73f, -4.39f, -6.0f, -7.5f, -11.0f, -7.5f)
            close()
            moveTo(12.0f, 17.0f)
            curveToRelative(-2.76f, 0f, -5.0f, -2.24f, -5.0f, -5.0f)
            reflectiveCurveToRelative(2.24f, -5.0f, 5.0f, -5.0f)
            reflectiveCurveToRelative(5.0f, 2.24f, 5.0f, 5.0f)
            reflectiveCurveToRelative(-2.24f, 5.0f, -5.0f, 5.0f)
            close()
            moveTo(12.0f, 9.0f)
            curveToRelative(-1.66f, 0f, -3.0f, 1.34f, -3.0f, 3.0f)
            reflectiveCurveToRelative(1.34f, 3.0f, 3.0f, 3.0f)
            reflectiveCurveToRelative(3.0f, -1.34f, 3.0f, -3.0f)
            reflectiveCurveToRelative(-1.34f, -3.0f, -3.0f, -3.0f)
            close()
        }
        _visibility = builder.build()
        return _visibility!!
    }

private var _visibilityOff: ImageVector? = null

public val Icons.Filled.VisibilityOff: ImageVector
    get() {
        if (_visibilityOff != null) return _visibilityOff!!
        val builder = ImageVector.Builder(
            name = "Filled.VisibilityOff",
            defaultWidth = 24.dp,
            defaultHeight = 24.dp,
            viewportWidth = 24f,
            viewportHeight = 24f,
        )
        builder.path(fill = SolidColor(Color(0xFF000000))) {
            moveTo(12.0f, 7.0f)
            curveToRelative(2.76f, 0f, 5.0f, 2.24f, 5.0f, 5.0f)
            curveToRelative(0f, 0.65f, -0.13f, 1.26f, -0.36f, 1.83f)
            lineToRelative(2.92f, 2.92f)
            curveToRelative(1.51f, -1.26f, 2.7f, -2.89f, 3.43f, -4.75f)
            curveToRelative(-1.73f, -4.39f, -6.0f, -7.5f, -11.0f, -7.5f)
            curveToRelative(-1.4f, 0f, -2.74f, 0.25f, -3.98f, 0.7f)
            lineToRelative(2.16f, 2.16f)
            curveTo(10.74f, 7.13f, 11.35f, 7.0f, 12.0f, 7.0f)
            close()
            moveTo(2f, 4.27f)
            lineToRelative(2.28f, 2.28f)
            lineToRelative(0.46f, 0.46f)
            curveTo(3.08f, 8.3f, 1.78f, 10.02f, 1f, 12.0f)
            curveToRelative(1.73f, 4.39f, 6.0f, 7.5f, 11.0f, 7.5f)
            curveToRelative(1.55f, 0f, 3.03f, -0.3f, 4.38f, -0.84f)
            lineToRelative(0.42f, 0.42f)
            lineTo(19.73f, 22.0f)
            lineTo(21.0f, 20.73f)
            lineTo(3.27f, 3.0f)
            lineTo(2f, 4.27f)
            close()
            moveTo(7.53f, 9.8f)
            lineToRelative(1.55f, 1.55f)
            curveToRelative(-0.05f, 0.21f, -0.08f, 0.43f, -0.08f, 0.65f)
            curveToRelative(0f, 1.66f, 1.34f, 3.0f, 3.0f, 3.0f)
            curveToRelative(0.22f, 0f, 0.44f, -0.03f, 0.65f, -0.08f)
            lineToRelative(1.55f, 1.55f)
            curveToRelative(-0.67f, 0.33f, -1.41f, 0.53f, -2.2f, 0.53f)
            curveToRelative(-2.76f, 0f, -5.0f, -2.24f, -5.0f, -5.0f)
            curveToRelative(0f, -0.79f, 0.2f, -1.53f, 0.53f, -2.2f)
            close()
            moveTo(11.84f, 9.02f)
            lineToRelative(3.15f, 3.15f)
            lineToRelative(0.02f, -0.16f)
            curveToRelative(0f, -1.66f, -1.34f, -3.0f, -3.0f, -3.0f)
            lineToRelative(-0.17f, 0.01f)
            close()
        }
        _visibilityOff = builder.build()
        return _visibilityOff!!
    }
