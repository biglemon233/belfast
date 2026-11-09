package androidx.compose.material.icons.outlined

import androidx.compose.material.icons.Icons
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.path
import androidx.compose.ui.unit.dp

private var _lockOpen: ImageVector? = null

public val Icons.Outlined.LockOpen: ImageVector
    get() {
        if (_lockOpen != null) return _lockOpen!!
        val builder = ImageVector.Builder(
            name = "Outlined.LockOpen",
            defaultWidth = 24.dp,
            defaultHeight = 24.dp,
            viewportWidth = 24f,
            viewportHeight = 24f,
        )
        builder.path(fill = SolidColor(Color(0xFF000000))) {
            moveTo(18.0f, 8.0f)
            horizontalLineToRelative(-1.0f)
            lineTo(17.0f, 6.0f)
            curveToRelative(0f, -2.76f, -2.24f, -5.0f, -5.0f, -5.0f)
            reflectiveCurveTo(7.0f, 3.24f, 7.0f, 6.0f)
            horizontalLineToRelative(2f)
            curveToRelative(0f, -1.66f, 1.34f, -3.0f, 3.0f, -3.0f)
            reflectiveCurveToRelative(3.0f, 1.34f, 3.0f, 3.0f)
            verticalLineToRelative(2f)
            lineTo(6.0f, 8.0f)
            curveToRelative(-1.1f, 0f, -2.0f, 0.9f, -2.0f, 2f)
            verticalLineToRelative(10.0f)
            curveToRelative(0f, 1.1f, 0.9f, 2f, 2f, 2f)
            horizontalLineToRelative(12.0f)
            curveToRelative(1.1f, 0f, 2f, -0.9f, 2f, -2.0f)
            lineTo(20.0f, 10.0f)
            curveToRelative(0f, -1.1f, -0.9f, -2.0f, -2.0f, -2.0f)
            close()
            moveTo(18.0f, 20.0f)
            lineTo(6.0f, 20.0f)
            lineTo(6.0f, 10.0f)
            horizontalLineToRelative(12.0f)
            verticalLineToRelative(10.0f)
            close()
            moveTo(12.0f, 17.0f)
            curveToRelative(1.1f, 0f, 2f, -0.9f, 2f, -2.0f)
            reflectiveCurveToRelative(-0.9f, -2.0f, -2.0f, -2.0f)
            reflectiveCurveToRelative(-2.0f, 0.9f, -2.0f, 2f)
            reflectiveCurveToRelative(0.9f, 2f, 2f, 2f)
            close()
        }
        _lockOpen = builder.build()
        return _lockOpen!!
    }

private var _playCircle: ImageVector? = null

public val Icons.Outlined.PlayCircle: ImageVector
    get() {
        if (_playCircle != null) return _playCircle!!
        val builder = ImageVector.Builder(
            name = "Outlined.PlayCircle",
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
            moveTo(12.0f, 20.0f)
            curveToRelative(-4.41f, 0f, -8.0f, -3.59f, -8.0f, -8.0f)
            reflectiveCurveToRelative(3.59f, -8.0f, 8.0f, -8.0f)
            reflectiveCurveToRelative(8.0f, 3.59f, 8.0f, 8.0f)
            reflectiveCurveTo(16.41f, 20.0f, 12.0f, 20.0f)
            close()
            moveTo(9.5f, 16.5f)
            lineToRelative(7.0f, -4.5f)
            lineToRelative(-7.0f, -4.5f)
            verticalLineTo(16.5f)
            close()
        }
        _playCircle = builder.build()
        return _playCircle!!
    }

private var _public: ImageVector? = null

public val Icons.Outlined.Public: ImageVector
    get() {
        if (_public != null) return _public!!
        val builder = ImageVector.Builder(
            name = "Outlined.Public",
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
            moveTo(4.0f, 12.0f)
            curveToRelative(0f, -0.61f, 0.08f, -1.21f, 0.21f, -1.78f)
            lineTo(8.99f, 15.0f)
            verticalLineToRelative(1f)
            curveToRelative(0f, 1.1f, 0.9f, 2f, 2f, 2f)
            verticalLineToRelative(1.93f)
            curveTo(7.06f, 19.43f, 4.0f, 16.07f, 4.0f, 12.0f)
            close()
            moveTo(17.89f, 17.4f)
            curveToRelative(-0.26f, -0.81f, -1.0f, -1.4f, -1.9f, -1.4f)
            horizontalLineToRelative(-1.0f)
            verticalLineToRelative(-3.0f)
            curveToRelative(0f, -0.55f, -0.45f, -1.0f, -1.0f, -1.0f)
            horizontalLineToRelative(-6.0f)
            verticalLineToRelative(-2.0f)
            horizontalLineToRelative(2f)
            curveToRelative(0.55f, 0f, 1f, -0.45f, 1f, -1.0f)
            lineTo(10.99f, 7.0f)
            horizontalLineToRelative(2f)
            curveToRelative(1.1f, 0f, 2f, -0.9f, 2f, -2.0f)
            verticalLineToRelative(-0.41f)
            curveTo(17.92f, 5.77f, 20.0f, 8.65f, 20.0f, 12.0f)
            curveToRelative(0f, 2.08f, -0.81f, 3.98f, -2.11f, 5.4f)
            close()
        }
        _public = builder.build()
        return _public!!
    }

private var _warningAmber: ImageVector? = null

public val Icons.Outlined.WarningAmber: ImageVector
    get() {
        if (_warningAmber != null) return _warningAmber!!
        val builder = ImageVector.Builder(
            name = "Outlined.WarningAmber",
            defaultWidth = 24.dp,
            defaultHeight = 24.dp,
            viewportWidth = 24f,
            viewportHeight = 24f,
        )
        builder.path(fill = SolidColor(Color(0xFF000000))) {
            moveTo(12.0f, 5.99f)
            lineTo(19.53f, 19.0f)
            lineTo(4.47f, 19.0f)
            lineTo(12.0f, 5.99f)
            moveTo(12.0f, 2f)
            lineTo(1f, 21.0f)
            horizontalLineToRelative(22.0f)
            lineTo(12.0f, 2f)
            close()
            moveTo(13.0f, 16.0f)
            horizontalLineToRelative(-2.0f)
            verticalLineToRelative(2f)
            horizontalLineToRelative(2f)
            verticalLineToRelative(-2.0f)
            close()
            moveTo(13.0f, 10.0f)
            horizontalLineToRelative(-2.0f)
            verticalLineToRelative(4.0f)
            horizontalLineToRelative(2f)
            verticalLineToRelative(-4.0f)
            close()
        }
        _warningAmber = builder.build()
        return _warningAmber!!
    }

private var _ondemandVideo: ImageVector? = null

public val Icons.Outlined.OndemandVideo: ImageVector
    get() {
        if (_ondemandVideo != null) return _ondemandVideo!!
        val builder = ImageVector.Builder(
            name = "Outlined.OndemandVideo",
            defaultWidth = 24.dp,
            defaultHeight = 24.dp,
            viewportWidth = 24f,
            viewportHeight = 24f,
        )
        builder.path(fill = SolidColor(Color(0xFF000000))) {
            moveTo(9.0f, 7.0f)
            verticalLineToRelative(8.0f)
            lineToRelative(7.0f, -4.0f)
            close()
            moveTo(21.0f, 3.0f)
            lineTo(3.0f, 3.0f)
            curveToRelative(-1.1f, 0f, -2.0f, 0.9f, -2.0f, 2f)
            verticalLineToRelative(12.0f)
            curveToRelative(0f, 1.1f, 0.9f, 2f, 2f, 2f)
            horizontalLineToRelative(5.0f)
            verticalLineToRelative(2f)
            horizontalLineToRelative(8.0f)
            verticalLineToRelative(-2.0f)
            horizontalLineToRelative(5.0f)
            curveToRelative(1.1f, 0f, 2f, -0.9f, 2f, -2.0f)
            lineTo(23.0f, 5.0f)
            curveToRelative(0f, -1.1f, -0.9f, -2.0f, -2.0f, -2.0f)
            close()
            moveTo(21.0f, 17.0f)
            lineTo(3.0f, 17.0f)
            lineTo(3.0f, 5.0f)
            horizontalLineToRelative(18.0f)
            verticalLineToRelative(12.0f)
            close()
        }
        _ondemandVideo = builder.build()
        return _ondemandVideo!!
    }

private var _deleteOutline: ImageVector? = null

public val Icons.Outlined.DeleteOutline: ImageVector
    get() {
        if (_deleteOutline != null) return _deleteOutline!!
        val builder = ImageVector.Builder(
            name = "Outlined.DeleteOutline",
            defaultWidth = 24.dp,
            defaultHeight = 24.dp,
            viewportWidth = 24f,
            viewportHeight = 24f,
        )
        builder.path(fill = SolidColor(Color(0xFF000000))) {
            moveTo(6.0f, 19.0f)
            curveToRelative(0f, 1.1f, 0.9f, 2f, 2f, 2f)
            horizontalLineToRelative(8.0f)
            curveToRelative(1.1f, 0f, 2f, -0.9f, 2f, -2.0f)
            lineTo(18.0f, 7.0f)
            lineTo(6.0f, 7.0f)
            verticalLineToRelative(12.0f)
            close()
            moveTo(8.0f, 9.0f)
            horizontalLineToRelative(8.0f)
            verticalLineToRelative(10.0f)
            lineTo(8.0f, 19.0f)
            lineTo(8.0f, 9.0f)
            close()
            moveTo(15.5f, 4.0f)
            lineToRelative(-1.0f, -1.0f)
            horizontalLineToRelative(-5.0f)
            lineToRelative(-1.0f, 1f)
            lineTo(5.0f, 4.0f)
            verticalLineToRelative(2f)
            horizontalLineToRelative(14.0f)
            lineTo(19.0f, 4.0f)
            horizontalLineToRelative(-3.5f)
            close()
        }
        _deleteOutline = builder.build()
        return _deleteOutline!!
    }

private var _save: ImageVector? = null

public val Icons.Outlined.Save: ImageVector
    get() {
        if (_save != null) return _save!!
        val builder = ImageVector.Builder(
            name = "Outlined.Save",
            defaultWidth = 24.dp,
            defaultHeight = 24.dp,
            viewportWidth = 24f,
            viewportHeight = 24f,
        )
        builder.path(fill = SolidColor(Color(0xFF000000))) {
            moveTo(17.0f, 3.0f)
            lineTo(5.0f, 3.0f)
            curveToRelative(-1.11f, 0f, -2.0f, 0.9f, -2.0f, 2f)
            verticalLineToRelative(14.0f)
            curveToRelative(0f, 1.1f, 0.89f, 2f, 2f, 2f)
            horizontalLineToRelative(14.0f)
            curveToRelative(1.1f, 0f, 2f, -0.9f, 2f, -2.0f)
            lineTo(21.0f, 7.0f)
            lineToRelative(-4.0f, -4.0f)
            close()
            moveTo(19.0f, 19.0f)
            lineTo(5.0f, 19.0f)
            lineTo(5.0f, 5.0f)
            horizontalLineToRelative(11.17f)
            lineTo(19.0f, 7.83f)
            lineTo(19.0f, 19.0f)
            close()
            moveTo(12.0f, 12.0f)
            curveToRelative(-1.66f, 0f, -3.0f, 1.34f, -3.0f, 3.0f)
            reflectiveCurveToRelative(1.34f, 3.0f, 3.0f, 3.0f)
            reflectiveCurveToRelative(3.0f, -1.34f, 3.0f, -3.0f)
            reflectiveCurveToRelative(-1.34f, -3.0f, -3.0f, -3.0f)
            close()
            moveTo(6.0f, 6.0f)
            horizontalLineToRelative(9.0f)
            verticalLineToRelative(4.0f)
            lineTo(6.0f, 10.0f)
            close()
        }
        _save = builder.build()
        return _save!!
    }
