// Companion screen sharing for AndroFuse, used when the phone is on mobile data and Wireless
// debugging cannot run. Compiled into the app by `mygo build` with Power.kt (plugin
// "androfusePower").
//
// Rules it enforces (INV-09):
//  - Every share starts with Android's own MediaProjection consent dialog; nothing is captured
//    without it, and FLAG_SECURE windows stay black as Android draws them.
//  - Capture runs only inside a foreground service of type mediaProjection, behind a visible
//    notification with Stop.
//  - Input is limited to the accessibility service's global actions (Back, Home, Recents) and
//    only while the user has turned that service on in Android's settings.
//
// The encoder writes scrcpy-server's video stream framing to the abstract local socket
// "androfuse-share" (64-byte device name, codec id, a session packet with the size, then 12-byte
// media headers), so the desktop decodes it with the same code as scrcpy (internal/scrcpy).

package dev.mygo

import android.accessibilityservice.AccessibilityService
import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.hardware.display.DisplayManager
import android.hardware.display.VirtualDisplay
import android.media.MediaCodec
import android.media.MediaCodecInfo
import android.media.MediaFormat
import android.media.projection.MediaProjection
import android.media.projection.MediaProjectionManager
import android.net.LocalServerSocket
import android.net.LocalSocket
import android.os.Build
import android.os.Bundle
import android.os.IBinder
import android.view.accessibility.AccessibilityEvent
import java.io.OutputStream
import java.nio.ByteBuffer
import java.nio.ByteOrder
import kotlin.math.max
import kotlin.math.roundToInt

/** Starts and stops sharing for AndroFusePowerPlugin. */
object AndroFuseShare {
    fun start(maxSize: Int, bitRate: Int, done: (Boolean, String) -> Unit) {
        val ctx = PluginHost.context
        val mpm = ctx.getSystemService(Context.MEDIA_PROJECTION_SERVICE) as MediaProjectionManager
        if (AndroFuseShareService.running) { done(true, ""); return }
        PluginHost.startForResult(mpm.createScreenCaptureIntent()) { code, data ->
            if (code != android.app.Activity.RESULT_OK || data == null) {
                done(false, "declined")
                return@startForResult
            }
            val intent = Intent(ctx, AndroFuseShareService::class.java)
                .putExtra("code", code).putExtra("data", data)
                .putExtra("maxSize", maxSize).putExtra("bitRate", bitRate)
            if (Build.VERSION.SDK_INT >= 26) ctx.startForegroundService(intent) else ctx.startService(intent)
            done(true, "")
        }
    }

    fun stop() {
        val ctx = PluginHost.context
        ctx.stopService(Intent(ctx, AndroFuseShareService::class.java))
    }

    /** Runs Back, Home or Recents through the accessibility service, when it is on. */
    fun globalAction(name: String): Boolean {
        val svc = AndroFuseA11yService.instance ?: return false
        val action = when (name) {
            "back" -> AccessibilityService.GLOBAL_ACTION_BACK
            "home" -> AccessibilityService.GLOBAL_ACTION_HOME
            "recents" -> AccessibilityService.GLOBAL_ACTION_RECENTS
            else -> return false
        }
        return svc.performGlobalAction(action)
    }

    fun openAccessibilitySettings() {
        val ctx = PluginHost.context
        ctx.startActivity(Intent(android.provider.Settings.ACTION_ACCESSIBILITY_SETTINGS).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    }
}

/** Foreground service holding the MediaProjection, the encoder and the local socket. */
class AndroFuseShareService : Service() {
    companion object {
        @Volatile var running = false
        private const val CHANNEL = "androfuse_share"
        private const val NOTIFICATION_ID = 0x4147
        private const val SOCKET = "androfuse-share"
        private const val CODEC_H264 = 0x68323634
    }

    private var projection: MediaProjection? = null
    private var display: VirtualDisplay? = null
    private var codec: MediaCodec? = null
    private var server: LocalServerSocket? = null
    @Volatile private var out: OutputStream? = null
    @Volatile private var config: ByteArray? = null
    private var width = 0
    private var height = 0
    @Volatile private var stopping = false

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        if (intent?.action == "stop") { stopSelf(); return START_NOT_STICKY }
        if (running) return START_NOT_STICKY
        val nm = getSystemService(NOTIFICATION_SERVICE) as NotificationManager
        if (Build.VERSION.SDK_INT >= 26) {
            nm.createNotificationChannel(NotificationChannel(CHANNEL, "Screen sharing", NotificationManager.IMPORTANCE_LOW))
        }
        val stop = PendingIntent.getService(this, 1, Intent(this, AndroFuseShareService::class.java).setAction("stop"),
            PendingIntent.FLAG_IMMUTABLE)
        val b = if (Build.VERSION.SDK_INT >= 26) Notification.Builder(this, CHANNEL) else @Suppress("DEPRECATION") Notification.Builder(this)
        val n = b.setSmallIcon(android.R.drawable.ic_menu_share)
            .setContentTitle("AndroFuse is sharing this screen")
            .setContentText("Your computer can see this screen")
            .setOngoing(true)
            .addAction(Notification.Action.Builder(null, "Stop", stop).build())
            .build()
        try {
            if (Build.VERSION.SDK_INT >= 29) startForeground(NOTIFICATION_ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_MEDIA_PROJECTION)
            else startForeground(NOTIFICATION_ID, n)
        } catch (e: Exception) {
            stopSelf()
            return START_NOT_STICKY
        }
        val code = intent?.getIntExtra("code", 0) ?: 0
        @Suppress("DEPRECATION")
        val data: Intent? = intent?.getParcelableExtra("data")
        val maxSize = intent?.getIntExtra("maxSize", 1280) ?: 1280
        val bitRate = intent?.getIntExtra("bitRate", 4_000_000) ?: 4_000_000
        val mpm = getSystemService(MEDIA_PROJECTION_SERVICE) as MediaProjectionManager
        val mp = data?.let { mpm.getMediaProjection(code, it) }
        if (mp == null) { stopSelf(); return START_NOT_STICKY }
        running = true
        projection = mp
        mp.registerCallback(object : MediaProjection.Callback() {
            override fun onStop() { stopSelf() }
        }, null)
        startCapture(maxSize, bitRate)
        Thread({ serve() }, "androfuse-share-socket").start()
        return START_NOT_STICKY
    }

    private fun startCapture(maxSize: Int, bitRate: Int) {
        val m = resources.displayMetrics
        val scale = maxSize.toFloat() / max(m.widthPixels, m.heightPixels)
        val s = if (scale < 1f) scale else 1f
        width = ((m.widthPixels * s) / 16f).roundToInt() * 16
        height = ((m.heightPixels * s) / 16f).roundToInt() * 16
        val fmt = MediaFormat.createVideoFormat(MediaFormat.MIMETYPE_VIDEO_AVC, width, height).apply {
            setInteger(MediaFormat.KEY_COLOR_FORMAT, MediaCodecInfo.CodecCapabilities.COLOR_FormatSurface)
            setInteger(MediaFormat.KEY_BIT_RATE, bitRate)
            setInteger(MediaFormat.KEY_FRAME_RATE, 30)
            setInteger(MediaFormat.KEY_I_FRAME_INTERVAL, 2)
            setLong(MediaFormat.KEY_REPEAT_PREVIOUS_FRAME_AFTER, 100_000)
        }
        val c = MediaCodec.createEncoderByType(MediaFormat.MIMETYPE_VIDEO_AVC)
        c.configure(fmt, null, null, MediaCodec.CONFIGURE_FLAG_ENCODE)
        val surface = c.createInputSurface()
        c.start()
        codec = c
        display = projection?.createVirtualDisplay("AndroFuse share", width, height, m.densityDpi,
            DisplayManager.VIRTUAL_DISPLAY_FLAG_AUTO_MIRROR, surface, null, null)
        Thread({ drain(c) }, "androfuse-share-encoder").start()
    }

    /** Copies encoded access units to the connected desktop, if any (frames are dropped otherwise). */
    private fun drain(c: MediaCodec) {
        val info = MediaCodec.BufferInfo()
        while (!stopping) {
            val i = try { c.dequeueOutputBuffer(info, 100_000) } catch (e: Exception) { break }
            if (i < 0) continue
            val buf = c.getOutputBuffer(i)
            if (buf != null && info.size > 0) {
                val bytes = ByteArray(info.size)
                buf.position(info.offset)
                buf.get(bytes)
                val isConfig = info.flags and MediaCodec.BUFFER_FLAG_CODEC_CONFIG != 0
                val isKey = info.flags and MediaCodec.BUFFER_FLAG_KEY_FRAME != 0
                if (isConfig) config = bytes
                write(bytes, info.presentationTimeUs, isConfig, isKey)
            }
            c.releaseOutputBuffer(i, false)
        }
    }

    private fun header(pts: Long, isConfig: Boolean, isKey: Boolean, size: Int): ByteArray {
        var flags = pts and 0x1FFFFFFFFFFFFFFFL
        if (isConfig) flags = flags or (1L shl 62)
        if (isKey) flags = flags or (1L shl 61)
        return ByteBuffer.allocate(12).order(ByteOrder.BIG_ENDIAN).putLong(flags).putInt(size).array()
    }

    @Synchronized private fun write(bytes: ByteArray, pts: Long, isConfig: Boolean, isKey: Boolean) {
        val o = out ?: return
        try {
            o.write(header(pts, isConfig, isKey, bytes.size))
            o.write(bytes)
            o.flush()
        } catch (e: Exception) {
            out = null
        }
    }

    /** Accepts one desktop at a time on the abstract socket and sends it the stream's preamble. */
    private fun serve() {
        val srv = try { LocalServerSocket(SOCKET) } catch (e: Exception) { stopSelf(); return }
        server = srv
        while (!stopping) {
            val s: LocalSocket = try { srv.accept() } catch (e: Exception) { break }
            val o = s.outputStream
            val name = ByteArray(64)
            val model = Build.MODEL.toByteArray(Charsets.UTF_8)
            System.arraycopy(model, 0, name, 0, minOf(model.size, 63))
            val ok = synchronized(this) {
                try {
                    o.write(name)
                    o.write(ByteBuffer.allocate(4).order(ByteOrder.BIG_ENDIAN).putInt(CODEC_H264).array())
                    val session = ByteBuffer.allocate(12).order(ByteOrder.BIG_ENDIAN)
                    session.put(0x80.toByte()).put(0).put(0).put(0).putInt(width).putInt(height)
                    o.write(session.array())
                    config?.let { o.write(header(0, true, false, it.size)); o.write(it) }
                    o.flush()
                    out = o
                    true
                } catch (e: Exception) { false }
            }
            if (!ok) { s.close(); continue }
            codec?.setParameters(Bundle().apply { putInt(MediaCodec.PARAMETER_KEY_REQUEST_SYNC_FRAME, 0) })
        }
    }

    override fun onDestroy() {
        stopping = true
        running = false
        try { server?.close() } catch (e: Exception) {}
        try { out?.close() } catch (e: Exception) {}
        try { display?.release() } catch (e: Exception) {}
        try { codec?.stop(); codec?.release() } catch (e: Exception) {}
        try { projection?.stop() } catch (e: Exception) {}
        super.onDestroy()
    }
}

/**
 * Accessibility service for Back, Home and Recents while the Companion shares the screen. It
 * declares no capabilities (MyGo plugins cannot ship the XML that grants gestures), so it can run
 * global actions only, and only after the user turns it on in Android's accessibility settings.
 */
class AndroFuseA11yService : AccessibilityService() {
    companion object { @Volatile var instance: AndroFuseA11yService? = null }
    override fun onServiceConnected() { instance = this }
    override fun onAccessibilityEvent(event: AccessibilityEvent?) {}
    override fun onInterrupt() {}
    override fun onDestroy() { instance = null; super.onDestroy() }
}
