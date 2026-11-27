package com.androfuse.dexhelper;

import android.app.ActivityOptions;
import android.content.ComponentName;
import android.content.Context;
import android.content.Intent;
import android.content.pm.ActivityInfo;
import android.content.pm.ApplicationInfo;
import android.content.pm.PackageManager;
import android.hardware.display.DisplayManager;
import android.os.Binder;
import android.os.Build;
import android.os.Bundle;
import android.os.Handler;
import android.os.HandlerThread;
import android.os.IBinder;
import android.os.IInterface;
import android.os.Parcel;
import android.os.PowerManager;
import android.os.SystemClock;
import android.view.Display;
import android.view.InputEvent;
import android.view.KeyCharacterMap;
import android.view.KeyEvent;

import java.io.BufferedReader;
import java.io.InputStreamReader;
import java.lang.reflect.Field;
import java.lang.reflect.Method;
import java.lang.reflect.Proxy;
import java.nio.charset.StandardCharsets;
import java.util.List;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * Supervises one Independent App Window virtual display ({@code displayId > 0}) under UID 2000.
 *
 * <p>Purpose: Keeps the isolated virtual display's {@code DisplayGroup}/{@code PowerGroup} awake
 * when the phone's physical screen ({@code Display 0}) is off or sleeping, and provides two-layer
 * Bounded Back protection (pre-check via task stack + window back callback info, plus empty-display
 * task recovery) so root-level Back never leaves a black launcher-less virtual display.
 *
 * <p>Notes: Never targets {@code Display.DEFAULT_DISPLAY} ({@code 0}) or invalid displays ({@code -1})
 * with screen wake locks or {@code cmd power wakeup}.
 */
final class AppGuard {

    interface Listener {
        void onRootBackBlocked();
    }

    private static final int KEYCODE_BACK = 4;
    private static final int DISPLAY_STATE_ON = 2; // Display.STATE_ON
    private static final int POWER_MODE_OFF = 0; // SurfaceControl.POWER_MODE_OFF
    private static final int POWER_MODE_NORMAL = 2; // SurfaceControl.POWER_MODE_NORMAL
    private static final int POWER_ACQUIRE_CAUSES_WAKEUP = 0x10000000;
    private static final int POWER_SCREEN_BRIGHT_WAKE_LOCK = 0x0000000a;
    private static final Pattern DISPLAY_ID_PATTERN = Pattern.compile("\\b(?:mDisplayId|displayId)=(\\d+)\\b");
    private static final Pattern PRIORITY_PATTERN = Pattern.compile("\\b(?:mPriority|priority)=(-?\\d+)\\b");

    private final Context sysCtx;
    private final int displayId;
    private final String targetPackage;
    private final Listener listener;
    private final DisplayManager displayManager;
    private final HandlerThread thread;
    private final Handler handler;
    private final AtomicBoolean closed = new AtomicBoolean(false);

    private Object powerBinder;
    private Object atmBinder;
    private Object inputManager;
    private Method injectInputEventMethod;

    private PowerManager.WakeLock cpuWakeLock;
    private PowerManager.WakeLock vdWakeLock;
    private DisplayManager.DisplayListener displayListener;
    private Object taskStackListenerProxy;
    private Object taskStackListenerToken;

    private volatile boolean everHadTask;
    private volatile boolean sharedDisplayGroup;
    private volatile boolean physicalWasOffAtStart;
    private volatile boolean physicalPanelBlankedByUs;
    private long lastWakeAtMs;
    private int wakeBurstCount;
    private long wakeBurstWindowStartMs;
    private long lastRelaunchWindowStartMs;
    private int relaunchBurstCount;

    private final Runnable checkTasksRunnable = this::checkDisplayTasks;
    private final Runnable wakeDisplayRunnable = this::wakeVirtualDisplayOnly;
    private final Runnable wakeVerifyRunnable = this::verifyAndFallbackWakeup;

    private AppGuard(Context sysCtx, int displayId, String targetPackage, Listener listener) {
        this.sysCtx = sysCtx;
        this.displayId = displayId;
        this.targetPackage = targetPackage;
        this.listener = listener;
        this.displayManager = (DisplayManager) sysCtx.getSystemService(Context.DISPLAY_SERVICE);
        this.thread = new HandlerThread("AndroFuse-AppGuard-" + displayId);
        this.thread.start();
        this.handler = new Handler(thread.getLooper());
    }

    /**
     * Starts an AppGuard supervisor for an active virtual display running {@code targetPackage}.
     *
     * <p>Inputs: system context, virtual {@code displayId > 0}, non-empty package name (optional leading '+'),
     * and optional root-Back listener.
     * <p>Side effects: acquires session-scoped CPU partial + display-scoped screen wake locks for
     * {@code displayId}, registers DisplayListener + ITaskStackListener, and schedules initial task probes.
     */
    static AppGuard start(Context sysCtx, int displayId, String targetPackage, Listener listener) {
        if (sysCtx == null) {
            throw new IllegalArgumentException("system context is required");
        }
        if (displayId <= 0) {
            throw new IllegalArgumentException("AppGuard requires virtual displayId > 0, got " + displayId);
        }
        String pkg = targetPackage == null ? "" : targetPackage.trim();
        if (pkg.startsWith("+")) {
            pkg = pkg.substring(1).trim();
        }
        if (pkg.isEmpty()) {
            throw new IllegalArgumentException("targetPackage is required");
        }
        AppGuard guard = new AppGuard(sysCtx, displayId, pkg, listener);
        guard.init();
        return guard;
    }

    @SuppressWarnings("deprecation")
    private void init() {
        try {
            Class<?> smCls = Class.forName("android.os.ServiceManager");
            Method getSvc = smCls.getDeclaredMethod("getService", String.class);

            IBinder pb = (IBinder) getSvc.invoke(null, "power");
            if (pb != null) {
                powerBinder = Class.forName("android.os.IPowerManager$Stub")
                    .getMethod("asInterface", IBinder.class)
                    .invoke(null, pb);
            }

            IBinder ab = (IBinder) getSvc.invoke(null, "activity_task");
            if (ab != null) {
                atmBinder = Class.forName("android.app.IActivityTaskManager$Stub")
                    .getMethod("asInterface", IBinder.class)
                    .invoke(null, ab);
            }
        } catch (Throwable ignored) {}

        try {
            inputManager = sysCtx.getSystemService(Context.INPUT_SERVICE);
            if (inputManager == null) {
                Class<?> imCls = Class.forName("android.hardware.input.InputManager");
                inputManager = imCls.getMethod("getInstance").invoke(null);
            }
            if (inputManager != null) {
                injectInputEventMethod = inputManager.getClass().getMethod("injectInputEvent", InputEvent.class, int.class);
            }
        } catch (Throwable ignored) {}

        PowerManager pm = (PowerManager) sysCtx.getSystemService(Context.POWER_SERVICE);
        if (pm != null) {
            try {
                cpuWakeLock = pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "androfuse:app-cpu-" + displayId);
                cpuWakeLock.setReferenceCounted(false);
                cpuWakeLock.acquire();
            } catch (Throwable ignored) {}

            vdWakeLock = createDisplayWakeLock(pm, POWER_SCREEN_BRIGHT_WAKE_LOCK | POWER_ACQUIRE_CAUSES_WAKEUP,
                "androfuse:app-vd-" + displayId, displayId);
            if (vdWakeLock != null) {
                try {
                    vdWakeLock.setReferenceCounted(false);
                    vdWakeLock.acquire();
                } catch (Throwable ignored) {}
            }
        }

        touchUserActivity(displayId);

        if (displayManager != null) {
            try {
                Display d0 = displayManager.getDisplay(Display.DEFAULT_DISPLAY);
                physicalWasOffAtStart = (d0 != null && d0.getState() != DISPLAY_STATE_ON);
                Display initial = displayManager.getDisplay(displayId);
                sharedDisplayGroup = (initial != null && queryDisplayGroupId(initial) == 0);
            } catch (Throwable ignored) {}

            displayListener = new DisplayManager.DisplayListener() {
                @Override
                public void onDisplayAdded(int id) {}

                @Override
                public void onDisplayRemoved(int id) {
                    if (id == displayId) {
                        close();
                    }
                }

                @Override
                public void onDisplayChanged(int id) {
                    if (id == displayId && !closed.get()) {
                        Display d = displayManager.getDisplay(displayId);
                        if (d == null) {
                            close();
                            return;
                        }
                        if (d.getState() != DISPLAY_STATE_ON) {
                            scheduleWakeVirtualDisplay(180L);
                        }
                    }
                }
            };
            try {
                displayManager.registerDisplayListener(displayListener, handler);
            } catch (Throwable ignored) {}

            try {
                Display initial = displayManager.getDisplay(displayId);
                if (sharedDisplayGroup) {
                    // Path B (OEM ROMs placing new_display in DisplayGroup 0):
                    // Wake PowerGroup 0 so the virtual display renders, then blank physical Display 0's panel.
                    if (physicalWasOffAtStart || (initial != null && initial.getState() != DISPLAY_STATE_ON)) {
                        runCmdPowerWakeupDisplay(displayId);
                        touchUserActivity(displayId);
                        handler.post(wakeDisplayRunnable);
                    }
                    if (physicalWasOffAtStart && setPhysicalDisplay0PowerMode(false)) {
                        physicalPanelBlankedByUs = true;
                    }
                } else if (initial != null && initial.getState() != DISPLAY_STATE_ON) {
                    // Path A (Isolated DisplayGroup != 0): wake only the virtual display's PowerGroup.
                    runCmdPowerWakeupDisplay(displayId);
                    touchUserActivity(displayId);
                    handler.post(wakeDisplayRunnable);
                }
            } catch (Throwable ignored) {}
        }

        registerTaskStackListener();
        handler.postDelayed(checkTasksRunnable, 600L);
        handler.postDelayed(checkTasksRunnable, 1500L);
    }

    /**
     * Evaluates Layer 1 Bounded Back for {@code displayId} before forwarding {@code KEYCODE_BACK}.
     *
     * <p>Returns: {@code true} if an inner activity, dialog, IME, or registered inner-app back
     * callback is active; {@code false} when the app is at its root screen or empty.
     * <p>Notes: Legacy apps with predictive back disabled default to {@code true} and rely on Layer 2
     * ({@link #checkDisplayTasks()}) to restore the root activity cleanly if they finish or move to back.
     */
    boolean shouldAllowBack() {
        if (closed.get()) {
            return false;
        }
        TaskState ts = queryDisplayTaskState();
        if (ts.totalActivities > 0) {
            everHadTask = true;
        }
        if (ts.totalActivities == 0) {
            if (everHadTask) {
                scheduleCheckDisplayTasks(20L);
            }
            notifyRootBackBlocked();
            return false;
        }
        if (ts.totalActivities > 1 || ts.taskCount > 1) {
            return true;
        }

        WindowInspect wi = inspectDisplayWindows(displayId);
        if (wi.imeVisible || wi.visibleAppWindows >= 2) {
            return true;
        }
        if (wi.hasBasePriority) {
            if (wi.basePriority >= 0) {
                return true;
            }
            if (ts.topActivity != null && isPredictiveBackEnabled(sysCtx, ts.topActivity)) {
                notifyRootBackBlocked();
                return false;
            }
        } else if (ts.topActivity != null && isPredictiveBackEnabled(sysCtx, ts.topActivity)) {
            notifyRootBackBlocked();
            return false;
        }
        return true;
    }

    /**
     * Checks {@link #shouldAllowBack()} and, if allowed, injects {@code KEYCODE_BACK} targeting {@code displayId}.
     *
     * <p>Returns: {@code true} if Back was delivered to the app, {@code false} if blocked at the main screen.
     */
    boolean performBoundedBack() {
        if (!shouldAllowBack()) {
            return false;
        }
        long now = SystemClock.uptimeMillis();
        boolean downOk = injectKey(KeyEvent.ACTION_DOWN, KEYCODE_BACK, now, now);
        boolean upOk = injectKey(KeyEvent.ACTION_UP, KEYCODE_BACK, now, SystemClock.uptimeMillis());
        scheduleCheckDisplayTasks(180L);
        return downOk && upOk;
    }

    boolean isSharedDisplayGroup() {
        return sharedDisplayGroup;
    }

    /**
     * Releases all wake locks, unregisters system listeners, restores physical panel power if needed,
     * and stops the supervisor thread idempotently.
     */
    void close() {
        if (!closed.compareAndSet(false, true)) {
            return;
        }
        handler.removeCallbacksAndMessages(null);
        if (displayManager != null && displayListener != null) {
            try {
                displayManager.unregisterDisplayListener(displayListener);
            } catch (Throwable ignored) {}
        }
        unregisterTaskStackListener();
        if (vdWakeLock != null) {
            try {
                if (vdWakeLock.isHeld()) {
                    vdWakeLock.release();
                }
            } catch (Throwable ignored) {}
        }
        if (cpuWakeLock != null) {
            try {
                if (cpuWakeLock.isHeld()) {
                    cpuWakeLock.release();
                }
            } catch (Throwable ignored) {}
        }
        if (physicalPanelBlankedByUs) {
            physicalPanelBlankedByUs = false;
            setPhysicalDisplay0PowerMode(true);
            if (physicalWasOffAtStart && powerBinder != null) {
                try {
                    Method goToSleep = powerBinder.getClass().getMethod("goToSleep", long.class, int.class, int.class);
                    goToSleep.invoke(powerBinder, SystemClock.uptimeMillis(), 0, 0);
                } catch (Throwable ignored) {}
            }
        }
        try {
            thread.quitSafely();
        } catch (Throwable ignored) {}
    }

    private void notifyRootBackBlocked() {
        if (listener != null && !closed.get()) {
            listener.onRootBackBlocked();
        }
    }

    private boolean injectKey(int action, int keycode, long downTime, long eventTime) {
        if (inputManager == null || injectInputEventMethod == null) {
            return false;
        }
        try {
            KeyEvent ev = new KeyEvent(downTime, eventTime, action, keycode, 0, 0,
                KeyCharacterMap.VIRTUAL_KEYBOARD, 0, 0, 0x101);
            KeyEvent.class.getMethod("setDisplayId", int.class).invoke(ev, displayId);
            Object res = injectInputEventMethod.invoke(inputManager, ev, 0);
            return !(res instanceof Boolean) || (Boolean) res;
        } catch (Throwable t) {
            return false;
        }
    }

    @SuppressWarnings("deprecation")
    private static PowerManager.WakeLock createDisplayWakeLock(PowerManager pm, int flags, String tag, int displayId) {
        if (displayId <= 0) {
            return null;
        }
        try {
            Method m = PowerManager.class.getMethod("newWakeLock", int.class, String.class, int.class);
            Object wl = m.invoke(pm, flags, tag, displayId);
            if (wl instanceof PowerManager.WakeLock) {
                return (PowerManager.WakeLock) wl;
            }
        } catch (Throwable ignored) {}

        // Fallback: construct WakeLock and stamp private mDisplayId via reflection before acquire().
        try {
            PowerManager.WakeLock wl = pm.newWakeLock(flags, tag);
            Field f = PowerManager.WakeLock.class.getDeclaredField("mDisplayId");
            f.setAccessible(true);
            f.setInt(wl, displayId);
            return wl;
        } catch (Throwable ignored) {}
        return null;
    }

    private void touchUserActivity(int targetDisplayId) {
        if (targetDisplayId <= 0 || powerBinder == null) {
            return;
        }
        long now = SystemClock.uptimeMillis();
        try {
            Method m = powerBinder.getClass().getMethod("userActivity", int.class, long.class, int.class, int.class);
            m.invoke(powerBinder, targetDisplayId, now, 0, 0);
        } catch (Throwable ignored) {}
    }

    private void scheduleWakeVirtualDisplay(long delayMs) {
        if (closed.get() || displayId <= 0) {
            return;
        }
        handler.removeCallbacks(wakeDisplayRunnable);
        handler.postDelayed(wakeDisplayRunnable, delayMs);
    }

    private void wakeVirtualDisplayOnly() {
        if (closed.get() || displayId <= 0 || displayManager == null) {
            return;
        }
        Display d = displayManager.getDisplay(displayId);
        if (d == null) {
            close();
            return;
        }
        if (d.getState() == DISPLAY_STATE_ON) {
            return;
        }
        long now = SystemClock.uptimeMillis();
        if (now - lastWakeAtMs < 150L) {
            return;
        }
        if (now - wakeBurstWindowStartMs > 10_000L) {
            wakeBurstWindowStartMs = now;
            wakeBurstCount = 0;
        }
        if (wakeBurstCount >= 4) {
            return;
        }
        lastWakeAtMs = now;
        wakeBurstCount++;

        reacquireVirtualDisplayWakeLock();
        touchUserActivity(displayId);
        handler.removeCallbacks(wakeVerifyRunnable);
        handler.postDelayed(wakeVerifyRunnable, 160L);
    }

    private void verifyAndFallbackWakeup() {
        if (closed.get() || displayId <= 0 || displayManager == null) {
            return;
        }
        Display d = displayManager.getDisplay(displayId);
        if (d == null) {
            close();
            return;
        }
        if (d.getState() == DISPLAY_STATE_ON) {
            return;
        }
        runCmdPowerWakeupDisplay(displayId);
        reacquireVirtualDisplayWakeLock();
        touchUserActivity(displayId);
        if (sharedDisplayGroup && (physicalPanelBlankedByUs || physicalWasOffAtStart)) {
            if (setPhysicalDisplay0PowerMode(false)) {
                physicalPanelBlankedByUs = true;
            }
        }
    }

    private static int queryDisplayGroupId(Display d) {
        if (d == null) {
            return 0;
        }
        try {
            Class<?> infoCls = Class.forName("android.view.DisplayInfo");
            Object info = infoCls.getDeclaredConstructor().newInstance();
            Method getInfo = Display.class.getMethod("getDisplayInfo", infoCls);
            Object ok = getInfo.invoke(d, info);
            if (ok instanceof Boolean && (Boolean) ok) {
                Field f = infoCls.getField("displayGroupId");
                return f.getInt(info);
            }
        } catch (Throwable ignored) {}
        return 0;
    }

    private static boolean setPhysicalDisplay0PowerMode(boolean on) {
        try {
            IBinder token = null;
            Class<?> scCls = Class.forName("android.view.SurfaceControl");
            try {
                Method getIds = scCls.getDeclaredMethod("getPhysicalDisplayIds");
                Method getTok = scCls.getDeclaredMethod("getPhysicalDisplayToken", long.class);
                getIds.setAccessible(true);
                getTok.setAccessible(true);
                long[] ids = (long[]) getIds.invoke(null);
                if (ids != null && ids.length > 0) {
                    token = (IBinder) getTok.invoke(null, ids[0]);
                }
            } catch (Throwable ignored) {}

            if (token == null) {
                try {
                    System.load("/system/lib64/libandroid_servers.so");
                } catch (Throwable t1) {
                    try {
                        System.loadLibrary("android_servers");
                    } catch (Throwable ignored) {}
                }
                ClassLoader loader = new dalvik.system.PathClassLoader("/system/framework/services.jar", ClassLoader.getSystemClassLoader());
                Class<?> dcCls = Class.forName("com.android.server.display.DisplayControl", true, loader);
                Method getIds = dcCls.getDeclaredMethod("getPhysicalDisplayIds");
                Method getTok = dcCls.getDeclaredMethod("getPhysicalDisplayToken", long.class);
                getIds.setAccessible(true);
                getTok.setAccessible(true);
                long[] ids = (long[]) getIds.invoke(null);
                if (ids != null && ids.length > 0) {
                    token = (IBinder) getTok.invoke(null, ids[0]);
                }
            }

            if (token == null) {
                return false;
            }
            Method setMode = scCls.getDeclaredMethod("setDisplayPowerMode", IBinder.class, int.class);
            setMode.setAccessible(true);
            setMode.invoke(null, token, on ? POWER_MODE_NORMAL : POWER_MODE_OFF);
            return true;
        } catch (Throwable ignored) {
            return false;
        }
    }

    private void reacquireVirtualDisplayWakeLock() {
        if (vdWakeLock == null) {
            return;
        }
        try {
            if (vdWakeLock.isHeld()) {
                vdWakeLock.release();
            }
        } catch (Throwable ignored) {}
        try {
            vdWakeLock.acquire();
        } catch (Throwable ignored) {}
    }

    private static void runCmdPowerWakeupDisplay(int targetDisplayId) {
        if (targetDisplayId <= 0) {
            return;
        }
        try {
            java.lang.Process p = new ProcessBuilder(
                "/system/bin/cmd",
                "power",
                "wakeup",
                "--display-id",
                Integer.toString(targetDisplayId),
                "--restore-wakelocks"
            ).redirectErrorStream(true).start();
            if (!p.waitFor(3L, TimeUnit.SECONDS)) {
                p.destroyForcibly();
            }
        } catch (Throwable ignored) {}
    }

    private void scheduleCheckDisplayTasks(long delayMs) {
        if (closed.get()) {
            return;
        }
        handler.removeCallbacks(checkTasksRunnable);
        handler.postDelayed(checkTasksRunnable, delayMs);
    }

    private void registerTaskStackListener() {
        if (atmBinder == null) {
            return;
        }
        try {
            Class<?> listenerCls = Class.forName("android.app.ITaskStackListener");
            Binder binder = new Binder() {
                @Override
                protected boolean onTransact(int code, Parcel data, Parcel reply, int flags) {
                    if (code == INTERFACE_TRANSACTION) {
                        if (reply != null) {
                            reply.writeString("android.app.ITaskStackListener");
                        }
                        return true;
                    }
                    if (code >= FIRST_CALL_TRANSACTION && !closed.get()) {
                        scheduleCheckDisplayTasks(120L);
                        return true;
                    }
                    return false;
                }
            };
            taskStackListenerToken = binder;
            Object proxy = Proxy.newProxyInstance(
                listenerCls.getClassLoader(),
                new Class<?>[]{listenerCls, IInterface.class},
                (p, method, args) -> {
                    if ("asBinder".equals(method.getName())) {
                        return binder;
                    }
                    Class<?> rt = method.getReturnType();
                    if (rt == boolean.class) return false;
                    if (rt == int.class) return 0;
                    return null;
                }
            );
            Method reg = findMethod(atmBinder.getClass(), "registerTaskStackListener", listenerCls);
            if (reg != null) {
                reg.invoke(atmBinder, proxy);
                taskStackListenerProxy = proxy;
            }
        } catch (Throwable ignored) {}
    }

    private void unregisterTaskStackListener() {
        if (atmBinder != null && taskStackListenerProxy != null) {
            try {
                Class<?> listenerCls = Class.forName("android.app.ITaskStackListener");
                Method unreg = findMethod(atmBinder.getClass(), "unregisterTaskStackListener", listenerCls);
                if (unreg != null) {
                    unreg.invoke(atmBinder, taskStackListenerProxy);
                }
            } catch (Throwable ignored) {}
        }
        taskStackListenerProxy = null;
        taskStackListenerToken = null;
    }

    private void checkDisplayTasks() {
        if (closed.get() || displayManager == null) {
            return;
        }
        if (displayManager.getDisplay(displayId) == null) {
            close();
            return;
        }
        TaskState ts = queryDisplayTaskState();
        if (ts.totalActivities > 0) {
            everHadTask = true;
            return;
        }
        if (!everHadTask) {
            return;
        }
        if (restoreExistingTargetTask()) {
            notifyRootBackBlocked();
            return;
        }
        if (relaunchTargetAppOnDisplay()) {
            notifyRootBackBlocked();
        }
    }

    private boolean restoreExistingTargetTask() {
        if (atmBinder == null) {
            return false;
        }
        List<?> tasks = queryGlobalTasks(24);
        if (tasks == null) {
            return false;
        }
        for (Object task : tasks) {
            if (task == null) continue;
            ComponentName base = getComponentField(task, "baseActivity");
            ComponentName top = getComponentField(task, "topActivity");
            boolean match = (base != null && targetPackage.equals(base.getPackageName()))
                || (top != null && targetPackage.equals(top.getPackageName()));
            if (!match) {
                continue;
            }
            int taskId = getIntField(task, "taskId", -1);
            int numAct = getIntField(task, "numActivities", 0);
            if (taskId <= 0 || numAct <= 0) {
                continue;
            }
            Bundle opts = makeLaunchDisplayBundle(displayId);
            try {
                Method move = findMethod(atmBinder.getClass(), "moveRootTaskToDisplay", int.class, int.class);
                if (move != null) {
                    move.invoke(atmBinder, taskId, displayId);
                    if (queryDisplayTaskState().totalActivities > 0) {
                        return true;
                    }
                }
            } catch (Throwable ignored) {}
            try {
                Method startRecents = findMethod(atmBinder.getClass(), "startActivityFromRecents", int.class, Bundle.class);
                if (startRecents != null) {
                    startRecents.invoke(atmBinder, taskId, opts);
                    if (queryDisplayTaskState().totalActivities > 0) {
                        return true;
                    }
                }
            } catch (Throwable ignored) {}
        }
        return false;
    }

    private boolean relaunchTargetAppOnDisplay() {
        long now = SystemClock.uptimeMillis();
        if (now - lastRelaunchWindowStartMs > 10_000L) {
            lastRelaunchWindowStartMs = now;
            relaunchBurstCount = 0;
        }
        if (relaunchBurstCount >= 3) {
            return false;
        }
        relaunchBurstCount++;

        PackageManager pm = sysCtx.getPackageManager();
        if (pm == null) {
            return false;
        }
        Intent launch = pm.getLaunchIntentForPackage(targetPackage);
        if (launch != null) {
            launch.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK
                | Intent.FLAG_ACTIVITY_RESET_TASK_IF_NEEDED
                | Intent.FLAG_ACTIVITY_NO_ANIMATION);
            try {
                sysCtx.startActivity(launch, makeLaunchDisplayBundle(displayId));
                try {
                    Thread.sleep(120L);
                } catch (InterruptedException ignored) {}
                if (queryDisplayTaskState().totalActivities > 0) {
                    return true;
                }
            } catch (Throwable ignored) {}

            ComponentName comp = launch.getComponent();
            if (comp != null) {
                return runCmdActivityStart(comp.flattenToShortString());
            }
        }
        return false;
    }

    private boolean runCmdActivityStart(String component) {
        try {
            java.lang.Process p = new ProcessBuilder(
                "/system/bin/cmd",
                "activity",
                "start",
                "-W",
                "--display",
                Integer.toString(displayId),
                "-n",
                component
            ).redirectErrorStream(true).start();
            p.waitFor(3L, TimeUnit.SECONDS);
            return p.exitValue() == 0;
        } catch (Throwable t) {
            return false;
        }
    }

    private static Bundle makeLaunchDisplayBundle(int displayId) {
        try {
            ActivityOptions ao = ActivityOptions.makeBasic();
            ao.setLaunchDisplayId(displayId);
            return ao.toBundle();
        } catch (Throwable t) {
            return null;
        }
    }

    private static final class TaskState {
        int taskCount;
        int totalActivities;
        ComponentName topActivity;
    }

    private TaskState queryDisplayTaskState() {
        TaskState state = new TaskState();
        if (atmBinder == null) {
            return state;
        }
        try {
            Method m = findMethod(atmBinder.getClass(), "getAllRootTaskInfosOnDisplay", int.class);
            if (m != null) {
                Object raw = m.invoke(atmBinder, displayId);
                if (raw instanceof List<?>) {
                    for (Object info : (List<?>) raw) {
                        accumulateTaskInfo(state, info, false);
                    }
                    return state;
                }
            }
        } catch (Throwable ignored) {}

        List<?> tasks = queryGlobalTasks(24);
        if (tasks != null) {
            for (Object info : tasks) {
                if (info == null) continue;
                int disp = getIntField(info, "displayId", -1);
                if (disp == displayId) {
                    accumulateTaskInfo(state, info, true);
                }
            }
        }
        return state;
    }

    private static void accumulateTaskInfo(TaskState state, Object info, boolean runningTask) {
        if (info == null) {
            return;
        }
        int actType = getActivityType(info);
        if (actType == 2 || actType == 3) { // ACTIVITY_TYPE_HOME / ACTIVITY_TYPE_RECENTS
            return;
        }
        if (hasBooleanField(info, "visible") && !getBooleanField(info, "visible", true)) {
            return;
        }
        if (hasBooleanField(info, "isVisible") && !getBooleanField(info, "isVisible", true)) {
            return;
        }
        int num = getIntField(info, "numActivities", 0);
        if (num <= 0 && !runningTask) {
            return;
        }
        if (num > 0) {
            state.taskCount++;
            state.totalActivities += num;
            if (state.topActivity == null) {
                state.topActivity = getComponentField(info, "topActivity");
            }
        }
    }

    private List<?> queryGlobalTasks(int maxNum) {
        if (atmBinder == null) {
            return null;
        }
        for (Method m : atmBinder.getClass().getMethods()) {
            if (!"getTasks".equals(m.getName())) {
                continue;
            }
            Class<?>[] pt = m.getParameterTypes();
            try {
                if (pt.length == 1 && pt[0] == int.class) {
                    Object res = m.invoke(atmBinder, maxNum);
                    if (res instanceof List<?>) return (List<?>) res;
                } else if (pt.length == 3 && pt[0] == int.class && pt[1] == boolean.class && pt[2] == boolean.class) {
                    Object res = m.invoke(atmBinder, maxNum, false, false);
                    if (res instanceof List<?>) return (List<?>) res;
                } else if (pt.length == 4 && pt[0] == int.class && pt[1] == boolean.class && pt[2] == boolean.class && pt[3] == int.class) {
                    Object res = m.invoke(atmBinder, maxNum, false, false, displayId);
                    if (res instanceof List<?>) return (List<?>) res;
                }
            } catch (Throwable ignored) {}
        }
        return null;
    }

    private static final class WindowInspect {
        boolean imeVisible;
        int visibleAppWindows;
        boolean hasBasePriority;
        int basePriority;
    }

    private static WindowInspect inspectDisplayWindows(int targetDisplayId) {
        WindowInspect out = new WindowInspect();
        java.lang.Process p = null;
        try {
            p = new ProcessBuilder("/system/bin/dumpsys", "window", "windows")
                .redirectErrorStream(true)
                .start();
            try (BufferedReader br = new BufferedReader(new InputStreamReader(p.getInputStream(), StandardCharsets.UTF_8))) {
                boolean inBlock = false;
                int curDisplayId = -1;
                boolean curVisible = false;
                boolean curIme = false;
                boolean curBaseApp = false;
                boolean curIgnoreSys = false;
                boolean curHasPriority = false;
                int curPriority = 0;
                boolean expectBackCallbackPriority = false;

                String line;
                while ((line = br.readLine()) != null) {
                    String trimmed = line.trim();
                    if (trimmed.startsWith("Window #")) {
                        if (inBlock) {
                            recordWindow(out, targetDisplayId, curDisplayId, curVisible, curIme, curBaseApp, curIgnoreSys, curHasPriority, curPriority);
                        }
                        inBlock = true;
                        curDisplayId = -1;
                        curVisible = false;
                        curIme = trimmed.contains("InputMethod");
                        curBaseApp = false;
                        curIgnoreSys = trimmed.contains("Wallpaper");
                        curHasPriority = false;
                        curPriority = 0;
                        expectBackCallbackPriority = false;
                        continue;
                    }
                    if (!inBlock) {
                        continue;
                    }
                    if (trimmed.isEmpty()) {
                        expectBackCallbackPriority = false;
                        continue;
                    }
                    Matcher dm = DISPLAY_ID_PATTERN.matcher(trimmed);
                    if (dm.find()) {
                        try {
                            curDisplayId = Integer.parseInt(dm.group(1));
                        } catch (NumberFormatException ignored) {}
                    }
                    if (trimmed.contains("ty=INPUT_METHOD")) {
                        curIme = true;
                    } else if (trimmed.contains("ty=BASE_APPLICATION")) {
                        curBaseApp = true;
                    } else if (trimmed.contains("ty=WALLPAPER")
                        || trimmed.contains("ty=STATUS_BAR")
                        || trimmed.contains("ty=NAVIGATION_BAR")
                        || trimmed.contains("ty=NOTIFICATION_SHADE")) {
                        curIgnoreSys = true;
                    }
                    if (trimmed.contains("isVisible=true")
                        || trimmed.contains("isOnScreen=true")
                        || trimmed.contains("isReadyForDisplay()=true")) {
                        curVisible = true;
                    }
                    if (trimmed.contains("mOnBackInvokedCallbackInfo") || trimmed.contains("OnBackInvokedCallbackInfo")) {
                        expectBackCallbackPriority = true;
                    }
                    if (expectBackCallbackPriority) {
                        Matcher pm = PRIORITY_PATTERN.matcher(trimmed);
                        if (pm.find()) {
                            try {
                                curPriority = Integer.parseInt(pm.group(1));
                                curHasPriority = true;
                                expectBackCallbackPriority = false;
                            } catch (NumberFormatException ignored) {}
                        }
                    }
                }
                if (inBlock) {
                    recordWindow(out, targetDisplayId, curDisplayId, curVisible, curIme, curBaseApp, curIgnoreSys, curHasPriority, curPriority);
                }
            }
            if (!p.waitFor(1500L, TimeUnit.MILLISECONDS)) {
                p.destroyForcibly();
            }
        } catch (Throwable ignored) {
            if (p != null) {
                try { p.destroyForcibly(); } catch (Throwable ignored2) {}
            }
        }
        return out;
    }

    private static void recordWindow(
        WindowInspect out,
        int targetDisplayId,
        int dispId,
        boolean visible,
        boolean ime,
        boolean baseApp,
        boolean ignoreSys,
        boolean hasPriority,
        int priority
    ) {
        if (dispId != targetDisplayId || !visible) {
            return;
        }
        if (ime) {
            out.imeVisible = true;
            return;
        }
        if (ignoreSys) {
            return;
        }
        out.visibleAppWindows++;
        if (baseApp && hasPriority) {
            out.hasBasePriority = true;
            out.basePriority = priority;
        }
    }

    private static boolean isPredictiveBackEnabled(Context sysCtx, ComponentName comp) {
        PackageManager pm = sysCtx.getPackageManager();
        if (pm == null || comp == null) {
            return false;
        }
        try {
            ActivityInfo actInfo = pm.getActivityInfo(comp, PackageManager.GET_META_DATA);
            ApplicationInfo appInfo = actInfo.applicationInfo;
            try {
                Class<?> dCls = Class.forName("android.window.WindowOnBackInvokedDispatcher");
                for (Method m : dCls.getMethods()) {
                    if (!"isOnBackInvokedCallbackEnabled".equals(m.getName())) {
                        continue;
                    }
                    Class<?>[] pt = m.getParameterTypes();
                    if (pt.length == 3 && pt[0] == ActivityInfo.class && pt[1] == ApplicationInfo.class && Context.class.isAssignableFrom(pt[2])) {
                        Object res = m.invoke(null, actInfo, appInfo, sysCtx);
                        if (res instanceof Boolean) return (Boolean) res;
                    }
                }
            } catch (Throwable ignored) {}

            try {
                Method m = ActivityInfo.class.getMethod("isOnBackInvokedCallbackEnabled");
                Object res = m.invoke(actInfo);
                if (res instanceof Boolean && (Boolean) res) return true;
            } catch (Throwable ignored) {}
            try {
                if (appInfo != null) {
                    Method m = ApplicationInfo.class.getMethod("isOnBackInvokedCallbackEnabled");
                    Object res = m.invoke(appInfo);
                    if (res instanceof Boolean) return (Boolean) res;
                }
            } catch (Throwable ignored) {}

            // Android 16+ (SDK 36+) enables predictive back by default for targetSdk >= 36 unless opted out.
            if (Build.VERSION.SDK_INT >= 36 && appInfo != null && appInfo.targetSdkVersion >= 36) {
                return true;
            }
        } catch (Throwable ignored) {}
        return false;
    }

    private static Method findMethod(Class<?> cls, String name, Class<?>... params) {
        try {
            return cls.getMethod(name, params);
        } catch (Throwable t) {
            try {
                Method m = cls.getDeclaredMethod(name, params);
                m.setAccessible(true);
                return m;
            } catch (Throwable ignored) {
                return null;
            }
        }
    }

    private static int getActivityType(Object info) {
        try {
            Field cfgField = getField(info.getClass(), "configuration");
            if (cfgField != null) {
                Object cfg = cfgField.get(info);
                if (cfg != null) {
                    Field wcField = getField(cfg.getClass(), "windowConfiguration");
                    if (wcField != null) {
                        Object wc = wcField.get(cfg);
                        if (wc != null) {
                            Method m = wc.getClass().getMethod("getActivityType");
                            Object res = m.invoke(wc);
                            if (res instanceof Integer) return (Integer) res;
                        }
                    }
                }
            }
        } catch (Throwable ignored) {}
        return 0;
    }

    private static Field getField(Class<?> c, String name) {
        Class<?> cur = c;
        while (cur != null && cur != Object.class) {
            try {
                Field f = cur.getDeclaredField(name);
                f.setAccessible(true);
                return f;
            } catch (Throwable ignored) {}
            cur = cur.getSuperclass();
        }
        return null;
    }

    private static int getIntField(Object obj, String name, int def) {
        Field f = getField(obj.getClass(), name);
        if (f == null) return def;
        try {
            return f.getInt(obj);
        } catch (Throwable ignored) {
            return def;
        }
    }

    private static boolean hasBooleanField(Object obj, String name) {
        return getField(obj.getClass(), name) != null;
    }

    private static boolean getBooleanField(Object obj, String name, boolean def) {
        Field f = getField(obj.getClass(), name);
        if (f == null) return def;
        try {
            return f.getBoolean(obj);
        } catch (Throwable ignored) {
            return def;
        }
    }

    private static ComponentName getComponentField(Object obj, String name) {
        Field f = getField(obj.getClass(), name);
        if (f == null) return null;
        try {
            Object v = f.get(obj);
            return v instanceof ComponentName ? (ComponentName) v : null;
        } catch (Throwable ignored) {
            return null;
        }
    }
}
