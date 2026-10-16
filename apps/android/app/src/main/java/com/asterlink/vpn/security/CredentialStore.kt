package com.asterlink.vpn.security

import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import com.asterlink.vpn.session.CredentialStore
import com.asterlink.vpn.session.CredentialStoreException
import java.io.IOException
import java.nio.charset.StandardCharsets
import java.security.GeneralSecurityException
import java.security.KeyStore
import java.security.MessageDigest
import javax.crypto.AEADBadTagException
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.withContext

/**
 * "Remember password" (handoff_app_android.md 5.3). The password is encrypted with AES-256-GCM
 * under a key that lives only in the Android Keystore (never exportable, never in a backup:
 * backup and device transfer are off, and a restored ciphertext could not be decrypted on
 * another device anyway). The ciphertext is bound to its account (API base + email) as GCM
 * associated data, so an entry cannot be replayed under another account. There is no
 * plaintext fallback; nothing here stores a token.
 *
 * Entry names are a SHA-256 of the account, so the preferences file does not list emails.
 */
class KeystoreCredentialStore(
    private val store: DataStore<Preferences>,
    private val io: CoroutineDispatcher = Dispatchers.IO,
) : CredentialStore {
    companion object {
        const val KEY_ALIAS = "com.asterlink.vpn.credentials.v1"
        private const val KEYSTORE = "AndroidKeyStore"
        private const val TRANSFORMATION = "AES/GCM/NoPadding"
        private const val TAG_BITS = 128
        private const val VERSION = "v1"

        fun entryName(account: String): String {
            val digest = MessageDigest.getInstance("SHA-256").digest(account.toByteArray(StandardCharsets.UTF_8))
            return "pw." + digest.joinToString("") { "%02x".format(it) }
        }
    }

    override suspend fun save(account: String, password: String) {
        val sealed = withContext(io) {
            guarded {
                val cipher = Cipher.getInstance(TRANSFORMATION)
                cipher.init(Cipher.ENCRYPT_MODE, key(create = true))
                cipher.updateAAD(account.toByteArray(StandardCharsets.UTF_8))
                val body = cipher.doFinal(password.toByteArray(StandardCharsets.UTF_8))
                VERSION + ":" + encode(cipher.iv) + ":" + encode(body)
            }
        }
        edit { it[stringPreferencesKey(entryName(account))] = sealed }
    }

    override suspend fun read(account: String): String? {
        val name = stringPreferencesKey(entryName(account))
        val sealed = try {
            store.data.first()[name]
        } catch (error: IOException) {
            throw CredentialStoreException("credential storage unreadable", error)
        } ?: return null
        val plain = withContext(io) {
            try {
                val parts = sealed.split(':')
                if (parts.size != 3 || parts[0] != VERSION) return@withContext null
                val key = key(create = false) ?: return@withContext null
                val cipher = Cipher.getInstance(TRANSFORMATION)
                cipher.init(Cipher.DECRYPT_MODE, key, GCMParameterSpec(TAG_BITS, decode(parts[1])))
                cipher.updateAAD(account.toByteArray(StandardCharsets.UTF_8))
                String(cipher.doFinal(decode(parts[2])), StandardCharsets.UTF_8)
            } catch (error: AEADBadTagException) {
                null
            } catch (error: IllegalArgumentException) {
                null
            } catch (error: GeneralSecurityException) {
                throw CredentialStoreException("keystore unavailable", error)
            }
        }
        // An entry that no longer opens (key gone after a reset, or tampered) is dropped,
        // so the user is simply asked for the password again.
        if (plain == null) delete(account)
        return plain
    }

    override suspend fun delete(account: String) {
        edit { it.remove(stringPreferencesKey(entryName(account))) }
    }

    private suspend fun edit(change: (androidx.datastore.preferences.core.MutablePreferences) -> Unit) {
        try {
            store.edit { change(it) }
        } catch (error: IOException) {
            throw CredentialStoreException("credential storage unwritable", error)
        }
    }

    private fun key(create: Boolean): SecretKey? {
        val keyStore = KeyStore.getInstance(KEYSTORE).apply { load(null) }
        (keyStore.getKey(KEY_ALIAS, null) as? SecretKey)?.let { return it }
        if (!create) return null
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, KEYSTORE)
        generator.init(
            KeyGenParameterSpec.Builder(KEY_ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .setRandomizedEncryptionRequired(true)
                .build(),
        )
        return generator.generateKey()
    }

    private inline fun <T> guarded(block: () -> T): T = try {
        block()
    } catch (error: GeneralSecurityException) {
        throw CredentialStoreException("keystore unavailable", error)
    } catch (error: java.security.ProviderException) {
        throw CredentialStoreException("keystore unavailable", error)
    }

    private fun encode(bytes: ByteArray) = Base64.encodeToString(bytes, Base64.NO_WRAP)

    private fun decode(text: String): ByteArray = Base64.decode(text, Base64.NO_WRAP)
}
