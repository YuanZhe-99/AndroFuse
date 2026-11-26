package com.github.yuanzhe_99.androfuse.internal.androidcore

import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import dev.mygo.*
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

// The implementation of package androidcore on Android: loads the Rust core that this module's
// Android library carries in src/main/jniLibs/<abi>/, and seals secrets with Android Keystore.

fun makeLibrary(): Library = CoreLibrary()

fun makeVault(): Vault = KeystoreVault()

private class CoreLibrary : Library {
    override fun load() {
        try {
            System.loadLibrary("androfuse_core")
        } catch (e: UnsatisfiedLinkError) {
            throw NativeUnsupportedException()
        }
    }
}

private class KeystoreVault : Vault {
    private val alias = "androfuse_vault"

    private fun key(): SecretKey {
        val ks = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        (ks.getKey(alias, null) as? SecretKey)?.let { return it }
        val gen = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore")
        gen.init(
            KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .build()
        )
        return gen.generateKey()
    }

    override fun seal(plain: ByteArray): ByteArray {
        val c = Cipher.getInstance("AES/GCM/NoPadding")
        c.init(Cipher.ENCRYPT_MODE, key())
        return c.iv + c.doFinal(plain)
    }

    override fun open(sealed: ByteArray): ByteArray {
        if (sealed.size < 13) throw NativeException("vault", 1, "sealed data too short")
        val c = Cipher.getInstance("AES/GCM/NoPadding")
        c.init(Cipher.DECRYPT_MODE, key(), GCMParameterSpec(128, sealed.copyOfRange(0, 12)))
        return c.doFinal(sealed, 12, sealed.size - 12)
    }
}
