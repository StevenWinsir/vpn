using System.Globalization;

namespace AsterLink.Shared.Client;

/// <summary>Display helpers. Traffic is shown in GB like the website: the base is 1024 (handoff D7), only the unit name
/// changed. Integers never go through double for accounting.</summary>
public static class Formatting
{
    private static readonly string[] Units = ["B", "KB", "MB", "GB", "TB"];

    public static string Bytes(long value)
    {
        var amount = (double)Math.Max(0, value);
        var index = 0;
        while (amount >= 1024 && index < Units.Length - 1) { amount /= 1024; index++; }
        return index == 0
            ? ((long)amount).ToString(CultureInfo.InvariantCulture) + " B"
            : amount.ToString("0.00", CultureInfo.InvariantCulture) + " " + Units[index];
    }

    public static string Speed(long bytesPerSecond) => Bytes(bytesPerSecond) + "/s";

    public static string Date(DateTimeOffset? value) =>
        value is null ? "—" : value.Value.ToLocalTime().ToString("yyyy-MM-dd", CultureInfo.InvariantCulture);

    /// <summary>Bytes counted against the plan for <paramref name="physical"/> bytes at a line rate; the Core's formula, integers only, no overflow for any long.</summary>
    public static long BilledBytes(long physical, long ratePermille)
    {
        if (physical <= 0 || ratePermille <= 0) return 0;
        var billed = (Int128)(physical / 1000) * ratePermille + (Int128)(physical % 1000) * ratePermille / 1000;
        return billed > long.MaxValue ? long.MaxValue : (long)billed;
    }

    /// <summary>500‰ → "0.5×", 1000‰ → "1×". Pure integer arithmetic.</summary>
    public static string Rate(long permille)
    {
        var whole = permille / 1000;
        var fraction = permille % 1000;
        if (fraction == 0) return whole.ToString(CultureInfo.InvariantCulture) + "×";
        var digits = fraction.ToString("D3", CultureInfo.InvariantCulture).TrimEnd('0');
        return whole.ToString(CultureInfo.InvariantCulture) + "." + digits + "×";
    }

    /// <summary>Whole days (rounded up) until <paramref name="expires"/>; null when there is no date; 0 when already past.</summary>
    public static int? DaysLeft(DateTimeOffset? expires, DateTimeOffset now)
    {
        if (expires is null) return null;
        var left = expires.Value - now;
        return left <= TimeSpan.Zero ? 0 : (int)Math.Ceiling(left.TotalDays);
    }

    /// <summary>Fraction of the plan already used, 0..1, from integer byte counts.</summary>
    public static double UsedFraction(long totalBytes, long remainingBytes)
    {
        if (totalBytes <= 0) return 0;
        return Math.Clamp(1.0 - (double)remainingBytes / totalBytes, 0, 1);
    }
}
