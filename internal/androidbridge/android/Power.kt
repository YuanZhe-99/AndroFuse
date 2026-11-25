// Companion power control for AndroFuse (PLAN §9.1-3), compiled into the app by `mygo build`
// from internal/androidbridge/mygo-plugin.json. Go reaches it as mygo.Native("androfusePower").
//
// Rules it enforces:
//  - Standby (a foreground service that keeps the Companion reachable in the background) runs only
//    while the user has switched it on, behind a minimum-importance notification.
//  - The partial wake lock and Wi-Fi lock are an "activity lease" held only while a transfer is
//    active; each acquire carries a hard timeout so a lost release can never drain the battery.
//  - Network changes arrive as events (ConnectivityManager.NetworkCallback), never by polling.

package dev.mygo

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.ServiceInfo
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.wifi.WifiManager
import android.os.Build
import android.os.IBinder
import android.os.PowerManager
import org.json.JSONObject

/** Plugin entry point; methods run on the UI thread. */
class AndroFusePowerPlugin : MyGoPlugin {
    private var wakeLock: PowerManager.WakeLock? = null
    private var wifiLock: WifiManager.WifiLock? = null
    private var netCallback: ConnectivityManager.NetworkCallback? = null

    override fun call(method: String, args: JSONObject, reply: PluginReply) {
        val ctx = PluginHost.context
        when (method) {
            "startStandby" -> {
                val intent = Intent(ctx, AndroFuseStandbyService::class.java)
                    .putExtra("title", args.optString("title", "AndroFuse"))
                    .putExtra("text", args.optString("text", ""))
                if (Build.VERSION.SDK_INT >= 26) ctx.startForegroundService(intent) else ctx.startService(intent)
                reply.success(true)
            }
            "stopStandby" -> {
                ctx.stopService(Intent(ctx, AndroFuseStandbyService::class.java))
                reply.success(true)
            }
            "acquireActive" -> {
                val timeoutMs = args.optLong("timeoutMs", 120_000L).coerceIn(1_000L, 600_000L)
                acquireActive(ctx, timeoutMs)
                reply.success(true)
            }
            "releaseActive" -> {
                releaseActive()
                reply.success(true)
            }
            "watchNetwork" -> {
                watchNetwork(ctx)
                reply.success(currentNetwork(ctx))
            }
            "state" -> {
                val pm = ctx.getSystemService(Context.POWER_SERVICE) as PowerManager
                reply.success(JSONObject()
                    .put("interactive", pm.isInteractive)
                    .put("powerSave", pm.isPowerSaveMode)
                    .put("activeHeld", wakeLock?.isHeld == true)
                    .put("standby", AndroFuseStandbyService.running)
                    .put("network", currentNetwork(ctx)))
            }
            else -> reply.failure(PluginFailure("unknown_method", method))
        }
    }

    private fun acquireActive(ctx: Context, timeoutMs: Long) {
        val pm = ctx.getSystemService(Context.POWER_SERVICE) as PowerManager
        val wl = wakeLock ?: pm.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "AndroFuse:transfer")
            .also { it.setReferenceCounted(false); wakeLock = it }
        wl.acquire(timeoutMs) // renews the timeout when already held

        val wm = ctx.applicationContext.getSystemService(Context.WIFI_SERVICE) as WifiManager
        val mode = if (Build.VERSION.SDK_INT >= 29) WifiManager.WIFI_MODE_FULL_LOW_LATENCY
            else @Suppress("DEPRECATION") WifiManager.WIFI_MODE_FULL_HIGH_PERF
        val fl = wifiLock ?: wm.createWifiLock(mode, "AndroFuse:transfer")
            .also { it.setReferenceCounted(false); wifiLock = it }
        if (!fl.isHeld) fl.acquire()
    }

    private fun releaseActive() {
        wakeLock?.let { if (it.isHeld) it.release() }
        wifiLock?.let { if (it.isHeld) it.release() }
    }

    private fun watchNetwork(ctx: Context) {
        if (netCallback != null) return
        val cm = ctx.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
        val cb = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) = PluginHost.emit("androfusePower", "network", currentNetwork(ctx))
            override fun onLost(network: Network) = PluginHost.emit("androfusePower", "network", currentNetwork(ctx))
            override fun onCapabilitiesChanged(network: Network, caps: NetworkCapabilities) =
                PluginHost.emit("androfusePower", "network", currentNetwork(ctx))
        }
        cm.registerDefaultNetworkCallback(cb)
        netCallback = cb
    }

    private fun currentNetwork(ctx: Context): String {
        val cm = ctx.getSystemService(Context.CONNECTIVITY_SERVICE) as ConnectivityManager
        val caps = cm.getNetworkCapabilities(cm.activeNetwork) ?: return "none"
        return when {
            caps.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) -> "wifi"
            caps.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET) -> "ethernet"
            caps.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) -> "cellular"
            else -> "other"
        }
    }
}

/** Opt-in foreground service that keeps the Companion process alive for background standby. */
class AndroFuseStandbyService : Service() {
    companion object {
        @Volatile var running = false
        private const val CHANNEL = "androfuse_standby"
        private const val NOTIFICATION_ID = 0x4146
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        val nm = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        if (Build.VERSION.SDK_INT >= 26 && nm.getNotificationChannel(CHANNEL) == null) {
            nm.createNotificationChannel(
                NotificationChannel(CHANNEL, "AndroFuse standby", NotificationManager.IMPORTANCE_MIN)
                    .apply { setShowBadge(false) })
        }
        val builder = if (Build.VERSION.SDK_INT >= 26) Notification.Builder(this, CHANNEL)
            else @Suppress("DEPRECATION") Notification.Builder(this)
        val n = builder
            .setSmallIcon(applicationInfo.icon)
            .setContentTitle(intent?.getStringExtra("title") ?: "AndroFuse")
            .setContentText(intent?.getStringExtra("text") ?: "")
            .setOngoing(true)
            .build()
        // Android 14+ requires FOREGROUND_SERVICE_CONNECTED_DEVICE plus a connectivity permission
        // (CHANGE_NETWORK_STATE, declared in mygo-plugin.json) for this type, and may refuse a
        // background start: never let a refusal crash the Companion, just stay out of standby.
        try {
            if (Build.VERSION.SDK_INT >= 29) {
                startForeground(NOTIFICATION_ID, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_CONNECTED_DEVICE)
            } else {
                startForeground(NOTIFICATION_ID, n)
            }
        } catch (e: RuntimeException) {
            android.util.Log.w("AndroFuseStandby", "foreground start refused: ${e.message}")
            running = false
            stopSelf()
            return START_NOT_STICKY
        }
        running = true
        // Not sticky: if the system reclaims the process the user reopens the app, rather than the
        // service silently restarting itself in the background.
        return START_NOT_STICKY
    }

    override fun onDestroy() {
        running = false
        super.onDestroy()
    }
}
