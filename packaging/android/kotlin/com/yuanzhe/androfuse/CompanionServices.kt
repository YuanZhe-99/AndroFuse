package com.yuanzhe.androfuse

import android.app.Activity
import android.app.Notification
import android.content.Context
import android.content.Intent
import android.media.projection.MediaProjectionManager
import android.os.Build
import android.service.notification.NotificationListenerService
import android.service.notification.StatusBarNotification
import android.service.quicksettings.Tile
import android.service.quicksettings.TileService

/**
 * Mirrors non-sensitive notification summaries when user-enabled in Android settings.
 * Respects OS redaction and ignores FLAG_SECURE / private lock-screen metadata.
 */
class AndroFuseNotificationService : NotificationListenerService() {
    override fun onListenerConnected() {
        super.onListenerConnected()
        CompanionBridgeRegistry.notificationListenerGranted = true
    }

    override fun onListenerDisconnected() {
        CompanionBridgeRegistry.notificationListenerGranted = false
        super.onListenerDisconnected()
    }

    override fun onNotificationPosted(sbn: StatusBarNotification?) {
        if (sbn == null) return
        val notif = sbn.notification ?: return
        if (notif.visibility == Notification.VISIBILITY_SECRET) {
            return
        }
        val extras = notif.extras
        val title = extras?.getCharSequence(Notification.EXTRA_TITLE)?.toString().orEmpty()
        if (title.isNotEmpty()) {
            CompanionBridgeRegistry.lastPackage = sbn.packageName
        }
    }
}

/**
 * Quick Settings tile allowing one-tap launch into the AndroFuse Companion host.
 */
class AndroFuseTileService : TileService() {
    override fun onStartListening() {
        super.onStartListening()
        CompanionBridgeRegistry.quickTileReady = true
        qsTile?.apply {
            state = if (CompanionBridgeRegistry.companionActive) Tile.STATE_ACTIVE else Tile.STATE_INACTIVE
            updateTile()
        }
    }

    override fun onClick() {
        super.onClick()
        val launchIntent = packageManager.getLaunchIntentForPackage(packageName)?.apply {
            addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        }
        if (launchIntent != null) {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE) {
                startActivityAndCollapse(
                    android.app.PendingIntent.getActivity(
                        this,
                        0,
                        launchIntent,
                        android.app.PendingIntent.FLAG_IMMUTABLE,
                    ),
                )
            } else {
                @Suppress("DEPRECATION")
                startActivityAndCollapse(launchIntent)
            }
        }
    }
}

/**
 * Standard Android OS consent intents for MediaProjection and Storage Access Framework (SAF).
 */
object AndroFuseProjectionContract {
    fun createMediaProjectionConsentIntent(context: Context): Intent? {
        val mgr = context.getSystemService(Context.MEDIA_PROJECTION_SERVICE) as? MediaProjectionManager
        return mgr?.createScreenCaptureIntent()
    }

    fun createDocumentTreeIntent(): Intent {
        return Intent(Intent.ACTION_OPEN_DOCUMENT_TREE).apply {
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_PERSISTABLE_URI_PERMISSION)
        }
    }

    fun markMediaProjectionResult(resultCode: Int) {
        CompanionBridgeRegistry.mediaProjectionGranted = resultCode == Activity.RESULT_OK
    }
}

internal object CompanionBridgeRegistry {
    @Volatile var notificationListenerGranted: Boolean = false
    @Volatile var quickTileReady: Boolean = false
    @Volatile var mediaProjectionGranted: Boolean = false
    @Volatile var safTreeGranted: Boolean = false
    @Volatile var companionActive: Boolean = false
    @Volatile var lastPackage: String = ""
}
