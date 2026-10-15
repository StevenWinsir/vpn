namespace AsterLink.Shared.Client;

public sealed record UserCleanupResult(int PasswordsRemoved, bool PreferencesRemoved, string? Error);

/// <summary>
/// The per-user half of uninstalling: saved passwords (Credential Manager) and the preference folder under
/// %LocalAppData%. It runs as the ordinary user (the uninstaller starts it with <c>--cleanup-user-data</c> as the
/// original, non-elevated user), because an elevated uninstaller cannot and must not reach other users' vaults.
/// Nothing here is automatic: the caller passes exactly what the user chose.
/// </summary>
public static class UserDataCleanup
{
    public static string DefaultPreferencesDirectory() =>
        Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "AsterLink");

    public static UserCleanupResult Run(bool passwords, bool preferences, Func<int> deletePasswords, string preferencesDirectory)
    {
        var removed = 0;
        var prefsGone = false;
        var errors = new List<string>();
        if (passwords)
        {
            try { removed = deletePasswords(); }
            catch (Exception error) when (error is CredentialStoreException) { errors.Add("saved passwords: " + error.Message); }
        }
        if (preferences)
        {
            try { prefsGone = RemovePreferences(preferencesDirectory); }
            catch (Exception error) when (error is IOException or UnauthorizedAccessException) { errors.Add("preferences: " + error.Message); }
        }
        return new UserCleanupResult(removed, prefsGone, errors.Count == 0 ? null : string.Join("; ", errors));
    }

    private static bool RemovePreferences(string directory)
    {
        var info = new DirectoryInfo(directory);
        if (!info.Exists) return false;
        // A link here could point anywhere; the link itself is removed, never what it points at.
        if (info.LinkTarget is not null || (info.Attributes & FileAttributes.ReparsePoint) != 0) { info.Delete(); return true; }
        info.Delete(recursive: true);
        return true;
    }
}
