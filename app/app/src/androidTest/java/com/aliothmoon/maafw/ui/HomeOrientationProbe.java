package com.aliothmoon.maafw.ui;

import android.app.Activity;
import android.app.Instrumentation;
import android.content.pm.ActivityInfo;
import android.content.res.Configuration;
import android.graphics.Bitmap;
import android.graphics.Rect;
import android.os.Bundle;
import android.os.ParcelFileDescriptor;
import android.os.SystemClock;
import android.view.accessibility.AccessibilityNodeInfo;
import java.io.File;
import java.io.FileOutputStream;
import java.io.InputStream;

/** Framework-only probe for a provisioned, idle device, including minified release builds. */
public class HomeOrientationProbe extends Instrumentation {
    private volatile Activity resumedActivity;

    @Override public void onCreate(Bundle arguments) {
        super.onCreate(arguments);
        start();
    }

    @Override public void callActivityOnResume(Activity activity) {
        super.callActivityOnResume(activity);
        resumedActivity = activity;
    }

    @Override public void onStart() {
        Bundle result = new Bundle();
        Activity activity = null;
        try {
            // Shell launch also works on ROMs that block background Activity launches by apps.
            ParcelFileDescriptor command = getUiAutomation().executeShellCommand(
                    "am start -a android.intent.action.MAIN -n " + getTargetContext().getPackageName()
                            + "/com.aliothmoon.maafw.MainActivity");
            try (InputStream stream = new ParcelFileDescriptor.AutoCloseInputStream(command)) {
                byte[] buffer = new byte[1024];
                while (stream.read(buffer) != -1) { /* Wait for launch result. */ }
            }
            long deadline = SystemClock.uptimeMillis() + 15000;
            while (resumedActivity == null && SystemClock.uptimeMillis() < deadline) SystemClock.sleep(100);
            activity = resumedActivity;
            require(activity != null, "Activity did not resume");
            require(activity.getRequestedOrientation() == ActivityInfo.SCREEN_ORIENTATION_PORTRAIT,
                    "Production portrait lock changed");
            final Activity target = activity;
            runOnMainSync(() -> target.setRequestedOrientation(ActivityInfo.SCREEN_ORIENTATION_LANDSCAPE));
            awaitOrientation(target, Configuration.ORIENTATION_LANDSCAPE);
            SystemClock.sleep(2000);
            capture(target, "home-landscape-verified.png");
            AccessibilityNodeInfo root = getUiAutomation().getRootInActiveWindow();
            require(root != null, "No accessible application window");
            for (String key : new String[]{"hangar_config_label", "overlay_alas_start"}) {
                int id = target.getResources().getIdentifier(key, "string", getTargetContext().getPackageName());
                require(id != 0 && hasVisibleText(root, target.getString(id)), "Control not visible: " + key);
            }
            result.putString("landscape", "PASS");
        } catch (Throwable error) {
            result.putString("failure", error.toString());
        } finally {
            try {
                if (activity != null) {
                    final Activity target = activity;
                    runOnMainSync(() -> target.setRequestedOrientation(ActivityInfo.SCREEN_ORIENTATION_PORTRAIT));
                    awaitOrientation(target, Configuration.ORIENTATION_PORTRAIT);
                    SystemClock.sleep(1000);
                    capture(target, "home-portrait-restored.png");
                    result.putString("portraitRestored", "true");
                }
            } catch (Throwable error) {
                result.putString("failure", error.toString());
            }
            finish(result.containsKey("failure") ? Activity.RESULT_CANCELED : Activity.RESULT_OK, result);
        }
    }

    private static void awaitOrientation(Activity activity, int expected) {
        long deadline = SystemClock.uptimeMillis() + 10000;
        while (activity.getResources().getConfiguration().orientation != expected
                && SystemClock.uptimeMillis() < deadline) SystemClock.sleep(100);
        require(activity.getResources().getConfiguration().orientation == expected, "Orientation did not settle");
    }

    private static boolean hasVisibleText(AccessibilityNodeInfo node, String text) {
        Rect bounds = new Rect();
        node.getBoundsInScreen(bounds);
        if (text.contentEquals(node.getText() == null ? "" : node.getText())
                && node.isVisibleToUser() && !bounds.isEmpty()) return true;
        for (int i = 0; i < node.getChildCount(); i++) {
            AccessibilityNodeInfo child = node.getChild(i);
            if (child != null && hasVisibleText(child, text)) return true;
        }
        return false;
    }

    private void capture(Activity activity, String name) throws Exception {
        Bitmap bitmap = getUiAutomation().takeScreenshot();
        try (FileOutputStream stream = new FileOutputStream(new File(activity.getCacheDir(), name))) {
            bitmap.compress(Bitmap.CompressFormat.PNG, 100, stream);
        } finally {
            bitmap.recycle();
        }
    }

    private static void require(boolean value, String message) {
        if (!value) throw new AssertionError(message);
    }
}
