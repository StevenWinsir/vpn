package com.asterlink.vpn.security

import android.content.Context
import androidx.datastore.core.DataStore
import androidx.datastore.preferences.core.Preferences
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import com.asterlink.vpn.session.LoginPreferences
import java.io.IOException
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.catch
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map

/** App-private DataStore files. Both are excluded from backup and device transfer. */
val Context.credentialStore: DataStore<Preferences> by preferencesDataStore(name = "credentials")
val Context.settingsStore: DataStore<Preferences> by preferencesDataStore(name = "settings")

enum class ThemeMode { System, Light, Dark }

/** The remembered email and the appearance choice. Never a password or token. */
class AppPreferences(private val store: DataStore<Preferences>) : LoginPreferences {
    private val email = stringPreferencesKey("remembered_email")
    private val theme = stringPreferencesKey("theme_mode")

    override suspend fun rememberedEmail(): String? = safeData().first()[email]?.takeIf { it.isNotBlank() }

    /** Best effort: a prefilled email is a convenience, never a reason to fail a sign-in. */
    override suspend fun setRememberedEmail(email: String?) {
        try {
            store.edit { if (email.isNullOrBlank()) it.remove(this.email) else it[this.email] = email }
        } catch (error: IOException) {
            // Next launch shows an empty email field.
        }
    }

    val themeMode: Flow<ThemeMode> = safeData().map { prefs ->
        ThemeMode.entries.firstOrNull { it.name == prefs[theme] } ?: ThemeMode.System
    }

    suspend fun setThemeMode(mode: ThemeMode) {
        store.edit { it[theme] = mode.name }
    }

    private fun safeData(): Flow<Preferences> = store.data.catch { error ->
        if (error is IOException) emit(androidx.datastore.preferences.core.emptyPreferences()) else throw error
    }
}
