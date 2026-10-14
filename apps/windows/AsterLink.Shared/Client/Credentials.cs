using System.Globalization;

namespace AsterLink.Shared.Client;

public sealed class CredentialStoreException(string message, int? nativeError = null) : Exception(message)
{
    public int? NativeError { get; } = nativeError;
}

/// <summary>
/// The remembered login. The password is held only by the operating system's credential vault of
/// the current Windows user; there is intentionally no plaintext fallback anywhere.
/// </summary>
public interface ICredentialStore
{
    void Save(string account, string password);
    string? Read(string account);
    void Delete(string account);
}

/// <summary>Items are scoped to the exact API base plus the lower-cased email, so a changed server address can never receive a password saved for another one.</summary>
public static class CredentialAccount
{
    public const string TargetPrefix = "AsterLink/";

    public static string Key(Uri apiBase, string email) =>
        apiBase.AbsoluteUri.TrimEnd('/') + "|" + email.Trim().ToLower(CultureInfo.InvariantCulture);

    /// <summary>Credential Manager target name: product + API base + normalised email.</summary>
    public static string TargetName(string account) => TargetPrefix + account;
}

/// <summary>Non-secret remembered choices (never the password).</summary>
public interface ILoginPreferences
{
    string? RememberedEmail { get; set; }
    string Theme { get; set; }
    bool TrayNoticeShown { get; set; }
}

/// <summary>A fixed per-installation device UUID.</summary>
public interface IDeviceIdentity
{
    string DeviceId();
}

public static class DeviceIds
{
    public static string Generate() => Guid.NewGuid().ToString("D");

    public static bool IsValid(string? value) => value is { Length: 36 } && Guid.TryParseExact(value, "D", out _);
}
