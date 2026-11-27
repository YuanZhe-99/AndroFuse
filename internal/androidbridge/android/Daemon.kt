// Binder handshake and zero-copy socketpair IPC between the Companion app (`untrusted_app`)
// and the persistent `uid=2000` (`shell`) AndroFuse daemon (`PLAN` §9.2).
//
// Security rules it enforces (`INV-07`, `INV-09`):
//  - `AndroFuseDaemonProvider.call()` accepts only `Process.SHELL_UID` (`2000`) or `Process.ROOT_UID`
//    via kernel `Binder.getCallingUid()`, rejecting all other apps with `SecurityException`.
//  - Stream verification uses `Os.socketpair` + `ParcelFileDescriptor` (`TX_ECHO_FD`) without
//    opening TCP ports or invoking arbitrary shell commands.

package dev.mygo

import android.content.ContentProvider
import android.content.ContentValues
import android.database.Cursor
import android.net.LocalServerSocket
import android.net.LocalSocket
import android.net.Uri
import android.os.Binder
import android.os.Bundle
import android.os.IBinder
import android.os.Parcel
import android.os.ParcelFileDescriptor
import android.os.Process
import android.system.Os
import android.system.OsConstants
import org.json.JSONObject
import java.io.FileInputStream
import java.io.FileOutputStream
import java.io.InputStream
import java.io.OutputStream
import java.nio.charset.StandardCharsets
import java.security.SecureRandom
import java.util.Arrays
import java.util.concurrent.ConcurrentHashMap

/** Exported ContentProvider receiving the daemon's scoped `IBinder` from UID 2000. */
class AndroFuseDaemonProvider : ContentProvider() {
    override fun onCreate(): Boolean = true

    override fun query(uri: Uri, projection: Array<out String>?, selection: String?, selectionArgs: Array<out String>?, sortOrder: String?): Cursor? = null
    override fun getType(uri: Uri): String? = null
    override fun insert(uri: Uri, values: ContentValues?): Uri? = null
    override fun delete(uri: Uri, selection: String?, selectionArgs: Array<out String>?): Int = 0
    override fun update(uri: Uri, values: ContentValues?, selection: String?, selectionArgs: Array<out String>?): Int = 0

    override fun call(method: String, arg: String?, extras: Bundle?): Bundle {
        val callerUid = Binder.getCallingUid()
        if (callerUid != Process.SHELL_UID && callerUid != Process.ROOT_UID) {
            throw SecurityException("AndroFuseDaemonProvider rejects caller UID $callerUid")
        }
        val out = Bundle()
        if (method == "sendBinder") {
            val binder = extras?.getBinder("binder")
            if (binder != null) {
                AndroFuseDaemon.onBinderReceived(binder)
                out.putBoolean("ok", true)
            } else {
                out.putBoolean("ok", false)
            }
        }
        return out
    }
}

/** Singleton client managing the active `IBinder` reference to the UID 2000 daemon. */
object AndroFuseDaemon {
    private const val DESCRIPTOR = "com.androfuse.daemon.IDaemon"
    private const val TX_PING = IBinder.FIRST_CALL_TRANSACTION
    private const val TX_REGISTER_CLIENT = IBinder.FIRST_CALL_TRANSACTION + 1
    private const val TX_ECHO_FD = IBinder.FIRST_CALL_TRANSACTION + 2
    private const val TX_STOP = IBinder.FIRST_CALL_TRANSACTION + 3
    private const val TX_OPEN_SESSION = IBinder.FIRST_CALL_TRANSACTION + 4
    private const val TX_CLOSE_SESSION = IBinder.FIRST_CALL_TRANSACTION + 5
    private const val TX_LIST_APPS = IBinder.FIRST_CALL_TRANSACTION + 6

    private class BridgeSession(
        val id: String,
        val videoPfd: ParcelFileDescriptor,
        val ctrlPfd: ParcelFileDescriptor?,
        val videoServer: LocalServerSocket,
        val ctrlServer: LocalServerSocket?
    ) {
        @Volatile var closed = false
        @Volatile var videoSock: LocalSocket? = null
        @Volatile var ctrlSock: LocalSocket? = null

        fun closeLocal() {
            if (closed) return
            closed = true
            try { videoSock?.close() } catch (_: Throwable) {}
            try { ctrlSock?.close() } catch (_: Throwable) {}
            try { videoServer.close() } catch (_: Throwable) {}
            try { ctrlServer?.close() } catch (_: Throwable) {}
            try { videoPfd.close() } catch (_: Throwable) {}
            try { ctrlPfd?.close() } catch (_: Throwable) {}
        }
    }

    private val lock = Any()
    private val rng = SecureRandom()
    private val clientToken = Binder()
    private val sessions = ConcurrentHashMap<String, BridgeSession>()
    private var remote: IBinder? = null
    private var deathRecipient: IBinder.DeathRecipient? = null
    private var lastStatus: JSONObject = emptyStatus()

    private fun emptyStatus(): JSONObject = JSONObject()
        .put("ready", false)
        .put("pid", 0)
        .put("has_trusted_display", false)
        .put("fd_verified", false)

    fun onBinderReceived(binder: IBinder) {
        synchronized(lock) {
            unlinkLocked()
            val dr = IBinder.DeathRecipient {
                synchronized(lock) {
                    if (remote === binder) {
                        remote = null
                        deathRecipient = null
                        lastStatus = emptyStatus()
                    }
                }
                emitState(lastStatus)
            }
            try {
                binder.linkToDeath(dr, 0)
                remote = binder
                deathRecipient = dr
            } catch (e: Exception) {
                remote = null
                deathRecipient = null
                lastStatus = emptyStatus()
                emitState(lastStatus)
                return
            }
        }
        registerClient(binder)
        val st = refreshStatusLocked(binder)
        emitState(st)
    }

    fun status(): JSONObject {
        val b = synchronized(lock) { remote } ?: return synchronized(lock) { lastStatus }
        return refreshStatusLocked(b)
    }

    fun testSocketPair(): JSONObject {
        val b = synchronized(lock) { remote }
            ?: return JSONObject().put("ok", false).put("error", "daemon not connected")
        var localPfd: ParcelFileDescriptor? = null
        var remotePfd: ParcelFileDescriptor? = null
        try {
            val fd0 = java.io.FileDescriptor()
            val fd1 = java.io.FileDescriptor()
            Os.socketpair(OsConstants.AF_UNIX, OsConstants.SOCK_STREAM, 0, fd0, fd1)
            try {
                localPfd = ParcelFileDescriptor.dup(fd0)
                remotePfd = ParcelFileDescriptor.dup(fd1)
            } finally {
                try { Os.close(fd0) } catch (_: Throwable) {}
                try { Os.close(fd1) } catch (_: Throwable) {}
            }

            val payload = "ping".toByteArray(StandardCharsets.UTF_8)
            FileOutputStream(localPfd.fileDescriptor).write(payload)

            val data = Parcel.obtain()
            val reply = Parcel.obtain()
            try {
                data.writeInterfaceToken(DESCRIPTOR)
                remotePfd.writeToParcel(data, 0)
                b.transact(TX_ECHO_FD, data, reply, 0)
                reply.readException()
            } finally {
                data.recycle()
                reply.recycle()
                try { remotePfd.close() } catch (_: Throwable) {}
                remotePfd = null
            }

            val want = "DAEMON-OK:ping"
            val buf = ByteArray(64)
            val fin = FileInputStream(localPfd.fileDescriptor)
            var total = 0
            while (total < want.length && total < buf.size) {
                val n = fin.read(buf, total, buf.size - total)
                if (n <= 0) break
                total += n
            }
            val got = String(Arrays.copyOf(buf, total), StandardCharsets.UTF_8)
            val ok = got == want
            synchronized(lock) {
                lastStatus = JSONObject(lastStatus.toString()).put("fd_verified", ok)
            }
            emitState(status())
            return JSONObject().put("ok", ok).put("reply", got)
        } catch (e: Exception) {
            return JSONObject().put("ok", false).put("error", e.message ?: e.javaClass.simpleName)
        } finally {
            try { localPfd?.close() } catch (_: Throwable) {}
            try { remotePfd?.close() } catch (_: Throwable) {}
        }
    }

    fun stopDaemon(): Boolean {
        val b = synchronized(lock) { remote } ?: return false
        for (sid in sessions.keys.toList()) {
            sessions.remove(sid)?.closeLocal()
        }
        val data = Parcel.obtain()
        val reply = Parcel.obtain()
        return try {
            data.writeInterfaceToken(DESCRIPTOR)
            b.transact(TX_STOP, data, reply, 0)
            reply.readException()
            synchronized(lock) {
                unlinkLocked()
                lastStatus = emptyStatus()
            }
            emitState(emptyStatus())
            true
        } catch (_: Exception) {
            false
        } finally {
            data.recycle()
            reply.recycle()
        }
    }

    fun openSession(
        maxSize: Int,
        bitRate: Int,
        maxFps: Int,
        codec: String,
        newDisplay: String,
        hideDecorations: Boolean,
        flexDisplay: Boolean,
        displayId: Int,
        control: Boolean,
        startApp: String = ""
    ): JSONObject {
        val b = synchronized(lock) { remote }
            ?: return JSONObject().put("ok", false).put("error", "uid=2000 daemon is not attached")
        val sid = String.format("%08x", rng.nextInt(0x7fffffff))
        val videoSockName = "androfuse-dvid-$sid"
        val ctrlSockName = if (control) "androfuse-dctrl-$sid" else ""

        var localVid: ParcelFileDescriptor? = null
        var remoteVid: ParcelFileDescriptor? = null
        var localCtrl: ParcelFileDescriptor? = null
        var remoteCtrl: ParcelFileDescriptor? = null
        var vidServer: LocalServerSocket? = null
        var ctrlServer: LocalServerSocket? = null

        try {
            val v0 = java.io.FileDescriptor()
            val v1 = java.io.FileDescriptor()
            Os.socketpair(OsConstants.AF_UNIX, OsConstants.SOCK_STREAM, 0, v0, v1)
            try {
                localVid = ParcelFileDescriptor.dup(v0)
                remoteVid = ParcelFileDescriptor.dup(v1)
            } finally {
                try { Os.close(v0) } catch (_: Throwable) {}
                try { Os.close(v1) } catch (_: Throwable) {}
            }

            if (control) {
                val c0 = java.io.FileDescriptor()
                val c1 = java.io.FileDescriptor()
                Os.socketpair(OsConstants.AF_UNIX, OsConstants.SOCK_STREAM, 0, c0, c1)
                try {
                    localCtrl = ParcelFileDescriptor.dup(c0)
                    remoteCtrl = ParcelFileDescriptor.dup(c1)
                } finally {
                    try { Os.close(c0) } catch (_: Throwable) {}
                    try { Os.close(c1) } catch (_: Throwable) {}
                }
            }

            vidServer = LocalServerSocket(videoSockName)
            if (control) {
                ctrlServer = LocalServerSocket(ctrlSockName)
            }

            val data = Parcel.obtain()
            val reply = Parcel.obtain()
            try {
                data.writeInterfaceToken(DESCRIPTOR)
                data.writeString(sid)
                remoteVid.writeToParcel(data, 0)
                data.writeInt(if (remoteCtrl != null) 1 else 0)
                remoteCtrl?.writeToParcel(data, 0)
                data.writeInt(maxSize)
                data.writeInt(bitRate)
                data.writeInt(maxFps)
                data.writeString(codec)
                data.writeString(newDisplay)
                data.writeInt(if (hideDecorations) 1 else 0)
                data.writeInt(if (flexDisplay) 1 else 0)
                data.writeInt(displayId)
                data.writeString(startApp)
                b.transact(TX_OPEN_SESSION, data, reply, 0)
                reply.readException()
            } finally {
                data.recycle()
                reply.recycle()
                try { remoteVid?.close() } catch (_: Throwable) {}
                try { remoteCtrl?.close() } catch (_: Throwable) {}
                remoteVid = null
                remoteCtrl = null
            }

            val bs = BridgeSession(sid, localVid!!, localCtrl, vidServer, ctrlServer)
            sessions[sid] = bs
            startBridgeAccept(bs, vidServer, localVid, isVideo = true)
            if (ctrlServer != null && localCtrl != null) {
                startBridgeAccept(bs, ctrlServer, localCtrl, isVideo = false)
            }

            return JSONObject()
                .put("ok", true)
                .put("session_id", sid)
                .put("video_socket", videoSockName)
                .put("ctrl_socket", ctrlSockName)
        } catch (e: Exception) {
            try { vidServer?.close() } catch (_: Throwable) {}
            try { ctrlServer?.close() } catch (_: Throwable) {}
            try { localVid?.close() } catch (_: Throwable) {}
            try { localCtrl?.close() } catch (_: Throwable) {}
            try { remoteVid?.close() } catch (_: Throwable) {}
            try { remoteCtrl?.close() } catch (_: Throwable) {}
            return JSONObject().put("ok", false).put("error", e.message ?: e.javaClass.simpleName)
        }
    }

    fun closeSession(sessionId: String): Boolean {
        sessions.remove(sessionId)?.closeLocal()
        val b = synchronized(lock) { remote } ?: return false
        val data = Parcel.obtain()
        val reply = Parcel.obtain()
        return try {
            data.writeInterfaceToken(DESCRIPTOR)
            data.writeString(sessionId)
            b.transact(TX_CLOSE_SESSION, data, reply, 0)
            reply.readException()
            true
        } catch (_: Exception) {
            false
        } finally {
            data.recycle()
            reply.recycle()
        }
    }

    fun listApps(knownPackagesCsv: String = ""): String {
        val b = synchronized(lock) { remote } ?: return "[]"
        var localPfd: ParcelFileDescriptor? = null
        var remotePfd: ParcelFileDescriptor? = null
        val data = Parcel.obtain()
        val reply = Parcel.obtain()
        return try {
            val fd0 = java.io.FileDescriptor()
            val fd1 = java.io.FileDescriptor()
            Os.socketpair(OsConstants.AF_UNIX, OsConstants.SOCK_STREAM, 0, fd0, fd1)
            try {
                localPfd = ParcelFileDescriptor.dup(fd0)
                remotePfd = ParcelFileDescriptor.dup(fd1)
            } finally {
                try { Os.close(fd0) } catch (_: Throwable) {}
                try { Os.close(fd1) } catch (_: Throwable) {}
            }

            data.writeInterfaceToken(DESCRIPTOR)
            data.writeInt(1)
            remotePfd.writeToParcel(data, 0)
            data.writeString(knownPackagesCsv)
            b.transact(TX_LIST_APPS, data, reply, 0)
            reply.readException()
            try { remotePfd.close() } catch (_: Throwable) {}
            remotePfd = null

            val baos = java.io.ByteArrayOutputStream(64 * 1024)
            val buf = ByteArray(32 * 1024)
            FileInputStream(localPfd.fileDescriptor).use { fin ->
                while (baos.size() < 20 * 1024 * 1024) {
                    val n = fin.read(buf)
                    if (n <= 0) break
                    baos.write(buf, 0, n)
                }
            }
            val raw = baos.toString("UTF-8").trim()
            if (raw.startsWith("[")) raw else "[]"
        } catch (_: Exception) {
            "[]"
        } finally {
            data.recycle()
            reply.recycle()
            try { localPfd?.close() } catch (_: Throwable) {}
            try { remotePfd?.close() } catch (_: Throwable) {}
        }
    }

    private fun startBridgeAccept(bs: BridgeSession, srv: LocalServerSocket, pfd: ParcelFileDescriptor, isVideo: Boolean) {
        Thread({
            try {
                val sock = srv.accept()
                if (bs.closed) {
                    try { sock.close() } catch (_: Throwable) {}
                    return@Thread
                }
                if (isVideo) {
                    try { sock.receiveBufferSize = 64 * 1024 } catch (_: Throwable) {}
                    try { sock.sendBufferSize = 64 * 1024 } catch (_: Throwable) {}
                }
                if (isVideo) bs.videoSock = sock else bs.ctrlSock = sock
                pumpStreams(bs, FileInputStream(pfd.fileDescriptor), sock.outputStream, isVideo)
                pumpStreams(bs, sock.inputStream, FileOutputStream(pfd.fileDescriptor), isVideo)
            } catch (_: Throwable) {
                if (isVideo) closeSession(bs.id)
            }
        }, "AndroFuse-Bridge-${if (isVideo) "vid" else "ctrl"}-${bs.id}").apply {
            isDaemon = true
            start()
        }
    }

    private fun pumpStreams(bs: BridgeSession, src: InputStream, dst: OutputStream, isVideo: Boolean) {
        Thread({
            val buf = ByteArray(32 * 1024)
            try {
                while (!bs.closed) {
                    val n = src.read(buf)
                    if (n <= 0) break
                    dst.write(buf, 0, n)
                }
            } catch (_: Throwable) {
            } finally {
                if (isVideo) closeSession(bs.id)
            }
        }, "AndroFuse-Pipe-${bs.id}").apply {
            isDaemon = true
            start()
        }
    }

    private fun registerClient(binder: IBinder) {
        val data = Parcel.obtain()
        val reply = Parcel.obtain()
        try {
            data.writeInterfaceToken(DESCRIPTOR)
            data.writeStrongBinder(clientToken)
            binder.transact(TX_REGISTER_CLIENT, data, reply, 0)
            reply.readException()
        } catch (_: Exception) {
        } finally {
            data.recycle()
            reply.recycle()
        }
    }

    private fun refreshStatusLocked(binder: IBinder): JSONObject {
        val data = Parcel.obtain()
        val reply = Parcel.obtain()
        try {
            data.writeInterfaceToken(DESCRIPTOR)
            binder.transact(TX_PING, data, reply, 0)
            reply.readException()
            val raw = reply.readString() ?: "{}"
            val parsed = JSONObject(raw)
            val out = JSONObject()
                .put("ready", parsed.optBoolean("ok", false))
                .put("pid", parsed.optInt("pid", 0))
                .put("has_trusted_display", parsed.optBoolean("has_trusted_display", false))
            synchronized(lock) {
                out.put("fd_verified", lastStatus.optBoolean("fd_verified", false))
                lastStatus = out
                return out
            }
        } catch (_: Exception) {
            synchronized(lock) {
                unlinkLocked()
                lastStatus = emptyStatus()
                return lastStatus
            }
        } finally {
            data.recycle()
            reply.recycle()
        }
    }

    private fun unlinkLocked() {
        val b = remote
        val dr = deathRecipient
        if (b != null && dr != null) {
            try { b.unlinkToDeath(dr, 0) } catch (_: Throwable) {}
        }
        remote = null
        deathRecipient = null
    }

    private fun emitState(st: JSONObject) {
        try {
            PluginHost.emit("androfusePower", "daemon", st)
        } catch (_: Throwable) {}
    }
}
