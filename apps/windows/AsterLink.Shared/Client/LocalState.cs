using System.Text.Json;

namespace AsterLink.Shared.Client;

/// <summary>
/// Non-secret per-user state under a directory the user owns (<c>%LocalAppData%\AsterLink</c>): the last
/// email, the theme, and a "told about the tray" flag. A corrupt file is treated as empty, never as a reason to fail.
/// The password is never here (it lives only in the Windows credential vault).
/// </summary>
public sealed class FileLoginPreferences : ILoginPreferences
{
    public const string FileName = "settings.json";
    private const long MaxBytes = 16 * 1024;
    private static readonly string[] Themes = ["system", "light", "dark"];

    private readonly string path;
    private readonly object gate = new();
    private Stored state;

    public FileLoginPreferences(string directory)
    {
        path = Path.Combine(directory, FileName);
        state = Load();
    }

    private sealed class Stored
    {
        public string? RememberedEmail { get; set; }
        public string Theme { get; set; } = "system";
        public bool TrayNoticeShown { get; set; }
    }

    public string? RememberedEmail
    {
        get { lock (gate) return state.RememberedEmail; }
        set { lock (gate) { state.RememberedEmail = string.IsNullOrWhiteSpace(value) ? null : value; Save(); } }
    }

    public string Theme
    {
        get { lock (gate) return Themes.Contains(state.Theme) ? state.Theme : "system"; }
        set { lock (gate) { state.Theme = Themes.Contains(value) ? value : "system"; Save(); } }
    }

    public bool TrayNoticeShown
    {
        get { lock (gate) return state.TrayNoticeShown; }
        set { lock (gate) { state.TrayNoticeShown = value; Save(); } }
    }

    private static readonly JsonSerializerOptions Json = new()
    {
        PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower,
        WriteIndented = true,
    };

    private Stored Load()
    {
        try
        {
            var info = new FileInfo(path);
            if (!info.Exists || info.Length > MaxBytes || info.LinkTarget is not null) return new Stored();
            return JsonSerializer.Deserialize<Stored>(File.ReadAllText(path), Json) ?? new Stored();
        }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException or JsonException)
        {
            return new Stored();
        }
    }

    private void Save()
    {
        try { AtomicFile.Write(path, JsonSerializer.SerializeToUtf8Bytes(state, Json)); }
        catch (Exception error) when (error is IOException or UnauthorizedAccessException)
        {
            // Preferences are a convenience: losing one must not break sign-in.
        }
    }
}

/// <summary>
/// A random UUID created once per Windows user and kept in that user's profile. It is not derived from
/// hardware (no MAC address), and an upgrade does not change it.
/// </summary>
public sealed class FileDeviceIdentity(string directory) : IDeviceIdentity
{
    public const string FileName = "device-id";
    private readonly object gate = new();
    private string? cached;

    public string DeviceId()
    {
        lock (gate)
        {
            if (cached is not null) return cached;
            var path = Path.Combine(directory, FileName);
            try
            {
                var info = new FileInfo(path);
                if (info.Exists && info.Length <= 64 && info.LinkTarget is null)
                {
                    var text = File.ReadAllText(path).Trim().ToLowerInvariant();
                    if (DeviceIds.IsValid(text)) return cached = text;
                }
            }
            catch (Exception error) when (error is IOException or UnauthorizedAccessException) { }

            var fresh = DeviceIds.Generate();
            // If it cannot be persisted the id still works for this run; the next run would mint another, so say nothing is stored.
            try { AtomicFile.Write(path, System.Text.Encoding.ASCII.GetBytes(fresh)); }
            catch (Exception error) when (error is IOException or UnauthorizedAccessException) { }
            return cached = fresh;
        }
    }
}

internal static class AtomicFile
{
    /// <summary>Write to a temporary file in the same directory, flush it to disk, then replace: a crash leaves the old file or the new one, never half of one.</summary>
    public static void Write(string path, byte[] content)
    {
        var directory = Path.GetDirectoryName(path)!;
        Directory.CreateDirectory(directory);
        var temporary = path + "." + Environment.ProcessId + ".tmp";
        using (var stream = new FileStream(temporary, FileMode.Create, FileAccess.Write, FileShare.None))
        {
            stream.Write(content);
            stream.Flush(flushToDisk: true);
        }
        File.Move(temporary, path, overwrite: true);
    }
}
