package com.asterlink.vpn

import androidx.datastore.preferences.core.PreferenceDataStoreFactory
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import com.asterlink.vpn.security.FileDeviceIdentity
import com.asterlink.vpn.security.KeystoreCredentialStore
import com.asterlink.vpn.session.CredentialAccount
import java.io.File
import java.security.KeyStore
import java.util.UUID
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith

/**
 * A01 on the device: the remembered password really is AES-GCM under an Android Keystore key,
 * scoped to API base + email, never stored in the clear, and a tampered or orphaned entry is
 * dropped instead of being trusted. The device id is a stable random UUID.
 */
@RunWith(AndroidJUnit4::class)
class SecurityStoreTest {
    private val context = InstrumentationRegistry.getInstrumentation().targetContext
    private lateinit var dir: File
    private lateinit var scope: CoroutineScope

    @Before
    fun setUp() {
        dir = File(context.cacheDir, "security-test-" + UUID.randomUUID()).apply { mkdirs() }
        scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    }

    @After
    fun tearDown() {
        scope.cancel()
        dir.deleteRecursively()
    }

    private fun store() = PreferenceDataStoreFactory.create(scope = scope) { File(dir, "credentials.preferences_pb") }

    @Test
    fun passwordsRoundTripEncryptedAndScopedToTheirAccount() = runBlocking {
        val data = store()
        val credentials = KeystoreCredentialStore(data)
        val password = "Fixture-Only-Password!密"
        val account = CredentialAccount.key("https://api.example.invalid/api/v1/client", "User@Example.invalid")
        val other = CredentialAccount.key("https://other.example.invalid/api/v1/client", "user@example.invalid")
        credentials.save(account, password)
        assertEquals(password, credentials.read(account))
        assertNull(credentials.read(other))

        // Nothing readable on disk: no password, no email.
        val raw = data.data.first().asMap().values.joinToString()
        assertFalse(raw.contains(password))
        assertFalse(raw.contains("example.invalid"))
        assertFalse(File(dir, "credentials.preferences_pb").readBytes().toString(Charsets.ISO_8859_1).contains("Fixture-Only"))

        // The key lives in the Keystore (non-exportable by construction).
        val keyStore = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        assertTrue(keyStore.containsAlias(KeystoreCredentialStore.KEY_ALIAS))

        credentials.delete(account)
        assertNull(credentials.read(account))
    }

    @Test
    fun aCiphertextMovedToAnotherAccountOrTamperedWithIsDropped() = runBlocking {
        val data = store()
        val credentials = KeystoreCredentialStore(data)
        val a = CredentialAccount.key("https://api.example.invalid/api/v1/client", "a@example.invalid")
        val b = CredentialAccount.key("https://api.example.invalid/api/v1/client", "b@example.invalid")
        credentials.save(a, "secret-a")
        val sealed = data.data.first()[stringPreferencesKey(KeystoreCredentialStore.entryName(a))]!!
        // Replay a's ciphertext under b: the GCM associated data (the account) does not match.
        data.edit { it[stringPreferencesKey(KeystoreCredentialStore.entryName(b))] = sealed }
        assertNull(credentials.read(b))
        assertNull(data.data.first()[stringPreferencesKey(KeystoreCredentialStore.entryName(b))])
        // Flip one byte of the body.
        val parts = sealed.split(':')
        val body = android.util.Base64.decode(parts[2], android.util.Base64.NO_WRAP).also { it[0] = (it[0].toInt() xor 1).toByte() }
        data.edit { it[stringPreferencesKey(KeystoreCredentialStore.entryName(a))] = parts[0] + ":" + parts[1] + ":" + android.util.Base64.encodeToString(body, android.util.Base64.NO_WRAP) }
        assertNull(credentials.read(a))
    }

    @Test
    fun theDeviceIdIsAStableRandomUuid() = runBlocking {
        val first = FileDeviceIdentity(dir).deviceId()
        assertTrue(FileDeviceIdentity.isValid(first))
        assertEquals(first, FileDeviceIdentity(dir).deviceId())
        File(dir, "device_id").writeText("not-a-uuid")
        val replaced = FileDeviceIdentity(dir).deviceId()
        assertTrue(FileDeviceIdentity.isValid(replaced))
        assertNotEquals(first, replaced)
    }
}
