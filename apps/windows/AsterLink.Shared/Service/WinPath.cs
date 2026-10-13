namespace AsterLink.Shared.Service;

/// <summary>
/// Lexical Windows path checks that run (and are tested) on any OS. They are the first line
/// of the protected-path policy; the second line (reparse points, ACLs) needs the real file
/// system and lives in AsterLink.Platform.
/// </summary>
public static class WinPath
{
    private const string InvalidChars = "*?\"<>|";

    /// <summary>
    /// True for a plain drive-absolute path (<c>C:\dir\file</c>). Rejects UNC, device and
    /// extended prefixes, alternate data streams, dot segments, control characters and
    /// segments Windows would silently rewrite (trailing space or dot).
    /// </summary>
    public static bool IsSafeAbsolute(string? path)
    {
        if (string.IsNullOrEmpty(path) || path.Length > 259) return false;
        if (path.Length < 3 || !char.IsAsciiLetter(path[0]) || path[1] != ':' || path[2] != '\\') return false;

        foreach (var c in path)
            if (c < 0x20 || InvalidChars.Contains(c) || c == '/') return false;
        if (path.IndexOf(':', 2) >= 0) return false; // alternate data stream or second drive colon

        var rest = path[3..];
        if (rest.EndsWith('\\')) rest = rest[..^1]; // one trailing separator is just a directory spelling
        if (rest.Length == 0) return true;
        foreach (var segment in rest.Split('\\'))
        {
            if (segment.Length == 0) return false;      // doubled or trailing separator
            if (segment is "." or "..") return false;
            if (segment[^1] is ' ' or '.') return false; // Windows strips these: a different file than named
            if (IsReservedDeviceName(segment)) return false;
        }
        return true;
    }

    /// <summary>Removes trailing separators; the input must already satisfy <see cref="IsSafeAbsolute"/>.</summary>
    public static string Trim(string path) => path.Length > 3 ? path.TrimEnd('\\') : path;

    public static bool Equal(string? a, string? b) =>
        a is not null && b is not null && IsSafeAbsolute(a) && IsSafeAbsolute(b)
        && string.Equals(Trim(a), Trim(b), StringComparison.OrdinalIgnoreCase);

    /// <summary>True when <paramref name="path"/> is strictly inside <paramref name="root"/>.</summary>
    public static bool IsUnder(string root, string? path)
    {
        if (path is null || !IsSafeAbsolute(root) || !IsSafeAbsolute(path)) return false;
        var prefix = Trim(root);
        var candidate = Trim(path);
        return candidate.Length > prefix.Length
            && candidate.StartsWith(prefix, StringComparison.OrdinalIgnoreCase)
            && candidate[prefix.Length] == '\\';
    }

    /// <summary>A path that is relative to an install root and uses forward slashes (manifest form).</summary>
    public static bool IsSafeRelative(string? path)
    {
        if (string.IsNullOrEmpty(path) || path.Length > 200) return false;
        if (path[0] == '/' || path.Contains('\\') || path.Contains(':')) return false;
        foreach (var c in path)
            if (c < 0x20 || InvalidChars.Contains(c)) return false;
        foreach (var segment in path.Split('/'))
        {
            if (segment.Length == 0 || segment is "." or "..") return false;
            if (segment[^1] is ' ' or '.') return false;
            if (IsReservedDeviceName(segment)) return false;
        }
        return true;
    }

    private static readonly string[] Reserved =
    [
        "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$",
        "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
        "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9",
    ];

    private static bool IsReservedDeviceName(string segment)
    {
        var dot = segment.IndexOf('.');
        var stem = dot < 0 ? segment : segment[..dot];
        return Reserved.Contains(stem, StringComparer.OrdinalIgnoreCase);
    }
}
