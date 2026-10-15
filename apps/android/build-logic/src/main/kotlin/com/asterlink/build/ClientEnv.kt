package com.asterlink.build

import java.io.File
import java.net.URI
import java.net.URISyntaxException

/**
 * Strict parser for apps/android/.env: public build parameters only.
 *
 * Mirrors scripts/native_env.py (and the Swift and C# parsers); all of them are pinned to
 * shared/contracts/env-v1.json. It is a parser, never `source .env`: unknown or duplicate
 * keys, shell syntax, quotes, credentials or queries in URLs and plain HTTP to anything
 * but a loopback host in development are refused.
 */
data class ClientEnv(val channel: String, val apiBaseUrl: String, val websiteBaseUrl: String) {
    class Invalid(message: String) : IllegalArgumentException(message)

    companion object {
        val ALLOWED = listOf("APP_ENV", "API_BASE_URL", "WEBSITE_BASE_URL")
        const val API_PATH = "/api/v1/client"
        private const val UNSAFE = "\"'$`\\;|&<>(){}*!"
        private val LOOPBACK = setOf("localhost", "127.0.0.1", "::1")
        private const val MAX_FILE_BYTES = 8192L

        fun parse(text: String): ClientEnv {
            val values = linkedMapOf<String, String>()
            text.split('\n').forEachIndexed { index, raw ->
                val line = raw.trimEnd('\r')
                if (line.isEmpty() || line.startsWith("#")) return@forEachIndexed
                val separator = line.indexOf('=')
                if (separator < 0) throw Invalid("malformed line ${index + 1}")
                val key = line.substring(0, separator)
                val value = line.substring(separator + 1)
                if (key !in ALLOWED) throw Invalid("unknown key $key")
                if (key in values) throw Invalid("duplicate key $key")
                if (!safe(value)) throw Invalid("unsafe value for $key")
                values[key] = value
            }
            for (key in ALLOWED) if (key !in values) throw Invalid("missing key $key")
            val channel = values.getValue("APP_ENV")
            if (channel != "development" && channel != "release") throw Invalid("invalid APP_ENV")
            val api = values.getValue("API_BASE_URL")
            val website = values.getValue("WEBSITE_BASE_URL")
            url(api, "API_BASE_URL", channel, API_PATH)
            url(website, "WEBSITE_BASE_URL", channel, null)
            return ClientEnv(channel, api, website)
        }

        fun load(file: File): ClientEnv {
            val path = file.toPath()
            if (java.nio.file.Files.isSymbolicLink(path) || !file.isFile) {
                throw Invalid("env file must be a regular, non-symlink file: ${file.name}")
            }
            if (file.length() > MAX_FILE_BYTES) throw Invalid("env file too large")
            return parse(file.readText(Charsets.UTF_8))
        }

        private fun safe(value: String): Boolean =
            value.isNotEmpty() && value.all { it.code in 0x21..0x7e && it !in UNSAFE }

        private fun url(value: String, key: String, channel: String, requiredPath: String?) {
            val uri = try {
                URI(value)
            } catch (error: URISyntaxException) {
                throw Invalid("invalid URL for $key")
            }
            val host = uri.host?.removePrefix("[")?.removeSuffix("]")?.lowercase()
            if (host.isNullOrEmpty() || uri.rawUserInfo != null || !uri.rawQuery.isNullOrEmpty() ||
                !uri.rawFragment.isNullOrEmpty() || uri.port !in -1..65535
            ) {
                throw Invalid("invalid URL for $key")
            }
            val scheme = uri.scheme?.lowercase()
            if (!(scheme == "https" || (scheme == "http" && channel == "development" && host in LOOPBACK))) {
                throw Invalid("invalid URL scheme/host for $key")
            }
            val path = uri.rawPath.orEmpty()
            if (requiredPath != null) {
                if (path != requiredPath) throw Invalid("invalid path for $key")
            } else if (path != "" && path != "/") {
                throw Invalid("invalid path for $key")
            }
        }
    }
}
