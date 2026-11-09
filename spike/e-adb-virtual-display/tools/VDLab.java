import java.lang.reflect.Method;

public final class VDLab {
    public static void main(String[] args) {
        try {
            run(args);
        } catch (Throwable t) {
            System.out.println("[vdlab] ERROR: " + t);
            t.printStackTrace(System.out);
        }
    }

    private static void run(String[] args) throws Exception {
        if (args.length == 0) {
            System.out.println("[vdlab] modes: methods | power <displayId> <on|off> | sfpower <physicalDisplayId> <mode> | phyids");
            return;
        }
        String mode = args[0];
        if ("methods".equals(mode)) {
            Object global = Class.forName("android.hardware.display.DisplayManagerGlobal")
                    .getMethod("getInstance").invoke(null);
            for (Method m : global.getClass().getMethods()) {
                String n = m.getName().toLowerCase();
                if (n.contains("power") || n.contains("display")) {
                    System.out.println("[vdlab] DMGlobal." + m);
                }
            }
            Class<?> sc = Class.forName("android.view.SurfaceControl");
            for (Method m : sc.getMethods()) {
                String n = m.getName().toLowerCase();
                if (n.contains("power") || n.contains("displaytoken") || n.contains("physicaldisplay")) {
                    System.out.println("[vdlab] SurfaceControl." + m);
                }
            }
        } else if ("power".equals(mode)) {
            int id = Integer.parseInt(args[1]);
            boolean on = Boolean.parseBoolean(args[2]);
            Object global = Class.forName("android.hardware.display.DisplayManagerGlobal")
                    .getMethod("getInstance").invoke(null);
            try {
                Method m = global.getClass().getMethod("requestDisplayPower", int.class, boolean.class);
                Object r = m.invoke(global, id, on);
                System.out.println("[vdlab] requestDisplayPower(int,boolean)(" + id + "," + on + ") -> " + r);
            } catch (NoSuchMethodException e) {
                System.out.println("[vdlab] no (int,boolean) variant");
            }
            try {
                Method m = global.getClass().getMethod("requestDisplayPower", int.class, int.class);
                Object r = m.invoke(global, id, on ? 2 : 1);
                System.out.println("[vdlab] requestDisplayPower(int,int)(" + id + "," + (on ? 2 : 1) + ") -> " + r);
            } catch (NoSuchMethodException e) {
                System.out.println("[vdlab] no (int,int) variant");
            }
        } else if ("sfpower".equals(mode)) {
            long physId = Long.parseLong(args[1]);
            int modeInt = Integer.parseInt(args[2]);
            Class<?> sc = Class.forName("android.view.SurfaceControl");
            Class<?> ibinder = Class.forName("android.os.IBinder");
            Object token = sc.getMethod("getPhysicalDisplayToken", long.class).invoke(null, physId);
            System.out.println("[vdlab] token=" + token);
            Object ok = sc.getMethod("setDisplayPowerMode", ibinder, int.class).invoke(null, token, modeInt);
            System.out.println("[vdlab] setDisplayPowerMode(" + physId + "," + modeInt + ") -> " + ok);
        } else if ("phyids".equals(mode)) {
            Class<?> sc = Class.forName("android.view.SurfaceControl");
            long[] ids = (long[]) sc.getMethod("getPhysicalDisplayIds").invoke(null);
            for (long id : ids) {
                System.out.println("[vdlab] physical display id: " + id);
            }
        } else {
            System.out.println("[vdlab] unknown mode: " + mode);
        }
    }
}
