package com.asterlink.vpn.session

import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

/**
 * Display helpers. Traffic is shown in GB (1 GB = 2^30 bytes, like the website); accounting
 * integers never go through a Double (only the final display rounding does).
 */
object Formatting {
    private val UNITS = arrayOf("B", "KB", "MB", "GB", "TB", "PB")

    fun bytes(value: Long): String {
        var amount = value.coerceAtLeast(0).toDouble()
        var index = 0
        while (amount >= 1024 && index < UNITS.lastIndex) {
            amount /= 1024
            index++
        }
        return if (index == 0) "${value.coerceAtLeast(0)} B" else String.format(Locale.ROOT, "%.2f %s", amount, UNITS[index])
    }

    fun speed(bytesPerSecond: Long): String = bytes(bytesPerSecond) + "/s"

    /** 500‰ → "0.5×", 1000‰ → "1×", 1250‰ → "1.25×". Pure integer arithmetic. */
    fun rate(permille: Long): String {
        val whole = permille / 1000
        val fraction = permille % 1000
        if (fraction == 0L) return "$whole×"
        return "$whole." + String.format(Locale.ROOT, "%03d", fraction).trimEnd('0') + "×"
    }

    /**
     * Physical bytes at a line's rate, in bytes (units / 1000, rounded down like the server's
     * remaining_bytes). Split so the product cannot overflow for any realistic counter.
     */
    fun billedBytes(physical: Long, ratePermille: Long): Long {
        if (physical <= 0 || ratePermille <= 0) return 0
        return (physical / 1000) * ratePermille + (physical % 1000) * ratePermille / 1000
    }

    private val DATE =DateTimeFormatter.ofPattern("yyyy-MM-dd", Locale.ROOT)
    private val DATE_TIME = DateTimeFormatter.ofPattern("yyyy-MM-dd HH:mm", Locale.ROOT)

    fun date(value: Instant?, zone: ZoneId = ZoneId.systemDefault()): String = value?.atZone(zone)?.format(DATE) ?: "—"

    fun dateTime(value: Instant?, zone: ZoneId = ZoneId.systemDefault()): String = value?.atZone(zone)?.format(DATE_TIME) ?: "—"

    /** Whole days (rounded up) until [expires]; null without a date; 0 when already past. */
    fun daysLeft(expires: Instant?, now: Instant): Long? {
        if (expires == null) return null
        val seconds = expires.epochSecond - now.epochSecond
        return if (seconds <= 0) 0 else (seconds + 86_399) / 86_400
    }

    /** Fraction of the plan already used, 0..1, from integer byte counts. */
    fun usedFraction(totalBytes: Long, remainingBytes: Long): Float {
        if (totalBytes <= 0) return 0f
        return (1.0 - remainingBytes.toDouble() / totalBytes.toDouble()).coerceIn(0.0, 1.0).toFloat()
    }

    /** 00:12:31, or 1:02:03:04 past a day. */
    fun elapsed(millis: Long): String {
        val total = (millis / 1000).coerceAtLeast(0)
        val days = total / 86_400
        val hours = total % 86_400 / 3600
        val minutes = total % 3600 / 60
        val seconds = total % 60
        val clock = String.format(Locale.ROOT, "%02d:%02d:%02d", hours, minutes, seconds)
        return if (days > 0) "$days:$clock" else clock
    }

    /** The avatar letter: the first letter or digit of the name or email. */
    fun initial(name: String, email: String): String {
        val source = name.trim().ifEmpty { email.trim() }
        val first = source.firstOrNull { it.isLetterOrDigit() } ?: return "?"
        return first.uppercaseChar().toString()
    }
}
