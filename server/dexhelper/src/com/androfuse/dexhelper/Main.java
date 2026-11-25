package com.androfuse.dexhelper;

import android.app.Application;
import android.app.Instrumentation;
import android.content.Context;
import android.content.ContextWrapper;
import android.content.Intent;
import android.content.pm.ActivityInfo;
import android.content.pm.ApplicationInfo;
import android.content.pm.PackageManager;
import android.content.pm.ResolveInfo;
import android.content.res.Resources;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.drawable.Drawable;
import android.os.Build;
import android.os.Handler;
import android.os.HandlerThread;
import android.os.IBinder;
import android.os.Looper;
import android.system.Os;
import android.util.Base64;
import android.util.DisplayMetrics;

import java.io.BufferedReader;
import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.IOException;
import java.io.InputStreamReader;
import java.io.OutputStream;
import java.io.RandomAccessFile;
import java.lang.reflect.Constructor;
import java.lang.reflect.Field;
import java.lang.reflect.Method;
import java.lang.reflect.Proxy;
import java.net.DatagramPacket;
import java.net.DatagramSocket;
import java.net.InetAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.nio.channels.FileLock;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Set;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.regex.Pattern;

/**
 * Clean-room app_process DeX helper for AndroFuse (UID 2000 shell, no Root).
 *
 * Subcommands:
 *   probe
 *   serve <token> <width> <height> <dpi> <grace_sec>
 *   recover
 */
public final class Main {

    private static final int RTSP_PORT = 7236;
    private static final int MODE_WIRELESS_DEX = 2;
    private static final int APP_ICON_SIZE_PX = 96;
    private static final Pattern HEX_SEGMENT_PATTERN = Pattern.compile("^[0-9a-fA-F]{12,}(_v[0-9]+)?$");
    private static final String DISPLAY_NAME = "AndroFuseDeX";
    private static final String DUMMY_MAC = "02:00:00:41:46:58";
    private static final String LOCK_PATH = "/data/local/tmp/androfuse-dex.lock";

    private Main() {}

    public static void main(String[] args) {
        if (args.length < 1) {
            System.err.println("usage: Main <probe|apps [--no-icons]|serve <token> <w> <h> <dpi> <grace_sec>|recover>");
            System.exit(2);
        }
        try {
            Looper.prepareMainLooper();
            if ("apps".equals(args[0])) {
                boolean includeIcons = !(args.length >= 2 && "--no-icons".equals(args[1]));
                runApps(includeIcons);
                return;
            }
            bootstrapWorkarounds();
            switch (args[0]) {
                case "probe":
                    runProbe();
                    break;
                case "recover":
                    runRecover();
                    break;
                case "serve":
                    if (args.length != 6) {
                        throw new IllegalArgumentException("serve expects <token> <width> <height> <dpi> <grace_sec>");
                    }
                    String token = validateToken(args[1]);
                    int width = Integer.parseInt(args[2]);
                    int height = Integer.parseInt(args[3]);
                    int dpi = Integer.parseInt(args[4]);
                    int graceSec = Integer.parseInt(args[5]);
                    runServe(token, width, height, dpi, graceSec);
                    break;
                default:
                    throw new IllegalArgumentException("unknown subcommand: " + args[0]);
            }
        } catch (Throwable t) {
            String msg = t.getClass().getSimpleName() + ": " + String.valueOf(t.getMessage());
            System.out.println("AFDEX1|ERR|" + sanitizeLine(msg));
            System.out.flush();
            t.printStackTrace(System.err);
            System.exit(1);
        }
    }

    private static String validateToken(String token) {
        if (token == null || !token.matches("[A-Za-z0-9_-]{4,48}")) {
            throw new IllegalArgumentException("invalid helper token");
        }
        return token;
    }

    private static void emit(String token, String kind, String payload) {
        System.out.println("AFDEX1|" + token + "|" + kind + "|" + payload);
        System.out.flush();
    }

    private static void runProbe() throws Exception {
        String manufacturer = nonNull(Build.MANUFACTURER);
        String model = nonNull(Build.MODEL);
        int sdk = Build.VERSION.SDK_INT;
        String oneUi = getSystemProperty("ro.build.version.oneui", "");
        String sem = getSystemProperty("ro.build.version.sem", "");

        boolean hasBuilder = false;
        boolean hasSetAp = false;
        boolean hasSetMode = false;
        boolean hasConnectConfig = false;
        boolean hasDisconnect = false;
        String connectError = "";

        try {
            Class<?> builderCls = Class.forName("android.hardware.display.SemWifiDisplayConfig$Builder");
            hasBuilder = true;
            builderCls.getMethod("setApConnection", String.class, String.class, String.class, String.class);
            hasSetAp = true;
            builderCls.getMethod("setMode", int.class);
            hasSetMode = true;
            builderCls.getMethod("build");
        } catch (Throwable t) {
            connectError = t.getClass().getSimpleName() + ": " + t.getMessage();
        }

        try {
            DisplayService svc = new DisplayService();
            hasConnectConfig = svc.hasConnectWithConfig();
            hasDisconnect = svc.hasDisconnect();
        } catch (Throwable t) {
            if (connectError.isEmpty()) {
                connectError = t.getClass().getSimpleName() + ": " + t.getMessage();
            }
        }

        List<DisplaySnapshot> displays = queryDisplays();
        StringBuilder sb = new StringBuilder();
        sb.append('{');
        sb.append("\"manufacturer\":").append(jsonStr(manufacturer)).append(',');
        sb.append("\"model\":").append(jsonStr(model)).append(',');
        sb.append("\"sdk\":").append(sdk).append(',');
        sb.append("\"oneui\":").append(jsonStr(oneUi)).append(',');
        sb.append("\"sem\":").append(jsonStr(sem)).append(',');
        sb.append("\"has_sem_builder\":").append(hasBuilder).append(',');
        sb.append("\"has_set_ap\":").append(hasSetAp).append(',');
        sb.append("\"has_set_mode\":").append(hasSetMode).append(',');
        sb.append("\"has_connect_with_config\":").append(hasConnectConfig).append(',');
        sb.append("\"has_disconnect_wifi_display\":").append(hasDisconnect).append(',');
        sb.append("\"probe_error\":").append(jsonStr(connectError)).append(',');
        sb.append("\"displays\":[");
        for (int i = 0; i < displays.size(); i++) {
            DisplaySnapshot d = displays.get(i);
            if (i > 0) sb.append(',');
            sb.append('{')
              .append("\"id\":").append(d.id).append(',')
              .append("\"name\":").append(jsonStr(d.name)).append(',')
              .append("\"width\":").append(d.width).append(',')
              .append("\"height\":").append(d.height).append(',')
              .append("\"dpi\":").append(d.dpi).append(',')
              .append("\"type\":").append(d.type).append(',')
              .append("\"flags\":").append(d.flags)
              .append('}');
        }
        sb.append("]}");
        System.out.println("AFDEX1|PROBE|" + sb);
        System.out.flush();
    }

    private static void runApps(boolean includeIcons) throws Exception {
        Context sysCtx = getSystemContextWorkaround();
        PackageManager pm = sysCtx.getPackageManager();
        if (pm == null) {
            throw new IllegalStateException("PackageManager unavailable in system context");
        }
        Intent launcherIntent = new Intent(Intent.ACTION_MAIN);
        launcherIntent.addCategory(Intent.CATEGORY_LAUNCHER);
        List<ResolveInfo> resolved = pm.queryIntentActivities(launcherIntent, PackageManager.MATCH_ALL);
        if (resolved == null || resolved.isEmpty()) {
            resolved = pm.queryIntentActivities(launcherIntent, 0);
        }

        Set<String> seen = new HashSet<>();
        int count = 0;
        if (resolved != null) {
            for (ResolveInfo ri : resolved) {
                if (ri == null || ri.activityInfo == null) continue;
                ActivityInfo act = ri.activityInfo;
                String pkg = nonNull(act.packageName).trim();
                if (isNoisyPackage(pkg) || !seen.add(pkg)) {
                    continue;
                }
                ApplicationInfo appInfo = act.applicationInfo;
                String label = "";
                try {
                    CharSequence cs = ri.loadLabel(pm);
                    if (cs != null) label = sanitizeLine(cs.toString()).trim();
                } catch (Throwable ignored) {}
                if (label.isEmpty() && appInfo != null) {
                    try {
                        CharSequence cs = appInfo.loadLabel(pm);
                        if (cs != null) label = sanitizeLine(cs.toString()).trim();
                    } catch (Throwable ignored) {}
                }
                if (label.isEmpty()) {
                    label = pkg;
                }

                boolean isSystem = false;
                if (appInfo != null) {
                    int f = appInfo.flags;
                    isSystem = (f & ApplicationInfo.FLAG_SYSTEM) != 0
                        && (f & ApplicationInfo.FLAG_UPDATED_SYSTEM_APP) == 0;
                }
                boolean resizeable = isActivityResizeable(act);

                String iconB64 = "";
                if (includeIcons) {
                    try {
                        Drawable d = loadAppDrawable(sysCtx, pm, ri, act, appInfo);
                        iconB64 = renderIconBase64(d, APP_ICON_SIZE_PX);
                    } catch (Throwable t) {
                        System.err.println("icon error for " + pkg + ": " + t);
                    }
                }

                StringBuilder row = new StringBuilder(256);
                row.append("AFDEX1|APP|{")
                   .append("\"package\":").append(jsonStr(pkg)).append(',')
                   .append("\"activity\":").append(jsonStr(nonNull(act.name))).append(',')
                   .append("\"label\":").append(jsonStr(label)).append(',')
                   .append("\"system\":").append(isSystem).append(',')
                   .append("\"resizeable\":").append(resizeable).append(',')
                   .append("\"icon_b64\":").append(jsonStr(iconB64))
                   .append('}');
                System.out.println(row);
                count++;
            }
        }
        System.out.println("AFDEX1|APPS_DONE|" + count);
        System.out.flush();
    }

    private static Drawable loadAppDrawable(Context sysCtx, PackageManager pm, ResolveInfo ri, ActivityInfo act, ApplicationInfo appInfo) {
        try {
            Drawable d = ri.loadIcon(pm);
            if (d != null) return d;
        } catch (Throwable ignored) {}
        int resId = ri.getIconResource();
        if (resId == 0 && act != null) resId = act.icon;
        if (resId == 0 && appInfo != null) resId = appInfo.icon;
        if (resId != 0 && appInfo != null) {
            try {
                Resources res = pm.getResourcesForApplication(appInfo);
                Drawable d = res.getDrawableForDensity(resId, DisplayMetrics.DENSITY_XXHIGH, null);
                if (d != null) return d;
            } catch (Throwable ignored) {}
            try {
                Context pkgCtx = sysCtx.createPackageContext(act.packageName, Context.CONTEXT_IGNORE_SECURITY);
                Drawable d = pkgCtx.getDrawable(resId);
                if (d != null) return d;
            } catch (Throwable ignored) {}
        }
        return null;
    }

    private static Context getSystemContextWorkaround() throws Exception {
        Class<?> atCls = Class.forName("android.app.ActivityThread");
        Object at = null;
        try {
            at = atCls.getMethod("systemMain").invoke(null);
        } catch (Throwable ignored) {}
        if (at == null) {
            bootstrapWorkarounds();
            try {
                at = atCls.getDeclaredMethod("currentActivityThread").invoke(null);
            } catch (Throwable ignored) {}
        }
        if (at == null) {
            Constructor<?> ctor = atCls.getDeclaredConstructor();
            ctor.setAccessible(true);
            at = ctor.newInstance();
        }
        try {
            Object ctx = atCls.getMethod("getSystemContext").invoke(at);
            if (ctx instanceof Context) {
                return (Context) ctx;
            }
        } catch (Throwable ignored) {}
        Class<?> ciCls = Class.forName("android.app.ContextImpl");
        Method createSys = ciCls.getDeclaredMethod("createSystemContext", atCls);
        createSys.setAccessible(true);
        return (Context) createSys.invoke(null, at);
    }

    private static boolean isActivityResizeable(ActivityInfo act) {
        try {
            Field f = ActivityInfo.class.getDeclaredField("resizeMode");
            f.setAccessible(true);
            return f.getInt(act) != 0;
        } catch (Throwable ignored) {
            return true;
        }
    }

    private static boolean isNoisyPackage(String pkg) {
        if (pkg == null || pkg.isEmpty()) return true;
        if ("com.android.shell".equals(pkg)
            || pkg.startsWith("org.chromium.webapk.")
            || pkg.startsWith("com.google.android.apps.chrome.webapps.")
            || pkg.endsWith(".test")) {
            return true;
        }
        for (String seg : pkg.split("\\.")) {
            if (HEX_SEGMENT_PATTERN.matcher(seg).matches()) {
                return true;
            }
        }
        return false;
    }

    private static String renderIconBase64(Drawable d, int sizePx) {
        if (d == null) return "";
        Bitmap bmp = Bitmap.createBitmap(sizePx, sizePx, Bitmap.Config.ARGB_8888);
        try {
            Canvas canvas = new Canvas(bmp);
            d.setBounds(0, 0, sizePx, sizePx);
            d.draw(canvas);
            ByteArrayOutputStream baos = new ByteArrayOutputStream(4096);
            if (!bmp.compress(Bitmap.CompressFormat.PNG, 90, baos)) {
                return "";
            }
            return Base64.encodeToString(baos.toByteArray(), Base64.NO_WRAP);
        } finally {
            bmp.recycle();
        }
    }

    private static void runRecover() throws Exception {
        DisplayService svc = new DisplayService();
        svc.disconnectWifiDisplayQuiet();
        waitForDisplayGone(DISPLAY_NAME, 5000);
        waitForPortFree(RTSP_PORT, 3000);
        new File(LOCK_PATH).delete();
        List<DisplaySnapshot> remaining = queryDisplays();
        boolean found = false;
        for (DisplaySnapshot d : remaining) {
            if (DISPLAY_NAME.equals(d.name)) {
                found = true;
                break;
            }
        }
        System.out.println("AFDEX1|RECOVER|" + (found ? "RESIDUAL" : "OK"));
        System.out.flush();
    }

    private static void runServe(String token, int width, int height, int dpi, int graceSec) throws Exception {
        if (width < 320 || height < 320 || width > 7680 || height > 4320 || dpi < 72 || dpi > 640) {
            throw new IllegalArgumentException("unsupported DeX metrics: " + width + "x" + height + "@" + dpi);
        }
        if (graceSec < 0 || graceSec > 120) {
            throw new IllegalArgumentException("invalid grace_sec: " + graceSec);
        }

        RandomAccessFile lockFile = new RandomAccessFile(LOCK_PATH, "rw");
        FileLock lock = lockFile.getChannel().tryLock();
        if (lock == null) {
            lockFile.close();
            throw new IllegalStateException("another AndroFuse DeX helper holds " + LOCK_PATH);
        }

        DisplayService svc = new DisplayService();
        LoopbackRtsp rtsp = null;
        AtomicBoolean cleaned = new AtomicBoolean(false);
        int activeDisplayId = -1;

        try {
            svc.disconnectWifiDisplayQuiet();
            waitForDisplayGone(DISPLAY_NAME, 5000);
            waitForPortFree(RTSP_PORT, 5000);

            rtsp = new LoopbackRtsp(svc);
            rtsp.start();

            DisplaySnapshot created = waitForDisplayAppear(DISPLAY_NAME, 12000);
            if (created == null || created.id <= 0) {
                throw new IllegalStateException("AndroFuseDeX virtual/wireless display did not appear");
            }
            activeDisplayId = created.id;

            DisplaySnapshot settled = applyMetrics(activeDisplayId, width, height, dpi);
            emit(token, "READY",
                settled.id + "|" + settled.width + "|" + settled.height + "|" + settled.dpi + "|" +
                settled.type + "|" + settled.flags + "|" + android.os.Process.myPid());

            final LoopbackRtsp finalRtsp = rtsp;
            final int cleanupDisplayId = activeDisplayId;
            Runtime.getRuntime().addShutdownHook(new Thread(() -> {
                if (cleaned.compareAndSet(false, true)) {
                    cleanupLoopback(svc, finalRtsp, cleanupDisplayId);
                }
            }, "AndroFuse-DeX-Shutdown"));

            BufferedReader stdin = new BufferedReader(new InputStreamReader(System.in, StandardCharsets.US_ASCII));
            String cmdLine;
            while ((cmdLine = stdin.readLine()) != null) {
                cmdLine = cmdLine.trim();
                if (cmdLine.isEmpty()) continue;
                if ("STOP".equals(cmdLine)) {
                    break;
                } else if ("PING".equals(cmdLine)) {
                    DisplaySnapshot cur = findDisplayById(activeDisplayId);
                    if (cur == null) {
                        emit(token, "LOST", String.valueOf(activeDisplayId));
                        break;
                    }
                    emit(token, "PONG", cur.id + "|" + cur.width + "|" + cur.height + "|" + cur.dpi);
                } else if (cmdLine.startsWith("RESIZE ")) {
                    String[] parts = cmdLine.split("\\s+");
                    if (parts.length == 4) {
                        int rw = Integer.parseInt(parts[1]);
                        int rh = Integer.parseInt(parts[2]);
                        int rd = Integer.parseInt(parts[3]);
                        DisplaySnapshot cur = applyMetrics(activeDisplayId, rw, rh, rd);
                        emit(token, "RESIZED", cur.id + "|" + cur.width + "|" + cur.height + "|" + cur.dpi);
                    }
                }
            }
            if (cmdLine == null && graceSec > 0) {
                emit(token, "GRACE", String.valueOf(graceSec));
                Thread.sleep(graceSec * 1000L);
            }
        } finally {
            if (cleaned.compareAndSet(false, true)) {
                cleanupLoopback(svc, rtsp, activeDisplayId);
            }
            try { lock.release(); } catch (Throwable ignored) {}
            try { lockFile.close(); } catch (Throwable ignored) {}
            new File(LOCK_PATH).delete();
        }
        emit(token, "STOPPED", "1");
    }

    private static void cleanupLoopback(DisplayService svc, LoopbackRtsp rtsp, int displayId) {
        if (displayId > 0) {
            runWmQuiet("size", "reset", "-d", String.valueOf(displayId));
            runWmQuiet("density", "reset", "-d", String.valueOf(displayId));
        }
        if (rtsp != null) {
            rtsp.stop();
        }
        svc.disconnectWifiDisplayQuiet();
        try {
            waitForDisplayGone(DISPLAY_NAME, 5000);
        } catch (InterruptedException ignored) {
            Thread.currentThread().interrupt();
        }
    }

    private static DisplaySnapshot applyMetrics(int displayId, int width, int height, int dpi) throws Exception {
        DisplaySnapshot current = findDisplayById(displayId);
        if (current != null && current.width == width && current.height == height && current.dpi == dpi) {
            return current;
        }
        // Initial DPI >= 160 avoids Samsung SystemUI crashing or mis-scaling before resize settles.
        int initialDpi = Math.max(dpi, 160);
        try {
            if (current == null || current.width != width || current.height != height) {
                runWm("size", width + "x" + height, "-d", String.valueOf(displayId));
            }
            if (current == null || current.dpi != initialDpi) {
                runWm("density", String.valueOf(initialDpi), "-d", String.valueOf(displayId));
            }
            DisplaySnapshot settled = waitForSettledMetrics(displayId, width, height, initialDpi, 5000);
            if (dpi != initialDpi) {
                runWm("density", String.valueOf(dpi), "-d", String.valueOf(displayId));
                settled = waitForSettledMetrics(displayId, width, height, dpi, 4000);
            }
            return settled;
        } catch (Throwable t) {
            DisplaySnapshot live = findDisplayById(displayId);
            if (live != null && live.width > 0 && live.height > 0) {
                System.err.println("warning: wm override skipped on display " + displayId + ": " + t.getMessage());
                return live;
            }
            throw t;
        }
    }

    private static void runWmQuiet(String... args) {
        try {
            runWm(args);
        } catch (Throwable ignored) {}
    }

    private static void runWm(String... args) throws Exception {
        List<String> cmd = new ArrayList<>();
        cmd.add("/system/bin/wm");
        for (String a : args) {
            cmd.add(a);
        }
        Process p = new ProcessBuilder(cmd).redirectErrorStream(true).start();
        StringBuilder out = new StringBuilder();
        try (BufferedReader br = new BufferedReader(new InputStreamReader(p.getInputStream(), StandardCharsets.UTF_8))) {
            String line;
            while ((line = br.readLine()) != null) {
                out.append(line).append('\n');
            }
        }
        if (!p.waitFor(5, TimeUnit.SECONDS)) {
            p.destroyForcibly();
            throw new IllegalStateException("wm timed out: " + cmd);
        }
        if (p.exitValue() != 0) {
            throw new IllegalStateException("wm failed (" + p.exitValue() + "): " + out.toString().trim());
        }
    }

    private static DisplaySnapshot waitForSettledMetrics(int displayId, int width, int height, int dpi, long timeoutMs)
            throws InterruptedException {
        long deadline = System.currentTimeMillis() + timeoutMs;
        DisplaySnapshot last = null;
        while (System.currentTimeMillis() < deadline) {
            last = findDisplayById(displayId);
            if (last != null && last.width == width && last.height == height && last.dpi == dpi) {
                return last;
            }
            Thread.sleep(100);
        }
        return last != null ? last : new DisplaySnapshot(displayId, DISPLAY_NAME, width, height, dpi, 0, 0);
    }

    private static DisplaySnapshot waitForDisplayAppear(String name, long timeoutMs) throws InterruptedException {
        long deadline = System.currentTimeMillis() + timeoutMs;
        while (System.currentTimeMillis() < deadline) {
            for (DisplaySnapshot d : queryDisplays()) {
                if (name.equals(d.name)) {
                    return d;
                }
            }
            Thread.sleep(150);
        }
        return null;
    }

    private static void waitForDisplayGone(String name, long timeoutMs) throws InterruptedException {
        long deadline = System.currentTimeMillis() + timeoutMs;
        while (System.currentTimeMillis() < deadline) {
            boolean found = false;
            for (DisplaySnapshot d : queryDisplays()) {
                if (name.equals(d.name)) {
                    found = true;
                    break;
                }
            }
            if (!found) return;
            Thread.sleep(100);
        }
    }

    private static DisplaySnapshot findDisplayById(int displayId) {
        for (DisplaySnapshot d : queryDisplays()) {
            if (d.id == displayId) return d;
        }
        return null;
    }

    private static void waitForPortFree(int port, long timeoutMs) throws InterruptedException {
        long deadline = System.currentTimeMillis() + timeoutMs;
        while (System.currentTimeMillis() < deadline) {
            try (ServerSocket ss = new ServerSocket(port, 1, InetAddress.getByName("127.0.0.1"))) {
                return;
            } catch (IOException ignored) {
                Thread.sleep(100);
            }
        }
    }

    private static List<DisplaySnapshot> queryDisplays() {
        List<DisplaySnapshot> out = new ArrayList<>();
        try {
            Class<?> dmgCls = Class.forName("android.hardware.display.DisplayManagerGlobal");
            Object dmg = dmgCls.getDeclaredMethod("getInstance").invoke(null);
            int[] ids = (int[]) dmgCls.getMethod("getDisplayIds").invoke(dmg);
            if (ids == null) return out;
            Method getInfo = dmgCls.getMethod("getDisplayInfo", int.class);
            for (int id : ids) {
                Object info = getInfo.invoke(dmg, id);
                if (info == null) continue;
                Class<?> c = info.getClass();
                String name = getStringField(c, info, "name", "");
                int w = getIntField(c, info, "logicalWidth", 0);
                int h = getIntField(c, info, "logicalHeight", 0);
                int dpi = getIntField(c, info, "logicalDensityDpi", 0);
                int type = getIntField(c, info, "type", 0);
                int flags = getIntField(c, info, "flags", 0);
                out.add(new DisplaySnapshot(id, name, w, h, dpi, type, flags));
            }
        } catch (Throwable ignored) {}
        return out;
    }

    private static String getStringField(Class<?> c, Object obj, String name, String def) {
        try {
            Field f = c.getField(name);
            Object v = f.get(obj);
            return v != null ? String.valueOf(v) : def;
        } catch (Throwable t) {
            try {
                Field f = c.getDeclaredField(name);
                f.setAccessible(true);
                Object v = f.get(obj);
                return v != null ? String.valueOf(v) : def;
            } catch (Throwable ignored) {
                return def;
            }
        }
    }

    private static int getIntField(Class<?> c, Object obj, String name, int def) {
        try {
            return c.getField(name).getInt(obj);
        } catch (Throwable t) {
            try {
                Field f = c.getDeclaredField(name);
                f.setAccessible(true);
                return f.getInt(obj);
            } catch (Throwable ignored) {
                return def;
            }
        }
    }

    private static String getSystemProperty(String key, String def) {
        try {
            Class<?> sp = Class.forName("android.os.SystemProperties");
            return (String) sp.getMethod("get", String.class, String.class).invoke(null, key, def);
        } catch (Throwable t) {
            return def;
        }
    }

    private static void bootstrapWorkarounds() {
        try {
            Class<?> atCls = Class.forName("android.app.ActivityThread");
            Constructor<?> ctor = atCls.getDeclaredConstructor();
            ctor.setAccessible(true);
            Object at = ctor.newInstance();
            Field sCur = atCls.getDeclaredField("sCurrentActivityThread");
            sCur.setAccessible(true);
            sCur.set(null, at);
            Field sys = atCls.getDeclaredField("mSystemThread");
            sys.setAccessible(true);
            sys.setBoolean(at, true);

            if (Build.VERSION.SDK_INT >= 31) {
                try {
                    Class<?> ccCls = Class.forName("android.app.ConfigurationController");
                    Class<?> internalCls = Class.forName("android.app.ActivityThreadInternal");
                    Constructor<?> ccCtor = ccCls.getDeclaredConstructor(internalCls);
                    ccCtor.setAccessible(true);
                    Object cc = ccCtor.newInstance(at);
                    Field f = atCls.getDeclaredField("mConfigurationController");
                    f.setAccessible(true);
                    f.set(at, cc);
                } catch (Throwable ignored) {}
            }

            Context fakeCtx = new ContextWrapper(null) {
                @Override public String getPackageName() { return "com.android.shell"; }
                @Override public String getOpPackageName() { return "com.android.shell"; }
            };
            try {
                Class<?> abdCls = Class.forName("android.app.ActivityThread$AppBindData");
                Constructor<?> abdCtor = abdCls.getDeclaredConstructor();
                abdCtor.setAccessible(true);
                Object abd = abdCtor.newInstance();
                ApplicationInfo ai = new ApplicationInfo();
                ai.packageName = "com.android.shell";
                Field aif = abdCls.getDeclaredField("appInfo");
                aif.setAccessible(true);
                aif.set(abd, ai);
                Field mba = atCls.getDeclaredField("mBoundApplication");
                mba.setAccessible(true);
                mba.set(at, abd);
            } catch (Throwable ignored) {}
            try {
                Application app = Instrumentation.newApplication(Application.class, fakeCtx);
                Field mia = atCls.getDeclaredField("mInitialApplication");
                mia.setAccessible(true);
                mia.set(at, app);
            } catch (Throwable ignored) {}
        } catch (Throwable ignored) {}
    }

    private static String nonNull(String s) { return s == null ? "" : s; }
    private static String sanitizeLine(String s) { return nonNull(s).replace('\r', ' ').replace('\n', ' '); }

    private static String jsonStr(String s) {
        StringBuilder out = new StringBuilder("\"");
        String v = nonNull(s);
        for (int i = 0; i < v.length(); i++) {
            char ch = v.charAt(i);
            switch (ch) {
                case '"': out.append("\\\""); break;
                case '\\': out.append("\\\\"); break;
                case '\b': out.append("\\b"); break;
                case '\f': out.append("\\f"); break;
                case '\n': out.append("\\n"); break;
                case '\r': out.append("\\r"); break;
                case '\t': out.append("\\t"); break;
                default:
                    if (ch < 0x20) out.append(String.format("\\u%04x", (int) ch));
                    else out.append(ch);
            }
        }
        return out.append('"').toString();
    }

    private static final class DisplaySnapshot {
        final int id;
        final String name;
        final int width;
        final int height;
        final int dpi;
        final int type;
        final int flags;

        DisplaySnapshot(int id, String name, int width, int height, int dpi, int type, int flags) {
            this.id = id;
            this.name = nonNull(name);
            this.width = width;
            this.height = height;
            this.dpi = dpi;
            this.type = type;
            this.flags = flags;
        }
    }

    private static final class DisplayService {
        private final Object manager;
        private final Method disconnectMethod;
        private final Method connectConfigMethod;

        DisplayService() throws Exception {
            Class<?> smCls = Class.forName("android.os.ServiceManager");
            IBinder binder = (IBinder) smCls.getDeclaredMethod("getService", String.class).invoke(null, "display");
            Class<?> stubCls = Class.forName("android.hardware.display.IDisplayManager$Stub");
            this.manager = stubCls.getMethod("asInterface", IBinder.class).invoke(null, binder);

            Method disc = null;
            Method conn = null;
            for (Method m : manager.getClass().getMethods()) {
                if ("disconnectWifiDisplay".equals(m.getName()) && m.getParameterCount() == 0) {
                    disc = m;
                } else if ("connectWifiDisplayWithConfig".equals(m.getName())) {
                    conn = m;
                }
            }
            this.disconnectMethod = disc;
            this.connectConfigMethod = conn;
        }

        boolean hasDisconnect() { return disconnectMethod != null; }
        boolean hasConnectWithConfig() { return connectConfigMethod != null; }

        void disconnectWifiDisplayQuiet() {
            if (disconnectMethod == null) return;
            try { disconnectMethod.invoke(manager); } catch (Throwable ignored) {}
        }

        void connectWifiDisplayWithConfig(Object config, Object callback) throws Exception {
            if (connectConfigMethod == null) {
                throw new NoSuchMethodException("IDisplayManager.connectWifiDisplayWithConfig not found");
            }
            connectConfigMethod.invoke(manager, config, callback);
        }
    }

    private static final class LoopbackRtsp {
        private final DisplayService svc;
        private final AtomicBoolean running = new AtomicBoolean(false);
        private final CountDownLatch playDone = new CountDownLatch(1);
        private int rtpVideoPort;
        private int rtpAudioPort;
        private Socket rtspSocket;
        private DatagramSocket videoDrain;
        private DatagramSocket audioDrain;

        LoopbackRtsp(DisplayService svc) {
            this.svc = svc;
        }

        void start() throws Exception {
            videoDrain = new DatagramSocket(0, InetAddress.getByName("127.0.0.1"));
            audioDrain = new DatagramSocket(0, InetAddress.getByName("127.0.0.1"));
            rtpVideoPort = videoDrain.getLocalPort();
            rtpAudioPort = audioDrain.getLocalPort();
            running.set(true);
            startDrainThread(videoDrain, "AndroFuse-RTP-Video");
            startDrainThread(audioDrain, "AndroFuse-RTP-Audio");

            Class<?> builderCls = Class.forName("android.hardware.display.SemWifiDisplayConfig$Builder");
            Object builder = builderCls.getDeclaredConstructor().newInstance();
            builderCls.getMethod("setApConnection", String.class, String.class, String.class, String.class)
                .invoke(builder, "127.0.0.1", String.valueOf(RTSP_PORT), DISPLAY_NAME, DUMMY_MAC);
            builderCls.getMethod("setMode", int.class).invoke(builder, MODE_WIRELESS_DEX);
            Object config = builderCls.getMethod("build").invoke(builder);

            Object callback = createCallbackProxy();
            svc.connectWifiDisplayWithConfig(config, callback);

            rtspSocket = connectRtsp(6000);
            if (rtspSocket == null) {
                throw new IllegalStateException("Android WFD source did not open loopback RTSP port " + RTSP_PORT);
            }
            Thread t = new Thread(this::loop, "AndroFuse-WFD-RTSP");
            t.setDaemon(true);
            t.start();

            if (!playDone.await(15, TimeUnit.SECONDS)) {
                throw new IllegalStateException("Timed out waiting for WFD RTSP PLAY OK");
            }
        }

        void stop() {
            running.set(false);
            if (rtspSocket != null) {
                try { rtspSocket.close(); } catch (IOException ignored) {}
            }
            if (videoDrain != null) videoDrain.close();
            if (audioDrain != null) audioDrain.close();
        }

        private void startDrainThread(DatagramSocket ds, String name) {
            Thread t = new Thread(() -> {
                byte[] buf = new byte[4096];
                DatagramPacket pkt = new DatagramPacket(buf, buf.length);
                while (running.get()) {
                    try {
                        ds.receive(pkt);
                    } catch (Throwable ignored) {
                        break;
                    }
                }
            }, name);
            t.setDaemon(true);
            t.start();
        }

        private Object createCallbackProxy() {
            try {
                Class<?> iface = Class.forName("android.hardware.display.IWifiDisplayConnectionCallback");
                return Proxy.newProxyInstance(
                    iface.getClassLoader(),
                    new Class<?>[]{iface},
                    (proxy, method, args) -> null
                );
            } catch (Throwable t) {
                return null;
            }
        }

        private Socket connectRtsp(long timeoutMs) {
            long deadline = System.currentTimeMillis() + timeoutMs;
            while (System.currentTimeMillis() < deadline) {
                try {
                    Socket s = new Socket("127.0.0.1", RTSP_PORT);
                    s.setTcpNoDelay(true);
                    s.setSoTimeout(35000);
                    return s;
                } catch (Throwable ignored) {
                    try { Thread.sleep(100); } catch (InterruptedException ie) {
                        Thread.currentThread().interrupt();
                        return null;
                    }
                }
            }
            return null;
        }

        private void loop() {
            try {
                BufferedReader in = new BufferedReader(
                    new InputStreamReader(rtspSocket.getInputStream(), StandardCharsets.US_ASCII));
                OutputStream out = rtspSocket.getOutputStream();
                String presentationUrl = "rtsp://127.0.0.1/wfd1.0/streamid=0";
                String sessionId = null;
                AtomicInteger clientCSeq = new AtomicInteger(1);

                while (running.get()) {
                    String start = in.readLine();
                    if (start == null) break;
                    if (start.trim().isEmpty()) continue;

                    String cseq = "1";
                    int contentLength = 0;
                    String sessionHeader = null;
                    String line;
                    while ((line = in.readLine()) != null && !line.isEmpty()) {
                        String lower = line.toLowerCase();
                        if (lower.startsWith("cseq:")) {
                            cseq = line.substring(5).trim();
                        } else if (lower.startsWith("content-length:")) {
                            contentLength = Integer.parseInt(line.substring(15).trim());
                        } else if (lower.startsWith("session:")) {
                            String raw = line.substring(8).trim();
                            int semi = raw.indexOf(';');
                            sessionHeader = semi >= 0 ? raw.substring(0, semi).trim() : raw;
                        }
                    }

                    String body = "";
                    if (contentLength > 0) {
                        char[] buf = new char[contentLength];
                        int off = 0;
                        while (off < contentLength) {
                            int n = in.read(buf, off, contentLength - off);
                            if (n < 0) break;
                            off += n;
                        }
                        body = new String(buf, 0, off);
                    }

                    if (start.startsWith("OPTIONS")) {
                        writeAscii(out,
                            "RTSP/1.0 200 OK\r\nCSeq: " + cseq +
                            "\r\nPublic: org.wfa.wfd1.0, GET_PARAMETER, SET_PARAMETER\r\n\r\n");
                        writeAscii(out,
                            "OPTIONS * RTSP/1.0\r\nCSeq: " + clientCSeq.getAndIncrement() +
                            "\r\nRequire: org.wfa.wfd1.0\r\n\r\n");
                    } else if (start.startsWith("GET_PARAMETER")) {
                        if (body.contains("wfd_video_formats")) {
                            String m3 =
                                "wfd_client_rtp_ports: RTP/AVP/UDP;unicast " + rtpVideoPort + " 0 mode=play\r\n" +
                                "wfd_audio_codecs: LPCM 00000003 00, AAC 0000000f 00\r\n" +
                                "wfd_video_formats: 40 00 01 10 0001bdeb 1fffffff 00003fff 10 0000 001f 11 0780 0438, " +
                                "02 10 0001bdeb 1fffffff 00000fff 10 0000 001f 11 0780 0438\r\n" +
                                "wfd_content_protection: none\r\n" +
                                "wfd_uibc_capability: input_category_list=HIDC;hidc_cap_list=Keyboard/USB, Mouse/USB;port=none\r\n";
                            writeAscii(out,
                                "RTSP/1.0 200 OK\r\nCSeq: " + cseq +
                                "\r\nContent-Type: text/parameters\r\nContent-Length: " +
                                m3.getBytes(StandardCharsets.US_ASCII).length + "\r\n\r\n" + m3);
                        } else {
                            writeAscii(out, "RTSP/1.0 200 OK\r\nCSeq: " + cseq + "\r\n\r\n");
                        }
                    } else if (start.startsWith("SET_PARAMETER")) {
                        for (String bLine : body.split("\r?\n")) {
                            if (bLine.startsWith("wfd_presentation_URL:")) {
                                String[] parts = bLine.substring(21).trim().split("\\s+");
                                if (parts.length > 0 && parts[0].startsWith("rtsp://")) {
                                    presentationUrl = parts[0];
                                }
                            }
                        }
                        writeAscii(out, "RTSP/1.0 200 OK\r\nCSeq: " + cseq + "\r\n\r\n");
                        if (body.contains("wfd_trigger_method: SETUP")) {
                            writeAscii(out,
                                "SETUP " + presentationUrl + " RTSP/1.0\r\nCSeq: " + clientCSeq.getAndIncrement() +
                                "\r\nTransport: RTP/AVP/UDP;unicast;client_port=" + rtpVideoPort + "-" + rtpAudioPort +
                                "\r\n\r\n");
                        } else if (body.contains("wfd_trigger_method: TEARDOWN")) {
                            break;
                        }
                    } else if (start.startsWith("RTSP/1.0 200 OK")) {
                        if (sessionHeader != null && sessionId == null) {
                            sessionId = sessionHeader;
                            writeAscii(out,
                                "PLAY " + presentationUrl + " RTSP/1.0\r\nCSeq: " + clientCSeq.getAndIncrement() +
                                "\r\nSession: " + sessionId + "\r\n\r\n");
                        } else if (sessionId != null) {
                            playDone.countDown();
                        }
                    }
                }
            } catch (Throwable ignored) {}
        }

        private static void writeAscii(OutputStream out, String s) throws IOException {
            out.write(s.getBytes(StandardCharsets.US_ASCII));
            out.flush();
        }
    }
}
