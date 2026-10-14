using System.Globalization;

namespace AsterLink.Shared.Client;

/// <summary>Display helpers. Traffic uses binary units (1 GiB = 2^30 bytes) and says so; integers never go through double for accounting.</summary>
public static class Formatting
{
    private static readonly string[] Units = ["B", "KiB", "MiB", "GiB", "TiB"];

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
        value is null ? "—" : value.Value.ToLocalTime().ToString("yyyy-MM-dd HH:mm", CultureInfo.InvariantCulture);

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
