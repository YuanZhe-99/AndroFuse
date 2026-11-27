package com.androfuse.dexhelper;

import android.content.AttributionSource;
import android.content.Context;
import android.content.pm.PackageManager;
import android.net.LocalSocket;
import android.net.LocalSocketAddress;
import android.os.Binder;
import android.os.Build;
import android.os.Bundle;
import android.os.IBinder;
import android.os.Parcel;
import android.os.ParcelFileDescriptor;
import android.os.Process;
import android.os.RemoteException;
import android.system.Os;
import android.system.StructStat;

import java.io.BufferedReader;
import java.io.File;
import java.io.FileInputStream;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.io.OutputStream;
import java.io.RandomAccessFile;
import java.lang.reflect.Field;
import java.lang.reflect.Method;
import java.nio.channels.FileLock;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.SecureRandom;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;

/**
 * Persistent zero-idle-wakeup UID 2000 daemon (`PLAN` §9.2) for AndroFuse.
 *
 * Pushes a scoped `IBinder` interface to the AndroFuse Companion app via
 * `IActivityManager.getContentProviderExternal` + `IContentProvider.call("sendBinder")`,
 * authenticates every transaction via kernel `Binder.getCallingUid()`, verifies zero-copy
 * `Os.socketpair` `ParcelFileDescriptor` transfers, and supervises `scrcpy-server v5.0.1`
 * video/control streams over those FDs without opening TCP ports or exposing arbitrary shell execution.
 */
final class Daemon {

    private static final String DESCRIPTOR = "com.androfuse.daemon.IDaemon";
    private static final String LOCK_PATH = "/data/local/tmp/androfuse-daemon.lock";
    private static final String DEX_HELPER_PATH = "/data/local/tmp/androfuse-dex-helper.jar";
    private static final String SCRCPY_SERVER_PATH = "/data/local/tmp/androfuse-scrcpy-server.jar";
    private static final String SCRCPY_VERSION = "5.0.1";
    private static final String DEFAULT_PKG = "com.yuanzhe.androfuse";
    private static final String DEFAULT_AUTHORITY = "com.yuanzhe.androfuse.daemon";

    private static final int TX_PING = IBinder.FIRST_CALL_TRANSACTION;
    private static final int TX_REGISTER_CLIENT = IBinder.FIRST_CALL_TRANSACTION + 1;
    private static final int TX_ECHO_FD = IBinder.FIRST_CALL_TRANSACTION + 2;
    private static final int TX_STOP = IBinder.FIRST_CALL_TRANSACTION + 3;
    private static final int TX_OPEN_SESSION = IBinder.FIRST_CALL_TRANSACTION + 4;
    private static final int TX_CLOSE_SESSION = IBinder.FIRST_CALL_TRANSACTION + 5;
    private static final int TX_LIST_APPS = IBinder.FIRST_CALL_TRANSACTION + 6;

    private static String verifyAndHashJar(String path) throws Exception {
        StructStat st = Os.stat(path);
        int selfUid = Os.getuid();
        if (st.st_uid != selfUid && st.st_uid != 0) {
            throw new SecurityException("Untrusted owner UID " + st.st_uid + " for " + path);
        }
        if ((st.st_mode & 0x0002) != 0) {
            throw new SecurityException("World-writable JAR rejected: " + path);
        }
        MessageDigest md = MessageDigest.getInstance("SHA-256");
        byte[] buf = new byte[32 * 1024];
        try (FileInputStream in = new FileInputStream(path)) {
            int n;
            while ((n = in.read(buf)) > 0) {
                md.update(buf, 0, n);
            }
        }
        byte[] digest = md.digest();
        StringBuilder hex = new StringBuilder(digest.length * 2);
        for (byte b : digest) {
            int v = b & 0xff;
            if (v < 0x10) {
                hex.append('0');
            }
            hex.append(Integer.toHexString(v));
        }
        return hex.toString();
    }

    private static String verifyPinnedJar(String path, String expectedSha256) throws Exception {
        String actual = verifyAndHashJar(path);
        if (expectedSha256 != null && !expectedSha256.isEmpty() && !expectedSha256.equalsIgnoreCase(actual)) {
            throw new SecurityException("JAR integrity mismatch for " + path);
        }
        return actual;
    }

    private Daemon() {}

    static void run(String[] args) throws Exception {
        String pkg = DEFAULT_PKG;
        String authority = DEFAULT_AUTHORITY;
        for (int i = 1; i < args.length; i++) {
            if ("--pkg".equals(args[i]) && i + 1 < args.length) {
                pkg = args[++i].trim();
            } else if ("--authority".equals(args[i]) && i + 1 < args.length) {
                authority = args[++i].trim();
            }
        }
        if (pkg.isEmpty() || authority.isEmpty()) {
            throw new IllegalArgumentException("daemon expects non-empty --pkg and --authority");
        }

        RandomAccessFile lockFile = new RandomAccessFile(LOCK_PATH, "rw");
        FileLock lock = lockFile.getChannel().tryLock();
        if (lock == null) {
            lockFile.close();
            throw new IllegalStateException("another AndroFuse daemon holds " + LOCK_PATH);
        }

        Context sysCtx = Main.getSystemContextWorkaround();
        PackageManager pm = sysCtx.getPackageManager();
        if (pm == null) {
            throw new IllegalStateException("PackageManager unavailable");
        }
        int expectedUid = pm.getPackageUid(pkg, 0);
        boolean hasTrustedDisplay = pm.checkPermission("android.permission.ADD_TRUSTED_DISPLAY", "com.android.shell")
            == PackageManager.PERMISSION_GRANTED;

        String helperSha = verifyAndHashJar(DEX_HELPER_PATH);
        String scrcpySha = new File(SCRCPY_SERVER_PATH).isFile() ? verifyAndHashJar(SCRCPY_SERVER_PATH) : "";

        CountDownLatch stopLatch = new CountDownLatch(1);
        DaemonBinder binder = new DaemonBinder(authority, expectedUid, hasTrustedDisplay, helperSha, scrcpySha, stopLatch);

        try {
            pushBinderWithRetry(authority, binder, 15_000L);
            System.out.println("AFDEX1|DAEMON|READY|" + Process.myPid() + "|" + Os.getuid() + "|" + hasTrustedDisplay);
            System.out.flush();
            stopLatch.await();
        } finally {
            binder.closeAllSessions();
            try { lock.release(); } catch (Throwable ignored) {}
            try { lockFile.close(); } catch (Throwable ignored) {}
        }
    }

    private static void pushBinderWithRetry(String authority, IBinder binder, long timeoutMs) throws Exception {
        long deadline = System.currentTimeMillis() + timeoutMs;
        Throwable lastErr = null;
        while (true) {
            try {
                pushBinderOnce(authority, binder);
                return;
            } catch (Throwable t) {
                lastErr = t;
                if (System.currentTimeMillis() >= deadline) {
                    throw new IllegalStateException("pushBinder to " + authority + " failed: " + lastErr, lastErr);
                }
                Thread.sleep(300L);
            }
        }
    }

    private static void pushBinderOnce(String authority, IBinder daemonBinder) throws Exception {
        Class<?> amCls = Class.forName("android.app.ActivityManager");
        Object am = amCls.getMethod("getService").invoke(null);
        if (am == null) {
            throw new IllegalStateException("ActivityManager.getService() returned null");
        }

        IBinder token = new Binder();
        Object holder = null;
        try {
            holder = getContentProviderExternal(am, authority, token);
            if (holder == null) {
                throw new IllegalStateException("getContentProviderExternal(" + authority + ") returned null");
            }
            Field pf = holder.getClass().getField("provider");
            Object provider = pf.get(holder);
            if (provider == null) {
                throw new IllegalStateException("ContentProviderHolder.provider is null for " + authority);
            }

            Bundle extras = new Bundle();
            extras.putBinder("binder", daemonBinder);
            invokeProviderCall(provider, authority, "sendBinder", null, extras);
        } finally {
            if (holder != null) {
                removeContentProviderExternal(am, authority, token);
            }
        }
    }

    private static Object getContentProviderExternal(Object am, String authority, IBinder token) throws Exception {
        Class<?> cls = am.getClass();
        try {
            Method m = cls.getMethod("getContentProviderExternal", String.class, int.class, IBinder.class, String.class);
            return m.invoke(am, authority, 0, token, "androfuse-daemon");
        } catch (NoSuchMethodException ignored) {
            Method m = cls.getMethod("getContentProviderExternal", String.class, int.class, IBinder.class);
            return m.invoke(am, authority, 0, token);
        }
    }

    private static void removeContentProviderExternal(Object am, String authority, IBinder token) {
        Class<?> cls = am.getClass();
        try {
            cls.getMethod("removeContentProviderExternal", String.class, IBinder.class).invoke(am, authority, token);
        } catch (Throwable ignored) {
            try {
                cls.getMethod("removeContentProviderExternal", String.class).invoke(am, authority);
            } catch (Throwable ignored2) {}
        }
    }

    private static void invokeProviderCall(Object provider, String authority, String method, String arg, Bundle extras) throws Exception {
        Class<?> cls = provider.getClass();
        for (Method m : cls.getMethods()) {
            if (!"call".equals(m.getName())) {
                continue;
            }
            Class<?>[] pt = m.getParameterTypes();
            if (Build.VERSION.SDK_INT >= 31
                && pt.length == 5
                && AttributionSource.class.isAssignableFrom(pt[0])
                && pt[1] == String.class
                && pt[2] == String.class
                && pt[3] == String.class
                && pt[4] == Bundle.class) {
                AttributionSource attr = new AttributionSource.Builder(Os.getuid())
                    .setPackageName("com.android.shell")
                    .build();
                m.invoke(provider, attr, authority, method, arg, extras);
                return;
            }
            if (pt.length == 6
                && pt[0] == String.class
                && pt[1] == String.class
                && pt[2] == String.class
                && pt[3] == String.class
                && pt[4] == String.class
                && pt[5] == Bundle.class) {
                m.invoke(provider, "com.android.shell", null, authority, method, arg, extras);
                return;
            }
            if (pt.length == 5
                && pt[0] == String.class
                && pt[1] == String.class
                && pt[2] == String.class
                && pt[3] == String.class
                && pt[4] == Bundle.class) {
                m.invoke(provider, "com.android.shell", authority, method, arg, extras);
                return;
            }
        }
        throw new NoSuchMethodException("No compatible IContentProvider.call overload on " + cls.getName());
    }

    private static final class ActiveSession {
        final String id;
        final java.lang.Process proc;
        final java.lang.Process dexProc;
        final ParcelFileDescriptor videoPfd;
        final ParcelFileDescriptor ctrlPfd;
        final LocalSocket videoSock;
        final LocalSocket ctrlSock;
        volatile boolean closed;

        ActiveSession(
            String id,
            java.lang.Process proc,
            java.lang.Process dexProc,
            ParcelFileDescriptor videoPfd,
            ParcelFileDescriptor ctrlPfd,
            LocalSocket videoSock,
            LocalSocket ctrlSock
        ) {
            this.id = id;
            this.proc = proc;
            this.dexProc = dexProc;
            this.videoPfd = videoPfd;
            this.ctrlPfd = ctrlPfd;
            this.videoSock = videoSock;
            this.ctrlSock = ctrlSock;
        }

        void close() {
            if (closed) {
                return;
            }
            closed = true;
            try { if (videoSock != null) videoSock.close(); } catch (Throwable ignored) {}
            try { if (ctrlSock != null) ctrlSock.close(); } catch (Throwable ignored) {}
            try { if (videoPfd != null) videoPfd.close(); } catch (Throwable ignored) {}
            try { if (ctrlPfd != null) ctrlPfd.close(); } catch (Throwable ignored) {}
            try {
                if (proc != null && proc.isAlive()) {
                    proc.destroy();
                    if (!proc.waitFor(500L, TimeUnit.MILLISECONDS)) {
                        proc.destroyForcibly();
                    }
                }
            } catch (Throwable ignored) {
                try { if (proc != null) proc.destroyForcibly(); } catch (Throwable ignored2) {}
            }
            if (dexProc != null) {
                try {
                    if (dexProc.isAlive()) {
                        try {
                            OutputStream os = dexProc.getOutputStream();
                            os.write("STOP\n".getBytes(StandardCharsets.UTF_8));
                            os.flush();
                        } catch (Throwable ignored) {}
                        if (!dexProc.waitFor(1500L, TimeUnit.MILLISECONDS)) {
                            dexProc.destroyForcibly();
                        }
                    }
                } catch (Throwable ignored) {
                    try { dexProc.destroyForcibly(); } catch (Throwable ignored2) {}
                }
            }
        }
    }

    private static final class DaemonBinder extends Binder {
        private final String authority;
        private final int expectedUid;
        private final boolean hasTrustedDisplay;
        private final String helperSha256;
        private String scrcpySha256;
        private final CountDownLatch stopLatch;
        private final SecureRandom rng = new SecureRandom();
        private final Object clientLock = new Object();
        private final Object sessionLock = new Object();
        private final Map<String, ActiveSession> sessions = new HashMap<>();
        private IBinder clientToken;
        private IBinder.DeathRecipient clientDeath;
        private boolean reattaching;

        DaemonBinder(String authority, int expectedUid, boolean hasTrustedDisplay, String helperSha256, String scrcpySha256, CountDownLatch stopLatch) {
            this.authority = authority;
            this.expectedUid = expectedUid;
            this.hasTrustedDisplay = hasTrustedDisplay;
            this.helperSha256 = helperSha256;
            this.scrcpySha256 = scrcpySha256;
            this.stopLatch = stopLatch;
        }

        private void enforceCaller() {
            int callingUid = Binder.getCallingUid();
            if (callingUid != expectedUid) {
                throw new SecurityException("Unauthorized caller UID " + callingUid + " (expected " + expectedUid + ")");
            }
        }

        private String statusJson() {
            int active;
            synchronized (sessionLock) {
                active = sessions.size();
            }
            return "{"
                + "\"ok\":true,"
                + "\"pid\":" + Process.myPid() + ","
                + "\"uid\":" + Os.getuid() + ","
                + "\"sdk\":" + Build.VERSION.SDK_INT + ","
                + "\"has_trusted_display\":" + hasTrustedDisplay + ","
                + "\"active_sessions\":" + active
                + "}";
        }

        @Override
        protected boolean onTransact(int code, Parcel data, Parcel reply, int flags) throws RemoteException {
            if (code == INTERFACE_TRANSACTION) {
                reply.writeString(DESCRIPTOR);
                return true;
            }
            switch (code) {
                case TX_PING: {
                    data.enforceInterface(DESCRIPTOR);
                    enforceCaller();
                    reply.writeNoException();
                    reply.writeString(statusJson());
                    return true;
                }
                case TX_REGISTER_CLIENT: {
                    data.enforceInterface(DESCRIPTOR);
                    enforceCaller();
                    IBinder token = data.readStrongBinder();
                    registerClientLocked(token);
                    reply.writeNoException();
                    return true;
                }
                case TX_ECHO_FD: {
                    data.enforceInterface(DESCRIPTOR);
                    enforceCaller();
                    ParcelFileDescriptor pfd = ParcelFileDescriptor.CREATOR.createFromParcel(data);
                    if (pfd == null) {
                        throw new IllegalArgumentException("missing ParcelFileDescriptor");
                    }
                    try (ParcelFileDescriptor owned = pfd) {
                        FileInputStream in = new FileInputStream(owned.getFileDescriptor());
                        FileOutputStream out = new FileOutputStream(owned.getFileDescriptor());
                        byte[] buf = new byte[4096];
                        int n = in.read(buf);
                        String msg = n > 0 ? new String(Arrays.copyOf(buf, n), StandardCharsets.UTF_8) : "";
                        out.write(("DAEMON-OK:" + msg).getBytes(StandardCharsets.UTF_8));
                        out.flush();
                    } catch (Throwable t) {
                        throw new IllegalStateException("echo fd failed: " + t.getMessage(), t);
                    }
                    reply.writeNoException();
                    return true;
                }
                case TX_OPEN_SESSION: {
                    data.enforceInterface(DESCRIPTOR);
                    enforceCaller();
                    String sessionId = data.readString();
                    ParcelFileDescriptor videoPfd = ParcelFileDescriptor.CREATOR.createFromParcel(data);
                    boolean hasCtrl = data.readInt() != 0;
                    ParcelFileDescriptor ctrlPfd = hasCtrl ? ParcelFileDescriptor.CREATOR.createFromParcel(data) : null;
                    int maxSize = data.readInt();
                    int bitRate = data.readInt();
                    int maxFps = data.readInt();
                    String codec = data.readString();
                    String newDisplay = data.readString();
                    boolean hideDecorations = data.readInt() != 0;
                    boolean flexDisplay = data.readInt() != 0;
                    int displayId = data.readInt();
                    String startApp = data.dataAvail() > 0 ? data.readString() : "";
                    openSession(sessionId, videoPfd, ctrlPfd, maxSize, bitRate, maxFps, codec, newDisplay, hideDecorations, flexDisplay, displayId, startApp);
                    reply.writeNoException();
                    return true;
                }
                case TX_CLOSE_SESSION: {
                    data.enforceInterface(DESCRIPTOR);
                    enforceCaller();
                    String sessionId = data.readString();
                    closeSession(sessionId);
                    reply.writeNoException();
                    return true;
                }
                case TX_STOP: {
                    data.enforceInterface(DESCRIPTOR);
                    enforceCaller();
                    closeAllSessions();
                    unregisterClientLocked();
                    reply.writeNoException();
                    stopLatch.countDown();
                    return true;
                }
                case TX_LIST_APPS: {
                    data.enforceInterface(DESCRIPTOR);
                    enforceCaller();
                    String json = listAppsJson();
                    reply.writeNoException();
                    reply.writeString(json);
                    return true;
                }
                default:
                    return super.onTransact(code, data, reply, flags);
            }
        }

        private void openSession(
            String sessionId,
            ParcelFileDescriptor videoPfd,
            ParcelFileDescriptor ctrlPfd,
            int maxSize,
            int bitRate,
            int maxFps,
            String codec,
            String newDisplay,
            boolean hideDecorations,
            boolean flexDisplay,
            int displayId,
            String startApp
        ) {
            if (sessionId == null || !sessionId.matches("[A-Za-z0-9_-]{4,32}")) {
                closeQuiet(videoPfd);
                closeQuiet(ctrlPfd);
                throw new IllegalArgumentException("invalid sessionId");
            }
            if (videoPfd == null) {
                closeQuiet(ctrlPfd);
                throw new IllegalArgumentException("missing video ParcelFileDescriptor");
            }
            String safeCodec = "h265".equalsIgnoreCase(codec) ? "h265" : "h264";
            String safeNewDisplay = newDisplay == null ? "" : newDisplay.trim();
            String safeStartApp = startApp == null ? "" : startApp.trim();
            if (!safeStartApp.isEmpty() && !safeStartApp.matches("\\+?[A-Za-z0-9_.-]{2,128}")) {
                closeQuiet(videoPfd);
                closeQuiet(ctrlPfd);
                throw new IllegalArgumentException("invalid start_app spec");
            }
            boolean isNativeDeX = safeNewDisplay.startsWith("dex");
            if (!safeNewDisplay.isEmpty()
                && !safeNewDisplay.matches("[0-9x/]{3,24}")
                && !safeNewDisplay.matches("dex(:[0-9]{3,4}x[0-9]{3,4}/[0-9]{2,3})?")) {
                closeQuiet(videoPfd);
                closeQuiet(ctrlPfd);
                throw new IllegalArgumentException("invalid new_display spec");
            }
            int safeBitRate = bitRate > 0 ? Math.min(bitRate, 64_000_000) : 8_000_000;
            String expectedScrcpy;
            synchronized (sessionLock) {
                expectedScrcpy = scrcpySha256;
            }
            try {
                String actualScrcpy = verifyPinnedJar(SCRCPY_SERVER_PATH, expectedScrcpy);
                if (expectedScrcpy == null || expectedScrcpy.isEmpty()) {
                    synchronized (sessionLock) {
                        if (scrcpySha256 == null || scrcpySha256.isEmpty()) {
                            scrcpySha256 = actualScrcpy;
                        }
                    }
                }
            } catch (Throwable t) {
                closeQuiet(videoPfd);
                closeQuiet(ctrlPfd);
                throw new SecurityException("scrcpy-server verification failed: " + t.getMessage(), t);
            }
            boolean wantCtrl = ctrlPfd != null;
            int scid = rng.nextInt(0x7fffffff);
            String scidHex = String.format("%08x", scid);

            java.lang.Process dexProc = null;
            int targetDisplayId = displayId;
            String scrcpyNewDisplay = safeNewDisplay;
            if (isNativeDeX) {
                int dexW = 1920;
                int dexH = 1080;
                int dexDpi = 160;
                if (safeNewDisplay.startsWith("dex:")) {
                    String spec = safeNewDisplay.substring(4);
                    int slash = spec.indexOf('/');
                    int xIdx = spec.indexOf('x');
                    if (xIdx > 0 && slash > xIdx) {
                        dexW = Integer.parseInt(spec.substring(0, xIdx));
                        dexH = Integer.parseInt(spec.substring(xIdx + 1, slash));
                        dexDpi = Integer.parseInt(spec.substring(slash + 1));
                    }
                }
                String dexToken = String.format("%08x", rng.nextInt(0x7fffffff));
                try {
                    DexServeHandle dh = startDeXLoopback(dexToken, dexW, dexH, dexDpi, helperSha256);
                    dexProc = dh.proc;
                    targetDisplayId = dh.displayId;
                    scrcpyNewDisplay = "";
                } catch (Throwable t) {
                    closeQuiet(videoPfd);
                    closeQuiet(ctrlPfd);
                    throw new IllegalStateException("dexhelper serve failed: " + t.getMessage(), t);
                }
            }

            List<String> cmd = new ArrayList<>();
            cmd.add("/system/bin/app_process");
            cmd.add("/");
            cmd.add("com.genymobile.scrcpy.Server");
            cmd.add(SCRCPY_VERSION);
            cmd.add("scid=" + scidHex);
            cmd.add("log_level=info");
            cmd.add("tunnel_forward=true");
            cmd.add("audio=false");
            cmd.add("video=true");
            cmd.add("control=" + wantCtrl);
            cmd.add("video_codec=" + safeCodec);
            cmd.add("video_bit_rate=" + safeBitRate);
            cmd.add("downsize_on_error=true");
            // Keep scrcpy-server.jar on disk across repeated 5G/daemon sessions; process exit
            // still releases any VirtualDisplay via binder death.
            cmd.add("cleanup=false");
            cmd.add("power_on=true");
            if (maxSize > 0) {
                cmd.add("max_size=" + Math.min(maxSize, 4096));
            }
            if (maxFps > 0) {
                cmd.add("max_fps=" + Math.min(maxFps, 120));
            }
            if (!scrcpyNewDisplay.isEmpty()) {
                cmd.add("new_display=" + scrcpyNewDisplay);
                if (flexDisplay) {
                    cmd.add("flex_display=true");
                }
                if (hideDecorations) {
                    cmd.add("vd_system_decorations=false");
                }
            } else if (targetDisplayId > 0) {
                cmd.add("display_id=" + targetDisplayId);
            }
            if (!safeStartApp.isEmpty()) {
                cmd.add("start_app=" + safeStartApp);
            }

            java.lang.Process proc = null;
            LocalSocket videoSock = null;
            LocalSocket ctrlSock = null;
            StringBuilder serverLog = new StringBuilder();
            try {
                ProcessBuilder pb = new ProcessBuilder(cmd);
                pb.environment().put("CLASSPATH", SCRCPY_SERVER_PATH);
                pb.redirectErrorStream(true);
                proc = pb.start();
                drainProcessOutput(proc, serverLog);

                String sockName = "scrcpy_" + scidHex;
                videoSock = connectLocalRetry(sockName, proc, 8_000L);
                try { videoSock.setReceiveBufferSize(64 * 1024); } catch (Throwable ignored) {}
                try { videoSock.setSendBufferSize(64 * 1024); } catch (Throwable ignored) {}
                if (wantCtrl) {
                    ctrlSock = connectLocalRetry(sockName, proc, 5_000L);
                }

                ActiveSession s = new ActiveSession(sessionId, proc, dexProc, videoPfd, ctrlPfd, videoSock, ctrlSock);
                ActiveSession prev;
                synchronized (sessionLock) {
                    prev = sessions.put(sessionId, s);
                }
                if (prev != null) {
                    prev.close();
                }

                pumpPair(s, videoSock.getInputStream(), new FileOutputStream(videoPfd.getFileDescriptor()), true);
                pumpPair(s, new FileInputStream(videoPfd.getFileDescriptor()), videoSock.getOutputStream(), true);
                if (wantCtrl && ctrlSock != null) {
                    pumpPair(s, ctrlSock.getInputStream(), new FileOutputStream(ctrlPfd.getFileDescriptor()), false);
                    pumpPair(s, new FileInputStream(ctrlPfd.getFileDescriptor()), ctrlSock.getOutputStream(), false);
                }
            } catch (Throwable t) {
                try { if (videoSock != null) videoSock.close(); } catch (Throwable ignored) {}
                try { if (ctrlSock != null) ctrlSock.close(); } catch (Throwable ignored) {}
                closeQuiet(videoPfd);
                closeQuiet(ctrlPfd);
                if (proc != null) {
                    try { proc.destroyForcibly(); } catch (Throwable ignored) {}
                }
                if (dexProc != null) {
                    try { dexProc.destroyForcibly(); } catch (Throwable ignored) {}
                }
                throw new IllegalStateException("scrcpy daemon session failed: " + t.getMessage() + " (" + serverLog + ")", t);
            }
        }

        private static LocalSocket connectLocalRetry(String sockName, java.lang.Process proc, long timeoutMs) throws IOException {
            LocalSocketAddress addr = new LocalSocketAddress(sockName, LocalSocketAddress.Namespace.ABSTRACT);
            long deadline = System.currentTimeMillis() + timeoutMs;
            IOException last = null;
            while (System.currentTimeMillis() < deadline) {
                if (!proc.isAlive()) {
                    throw new IOException("scrcpy-server exited prematurely (code=" + proc.exitValue() + ")");
                }
                LocalSocket s = new LocalSocket();
                try {
                    s.connect(addr);
                    return s;
                } catch (IOException e) {
                    last = e;
                    try { s.close(); } catch (Throwable ignored) {}
                    try { Thread.sleep(60L); } catch (InterruptedException ie) {
                        Thread.currentThread().interrupt();
                        throw new IOException("interrupted waiting for scrcpy socket", ie);
                    }
                }
            }
            throw last != null ? last : new IOException("timed out connecting to @" + sockName);
        }

        private static final class DexServeHandle {
            final java.lang.Process proc;
            final int displayId;

            DexServeHandle(java.lang.Process proc, int displayId) {
                this.proc = proc;
                this.displayId = displayId;
            }
        }

        private static DexServeHandle startDeXLoopback(String token, int width, int height, int dpi, String expectedHelperSha256) throws Exception {
            verifyPinnedJar(DEX_HELPER_PATH, expectedHelperSha256);
            ProcessBuilder pb = new ProcessBuilder(
                "/system/bin/app_process",
                "/",
                "com.androfuse.dexhelper.Main",
                "serve",
                token,
                Integer.toString(width),
                Integer.toString(height),
                Integer.toString(dpi),
                "5"
            );
            pb.environment().put("CLASSPATH", DEX_HELPER_PATH);
            pb.redirectErrorStream(true);
            java.lang.Process proc = pb.start();
            CountDownLatch readyLatch = new CountDownLatch(1);
            int[] readyDisplay = new int[] { 0 };
            StringBuilder dexLog = new StringBuilder();
            final String readyPrefix = "AFDEX1|" + token + "|READY|";
            final String errPrefix = "AFDEX1|" + token + "|ERR|";
            Thread reader = new Thread(() -> {
                try (BufferedReader br = new BufferedReader(new InputStreamReader(proc.getInputStream(), StandardCharsets.UTF_8))) {
                    String line;
                    while ((line = br.readLine()) != null) {
                        synchronized (dexLog) {
                            if (dexLog.length() < 1024) {
                                dexLog.append(line).append('\n');
                            }
                        }
                        if (line.startsWith(readyPrefix)) {
                            String[] parts = line.split("\\|");
                            if (parts.length >= 4) {
                                try {
                                    readyDisplay[0] = Integer.parseInt(parts[3].trim());
                                } catch (NumberFormatException ignored) {}
                            }
                            readyLatch.countDown();
                        } else if (line.startsWith(errPrefix)) {
                            readyLatch.countDown();
                        }
                    }
                } catch (Throwable ignored) {
                } finally {
                    readyLatch.countDown();
                }
            }, "AndroFuse-DeX-Serve");
            reader.setDaemon(true);
            reader.start();

            boolean signaled = readyLatch.await(15L, TimeUnit.SECONDS);
            int did = readyDisplay[0];
            if (!signaled || did <= 0) {
                try { proc.destroyForcibly(); } catch (Throwable ignored) {}
                String msg;
                synchronized (dexLog) {
                    msg = dexLog.toString().trim();
                }
                throw new IllegalStateException("DeX display did not become ready (" + msg + ")");
            }
            return new DexServeHandle(proc, did);
        }

        private void pumpPair(ActiveSession s, InputStream in, OutputStream out, boolean closeSessionOnEnd) {
            Thread t = new Thread(() -> {
                byte[] buf = new byte[32 * 1024];
                try {
                    while (!s.closed) {
                        int n = in.read(buf);
                        if (n <= 0) {
                            break;
                        }
                        out.write(buf, 0, n);
                    }
                } catch (Throwable ignored) {
                } finally {
                    if (closeSessionOnEnd) {
                        closeSession(s.id);
                    }
                }
            }, "AndroFuse-Daemon-Pump-" + s.id);
            t.setDaemon(true);
            t.start();
        }

        private static void drainProcessOutput(java.lang.Process proc, StringBuilder log) {
            Thread t = new Thread(() -> {
                byte[] buf = new byte[1024];
                try (InputStream in = proc.getInputStream()) {
                    while (true) {
                        int n = in.read(buf);
                        if (n <= 0) break;
                        synchronized (log) {
                            if (log.length() < 1024) {
                                log.append(new String(buf, 0, n, StandardCharsets.UTF_8));
                            }
                        }
                    }
                } catch (Throwable ignored) {}
            }, "AndroFuse-Scrcpy-Log");
            t.setDaemon(true);
            t.start();
        }

        private String listAppsJson() {
            try {
                verifyPinnedJar(DEX_HELPER_PATH, helperSha256);
                ProcessBuilder pb = new ProcessBuilder(
                    "/system/bin/app_process",
                    "/",
                    "com.androfuse.dexhelper.Main",
                    "apps",
                    "--no-icons"
                );
                pb.environment().put("CLASSPATH", DEX_HELPER_PATH);
                java.lang.Process p = pb.start();
                StringBuilder arr = new StringBuilder("[");
                boolean first = true;
                try (BufferedReader br = new BufferedReader(new InputStreamReader(p.getInputStream(), StandardCharsets.UTF_8))) {
                    String line;
                    while ((line = br.readLine()) != null) {
                        if (line.startsWith("AFDEX1|APP|")) {
                            String item = line.substring("AFDEX1|APP|".length()).trim();
                            if (!first) {
                                arr.append(',');
                            }
                            arr.append(item);
                            first = false;
                        }
                    }
                }
                if (!p.waitFor(15L, TimeUnit.SECONDS)) {
                    p.destroyForcibly();
                }
                arr.append(']');
                return arr.toString();
            } catch (Throwable ignored) {
                return "[]";
            }
        }

        private void closeSession(String sessionId) {
            if (sessionId == null) return;
            ActiveSession s;
            synchronized (sessionLock) {
                s = sessions.remove(sessionId);
            }
            if (s != null) {
                s.close();
            }
        }

        void closeAllSessions() {
            List<ActiveSession> copy;
            synchronized (sessionLock) {
                copy = new ArrayList<>(sessions.values());
                sessions.clear();
            }
            for (ActiveSession s : copy) {
                s.close();
            }
        }

        private static void closeQuiet(ParcelFileDescriptor pfd) {
            if (pfd != null) {
                try { pfd.close(); } catch (Throwable ignored) {}
            }
        }

        private void registerClientLocked(IBinder token) {
            synchronized (clientLock) {
                unregisterClientLocked();
                if (token == null) {
                    return;
                }
                IBinder.DeathRecipient dr = () -> {
                    // Clean up active sessions immediately when Companion exits (`PLAN` §9.2, `INV-03`).
                    closeAllSessions();
                    synchronized (clientLock) {
                        clientToken = null;
                        clientDeath = null;
                    }
                    scheduleReattach();
                };
                try {
                    token.linkToDeath(dr, 0);
                    clientToken = token;
                    clientDeath = dr;
                } catch (RemoteException dead) {
                    closeAllSessions();
                    clientToken = null;
                    clientDeath = null;
                    scheduleReattach();
                }
            }
        }

        private void scheduleReattach() {
            synchronized (clientLock) {
                if (reattaching || stopLatch.getCount() == 0 || clientToken != null) {
                    return;
                }
                reattaching = true;
            }
            Thread t = new Thread(() -> {
                try {
                    while (stopLatch.getCount() > 0) {
                        synchronized (clientLock) {
                            if (clientToken != null) {
                                return;
                            }
                        }
                        try {
                            pushBinderOnce(authority, DaemonBinder.this);
                            return;
                        } catch (Throwable ignored) {}
                        try {
                            Thread.sleep(2000L);
                        } catch (InterruptedException ie) {
                            Thread.currentThread().interrupt();
                            return;
                        }
                    }
                } finally {
                    synchronized (clientLock) {
                        reattaching = false;
                    }
                }
            }, "AndroFuse-Daemon-Reattach");
            t.setDaemon(true);
            t.start();
        }

        private void unregisterClientLocked() {
            synchronized (clientLock) {
                if (clientToken != null && clientDeath != null) {
                    try {
                        clientToken.unlinkToDeath(clientDeath, 0);
                    } catch (Throwable ignored) {}
                }
                clientToken = null;
                clientDeath = null;
            }
        }
    }
}
